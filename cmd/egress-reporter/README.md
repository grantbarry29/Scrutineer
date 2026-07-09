# egress-reporter

Tails the per-session Envoy egress proxy's JSON access log and submits each entry as
runtime egress evidence to the controller-owned reporter. It runs **beside Envoy in the
egress-proxy pod** — *not* in the agent pod — so its evidence originates outside the
agent's trust domain and is stamped **`observed`** by the reporter (Slice C,
[#62](https://github.com/grantbarry29/scrutineer/issues/62); design:
[`docs/design/evidence-integrity.md`](../../docs/design/evidence-integrity.md)).

## Behavior

- Long-running: polls the access-log file (default `/var/log/envoy/access.json`, the
  shared emptyDir written by the Envoy bootstrap) every 2s, parses JSON lines
  (`envoy.ParseAccessLogLine`), and POSTs batches (≤128) of `type: network` runtime
  decisions to `POST /v1/report`.
- Classifies each observed authority against the effective FQDN policy (`AGENT_POLICY_*`
  env, shared `enforcement.MatchDomain`), so decisions carry allow / deny (enforced) /
  dry-run (audit) + reason — the same policy the Envoy RBAC enforces, so evidence and
  enforcement agree (#32).
- Delivery is **at-least-once**: offsets are in-memory, so a restart re-reads the file;
  the controller's status merge dedups (times are pinned from Envoy's `%START_TIME%`).
  Reads are chunk-bounded (256KiB) and gated on delivery (#97): a backlog — restart
  catch-up or a reporter outage — waits in the file, not in memory, so the log growing
  to its 256Mi emptyDir limit cannot OOM this 128Mi-capped container. Failed submits
  retry next poll; the pending queue stays bounded (oldest dropped + logged).
- On SIGTERM it makes a final best-effort drain so evidence written just before session
  teardown still lands.

## Evidence & identity contract

- Authenticates with the egress-proxy pod's **dedicated per-session ServiceAccount**
  token (projected, audience `scrutineer-reporter`). The reporter authorizes that pod
  identity via its AgentSession owner reference and only then stamps `observed`
  (`internal/reporter`); assurance is never taken from the payload.
- `observed` means "independent of the agent", **not** tamper-proof — the boundaries in
  the evidence-integrity design (§5) apply (unprivileged pods, CNI-enforced routing lock,
  node not compromised).

## Configuration (env)

| Var | Meaning |
|-----|---------|
| `SCRUTINEER_SESSION_NAME` / `SCRUTINEER_SESSION_NAMESPACE` | Session the evidence targets |
| `SCRUTINEER_REPORTER_URL` | Reporter base URL |
| `SCRUTINEER_REPORTER_TOKEN_PATH` | Projected SA token file |
| `SCRUTINEER_ACCESS_LOG_PATH` | Optional; defaults to `/var/log/envoy/access.json` |
| `SCRUTINEER_METRICS_ADDR` | Optional Prometheus `/metrics` bind; defaults to `:9903` (`envoy.ReporterMetricsPort`); `disabled` turns it off |
| `AGENT_POLICY_MODE` | `enforced` ⇒ denials classified `deny`; otherwise `dry-run` |
| `AGENT_POLICY_ALLOWED_DOMAINS` / `AGENT_POLICY_DENIED_DOMAINS` | CSV FQDN policy (exact or `*.` wildcard) classified per observed authority |

## Operability

Serves Prometheus metrics on `:9903` `/metrics` (container port `metrics`, #55):
decisions by action, malformed access-log lines, submissions by outcome + latency, and
dropped decisions (pending-queue overflow = evidence lost). Dedicated registry in
[`internal/enforcement/egressmetrics`](../../internal/enforcement/egressmetrics/); a
metrics bind failure is logged and never stops the evidence pipeline. The Envoy container
beside it exposes `envoy_*` stats on `:9902` `/stats/prometheus` (stats-only listener;
the admin API stays loopback). Catalog:
[`docs/design/phase-4-observability-export.md`](../../docs/design/phase-4-observability-export.md).

## Files that change together

`cmd/egress-reporter/main.go` → core logic in `internal/enforcement/envoy`
(`accesslog.go` parser + `tailer.go`; the bootstrap's `json_format` keys must stay in
sync with `AccessLogEntry`) → metrics in `internal/enforcement/egressmetrics` (wired via
the Tailer's `OnDecision`/`OnMalformed` hooks + wrapped `Submit`; port consts in
`envoy.go` ↔ container ports in `resources.go` ↔ the observability-export catalog) →
pod wiring in `internal/enforcement/envoy/resources.go` +
`internal/controller/agentsession/egress_envoy.go` → `Dockerfile.egress-reporter` →
`docker-build-egress-reporter` / `kind-load-egress-reporter` Makefile targets (and
`test-e2e-images`).

## Build / test / validate

`make test` (unit); `make docker-build-egress-reporter` / `kind-load-egress-reporter`;
live path via the networking e2e suite (`make test-e2e-net`).
