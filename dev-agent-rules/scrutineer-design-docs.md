---
type: Agent Rule
title: Scrutineer Design Docs — When To Read
description: "Index and usage guidance for Scrutineer design docs in docs/design/. Consult the relevant design doc during planning and before non-trivial implementation (architecture, CRDs, policy, enforcement, reporter, observability). Do not paste whole docs into context — read the specific one that matches the task."
status: live
read_when: "Any non-trivial change in api/ or internal/{controller,enforcement,policy,reporter} — route to the matching design doc."
applies_to: ["api/v1alpha1/**", "internal/controller/**", "internal/enforcement/**", "internal/policy/**", "internal/reporter/**"]
always_load: false
---

# Scrutineer Design Docs — When To Read

Scrutineer keeps canonical architecture/design docs in [`docs/design/`](../docs/design/). They are the source of truth for **intent and invariants**. They are intentionally **not** always loaded — read the specific doc that matches the task, during planning and before non-trivial changes.

## How to use

1. Before implementing or planning a non-trivial change, identify which design doc(s) cover the area (table below) and **read them** with the file-reading tool.
2. Follow their stated invariants and non-goals. If code and a design doc disagree, reconcile rather than silently diverge.
3. After shipping a slice, update the doc's frontmatter `status` and the **GitHub Issue**.
4. Do not dump entire design docs into context speculatively — read the one(s) you need.

## Which doc for which task

| Task area | Read |
|-----------|------|
| Anything non-trivial / orientation | [`docs/design/architecture.md`](../docs/design/architecture.md) (start here) |
| **The enforcement doctrine** (adversarial-grade-only, lock gate, tier removal) | [`docs/design/untamperable-enforcement.md`](../docs/design/untamperable-enforcement.md) |
| Data-plane enforcement, `internal/enforcement` contract | [`docs/design/enforcement-architecture.md`](../docs/design/enforcement-architecture.md) |
| Runtime reporter / writing runtime evidence into `status` | [`docs/design/runtime-reporter-contract.md`](../docs/design/runtime-reporter-contract.md) |
| Out-of-pod egress chokepoint / observed-evidence trust boundary | [`docs/design/evidence-integrity.md`](../docs/design/evidence-integrity.md) |
| Future tool governance (out-of-pod tools chokepoint, credential mediation) | [`docs/design/tools-pod-chokepoint.md`](../docs/design/tools-pod-chokepoint.md) |
| Future file governance (network-POSIX arena workspace) | [`docs/design/arena-workspace.md`](../docs/design/arena-workspace.md) |
| Structured session events / reporter event payloads | [`docs/design/session-events.md`](../docs/design/session-events.md) |
| UI timeline projection over `status.events[]` (`internal/observability`) | [`docs/design/session-timeline.md`](../docs/design/session-timeline.md) |
| Human approval gates / `ApprovalPolicy` / `ApprovalRequest` / `requireHumanApproval` enforcement (incl. the dormant per-tool runtime-approval surface) | [`docs/design/approval-workflows.md`](../docs/design/approval-workflows.md) |
| Observability export (Prometheus / OTel traces / OTLP audit logs) | [`docs/design/observability-export.md`](../docs/design/observability-export.md) |
| Operational UI (deferred epic #11 — dashboard scope, anti-chatbot guardrails) | [`docs/design/operational-ui.md`](../docs/design/operational-ui.md) |
| Orchestrator decoupling / `RuntimeBackend` interface / orchestrator adapters (Tekton/Argo/Temporal) | [`docs/design/orchestrator-interface.md`](../docs/design/orchestrator-interface.md) |

The folder index is [`docs/design/index.md`](../docs/design/index.md). For task state and roadmap use **GitHub Issues** (see `dev-agent-rules/task-management.md`); for durable technical context use these design docs / component READMEs / code comments; for how to implement use `dev-agent-rules/scrutineer-workflow.md`.

## Legacy information policy (#127)

Three kinds of "old" content, three treatments — the litmus test: *does the passage
explain why the current design is shaped this way* (keep) *or how a dead design
operated* (extract)?

1. **Wholly superseded docs** stay in place with `status: historical` +
   `superseded_by:` frontmatter — never deleted (git history is invisible to agents)
   and never moved to an archive directory (breaks inbound links; the metadata already
   segregates them).
2. **Retired mechanics** must not sit inline in a live doc: extract them into the
   historical doc that owns that era (or git history via a purge commit), leaving a
   short pointer stub — the slices 5–8 stub in `enforcement-architecture.md`
   is the reference shape. A doc whose `status` is `approved`/`implemented` must
   contain no stale guidance, or the status field lies.
3. **Decision rationale** — rejected alternatives, threat analyses, "why not X" — stays
   inline; it is load-bearing for the current design and stops future agents from
   re-proposing rejected approaches.

## Authoring conventions

- Every design doc carries **OKF frontmatter** (`type`, `title`, `description`, `status`, `read_when`, optional `tracking_issue`/`superseded_by`); **scope** and **non-goals** stay in the body. `make lint-docs` enforces the contract.
- **Diagrams** use [Mermaid](https://mermaid.js.org/) fenced code blocks so they render on GitHub and in editors without external assets.
- Keep docs in sync: when a slice ships, update the doc's frontmatter `status` and the tracking GitHub Issue. Design docs describe intent; if code and a design doc disagree, treat it as a bug in one of them and reconcile (do not silently diverge).
