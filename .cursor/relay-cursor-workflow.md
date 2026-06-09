# Relay Cursor Workflow

> Rules and templates for **Cursor-assisted implementation** on Relay.
> **Not** the project status tracker — use [`.cursor/relay-project-status.md`](relay-project-status.md) for what is done, in progress, and planned.

Also loaded via [`.cursor/rules/relay-project-status.mdc`](rules/relay-project-status.mdc) (summary) and [`.cursor/rules/relay-product-vision.mdc`](rules/relay-product-vision.mdc) (product direction).

---

## Document map

| File | Audience | Contents |
|------|----------|----------|
| **`relay-project-status.md`** | Humans + agents | Operational state, queue, roadmap, gaps, recent fixes |
| **`relay-cursor-workflow.md`** (this file) | Primarily agents | Implementation contract, scope rules, task templates, how to update status |
| **`rules/relay-product-vision.mdc`** | Agents | Product vision, MVP boundaries, long-term direction |
| **`docs/design/`** | Humans + agents | Canonical architecture & per-phase design docs (read during planning; see `rules/relay-design-docs.mdc`) |

---

## Cursor Implementation Contract

When asked to implement a Relay task, Cursor must:

1. Read `relay-product-vision.mdc`, `relay-project-status.mdc`, **`relay-project-status.md`**, and this file when implementing or updating tracking.
2. Identify the **exact selected task** from **Ready for Cursor Queue** (or user prompt). If unclear, ask or propose a short list. Do not pick multiple roadmap phases automatically.
3. Before editing code, provide a short plan: selected task, acceptance criterion, expected files, verification command, non-goals. **During planning, read the relevant design doc(s) in [`docs/design/`](../docs/design/)** — start with `architecture.md`, then the phase/area doc that matches the task (see `relay-design-docs.mdc`). Follow their invariants and non-goals. Do not load all design docs at once; read the one(s) you need.
4. Implement **only** that task. Do not add adjacent roadmap items unless explicitly requested.
5. Keep changes reviewable. Prefer **1–4 non-generated files**; if more are needed, stop and explain.
6. **Explain as you go** (see below).
7. After implementation, summarize changes, tests run, and **update `relay-project-status.md`** (not this file, except if workflow rules themselves change).
8. End every implementation summary with **### Out-of-scope future work noticed** (see below).
9. If follow-up work is discovered, follow **Out-of-Scope Future Work Handling**; add tasks to the status file; do not implement unless asked.
10. If architecture is ambiguous, stop, offer 2–3 options, recommend one, wait for confirmation.
11. Preserve Kubernetes controller discipline: idempotent reconciliation, owner references, status subresources, conditions, events, least-privilege RBAC.

---

## How To Work On Roadmap Items

Roadmap checkboxes in `relay-project-status.md` are **capabilities**, not one PR or one prompt. Decompose into narrow tasks with one acceptance criterion and one verification command.

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

**No lost work rule (mandatory):** Anything noticed during implementation that is not in the current task scope **must** be recorded in **`relay-project-status.md`** before the session ends. Chat summaries, roadmap checkboxes alone, or one-line “suggested next picks” are **not** sufficient — they do not prevent holes in the project.

When implementing any task, distinguish:

1. **Current task requirements** — required for acceptance criteria.
2. **Necessary supporting changes** — compile, test, or consistency for the selected task only.
3. **Future requirements** — related but not required now.

**Do not implement future requirements** unless the user explicitly asks.

Instead, for **every** future requirement:

1. Notice it (gap, follow-up, adjacent phase, TODO in code, “we should later…”, enforcement hook, doc debt, test gap).
2. Search **`relay-project-status.md`** (Ready for Cursor Queue, **Discovered Follow-Up Tasks**, roadmap).
3. **If already tracked** — note where (task card title or roadmap bullet) in the end summary.
4. **If not tracked** — add a **scoped task card** under **Discovered Follow-Up Tasks** using the **Task Execution Template** below (preferred), or a roadmap checkbox if it is a large multi-slice capability without a clear next step yet.
5. **In the same session** — edit the status file; do not defer “I’ll add it later.”
6. Continue with only the original task.

### What counts as “tracked”

