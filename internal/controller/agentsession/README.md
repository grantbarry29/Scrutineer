# agentsession controller

The core Relay control-plane controller. Reconciles `AgentSession` custom resources into a
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
- Triggers: `AgentSession` plus owned `NetworkPolicy` and each backend's owned runtime
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
  controller owns; the data-plane sidecars ([`cmd/dns-proxy`](../../../cmd/dns-proxy),
  [`cmd/tool-gateway`](../../../cmd/tool-gateway), [`cmd/fs-gateway`](../../../cmd/fs-gateway)).
- Design: [`docs/design/architecture.md`](../../../docs/design/architecture.md),
  [`docs/design/phase-6-orchestrator-interface.md`](../../../docs/design/phase-6-orchestrator-interface.md).

## Interfaces & artifacts

- Reconciles `AgentSession`; owns the runtime object (`Job` or `Pod`) + `NetworkPolicy` (owner refs).
- Status subresource: `phase`, `observedGeneration`, `runtimeRef` (backend-neutral
  runtime identity), `jobName`/`podName` (`jobName` is a deprecated alias of
  `runtimeRef.name`), `conditions` (`Validated`, `PolicyResolved`, `RuntimeProfileResolved`,
  `PolicyPropagated`, `RuntimeCreated`, `Completed`, `Ready`), `result`, and
  reporter-populated `policyDecisions`/`violations`/`usage`.
- RBAC is generated from the `+kubebuilder:rbac` markers in `reconciler.go` via
  `make manifests` (into `config/rbac/`).

## Invariants & files that must change together

- The **reconciler — not the backend — owns** the observation→status mapping; backends
  return a neutral `observation` only.
- Idempotent reconcile; **terminal phases are never overwritten**; resolution events are
  gated by `conditionChanged` to avoid event spam on requeue.
- Adding/changing a backend touches `runtime_backend.go` ↔ its impl (e.g. `pod.go`) ↔
  `SetupWithManager`'s `Owns(backend.ownedType())`. Changing `+kubebuilder:rbac` markers
  requires re-running `make manifests`.

## Build / test / run / validate

`make test` (unit + envtest), `make manifests` / `make generate` after API or marker
changes, `make run` to run the controller against the current kubeconfig.

## Operability

Surfaces state via `status.phase`/`conditions`/`result`, Kubernetes events (e.g.
`JobCreated`, `JobRunning`, `JobSucceeded`, `JobFailed`, `SessionDenied`,
`AwaitingApproval`), audit emissions (`SessionPhaseChange`), and reconcile traces. Common
failure modes: invalid spec/task/policy/profile → `Denied`; Job not owned → `Denied`
(`JobConflict`); deadline exceeded → `TimedOut`.
