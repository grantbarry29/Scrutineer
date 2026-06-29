# Phase 3 Enforcement Architecture

Scrutineer Phase 3 moves from policy declaration and propagation to data-plane enforcement. The goal is not to turn Scrutineer into an orchestrator or agent framework. The controller should keep declaring desired governance state; enforcement backends should observe, enforce, and report evidence.

## Goals

- Enforce selected network and tool policies for `AgentSession` runtimes.
- Preserve control-plane / data-plane separation.
- Keep Kubernetes Job reconciliation as the MVP adapter, without baking enforcement permanently into Jobs.
- Report runtime decisions and violations back to `AgentSession.status`.
- Keep each implementation slice small enough to verify with envtest, samples, or e2e.

## Non-Goals

- Do not build a workflow engine.
- Do not implement a full UI, audit warehouse, or SIEM sink in Phase 3.
- Do not implement every enforcement backend at once.
- Do not require Envoy, Cilium, gVisor, or a tool gateway for the first slice.
- Do not make `AGENT_POLICY_*` env vars the enforcement boundary. They remain propagation hooks.

## Existing Control-Plane Inputs

Phase 2 already gives enforcement backends these inputs:

- `AgentSession.spec.policy` and `spec.policyRefs`
- `status.effectivePolicy`
- `status.matchedPolicies`
- `status.policyDecisions` with merge-time decisions
- `RuntimeProfile.spec.sidecars[]` as schema-only sidecar intent
- `RuntimeProfile.spec.pod.runtimeClassName` and `seccompProfile`
- Job env vars (`AGENT_POLICY_*`) for propagation and debugging

## Enforcement Backend Model

Use a narrow contract between the reconciler and data-plane components:

1. The controller resolves policy into `status.effectivePolicy`.
2. The controller renders backend-specific desired state into Kubernetes objects or pod template configuration.
3. Data-plane components enforce at runtime.
4. Data-plane components report decisions and violations through a bounded status update path.

Backends should be replaceable:

- NetworkPolicy baseline for coarse CIDR/namespace egress.
- DNS or egress proxy sidecar for FQDN allow/deny.
- Envoy sidecar for richer L7 egress policy.
- Tool gateway for MCP/tool-call authorization and logging.
- Sandbox/runtime profile for kernel/process isolation.

## Policy Modes

Phase 3 must define how modes affect runtime decisions:

| Mode | Runtime behavior |
|------|------------------|
| `audit-only` | Allow action, record audit decision/violation evidence when relevant |
| `dry-run` | Allow action, record what would have been denied |
| `enforced` | Deny action when policy says deny, record runtime decision/violation |

Mode handling must be backend-neutral. A network backend and a tool gateway should use the same mode semantics.

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

Implemented in [`internal/enforcement/`](../internal/enforcement/):

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

### Slice 5: RuntimeProfile Sidecar Injection — done

Implemented in `internal/controller/job/sidecars.go`:

- Injects enabled `dns-proxy`, `tool-gateway`, and `envoy` sidecars from `RuntimeProfile.spec.sidecars[]`
- Skips disabled and unknown types; placeholder `busybox` images until data-plane images ship
- Sets `SCRUTINEER_TOOL_GATEWAY_URL` on the agent when tool-gateway is enabled
- `RuntimeProfileDrift` detects sidecar template changes

Acceptance:

- Only inject known enabled sidecar types.
- Pending Job replace behavior handles profile drift.
- No external proxy implementation required yet.

### Slice 6: Tool Gateway Contract — done

Implemented in `internal/enforcement/toolgateway/`; see [`phase-3-tool-gateway-contract.md`](phase-3-tool-gateway-contract.md).

- `ToolRequest` — tool identity and correlation metadata
- `EvaluateTool` — allow/deny using shared mode semantics
- `RuntimeReport` — decisions + violations for `ApplyRuntimePolicyReport`
- `GatewayConfig` + `Backend` — desired config for future sidecar injection

Acceptance:

- Document tool identity, request metadata, allow/deny result, and decision reporting.
- Do not require a production gateway implementation in this slice.

### Slice 7: DNS/Egress Proxy Prototype — done

Implemented in `internal/enforcement/dnsproxy/`; see [`phase-3-dns-proxy-prototype.md`](phase-3-dns-proxy-prototype.md).

- `EvaluateEgress` — domain + CIDR policy with mode semantics
- `BuildConfig` / `EnvForConfig` — sidecar env propagation from effective policy
- `RuntimeReportFromEvent` — decisions + violations via `ApplyRuntimePolicyReport`
- `ApplyEgressProxyRuntimeEvent` — controller entry point for sidecar reports
- Job builder sets `HTTP_PROXY` on agent when dns-proxy enabled

Acceptance:

- Uses effective domain/CIDR policy.
- Honors policy modes.
- Reports runtime decisions and violations.

### Slice 8: File/Workspace Policy Design — done

Design only. See [`phase-3-file-workspace-policy.md`](phase-3-file-workspace-policy.md).

- **Recommendation:** mount strategy + RuntimeProfile hardening as MVP; defer path-level `PolicyRules` and FS gateway.
- **Stubs:** `internal/enforcement/workspace/types.go` for future backend kinds.
- **Deferred:** FS proxy sidecar, `allowedPaths`/`deniedPaths` CRD fields, real file enforcement.

## Phase 3b — Runtime evidence loop (critical path)

Slices 1–8 shipped contracts, design docs, and in-process merge helpers, but **nothing running in-cluster produces or reports runtime evidence**. `status.policyDecisions`, `status.violations`, and `status.usage` are empty at runtime. Phase 3b closes that gap and is a prerequisite for Phase 4 observability — it is the critical path, not optional hardening.

Ordered slices (tracked in [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues)):

1. **Runtime reporter mechanism design** — `docs/design/phase-3-runtime-reporter-contract.md`.
2. **Runtime reporter loop (impl)** — controller-owned PATCH callback populates status.
3. **Structured session events API** — durable, ordered `status.events[]` (the reporter's sink).
4. **First-party dns-proxy image MVP** — first real producer.
5. **First-party tool-gateway image MVP** — second real producer.
6. **Live network violation population** — enforced NetworkPolicy blocks → violations.
7. **File/workspace policy implementation** — deferred from slice 8.

## Open Questions

- ~~Should runtime reporters patch `AgentSession.status` directly or write separate evidence CRDs?~~ **Decided:** controller-owned **PATCH callback** — sidecars report to a controller endpoint that PATCHes `AgentSession.status`; no new evidence CRD. Keeps status the single source of truth. Detailed contract: [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) (Phase 3b slice 1).
- Should `RuntimeProfile.spec.sidecars[]` be enough for backend selection, or should Phase 3 introduce an `EnforcementProfile` / `ToolGateway` CRD first?
- What is the minimal production-safe status cap for runtime decisions and violations?
- How should active Job drift be handled for enforcement backend changes: deny mutation, set drift condition, or require session restart?
- How much can NetworkPolicy cover before DNS/proxy enforcement is needed?

## Recommended Next Work

Phase 3 slices 1–8 are complete. Pick the next slice from [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues).
