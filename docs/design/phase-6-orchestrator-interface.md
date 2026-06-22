# Phase 6 — Orchestrator Backend Interface

> **Status:** Slice 1 (design) + slice 2 (extraction) shipped. The AgentSession reconciler now calls Kubernetes Jobs only through a `runtimeBackend` interface selected from a registry keyed by `spec.runtime.orchestrator` (`internal/controller/agentsession/runtime_backend.go`). This decouples the controller from Jobs so other execution backends (Tekton, Argo, Temporal, external workers) can be governed without rewriting governance logic. Slice 2 is transitional — the backend still mutates session status directly; slice 2b normalizes to `Observation` (see migration plan).

## Purpose

The product vision is explicit: *"Keep Relay orchestrator-agnostic. Avoid coupling APIs or controllers permanently to Kubernetes Jobs"* and *"Treat Kubernetes Jobs, Tekton, Argo Workflows, Temporal … as execution backends Relay can govern, not systems Relay should replace."* Today the reconciler calls `batchv1.Job` directly in several places. This doc catalogs that coupling and proposes a narrow interface so the **governance logic** (validation, policy resolution, approval gate, evidence loop, status/conditions, audit) stays backend-independent while **runtime mechanics** (create/observe/stop) move behind a pluggable backend.

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
    Session *relayv1alpha1.AgentSession
    Task    *ResolvedTask
    Policy  *policy.Resolved
    Profile *relayv1alpha1.RuntimeProfile
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

Enforcement sidecars and the reporter token are injected by `internal/controller/job` into the **Pod template** — this is Kubernetes-Pod-specific. The `kubernetes-job` backend keeps that wiring. Backends without a co-located pod (e.g. an external Temporal worker) cannot inject sidecars the same way, which directly affects **evidence assurance**: such backends start at best `self-reported` over a different channel, or have no runtime evidence at all until an `observed` source exists. The interface deliberately does **not** promise sidecar parity across backends; the reporter contract (`phase-3-runtime-reporter-contract.md`) and assurance levels (`controller`/`self-reported`/`observed`) already model this honestly. New backends must declare their evidence channel and assurance explicitly.

## Invariants

- Governance order is fixed and backend-independent: validate → resolve → **approval gate** → runtime. A backend is only ever invoked for an approved, non-terminal session.
- `Ensure`/`Stop`/`Observe` are idempotent; the reconciler may call them every pass.
- The reconciler — not the backend — owns AgentSession status, conditions, events, and audit. Backends return normalized `Observation` only. *(Target invariant; temporarily relaxed in slice 2 where `kubernetesJobBackend` mutates status directly — restored in slice 2b.)*
- Ownership/GC stays owner-reference based where the backend uses `Owns()`; `blockOwnerDeletion=false` semantics (so the session can finalize) are preserved.
- No backend may weaken the evidence-integrity story silently; assurance level must reflect the backend's real channel.

## Migration plan (slices)

1. **This doc** (design). — *done*
2. **Extract `runtimeBackend` + `kubernetes-job` implementation** — *done*. The reconciler now routes runtime calls (`ensure`/`stop`/`runtimeGone`/`ownedType`) through a `backendRegistry` keyed by `spec.runtime.orchestrator`; the `kubernetesJobBackend` (in `internal/controller/agentsession/runtime_backend.go`) holds the moved `ensureJob`/`syncStatusFromJob`/`findPodName`/Job-stop/Job-observe logic. Behavior-preserving; all existing envtests green. **Transitional deviation from the target above:** the backend still mutates AgentSession status/conditions/events directly rather than returning a normalized `Observation` that the reconciler maps — see follow-up in slice 2b. The interface signatures (`ensure(session, …) error`) reflect this; `Observe`/`EnsureInput`/`Observation` from the target shape are deferred.
3. **Slice 2b — normalize to `Observation`** — refactor `kubernetesJobBackend` to return normalized runtime state (`Phase`/`RuntimeName`/`WorkloadName`/`PolicyInSync`) and move phase/condition/event/result mapping back into the reconciler so the reconciler — not the backend — owns AgentSession status (restores the invariant above). Prerequisite for a clean second backend.
4. **Generalize Job/Pod-specific status fields** (open question below) only if a second backend needs it.
4+. **Adapters** — Tekton, then Argo/Temporal, each its own slice with its own evidence/assurance declaration.

## Open questions

1. **Status field generality:** `status.jobName`/`status.podName` are orchestrator-specific. Keep as-is (populated only by `kubernetes-job`) or add a neutral `status.runtimeRef`? Defer until a second backend exists (API change).
2. **Drift/replace semantics:** `PolicyEnvDrift`+`ReplaceableForSync` are Job-specific (delete+recreate pending Jobs). Each backend defines its own drift/replace policy behind `Ensure`; the reconciler only consumes `Observation.PolicyInSync`.
3. **Evidence channel for non-pod backends:** how an external-worker backend reports runtime evidence and at what assurance — needs its own design when that adapter is built.
4. **Sidecar portability:** enforcement sidecars assume a shared pod. Backends without co-located pods need a different enforcement story (gateway/eBPF/out-of-pod) — out of scope here.

## Related

- [`architecture.md`](architecture.md) — control/data-plane split, lifecycle, status merge.
- [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) — reporter/evidence channel + assurance levels.
- Product vision *Core Direction* (orchestrator-agnostic) and *Design Guidance* (don't rebuild schedulers/workflow engines).
