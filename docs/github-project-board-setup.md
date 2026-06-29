# GitHub Project board — setup

Task **state** for Scrutineer lives in GitHub Issues (see
[`.cursor/rules/task-management.mdc`](../.cursor/rules/task-management.mdc)). A GitHub
**Project (v2)** board gives a kanban view over those issues.

**Current state:** the user-owned project **[Scrutineer](https://github.com/users/grantbarry29/projects/1)**
(project #1) exists and **all 13 migrated issues have been added to it** (via a classic
PAT with `project` scope — fine-grained PATs cannot access user-owned Projects v2). The
only remaining step is configuring how the board displays state (one-time, UI-only),
because the MCP API cannot add custom Status options — see the note at the bottom.

## Remaining one-time setup (pick one)

**Option A — group the board by labels (recommended, no field editing):**
The project already has a built-in **Labels** field, and the `status/*` labels carry the
full six-state granularity. Create a **Board** view and **Group by → Labels**. The board
columns then reflect `status/backlog`, `status/ready`, `status/in-progress`,
`status/blocked`, `status/review`, `status/done`, `status/needs-triage` with no extra
configuration. Labels stay the single source of truth.

**Option B — add the six Status options and mirror them:**
1. Open the **Status** field (Project settings → Fields → Status). It ships with only
   `Todo` / `In Progress` / `Done`. Add: `Backlog`, `Ready`, `Blocked`, `Review`
   (and optionally rename/keep the defaults), to get:
   `Backlog`, `Ready`, `In Progress`, `Blocked`, `Review`, `Done`.
2. Set each item's **Status** to match its `status/*` label.
3. Use a **Board** view grouped by Status.

> Note: every issue was added with **no Status set** (lands in "No Status"/Todo). Since
> none of the migrated issues are in-progress/review/done, that is accurate today.

## Alternative: no custom Status field

If you prefer not to maintain a separate Status field, create a **Table** or **Board**
view and **Group by → Labels**. The `status/*` labels already encode the column, so the
board reflects issue state without duplicating it. This keeps labels as the single source
of truth.

## Recommended saved views

- **Board by status** — group by Status (or by the `status/*` labels).
- **Agent-ready** — filter `label:status/ready label:agent-ready`, sorted by priority.
- **Needs triage** — filter `label:status/needs-triage`.
- **By area** — group by the `area/*` labels.

## MCP limitation note

The GitHub MCP server's `projects_write` can `create_project` and `add_project_item`, and
`create_iteration_field`, but it has **no method to add custom single-select options** to
the Status field. So even when automated, the six Status columns above must be configured
once in the GitHub UI (or you group by the `status/*` labels instead). Issue **labels**
remain the authoritative task-state signal regardless of board configuration.
