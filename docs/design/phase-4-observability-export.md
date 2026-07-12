---
type: Design Doc
title: Observability Export
description: "Canonical catalog of exported telemetry: Prometheus metric names and labels, OTel span names and attributes, OTLP audit record types, enable flags, and the trace-propagation contract. Update it whenever an exported signal changes."
status: implemented
read_when: "Exported telemetry — metrics, traces, audit logs, flags, propagation."
---

# Observability Export (Prometheus / OTel Traces / OTLP Audit Logs)

> **Note:** This doc is the canonical catalog of Scrutineer's exported telemetry — metric names + labels, span names + attributes, audit record types + attributes, enable flags, and the trace-propagation contract. Update it whenever an exported signal changes.

## Purpose

Scrutineer emits three independent telemetry signals from the control plane so platform/security teams can monitor, trace, and audit autonomous agent governance:

- **Prometheus metrics** — control-plane health and governance counts (always on).
- **OpenTelemetry traces** — reconcile and runtime-report spans, opt-in via OTLP.
- **OTLP audit logs** — structured governance audit records for SIEM/forensics, opt-in via OTLP.

These are **export surfaces, not new behavior**: they observe state the controller already computes. This doc exists so the future UI/SIEM/dashboard work (and sidecar trace continuation) has a stable contract and does not duplicate or drift names.

## Non-goals

- No in-cluster collector, Prometheus server, or storage — Scrutineer only *exposes/exports*; scraping and OTLP collection are the operator's infrastructure.
- No change to which metrics/spans/records exist (this doc catalogs the shipped set).
- Not a replacement for Kubernetes Events (those remain the per-object signal; see README event table).
- No PII/secret export — records carry identities, phases, actions, and targets only.

## Prometheus metrics

Exposed on the controller-runtime metrics endpoint (`--metrics-bind-address`, default `:8080`, path `/metrics`). All metrics use the `scrutineer_` prefix (namespace `scrutineer`). Registered via `metrics.Register`; see `internal/metrics/`.

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `scrutineer_agentsessions` | Gauge | `namespace`, `phase` | Current AgentSession count by lifecycle phase. |
| `scrutineer_agentsession_violations` | Gauge | `namespace` | Total policy violations currently recorded on sessions. |
| `scrutineer_approval_queue_depth` | Gauge | — | `ApprovalRequest`s awaiting a human decision (`status.state` Pending or unset). |
| `scrutineer_policy_violations_observed_total` | Counter | `namespace`, `type` | Novel violations appended to status (monotonic). |
| `scrutineer_runtime_reports_total` | Counter | `result` | `POST /v1/report` outcomes by result. |
| `scrutineer_runtime_report_duration_seconds` | Histogram | — | Latency of `/v1/report` handling (default buckets). |

**Collection model:** the three gauges are computed on scrape by `AgentSessionCollector` — it lists `AgentSession`s (phase/violation gauges) and `ApprovalRequest`s (`approval_queue_depth`); the counters/histogram are updated inline (`ObserveNovelViolations`, `ObserveRuntimeReport`). Label cardinality is bounded by namespaces, the fixed phase enum, and violation `type` (`network`/`tool`/`file`/…); `result` is a small fixed set. Avoid adding unbounded labels (session name, target, domain).

### Egress-proxy pod (data plane, #55)

