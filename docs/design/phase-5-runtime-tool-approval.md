# Phase 5 — Per-Tool Runtime Approval (mid-execution human gate)

> **Status:** **Complete (all slices 1–5 shipped).** Defines a **mid-execution** human-approval gate: a running agent's tool/MCP call is **held** until a human grants a scoped, time-bounded approval, then allowed (or denied). It reuses the `ApprovalRequest` CRD and the approver/`allOf`/audit machinery from [`phase-5-approval-workflows.md`](phase-5-approval-workflows.md) and extends the tool-gateway + reporter contracts. Verified end-to-end against a live kind cluster. Resolves open question #4 of the approval-workflows design.

## Purpose

Pre-execution gating ([`phase-5-approval-workflows.md`](phase-5-approval-workflows.md)) answers *"may this session run at all?"* before the Job starts. It cannot gate a **specific action** an agent decides to take mid-run — e.g. "the agent may run, but a human must approve *this particular* `deploy` / `kubectl apply` / `wire-transfer` tool call." That is the vision's *"approve one tool call … rather than a broad boolean."*

Today `policy.requireHumanApproval` lists action/tool types and the tool gateway already surfaces them as `ApprovalRequired` (`internal/enforcement/toolgateway/evaluate.go`), but it is **not a gate** — under restrictive modes it is treated as a would-deny, and the call is not actually held for a human. This design turns it into a real mid-execution gate.

## Relationship to existing work

