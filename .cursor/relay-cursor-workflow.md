# Relay Cursor Workflow

> Rules and templates for **Cursor-assisted implementation** on Relay.
> **Task state lives in GitHub Issues / Projects** — see [`.cursor/rules/task-management.mdc`](rules/task-management.mdc).
> Durable technical context lives in [`docs/design/`](../docs/design/), component `README.md`s, and code comments. There is no markdown task tracker.

Companion always-on rules: [`.cursor/rules/task-management.mdc`](rules/task-management.mdc) (issue/board protocol) and [`.cursor/rules/relay-product-vision.mdc`](rules/relay-product-vision.mdc) (product direction).

---

## Document map

| File | Audience | Contents |
|------|----------|----------|
| **GitHub Issues / Projects** | Humans + agents | **Live task state** — backlog/ready/in-progress/blocked/review/done, ownership, priority, blockers |
| **`rules/task-management.mdc`** | Agents | Label model + start/during/done issue protocol |
| **`docs/design/` + component READMEs + code comments** | Humans + agents | Durable technical context — canonical architecture & per-phase design, semantics, invariants, trust posture (read during planning; see `rules/relay-design-docs.mdc`) |
| **`relay-cursor-workflow.md`** (this file) | Primarily agents | Implementation contract, scope rules, task templates |
| **`rules/relay-product-vision.mdc`** | Agents | Product vision, MVP boundaries, long-term direction |

---

## Cursor Implementation Contract

When asked to implement a Relay task, Cursor must:

1. Read `relay-product-vision.mdc`, `task-management.mdc`, and this file when implementing. Pull **durable technical context** from the relevant [`docs/design/`](../docs/design/) doc, component READMEs, and code comments.
2. Identify the **exact selected task** — a **GitHub Issue** (`status/ready` + `agent-ready`, per `task-management.mdc`) or the user prompt. If unclear, ask or propose a short list. Do not pick multiple roadmap phases automatically. Claim the issue (assign / `agent-in-progress`) before editing code.
3. Before editing code, provide a short plan: selected task, acceptance criterion, expected files, verification command, non-goals. **During planning, read the relevant design doc(s) in [`docs/design/`](../docs/design/)** — start with `architecture.md`, then the phase/area doc that matches the task (see `relay-design-docs.mdc`). Follow their invariants and non-goals. Do not load all design docs at once; read the one(s) you need.
4. Implement **only** that task. Do not add adjacent roadmap items unless explicitly requested.
5. Keep changes reviewable. Prefer **1–4 non-generated files**; if more are needed, stop and explain.
6. **Explain as you go** (see below).
7. After implementation, summarize changes + tests run, and **update the GitHub Issue** (state, summary, files, tests). Update durable docs (`docs/design/`, component READMEs, code comments) **only** when technical context changed — never keep task state in markdown.
8. End every implementation summary with **### Out-of-scope future work noticed** (see below).
9. If follow-up work is discovered, follow **Out-of-Scope Future Work Handling**; create a **GitHub Issue**; do not implement unless asked.
10. If architecture is ambiguous, stop, offer 2–3 options, recommend one, wait for confirmation.
11. Preserve Kubernetes controller discipline: idempotent reconciliation, owner references, status subresources, conditions, events, least-privilege RBAC.
12. When the task is finished, run the **End-of-Task Handoff Protocol** below (commit, then offer selectable next-task options).

---

## End-of-Task Handoff Protocol

Whenever you finish a task (work is complete, verification passed), do all four of the following, in order:

1. **Sync the board first.** Update the GitHub Issue per `task-management.mdc` → *Before marking an issue complete*: comment the summary/files/tests, set `status/done`, and **close** it (`state_reason: completed`). Make sure any work discovered this session is already filed as its own issue. The board must reflect reality before you move on.
2. **Commit it.** Create a git commit with a short but aptly descriptive message summarizing the change (imperative mood, focused on the "what/why"). Only commit when the work is complete and verified (tests pass or the user accepted incomplete work); follow the git safety rules. **Do not push** unless the user explicitly asks.
3. **Offer next-task options.** Directly present the user with **2–4** candidate next tasks, drawn from open **GitHub Issues** (`status/ready` + `agent-ready`, ordered by `task-management.mdc` → *Work precedence* + `priority/*`). **Include a clear recommendation** (mark it and say why in one line).
4. **Make them selectable.** Put those options in the Cursor selectable option box (the `AskQuestion` tool / option card), not just inline prose, so the user can pick one in one click. Put the recommended option first and label it `(Recommended)`.

