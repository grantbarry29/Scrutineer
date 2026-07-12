---
type: Design Doc
title: Enforcement Architecture
description: "Data-plane enforcement architecture: the internal/enforcement contract, NetworkPolicy baseline, runtime-evidence loop, and the out-of-pod Envoy egress path. The cooperative in-pod slices were removed (#71); their sections remain as condensed historical stubs."
status: implemented
read_when: "Data-plane enforcement, the internal/enforcement contract, or the runtime evidence loop."
---

# Enforcement Architecture

> **Note:** the cooperative in-pod enforcement slices this phase shipped (5–8 below) were **removed** ([`untamperable-enforcement.md`](untamperable-enforcement.md), #71); their sections are kept as condensed historical stubs. What survives is the enforcement contract (`internal/enforcement`), the NetworkPolicy baseline, the runtime-evidence loop, and the out-of-pod Envoy egress path.

This design moves Scrutineer from policy declaration and propagation to data-plane enforcement. The goal is not to turn Scrutineer into an orchestrator or agent framework. The controller should keep declaring desired governance state; enforcement backends should observe, enforce, and report evidence.

## Goals

- Enforce selected network and tool policies for `AgentSession` runtimes.
- Preserve control-plane / data-plane separation.
- Keep Kubernetes Job reconciliation as the MVP adapter, without baking enforcement permanently into Jobs.
- Report runtime decisions and violations back to `AgentSession.status`.
- Keep each implementation slice small enough to verify with envtest, samples, or e2e.

## Non-Goals

- Do not build a workflow engine.
- Do not implement a full UI, audit warehouse, or SIEM sink in this scope.
- Do not implement every enforcement backend at once.
- Do not require Envoy, Cilium, gVisor, or a tool-execution chokepoint for the first slice.
- Do not make `AGENT_POLICY_*` env vars the enforcement boundary. They remain propagation hooks.

## Existing Control-Plane Inputs

The control plane already gives enforcement backends these inputs:

- `AgentSession.spec.policy` and `spec.policyRefs`
- `status.effectivePolicy`
- `status.matchedPolicies`
- `status.policyDecisions` with merge-time decisions
- `RuntimeProfile.spec.sidecars[]` as schema-only sidecar intent (renamed to `spec.enforcement[]` in #65)
- `RuntimeProfile.spec.pod.runtimeClassName` and `seccompProfile`
- Job env vars (`AGENT_POLICY_*`) for propagation and debugging

## Enforcement Backend Model

Use a narrow contract between the reconciler and data-plane components:

1. The controller resolves policy into `status.effectivePolicy`.
2. The controller renders backend-specific desired state into Kubernetes objects or pod template configuration.
3. Data-plane components enforce at runtime.
4. Data-plane components report decisions and violations through a bounded status update path.

Backends should be replaceable (every backend is out-of-pod):

- NetworkPolicy baseline for coarse CIDR/namespace egress + the routing lock.
- Per-session out-of-pod Envoy egress proxy for FQDN/L7 allow/deny (shipped).
- Future out-of-pod chokepoints: tools pod ([`tools-pod-chokepoint.md`](tools-pod-chokepoint.md)), arena workspace ([`arena-workspace.md`](arena-workspace.md)).
- Sandbox/runtime profile for kernel/process isolation.

## Policy Modes

Enforcement must define how modes affect runtime decisions:

| Mode | Runtime behavior |
|------|------------------|
| `audit-only` | Allow action, record audit decision/violation evidence when relevant |
| `dry-run` | Allow action, record what would have been denied |
| `enforced` | Deny action when policy says deny, record runtime decision/violation |

Mode handling must be backend-neutral. A network backend and a tool-execution chokepoint should use the same mode semantics.

## Runtime Reporting Contract

Runtime enforcement needs a safe append path for status:

- Preserve merge-time `status.policyDecisions`.
- Append runtime decisions with `phase: runtime`.
- Cap total decision count to avoid unbounded status growth.
- Populate `status.violations` for actual denied or would-deny activity.
- Never let an enforcement reporter wipe reconciler-owned status such as `phase`, `conditions`, `effectivePolicy`, or `matchedPolicies`.

Open design question: whether runtime reporters patch `AgentSession.status` directly, write a separate CRD, or emit Kubernetes Events first and let the controller aggregate. The first implementation should choose the smallest safe option.

## Suggested Implementation Slices

### Slice 1: Enforcement Backend Contract — done

Implemented in [`internal/enforcement/`](../../internal/enforcement/):

- `SessionContext` — normalized input from `AgentSession.status` + optional `RuntimeProfile`
- `Backend` — replaceable backend interface (`Kind`, `Capabilities`, `DesiredState`)
- `EvaluateRestrictive` / `ActionForMode` — shared `audit-only` / `dry-run` / `enforced` semantics
- `AppendRuntimeDecisions` — bounded runtime append helper (wired in slice 2)
- `RuntimeReport` — batch shape for data-plane evidence

Acceptance:

- Contract maps `AgentSession` + `effectivePolicy` + `RuntimeProfile` into backend desired state.
- Runtime decision append strategy is documented or stubbed.
- Unit tests cover mode action mapping.

### Slice 2: Runtime Policy Decision Append — done

Implemented in `internal/controller/agentsession/policy_decisions.go`:

- `ApplyPolicyStatus` — refresh merge-time decisions while re-appending prior `phase: runtime` entries
- `AppendRuntimePolicyDecisions` / `ApplyRuntimePolicyReport` — reporter entry points
- `patchStatus` — `mergeRuntimePolicyDecisionsInPlace` unions runtime evidence from stale/live snapshots

Acceptance:

- Merge-time decisions are preserved.
- Runtime entries are appended without exceeding max count.
- Status patch path cannot wipe controller-owned fields.

### Slice 3: NetworkPolicy Baseline — done

Implemented in `internal/enforcement/networkpolicy/` and `internal/controller/agentsession/networkpolicy.go`:

- `Build` renders egress NetworkPolicy for `allowedCIDRs` (allowlist) or `deniedCIDRs` (0.0.0.0/0 with except)
- DNS egress to all namespaces on port 53 (required for resolution; FQDN policy still needs slice 7)
- Applied only when effective policy mode is **enforced** and CIDR rules are present
- `allowedDomains` / `deniedDomains` are **not** enforced by NetworkPolicy
- Reconciler creates/updates/deletes owned `scrutineer-netpol-<session>` objects; removed on terminal phase

Acceptance:

- Owned by the `AgentSession`.
- Reconciled idempotently.
- Deleted by owner references/finalizer cleanup.
- Clearly documents FQDN limitations.

### Slice 4: Violation Reporting MVP — done

Implemented in `internal/enforcement/violations.go` and `internal/controller/agentsession/violations.go`:

- `AppendViolations` — bounded append (max 64) with truncation summary
- `ViolationFromDecision` / `ViolationsFromDecisions` — `deny` and `dry-run` decisions become violations; `audit` skipped
- `ApplyRuntimePolicyReport` — appends explicit violations and derives from runtime decisions (deduped)
- `patchStatus` — `mergeViolationsInPlace` from stale/live snapshots

Acceptance:

- `audit-only`, `dry-run`, and `enforced` outcomes produce consistent entries.
- Status list is bounded.
- README documents what is and is not populated.

### Slices 5–8: the cooperative in-pod tier — shipped, then removed (#71)

Slices 5–8 delivered in-pod sidecar injection, a tool-call evaluation contract, a
DNS/egress proxy prototype, and a file/workspace policy design. All of it was
cooperative — it shared the agent's pod and trust domain — and was **removed** in the
scope narrowing ([`untamperable-enforcement.md`](untamperable-enforcement.md) §5). The original
slice write-ups and their design docs live in git history (deleted in #74). Surviving
descendants: the mode semantics and evidence contract (unchanged) and the successor designs
([`tools-pod-chokepoint.md`](tools-pod-chokepoint.md), [`arena-workspace.md`](arena-workspace.md)).

## Runtime evidence loop (critical path)

Slices 1–8 shipped contracts, design docs, and in-process merge helpers, but **nothing running in-cluster produces or reports runtime evidence**. `status.policyDecisions`, `status.violations`, and `status.usage` are empty at runtime. The runtime evidence loop closes that gap and is a prerequisite for observability export — it is the critical path, not optional hardening.

Ordered slices (tracked in [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues)):

1. **Runtime reporter mechanism design** — `docs/design/runtime-reporter-contract.md`.
2. **Runtime reporter loop (impl)** — controller-owned PATCH callback populates status.
3. **Structured session events API** — durable, ordered `status.events[]` (the reporter's sink).
4. **First real producers** — shipped as cooperative in-pod images (removed in #71); the surviving producer is the **egress-reporter** in the out-of-pod egress-proxy pod.
5. **Live network violation population** — enforced NetworkPolicy blocks → violations.
6. **File/workspace policy implementation** — deferred to the arena design.

## Open Questions

- ~~Should runtime reporters patch `AgentSession.status` directly or write separate evidence CRDs?~~ **Decided:** controller-owned **PATCH callback** — data-plane producers report to a controller endpoint that PATCHes `AgentSession.status`; no new evidence CRD. Keeps status the single source of truth. Detailed contract: [`runtime-reporter-contract.md`](runtime-reporter-contract.md).
- ~~Should `RuntimeProfile.spec.sidecars[]` be enough for backend selection, or should this design introduce an `EnforcementProfile` / `ToolGateway` CRD first?~~ **Decided:** the list on `RuntimeProfile` is enough — its entry `type` selects the backend. Renamed `spec.sidecars[]` → `spec.enforcement[]` in #65 (it now covers out-of-pod backends like the Envoy egress proxy, not just in-pod sidecars); no separate `EnforcementProfile`/`ToolGateway` CRD was introduced.
- What is the minimal production-safe status cap for runtime decisions and violations?
- How should active Job drift be handled for enforcement backend changes: deny mutation, set drift condition, or require session restart?
- How much can NetworkPolicy cover before DNS/proxy enforcement is needed?

## Recommended Next Work

Slices 1–8 are complete. Pick the next slice from [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues).
