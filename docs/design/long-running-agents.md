# Long-Running & App-Driven Agent Runtimes — Investigation

**Status:** open investigation — **not designed, not scheduled.** This doc frames a question raised by the runtime model; it is not an agreed direction and does not commit Scrutineer to supporting long-running agents.
**Scope:** whether (and how) Scrutineer should govern **long-running, app-driven agents** — agents that run as a service and receive work continuously (queue, webhook, API) rather than executing one declared task and exiting.
**Non-goals:** proposing a design. This is questions and options only.
**Tracking:** #94 (investigation tracker — answer its gating question before any design work).

---

## The signal that raised this

`spec.task` can be stubbed (`prompt: noop`) — the contract requires just one non-empty field among `description`/`prompt`/`promptConfigMapRef` (a reconcile-time check, `validateSpec`; the CRD schema itself is laxer until the #30 CEL work) — so an agent can source its real work elsewhere (baked into the image, `runtime.command`/`args`, or an external queue/API). If that **stub-task pattern turns out to be common in practice, that is evidence** that users want to govern agents whose work is *app-sourced and continuous*, not *declared-and-one-shot*. Worth watching for post-launch.

## Why today's model doesn't serve it

- **Run-to-completion.** The `kubernetes-job` / `kubernetes-pod` backends are one-shot; the `AgentSession` lifecycle (`Pending → Running → Succeeded/Failed`, terminal-sticky) assumes the agent *completes*. A service agent never "Succeeds."
- **Session-scoped and ephemeral.** Per-session Envoy pod, ephemeral workspace, and bounded `status`/evidence lists all assume a short, bounded run. A long-lived session breaks those assumptions (evidence grows unbounded; "one session = one task" stops holding). One of these is already solved: the egress access log no longer caps session lifetime — the ingested prefix rotates away ([`access-log-rotation.md`](access-log-rotation.md), #98), so disk is bounded by ingest lag, not lifetime.
- **Shape mismatch.** A continuously-serving agent is *Deployment*-shaped; Scrutineer models Job/Pod. There is no first-class "governed long-running agent" today.

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