Each per-session egress-proxy pod exposes two scrape endpoints (container ports are named for scrape configs; no PodMonitor/annotations shipped — discovery is the operator's infrastructure):

- **Envoy stats** — `:9902` (`envoy.StatsPort`, port name `envoy-stats`), path `/stats/prometheus`. A stats-only listener routes exactly that path to the loopback admin cluster; the admin API itself stays bound to `127.0.0.1:9901`. Full upstream/RBAC/CONNECT stats under the standard `envoy_*` names. The agent cannot reach it (the routing lock allows agent→Envoy on the proxy port only).
- **egress-reporter** — `:9903` (`envoy.ReporterMetricsPort`, port name `metrics`), path `/metrics`, bind overridable via `SCRUTINEER_METRICS_ADDR` (`disabled` turns it off). Dedicated registry (`internal/enforcement/egressmetrics`); a metrics failure never stops the evidence pipeline.

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `scrutineer_egress_reporter_decisions_total` | Counter | `action` | Access-log entries parsed into egress evidence, by decision action. |
| `scrutineer_egress_reporter_malformed_lines_total` | Counter | — | Unparseable Envoy access-log lines skipped. |
| `scrutineer_egress_reporter_submissions_total` | Counter | `outcome` | Evidence batch submissions to the controller reporter (`ok`/`error`). |
| `scrutineer_egress_reporter_submission_duration_seconds` | Histogram | — | Submission latency (default buckets). |
| `scrutineer_egress_reporter_dropped_decisions_total` | Counter | — | Decisions discarded to pending-queue overflow — evidence lost to a prolonged reporter outage. |

## OpenTelemetry traces

Opt-in OTLP/HTTP export. Disabled (noop tracer) when the endpoint is empty, **but the W3C propagator is always installed** so inbound `traceparent` headers are honored even without export. Instrumentation scope root: `github.com/grantbarry29/scrutineer` (`/agentsession`, `/reporter`). See `internal/tracing/`.

| Span | Started by | Attributes |
|------|-----------|------------|
| `agentsession.reconcile` | reconciler (per reconcile) | `scrutineer.session.namespace`, `scrutineer.session.name`, `scrutineer.session.phase`, `scrutineer.reconcile.requeue_after_seconds`; records error + `Error` status on failure. |
| `runtime.report` | reporter HTTP middleware on `/v1/report` | `scrutineer.session.namespace`, `scrutineer.session.name`, `scrutineer.report.result`, `scrutineer.report.backend`, `scrutineer.report.decisions`. |

**Propagation contract:** composite `TraceContext` + `Baggage`. The reporter extracts context from request headers (`tracing.HTTPMiddleware`), so a data-plane sidecar (or agent runtime) that sends `traceparent` on its report continues a single distributed trace from agent action → evidence ingestion. `service.name` comes from `--otel-service-name`.

## OTLP audit logs

Opt-in OTLP/HTTP **log** export (separate from traces) for governance audit/forensics. Disabled (noop) when the endpoint is empty. Logger name `scrutineer.audit`; emitted best-effort and never blocks reconciliation. See `internal/audit/`.

Records are emitted as OTLP log records: body = human message, severity `INFO`, timestamp = event time. `EventType` values:

| `scrutineer.audit.event_type` | Emitted when | Key attributes (besides namespace/name) |
|--------------------------|--------------|------------------------------------------|
| `policy.violation` | A novel violation is appended to status | `scrutineer.audit.actor` (`scrutineer-controller`), `scrutineer.audit.action` (`violation`), `scrutineer.audit.type`, `scrutineer.audit.target`, `scrutineer.audit.assurance` |
| `session.phase_change` | An AgentSession lifecycle phase changes | `scrutineer.audit.actor`, `scrutineer.session.from_phase`, `scrutineer.session.phase` |
| `runtime.report` | Runtime evidence is merged from a backend | `scrutineer.audit.actor`/`scrutineer.report.backend`, `scrutineer.audit.action` (`accepted`), `scrutineer.audit.count`, `scrutineer.audit.assurance` (`self-reported`) |
| `approval.granted` / `approval.denied` | A human-approval gate is resolved | `scrutineer.audit.actor` (approver, or joined set for `allOf`), `scrutineer.audit.action` (`granted`/`denied`), `scrutineer.audit.type` (`approval`), `scrutineer.audit.target` (gated action), `scrutineer.audit.assurance` (`controller`) |

**Attribute namespace:** all keys are `scrutineer.audit.*` / `scrutineer.session.*` / `scrutineer.report.*` for stable SIEM routing. Record builders live in `internal/audit/record.go` (`PolicyViolation`, `SessionPhaseChange`, `RuntimeReport`, `ApprovalDecision`).

**Assurance:** `policy.violation`, `runtime.report`, and `approval.granted`/`approval.denied` carry `scrutineer.audit.assurance` (`controller` | `self-reported` | `observed`, mirroring `api/v1alpha1.EvidenceAssurance`) so audit consumers know how much to trust each record. Cooperative sidecar evidence is `self-reported` (empty violation assurance is normalized to `self-reported`); control-plane approval decisions are `controller`. See [`phase-3-runtime-reporter-contract.md`](phase-3-runtime-reporter-contract.md) §5.

## Enable flags (`cmd/main.go`)

| Flag | Default | Effect |
|------|---------|--------|
| `--metrics-bind-address` | `:8080` | Prometheus `/metrics` bind (metrics always on). |
| `--otel-exporter-otlp-endpoint` | _empty_ | OTLP/HTTP trace endpoint; **empty disables trace export** (propagation still active). |
| `--otel-service-name` | `scrutineer-controller` | `service.name` resource attribute (traces + audit logs). |
| `--otel-exporter-otlp-insecure` | `true` | Disable TLS verification for the trace exporter. |
| `--audit-log-otlp-endpoint` | _empty_ | OTLP/HTTP log endpoint; **empty disables audit export**. |
| `--audit-log-otlp-insecure` | `true` | Disable TLS verification for the audit log exporter. |

## Invariants

- Metric names are stable API; renames are breaking. Prefer adding over renaming.
- Keep label cardinality bounded — never label by session name, target, domain, or other unbounded values.
- Traces and audit logs are independent opt-ins; neither failing export ever blocks or fails reconciliation.
- The propagator is installed even when trace export is off (so sidecars can still continue traces once export is enabled).
- Telemetry exposes existing state only; it must not become a side channel for control decisions.

## Follow-ups (tracked in [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues))

- Surface `assuranceLevel` in the **future UI** evidence views (audit records now carry `scrutineer.audit.assurance`; see Runtime evidence integrity).
