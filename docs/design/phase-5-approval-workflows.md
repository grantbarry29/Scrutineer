# Phase 5 — Human Approval Workflows

> **Status:** Design (Phase 5 · slice 1). No code yet. Defines the CRD shapes, controller gate/resume state machine, and invariants for scoped, auditable approvals. Implementation lands in later slices (tracked in [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues)).

## Purpose

Today `policy.requireHumanApproval` is **declared and propagated but not enforced**: the controller only emits an `ApprovalNotEnforced` warning event and runs the session anyway. Phase 5 turns approval into a **real, scoped, auditable gate** — a session that requires approval does not execute until a human grants a matching, time-bounded approval, and every decision is recorded for audit.

This aligns with the product vision (*Policy And Enforcement Model*): "Human approval should become scoped and auditable: approve one tool call, domain, file write, deployment, credential use, or bounded time window rather than a broad boolean."

## Scope of this design

- `ApprovalPolicy` CRD — **what** requires approval (reusable, declarative).
- `ApprovalRequest` CRD — **per-pending-decision** object a human acts on.
- Controller **gate/resume** state machine on `AgentSession` (new `PhaseAwaitingApproval` + `ApprovalRequired` condition).
- Audit surface: `status.policyDecisions` (`type: approval`) + Kubernetes events.

## Non-goals

- Mid-execution, per-tool-call runtime approval (agent pauses on a specific tool invocation). This design covers **pre-execution session gating** first; runtime per-action approval is a later slice that reuses `ApprovalRequest`.
- External integrations (Slack/PagerDuty) — separate slice (notification hooks).
- A UI approval inbox — Phase 7.
- Credential issuance on approval — Phase 8 (`CredentialProfile`).
- Changing the existing policy merge model; `ApprovalPolicy` is referenced like other policies.

## Relationship to existing model

| Existing | Phase 5 change |
|----------|----------------|
| `policy.requireHumanApproval: []string` (action types) | Kept as the **trigger signal**. When non-empty (after merge), the session needs approval. `ApprovalPolicy` refines *how* it is gated (approvers, expiry, scope granularity). |
| `EventReasonApprovalNotEnforced` warning | Replaced by real gating once an `ApprovalPolicy` applies; warning retained only when approval is declared but **no** `ApprovalPolicy`/gate is configured (explicit opt-out / audit-only). |
| `status.policyDecisions` `type: approval` (currently `audit`) | Gains runtime `allow`/`deny` entries when a request is granted/denied. |

## CRD sketches

> Field names are a design proposal; finalize during slice 2. Namespace-scoped, same-namespace references only (matches existing ref scoping).

### `ApprovalPolicy` (declarative — slice 2 — **shipped 2026-06-21**)

Shipped in `api/v1alpha1/approvalpolicy_types.go` (CRD `approvalpolicies`, short names `appol`/`approvalpol`). `requirement` (`default`/`allOf`, default `default`) and `onTimeout` (`deny`/`allow`, default `deny` — fail closed) are enum-validated with defaults; `actions` is required (`minItems: 1`); `approvers[].kind` is an enum (`User`/`Group`/`ServiceAccount`); `expiresAfter` is a Go duration string. No controller behavior yet (slice 3). Sample: `config/samples/scrutineer_v1alpha1_approvalpolicy.yaml`.

```yaml
apiVersion: scrutineer.sh/v1alpha1
kind: ApprovalPolicy
metadata:
  name: prod-deploys
  namespace: team-a
spec:
  # Which action types this policy gates. Matches against the action types
  # surfaced by requireHumanApproval / runtime decisions.
  actions: ["deploy", "credential-use"]
  # Who may approve. MVP: subjects checked at decision time (see open questions).
  approvers:
    - kind: Group
      name: platform-oncall
  # How long a granted approval stays valid before the session must re-request.
  expiresAfter: 1h
  # default | allOf — number of distinct approvers required.
  requirement: default        # default = 1 approver
  # What happens if no approval arrives before the session/approval deadline.
  onTimeout: deny             # deny | allow (audit-only escape hatch)
```

`status`: `observedGeneration` (reserved), later usage counters.

### `ApprovalRequest` (per-decision object — slice 3 — **shipped 2026-06-21**)

Shipped in `api/v1alpha1/approvalrequest_types.go` (CRD `approvalrequests`, short names `appreq`/`approvalreq`). Created **by the controller** (owner-referenced by the `AgentSession`) when a gated session needs approval. Humans act on it by patching `spec.decision` (RBAC-scoped) or via a future UI/CLI. `spec.decision` is enum `""`/`granted`/`denied`; `status` (controller-owned) carries `state`/`decidedBy`/`decidedAt`/`expiresAt`/`reason`. One request per session (name = session name, 1:1 for MVP).

