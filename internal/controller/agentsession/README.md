# agentsession controller

The core Scrutineer control-plane controller. Reconciles `AgentSession` custom resources into a
governed runtime workload and tracks observed governance status. Compiled into the manager
binary ([`cmd/main.go`](../../../cmd/main.go) → `SetupWithManager`); the root
[`README.md`](../../../README.md) is the manager/deployment overview.

## Purpose

Turn a declared `AgentSession` (task + policy + runtime profile) into a running, governed
workload and keep its status a faithful record of what was validated, propagated, created,
and observed. It stays **orchestrator-agnostic**: all orchestrator-specific work goes
through the `runtimeBackend` interface (two backends today: `kubernetes-job` and
`kubernetes-pod`).

## Responsibilities / Non-responsibilities

- **Does:** validate the spec; resolve task, policy (inline + referenced `AgentPolicy`/
  `ToolPolicy`), and `RuntimeProfile`; run the pre-runtime **human-approval gate**;
  reconcile mid-execution per-tool `ApprovalRequest`s; create/observe the runtime via the
  selected backend; map a backend-neutral `observation` onto status/conditions/events/
  result; manage the finalizer, cancellation, and idempotent requeue; emit Kubernetes
  events, audit records, and traces.
- **Does not:** build the pod/sidecar template (delegates to [`../job`](../job)); enforce
  policy at runtime (data-plane sidecars do); write runtime evidence into status (the
  [reporter](../../reporter) does, via `PatchRuntimePolicyReport`); couple to Jobs (the
  backend abstraction hides that).

## Entry point & execution model

- `SetupWithManager` registers the controller; `Reconcile` is the single idempotent loop.
- Triggers: `AgentSession` plus owned `NetworkPolicy`, the per-session egress-proxy
  objects (`Service`/`ServiceAccount`/`ConfigMap`), and each backend's owned runtime
  object, and watches on `Pod`, `AgentPolicy`, `ToolPolicy`, `RuntimeProfile`, and
  `ApprovalRequest` (mapped back to affected sessions).

## Control / data flow

Per `Reconcile` (see the doc comment in [`reconciler.go`](./reconciler.go)): fetch →
handle deletion/finalizer → init `Pending` → validate → resolve task/policy/profile →
cancellation short-circuit → terminal short-circuit → approval gate → runtime-approval
reconcile → `backend.ensure(...)` → `applyObservation` → single status patch. Re-running
against an unchanged cluster makes no API mutations.

## Major internal files

- `reconciler.go` — loop, finalizer, status/condition/event mapping (`applyObservation`,
  `applyRuntimePhase`, `setReadyCondition`).