| Tracking | Sufficient? |
|----------|-------------|
| Full **Discovered Follow-Up Tasks** card (Why, Scope, Acceptance, Files, Verification) | Yes |
| Roadmap phase bullet (for coarse capabilities) | Yes, if no immediate slice exists |
| “Next suggested queue picks” one-liner only | **No** — promote to task cards |
| Mention only in chat / PR comment | **No** |
| “We’ll do it later” without editing status | **No** |

If multiple small items belong together, one task card may cover them; if they are independent, use separate cards so nothing is bundled into oblivion.

### End-of-task checklist (agents)

Before marking work complete:

- [ ] Every out-of-scope item from this session has **Already tracked: yes** with a pointer to a task card or roadmap line, **or** was added to **`relay-project-status.md`** in this session.
- [ ] User-facing summary includes **### Out-of-scope future work noticed** (see below).
- [ ] Completed work reflected in status (recent fixes, gaps table, roadmap `[x]` where appropriate).

### End-of-task summary requirement

After every implementation task, include:

### Out-of-scope future work noticed

- `None.` — if nothing relevant.

Or one bullet per item:

- **Description** — what was noticed
- **Already tracked:** yes → link to task card title / roadmap section; **no** → confirm added under **Discovered Follow-Up Tasks** (task title) in this session

### Examples

| While implementing… | Do not… | Instead… |
|---------------------|---------|----------|
| `status.podName` | Redesign Job retry/backoff | Add task if not tracked |
| Cancellation | Add finalizers | Reference finalizer tasks in status |
| Policy env vars | Add Envoy/Cilium/NetworkPolicy | Reference Phase 3 in status |
| Events | Build audit backend / UI | Reference Phase 4 in status |
| `promptConfigMapRef` | Cross-namespace refs | Add reference-scoping task |
| ServiceAccount fields | Redesign identity | Reference Phase 8 / CredentialProfile |

---

## Repository State Scan Rule

Scan the repo against **`relay-project-status.md`** when:

- the user asks what to work on next or to tighten rules
- a task is completed
- a new subsystem is introduced
- the status file may be stale

Compare: CRD/API fields, controller behavior, tests, samples, README, Makefile, CI, RBAC, generated manifests, TODO comments, status claims.

Look for mismatches (status says done but code does not; API without controller behavior; missing tests; stale samples; etc.).

**During a scan, do not implement fixes unless asked.** Update **`relay-project-status.md`** (and rules if needed).

Promote items from **Discovered Follow-Up Tasks** into **Ready for Cursor Queue** when appropriate.

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

## Task Execution Template

Use this format for new tasks in **`relay-project-status.md`** (queue or discovered):

### Task: `<short name>`

**Goal:**  
One sentence, user-visible capability.

**Scope:**  
What to implement.

**Non-goals:**  
What must not be included.

**Acceptance criteria:**  
- Observable result
- Status/behavior expectation
- Test expectation

**Expected files:**  
- paths

**Verification command:**  
`make test`, `make test-e2e`, etc.

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

When a future feature seems relevant: check status file → add task if missing → do not implement in the current task.

---

## Explain As You Go

For each implementation task, concisely explain:

- why the change is needed
- what Kubernetes/controller-runtime concept is involved
- what invariant must hold
- how the test proves the behavior

Keep it short and educational.

---

## How To Update `relay-project-status.md`

After completing a task:

- Remove or complete the matching **Ready for Cursor Queue** card; add a line under **Recently completed** if useful.
- Update roadmap `[ ]` / `[~]` / `[x]` when a capability is done.
- Add **Recent fixes** for user-visible changes.
- Update **What works today**, **Known gaps**, **Current Operational State** when they change.
- Update **Last updated** at the top of the status file.
- Add discovered work to **Discovered Follow-Up Tasks** (do not implement unless asked).
- Include **### Out-of-scope future work noticed** in the user-facing summary.
- Do not remove long-term roadmap items.
- Do not mark complete unless tests pass or the user accepts incomplete work.
- Consider a lightweight **Repository State Scan** after API/controller/RBAC/test changes.

Do **not** duplicate the full roadmap or workflow rules into the status file.
