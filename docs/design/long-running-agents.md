# Long-Running & App-Driven Agent Runtimes — Investigation

**Status:** open investigation — **not designed, not scheduled.** This doc frames a question raised by the runtime model; it is not an agreed direction and does not commit Scrutineer to supporting long-running agents.
**Scope:** whether (and how) Scrutineer should govern **long-running, app-driven agents** — agents that run as a service and receive work continuously (queue, webhook, API) rather than executing one declared task and exiting.
**Non-goals:** proposing a design. This is questions and options only.
**Tracking:** #94 (investigation tracker — answer its gating question before any design work).

---

## The signal that raised this

`spec.task` can be stubbed (`prompt: noop`) — the contract requires just one non-empty field among `description`/`prompt`/`promptConfigMapRef` (a reconcile-time check, `validateSpec`; the CRD schema itself is laxer until the #30 CEL work) — so an agent can source its real work elsewhere (baked into the image, `runtime.command`/`args`, or an external queue/API). If that **stub-task pattern turns out to be common in practice, that is evidence** that users want to govern agents whose work is *app-sourced and continuous*, not *declared-and-one-shot*. Worth watching for post-launch.

## Why today's model doesn't serve it

- **Run-to-completion semantics.** The `AgentSession` lifecycle (`Pending → Running → Succeeded/Failed`, terminal-sticky) assumes the agent *completes*. A service agent never "Succeeds." (Mechanically, an *indefinite* run is already expressible — see the gap analysis below — but nothing blesses long `Running` as a steady state, defines drain-on-cancel, or says what an agent-pod restart means for the session record.)
- **Audit fidelity, not audit growth.** Evidence does **not** grow unbounded — `status.policyDecisions` / `status.violations` are capped (64 each, `internal/enforcement/decisions.go`) with value-aware eviction (deny/dry-run outlive allow-floods) and an always-visible truncation marker. The real problem is what the cap *means* at long horizons: for a bounded task the status list is a **complete record**; for a week-long service it is a **rolling sample plus counters** — "if Scrutineer says it happened, it happened" quietly degrades. Disk-side, the egress access log no longer caps session lifetime — the ingested prefix rotates away ([`access-log-rotation.md`](access-log-rotation.md), #98), so disk is bounded by ingest lag, not lifetime.
- **Shape mismatch.** A continuously-serving agent is *Deployment*-shaped; Scrutineer models Job/Pod. There is no first-class "governed long-running agent" today — and the deeper blocker is not the backend seam but the **evidence-identity model** (see the gap analysis).

## Current-state gap analysis (verified against code, 2026-07-11)

What a long-running session would actually hit today, from reading the runtime surfaces — split into what already holds and what doesn't.

**Already long-running-compatible (more than this doc originally assumed):**

- **Indefinite sessions are expressible.** `spec.runtime.timeoutSeconds` is optional; nil sets no `ActiveDeadlineSeconds` (`internal/controller/job/builder.go`). A `kubernetes-pod` session simply stays `Running`.
- **A stop lever exists** — `spec.cancelRequested`.
- **Live policy propagation to running sessions exists and fails closed.** `reconcileEgressDrift` (`internal/controller/agentsession/egress_envoy.go`) pushes an `AgentPolicy` change into the running session's Envoy ConfigMap and recreates the proxy pod; during the swap the default-deny routing lock holds — a brief egress *outage*, never a bypass window. The recreate has an **evidence-loss window** (access-log entries not yet ingested from the old pod are discarded) — eliminating the recreate is #116 (filesystem-dynamic xDS hot reload), which is a shared brick with this investigation.
- **Runtime approvals** (`approval_runtime.go`) already work mid-run.
- **Proxy failure self-heals** (failed egress-proxy pods are replaced), and access-log rotation (#98) keeps disk bounded for arbitrarily long sessions.

**The gaps, in cost order:**

1. **Audit at scale — the one substantial engineering item.** The capped `status` lists become a rolling sample (see above). Fix = durable evidence export (S3/OTLP sink) with `status` holding a rolling window plus a pointer; #2 (external artifact/storage export) is the natural enabler and has standalone value regardless of this investigation's outcome.
2. **Lifecycle semantics** — mostly definition, not code: long `Running` as a legitimate steady state, drain-on-cancel, restart semantics for the session record (a Job-owned agent pod restarting changes pod name; reporter auth via Job ownership still holds).
3. **Per-unit intent** (open question 5): with app-sourced work, `spec.task` is a stub; the record captures observed *effects* without per-unit *intent*. Needs self-reported unit-boundary events, honestly labeled `self-reported`.
4. **Workspace** — `emptyDir` only (`internal/controller/job/builder.go`); long-lived state needs a PVC option or explicit externalization.
5. **Replicated / Deployment shape — the expensive tier.** The `RuntimeBackend` seam makes a Deployment backend *schedulable* trivially, but the evidence-identity model assumes **one agent pod per session**: one deterministic proxy pod name, one dedicated per-session ServiceAccount, and the reporter's 3-factor caller check (`internal/reporter/auth.go`). Replicas require per-replica chokepoints and identities — an identity-model redesign with its own design doc, not a backend plug-in.

**Cost tiers (for when the gating question is answered):**

- **Tier 0 — supported today, zero code:** decompose into one `AgentSession` per unit of app work. Arguably the *stronger* governance model, not a workaround: complete evidence per unit, real per-unit intent in each `spec.task`, bounded blast radius. This is the presumptive answer unless the gating question is ruled out.
- **Tier 1 — native single-replica long-`Running`, ~4–5 issues, gated on demand:** durable evidence export (#2, the no-regrets first brick), lifecycle semantics (gap 2), PVC workspace (gap 4), per-unit intent events (gap 3). Weeks of part-time work; none of it blocks launch, and #2 stands alone.
- **Tier 2 — replicated / *Deployment*-shaped, out of scope until pulled:** the per-replica evidence-identity redesign (gap 5). The "not an orchestrator" boundary holds until a real workload forces it.

## Open questions (for when/if this is scheduled)

1. **Is this even in scope?** The strongest counter-answer: Scrutineer stays "one governed task per session," and a long-running agent is **decomposed into many short sessions** (one per unit of app work). That preserves the audit model and needs *no new runtime* — making this a **docs/pattern** answer, not a feature. Rule this in or out first; it may end the investigation.
2. **Lifecycle:** what replaces `Succeeded` for a service — long-lived `Running` with checkpoints, a new phase?
3. **Runtime backend:** a `Deployment`/`StatefulSet` backend behind the existing `RuntimeBackend` interface, or out of scope?
4. **Evidence at scale:** bounded `status` lists don't fit a long-lived session — does evidence move to an external sink (OTLP/audit) with `status` holding only a rolling window?
5. **Intent in the audit trail:** with app-sourced work the session-level `task` is a stub, so how is *per-unit* intent captured (per-request events)? Same coin as the intent-vs-observed theme.

## Related

- **Intent-in-audit tension:** a stub `task` means Scrutineer records observed *effects* but not declared *intent* — fine for effects-only governance, weaker for audit. This investigation and that tension are the same issue seen from two sides.
- **`RuntimeBackend` interface** ([`phase-6-orchestrator-interface.md`](phase-6-orchestrator-interface.md)) is the seam any long-running backend would plug into.

## What would trigger scheduling

Real demand: users asking to govern long-running / service / app-driven agents rather than one-shot task runs. Absent that signal, the likely resolution is Open-question 1 — decompose into short sessions and document the pattern — which needs no new runtime and no code.
