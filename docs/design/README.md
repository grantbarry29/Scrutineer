---
type: Index
title: Scrutineer Design Docs — Index
description: "Index of the design-doc bundle (converted to a reserved index.md later in the #127 migration)."
status: live
read_when: "Finding the right design doc."
---

# Scrutineer Design Docs

Canonical design documentation for Scrutineer. These docs describe **architecture and intent** — they are the reference an implementer (human or AI agent) should read before non-trivial work. They are **not** loaded into agent context automatically; consult the relevant doc during planning (see [`dev-agent-rules/scrutineer-design-docs.md`](../../dev-agent-rules/scrutineer-design-docs.md)).

For *task state, queue, and roadmap*, see [GitHub Issues / Projects](https://github.com/grantbarry29/scrutineer/issues). For *how to implement tasks*, see [`dev-agent-rules/scrutineer-workflow.md`](../../dev-agent-rules/scrutineer-workflow.md). For *product direction*, see [`dev-agent-rules/scrutineer-product-vision.md`](../../dev-agent-rules/scrutineer-product-vision.md).

## Index

| Doc | Read when… |
|-----|-----------|
| [`architecture.md`](architecture.md) | **Start here.** Whole-project architecture: control/data-plane split, CRD model, lifecycle, reconciliation, policy/evidence model, code map, invariants. |
| [`untamperable-enforcement.md`](untamperable-enforcement.md) | **The enforcement doctrine.** Read before any enforcement work: adversarial-grade-only doctrine, verified-or-refused lock gate, removal of the cooperative in-pod tier, sequencing, and which docs below are historical. |
| [`tools-pod-chokepoint.md`](tools-pod-chokepoint.md) | Draft/deferred (epic #76): out-of-pod successor to the removed cooperative tool tier — tools pod, credential mediation, ext_authz, approval holds at the chokepoint. |
| [`arena-workspace.md`](arena-workspace.md) | Draft/deferred: out-of-pod successor to file governance — network-POSIX arena pod (FUSE/9p analysis). |
| [`llm-gateway-chokepoint.md`](llm-gateway-chokepoint.md) | Draft/deferred (epic #77): out-of-pod, credential-locked gateway for the agent's model calls — turns advisory `spec.model` into enforced, `observed` governance (provider/model allowlist, token/cost caps, prompt evidence). |
| [`long-running-agents.md`](long-running-agents.md) | Open investigation (not designed/scheduled): whether Scrutineer should govern long-running, app-driven agents vs. the current one-shot Job/Pod model — questions and options only, likely a docs/pattern answer. |
| [`phase-3-enforcement-architecture.md`](phase-3-enforcement-architecture.md) | Working on data-plane enforcement, the `internal/enforcement` contract, or any Phase 3/3b slice. |
| [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) | Implementing the runtime reporter (data-plane → controller evidence loop) or anything that writes runtime evidence into status. |
| [`phase-4-session-events.md`](phase-4-session-events.md) | Working on `status.events[]`, timeline ingestion, or reporter event payloads. |
| [`phase-4-session-timeline.md`](phase-4-session-timeline.md) | Working on UI timeline projection over `status.events[]` (`internal/observability`). |
| [`phase-5-approval-workflows.md`](phase-5-approval-workflows.md) | Working on human approval gates — `ApprovalPolicy` / `ApprovalRequest` CRDs, the controller gate/resume state machine, or `requireHumanApproval` enforcement. Also records the dormant per-tool runtime-approval surface (hold protocol → `tools-pod-chokepoint.md`). |
| [`phase-4-observability-export.md`](phase-4-observability-export.md) | Working on exported telemetry — Prometheus metrics, OTel trace spans, or OTLP audit logs (names, labels, attributes, flags, propagation). |
| [`evidence-integrity.md`](evidence-integrity.md) | Working on runtime-evidence integrity — the *cooperative → adversarial* trust boundary, mandatory out-of-pod egress, and `observed`-assurance evidence. Read before touching egress trust boundaries or #8/#32 enforcement placement. |
| [`bypass-attempt-evidence.md`](bypass-attempt-evidence.md) | Design note (decided): why bypass *attempts* against the routing lock leave no evidence today, the interim options compared, and the decision to defer wholly to the #64 node interceptor (Hubble-adapter contingency recorded). |
| [`access-log-rotation.md`](access-log-rotation.md) | Design note (decided, #98): why the egress access log rotates, the only-ingested-bytes-are-removed invariant that preserves tamper evidence against flooding, the rename→reopen→drain→delete protocol, and its failure semantics. |
| [`phase-6-orchestrator-interface.md`](phase-6-orchestrator-interface.md) | Decoupling the reconciler from Kubernetes Jobs — the `RuntimeBackend` interface, `spec.runtime.orchestrator` selection, or adding an orchestrator adapter (Tekton/Argo/Temporal). |
| [`artifact-export.md`](artifact-export.md) | Design (code-verified, sliced into #117–#120 + demo #122; epic #2): pluggable object-store export for collected session outputs — S3 backend, digests in `status.artifacts`, fallback semantics, retention posture, reuse path for future evidence export. |

## Conventions

- **Diagrams** use [Mermaid](https://mermaid.js.org/) fenced code blocks so they render on GitHub and in editors without external assets.
- Each design doc carries **OKF frontmatter** (`type`, `title`, `description`, `status`, `read_when`, optional `tracking_issue`/`superseded_by`); **scope** and **non-goals** stay in the body. `make lint-docs` enforces the frontmatter contract.
- Keep docs in sync: when a slice ships, update the doc's frontmatter `status` and the tracking GitHub Issue.
- Design docs describe intent; if code and a design doc disagree, treat it as a bug in one of them and reconcile (do not silently diverge).
