
# Task Management

**GitHub Issues/Projects are the source of truth for task _state_** — backlog, ready,
in progress, blocked, review, done — plus ownership, priority, dependencies, and
blockers. **Repository markdown is the source of truth for durable technical context** —
design notes, architecture decisions, and agent guidance
(`docs/design/`, `dev-agent-rules/`, component `README.md`s, `dev-agent-rules/scrutineer-cursor-workflow.md`).

> The board is the **only** task tracker — there is no markdown queue/status file (the old
> `.cursor/scrutineer-project-status.md` was removed; do not recreate it). **The board must always reflect
> reality.** Discover work → file an issue. Start work → move it to in-progress. Make progress →
> comment. Get blocked → mark blocked. Finish → update + close. Never let the board go stale, and never
> track task state in chat or markdown.

Repo: [`grantbarry29/scrutineer`](https://github.com/grantbarry29/scrutineer) ·
Issues: <https://github.com/grantbarry29/scrutineer/issues> ·
Board: <https://github.com/users/grantbarry29/projects/1> (user project **#1**).

## Label model

- **Status:** `status/backlog`, `status/ready`, `status/in-progress`, `status/blocked`, `status/review`, `status/done`, `status/needs-triage`
- **Priority:** `priority/p0`–`priority/p3`
- **Type:** `type/{bug,feature,refactor,docs,test,chore,research}`
- **Area:** `area/{controller,api,reporter,enforcement,sidecar,observability,security,ui,docs,infra,testing,unknown}`
- **Agent:** `agent-ready`, `agent-discovered`, `agent-in-progress`, `agent-needs-human`, `agent-blocked`

Useful queries:
- Ready for an agent: [`is:issue is:open label:status/ready label:agent-ready`](https://github.com/grantbarry29/scrutineer/issues?q=is%3Aissue+is%3Aopen+label%3Astatus%2Fready+label%3Aagent-ready)
- Needs human triage: [`is:issue is:open label:status/needs-triage`](https://github.com/grantbarry29/scrutineer/issues?q=is%3Aissue+is%3Aopen+label%3Astatus%2Fneeds-triage)

## Work precedence (choosing the next issue)

When picking the next thing to do (and when offering next-task options), prefer the **lowest-numbered
bucket that still has an open card**. Ship value cheaply → make it visible → deepen trust → enterprise
→ breadth last:

1. **Smaller functional gaps** — cheap, adopter-facing wins.
2. **Test-only loose ends** — tighten confidence.
3. **Operational UI (Phase 7)** — surface the governance value; API-first read-only MVP first (epic).
4. **Runtime evidence integrity** — cooperative → adversarial trust (core thesis).
5. **Enterprise platform (Phase 8)** — multi-tenant identity/credentials/HA/sandboxes (epic).
6. **Orchestrator adapters (Phase 6 remainder)** — pure breadth; abstraction already proven (epic).

Within a bucket, order by `priority/*`. This ordering lives here (not in any markdown queue); revisit it
with the user when priorities shift.

## Status lifecycle

```
needs-triage ─► backlog ─► ready ─► in-progress ─► review ─► done (close)
                                  └─► blocked ─┘
```

- **`needs-triage`** — created but priority/scope unclear; a human should classify it. Pair with `agent-needs-human`.
- **`backlog`** — accepted but not queued yet.
- **`ready`** — well-scoped, unblocked, next up. Add `agent-ready` if an agent can take it without a human.
- **`in-progress`** — actively being worked. Exactly one status label at a time; swap, don't stack.
- **`blocked`** — cannot proceed; pair with `agent-blocked` and a comment naming the blocker (link the blocking issue).
- **`review`** — implementation done, awaiting review/verification (e.g. a PR is open).
- **`done`** — acceptance criteria met and merged/verified; **close the issue** with `state_reason: completed`.

Keep **exactly one** `status/*` label on an open issue. When you change state, remove the old status
label and add the new one in the same `issue_write` update.

## MCP operations (how to act on the board)

These tools come from the **GitHub MCP server**, configured as the remote `github`
server in the repo [`.mcp.json`](../.mcp.json) (GitHub's hosted endpoint, per-user
OAuth — no token is committed). To use it: trust the server when Claude Code prompts,
then complete the browser OAuth flow on first use. **Grant the `project` scope** during
OAuth in addition to `repo` — Projects v2 (board #1) writes fail without it. If the
`github` server isn't connected (check `/mcp`), fall back to the `gh` CLI, which is
authenticated but **lacks `project` scope** (run `gh auth refresh -s project` to add it).

| Goal | Tool | Notes |
|------|------|-------|
| Find work / check duplicates | `search_issues`, `list_issues`, `issue_read` | Search before creating to avoid duplicates |
| Create / update an issue, set labels, change state, close | `issue_write` | `method: create|update`; `labels`, `state: open|closed`, `state_reason: completed|not_planned|duplicate` |
| Add a progress/blocked/handoff note | `add_issue_comment` | Use for transient status; do **not** put transient notes in repo markdown |
| Add an issue to the board | `projects_write` (`add_project_item`) | `owner: grantbarry29`, `owner_type: user`, `project_number: 1`, `item_owner: grantbarry29`, `item_repo: Scrutineer` |
| Link a child to an epic/parent | `sub_issue_write` (`add`) | `sub_issue_id` is the child's **node/database id** (get it from `issue_read`), not its issue number |
| Create a missing label | `label_write` | Only when a needed label from the model above doesn't exist yet |

> Check a tool's input schema before calling it (visible via `/mcp`, or the server's
> tool descriptors) — argument names occasionally shift between server versions.

## Creating a new issue (discovered work, bugs, follow-ups) — mandatory

When you discover any out-of-scope work, bug, gap, doc debt, test hole, or "we should later…":

1. **Search first** (`search_issues`) — don't duplicate an existing issue. If it exists, reference it.
2. **Create it** (`issue_write` `create`) using the canonical **Issue Body Template** in
   `scrutineer-cursor-workflow.md` (Summary, Context, Acceptance criteria, Non-goals, Implementation notes,
   Dependencies/blockers, Verification).
3. **Label it fully** — exactly one `status/*` (usually `backlog`, or `needs-triage` + `agent-needs-human`
   if unclear), one `type/*` (**`type/bug` for bugs**), a `priority/*`, and at least one `area/*`. Add
   `agent-discovered` when found mid-task.
4. **Add it to the board** (`projects_write add_project_item`, project **#1**).
5. **Link it** — if it belongs to an epic, attach it with `sub_issue_write`; and cross-link it from the
   issue you were working on with a comment.
6. **Do not implement it now** unless the user asks — keep the current task narrow.

## At the start of a work session

1. Query the board (`search_issues` / `list_issues`) for the highest-priority issue with `status/ready`
   **and** `agent-ready` (or take the issue the user named).
2. **Claim exactly one issue before modifying code** (`issue_write update`): swap `status/ready`→
   `status/in-progress`, add `agent-in-progress`, assign yourself if possible.
3. Read the issue's acceptance criteria and all linked repo markdown specs / design docs.
4. Search the repo for relevant implementation context before editing files.

## During work — keep the board live

1. **Made meaningful progress but not done?** Add a brief `add_issue_comment` (what landed, what's left)
   and leave it `status/in-progress`. Don't silently sit on stale state.
2. **Blocked?** Set `status/blocked` + `agent-blocked`, comment the reason, and link the blocking issue.
   Resume to `status/in-progress` when unblocked.
3. **Opened a PR?** Move to `status/review` and link the PR on the issue.
4. **Discovered new work/bugs?** Follow *Creating a new issue* above — in the **same session**. Never
   defer tracking to chat-only.
5. Update durable docs only when the information stays useful after the issue closes — specs/design
   decisions in `docs/design/`, semantics in code comments, component `README.md`s. **Never** keep task
   state in markdown.

## Before marking an issue complete

1. Verify **every** acceptance criterion.
2. Run relevant tests, or explicitly record why tests were not run.
3. Update the issue (`add_issue_comment` and/or `issue_write`) with summary, files changed, tests run,
   and remaining risks; link relevant PRs/commits.
4. Set `status/done` and **close** the issue (`issue_write` `state: closed`, `state_reason: completed`),
   removing `agent-in-progress`. Only after repo state matches the issue result.
5. If a capability/phase shipped, update durable docs next to the code (`docs/design/`, component
   `README.md`s, code comments) and **close the parent epic** once all its child issues are closed.

## Implementation contract (summary)

Full detail: [`dev-agent-rules/scrutineer-cursor-workflow.md`](scrutineer-cursor-workflow.md).

1. Read `scrutineer-product-vision.md`, this file, and `scrutineer-cursor-workflow.md` when implementing; pull
   durable technical context from `docs/design/` / component READMEs / code comments.
2. Pick **one** GitHub Issue (`status/ready` + `agent-ready`, by *Work precedence* + priority) — or ask
   the user. Claim it (→ `status/in-progress`, `agent-in-progress`) before editing code.
3. Plan first: task, acceptance, files, verification, non-goals.
4. Implement only that task; prefer 1–4 non-generated files.
5. Explain as you go; update the issue (and durable docs if they changed) when done.
6. End the user-facing summary with **### Out-of-scope future work noticed**.
7. **No lost work:** every out-of-scope item becomes a **GitHub Issue** (`agent-discovered`, linked
   from the current issue) in the same session — not chat-only.
8. **End-of-Task Handoff Protocol** (see `scrutineer-cursor-workflow.md`): sync the board (done + close) →
   commit (don't push unless asked) → offer 2–4 selectable next-task options with a recommendation.

Unless explicitly selected by the user, do **not** add new CRDs, webhooks, sidecars, policy engines,
UI, Envoy, Cilium/eBPF, gVisor/Kata, tool-execution chokepoints, real policy enforcement, approval workflows,
multi-cluster support, a new orchestrator adapter, or a replacement for Kubernetes Job reconciliation.

## Relationship to other rules

- `scrutineer-product-vision.md` — product direction and MVP boundaries.
- `scrutineer-cursor-workflow.md` — implementation contract (full), scope rules, Issue Body Template, and the
  End-of-Task Handoff Protocol. Authoritative for **how** to implement; task state lives on the board.
- `docs/design/`, component `README.md`s, code comments — durable technical context.

There is **no** markdown task tracker; do not create one.
