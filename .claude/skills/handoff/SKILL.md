---
name: handoff
description: End-of-Task Handoff for Scrutineer — run whenever a task is finished (work complete, verification passed). Syncs the GitHub board (evidence comment, status/done, close) and commits the work.
---

# End-of-Task Handoff

The canonical end-of-task procedure. `dev-agent-rules/scrutineer-workflow.md`
(End-of-Task Handoff Protocol) delegates here — this file is the single source of
truth for the steps; do not copy them back into a rule (#137). Do both steps, in
order.

Do **not** end the handoff by prompting the user with a menu of suggested next
tasks — they drive what comes next. Stop after the work is synced and committed.

## 1. Sync the board first

Update the GitHub Issue per `dev-agent-rules/task-management.md` → *Before marking
an issue complete*: comment the summary / files changed / tests run / remaining
risks (link commits/PRs), set `status/done` (exactly one status label — swap,
don't stack), remove `agent-in-progress`, and **close** it
(`state_reason: completed`) — only after repo state actually matches the issue
result. Make sure every piece of work discovered this session is already filed as
its own issue (`agent-discovered`, on board #1, cross-linked from the current
issue). The board must reflect reality before you move on.

## 2. Commit it

Create a git commit with a short but aptly descriptive message (imperative mood,
focused on the what/why). Only commit when the work is complete and verified —
tests pass, or the user accepted incomplete work. Follow the git safety rules.
**Do not push** unless the user has asked for pushes.

---

This protocol does not replace the **### Out-of-scope future work noticed**
summary requirement from `scrutineer-workflow.md` — do both.
