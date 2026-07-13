---
name: claim-issue
description: Claim a Scrutineer GitHub issue before editing code — pick the named issue (or the highest-precedence status/ready + agent-ready one), swap labels to status/in-progress + agent-in-progress, assign, then load its acceptance criteria and linked design docs.
---

# Claim an Issue

The canonical start-of-work procedure. `dev-agent-rules/task-management.md`
(*At the start of a work session*) delegates here — this file is the single
source of truth for the steps; do not copy them back into a rule (#137).

## 1. Pick exactly one issue

Take the issue the user named. Otherwise query the board for the
highest-priority open issue with `status/ready` **and** `agent-ready`, choosing
the lowest-numbered *Work precedence* bucket (`task-management.md`) that still
has an open card, then `priority/*` within it. If the choice is ambiguous,
propose a short list and let the user pick — never claim more than one, and
never claim an epic wholesale.

## 2. Claim it before modifying any code

In one update (`issue_write` via the github MCP server, or `gh issue edit` as
the fallback): swap `status/ready` → `status/in-progress` (exactly one
`status/*` label — swap, don't stack), add `agent-in-progress`, and assign
yourself if possible.

## 3. Read the scope

Read the issue's acceptance criteria, non-goals, and **all** linked repo
markdown specs / design docs (route via `docs/design/index.md` or each doc's
`read_when`). Never trust a `status: historical` doc without reading its
`superseded_by` target.

## 4. Gather context, then plan

Search the repo for the relevant implementation context before editing files.
Then state a short plan before touching code: selected task, acceptance
criterion, expected files (prefer 1–4 non-generated), primary verification
command, non-goals.