Keep it to 2–4 options. If there is genuinely only one sensible next step, still offer it plus an "other / you decide" choice. This protocol does not replace the **### Out-of-scope future work noticed** summary requirement — do both.

---

## How To Work On Roadmap Items

Roadmap phases (tracked as GitHub epics, e.g. Phase 6/7/8) are **capabilities**, not one PR or one prompt. Decompose into narrow **GitHub Issues**, each with one acceptance criterion and one verification command.

**Avoid prompts like:**

> Implement session cancellation, finalizers, CI, AgentPolicy, and NetworkPolicy baseline.

**Prefer a sequence like:**

1. Add `spec.cancelRequested` and kubebuilder markers only → `make manifests && make test`
2. Reconciler: detect cancel and delete owned Job → `make test`
3. Set `status.phase=Cancelled` and conditions → `make test`
4. E2e spec for cancellation → `make test-e2e`
5. Finalizer: attach and Job cleanup on delete → `make test`
6. GitHub Actions: `make test`
7. Separate workflow: kind + `make test-e2e`

Same pattern elsewhere: API shape → reconciler → tests → CI. Never multiple phases at once unless the user requests a design pass.

---

## Out-of-Scope Future Work Handling

**No lost work rule (mandatory):** Anything noticed during implementation that is not in the current task scope **must** be recorded as a **GitHub Issue** before the session ends. Chat summaries or one-line “suggested next picks” are **not** sufficient — they do not prevent holes in the project.

When implementing any task, distinguish:

1. **Current task requirements** — required for acceptance criteria.
2. **Necessary supporting changes** — compile, test, or consistency for the selected task only.
3. **Future requirements** — related but not required now.

**Do not implement future requirements** unless the user explicitly asks.

Instead, for **every** future requirement:

1. Notice it (gap, follow-up, adjacent phase, TODO in code, “we should later…”, enforcement hook, doc debt, test gap).
2. Search **GitHub Issues** for an existing tracker (and the open epics for roadmap context).
3. **If already tracked** — note the issue number in the end summary.
4. **If not tracked** — create a **GitHub Issue** using the **Task Execution Template** below for the body; label it `agent-discovered` (+ status/type/priority/area); link it from the current issue.
5. **In the same session** — create the issue; do not defer “I’ll add it later.”
6. Continue with only the original task.

### What counts as “tracked”

| Tracking | Sufficient? |
|----------|-------------|
| **GitHub Issue** with body (Summary, Context, Acceptance, Files, Verification) + labels | Yes |
| Epic issue covering a coarse capability with child issues to follow | Yes, if no immediate slice exists |
| “Next suggested queue picks” one-liner only | **No** — create the issue |
| Mention only in chat / PR comment | **No** |
| “We’ll do it later” without creating an issue | **No** |

If multiple small items belong together, one issue may cover them; if they are independent, use separate issues so nothing is bundled into oblivion.

### End-of-task checklist (agents)

Before marking work complete:

- [ ] Every out-of-scope item from this session has **Already tracked: yes** with an issue number, **or** a new **GitHub Issue** was created this session.
- [ ] User-facing summary includes **### Out-of-scope future work noticed** (see below).
- [ ] Completed work reflected in the **GitHub Issue** (state → `status/done`); durable docs updated only if technical context changed.

### End-of-task summary requirement

After every implementation task, include:

### Out-of-scope future work noticed

- `None.` — if nothing relevant.

Or one bullet per item:

- **Description** — what was noticed
- **Already tracked:** yes → issue number; **no** → confirm a **GitHub Issue** was created this session (number/title)

### Examples

