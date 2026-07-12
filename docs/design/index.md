# Scrutineer Design Docs

Canonical design documentation — the reference an implementer (human or AI agent) reads
before non-trivial work. Each doc's frontmatter carries its `type`, `status`,
`description`, and `read_when`; do not trust a doc whose `status` is `historical`
without reading its `superseded_by` target. Authoring conventions:
[`dev-agent-rules/scrutineer-design-docs.md`](../../dev-agent-rules/scrutineer-design-docs.md).

# Orientation

* [Scrutineer Architecture & Design](architecture.md) - Whole-project architecture: control/data-plane split, CRD model, lifecycle, reconciliation, policy/evidence model, code map, invariants.
* [Untamperable Enforcement](untamperable-enforcement.md) - The enforcement doctrine: adversarial-grade-only, the verified-or-refused lock gate, removal of the cooperative in-pod tier, and deferral of tool/file governance to out-of-pod chokepoints.

# Enforcement & evidence

* [Enforcement Architecture](enforcement-architecture.md) - Data-plane enforcement architecture: the internal/enforcement contract, NetworkPolicy baseline, runtime-evidence loop, and the out-of-pod Envoy egress path. The cooperative in-pod slices were removed (#71); their sections remain as condensed historical stubs.
* [Runtime Reporter Contract](runtime-reporter-contract.md) - How a data-plane component reports runtime evidence into AgentSession status (policyDecisions/violations): wire contract, identity, assurance stamping. The live caller is the egress-reporter in the per-session egress-proxy pod.
* [Evidence Integrity — Per-Session Egress Chokepoint](evidence-integrity.md) - Moving egress evidence from cooperative to adversarial-grade: per-session out-of-pod Envoy, explicit-proxy routing, caller-class observed stamping. Shipped via #8/#32/#62; the cooperative tier it hardened against was removed entirely (#71).
* [Bypass-Attempt Evidence for the Egress Lock](bypass-attempt-evidence.md) - Why bypass attempts against the routing lock leave no evidence today; interim options compared. Decision: defer wholly to the #64 node interceptor; approved contingency shape recorded.
* [Access-Log Rotation vs. Tamper Evidence](access-log-rotation.md) - Why the egress access log rotates, the only-ingested-bytes-are-removed invariant that preserves tamper evidence against flooding, and the rename→reopen→drain→delete protocol with its failure semantics.

# Observability & session evidence

* [Structured Session Events API](session-events.md) - status.events[] — the durable, ordered, capped runtime timeline stream: schema, ingestion via POST /v1/report, preservation across reconciler status patches.
* [Session Timeline Projection Model](session-timeline.md) - Normalizes status.events[] into stable, UI-ready timeline entries (internal/observability) — sorting, severity, titles, and filter semantics for future UI/API consumers.
* [Observability Export](observability-export.md) - Canonical catalog of exported telemetry: Prometheus metric names and labels, OTel span names and attributes, OTLP audit record types, enable flags, and the trace-propagation contract. Update it whenever an exported signal changes.

# Governance surfaces

* [Human Approval Workflows](approval-workflows.md) - Scoped, auditable human approval gates: ApprovalPolicy/ApprovalRequest CRDs, the controller gate/resume state machine, requireHumanApproval enforcement; also records the dormant per-tool runtime-approval surface.
* [Artifact Export](artifact-export.md) - Pluggable object-store export for collected session outputs — S3 backend, digests in status.artifacts, fallback semantics, retention posture; sliced into #117–#120 + demo #122.

# Runtime backends

* [Orchestrator Backend Interface](orchestrator-interface.md) - The runtimeBackend interface and registry keyed by spec.runtime.orchestrator; the reconciler owns all status/condition/event mapping. Proven by two in-tree backends (kubernetes-job, kubernetes-pod). Next: the external adapter design (Tekton first).

# Deferred designs (drafts)

* [Tools-Pod Chokepoint](tools-pod-chokepoint.md) - Out-of-pod successor to the removed cooperative tool tier: a per-session tools pod executes tool calls reached only through the session Envoy and holds the credentials the agent never sees — tool policy, argument rules, and approval holds, observed and mandatory.
* [LLM-Gateway Chokepoint](llm-gateway-chokepoint.md) - Out-of-pod, credential-locked gateway for the agent's model calls — turns advisory spec.model into enforced, observed governance (provider/model allowlist, token/cost caps, prompt evidence). Sibling of the tools-pod chokepoint under the same doctrine.
* [Arena Workspace](arena-workspace.md) - Out-of-pod successor to file governance: the governed workspace lives in a separate per-session pod served over a network-POSIX protocol (FUSE/9p analysis), so every file operation crosses a mediated, policy-checked boundary.
* [Operational UI Vision](operational-ui.md) - Vision for the deferred operational-UI epic (#11): a governance/observability dashboard — never a chatbot — the operational questions it must answer, and the backend surfaces it will consume. A nice-to-have feature epic, not core product.

# Investigations

* [Long-Running & App-Driven Agent Runtimes](long-running-agents.md) - Open investigation — not an agreed direction: whether Scrutineer should govern long-running, app-driven agents vs. the one-shot Job/Pod model. Questions and options only; answer the gating question in #94 before any design work.
