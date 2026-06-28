# Relay Design Docs

Canonical design documentation for Relay. These docs describe **architecture and intent** — they are the reference an implementer (human or AI agent) should read before non-trivial work. They are **not** loaded into agent context automatically; consult the relevant doc during planning (see `.cursor/rules/relay-design-docs.mdc`).

For *task state, queue, and roadmap*, see [GitHub Issues / Projects](https://github.com/grantbarry29/Relay/issues). For *how to implement tasks*, see [`.cursor/relay-cursor-workflow.md`](../../.cursor/relay-cursor-workflow.md). For *product direction*, see [`.cursor/rules/relay-product-vision.mdc`](../../.cursor/rules/relay-product-vision.mdc).

## Index

| Doc | Read when… |
|-----|-----------|
| [`architecture.md`](architecture.md) | **Start here.** Whole-project architecture: control/data-plane split, CRD model, lifecycle, reconciliation, policy/evidence model, code map, invariants. |
| [`phase-3-enforcement-architecture.md`](phase-3-enforcement-architecture.md) | Working on data-plane enforcement, the `internal/enforcement` contract, or any Phase 3/3b slice. |
| [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) | Implementing the runtime reporter (sidecar → controller evidence loop) or anything that writes runtime evidence into status. |
| [`phase-3-dns-proxy-prototype.md`](phase-3-dns-proxy-prototype.md) | Working on egress/DNS governance or the dns-proxy sidecar. |
| [`phase-3-tool-gateway-contract.md`](phase-3-tool-gateway-contract.md) | Working on tool/MCP governance or the tool-gateway sidecar. |
| [`phase-3-tool-argument-constraints.md`](phase-3-tool-argument-constraints.md) | Working on argument-level tool/MCP governance — `ToolPolicy` argument rules, the `ArgumentConstraint` schema, or gateway per-call argument evaluation. |
| [`phase-3-file-workspace-policy.md`](phase-3-file-workspace-policy.md) | Working on file/workspace governance (mount strategy, FS gateway, path rules). |
| [`phase-4-session-events.md`](phase-4-session-events.md) | Working on `status.events[]`, timeline ingestion, or reporter event payloads. |
| [`phase-4-session-timeline.md`](phase-4-session-timeline.md) | Working on UI timeline projection over `status.events[]` (`internal/observability`). |
| [`phase-5-approval-workflows.md`](phase-5-approval-workflows.md) | Working on human approval gates — `ApprovalPolicy` / `ApprovalRequest` CRDs, the controller gate/resume state machine, or `requireHumanApproval` enforcement. |
| [`phase-5-runtime-tool-approval.md`](phase-5-runtime-tool-approval.md) | Working on **mid-execution** per-tool approval — holding a running agent's tool/MCP call for a scoped, time-bounded human grant (runtime `ApprovalRequest`, reporter approval channel, gateway hold-and-ask). |
| [`phase-4-observability-export.md`](phase-4-observability-export.md) | Working on exported telemetry — Prometheus metrics, OTel trace spans, or OTLP audit logs (names, labels, attributes, flags, propagation). |
| [`phase-6-orchestrator-interface.md`](phase-6-orchestrator-interface.md) | Decoupling the reconciler from Kubernetes Jobs — the `RuntimeBackend` interface, `spec.runtime.orchestrator` selection, or adding an orchestrator adapter (Tekton/Argo/Temporal). |

## Conventions

- **Diagrams** use [Mermaid](https://mermaid.js.org/) fenced code blocks so they render on GitHub and in editors without external assets.
- Each design doc states its **status** (design / implemented), **scope**, and **non-goals**, and links to its tracking GitHub Issue.
- Keep docs in sync: when a slice ships, update its status line here and update the tracking GitHub Issue.
- Design docs describe intent; if code and a design doc disagree, treat it as a bug in one of them and reconcile (do not silently diverge).