| While implementing… | Do not… | Instead… |
|---------------------|---------|----------|
| `status.podName` | Redesign Job retry/backoff | Create an issue if not tracked |
| Cancellation | Add finalizers | Reference the finalizer issue |
| Policy env vars | Add Envoy/Cilium/NetworkPolicy | Reference the Phase 3 / FQDN issue |
| Events | Build audit backend / UI | Reference the Phase 4 / UI epic |
| `promptConfigMapRef` | Cross-namespace refs | Create a reference-scoping issue |
| ServiceAccount fields | Redesign identity | Reference Phase 8 / CredentialProfile issues |

---

## Repository State Scan Rule

Scan the repo against **GitHub Issues** and the durable claims in `docs/design/` / READMEs when:

- the user asks what to work on next or to tighten rules
- a task is completed
- a new subsystem is introduced
- the issues or design docs may be stale

Compare: CRD/API fields, controller behavior, tests, samples, README, Makefile, CI, RBAC, generated manifests, TODO comments, issue claims.

Look for mismatches (an issue says done but code does not; API without controller behavior; missing tests; stale samples; etc.).

**During a scan, do not implement fixes unless asked.** File **GitHub Issues** for newly found gaps (and fix `docs/design/` / READMEs only where their durable technical claims are wrong).

Promote issues to `status/ready` (and `agent-ready`) when appropriate.

---

## Task Sizing Rules

- A good task usually touches **1–4 files** (plus generated CRD YAML when API markers change).
- Every task needs a **clear, testable** acceptance criterion.
- One **primary verification command** per task (`make test`, `make test-e2e`, etc.).
- Avoid spanning multiple roadmap phases in one task.
- Avoid new architecture (CRDs, sidecars, enforcement) unless the user asks for design first.
- Multi-subsystem work: propose a plan and wait for confirmation.
- **Runtime-evidence slices** (anything that populates `status.policyDecisions`, `status.violations`, `status.usage`, or `status.events`): must include a test that drives the path with a **simulated/fake report** — do not require a real sidecar image to prove the behavior. Reporter input must be idempotent and capped.

---

## Issue Body Template (a.k.a. Task Execution Template)

Use this shape for the body of a new **GitHub Issue** (also handy when an existing issue needs a clearer scope). This is the **single** canonical template — `task-management.mdc` points here. Keep it tight:

**Summary** — one or two sentences: the user-visible capability, or the bug.

**Context** — background, constraints, and links to the relevant `docs/design/` doc(s) / code.

**Acceptance criteria**
- [ ] Observable result
- [ ] Status/behavior expectation
- [ ] Test expectation

**Non-goals** — what must not be included.

**Implementation notes** — known files/functions/commands; expected files to touch.

**Dependencies / blockers** — list, or "None known".

**Verification** — primary command (`make test`, `make test-e2e`, …).

---

## Scope Boundaries

Unless the user explicitly asks for design work or the selected task requires it, do not:

- add new CRDs
- add a UI
- add Envoy, Cilium/eBPF, NetworkPolicy generation, gVisor/Kata/Firecracker
- add a tool gateway
- add real policy enforcement
- add approval workflows
- add multi-cluster support
- refactor project structure
- replace Kubernetes Job reconciliation
- introduce a new orchestrator adapter

When a future feature seems relevant: check **GitHub Issues** → file an issue if missing → do not implement in the current task.

---

## Explain As You Go

For each implementation task, concisely explain:

- why the change is needed
- what Kubernetes/controller-runtime concept is involved
- what invariant must hold
- how the test proves the behavior

Keep it short and educational.

---

## How To Update Tracking After A Task

**Task state → GitHub Issue** (primary):

- Update the issue: summary of changes, files changed, tests run, remaining risks; link PRs/commits.
- Move it to `status/done` and **close** it (`state_reason: completed`) only after repo state matches the issue result (tests pass or the user accepts incomplete work).
- Create **GitHub Issues** for discovered work (`agent-discovered`); add them to board #1; link them from the current issue. Do not implement unless asked.
- Close the parent epic once all its child issues are closed.
- Include **### Out-of-scope future work noticed** in the user-facing summary.

**Durable context → `docs/design/`, component READMEs, code comments** (only when it changed):

- Update the relevant design doc, README, or code comment when behavior/semantics/invariants change.
- **Never** keep task state or status in markdown; do not (re)create a status/queue tracker file. That state lives only in GitHub Issues.
