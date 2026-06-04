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

1. Defines a namespaced CRD `AgentSession` in group `relay.secureai.dev/v1alpha1`.
2. Accepts inline policy on the AgentSession spec.
3. Reconciles each `AgentSession` into a `batch/v1` Job named
   `relay-session-<agentsession-name>`, owned by the AgentSession so GC works.
4. Injects task / model / policy information into the Job's agent container as
   `RELAY_*` and `AGENT_*` environment variables.
5. Applies basic runtime constraints:
   - `backoffLimit: 0`
   - `activeDeadlineSeconds` from `spec.runtime.timeoutSeconds`
   - container `securityContext`: `allowPrivilegeEscalation=false`, drop ALL caps
   - resource requests/limits from `spec.runtime.resources`
   - optional emptyDir workspace mount
6. Tracks lifecycle in `status`:
   `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied` / `Cancelled`
7. Supports **session cancellation** via `spec.cancelRequested` (stops the owned Job, sets `Phase=Cancelled`).
8. Emits Kubernetes Events for every interesting transition.
9. Ships sample AgentSessions (success, failure, cancellation).
10. Is structured for extensibility — the controller will be the integration
   point for `AgentPolicy`, `ToolPolicy`, `RuntimeProfile`, `ApprovalPolicy`,
   `ToolGateway`, external orchestrators, and runtime enforcement backends.

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
├── internal/controller/          # AgentSessionReconciler + helpers
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
| `policy`   | Inline governance rules (domains, tools, approvals, quotas)           |
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

| Field                 | Meaning                                                |
|-----------------------|--------------------------------------------------------|
| `phase`               | One of `Pending` / `Validating` / `Starting` / `Running` / `Succeeded` / `Failed` / `Denied` / `TimedOut` / `Cancelled` |
| `observedGeneration`  | Last spec generation the controller acted on           |
| `startTime`           | When the session left `Pending`                        |
| `completionTime`      | When the session reached a terminal phase              |
| `conditions`          | `Validated`, `RuntimeCreated`, `Completed`             |
| `jobName` / `podName` | Backing `Job` and `Pod` names                          |
| `result`              | Terminal outcome / summary / exit code / message       |
| `usage`               | Tokens / tool calls / network requests (populated by future sidecar/audit) |
| `violations`          | List of policy violations observed during the session  |
| `artifacts`           | Artifacts collected from the workspace                 |

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
```

Plus any `spec.runtime.env` entries the user adds.

---

## Reconciler flow

```
                              ┌─────────────────────────────┐
                              │ Fetch AgentSession           │
                              └──────────────┬──────────────┘
                                             ▼
                                        deleted?
                                             │ yes ─► return
                                             ▼
                              ┌─────────────────────────────┐
                              │ Initialize phase = Pending   │
                              └──────────────┬──────────────┘
                                             ▼
                              ┌─────────────────────────────┐
                              │ Validate spec                │
                              │   - task non-empty           │
                              │   - runtime.image required   │
                              │   - orchestrator allowed     │
                              │   - bounds (temp, tokens, …) │
                              └──────────────┬──────────────┘
                                             │ fail ─► Denied + ValidationFailed + return
                                             ▼
                              ┌─────────────────────────────┐
                              │ Ensure backing Job exists    │
                              │   create if missing          │
                              │   set OwnerReference         │
                              │   phase = Starting           │
                              │   event JobCreated           │
                              └──────────────┬──────────────┘
                                             ▼
                              ┌─────────────────────────────┐
                              │ Sync status from Job         │
                              │   active > 0  -> Running     │
                              │   succeeded   -> Succeeded   │
                              │   failed/DL   -> Failed /    │
                              │                  TimedOut    │
                              └──────────────┬──────────────┘
                                             ▼
                              ┌─────────────────────────────┐
                              │ Find owning Pod, set podName │
                              └──────────────┬──────────────┘
                                             ▼
                              ┌─────────────────────────────┐
                              │ Patch status subresource     │
                              │ Requeue if non-terminal      │
                              └─────────────────────────────┘
```

Reconciliation is idempotent and uses the status subresource exclusively for
status updates. Garbage collection of the Job (and its Pods) is delegated to
Kubernetes via owner references.

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
| `make dev-sample`| Apply both sample AgentSessions.                                  |
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

### 7. Try the cancellation sample

```
kubectl apply -f config/samples/relay_v1alpha1_agentsession_cancel.yaml
kubectl get agentsession cancel-at-create-sample -w
```

Expect `Phase=Cancelled` and no `relay-session-cancel-at-create-sample` Job.

To cancel a long-running session, apply the success sample, wait for `Running`, then patch `cancelRequested` as described in [Cancelling a running session](#cancelling-a-running-session).

---

## Current MVP behavior (quick reference)

| Capability | Shipped? | Notes |
|------------|----------|-------|
| Reconcile to Kubernetes Job | Yes | `runtime.orchestrator: kubernetes-job` only |
| Task prompt / ConfigMap prompt | Yes | `spec.task` or `promptConfigMapRef` (same namespace) |
| Inline policy → env vars | Yes | Not enforced at network/tool layer yet |
| Session cancellation | Yes | `spec.cancelRequested` → Job delete + `Phase=Cancelled` |
| Human approval gate | No | Declared only; does not block runs |
| `status.usage` / `violations` / `artifacts` | No | Reserved for future observability backends |

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
- [x] The failing sample transitions to `Failed` and emits `JobFailed`

---

## Long-term roadmap

The MVP is intentionally narrow. The following items are tracked separately and
are explicit hook points in the current code:

### Additional CRDs

- **`AgentPolicy`** — cluster- or namespace-scoped reusable policy, referenced
  by name from `AgentSession.spec.policyRef`. Inline `policy` becomes one of
  several ways to compose policy.
- **`ToolPolicy`** — first-class object describing which tools/MCP servers a
  session can call and what their per-call constraints are.
- **`ApprovalPolicy`** + `ApprovalRequest`/`Approval` — block-and-wait
  semantics for `requireHumanApproval` actions.
- **`RuntimeProfile`** — opt-in stricter runtime hardening
  (`runAsNonRoot`, `readOnlyRootFilesystem`, seccomp, AppArmor, sandbox
  runtime selection).
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
