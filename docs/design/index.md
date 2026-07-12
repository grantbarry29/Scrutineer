# Scrutineer Design Docs

Canonical design documentation — the reference an implementer (human or AI agent) reads
before non-trivial work. Each doc's frontmatter carries its `type`, `status`,
`description`, and `read_when`; do not trust a doc whose `status` is `historical`
without reading its `superseded_by` target. Authoring conventions:
[`dev-agent-rules/scrutineer-design-docs.md`](../../dev-agent-rules/scrutineer-design-docs.md).

# Orientation

* [Scrutineer Architecture & Design](architecture.md) - Whole-project architecture: control/data-plane split, CRD model, lifecycle, reconciliation, policy/evidence model, code map, invariants. Start here.
* [Untamperable Enforcement](untamperable-enforcement.md) - The enforcement doctrine: adversarial-grade-only, the verified-or-refused lock gate, removal of the cooperative in-pod tier. Read before any enforcement work.

# Enforcement & evidence

* [Enforcement Architecture](enforcement-architecture.md) - The `internal/enforcement` contract, NetworkPolicy baseline, runtime-evidence loop, and the out-of-pod Envoy egress path.
* [Runtime Reporter Contract](runtime-reporter-contract.md) - How a data-plane component reports runtime evidence into AgentSession status: wire contract, identity, assurance stamping.
* [Evidence Integrity — Per-Session Egress Chokepoint](evidence-integrity.md) - Moving egress evidence from cooperative to adversarial-grade via the per-session out-of-pod Envoy.
* [Bypass-Attempt Evidence for the Egress Lock](bypass-attempt-evidence.md) - Why bypass attempts leave no evidence today; decided: defer wholly to the #64 node interceptor.
* [Access-Log Rotation vs. Tamper Evidence](access-log-rotation.md) - The only-ingested-bytes-are-removed invariant and the rename→reopen→drain→delete protocol.

# Observability & session evidence

* [Structured Session Events API](session-events.md) - `status.events[]`: the durable, ordered, capped runtime timeline stream.
* [Session Timeline Projection Model](session-timeline.md) - Normalizes `status.events[]` into stable, UI-ready timeline entries.
* [Observability Export](observability-export.md) - Canonical catalog of exported telemetry: Prometheus metrics, OTel spans, OTLP audit records, flags.

# Governance surfaces

* [Human Approval Workflows](approval-workflows.md) - Scoped, auditable approval gates: ApprovalPolicy/ApprovalRequest CRDs and the gate/resume state machine.
* [Artifact Export](artifact-export.md) - Pluggable object-store export for collected session outputs; sliced into #117–#120 + demo #122.

# Runtime backends

* [Orchestrator Backend Interface](orchestrator-interface.md) - The `runtimeBackend` interface and registry; proven by the kubernetes-job and kubernetes-pod backends.

# Deferred designs (drafts)

* [Tools-Pod Chokepoint](tools-pod-chokepoint.md) - Out-of-pod tool governance: per-session tools pod, credential mediation, approval holds (epic #76).
* [LLM-Gateway Chokepoint](llm-gateway-chokepoint.md) - Credential-locked gateway turning advisory `spec.model` into enforced, observed governance (epic #77).
* [Arena Workspace](arena-workspace.md) - Out-of-pod file governance: network-POSIX per-session workspace pod (FUSE/9p analysis).
* [Operational UI Vision](operational-ui.md) - The deferred nice-to-have UI epic (#11): governance/observability dashboard guardrails and the backend surfaces it will consume.

# Investigations

* [Long-Running & App-Driven Agent Runtimes](long-running-agents.md) - Open question: should Scrutineer govern service-style agents? Not an agreed direction (#94).