```yaml
apiVersion: scrutineer.sh/v1alpha1
kind: ApprovalRequest
metadata:
  name: my-session-deploy        # derived, stable per (session, scope)
  namespace: team-a
  ownerReferences: [{ kind: AgentSession, name: my-session, ... }]
spec:
  sessionRef: { name: my-session }
  policyRef:  { name: prod-deploys }
  action: deploy                  # the gated action type
  scope:                          # what is being approved (bounded)
    target: "cluster/prod"        # optional: domain/tool/path/deploy target
    window: 1h                    # bounded validity once granted
  # Human-set decision (the only mutable part for approvers):
  decision: ""                    # "" (pending) | granted | denied
status:
  state: Pending                  # Pending | Granted | Denied | Expired
  decidedBy: ""                   # subject who set the decision
  decidedAt: null
  expiresAt: null                 # set when granted (now + scope.window)
  reason: ""
```

**Invariants:**
- The controller is the only writer of `status`; approvers only set `spec.decision` (enforced by RBAC + optional future validating webhook).
- An `ApprovalRequest` is owned by exactly one `AgentSession`; deleting the session garbage-collects it.
- Idempotent: re-reconciling a pending session does not create duplicate requests for the same `(session, action, scope)` — name is derived from that tuple.

## Controller gate/resume state machine

New phase `PhaseAwaitingApproval` and condition `ApprovalRequired` (proposed constants).

