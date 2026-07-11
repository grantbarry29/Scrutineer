# Scrutineer

**Scrutineer is a Kubernetes-native governance layer for autonomous AI agent execution.**

Scrutineer is **not** a workflow engine. It is not trying to replace
[Kubernetes Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/),
[Tekton](https://tekton.dev/), [Argo Workflows](https://argoproj.github.io/argo-workflows/),
or [Temporal](https://temporal.io/) — those systems already run work.

Scrutineer's job is different: it is the control plane that **governs** autonomous AI
agents while they run inside enterprise environments. It wraps execution with
policy, untamperable egress enforcement, audit, observability, and human approval,
then delegates the actual *running* of the agent to one of the orchestrators above.

This repository is a Kubebuilder-based Kubernetes operator built around the
`AgentSession` CRD. It is **bring-your-own-agent**: your image holds the reasoning
loop, model calls, and tool use; Scrutineer schedules that workload (Job or bare Pod),
resolves and propagates reusable policy, and **enforces network egress from outside
the agent's trust domain** — a per-session, out-of-pod Envoy chokepoint plus a
default-deny routing lock the agent cannot alter. Runtime evidence is recorded back
into status stamped `observed`, observability and audit signals are exported, and
sensitive actions gate behind human approval. Enforcement ships **untamperable or
not at all** ([decision record](docs/design/untamperable-enforcement.md)): where a guarantee
depends on cluster behavior, Scrutineer proves it empirically and refuses rather
than degrading silently.

---

> **Design docs:** architecture and per-phase design live in [`docs/design/`](docs/design/) — start with [`architecture.md`](docs/design/architecture.md). **Task tracking and the roadmap are in [GitHub Issues / Projects](https://github.com/grantbarry29/scrutineer/issues)**; durable technical context lives in `docs/design/` and component `README.md`s.

## Quickstart

One command from a fresh clone to a running Scrutineer on a local
[kind](https://kind.sigs.k8s.io/) cluster (needs Docker, kind, kubectl; builds the
first-party images from your checkout so the controller always matches the
manifests it is deployed with — tagged `dev-<git describe>`, so what a cluster runs
is never confusable with a published `vX.Y.Z` release image, which only the release
workflow produces):

```sh
make quickstart
```

The first run takes about **5 minutes** (it builds the first-party images from your
checkout); repeat runs are much faster. It creates a dedicated `scrutineer-quickstart` cluster, loads the controller and
egress-proxy images into it, installs the CRDs, deploys the controller, and prints the
**routing-lock verification verdict** — Scrutineer empirically proves the cluster's CNI
enforces NetworkPolicy before it will run enforced sessions (*verified-or-refused*; see
[`docs/design/untamperable-enforcement.md`](docs/design/untamperable-enforcement.md)). If the verdict
comes back `Refused` on your kind version, retry with
`make quickstart-down && make quickstart QUICKSTART_CNI=calico`.

Then run the guided demo of the untamperable egress path (the cluster needs
**internet egress** — the demo probes fetch `example.com`) — a denied request rejected
live at the per-session chokepoint, a bypass attempt killed by the routing lock, and
`observed` evidence the agent could not have forged, contrasted against `audit-only`
mode ([walkthrough](docs/demo.md)):

```sh
make demo
```

Tear down with `make demo-down` / `make quickstart-down`.

## Long-term product vision

Scrutineer aims to become the runtime control plane for safely running autonomous AI
agents inside enterprise environments.

**Shipped today:**

- **Untamperable network egress governance** — per-session out-of-pod Envoy chokepoint
  (FQDN allow/deny, enforce or dry-run) + default-deny routing lock; evidence stamped
  `observed` from the proxy pod's own identity (never the agent's word)
- **Verified-or-refused** — a differential canary probe proves the CNI actually enforces
  NetworkPolicy; enforced sessions on an unverified cluster refuse to start, loudly
- Reusable policy CRDs (`AgentPolicy`, `RuntimeProfile`) merged into per-session
  effective policy, with first-class violations as cluster events and CRD status
- Audit + observability (Prometheus metrics — control plane and egress data plane,
  OpenTelemetry traces, OTLP audit sink, session events/timeline, log/artifact capture)
- Human approval gates for sensitive actions
- Kubernetes Jobs **or** bare Pods behind a backend-neutral `runtimeBackend` interface

**Future** (tracked as epics):

- Tool governance via an out-of-pod **tools-pod chokepoint** (#76) and model-call
  governance via an **LLM gateway** (#77) — the tool/file policy surface was removed
  with the cooperative in-pod tier ([decision record](docs/design/untamperable-enforcement.md))
  and returns only with real enforcement backends
- Node-level transparent interception, no CNI dependency (#64)
- Identity and credential isolation / mediation (#25), sandboxes (gVisor / Kata) (#29)
- Additional orchestrators behind the same interface: Tekton, Argo, Temporal
- An operational governance/observability UI

Live task state and the product roadmap are in
[GitHub Issues / Projects](https://github.com/grantbarry29/scrutineer/issues); durable technical context
lives in [`docs/design/`](docs/design/) and component `README.md`s.

---

## What Scrutineer does today

**Control plane (the manager — [`cmd/main.go`](cmd/main.go)):**

1. Defines namespaced CRDs (`scrutineer.sh/v1alpha1`): `AgentSession`, `AgentPolicy`, `RuntimeProfile`, `ApprovalPolicy`, `ApprovalRequest`.
2. Reconciles each `AgentSession` into a runtime object named `scrutineer-session-<name>`, owned by the session, behind a backend-neutral `runtimeBackend` interface: a `batch/v1` Job (`kubernetes-job`, default) or a bare `v1` Pod (`kubernetes-pod`). `status.runtimeRef` records which object was created.
3. Merges reusable policies (`spec.policyRefs`) with inline `spec.policy` overrides → `status.effectivePolicy`, `status.policyDecisions`, and `AGENT_POLICY_*` env vars.
4. Applies optional `RuntimeProfile` hardening and enables the data-plane egress backend via `spec.runtimeProfileRef` / `spec.enforcement[]` (the `envoy` type provisions a per-session **out-of-pod** egress proxy and locks the agent pod's egress to it).
5. Tracks lifecycle in `status.phase` and structured `status.conditions` (including aggregate `Ready`), emits Kubernetes Events, supports cancellation and finalizer-gated deletion.
6. Gates sensitive actions behind **human approval** (`ApprovalPolicy` → `AwaitingApproval` → grant/deny) with an opt-in mutating webhook that stamps the authenticated approver identity.
7. **Verifies before it claims** (the lock gate): a differential canary probe proves the CNI actually enforces egress `NetworkPolicy`; enforced sessions on an unverified cluster **refuse to start** with condition `EgressLockVerified=False` instead of running unprotected.

**Data plane (out-of-pod, agent-untamperable):**

8. A per-session **Envoy egress proxy** (own pod/identity/netns) enforces FQDN allow/deny as RBAC config; a default-deny routing-lock `NetworkPolicy` makes it the agent's only egress path.
9. The co-located [egress-reporter](cmd/egress-reporter/) tails Envoy's access log and reports runtime evidence to the in-manager [reporter](internal/reporter/), which merges `status.policyDecisions`, `status.violations`, `status.usage`, and `status.events` back onto the session — authenticated per-pod, stamped **`observed`** from the caller's identity (never the payload).

**Observability & audit:** Prometheus metrics, OpenTelemetry reconcile/reporter traces, and an OTLP structured audit sink; optional log/artifact collection into `status.artifacts`.

See [AgentSession controller reference](#agentsession-controller-reference) for the full behavior catalog.

### Current limitations

- **Tool and file governance is not enforced yet.** Scrutineer ships only enforcement
  the agent cannot tamper with (untamperable or absent;
  [`docs/design/untamperable-enforcement.md`](docs/design/untamperable-enforcement.md)); the
  cooperative in-pod tier was removed rather than presented as governance. Today that
  means **egress is enforced and `observed`**
  (see [Egress enforcement](#egress-enforcement-guarantees--assumptions)); there is
  **no tool/file policy surface** — those fields were removed with the cooperative tier
  and return with their out-of-pod chokepoints (tools pod, arena workspace — deferred
  designs in [`docs/design/`](docs/design/), decision #75).
- **Two in-cluster orchestrators.** `runtime.orchestrator` accepts `kubernetes-job`
  (default) and `kubernetes-pod`; external backends (`tekton` / `argo` / `temporal` /
  `external`) remain reserved.
- **No operational UI yet** (Phase 7), and **no per-session identity / multi-tenancy**
  hardening yet (Phase 8).

### Egress enforcement: guarantees & assumptions

When a `RuntimeProfile` enables the `envoy` enforcement backend, agent egress is
governed **adversarial-grade** — the agent cannot bypass it or forge its record —
**under the assumptions below**. This is deliberately narrower than "tamper-proof";
we never claim more than these boundaries support. (Design:
[`docs/design/evidence-integrity.md`](docs/design/evidence-integrity.md).)

**What holds:**

- **Egress goes only through the proxy.** A per-session Envoy runs in its **own pod**
  (own identity/netns), and a default-deny egress `NetworkPolicy` on the agent pod
  permits **only** that Envoy. The agent reaches it by ClusterIP, so **direct DNS is
  denied** — no DNS-exfil path.
- **The agent can't tamper with the enforcement point.** It lives outside the agent's
  pod, so a compromised agent can't reconfigure, restart, or read the proxy's identity.
- **FQDN allow/deny is enforced at the proxy.** The policy's `allowedDomains`/
  `deniedDomains` render into Envoy RBAC on the `:authority`, so denied (or not-allowed)
  hosts are blocked at the chokepoint for both plain HTTP and HTTPS `CONNECT` — not just
  recorded. Matching is exact or `*.`-wildcard (subdomains, apex excluded), shared with
  the egress-reporter's evidence classification so enforcement and evidence agree.
- **Evidence is independent.** Egress is recorded as `observed` — stamped by the
  reporter from the **proxy pod's authenticated identity**, never from the payload, so
  the agent cannot forge `observed` records or suppress them by lying. In enforced mode a
  blocked host is an `observed` deny; in audit mode it flows but is recorded as an
  `observed` dry-run.
- **Defense in depth.** Even the proxy pod is denied cloud-metadata (and operator-set
  cluster) CIDRs, so a compromised Envoy still can't reach `169.254.169.254`.

**What it assumes (and does not claim beyond):**

- **The CNI enforces egress `NetworkPolicy`.** Required. Verified against a CNI matrix
  (kindnet + Calico) by the networking e2e suite; operators on other CNIs must confirm
  egress-policy support.
- **The agent pod is unprivileged** (`drop ALL`, `seccomp: RuntimeDefault`, no
  `CAP_NET_ADMIN`) and **the node/CNI is not compromised** — node-level escape is a
  higher threat tier, explicitly out of scope.
- **Coverage is HTTP/S + client-`CONNECT`-tunneled TCP.** Proxy-unaware or raw non-HTTP
  tools **fail closed** (no leak; they need proxy config or the future transparent
  node interceptor, [#64](https://github.com/grantbarry29/scrutineer/issues/64)). FQDN
  matching is **host-level** (SNI/`:authority`) — HTTPS is CONNECT-tunnelled, so there is
  no path/method matching.
- **`observed` means "independent of the agent," not "tamper-proof."** It is only as
  strong as the assumptions above.

Agents that legitimately need the Kubernetes API can opt into
`spec.pod.automountServiceAccountToken: true` (see
[RuntimeProfile](#reusable-runtime-profile-runtimeprofile)); that traffic still transits
the proxy.

---

## Repository layout

```
.
├── .devcontainer/                # one-shot Cursor/VS Code dev env (kind + CRDs)
├── api/v1alpha1/                 # CRD types + deepcopy (6 kinds)
├── cmd/
│   ├── main.go                   # manager: controller + reporter + lock-probe + optional webhook
│   └── egress-reporter/          # observed-evidence producer beside Envoy (README)
├── internal/
│   ├── controller/agentsession/  # AgentSession reconciler + runtimeBackend (README)
│   ├── controller/job/           # Job build, proxy-env wiring, drift detection
│   ├── enforcement/              # backend-neutral contract + networkpolicy/envoy/lockverify
│   ├── reporter/                 # runtime-evidence + approval HTTP service (README)
│   ├── approval/                 # approval gate helpers + notifier
│   ├── policy/                   # policy merge + decision records
│   ├── webhook/v1alpha1/         # approver-identity mutating webhook
│   ├── audit/ · metrics/ · observability/ · tracing/  # audit sink, Prometheus, OTel
├── docs/design/                  # architecture & per-phase design docs (start: architecture.md)
├── docs/templates/               # component README template
├── config/
│   ├── crd/bases/                # CRD YAML (generated)
│   ├── default/                  # top-level kustomization
│   ├── manager/ · rbac/          # Deployment, Role/Binding/SA
│   ├── webhook/ · webhooks/ · certmanager/  # opt-in approver-identity webhook + TLS
│   ├── reporter-standalone/      # opt-in overlay: reporter as its own Deployment + SA
│   └── samples/                  # sample manifests (make verify-samples)
├── Dockerfile · Dockerfile.egress-reporter
├── Makefile · PROJECT · go.mod · README.md
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
| `policyRefs` | Reusable `AgentPolicy` objects (same namespace)                   |
| `workspace`| Per-session workspace volume (ephemeral for MVP)                      |
| `outputs`  | Whether to retain logs/artifacts                                      |
| `cancelRequested` | When `true`, stop the owned Job and reach terminal `Cancelled` |

### Cancelling a running session

Set `spec.cancelRequested: true` on an existing `AgentSession` (or create one with it already set). The controller:

1. Deletes the owned Job `scrutineer-session-<session-name>` (and child Pods via `Background` propagation).
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
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_cancel.yaml
```

Cancellation stops the **Kubernetes runtime** (Job/Pod). It does not send a graceful shutdown signal to agent logic inside the container; stronger teardown belongs in future runtime profiles.

### Reference scoping (MVP)

External references resolve in the **same namespace** as the `AgentSession`:

| Ref | Kind | Namespace behavior |
|-----|------|-------------------|
| `spec.task.promptConfigMapRef` | ConfigMap | Same namespace as session |
| `spec.policyRefs[]` | AgentPolicy | Same namespace as session |
| `spec.runtimeProfileRef` | RuntimeProfile | Same namespace as session |

Cross-namespace refs are not supported in the MVP. Future CRDs may add an explicit `namespace` field on refs.

### Reusable runtime profile (`RuntimeProfile`)

Platform teams can publish opt-in runtime hardening once; sessions reference a profile via `spec.runtimeProfileRef`.

**Applied to the Job pod template today:**

| Source | Fields merged into Job |
|--------|------------------------|
| Scrutineer baseline | Capability drops (`ALL`), `allowPrivilegeEscalation: false` (busybox-friendly; no forced `runAsNonRoot`) |
| `RuntimeProfile.spec.container` | `runAsNonRoot`, `runAsUser`, `runAsGroup`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation`, `capabilities` (profile wins when set; pair `runAsNonRoot` with `runAsUser` for root-default images like busybox) |
| `RuntimeProfile.spec.pod` | `runtimeClassName`, `seccompProfile`, `automountServiceAccountToken` |

By default the agent pod's ServiceAccount token is **not** mounted, so a compromised agent gets no apiserver credential. Set `spec.pod.automountServiceAccountToken: true` **only** for agents that legitimately call the Kubernetes API, and pair it with a dedicated, minimally-scoped ServiceAccount via `spec.runtime.serviceAccountName` — the token grants whatever that SA's RBAC allows. Under the egress lock the agent's apiserver traffic still transits the session's Envoy proxy (and is recorded as `observed` evidence).

**Status written on reconcile:**

| Field | Meaning |
|-------|---------|
| `status.matchedRuntimeProfile` | Which `RuntimeProfile` was applied (name, UID, resourceVersion) |
| `RuntimeProfileResolved` condition | `ProfileApplied` when a ref resolves; `NoProfileRef` when unset |

**Enforcement backends (`spec.enforcement[]`):** the only type today is **envoy** — a per-session out-of-pod egress proxy (upstream `envoyproxy/envoy` + the first-party `scrutineer-egress-reporter` for `observed` evidence) plus the default-deny routing lock on the agent pod — see `docs/design/evidence-integrity.md` and `docs/design/untamperable-enforcement.md`. Future out-of-pod chokepoints (tools pod, arena workspace) will add new types. Per-binary READMEs live under [`cmd/`](cmd/).

**Runtime reporter wiring:** the egress-proxy pod (not the agent pod) carries:

| Injected into the egress-reporter | Value |
|-----------------------------------|-------|
| `SCRUTINEER_REPORTER_URL` | `http://scrutineer-controller-reporter.scrutineer-system.svc:8088` (base URL; append `/v1/report`) |
| `SCRUTINEER_REPORTER_TOKEN_PATH` | `/var/run/secrets/scrutineer/reporter-token/token` |
| Projected volume | ServiceAccount token with audience `scrutineer-reporter` (600s expiry, kubelet-refreshed) |

The agent container does **not** receive the reporter URL or token — evidence comes only from outside its trust domain. Deploy the controller with `make deploy` (or `make dev-deploy`) so the `scrutineer-controller-reporter` Service exposes port `:8088`. Contract: [`docs/design/phase-3-runtime-reporter-contract.md`](docs/design/phase-3-runtime-reporter-contract.md).

**Profile change behavior:**

- Updating a referenced `RuntimeProfile` re-reconciles affected sessions (controller watch).
- If the owned Job has **not** started pods yet (`Active==0`), the controller **replaces** the Job so the pod template matches.
- If the Job is **already running**, pod templates are immutable — the running pod may retain the old security context until the Job is replaced manually or the session ends.

**Samples:**

```bash
kubectl apply -f config/samples/scrutineer_v1alpha1_runtimeprofile.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_runtimeprofile_ref.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_runtimeprofile_enforcement.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_runtimeprofile_enforcement.yaml
```

### Reusable policy (`AgentPolicy`)

Platform teams can publish baseline governance once; sessions reference policies and add inline overrides.

**Merge order** (implemented in [`internal/policy/`](internal/policy/)):

1. `spec.policyRefs` in list order
2. `spec.policy` inline overrides last (wins on conflict)
3. List fields are unioned
4. Effective **mode** = strictest across matched policies (`enforced` > `dry-run` > `audit-only`)

**Status written on reconcile:**

| Field | Meaning |
|-------|---------|
| `status.effectivePolicy` | Merged rules + mode propagated to the Job |
| `status.matchedPolicies` | Which policy CRDs contributed |
| `status.policyDecisions` | Bounded merge-time audit log (max 64) |

**Propagation today:** `AGENT_POLICY_*` and `AGENT_POLICY_MODE` env vars on the agent container (a propagation hook, not enforcement). Network FQDN rules are **enforced** at the per-session out-of-pod Envoy proxy when the `envoy` backend is enabled; `audit-only` / `dry-run` / `enforced` modes govern whether a matched rule blocks or only records. There is no tool/file policy surface until the tools/arena chokepoints land (#75 clean break).

**Policy change behavior:**

- Updating a referenced `AgentPolicy` re-reconciles affected sessions (controller watch).
- `status.effectivePolicy` updates immediately.
- If the owned Job has **not** started pods yet (`Active==0`), the controller **replaces** the Job so env vars match.
- If the Job is **already running**, pod templates are immutable — env inside the pod may be stale; `PolicyPropagated=False` / `PolicyEnvDrift` surfaces the gap.

**Samples:**

```bash
kubectl apply -f config/samples/scrutineer_v1alpha1_agentpolicy.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_policy_ref.yaml

# prod-agents-baseline must exist for the combined sample:
```

### Inline sample

```yaml
apiVersion: scrutineer.sh/v1alpha1
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
    # Domains match exactly, or use a "*." wildcard for a subdomain tree
    # (e.g. "*.github.com" covers api.github.com but not github.com itself).
    allowedDomains: ["github.com", "*.github.com"]
    deniedDomains:  ["dropbox.com", "*.gmail.com"]
    requireHumanApproval: [production_deploy, external_write]
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
| `runtimeRef` | Yes | Backend-neutral identity of the runtime object created (`apiVersion`/`kind`/`name`/`uid`); `kind` is `Job` or `Pod`. Prefer this over `jobName`. |
| `jobName` / `podName` | Yes | `jobName`: owned Job name (**deprecated** alias of `runtimeRef.name`; empty for the `kubernetes-pod` backend). `podName`: the agent Pod (newest Job-owned Pod, or the Pod itself for `kubernetes-pod`). |
| `matchedPolicies` | Yes | Policy CRDs that contributed to `effectivePolicy` |
| `effectivePolicy` | Yes | Merged rules + mode propagated to the Job |
| `policyDecisions` | Yes | Merge-time audit entries plus runtime decisions reported by the data plane (egress-reporter → reporter, max 64) |
| `matchedRuntimeProfile` | Yes | Applied `RuntimeProfile` ref (when set) |
| `result` | Yes | Terminal outcome / summary (on success, failure, timeout, cancel) |
| `usage` | **Yes** (from runtime reports) | Network/tool decisions increment counters; optional `usage` delta in `POST /v1/report` for tokens |
| `violations` | **Yes** (runtime reports) | Bounded list; `deny` and `dry-run` outcomes via `ApplyRuntimePolicyReport` |
| `events` | **Yes** (runtime reports) | Structured timeline stream (max 256); appended via `POST /v1/report` `events[]` |
| `artifacts` | **Yes** (when `spec.outputs` enabled) | Collected logs (ConfigMap) + workspace tar (Secret) references |

### Environment variables injected into the agent container

Scrutineer always injects these (empty when not set):

```
SCRUTINEER_SESSION_NAME
SCRUTINEER_SESSION_NAMESPACE
AGENT_TASK_DESCRIPTION
AGENT_TASK_PROMPT
AGENT_MODEL_PROVIDER
AGENT_MODEL_NAME
AGENT_MODEL_BASE_URL                 # optional; OpenAI-compatible endpoint override (e.g. OpenRouter)
AGENT_POLICY_ALLOWED_DOMAINS         # comma-separated
AGENT_POLICY_DENIED_DOMAINS          # comma-separated
AGENT_POLICY_ALLOWED_CIDRS           # comma-separated
AGENT_POLICY_DENIED_CIDRS            # comma-separated
AGENT_POLICY_REQUIRE_HUMAN_APPROVAL  # comma-separated
AGENT_POLICY_MODE
```

`AGENT_POLICY_*` values are propagated from merged policy to the agent container as a propagation/debugging hook. Enforcement happens out-of-pod: the session's Envoy proxy applies FQDN allow/deny per the effective mode.

Plus any `spec.runtime.env` entries the user adds.

---

## AgentSession controller reference

The controller lives in [`internal/controller/agentsession/`](internal/controller/agentsession/) (reconcile loop, policy/runtime watches, validation) and delegates pod/Job construction to [`internal/controller/job/`](internal/controller/job/) (build, explicit-proxy env injection, drift detection, status helpers — see its [package README](internal/controller/job/README.md)). Orchestrator-specific work goes through a backend-neutral `runtimeBackend` interface with two backends today — `kubernetes-job` and `kubernetes-pod`, both built from the shared `job.BuildPodTemplateSpec`; the reconciler maps each backend's normalized observation onto status, so governance semantics stay backend-independent. See the [package README](internal/controller/agentsession/README.md).

### Reconcile triggers

| Source | Mechanism | Effect |
|--------|-----------|--------|
| `AgentSession` | Primary `For()` watch | Any spec/status change on the session |
| Owned `Job` | `Owns(&batchv1.Job{})` | Job status transitions re-queue the parent session |
| Session `Pod` | `Watches(&corev1.Pod{})` | Job-owned Pods labeled `scrutineer.sh/session=<name>` re-queue the session (faster `podName` / Running updates) |
| `AgentPolicy` | Secondary watch | Sessions with matching `spec.policyRefs` re-reconcile |
| `RuntimeProfile` | Secondary watch | Sessions with matching `spec.runtimeProfileRef` re-reconcile |
| `ApprovalRequest` | Secondary watch | Approval grant/deny for a session re-reconciles it (gate resume + per-tool holds) |
| Timer | `RequeueAfter: 15s` | Backstop poll while Job is in flight (non-terminal sessions) |

### Reconcile flow

```
Fetch AgentSession
    │
    ├─ deleting? ──► stop owned Job ──► remove finalizer ──► return
    │
    ├─ ensure finalizer scrutineer.sh/finalizer
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
    ├─ cancelRequested? ──► delete Job ──► Cancelled, Completed, Ready=False ──► return
    │
    ├─ requireHumanApproval matches an ApprovalPolicy? ──► AwaitingApproval (create ApprovalRequest)
    │       ├── granted ──► proceed         ├── denied / onTimeout=deny ──► Denied
    │       └── no matching ApprovalPolicy ──► ApprovalNotEnforced warning (no gate)
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

Reconciliation is **idempotent**. Status updates use the status subresource with condition merging so concurrent writes do not drop condition types. The owned Job is named deterministically `scrutineer-session-<session-name>`; a foreign Job at that name causes `Phase=Denied` (`JobConflict`).

### Validation (`validateSpec`)

Controller-side checks (in addition to CRD OpenAPI validation):

| Check | Denial reason |
|-------|---------------|
| Task: description, prompt, or `promptConfigMapRef` required | `InvalidSpec` |
| `runtime.image` and `model.provider` / `model.name` non-empty | `InvalidSpec` |
| `runtime.orchestrator` must be `kubernetes-job` or `kubernetes-pod` | `InvalidSpec` |
| Temperature in `[0, 2]`; `maxTokens >= 1`; `timeoutSeconds >= 1` | `InvalidSpec` |
| `policyRefs[].kind` is `AgentPolicy` | `InvalidSpec` |
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

1. `spec.policyRefs` in list order
2. `spec.policy` inline overrides last
3. List fields union
4. Effective mode = strictest (`enforced` > `dry-run` > `audit-only`)

Written to status each reconcile: `effectivePolicy`, `matchedPolicies`, `policyDecisions` (merge-time + runtime, max 64). Propagated to the Job as `AGENT_POLICY_*` env vars (a hook, not enforcement); FQDN rules are enforced at the session's out-of-pod Envoy proxy, whose egress-reporter reports runtime evidence.

When a referenced policy changes:

- `status.effectivePolicy` updates immediately
- **Pending** Job (`Active==0`): controller **replaces** the Job (`PolicyEnvSynced` event)
- **Active** Job: pod template is immutable; `PolicyPropagated=False` / `PolicyEnvDrift` surfaces stale env

### Runtime profile resolution

When `spec.runtimeProfileRef` is set, the controller loads the `RuntimeProfile` (same namespace) and merges container/pod security fields plus enabled `spec.enforcement[]` into the Job template (`envoy` — the only type — provisions the out-of-pod proxy and points the agent at it). Profile drift (including enforcement changes) follows the same pending-Job-replace rules as policy env drift.

### Job lifecycle (`internal/controller/job`)

| Setting | Value |
|---------|-------|
| Name | `scrutineer-session-<session-name>` |
| Labels | `scrutineer.sh/session`, `app.kubernetes.io/name=scrutineer`, `app.kubernetes.io/component=agent-session` |
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

1. List Pods in the session namespace with label `scrutineer.sh/session=<session.Name>`
2. Keep only Pods whose ownerReference points at the **current** Job UID
3. Pick the Pod with the latest `creationTimestamp` (name breaks ties lexicographically)

### Cancellation and deletion

**Cancellation** (`spec.cancelRequested: true`): deletes the owned Job, sets `phase=Cancelled`, `result.outcome=cancelled`, `Completed=True` / `SessionCancelled`, `Ready=False`. Idempotent when the Job is already gone.

**Deletion**: finalizer `scrutineer.sh/finalizer` blocks AgentSession removal until the owned Job is deleted. `blockOwnerDeletion` is cleared on the Job so deletion cannot deadlock.

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
| `ApprovalNotEnforced` | Warning | `requireHumanApproval` declared but no `ApprovalPolicy` gates it |
| `ApprovalRequested` | Normal | Session is blocked on a human approval gate (`AwaitingApproval`) |
| `ApprovalGranted` | Normal | Approval granted; session resumes |
| `ApprovalDenied` | Warning | Approval denied or timed out; session `Denied` |
| `ApprovalNotified` | Normal | Approvers notified of an open gate (`--approval-webhook-url`) |
| `ApprovalNotifyFailed` | Warning | Approval notification delivery failed (will retry) |
| `ApprovalUnauthorized` | Warning | Grant set by a subject not listed in the policy's approvers; not honored |
| `ApprovalPartiallyApproved` | Normal | `allOf` gate received a valid grant but still needs more approvers |
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
kubectl get job scrutineer-session-<name> -o wide
kubectl get pods -l scrutineer.sh/session=<name>
```

---

## CI tiers

Per push/PR: **Lint** and **Test** (unit + envtest) always run; the cluster-heavy
workflows — **E2E** (standard + kindnet networking enforcement suite) and
**Quickstart Smoke** (`make quickstart && make demo` end-to-end) — skip docs-only
changes (#86). Nightly (+ manual dispatch): **Nightly Networking** cross-checks the
enforcement suite on Calico and a dual-stack cluster (#93). All cluster jobs build
the first-party images from the checkout under test — never registry pulls, which
can silently predate the checkout's behavior (#109).

## Developing with the dev container (recommended for contributors)

The repo ships with a `.devcontainer/` that gives you a fully wired Scrutineer dev
environment with **zero host setup beyond Docker + Cursor/VS Code**.

What you get when you open the folder in a Dev Container:

- Go 1.23 toolchain
- Docker-in-Docker
- `kubectl`, `kind`, `kustomize` pre-installed
- A local `kind` cluster named **`scrutineer-dev`** created automatically
- The Scrutineer CRD installed into that cluster on first start

### Open it

1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/) on
   your host (or any Docker-compatible runtime).
2. Open this folder in Cursor / VS Code.
3. When prompted, choose **"Reopen in Container"**, or run the
   `Dev Containers: Reopen in Container` command.

On first build the `postCreateCommand` (`.devcontainer/bootstrap.sh`) will:

1. Wait for the in-container Docker daemon.
2. `go mod download`.
3. Create the `scrutineer-dev` kind cluster (idempotent — re-runs are safe).
4. `kubectl apply` the Scrutineer CRD.
5. Print the next-step commands.

### Inside the container

```bash
# (1) Run the controller against the kind cluster from your terminal:
make run

# (2) In a second terminal, apply a sample AgentSession:
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml
kubectl get agentsessions -w
kubectl describe agentsession github-readme-update
kubectl logs job/scrutineer-session-github-readme-update

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
| `make kind-up`  | Create the `scrutineer-dev` kind cluster (no-op if it exists).          |
| `make kind-down`| Delete the `scrutineer-dev` kind cluster.                               |
| `make kind-load`| `docker-build` the controller image + `kind load docker-image`.    |
| `make dev-up`   | `kind-up` + install CRDs. Use with `make run` for the dev loop.    |
| `make dev-deploy`| Build + load + deploy the controller into the kind cluster.       |
| `make dev-sample`| Apply success + failing sample AgentSessions.                     |
| `make verify-samples` | Server-side dry-run all `config/samples/scrutineer_*.yaml` (needs CRDs). |
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

#### Pinned tool versions

These are pinned so contributors don't hit Go/envtest/apiserver version skew.
The values below are mirrored from the source of truth — `Makefile` and
`.devcontainer/kind-config.yaml` — so update those files (not just this table)
when a version changes:

| Tool | Version | Pinned in |
|------|---------|-----------|
| Go toolchain | `1.23` | `.devcontainer/devcontainer.json` (`VARIANT=1-1.23-bookworm`), CI workflows |
| `controller-gen` | `v0.16.1` | `Makefile` (`CONTROLLER_TOOLS_VERSION`) |
| `kustomize` | `v5.4.3` | `Makefile` (`KUSTOMIZE_VERSION`) |
| `setup-envtest` | `release-0.19` | `Makefile` |
| envtest Kubernetes assets | `1.31.0` | `Makefile` (`ENVTEST_K8S_VERSION`) |
| kind node image (dev + e2e) | `kindest/node:v1.31.4` | `.devcontainer/kind-config.yaml` |
| kind CLI (CI e2e) | `v0.31.0` | `.github/workflows/e2e.yaml` |

`make test` runs the envtest suite against the `ENVTEST_K8S_VERSION` apiserver
(`1.31.0`), and the dev/e2e `kind` cluster pins `kindest/node:v1.31.4` so the
CRD is exercised against a matching apiserver version in both unit and e2e
runs. Do **not** upgrade these unless something is broken.

### 1. Generate code and CRDs

```
make generate    # regenerate zz_generated.deepcopy.go
make manifests   # regenerate config/crd/bases and RBAC
```

A pre-generated CRD is already checked in at
`config/crd/bases/scrutineer.sh_agentsessions.yaml`, so this step is only
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
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml
```

### 5. Observe it

```
kubectl get agentsessions
kubectl describe agentsession github-readme-update
kubectl get jobs
kubectl logs job/scrutineer-session-github-readme-update
```

You should see:

- `kubectl get agentsessions` showing `Phase` transition
  `Starting` → `Running` → `Succeeded`
- `kubectl describe` showing Events: `JobCreated`, `JobRunning`, `JobSucceeded`
- `kubectl logs` showing the injected `SCRUTINEER_*` / `AGENT_*` env values

### 6. Try the failing sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_failing.yaml
kubectl get agentsessions
```

It should transition to `Failed` with a `JobFailed` event and
`Completed=False` condition.

### 7. Try the prompt ConfigMap sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_prompt_cm.yaml
kubectl get agentsession github-readme-from-cm -w
```

Applies a ConfigMap plus an AgentSession that loads `spec.task.promptConfigMapRef`
(same namespace). Expect `Succeeded` when the controller is running.

### 8. Try the cancellation sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_cancel.yaml
kubectl get agentsession cancel-at-create-sample -w
```

Expect `Phase=Cancelled` and no `scrutineer-session-cancel-at-create-sample` Job.

To cancel a long-running session, apply the success sample, wait for `Running`, then patch `cancelRequested` as described in [Cancelling a running session](#cancelling-a-running-session).

### 9. Try the RuntimeProfile sample

```
kubectl apply -f config/samples/scrutineer_v1alpha1_runtimeprofile.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_runtimeprofile_ref.yaml
kubectl get agentsession session-with-runtimeprofile -w
```

Expect a Job whose pod template includes settings from `hardened-agent` (see `kubectl get job scrutineer-session-session-with-runtimeprofile -o yaml`). The sample uses stricter container hardening; use an image compatible with `runAsNonRoot` / `readOnlyRootFilesystem` in production.

### Validate samples against the installed CRD

After `make install` (or `make dev-up`), check that hand-maintained samples still match the API:

```
make verify-samples
```

This runs `kubectl apply --dry-run=server` on each `config/samples/scrutineer_*.yaml` (success, failing, cancel-at-create, prompt ConfigMap, AgentPolicy/RuntimeProfile refs).

---

## Current behavior (quick reference)

| Capability | Shipped? | Notes |
|------------|----------|-------|
| Reconcile to Kubernetes runtime | Yes | `runtime.orchestrator: kubernetes-job` (default) or `kubernetes-pod`, via the `runtimeBackend` interface; `status.runtimeRef` records the object created |
| Task prompt / ConfigMap prompt | Yes | `spec.task` or `promptConfigMapRef` (same namespace) |
| `AgentPolicy` + `spec.policyRefs` | Yes | Same-namespace merge → `status.effectivePolicy`; inline `spec.policy` overrides win |
| Policy modes (`audit-only` / `dry-run` / `enforced`) | Yes | Strictest mode in status + `AGENT_POLICY_MODE`; **enforced** at the egress chokepoint |
| `status.policyDecisions` | Yes | Merge-time + runtime decisions (max 64) |
| Policy / profile change → Job env sync | Partial | Replaces **pending** Jobs; `PolicyEnvDrift` if Job already active |
| `RuntimeProfile` + `runtimeProfileRef` | Yes | Same-namespace; merges into Job pod template; watch + pending Job replace |
| Enforcement backends (`spec.enforcement`) | Yes | Out-of-pod `envoy` egress proxy with `observed` evidence (the only type; the cooperative in-pod tier was removed, #71) |
| **Network egress enforcement** | Yes | Out-of-pod `envoy` egress proxy + default-deny routing lock, gated verified-or-refused by the lock probe (`observed` evidence) |
| **Tool-call governance** | Not yet | No policy surface until the tools-pod chokepoint lands (deferred design; #75 clean break) |
| **File-access governance** | Not yet | No policy surface until the arena workspace lands (deferred design; #75 clean break) |
| **Runtime evidence loop** | Yes | [reporter](internal/reporter/) merges `policyDecisions`/`violations`/`usage`/`events` from the egress-reporter |
| Human approval gate | Yes | `ApprovalPolicy` → `AwaitingApproval` → grant/deny; per-tool runtime holds; authenticated-approver webhook (opt-in) |
| Observability & audit | Yes | Prometheus metrics, OTel traces, OTLP audit sink |
| `status.usage` / `status.violations` / `status.events` | Yes (runtime) | Populated from egress-reporter reports — see [Status fields](#status-fields) |
| `status.artifacts` | Yes | Logs (ConfigMap) + workspace tar (Secret) when `spec.outputs` enabled |
| Pod watch · `Ready` condition · finalizer cleanup · cancellation | Yes | See controller reference above |

Live task state & roadmap: [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues). Durable context: [`docs/design/`](docs/design/).

### Deploying the controller into the cluster

```
make docker-build IMG=ghcr.io/grantbarry29/scrutineer:dev
make docker-push  IMG=ghcr.io/grantbarry29/scrutineer:dev
make deploy       IMG=ghcr.io/grantbarry29/scrutineer:dev
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
- [x] The controller creates a Job named `scrutineer-session-github-readme-update`
- [x] The Job runs and exits 0
- [x] `status.phase` transitions `Pending` → `Starting` → `Running` → `Succeeded`
- [x] `status.jobName` is populated
- [x] `status.podName` is populated once a pod exists
- [x] Kubernetes Events `JobCreated`, `JobRunning`, `JobSucceeded` are visible in `kubectl describe`
- [x] `status.conditions` include `Validated`, `RuntimeCreated`, `Completed`, and `Ready`
- [x] The failing sample transitions to `Failed` and emits `JobFailed`

---

## Roadmap

Phases 0–5 have shipped (control-plane reconciliation, reusable policy + runtime
profiles, the runtime-evidence loop, observability/audit export, and human approval
workflows), followed by the narrowing to **adversarial-grade-only enforcement** (#69–#71): enforcement is
adversarial-grade only — the out-of-pod Envoy egress path with the verified-or-refused
lock gate — and the cooperative in-pod tier was removed. Phase 6 is in progress.
Live task state and the **roadmap** are in
[GitHub Issues / Projects](https://github.com/grantbarry29/scrutineer/issues); durable technical context and
design docs are in [`docs/design/`](docs/design/). Highlights of what remains:

### Shipped CRDs

`AgentSession`, `AgentPolicy`, `RuntimeProfile`, `ApprovalPolicy`,
`ApprovalRequest` — all namespace-scoped. (`ToolPolicy` was removed with the
cooperative tier — it returns, likely reshaped, with the tools-pod chokepoint.) A `CredentialProfile` (credential
mediation at the tools-pod chokepoint) and `SessionTemplate` (parameterized
blueprints) remain future work.

### In progress / next

- **Adversarial-grade egress (#8) — shipped.** The per-session out-of-pod Envoy proxy,
  the default-deny routing lock (kindnet + Calico), identity-authenticated `observed`
  egress evidence, and **FQDN allow/deny enforced at the proxy**
  ([#32](https://github.com/grantbarry29/scrutineer/issues/32)) are in place; agents can't
  bypass egress or forge its record (under the documented assumptions). Next in this area:
  a transparent node interceptor ([#64](https://github.com/grantbarry29/scrutineer/issues/64))
  for non-HTTP protocols.
- **Phase 6 — orchestrator adapters.** The backend-neutral `runtimeBackend`
  interface, `status.runtimeRef`, and a second in-tree backend (`kubernetes-pod`)
  have shipped, proving Scrutineer is orchestrator-agnostic. Next is the external
  adapter design (`tekton` first, then `argo`, `temporal`, `external`). Design:
  [`docs/design/phase-6-orchestrator-interface.md`](docs/design/phase-6-orchestrator-interface.md).

### Future

- **Stronger runtime enforcement** — building on the shipped out-of-pod Envoy egress +
  `observed` evidence (#8): eBPF process/file/syscall observation and sandbox runtimes
  (gVisor / Kata / Firecracker), extending *adversarial-grade* integrity from egress to
  syscalls/files.
- **Phase 7 — operational UI** — a governance/observability dashboard (session
  list/detail, timelines, approval inbox, runtime topology, audit/forensics).
- **Phase 8 — enterprise platform** — per-session identity, `CredentialProfile`
  scoped secrets, multi-tenancy, HA, and (later) multi-cluster.

---

## License

Apache 2.0. See [LICENSE](./LICENSE).
