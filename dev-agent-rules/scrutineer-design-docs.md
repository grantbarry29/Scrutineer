
# Scrutineer Design Docs — When To Read

Scrutineer keeps canonical architecture/design docs in [`docs/design/`](../docs/design/). They are the source of truth for **intent and invariants**. They are intentionally **not** always loaded — read the specific doc that matches the task, during planning and before non-trivial changes.

## How to use

1. Before implementing or planning a non-trivial change, identify which design doc(s) cover the area (table below) and **read them** with the file-reading tool.
2. Follow their stated invariants and non-goals. If code and a design doc disagree, reconcile rather than silently diverge.
3. After shipping a slice, update the doc's status line and the **GitHub Issue**.
4. Do not dump entire design docs into context speculatively — read the one(s) you need.

## Which doc for which task

| Task area | Read |
|-----------|------|
| Anything non-trivial / orientation | [`docs/design/architecture.md`](../docs/design/architecture.md) (start here) |
| **The enforcement pivot** (adversarial-grade-only, lock gate, removal) | [`docs/design/untamperable-pivot.md`](../docs/design/untamperable-pivot.md) |
| Data-plane enforcement, `internal/enforcement` contract | [`docs/design/phase-3-enforcement-architecture.md`](../docs/design/phase-3-enforcement-architecture.md) |
| Runtime reporter / writing runtime evidence into `status` | [`docs/design/phase-3-runtime-reporter-contract.md`](../docs/design/phase-3-runtime-reporter-contract.md) |
| Out-of-pod egress chokepoint / observed-evidence trust boundary | [`docs/design/evidence-integrity.md`](../docs/design/evidence-integrity.md) |
| Future tool governance (out-of-pod tools chokepoint, credential mediation) | [`docs/design/tools-pod-chokepoint.md`](../docs/design/tools-pod-chokepoint.md) |
| Future file governance (network-POSIX arena workspace) | [`docs/design/arena-workspace.md`](../docs/design/arena-workspace.md) |
| Structured session events / reporter event payloads | [`docs/design/phase-4-session-events.md`](../docs/design/phase-4-session-events.md) |
| UI timeline projection over `status.events[]` (`internal/observability`) | [`docs/design/phase-4-session-timeline.md`](../docs/design/phase-4-session-timeline.md) |
| Human approval gates / `ApprovalPolicy` / `ApprovalRequest` / `requireHumanApproval` enforcement | [`docs/design/phase-5-approval-workflows.md`](../docs/design/phase-5-approval-workflows.md) |
| Mid-execution per-tool approval (hold a running tool/MCP call for a scoped human grant) | [`docs/design/phase-5-runtime-tool-approval.md`](../docs/design/phase-5-runtime-tool-approval.md) |
| Observability export (Prometheus / OTel traces / OTLP audit logs) | [`docs/design/phase-4-observability-export.md`](../docs/design/phase-4-observability-export.md) |
| Orchestrator decoupling / `RuntimeBackend` interface / orchestrator adapters (Tekton/Argo/Temporal) | [`docs/design/phase-6-orchestrator-interface.md`](../docs/design/phase-6-orchestrator-interface.md) |

The folder index is [`docs/design/README.md`](../docs/design/README.md). For task state and roadmap use **GitHub Issues** (see `dev-agent-rules/task-management.md`); for durable technical context use these design docs / component READMEs / code comments; for how to implement use `dev-agent-rules/scrutineer-cursor-workflow.md`.