Validation is synchronous within a reconcile and surfaced via the `Validated`
condition, not a distinct phase (the `Validating` phase was removed as dead state —
see issue #31), so `Pending` transitions directly to the post-validation outcome.

```mermaid
stateDiagram-v2
    [*] --> Pending
    Pending --> Denied: invalid spec/policy
    Pending --> AwaitingApproval: requireHumanApproval matched + ApprovalPolicy applies
    Pending --> Starting: no approval needed
    AwaitingApproval --> Starting: ApprovalRequest Granted (not expired)
    AwaitingApproval --> Denied: ApprovalRequest Denied or onTimeout=deny
    Starting --> Running
    Running --> Succeeded
    Running --> Failed
```

**Reconcile logic (slice 3 — implemented):** see `internal/controller/agentsession/approval.go` (`reconcileApprovalGate`), wired in `reconciler.go` between the terminal check and `ensureJob`.

> MVP semantics note: the gate is keyed on **one** `ApprovalRequest` per session (name = session name). `ApprovalPolicy.expiresAfter` is enforced as the **decision deadline** (measured from request creation): if no decision arrives before it, `onTimeout` applies (`deny` → `Denied`, `allow` → proceed). Grant-validity windows, multi-scope requests, and consume-time TOCTOU re-checks are deferred to later slices. Cancellation while `AwaitingApproval` is handled by the existing `cancelRequested` path (terminal `Cancelled`, no Job).

1. After validation + policy resolve, if effective `requireHumanApproval` is non-empty **and** a matching `ApprovalPolicy` applies:
   - Do **not** create the Job.
   - Ensure an `ApprovalRequest` exists (create if missing, owner-ref the session).
   - Set `phase = AwaitingApproval`, condition `ApprovalRequired = True`, emit `ApprovalRequested` event, append `policyDecisions{type: approval, action: audit, reason: ApprovalRequired}`.
   - Re-reconcile is triggered by a **watch on `ApprovalRequest`** (map → owning session).
2. When the `ApprovalRequest` reaches `Granted` and `now < expiresAt`:
   - Proceed to `Starting` (create Job as today); condition `ApprovalRequired = False` (reason `Approved`); append `policyDecisions{type: approval, action: allow, actor: <decidedBy>}`; emit `ApprovalGranted`.
3. When `Denied` or `Expired` (and `onTimeout: deny`):
   - Terminal `phase = Denied`; emit `ApprovalDenied`; append `policyDecisions{type: approval, action: deny}`.
4. **Cancellation** (`spec.cancelRequested`) while `AwaitingApproval`: terminal `Cancelled`, no Job, request marked `Expired`.

**Invariants:**
- A gated session **never** creates a Job before a non-expired grant exists (the core safety property).
- Approval evidence is control-plane authoritative → `policyDecisions` entries get `assuranceLevel: controller` (see evidence-integrity work).
- Idempotent and watch-driven (no busy-poll); reuses the existing status-patch union-merge.
- Expiry is enforced at consume time (re-check `now < expiresAt` immediately before Job creation) to avoid TOCTOU on a stale grant.

## Audit trail

Every transition records **who** (`decidedBy`), **when** (`decidedAt`), **scope** (`action` + `scope.target`/`window`), and **expiry** — on the `ApprovalRequest.status`, mirrored into `AgentSession.status.policyDecisions`, Kubernetes events, and (since 2026-06-21) the **OTLP audit sink** as `approval.granted`/`approval.denied` records (actor = approver or joined `allOf` set; target = gated action). This answers the vision's audit questions: who authorized the run and under what bounded scope. See [`phase-4-observability-export.md`](phase-4-observability-export.md) for the audit record catalog.

## Open questions (resolve in slice 2/3)

1. **Approver authn/authz:** MVP = Kubernetes RBAC on `patch ApprovalRequest spec.decision` (only authorized subjects can grant). Slice 5 (2026-06-21) added best-effort identity: approvers self-declare `spec.decidedBy`. **Resolved (slice 8, shipped 2026-06-24):** an opt-in **mutating admission webhook** (`internal/webhook/v1alpha1/approvalrequest_webhook.go`, enabled by `--enable-webhooks`) overwrites `spec.decidedBy` with the apiserver-authenticated `req.UserInfo.Username` whenever a decision is asserted/changed, so the recorded approver identity is **non-spoofable** and the approver-allowlist/`allOf` checks act on a trustworthy subject. It is a no-op for the controller's own writes (decision stays empty; status is separate). When the webhook is **not** deployed, behavior is unchanged (self-declared `decidedBy`, RBAC the only gate) — the webhook is the path to authenticated identity, not a hard dependency. `failurePolicy: Fail` (fail-closed: identity capture cannot be bypassed by DoSing the webhook); requires cert-manager (`config/webhooks` overlay).
2. **`approvers` matching:** **resolved (slice 5):** enforced by the gate — a grant is honored only when `spec.decidedBy` matches a listed `ApprovalPolicy.approvers[].name` (match by name; Kind advisory). Empty `approvers` ⇒ any grant accepted (RBAC is the gate). An unlisted grant keeps the session `AwaitingApproval` and emits `ApprovalUnauthorized`.
3. **Multiple required approvers (`allOf`)** — **resolved (slice 6, shipped 2026-06-21):** when `requirement: allOf` and `approvers` is non-empty, the gate accumulates each valid grant's `spec.decidedBy` into controller-owned `ApprovalRequest.status.approvedBy[]` and only opens once that set covers every listed approver; until then the session stays `AwaitingApproval` and emits `ApprovalPartiallyApproved`. The approval `policyDecision` actor is the joined approver set. **Fail-closed limitation:** approvers grant sequentially through the single `spec.decidedBy` field, so two grants coalesced into one reconcile record only the latest grantor — the missed approver simply re-submits; the gate never opens early. An `allOf` policy with no listed approvers degenerates to single-approver. With the slice-8 identity webhook enabled, each accumulated grantor is now an **authenticated** subject (so `allOf` coverage cannot be forged by self-declaring multiple names across re-submits). A list-typed multi-grant spec (concurrent grants in one write) remains future work.
4. **Per-tool runtime approval** — **designed (2026-06-21):** see [`phase-5-runtime-tool-approval.md`](phase-5-runtime-tool-approval.md). Reuses `ApprovalRequest` (runtime variant keyed by `requestId`) and the approver/`allOf`/audit machinery; the tool-gateway holds the specific call and the reporter gains an approval request/lookup channel. It ships as a **cooperative** gate (gateway shares the pod/SA), explicitly stamped `self-reported` — it does **not** wait for adversarial-grade enforcement, but is honest that it isn't one.

## Implementation slices (tracking)

Phase 5 shipped (slices 1–8); remaining loose ends are tracked as GitHub Issues
([#6](https://github.com/grantbarry29/scrutineer/issues/6) webhook e2e,
[#7](https://github.com/grantbarry29/scrutineer/issues/7) concurrent multi-grant):

1. **This doc** (design). — **done**
2. `ApprovalPolicy` CRD (declarative only). — **done (2026-06-21)**
3. `ApprovalRequest` CRD + controller gate/resume + `PhaseAwaitingApproval`. — **done (2026-06-21)**
4. Notification hooks (generic webhook → Slack/PagerDuty adapters). — **done (2026-06-21)** — `internal/approval` `Notifier` (noop + webhook); reconciler fires once on gate open (annotation-guarded, best-effort, retried); `--approval-webhook-url` flag. Slack/PagerDuty are future adapters over `Notifier`.
5. Approver allowlist (best-effort `decidedBy`). — **done (2026-06-21)** — see open questions #1/#2.
6. Multi-approver (`allOf`). — **done (2026-06-21)** — `status.approvedBy[]` accumulation; gate opens on full coverage; `ApprovalPartiallyApproved` event. See open question #3.
7. Approval-decision audit records. — **done (2026-06-21)** — `approval.granted`/`approval.denied` OTLP records on the gate (see Audit trail above + `phase-4-observability-export.md`).
8. Authenticated approver identity (mutating admission webhook). — **done (2026-06-24)** — `internal/webhook/v1alpha1/approvalrequest_webhook.go` stamps `spec.decidedBy` from the authenticated `req.UserInfo` on decision writes (opt-in via `--enable-webhooks`; cert-manager overlay `config/webhooks`); resolves open questions #1 and #3. **Remaining:** committed opt-in webhook-mode e2e (needs cert-manager in kind) — GitHub Issue [#6](https://github.com/grantbarry29/scrutineer/issues/6).

## Related

- [`architecture.md`](architecture.md) — control/data-plane split, lifecycle, status merge.
- [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) §5 — evidence assurance levels.
- Product vision *Policy And Enforcement Model* and *Trust And Threat Model*.