- `runtime_backend.go` + `pod.go` — `runtimeBackend` interface, backend registry, and the
  `kubernetes-job` backend (`pod.go` discovers the Job's agent Pod for `status.podName`);
  `observation`/`runtimePhase` are the backend-neutral output.
- `backend_pod.go` — the `kubernetes-pod` reference backend (runs the agent as a bare
  Pod via `job.BuildPodTemplateSpec`), selected by `spec.runtime.orchestrator: kubernetes-pod`.
- `egress_envoy.go` — `egressBackend` seam + the interim `explicitProxyEgressBackend`,
  which provisions the per-session out-of-pod Envoy proxy (`ServiceAccount`/`ConfigMap`/
  `Service`/`Pod` from [`internal/enforcement/envoy`](../../enforcement/envoy)) when a
  `RuntimeProfile` enables the `envoy` type, and tears it down on terminal. The Pod runs
  two containers: Envoy and the **egress-reporter**
  ([`cmd/egress-reporter`](../../../cmd/egress-reporter)), which tails Envoy's access log
  and submits `observed` evidence with the pod's projected per-session SA token (Slice C,
  #62; reporter URL/audience passed from the job package). `Reconcile` provisions it (and
  `resolveEgressProxyEndpoint` records the Envoy Service ClusterIP into
  `status.egressProxyEndpoint`) **before** the agent runtime is built, so the agent is
  pointed at Envoy by ClusterIP — no DNS needed under the routing lock. A future node
  interceptor (#64) implements the same interface.
- `networkpolicy.go` — reconciles **two** per-session egress policies via
  `reconcileOwnedNetworkPolicy`: the agent-pod **routing lock** (allow only the session's
  Envoy pod; deny DNS and everything else — the mandatory chokepoint) and, on the Envoy pod,
  the **egress backstop** (`networkpolicy.BuildEgressProxyBackstop`: allow DNS + internet
  EXCEPT `EgressBackstopCIDRs`, so even a compromised Envoy can't reach cloud metadata).
  Both are torn down on terminal.
- `lock_gate.go` — the **verified-or-refused gate** (#70,
  [`docs/design/untamperable-pivot.md`](../../../docs/design/untamperable-pivot.md) §4):
  before runtime creation, enforced-mode sessions whose enforcement substrate is a
  NetworkPolicy consult `LockVerifier`
  ([`internal/enforcement/lockverify`](../../enforcement/lockverify) — differential
  canary probe proving the CNI enforces NetworkPolicy). Unverified ⇒ the session holds
  at `Pending` with condition `EgressLockVerified=False` (reason
  `CNIDoesNotEnforceNetworkPolicy` / `ProbeInconclusive`) and a warning event on
  transition; audit/dry-run sessions get the condition but run. Nil `LockVerifier`
  (reporter-only, unit suites) disables the gate; `cmd/main.go` always wires it for
  the controller role — there is no attestation override.
- `validation.go`, `task.go`, `policy.go` / `policy_decisions.go`, `runtimeprofile.go` —
  spec validation and resolution.
- `approval.go` / `approval_runtime.go` — pre-runtime gate + per-tool runtime approvals.
- `status.go` / `reporter_status.go` (`PatchRuntimePolicyReport`), `events.go`,
  `*_watch.go` — status merges consumed by the reporter, events, and watch mappers.

## Repository dependencies & related components

- Depends on [`api/v1alpha1`](../../../api/v1alpha1), [`../job`](../job) (pod template),
  [`internal/approval`](../../../internal/approval), [`internal/audit`](../../../internal/audit),
  [`internal/tracing`](../../../internal/tracing).
- Related: the [reporter](../../reporter) writes observed evidence into the status this
  controller owns; the data-plane producer is the egress-reporter
  ([`cmd/egress-reporter`](../../../cmd/egress-reporter)) in the per-session
  egress-proxy pod.
- Design: [`docs/design/architecture.md`](../../../docs/design/architecture.md),
  [`docs/design/phase-6-orchestrator-interface.md`](../../../docs/design/phase-6-orchestrator-interface.md).

## Interfaces & artifacts

- Reconciles `AgentSession`; owns the runtime object (`Job` or `Pod`), the per-session
  egress-proxy `Service`/`ServiceAccount`/`ConfigMap`/`Pod`, and **two** `NetworkPolicy`
  objects — the agent routing lock and the Envoy egress backstop (all owner refs).
- `--egress-backstop-cidrs` (manager flag → `EgressBackstopCIDRs`): CIDRs hard-denied to the
  Envoy proxy pod; empty ⇒ safe default `169.254.0.0/16` (cloud metadata). Operators add
  cluster/service/API CIDRs.
- Status subresource: `phase`, `observedGeneration`, `runtimeRef` (backend-neutral
  runtime identity), `jobName`/`podName` (`jobName` is a deprecated alias of
  `runtimeRef.name`), `egressProxyEndpoint` (the Envoy ClusterIP URL the agent proxies
  through), `conditions` (`Validated`, `PolicyResolved`, `RuntimeProfileResolved`,
  `PolicyPropagated`, `RuntimeCreated`, `Completed`, `Ready`), `result`, and
  reporter-populated `policyDecisions`/`violations`/`usage`.
- RBAC is generated from the `+kubebuilder:rbac` markers in `reconciler.go` via
  `make manifests` (into `config/rbac/`). The manager role is least-privilege —
  each verb maps to an actual client call (audited 2026-06-27):

  | Resource | Verbs | Why |
  |---|---|---|
  | `agentsessions` | get,list,watch,update,patch | Primary resource: reconcile + finalizer; **never** created/deleted by the controller |
  | `agentsessions/status`, `agentsessions/finalizers` | get,update,patch / update | Status subresource + finalizer |
  | `agentpolicies`, `toolpolicies`, `runtimeprofiles`, `approvalpolicies` | get,list,watch | Read-only policy/profile refs + watches |
  | `approvalrequests` (+`/status`) | get,list,watch,create,update,patch | Created with an owner ref (GC deletes them → no `delete`) |
  | `jobs` (batch) | get,list,watch,create,update,patch,delete | `kubernetes-job` runtime object (create + drift-replace + cancel) |
  | `pods` | get,list,watch,create,update,patch,delete | `kubernetes-pod` runtime object (create/owner-patch/stop); `get,list,watch` also serve the Job-owned-pod watch + `status.podName` |
  | `networkpolicies` | get,list,watch,create,update,patch,delete | Per-session agent routing lock + Envoy egress backstop (create/update/delete) |
  | `configmaps` | get,list,watch,create,update,patch,delete | Prompt ConfigMap read + output logs/artifact tar; per-session egress-proxy bootstrap (created + torn down on terminal → `delete`) |
  | `secrets` | get,list,watch,create,update,patch | Output artifacts (created with owner ref → GC deletes, no `delete`) |
  | `services`, `serviceaccounts` | get,list,watch,create,update,patch,delete | Per-session Envoy egress proxy Service + dedicated identity (created + torn down on terminal) |
  | `events` | create,patch | Kubernetes events |
  | `pods/log`, `pods/exec` | get / create | Log + workspace-tar collection (`spec.outputs`) |

  `list`/`watch` are required wherever the controller `Get`s through the cached
  client (informers `LIST`+`WATCH` even for single-object reads).

## Invariants & files that must change together

- The **reconciler — not the backend — owns** the observation→status mapping; backends
  return a neutral `observation` only.
- Idempotent reconcile; **terminal phases are never overwritten**; resolution events are
  gated by `conditionChanged` to avoid event spam on requeue.
- Adding/changing a backend touches `runtime_backend.go` ↔ its impl (e.g. `pod.go`) ↔
  `SetupWithManager`'s `Owns(backend.ownedType())`. Changing `+kubebuilder:rbac` markers
  requires re-running `make manifests`.
- The egress proxy is provisioned **before the agent runtime** in `Reconcile` (so its Service
  ClusterIP is known and injected as the agent's proxy) and again idempotently in
  `patchStatusWithEnforcement`; it is **torn down on terminal**, same as the NetworkPolicies.
  Its objects are create-if-missing and a function of the session name only, so provisioning
  and teardown share `egressBackend.desiredObjects`. Enablement is the `envoy` type in the
  `RuntimeProfile`.
- **Routing lock ⇄ ClusterIP ⇄ DNS are coupled:** the agent lock denies DNS, so the agent
  must reach Envoy by ClusterIP (`status.egressProxyEndpoint`, resolved before the Job is
  built and consumed by `../job`'s `agentEnvoyProxyURL`). Changing any one — the lock's
  allowed peer, the endpoint form, or the agent proxy env — requires changing the others.
  The Envoy backstop's `EgressBackstopCIDRs` must always keep DNS reachable (dedicated rule)
  or Envoy can't resolve upstreams.

## Build / test / run / validate

`make test` (unit + envtest), `make manifests` / `make generate` after API or marker
changes, `make run` to run the controller against the current kubeconfig.

## Operability

Surfaces state via `status.phase`/`conditions`/`result`, Kubernetes events (e.g.
`JobCreated`, `JobRunning`, `JobSucceeded`, `JobFailed`, `SessionDenied`,
`AwaitingApproval`), audit emissions (`SessionPhaseChange`), and reconcile traces. Common
failure modes: invalid spec/task/policy/profile → `Denied`; Job not owned → `Denied`
(`JobConflict`); deadline exceeded → `TimedOut`.
