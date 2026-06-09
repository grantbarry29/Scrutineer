# Relay

**Relay is a Kubernetes-native governance layer for autonomous AI agent execution.**

Relay is **not** a workflow engine. It is not trying to replace
[Kubernetes Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/),
[Tekton](https://tekton.dev/), [Argo Workflows](https://argoproj.github.io/argo-workflows/),
or [Temporal](https://temporal.io/) — those systems already run work.

Relay's job is different: it is the control plane that **governs** autonomous AI
agents while they run inside enterprise environments. It wraps execution with
policy, identity isolation, audit, observability, and (eventually) strong
runtime enforcement, then delegates the actual *running* of the agent to one of
the orchestrators above.

This repository contains the MVP: a Kubebuilder-based Kubernetes operator that
introduces a single CRD — `AgentSession` — and reconciles it onto a Kubernetes
`Job` with policy metadata injected as environment variables.

---

> **Design docs:** architecture and per-phase design live in [`docs/design/`](docs/design/) — start with [`architecture.md`](docs/design/architecture.md). Project tracking is in [`.cursor/relay-project-status.md`](.cursor/relay-project-status.md).

## Long-term product vision

Relay aims to become the runtime control plane for safely running autonomous AI
agents inside enterprise environments. Planned capabilities:

- Runtime governance for AI agents (per-session policy)
- Network egress policy (FQDN + CIDR allow/deny)
- Tool access policy (which tools/MCP servers an agent may invoke)
- File / workspace policy (what the agent may read/write)
- Identity and credential isolation (per-session SA, KMS-scoped secrets)
- Audit logs of every model call, tool call, and network request
- First-class policy violations as cluster events and CRD status
- Observability (metrics, traces, structured run records)
- Human approval gates for sensitive actions
- Integrations with existing orchestrators: Kubernetes Jobs, Tekton, Argo, Temporal
- Optional enforcement via Envoy sidecars, Cilium/eBPF, NetworkPolicy,
  DNS proxying, and eventually stronger sandboxes (gVisor / Kata / Firecracker)

The MVP in this repo is the first vertical slice of that vision: the
`AgentSession` CRD plus a controller that ties it to a real Kubernetes runtime.

---

## What the MVP does

1. Defines namespaced CRDs: `AgentSession`, `AgentPolicy`, `ToolPolicy`, and `RuntimeProfile` (`relay.secureai.dev/v1alpha1`).
2. Reconciles each `AgentSession` into a `batch/v1` Job named `relay-session-<name>`, owned by the session.
3. Merges reusable policies (`spec.policyRefs`) with inline `spec.policy` overrides → `status.effectivePolicy` + `AGENT_POLICY_*` env vars.
4. Applies optional `RuntimeProfile` hardening to the Job pod template via `spec.runtimeProfileRef`.
5. Injects task / model / policy into the agent container as `RELAY_*` and `AGENT_*` environment variables.
6. Tracks lifecycle in `status.phase` and structured `status.conditions` (including aggregate `Ready`).
7. Watches owned Jobs, session Pods, and referenced policy/profile CRDs to re-reconcile promptly.
8. Supports **session cancellation** (`spec.cancelRequested`) and **finalizer-gated deletion** (Job cleanup before session removal).
9. Emits Kubernetes Events for validation, Job lifecycle, policy/profile resolution, and drift.
10. Ships samples and envtest/e2e coverage for the vertical slice above.

See [AgentSession controller reference](#agentsession-controller-reference) for the full behavior catalog.

### What the MVP intentionally does **not** do yet

- No real network egress enforcement. The policy fields are surfaced into the
  container, but the kernel/proxy hasn't been told to stop anything yet.
  Hook points for Envoy sidecars, DNS proxying, NetworkPolicy, and Cilium/eBPF
  are called out in code comments.
- No approval gating. `policy.requireHumanApproval` is surfaced into the
  container's environment but does not block execution. A future
  `ApprovalPolicy` CRD will introduce a real gate.
- No external orchestrators. `runtime.orchestrator` only accepts
  `kubernetes-job` today; `tekton`, `argo`, `temporal`, and `external` are
  reserved for future work.

---

## Repository layout

```
.
├── .devcontainer/                # one-shot Cursor/VS Code dev env (kind + CRDs)
├── api/v1alpha1/                 # CRD types + deepcopy
├── cmd/main.go                   # controller-manager entrypoint
├── internal/controller/
│   ├── agentsession/             # AgentSession reconciler + policy/runtime watches
│   └── job/                      # Kubernetes Job build, drift detection, status helpers
├── internal/enforcement/         # backend-neutral enforcement contract + backends
├── docs/design/                  # architecture & per-phase design docs (start: architecture.md)
├── config/
│   ├── crd/bases/                # CRD YAML
│   ├── default/                  # top-level kustomization
│   ├── manager/                  # controller-manager Deployment
│   ├── rbac/                     # Role / Binding / SA
│   └── samples/                  # sample AgentSessions
├── hack/boilerplate.go.txt
├── Dockerfile
├── Makefile
├── PROJECT
├── go.mod
└── README.md
```

---

## The `AgentSession` CRD

An `AgentSession` is **one governed autonomous AI agent execution**. It is *not*
a generic workflow task. The spec captures four things:

| Field      | Meaning                                                              |
|------------|----------------------------------------------------------------------|
| `task`     | What the agent should do (description / prompt / prompt ConfigMapRef) |
| `model`    | Which provider/model the agent should call                            |
| `runtime`  | Where/how it should execute (orchestrator, image, command, resources) |
| `policy`   | Inline governance overrides (domains, tools, approvals, quotas)     |
| `policyRefs` | Reusable `AgentPolicy` / `ToolPolicy` objects (same namespace)    |
| `workspace`| Per-session workspace volume (ephemeral for MVP)                      |
| `outputs`  | Whether to retain logs/artifacts                                      |
| `cancelRequested` | When `true`, stop the owned Job and reach terminal `Cancelled` |

### Cancelling a running session

Set `spec.cancelRequested: true` on an existing `AgentSession` (or create one with it already set). The controller:

1. Deletes the owned Job `relay-session-<session-name>` (and child Pods via `Background` propagation).
2. Sets `status.phase` to `Cancelled`, `status.result.outcome` to `cancelled`, and a `Completed` condition with reason `SessionCancelled`.
3. Emits a `SessionCancelled` Kubernetes Event.
4. Does **not** create a new Job while cancellation remains requested.

**Cancel a session that is already running:**

```bash
kubectl patch agentsession my-session --type=merge -p '{"spec":{"cancelRequested":true}}'
kubectl get agentsession my-session -w
kubectl describe agentsession my-session   # Event: SessionCancelled
```

**Create an already-cancelled session** (no Job is started):

```bash
kubectl apply -f config/samples/relay_v1alpha1_agentsession_cancel.yaml
```

Cancellation stops the **Kubernetes runtime** (Job/Pod). It does not send a graceful shutdown signal to agent logic inside the container; stronger teardown belongs in future runtime profiles.

### Reference scoping (MVP)

External references resolve in the **same namespace** as the `AgentSession`:

| Ref | Kind | Namespace behavior |
|-----|------|-------------------|
| `spec.task.promptConfigMapRef` | ConfigMap | Same namespace as session |
| `spec.policyRefs[]` | AgentPolicy, ToolPolicy | Same namespace as session |
| `spec.runtimeProfileRef` | RuntimeProfile | Same namespace as session |

Cross-namespace refs are not supported in the MVP. Future CRDs may add an explicit `namespace` field on refs.

### Reusable runtime profile (`RuntimeProfile`)

Platform teams can publish opt-in runtime hardening once; sessions reference a profile via `spec.runtimeProfileRef`.

**Applied to the Job pod template today:**

| Source | Fields merged into Job |
|--------|------------------------|
| Relay baseline | Capability drops (`ALL`), `allowPrivilegeEscalation: false` (busybox-friendly; no forced `runAsNonRoot`) |
| `RuntimeProfile.spec.container` | `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation`, `capabilities` (profile wins when set) |
| `RuntimeProfile.spec.pod` | `runtimeClassName`, `seccompProfile` |

**Status written on reconcile:**

| Field | Meaning |
|-------|---------|
| `status.matchedRuntimeProfile` | Which `RuntimeProfile` was applied (name, UID, resourceVersion) |
| `RuntimeProfileResolved` condition | `ProfileApplied` when a ref resolves; `NoProfileRef` when unset |

**Sidecars (Phase 3):** enabled `spec.sidecars[]` entries (`envoy`, `dns-proxy`, `tool-gateway`) are injected into the Job pod template with placeholder images until first-party data-plane images ship. Sandbox `runtimeClassName` is written to the pod template but not enforced by Relay until sandbox runtimes are integrated.

**Profile change behavior:**

- Updating a referenced `RuntimeProfile` re-reconciles affected sessions (controller watch).
- If the owned Job has **not** started pods yet (`Active==0`), the controller **replaces** the Job so the pod template matches.
- If the Job is **already running**, pod templates are immutable — the running pod may retain the old security context until the Job is replaced manually or the session ends.

**Samples:**

```bash
kubectl apply -f config/samples/relay_v1alpha1_runtimeprofile.yaml
kubectl apply -f config/samples/relay_v1alpha1_agentsession_runtimeprofile_ref.yaml
```

### Reusable policy (`AgentPolicy` + `ToolPolicy`)

Platform teams can publish baseline governance once; sessions reference policies and add inline overrides.

**Merge order** (see [`.cursor/relay-project-status.md`](.cursor/relay-project-status.md) for full detail):

1. `spec.policyRefs` in list order (recommended: `AgentPolicy` entries, then `ToolPolicy`)
2. `spec.policy` inline overrides last (wins on conflict)
3. List fields are unioned; numeric caps take the **minimum** (strictest)
4. Effective **mode** = strictest across matched policies (`enforced` > `dry-run` > `audit-only`)

**Status written on reconcile:**

| Field | Meaning |
|-------|---------|
| `status.effectivePolicy` | Merged rules + mode propagated to the Job |
| `status.matchedPolicies` | Which policy CRDs contributed |
| `status.policyDecisions` | Bounded merge-time audit log (max 64) |

**Propagation today:** `AGENT_POLICY_*` and `AGENT_POLICY_MODE` env vars on the agent container. Modes and rules are **declared**, not enforced at the network/tool layer yet (Phase 3).

**Policy change behavior:**

- Updating a referenced `AgentPolicy` or `ToolPolicy` re-reconciles affected sessions (controller watch).
- `status.effectivePolicy` updates immediately.
- If the owned Job has **not** started pods yet (`Active==0`), the controller **replaces** the Job so env vars match.
- If the Job is **already running**, pod templates are immutable — env inside the pod may be stale; `PolicyPropagated=False` / `PolicyEnvDrift` surfaces the gap.

**Samples:**

```bash
kubectl apply -f config/samples/relay_v1alpha1_agentpolicy.yaml
kubectl apply -f config/samples/relay_v1alpha1_agentsession_policy_ref.yaml

kubectl apply -f config/samples/relay_v1alpha1_toolpolicy.yaml
# prod-agents-baseline must exist for the combined sample:
kubectl apply -f config/samples/relay_v1alpha1_agentsession_toolpolicy_ref.yaml
```

### Inline sample

```yaml
apiVersion: relay.secureai.dev/v1alpha1
kind: AgentSession
metadata:
  name: github-readme-update
  namespace: default
spec:
  task:
    description: "Update the README with installation instructions"
    prompt: "Clone the repo, inspect the README, and propose an updated version."
  model:
    provider: openai
    name: gpt-4.1
    temperature: "0.2"
    maxTokens: 4096
  runtime:
    orchestrator: kubernetes-job
    image: busybox:latest
    command:
    - sh
    - -c
    - "echo Running governed agent session; echo $AGENT_TASK_PROMPT; sleep 5; echo Done"
    timeoutSeconds: 900
    serviceAccountName: default
    resources:
      requests:
        cpu: "500m"
        memory: "512Mi"
      limits:
        cpu: "2"
        memory: "2Gi"
  policy:
    allowedDomains: [github.com, api.github.com]
    deniedDomains:  [dropbox.com, gmail.com]
    allowedTools:   [shell, github]
    deniedTools:    [kubectl-prod]
    requireHumanApproval: [production_deploy, external_write]
    maxNetworkRequests: 100
    maxToolCalls: 25
  workspace:
    ephemeral: true
    size: 5Gi
    mountPath: /workspace
  outputs:
    collectLogs: true
    collectArtifacts: false
    artifactPath: /workspace/artifacts
```

### Status fields

| Field | Populated? | Meaning |
|-------|------------|---------|
| `phase` | Yes | `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied` / `Cancelled` |
| `observedGeneration` | Yes | Last spec generation reconciled |
| `startTime` | Yes | Set when the owned Job is first created |
| `completionTime` | Yes | Set when the session reaches a terminal phase |
| `conditions` | Yes | See [Conditions](#conditions) |
| `jobName` / `podName` | Yes | Owned Job name; newest Job-owned Pod (when known) |
| `matchedPolicies` | Yes | Policy CRDs that contributed to `effectivePolicy` |
| `effectivePolicy` | Yes | Merged rules + mode propagated to the Job |
| `policyDecisions` | Yes | Merge-time audit entries only (max 64); runtime append is Phase 3 |
| `matchedRuntimeProfile` | Yes | Applied `RuntimeProfile` ref (when set) |
| `result` | Yes | Terminal outcome / summary (on success, failure, timeout, cancel) |
| `usage` | **No** (reserved) | Phase 4 — token/tool/network metrics from observability backends |
| `violations` | **Yes** (runtime reports) | Bounded list; `deny` and `dry-run` outcomes via `ApplyRuntimePolicyReport` |
| `artifacts` | **No** (reserved) | Phase 4 — collected workspace artifacts (`spec.outputs`) |

### Environment variables injected into the agent container

Relay always injects these (empty when not set):

```
RELAY_SESSION_NAME
RELAY_SESSION_NAMESPACE
AGENT_TASK_DESCRIPTION
AGENT_TASK_PROMPT
AGENT_MODEL_PROVIDER
AGENT_MODEL_NAME
AGENT_POLICY_ALLOWED_DOMAINS         # comma-separated
AGENT_POLICY_DENIED_DOMAINS          # comma-separated
AGENT_POLICY_ALLOWED_CIDRS           # comma-separated
AGENT_POLICY_DENIED_CIDRS            # comma-separated
AGENT_POLICY_ALLOWED_TOOLS           # comma-separated
AGENT_POLICY_DENIED_TOOLS            # comma-separated
AGENT_POLICY_REQUIRE_HUMAN_APPROVAL  # comma-separated
AGENT_POLICY_MAX_NETWORK_REQUESTS
AGENT_POLICY_MAX_TOOL_CALLS
AGENT_POLICY_MAX_TOOL_CALLS_PER_MINUTE
AGENT_POLICY_MODE
```

`AGENT_POLICY_*` values are **declared and propagated** from merged policy; rate limiting and other enforcement are **Phase 3** (sidecar / tool gateway).

Plus any `spec.runtime.env` entries the user adds.

---

## AgentSession controller reference

The controller lives in `internal/controller/agentsession/` (reconcile loop, policy/runtime watches, validation) and delegates Job construction to `internal/controller/job/` (build, drift detection, status helpers).

### Reconcile triggers

| Source | Mechanism | Effect |
|--------|-----------|--------|
| `AgentSession` | Primary `For()` watch | Any spec/status change on the session |
| Owned `Job` | `Owns(&batchv1.Job{})` | Job status transitions re-queue the parent session |
| Session `Pod` | `Watches(&corev1.Pod{})` | Job-owned Pods labeled `relay.secureai.dev/session=<name>` re-queue the session (faster `podName` / Running updates) |
| `AgentPolicy` | Secondary watch | Sessions with matching `spec.policyRefs` re-reconcile |
| `ToolPolicy` | Secondary watch | Sessions with matching `spec.policyRefs` re-reconcile |
| `RuntimeProfile` | Secondary watch | Sessions with matching `spec.runtimeProfileRef` re-reconcile |
| Timer | `RequeueAfter: 15s` | Backstop poll while Job is in flight (non-terminal sessions) |

### Reconcile flow

```
Fetch AgentSession
    │
    ├─ deleting? ──► stop owned Job ──► remove finalizer ──► return
    │
    ├─ ensure finalizer relay.secureai.dev/finalizer
    │
    ├─ phase = Pending (first observation); observedGeneration = generation
    │
    ├─ validateSpec ──fail──► Denied, Validated=False, Ready=False, events ──► return
    │
    ├─ resolveTask (inline prompt or ConfigMap ref) ──fail──► Denied ──► return
    │
    ├─ resolvePolicy (policyRefs merge + inline overrides) ──fail──► Denied ──► return
    │       └── status.effectivePolicy, matchedPolicies, policyDecisions, PolicyResolved
    │
    ├─ resolveRuntimeProfile (optional ref) ──fail──► Denied ──► return
    │       └── matchedRuntimeProfile, RuntimeProfileResolved
    │
    ├─ requireHumanApproval declared? ──► ApprovalNotEnforced warning event (no gate)
    │
    ├─ cancelRequested? ──► delete Job ──► Cancelled, Completed, Ready=False ──► return
    │
    ├─ already terminal? ──► patch status ──► return (no new Job)
    │
    ├─ ensureJob (create or sync owned Job)
    │       ├── policy env drift / runtime profile drift on pending Job → replace Job
    │       └── active Job with stale policy env → PolicyEnvDrift condition + warning event
    │
    ├─ syncStatusFromJob (Running / Succeeded / Failed / TimedOut)
    ├─ findPodName (newest Pod owned by current Job UID)
    ├─ set Ready condition from phase
    └─ patch status; requeue after 15s if non-terminal
```

Reconciliation is **idempotent**. Status updates use the status subresource with condition merging so concurrent writes do not drop condition types. The owned Job is named deterministically `relay-session-<session-name>`; a foreign Job at that name causes `Phase=Denied` (`JobConflict`).

### Validation (`validateSpec`)

Controller-side checks (in addition to CRD OpenAPI validation):

| Check | Denial reason |
|-------|---------------|
| Task: description, prompt, or `promptConfigMapRef` required | `InvalidSpec` |
| `runtime.image` and `model.provider` / `model.name` non-empty | `InvalidSpec` |
| `runtime.orchestrator` must be `kubernetes-job` | `InvalidSpec` |
| Temperature in `[0, 2]`; `maxTokens >= 1`; `timeoutSeconds >= 1` | `InvalidSpec` |
| Policy numeric caps `>= 0` | `InvalidSpec` |
| `policyRefs[].kind` in `AgentPolicy`, `ToolPolicy` | `InvalidSpec` |
| `runtimeProfileRef` shape | `InvalidSpec` |
| Workspace `size` parseable as quantity | `InvalidSpec` |
| Missing ConfigMap / key (task resolution) | `InvalidTask` |
| Missing or invalid `policyRefs` target (policy resolution) | `InvalidPolicy` |
| Missing `RuntimeProfile` (profile resolution) | `InvalidRuntimeProfile` |
| Foreign Job occupies deterministic name | `JobConflict` |

### Task resolution

- Inline `spec.task.description` and `spec.task.prompt` pass through to Job env vars.
- `spec.task.promptConfigMapRef` loads the prompt from a ConfigMap key in the **same namespace** as the session.

### Policy resolution and propagation

Merge order:

1. `spec.policyRefs` in list order (`AgentPolicy`, then `ToolPolicy` recommended)
2. `spec.policy` inline overrides last
3. List fields union; numeric caps take the **minimum** (strictest)
4. Effective mode = strictest (`enforced` > `dry-run` > `audit-only`)

Written to status each reconcile: `effectivePolicy`, `matchedPolicies`, `policyDecisions` (merge-time, max 64). Propagated to the Job as `AGENT_POLICY_*` env vars. **Declared only** — network/tool enforcement is Phase 3.

When a referenced policy changes:

- `status.effectivePolicy` updates immediately
- **Pending** Job (`Active==0`): controller **replaces** the Job (`PolicyEnvSynced` event)
- **Active** Job: pod template is immutable; `PolicyPropagated=False` / `PolicyEnvDrift` surfaces stale env

### Runtime profile resolution

When `spec.runtimeProfileRef` is set, the controller loads the `RuntimeProfile` (same namespace) and merges container/pod security fields plus enabled `spec.sidecars[]` into the Job template. Profile drift (including sidecar changes) follows the same pending-Job-replace rules as policy env drift.

### Job lifecycle (`internal/controller/job`)

| Setting | Value |
|---------|-------|
| Name | `relay-session-<session-name>` |
| Labels | `relay.secureai.dev/session`, `app.kubernetes.io/name=relay`, `app.kubernetes.io/component=agent-session` |
| `backoffLimit` | `0` |
| `ttlSecondsAfterFinished` | `300` |
| Container | `agent`; baseline drops `ALL` capabilities, `allowPrivilegeEscalation=false` |
| Workspace | Optional `emptyDir` when `spec.workspace.ephemeral=true` |

### Phase mapping from Job status

| Job observation | Session `phase` | `Completed` condition |
|-----------------|---------------|----------------------|
| `status.succeeded > 0` | `Succeeded` | `True` / `JobSucceeded` |
| `status.active > 0` | `Running` | (unchanged) |
| `DeadlineExceeded` condition | `TimedOut` | `False` / `JobTimedOut` |
| `status.failed > backoffLimit` | `Failed` | `False` / `JobFailed` |
| Job created, not yet active | `Starting` | (unchanged) |

### `status.podName` selection

1. List Pods in the session namespace with label `relay.secureai.dev/session=<session.Name>`
2. Keep only Pods whose ownerReference points at the **current** Job UID
3. Pick the Pod with the latest `creationTimestamp` (name breaks ties lexicographically)

### Cancellation and deletion

**Cancellation** (`spec.cancelRequested: true`): deletes the owned Job, sets `phase=Cancelled`, `result.outcome=cancelled`, `Completed=True` / `SessionCancelled`, `Ready=False`. Idempotent when the Job is already gone.

**Deletion**: finalizer `relay.secureai.dev/finalizer` blocks AgentSession removal until the owned Job is deleted. `blockOwnerDeletion` is cleared on the Job so deletion cannot deadlock.

### Conditions

| Type | When `True` | When `False` | Common reasons |
|------|-------------|--------------|----------------|
| `Validated` | Spec accepted | Validation / resolution failed | `SpecValid`, `InvalidSpec`, `InvalidTask`, `InvalidPolicy`, `InvalidRuntimeProfile`, `JobConflict` |
| `PolicyResolved` | Policies merged | — | `PoliciesMerged` |
| `PolicyPropagated` | Job env matches effective policy | Active Job has stale env | `EnvCurrent`, `PolicyEnvDrift` |
| `RuntimeProfileResolved` | Profile applied or not referenced | — | `ProfileApplied`, `NoProfileRef` |
| `RuntimeCreated` | Owned Job exists | — | `JobCreated` |
| `Completed` | Terminal success or cancel | Terminal failure / timeout | `JobSucceeded`, `JobFailed`, `JobTimedOut`, `SessionCancelled` |
| `Ready` | Session running or succeeded | Not yet running, denied, failed, timed out, or cancelled | `JobRunning`, `JobSucceeded`, `NotReady`, `SessionDenied`, `JobFailed`, `JobTimedOut`, `SessionCancelled` |

`Ready` is an **aggregate** summary derived from `status.phase` — not a Pod readiness probe. It answers: “Is this session actively running or successfully finished?”

### Kubernetes Events

Inspect with:

```bash
kubectl describe agentsession <name> -n <namespace>
kubectl get events -n <namespace> --field-selector involvedObject.kind=AgentSession
```

| Reason | Type | When emitted |
|--------|------|--------------|
| `ValidationFailed` | Warning | Spec validation or task/policy/profile resolution failed |
| `SessionDenied` | Warning | Session reached `Phase=Denied` |
| `JobCreated` | Normal | Owned Job created |
| `JobRunning` | Normal | Job has active pods (`Phase=Running`) |
| `JobSucceeded` | Normal | Job completed successfully |
| `JobFailed` | Warning | Job failed or timed out |
| `SessionCancelled` | Normal | `spec.cancelRequested` processed |
| `ApprovalNotEnforced` | Warning | `requireHumanApproval` declared but MVP does not gate execution |
| `PolicyResolved` | Normal | Referenced policies merged |
| `RuntimeProfileResolved` | Normal | RuntimeProfile applied to Job template |
| `PolicyEnvDrift` | Warning | Effective policy changed but active Job env is stale |
| `PolicyEnvSynced` | Normal | Pending Job replaced to sync policy env |

### Inspecting a session

```bash
# High-level phase and conditions
kubectl get agentsession <name> -o jsonpath='{.status.phase}{"\n"}{range .status.conditions[*]}{.type}={.status} ({.reason}){"\n"}{end}'

# Effective policy and Job linkage
kubectl get agentsession <name> -o jsonpath='{.status.effectivePolicy.mode}{"\n"}{.status.jobName}{"\n"}{.status.podName}{"\n"}'

# Owned Job and labeled Pods
kubectl get job relay-session-<name> -o wide
kubectl get pods -l relay.secureai.dev/session=<name>
```

---

## Quick start with the dev container (recommended)

The repo ships with a `.devcontainer/` that gives you a fully wired Relay dev
environment with **zero host setup beyond Docker + Cursor/VS Code**.

What you get when you open the folder in a Dev Container:

- Go 1.23 toolchain
- Docker-in-Docker
- `kubectl`, `kind`, `kustomize` pre-installed
- A local `kind` cluster named **`relay-dev`** created automatically
- The Relay CRD installed into that cluster on first start

### Open it

1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/) on
   your host (or any Docker-compatible runtime).
2. Open this folder in Cursor / VS Code.
3. When prompted, choose **"Reopen in Container"**, or run the
   `Dev Containers: Reopen in Container` command.

On first build the `postCreateCommand` (`.devcontainer/bootstrap.sh`) will:

1. Wait for the in-container Docker daemon.
2. `go mod download`.
3. Create the `relay-dev` kind cluster (idempotent — re-runs are safe).
4. `kubectl apply` the Relay CRD.
5. Print the next-step commands.

### Inside the container

```bash
# (1) Run the controller against the kind cluster from your terminal:
make run

# (2) In a second terminal, apply a sample AgentSession:
kubectl apply -f config/samples/relay_v1alpha1_agentsession.yaml
kubectl get agentsessions -w
kubectl describe agentsession github-readme-update
kubectl logs job/relay-session-github-readme-update

# (3) Or build, kind-load, and deploy the controller as an in-cluster Pod:
make dev-deploy

# (4) Both samples at once:
make dev-sample

# (5) Tear it all down:
make dev-down
```

### Dev-cluster Makefile targets

| Target          | What it does                                                       |
|-----------------|--------------------------------------------------------------------|
| `make kind-up`  | Create the `relay-dev` kind cluster (no-op if it exists).          |
| `make kind-down`| Delete the `relay-dev` kind cluster.                               |
| `make kind-load`| `docker-build` the controller image + `kind load docker-image`.    |
| `make dev-up`   | `kind-up` + install CRDs. Use with `make run` for the dev loop.    |
| `make dev-deploy`| Build + load + deploy the controller into the kind cluster.       |
| `make dev-sample`| Apply success + failing sample AgentSessions.                     |
| `make verify-samples` | Server-side dry-run all `config/samples/relay_*.yaml` (needs CRDs). |
| `make dev-down` | Alias for `kind-down`.                                             |

You can also run these targets **outside** the dev container as long as Docker,
`kind`, and `kubectl` are on your `PATH`.

---

## Running the MVP without the dev container

### Prerequisites

- Go 1.23+
- A Kubernetes cluster you can reach via `kubectl` (kind/k3d/minikube/EKS/GKE all fine)
- `make`
- Optional: `docker`/`podman` if you want to build a controller image

The Makefile auto-installs `controller-gen`, `kustomize`, and `setup-envtest`
into `./bin/` on first use.

### 1. Generate code and CRDs

```
make generate    # regenerate zz_generated.deepcopy.go
make manifests   # regenerate config/crd/bases and RBAC
```

A pre-generated CRD is already checked in at
`config/crd/bases/relay.secureai.dev_agentsessions.yaml`, so this step is only
needed after editing `api/v1alpha1/*.go`.

### 2. Install the CRD

```
make install
```

This applies `config/crd` to the cluster pointed at by your current kubeconfig.

### 3. Run the controller against your cluster

From a separate terminal:

```
make run
```

This runs the controller-manager locally and connects to your cluster as your
current kubeconfig user.

### 4. Apply a sample AgentSession

```
kubectl apply -f config/samples/relay_v1alpha1_agentsession.yaml
```

### 5. Observe it

```
kubectl get agentsessions
kubectl describe agentsession github-readme-update
kubectl get jobs
kubectl logs job/relay-session-github-readme-update
```

You should see:

- `kubectl get agentsessions` showing `Phase` transition
  `Starting` → `Running` → `Succeeded`
- `kubectl describe` showing Events: `JobCreated`, `JobRunning`, `JobSucceeded`
- `kubectl logs` showing the injected `RELAY_*` / `AGENT_*` env values

### 6. Try the failing sample

```
kubectl apply -f config/samples/relay_v1alpha1_agentsession_failing.yaml
kubectl get agentsessions
```

It should transition to `Failed` with a `JobFailed` event and
`Completed=False` condition.

### 7. Try the prompt ConfigMap sample

```
kubectl apply -f config/samples/relay_v1alpha1_agentsession_prompt_cm.yaml
kubectl get agentsession github-readme-from-cm -w
```

Applies a ConfigMap plus an AgentSession that loads `spec.task.promptConfigMapRef`
(same namespace). Expect `Succeeded` when the controller is running.

### 8. Try the cancellation sample

```
kubectl apply -f config/samples/relay_v1alpha1_agentsession_cancel.yaml
kubectl get agentsession cancel-at-create-sample -w
```

Expect `Phase=Cancelled` and no `relay-session-cancel-at-create-sample` Job.

To cancel a long-running session, apply the success sample, wait for `Running`, then patch `cancelRequested` as described in [Cancelling a running session](#cancelling-a-running-session).

### 9. Try the RuntimeProfile sample

```
kubectl apply -f config/samples/relay_v1alpha1_runtimeprofile.yaml
kubectl apply -f config/samples/relay_v1alpha1_agentsession_runtimeprofile_ref.yaml
kubectl get agentsession session-with-runtimeprofile -w
```

Expect a Job whose pod template includes settings from `hardened-agent` (see `kubectl get job relay-session-session-with-runtimeprofile -o yaml`). The sample uses stricter container hardening; use an image compatible with `runAsNonRoot` / `readOnlyRootFilesystem` in production.

### Validate samples against the installed CRD

After `make install` (or `make dev-up`), check that hand-maintained samples still match the API:

```
make verify-samples
```

This runs `kubectl apply --dry-run=server` on each `config/samples/relay_*.yaml` (success, failing, cancel-at-create, prompt ConfigMap, AgentPolicy/ToolPolicy/RuntimeProfile refs).

---

## Current MVP behavior (quick reference)

| Capability | Shipped? | Notes |
|------------|----------|-------|
| Reconcile to Kubernetes Job | Yes | `runtime.orchestrator: kubernetes-job` only |
| Task prompt / ConfigMap prompt | Yes | `spec.task` or `promptConfigMapRef` (same namespace) |
| `AgentPolicy` + `spec.policyRefs` | Yes | Same-namespace; merge → `status.effectivePolicy` |
| `ToolPolicy` in `policyRefs` | Yes | Tool/MCP rules + `maxCallsPerMinute` merged and propagated |
| Inline `spec.policy` overrides | Yes | Merged last; propagated as `AGENT_POLICY_*` env |
| Policy modes (`audit-only` / `dry-run` / `enforced`) | Yes | Declared in status + `AGENT_POLICY_MODE`; not enforced yet |
| `status.policyDecisions` | Yes | Merge-time audit entries only (max 64) |
| Policy change → Job env sync | Partial | Replaces **pending** Jobs; `PolicyEnvDrift` if Job already active |
| `RuntimeProfile` + `runtimeProfileRef` | Yes | Same-namespace; merges into Job pod template; watch + pending Job replace |
| `RuntimeProfile.spec.sidecars` | Injected when enabled | `dns-proxy`, `tool-gateway`, `envoy`; placeholder images until data-plane ships |
| Pod watch for reconcile | Yes | Faster `podName` / Running updates |
| `Ready` condition | Yes | Aggregate readiness from `status.phase` |
| Finalizer + Job cleanup on delete | Yes | `relay.secureai.dev/finalizer` |
| Session cancellation | Yes | `spec.cancelRequested` → Job delete + `Phase=Cancelled` |
| Human approval gate | No | Declared only; does not block runs |
| `status.usage` / `artifacts` | No | Reserved — see [Status fields](#status-fields) |
| `status.violations` | Yes (runtime) | Populated by enforcement reporters — see [Status fields](#status-fields) |

Project tracking: [`.cursor/relay-project-status.md`](.cursor/relay-project-status.md).

### Deploying the controller into the cluster

```
make docker-build IMG=ghcr.io/secureai/relay:dev
make docker-push  IMG=ghcr.io/secureai/relay:dev
make deploy       IMG=ghcr.io/secureai/relay:dev
```

To remove:

```
make undeploy
make uninstall
```

---

## Acceptance criteria (verified by the samples)

After running the controller and applying the success sample:

- [x] `AgentSession` CRD is installed in the cluster
- [x] The sample AgentSession is accepted
- [x] The controller creates a Job named `relay-session-github-readme-update`
- [x] The Job runs and exits 0
- [x] `status.phase` transitions `Pending` → `Starting` → `Running` → `Succeeded`
- [x] `status.jobName` is populated
- [x] `status.podName` is populated once a pod exists
- [x] Kubernetes Events `JobCreated`, `JobRunning`, `JobSucceeded` are visible in `kubectl describe`
- [x] `status.conditions` include `Validated`, `RuntimeCreated`, `Completed`, and `Ready`
- [x] The failing sample transitions to `Failed` and emits `JobFailed`

---

## Long-term roadmap

The MVP is intentionally narrow. The following items are tracked separately and
are explicit hook points in the current code:

### Additional CRDs

- **`AgentPolicy`** — **shipped** (namespace-scoped). Referenced from `spec.policyRefs`.
- **`ToolPolicy`** — **shipped** (namespace-scoped). Tool/MCP allowlists and caps via `spec.policyRefs`.
- **`ApprovalPolicy`** + `ApprovalRequest`/`Approval` — block-and-wait
  semantics for `requireHumanApproval` actions.
- **`RuntimeProfile`** — **shipped** (namespace-scoped). Referenced from `spec.runtimeProfileRef`; merges container/pod security and enabled sidecars into the Job. Sandbox runtime enforcement remains future work.
- **`ToolGateway`** — Relay-managed proxy endpoint that brokers MCP/tool API
  calls, attributes them to the session, and enforces `ToolPolicy`.

### Runtime enforcement

- **Envoy sidecar** for outbound HTTP/S egress policy (FQDN, header,
  method-aware rules).
- **DNS proxy** to enforce a session-scoped allowlist at the resolver layer.
- **`NetworkPolicy`** and **`CiliumNetworkPolicy`** generated per-session for
  L3/L4 isolation.
- **eBPF** enforcement for process / file / syscall events.
- **Sandbox runtimes** (gVisor, Kata, Firecracker) selected via
  `RuntimeProfile`.

### Orchestrator integrations

`runtime.orchestrator` will grow beyond `kubernetes-job`:

- `tekton` — reconcile into a `TaskRun` / `PipelineRun`
- `argo` — reconcile into a `Workflow`
- `temporal` — start a Workflow Execution
- `external` — webhook out to a user-supplied dispatcher

The current reconciler is structured so that adding a dispatcher is a matter of
implementing a new backend behind a small interface; the policy injection,
status tracking, and event emission paths stay the same.

### Audit, observability, dashboard

- Structured audit log for every model call, tool call, and network request
  (correlated by `RELAY_SESSION_NAME`).
- Prometheus metrics for session phases, durations, and violation counts.
- A small dashboard for browsing recent AgentSessions, their policy, and their
  audit trail.

---

## License

Apache 2.0. See [LICENSE](./LICENSE).
