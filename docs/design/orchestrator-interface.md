---
type: Design Doc
title: Orchestrator Backend Interface
description: "The runtimeBackend interface and registry keyed by spec.runtime.orchestrator; the reconciler owns all status/condition/event mapping. Proven by two in-tree backends (kubernetes-job, kubernetes-pod). Next: the external adapter design (Tekton first)."
status: implemented
read_when: "Decoupling from Kubernetes Jobs — RuntimeBackend, orchestrator selection, adding adapters."
---

# Orchestrator Backend Interface

> Slices 1–8 **shipped (2026-06-27)**. The AgentSession reconciler calls runtimes only through a `runtimeBackend` interface selected from a registry keyed by `spec.runtime.orchestrator` (`internal/controller/agentsession/runtime_backend.go`); backends return a normalized `observation` and the **reconciler** owns all status/condition/event/result mapping (`applyObservation`/`applyRuntimePhase`). The abstraction is now proven by **two** in-tree backends: `kubernetes-job` (default) and `kubernetes-pod` (a bare Pod, the reference adapter). Shipped generalizations: a shared pod-template builder (`job.BuildPodTemplateSpec`, slice 3), a backend-neutral `status.runtimeRef` (slice 4, resolves open question #1), the `kubernetes-pod` backend with lifecycle/drift correctness + GC parity (slices 5–7), and a live kind e2e (slice 8).
>
> **Next (slices 9–10):** docs/status alignment (slice 9, this update) and the **external** adapter design (slice 10, Tekton first) on top of the now-proven interface.

## Purpose

The product vision is explicit: *"Keep Scrutineer orchestrator-agnostic. Avoid coupling APIs or controllers permanently to Kubernetes Jobs"* and *"Treat Kubernetes Jobs, Tekton, Argo Workflows, Temporal … as execution backends Scrutineer can govern, not systems Scrutineer should replace."* Today the reconciler calls `batchv1.Job` directly in several places. This doc catalogs that coupling and proposes a narrow interface so the **governance logic** (validation, policy resolution, approval gate, evidence loop, status/conditions, audit) stays backend-independent while **runtime mechanics** (create/observe/stop) move behind a pluggable backend.

This is a **design-only** slice. Implementation (extracting the interface, making Jobs the first backend) is slice 2; concrete adapters are later slices.

## Non-goals

- Implementing any new backend (Tekton/Argo/Temporal) — those are later slices.
- Changing the AgentSession API/CRD schema. `spec.runtime.orchestrator` already exists; `status.jobName`/`status.podName` generalization is an open question, not this slice.
- Reworking the status-merge/union logic, approval gate, policy model, or reporter contract.
- Breaking or behaviorally changing the existing `kubernetes-job` path (slice 2 must be behavior-preserving).
- Building a generic workflow engine or scheduler (vision non-goal).

## Current coupling (what touches `batchv1.Job` today)

The governance pipeline in `internal/controller/agentsession/reconciler.go` is already backend-neutral up to the approval gate. Job coupling is concentrated after it:

| Touchpoint | Location | What it does |
|------------|----------|--------------|
| Create/ensure runtime | `ensureJob` (`reconciler.go`) | Builds a `batchv1.Job` via `job.Build`, Get/Create, ownership check (`ErrJobNotOwned`), drift detection + replace (`job.PolicyEnvDrift`/`RuntimeProfileDrift`/`ReplaceableForSync`), sets `RuntimeCreated`/`PolicyPropagated` conditions + `PhaseStarting`. |
| Observe status | `syncStatusFromJob` (`reconciler.go`) | Maps `Job.Status` (`Succeeded`/`Active`/`Failed`+`job.BackoffExhausted`/`job.TimedOut`) → AgentSession phase + `Completed` condition + `SessionResult`. |
| Find pod | `findPodName` (`pod.go`) | Locates the owned Pod for `status.podName` and reporter wiring. |
| Stop runtime | `stopRuntimeJob` (`reconciler.go`) | Deletes the Job on cancellation and finalizer cleanup. |
| Deletion gate | `handleDeletion` (`reconciler.go`) | Uses `job.NameFor`, waits for the `batchv1.Job` to disappear before removing the finalizer. |
| Manager wiring | `SetupWithManager` | `Owns(&batchv1.Job{})` + Pod watch → session mapper. |
| Runtime spec build | `internal/controller/job` (`builder.go`, `sidecars.go`, `sync.go`, `status.go`) | Translates session+policy+profile into a Job pod template, injects enforcement sidecars, reporter token projection, drift helpers. |

The `internal/controller/job` package already has a clean function surface (`NameFor`, `Build`, `TimedOut`, `BackoffExhausted`, `ReplaceableForSync`, `PolicyEnvDrift`, `RuntimeProfileDrift`) — most of the extraction work is wrapping these behind an interface, not rewriting them.

## Proposed `RuntimeBackend` interface

A backend owns runtime mechanics only. It receives a fully-resolved, governance-approved session and returns **normalized** runtime state; the reconciler maps that to phase/conditions/events exactly as today.

```go
// package runtime (internal/controller/runtime)

// Backend executes and observes a governed AgentSession on a specific orchestrator.
// Implementations are stateless; all inputs arrive per-call.
type Backend interface {
    // Name is the spec.runtime.orchestrator value this backend handles
    // (e.g. "kubernetes-job").
    Name() string

    // Ensure creates the runtime if absent and reconciles drift, idempotently.
    // Returns normalized observed state. Ownership/conflict is reported via
    // ErrRuntimeNotOwned. Must be safe to call every reconcile.
    Ensure(ctx context.Context, in EnsureInput) (Observation, error)

    // Observe returns current normalized state without mutating anything.
    Observe(ctx context.Context, in RuntimeRef) (Observation, error)

    // Stop requests termination (cancellation/finalizer). Idempotent;
    // NotFound is success.
    Stop(ctx context.Context, in RuntimeRef) error

    // OwnedType is the runtime object kind to watch via Owns() so completions
    // trigger reconciles (e.g. &batchv1.Job{}). May return nil for backends
    // observed by polling/webhook instead of owner-ref watches.
    OwnedType() client.Object
}

// EnsureInput is the resolved, approved session plus its effective policy/profile.
type EnsureInput struct {
    Session *scrutineerv1alpha1.AgentSession
    Task    *ResolvedTask
    Policy  *policy.Resolved
    Profile *scrutineerv1alpha1.RuntimeProfile
}

// RuntimeRef identifies a session's runtime without leaking backend types.
type RuntimeRef struct{ Namespace, Name string }

// RuntimePhase is the backend-neutral lifecycle of a runtime, mapped by the
// reconciler onto AgentSessionPhase.
type RuntimePhase string

const (
    RuntimeStarting  RuntimePhase = "Starting"
    RuntimeRunning   RuntimePhase = "Running"
    RuntimeSucceeded RuntimePhase = "Succeeded"
    RuntimeFailed    RuntimePhase = "Failed"
    RuntimeTimedOut  RuntimePhase = "TimedOut"
)

// Observation is normalized runtime state. RuntimeName/WorkloadName feed the
// (currently Job/Pod-specific) status fields; assurance-relevant identity for
// the reporter stays backend-internal.
type Observation struct {
    Phase        RuntimePhase
    RuntimeName  string // e.g. Job name → status.jobName today
    WorkloadName string // e.g. Pod name → status.podName today
    Message      string
    PolicyInSync bool   // false → PolicyEnvDrift surfaced by the reconciler
}
```

### Selection

`spec.runtime.orchestrator` (default `kubernetes-job`, currently the only value accepted by `validateSpec`) selects the backend from a small registry built at manager startup. Validation stays the gate for unsupported values; the registry maps name → `Backend`. `SetupWithManager` calls `Owns(b.OwnedType())` for each registered backend.

### Reconciler shape after extraction

```
… validate → resolve policy/profile → approval gate …            (unchanged, backend-neutral)
backend := registry[session.Spec.Runtime.Orchestrator]
obs, err := backend.Ensure(ctx, EnsureInput{…})                  (was ensureJob)
applyObservation(session, obs)                                   (was syncStatusFromJob; maps RuntimePhase→AgentSessionPhase, sets conditions/result)
… patch status with enforcement …                               (unchanged)
```

Cancellation and `handleDeletion` call `backend.Stop`/`backend.Observe` instead of `stopRuntimeJob`/`getJob`.

## Where the data plane / evidence loop fits

Enforcement sidecars and the reporter token are injected by `internal/controller/job` into the **Pod template** — this is Kubernetes-Pod-specific. The `kubernetes-job` backend keeps that wiring. Backends without a co-located pod (e.g. an external Temporal worker) cannot inject sidecars the same way, which directly affects **evidence assurance**: such backends start at best `self-reported` over a different channel, or have no runtime evidence at all until an `observed` source exists. The interface deliberately does **not** promise sidecar parity across backends; the reporter contract (`runtime-reporter-contract.md`) and assurance levels (`controller`/`self-reported`/`observed`) already model this honestly. New backends must declare their evidence channel and assurance explicitly.

## Invariants

- Governance order is fixed and backend-independent: validate → resolve → **approval gate** → runtime. A backend is only ever invoked for an approved, non-terminal session.
- `Ensure`/`Stop`/`Observe` are idempotent; the reconciler may call them every pass.
- The reconciler — not the backend — owns AgentSession status, conditions, events, and audit. Backends return a normalized `observation` only. *(Held since slice 2b: `kubernetesJobBackend` performs only runtime mutations; `applyObservation`/`applyRuntimePhase` own status mapping.)*
- Ownership/GC stays owner-reference based where the backend uses `Owns()`; `blockOwnerDeletion=false` semantics (so the session can finalize) are preserved.
- No backend may weaken the evidence-integrity story silently; assurance level must reflect the backend's real channel.

## Second backend: `kubernetes-pod` (reference adapter)

The first non-Job backend is a **bare Pod** (`spec.runtime.orchestrator: kubernetes-pod`): the agent + enforcement sidecars run as a single Pod the controller owns directly, with no Job wrapper. It is chosen as the *reference* second backend deliberately:

- **Dependency-free and fully testable.** No external CRDs, operators, or `go.mod` additions; it runs in the existing envtest control plane and the kind e2e harness exactly like the Job backend. An external adapter (Tekton/Argo) would add a heavyweight dependency and bespoke e2e infra before the abstraction is proven.
- **Exercises every generalization point** a real adapter needs, so it de-risks them: a different runtime object kind (`Pod` vs `Job`), different completion semantics (`Pod.Status.Phase` vs Job `Succeeded`/`Failed` counts + backoff), different timeout handling (`Pod.spec.activeDeadlineSeconds` directly), different drift/replace (Pods are largely immutable → delete+recreate, no Job-backoff wrapper), a different `ownedType` (`&corev1.Pod{}`), and it forces `status.runtimeRef` generalization.
- **Reuses the data-plane wiring unchanged.** Because it is still a co-located Pod, the shared pod-template builder injects the same enforcement sidecars + projected reporter token, so runtime evidence stays `self-reported` with **no assurance regression** (same trust posture as the Job backend).
- **Honest product value.** A bare Pod is a legitimate runtime for agents that want a simple lifecycle with no Job retry/TTL/backoff wrapper; it is not throwaway scaffolding.

### Job vs Pod backend (what differs behind the interface)

| Concern | `kubernetes-job` | `kubernetes-pod` |
|---------|------------------|------------------|
| Runtime object | `batchv1.Job` (wraps a Pod template) | `corev1.Pod` (the workload directly) |
| `observation.runtimeName` / `runtimeRef` | Job name, kind `Job` | Pod name, kind `Pod` |
| `observation.workloadName` (`status.podName`) | owned Pod discovered via Job UID | the Pod itself |
| Completion → `runtimePhase` | `Succeeded`/`Failed`+`BackoffExhausted`/`TimedOut`/`Active` | `Pod.Status.Phase` `Succeeded`/`Failed`; `DeadlineExceeded` → `TimedOut`; `Running` |
| Timeout | Job `activeDeadlineSeconds` | Pod `activeDeadlineSeconds` |
| Drift replace | delete+recreate pending Job (`ReplaceableForSync`) | delete+recreate pending Pod (Pods are immutable) |
| `ownedType()` (watch) | `&batchv1.Job{}` | `&corev1.Pod{}` |

The **governance pipeline does not change**: validate → resolve → approval gate → `backend.ensure` → `applyObservation`. Only the runtime mechanics behind `runtimeBackend` differ.

### Shared pod-template builder

`internal/controller/job.Build` currently constructs the agent container + Pod spec (sidecar injection, projected reporter token, `automountServiceAccountToken: false`, security context, workspace volumes) **inline** and wraps it in a Job. Slice 3 extracts that Pod-template construction into a shared, exported builder (e.g. `BuildPodTemplateSpec`) that **both** backends call, so the data-plane/governance wiring is identical and defined once. `Build` becomes "shared pod template + Job wrapper (`backoffLimit`/`ttl`/`activeDeadlineSeconds`)"; the Pod backend uses the shared template directly. This keeps the evidence/sidecar story uniform and avoids two divergent copies.

## Migration plan (slices)

1. **Design doc** (this doc). — *done*.
2. **Extract `runtimeBackend` + `kubernetes-job` implementation** — *done*. Reconciler routes `ensure`/`stop`/`runtimeGone`/`ownedType` through a `backendRegistry` keyed by `spec.runtime.orchestrator`; `kubernetesJobBackend` holds the Job mechanics. Behavior-preserving.
2b. **Normalize to `observation`** — *done*. Backend returns a neutral `observation`; reconciler's `applyObservation`/`applyRuntimePhase` own all status mapping. Behavior-preserving.
3. **Extract the shared pod-template builder** (`internal/controller/job`) — *done*. `job.BuildPodTemplateSpec` builds the agent Pod template; `Build` wraps it in the Job. Output unchanged; both backends reuse it.
4. **Generalize status to `status.runtimeRef`** — *done*. Additive `RuntimeRef` API field + `observation.runtimeRef`; `applyObservation` populates it; Job backend fills kind `Job` and keeps `jobName`/`podName` (`jobName` documented as a deprecated alias). (Resolves open question #1.)
5. **`kubernetes-pod` backend** — *done*. `kubernetesPodBackend` (`ensure`/`stop`/`runtimeGone`/`ownedType`) registered next to the Job backend; `validateSpec` + CRD enum accept `kubernetes-pod`.
6. **Pod backend correctness + unit tests** — *done*. `podRuntimePhase` (succeeded/failed/`DeadlineExceeded`→timed-out/running/pending), drift→replace (pending) vs surface-only (running), `runtimeGone`; table-driven + fake-client unit tests.
7. **Watch wiring** — *done*. `SetupWithManager` `Owns` every registered backend's `ownedType()` (Job **and** Pod, deduped); Pod `stop()` has Job-parity `blockOwnerDeletion` handling; envtest.
8. **Live e2e** — *done*. A `kubernetes-pod` session runs as a Pod (no Job) and reaches `Succeeded` with `status.runtimeRef` kind `Pod` (`test/e2e/pod_backend_test.go`).
9. **Docs + status alignment** — *done (this update)*. Design-doc statuses, `architecture.md` (`runtimeRef`, second backend), README orchestrator section, status tracker.
10. **External adapters (future, design per-adapter)** — **Tekton** (`PipelineRun`/`TaskRun`; pods still co-located → sidecars/reporter portable, `self-reported`), then **Argo Workflows**, then **Temporal / external worker** (no co-located pod → needs its own evidence channel + assurance declaration, open questions #3/#4). Each is its own design slice on top of the proven interface.

## Open questions

1. **Status field generality:** `status.jobName`/`status.podName` are orchestrator-specific. **Resolved (slice 4 — designed):** add a backend-neutral `status.runtimeRef` (`apiVersion`, `kind`, `name`, optional `uid`) carrying the runtime object's identity for **any** backend, and extend `observation` with a `runtimeRef` so `applyObservation` populates it generically. `status.jobName`/`status.podName` are **retained, additive, and back-compatible**: the `kubernetes-job` backend keeps setting `jobName` (= `runtimeRef.name`, kind `Job`) and `podName` (the workload Pod); the `kubernetes-pod` backend sets `runtimeRef` (kind `Pod`) and `podName`, leaving `jobName` empty. New code/UI should read `runtimeRef`; `jobName` is a deprecated alias (kept through MVP, removed no earlier than a future API revision). This is an additive optional field — no breaking change.
2. **Drift/replace semantics:** `PolicyEnvDrift`+`ReplaceableForSync` are Job-specific (delete+recreate pending Jobs). Each backend defines its own drift/replace policy behind `Ensure`; the reconciler only consumes `Observation.PolicyInSync`.
3. **Evidence channel for non-pod backends:** how an external-worker backend reports runtime evidence and at what assurance — needs its own design when that adapter is built.
4. **Sidecar portability:** enforcement sidecars assume a shared pod. Backends without co-located pods need a different enforcement story (gateway/eBPF/out-of-pod) — out of scope here.

## Related

- [`architecture.md`](architecture.md) — control/data-plane split, lifecycle, status merge.
- [`runtime-reporter-contract.md`](runtime-reporter-contract.md) — reporter/evidence channel + assurance levels.
- Product vision *Core Direction* (orchestrator-agnostic) and *Design Guidance* (don't rebuild schedulers/workflow engines).
