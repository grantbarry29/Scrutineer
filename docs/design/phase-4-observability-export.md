# Observability Export (Prometheus / OTel Traces / OTLP Audit Logs)

> **Status:** Implemented (Phase 4). This doc is the canonical catalog of Relay's exported telemetry — metric names + labels, span names + attributes, audit record types + attributes, enable flags, and the trace-propagation contract. Update it whenever an exported signal changes.

## Purpose

Relay emits three independent telemetry signals from the control plane so platform/security teams can monitor, trace, and audit autonomous agent governance:

- **Prometheus metrics** — control-plane health and governance counts (always on).
- **OpenTelemetry traces** — reconcile and runtime-report spans, opt-in via OTLP.
- **OTLP audit logs** — structured governance audit records for SIEM/forensics, opt-in via OTLP.

These are **export surfaces, not new behavior**: they observe state the controller already computes. This doc exists so the future UI/SIEM/dashboard work (and sidecar trace continuation) has a stable contract and does not duplicate or drift names.

## Non-goals

- No in-cluster collector, Prometheus server, or storage — Relay only *exposes/exports*; scraping and OTLP collection are the operator's infrastructure.
- No change to which metrics/spans/records exist (this doc catalogs the shipped set).
- Not a replacement for Kubernetes Events (those remain the per-object signal; see README event table).
- No PII/secret export — records carry identities, phases, actions, and targets only.

## Prometheus metrics

Exposed on the controller-runtime metrics endpoint (`--metrics-bind-address`, default `:8080`, path `/metrics`). All metrics use the `relay_` prefix (namespace `relay`). Registered via `metrics.Register`; see `internal/metrics/`.

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `relay_agentsessions` | Gauge | `namespace`, `phase` | Current AgentSession count by lifecycle phase. |
| `relay_agentsession_violations` | Gauge | `namespace` | Total policy violations currently recorded on sessions. |
| `relay_approval_queue_depth` | Gauge | — | `ApprovalRequest`s awaiting a human decision (`status.state` Pending or unset). |
| `relay_policy_violations_observed_total` | Counter | `namespace`, `type` | Novel violations appended to status (monotonic). |
| `relay_runtime_reports_total` | Counter | `result` | `POST /v1/report` outcomes by result. |
| `relay_runtime_report_duration_seconds` | Histogram | — | Latency of `/v1/report` handling (default buckets). |

**Collection model:** the three gauges are computed on scrape by `AgentSessionCollector` — it lists `AgentSession`s (phase/violation gauges) and `ApprovalRequest`s (`approval_queue_depth`); the counters/histogram are updated inline (`ObserveNovelViolations`, `ObserveRuntimeReport`). Label cardinality is bounded by namespaces, the fixed phase enum, and violation `type` (`network`/`tool`/`file`/…); `result` is a small fixed set. Avoid adding unbounded labels (session name, target, domain).

## OpenTelemetry traces

Opt-in OTLP/HTTP export. Disabled (noop tracer) when the endpoint is empty, **but the W3C propagator is always installed** so inbound `traceparent` headers are honored even without export. Instrumentation scope root: `github.com/secureai/relay` (`/agentsession`, `/reporter`). See `internal/tracing/`.

| Span | Started by | Attributes |
|------|-----------|------------|
| `agentsession.reconcile` | reconciler (per reconcile) | `relay.session.namespace`, `relay.session.name`, `relay.session.phase`, `relay.reconcile.requeue_after_seconds`; records error + `Error` status on failure. |
| `runtime.report` | reporter HTTP middleware on `/v1/report` | `relay.session.namespace`, `relay.session.name`, `relay.report.result`, `relay.report.backend`, `relay.report.decisions`. |

**Propagation contract:** composite `TraceContext` + `Baggage`. The reporter extracts context from request headers (`tracing.HTTPMiddleware`), so a data-plane sidecar (or agent runtime) that sends `traceparent` on its report continues a single distributed trace from agent action → evidence ingestion. `service.name` comes from `--otel-service-name`.

## OTLP audit logs

Opt-in OTLP/HTTP **log** export (separate from traces) for governance audit/forensics. Disabled (noop) when the endpoint is empty. Logger name `relay.audit`; emitted best-effort and never blocks reconciliation. See `internal/audit/`.

Records are emitted as OTLP log records: body = human message, severity `INFO`, timestamp = event time. `EventType` values:

| `relay.audit.event_type` | Emitted when | Key attributes (besides namespace/name) |
|--------------------------|--------------|------------------------------------------|
| `policy.violation` | A novel violation is appended to status | `relay.audit.actor` (`relay-controller`), `relay.audit.action` (`violation`), `relay.audit.type`, `relay.audit.target` |
| `session.phase_change` | An AgentSession lifecycle phase changes | `relay.audit.actor`, `relay.session.from_phase`, `relay.session.phase` |
| `runtime.report` | Runtime evidence is merged from a backend | `relay.audit.actor`/`relay.report.backend`, `relay.audit.action` (`accepted`), `relay.audit.count` |
| `approval.granted` / `approval.denied` | A human-approval gate is resolved | `relay.audit.actor` (approver, or joined set for `allOf`), `relay.audit.action` (`granted`/`denied`), `relay.audit.type` (`approval`), `relay.audit.target` (gated action) |

**Attribute namespace:** all keys are `relay.audit.*` / `relay.session.*` / `relay.report.*` for stable SIEM routing. Record builders live in `internal/audit/record.go` (`PolicyViolation`, `SessionPhaseChange`, `RuntimeReport`).

## Enable flags (`cmd/main.go`)

| Flag | Default | Effect |
|------|---------|--------|
| `--metrics-bind-address` | `:8080` | Prometheus `/metrics` bind (metrics always on). |
| `--otel-exporter-otlp-endpoint` | _empty_ | OTLP/HTTP trace endpoint; **empty disables trace export** (propagation still active). |
| `--otel-service-name` | `relay-controller` | `service.name` resource attribute (traces + audit logs). |
| `--otel-exporter-otlp-insecure` | `true` | Disable TLS verification for the trace exporter. |
| `--audit-log-otlp-endpoint` | _empty_ | OTLP/HTTP log endpoint; **empty disables audit export**. |
| `--audit-log-otlp-insecure` | `true` | Disable TLS verification for the audit log exporter. |

## Invariants

- Metric names are stable API; renames are breaking. Prefer adding over renaming.
- Keep label cardinality bounded — never label by session name, target, domain, or other unbounded values.
- Traces and audit logs are independent opt-ins; neither failing export ever blocks or fails reconciliation.
- The propagator is installed even when trace export is off (so sidecars can still continue traces once export is enabled).
- Telemetry exposes existing state only; it must not become a side channel for control decisions.

## Follow-ups (tracked in `.cursor/relay-project-status.md`)

- Surface `assuranceLevel` on evidence in audit records / future UI (Runtime evidence integrity).