| Surface | Reused / extended |
|---------|-------------------|
| `ApprovalRequest` CRD | **Reused**, with a runtime variant (per tool call, keyed by `requestID`) instead of 1-per-session. Same approver allowlist, `allOf`, expiry, and audit. |
| `ApprovalPolicy` CRD | **Reused** — matches the gated `action`/tool; supplies approvers, `expiresAfter` (decision deadline), `onTimeout`. |
| Controller approval gate (`internal/controller/agentsession/approval.go`) | **Generalized** to also reconcile runtime-scoped requests (decision → state → audit) without gating the *session* phase. |
| Tool gateway `EvaluateTool` | **Extended** — `requireHumanApproval` becomes a hold-and-ask outcome, ordered **after** hard denies (deniedTools / argument-rule deny) so an auto-denied call is never escalated to a human. |
| Reporter contract ([`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md)) | **Extended** with an approval request/lookup channel (the existing `/v1/report` is one-way; approval needs a response). |
| Argument constraints ([`phase-3-tool-argument-constraints.md`](phase-3-tool-argument-constraints.md)) | **Complementary** — argument rules decide *automatically* (allow/deny); runtime approval asks a *human* ("ask"). Both run after the name gate. |

## Threat model honesty (read this first)

The tool gateway is an **in-pod sidecar** that shares the agent's pod and ServiceAccount. So, exactly like all current tool/dns/fs enforcement, this gate is **cooperative**:

- It governs a *cooperative or merely prompt-injected* agent that routes tool calls through the gateway.
- A **fully compromised** agent can bypass the gateway entirely (call the tool directly), so mid-execution approval is **not** an adversarial-grade control on its own.
- Evidence that a call was *held* and *approved* is therefore **`self-reported`** (the gateway attests it). The human decision object (`ApprovalRequest`) is control-plane data, but the *enforcement that the call waited* is cooperative.

Adversarial-grade per-action approval requires out-of-pod interposition (egress/MCP proxy the agent cannot bypass, or kernel/eBPF), tracked separately. This doc does **not** claim more than cooperative assurance, and the controller MUST stamp runtime-approval decisions `assuranceLevel: self-reported` (never let the gateway self-attest higher).

## Non-goals

- Adversarial-grade enforcement / out-of-pod interposition (separate future work).
- A UI approval inbox (Phase 7) — this provides the data; CLI/`kubectl patch` is the MVP actuator.
- Credential issuance on approval (Phase 8 `CredentialProfile`).
- New policy *language* (CEL) — triggers stay the structured `requireHumanApproval` list + `ApprovalPolicy`.
- Changing pre-execution session gating semantics.

## Flow

```mermaid
sequenceDiagram
    participant A as Agent (in pod)
    participant G as Tool-gateway sidecar
    participant R as Reporter endpoint (controller)
    participant K as kube-apiserver
    participant H as Human approver

    A->>G: POST /v1/tools/invoke {tool: deploy, arguments}
    G->>G: name gate ok; not auto-denied; requireHumanApproval[deploy] => HOLD
    G->>R: POST /v1/approvals {session, requestId, action, target, argDigest}
    R->>K: create/lookup ApprovalRequest (owner=session, runtime scope)
    R-->>G: {approvalId, state: Pending}
    loop until decided or deadline
        G->>R: GET /v1/approvals/{approvalId}
        R-->>G: {state: Pending}
    end
    H->>K: patch ApprovalRequest spec.decision=granted, decidedBy
    K-->>R: (controller reconciles) state=Granted, expiresAt
    G->>R: GET /v1/approvals/{approvalId}
    R-->>G: {state: Granted, expiresAt}
    G-->>A: 200 (tool call allowed) — or 403 on Denied/Expired
```

1. The gateway evaluates a call; if it passes hard checks but the tool is in `requireHumanApproval`, it does **not** allow it. It registers an approval need with the controller and **holds** the agent's call.
2. The controller creates (idempotently, by `requestId`) a **runtime-scoped** `ApprovalRequest` owned by the session, then runs the *existing* gate machinery (approver allowlist, `allOf`, expiry, notification, audit) — but it gates the **call**, not the session phase.
3. The gateway learns the decision (poll/long-poll) and allows or blocks the specific call. Modes: `enforced` blocks until grant; `dry-run`/`audit-only` record "would require approval" and allow through (consistent with argument-constraint mode semantics).

## Control channel (reporter contract extension)

The existing `/v1/report` is fire-and-forget; approval needs a response, so add two endpoints to the same controller-owned reporter, with the **same** auth (TokenReview + pod→session ownership; gateway gets no Kubernetes RBAC):

```
POST /v1/approvals      # register/lookup an approval need (idempotent by requestId)
  body: { session:{namespace,name}, requestId, action, target, argDigest?, mode }
  -> 200 { approvalId, state }           # Pending|Granted|Denied|Expired

GET  /v1/approvals/{approvalId}          # poll current state
  -> 200 { state, expiresAt? }
```

- **Idempotent:** `POST` with the same `(session, requestId)` returns the same `approvalId` and never creates a duplicate `ApprovalRequest` (name derived from the tuple).
- **No raw arguments cross the wire** beyond what evidence needs: send an `argDigest` (sha256 over the canonicalized args) and the policy-defined `target`, never raw values (redaction invariant from argument constraints).
- **Abuse controls (NEW holds only):** creating a new hold is rate-limited per session (min interval, `429` + `Retry-After`) and capped at a max number of *undecided* runtime holds per session (`DefaultMaxOutstandingApprovals`, `429`). Re-registering an existing `requestId` (the gateway keepalive) and all `GET` polls are exempt, so a cooperating agent's long-poll loop is never throttled. This bounds `ApprovalRequest` object creation by a chatty or hostile agent. The handler lists runtime `ApprovalRequest`s to count outstanding holds (reporter RBAC: `approvalrequests: get;list;create`).
- **Blocking model (gateway side):** bounded **long-poll** — the gateway holds the agent's call up to e.g. 25s per attempt, polling the controller; on timeout it returns `202 {approvalId, retryAfter}` so a cooperating agent re-POSTs the *same* call (same `requestId`, deduped). This bounds held connections while keeping the common case a single synchronous call. (A pure long-hold or pure client-poll are the two extremes; the hybrid is the recommendation.)

## CRD reuse + minimal additions

Runtime approval is per tool call, not per session, so the current "one `ApprovalRequest` per session (name = session name)" rule is relaxed for the runtime variant:

- **Name:** derived from `(session, requestId)` (stable, idempotent), e.g. `<session>-rt-<short-hash>`.
- **New/clarified fields (proposal, finalize in impl):**
  - `spec.trigger: session | runtime` (default `session`) — distinguishes pre-exec gating from a held tool call. Controller skips session-phase gating for `runtime`.
  - `spec.requestId` — the gateway's correlation id (idempotency key).
  - `spec.scope.target` — already exists; carries the tool id (and optionally `tool@server`).
  - `spec.scope.argDigest` (new, optional) — redacted argument fingerprint for audit/scoping.
- **Unchanged:** `decision`/`decidedBy`, approver allowlist, `allOf` `status.approvedBy[]`, `expiresAt`, controller-sole-writer-of-status invariant, owner-ref GC.

This keeps a single CRD and one human actuation surface (`kubectl patch ... spec.decision`).

## Evaluation order in the gateway

`EvaluateTool` must escalate to a human only for calls that would otherwise be allowed:

```
deniedTools  ->  allowedTools (allowlist)  ->  argument-rule Deny / allowlist-miss  ->  requireHumanApproval (HOLD+ask)  ->  allow
```

(Today approval is checked *before* argument rules and is not a gate; this design reorders it after hard denies and makes it a real hold.)

## Audit

Each held call records, on grant/deny/timeout: a runtime `policyDecision` (`type: approval`, `phase: runtime`, `action: allow|deny`, `target: <tool>`, `rule: requireHumanApproval`, redacted `argDigest`), plus the existing `ApprovalRequest.status` (who/when/scope/expiry) and the OTLP `approval.granted`/`approval.denied` records. **Assurance:** the *decision* is controller-observed, but the *held-and-enforced* fact is `self-reported` (cooperative gateway) — the runtime `policyDecision` is stamped `self-reported`, matching the reporter contract. Pre-execution session approvals remain `controller`.

## Invariants

- A held call is **never** allowed before a non-expired matching grant (cooperative-strength core property); expiry re-checked at consume time (TOCTOU).
- The gateway holds calls only for `requireHumanApproval` matches that passed all hard checks; auto-denied calls are never escalated.
- The controller is the only writer of `ApprovalRequest.status`; the gateway has no Kubernetes RBAC and acts only through the reporter for its own session.
- No raw argument values cross the control channel or land in evidence (digest + policy target only).
- `enforced` blocks; `dry-run`/`audit-only` record-and-allow. Cancellation of the session terminates outstanding holds (their requests → `Expired`).
- Runtime approval decisions are `assuranceLevel: self-reported`; never self-attested higher.

## Migration plan (slices)

1. **This doc** (design). — **done**
2. **Controller: runtime `ApprovalRequest` variant** — `spec.trigger=runtime` + `requestId` + `scope.argDigest`; the reconciler resolves decision→state→audit for runtime requests **without touching session phase** (`reconcileRuntimeApprovals` in `internal/controller/agentsession/approval_runtime.go`), reusing the approver-allowlist/`allOf`/`onTimeout` machinery; expiry window is nil-policy-safe (policy `expiresAfter` → request `scope.window`); the human decision is mirrored to the audit sink. — **done (2026-06-21)**
3. **Reporter: approval channel** — `POST /v1/approvals` (idempotent create/lookup, keyed by `requestId` via `RuntimeApprovalName`) + `GET /v1/approvals/{id}?namespace=` (poll), reusing the reporter's TokenReview + pod→Job→session ownership; the handler only creates the runtime `ApprovalRequest` (owner-ref'd to the session for GC) and reports the controller-observed state — the controller stays the sole writer of `.status`. `internal/reporter/approvals.go`. — **done (2026-06-23)**
4. **Tool gateway: hold-and-ask** — `EvaluateTool` reordered so `requireHumanApproval` runs **after** hard denies (deniedTools / allowlist / argument-rule deny), turning a blocked approval-required outcome (enforced mode) into a hold; `handleApprovalHold` (`internal/enforcement/toolgateway/gateway.go`) registers via the reporter approval channel (`ReporterClient.RegisterApproval`/`GetApproval`), bounded long-polls (`ApprovalHoldTimeout`/`ApprovalPollInterval`, default 25s/1s), then allows (200) on grant, denies (403) on deny/expire, or returns `202 {approvalId}` + `Retry-After` while still pending (re-invoke is idempotent by `requestId`, derived from `tool|server|argDigest` when the agent omits one). Fails closed (403) when no channel is configured. Resolved holds emit self-reported runtime evidence via `ApprovalResolvedReport` (`type: approval`, `rule: requireHumanApproval`, redacted `argDigest`, never raw args). Audit/dry-run modes keep recording "would require approval" and allow through. — **done (2026-06-23)**
5. **Live e2e** — `test/e2e/tool_approval_test.go`: an enforced `requireHumanApproval` `deploy` call is held in a running pod; the gateway registers `ApprovalRequest <session>-rt-<digest>` via the reporter channel; granting it (set `spec.decision=granted`) releases the call without changing the session phase; `status.policyDecisions` then shows a `type:approval` `action:allow` `reason:ApprovalGranted` decision carrying only `argDigest=sha256:…` (the request arg value never leaks). Reuses the in-cluster reporter (its e2e ClusterRole gains `approvalrequests: get;create`) and the loaded tool-gateway/controller images. — **done (2026-06-23)**
6. **Observability surface: `status.pendingApprovals`** — `AgentSessionStatus.pendingApprovals[]` (new `RuntimeApprovalSummary`: `name`, `requestId`, `action`, `target`, `argDigest`, `state`, `policyRef`, `requestedAt`, `reason`) lists outstanding runtime holds awaiting a human, answering the operational "what needs approval now?" question for the future UI. `reconcileRuntimeApprovals` recomputes it each pass (sorted by name, capped at 64; empty clears the field) and `patchStatusWithEnforcement` clears it on terminal phases. Redaction-safe by construction — only `argDigest`, never raw args. Controller-owned and written through the reconciler's normal status patch (replaced each pass, not union-merged). — **done (2026-06-24)**

## Open questions

1. **Blocking model default** — ~~bounded long-poll (recommended) vs. pure client re-poll~~ **resolved (slice 3):** bounded long-poll with a configurable hold timeout (`ApprovalHoldTimeout`, default 25s) + `202`/`Retry-After` re-invoke.
2. **Per-argument scoping granularity** — approve the tool, or the tool+argDigest (so a re-call with different args re-asks)? Default: scope to tool+argDigest for write-ish tools; tool-only for read-ish. Make it an `ApprovalPolicy` knob if needed.
3. **Approval reuse window** — does one grant cover repeated identical calls within `expiresAfter`, or one call each? Default: reuse within window for the same `(tool, argDigest)`; revisit per threat model.
4. **Notification fan-out** — reuse the slice-4 `Notifier` so held calls page approvers; confirm rate-limiting for chatty agents.
5. **Adversarial upgrade path** — when out-of-pod interposition exists, the same `ApprovalRequest`/channel should carry `observed` assurance; keep the schema forward-compatible.

## Related

- [`phase-5-approval-workflows.md`](phase-5-approval-workflows.md) — pre-execution gating, CRDs, approver/`allOf`/audit machinery (resolves its open question #4).
- [`phase-3-tool-gateway-contract.md`](phase-3-tool-gateway-contract.md) — gateway request/auth model this extends.
- [`phase-3-tool-argument-constraints.md`](phase-3-tool-argument-constraints.md) — automatic per-call decisions (the "decide" counterpart to this "ask").
- [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) — reporter auth/ownership reused by the approval channel.
- Product vision *Policy And Enforcement Model* (scoped, auditable approvals) and *Trust And Threat Model* (cooperative vs adversarial integrity).
