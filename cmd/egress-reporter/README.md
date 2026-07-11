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
- Classifies each observed authority against the effective egress policy from the
  `AGENT_POLICY_*` env: FQDN rules via the shared `enforcement.MatchDomain` (#32), and
  IPv4/CIDR rules — matched only when the authority is an IPv4 literal — via
  `enforcement.MatchIPCIDR` (#125). Deny order is deniedDomains → deniedCIDRs →
  non-canonical-numeric refusal, then the allow-union (allowed by the domain list OR the
  CIDR list; default-deny when any allow-list exists). When CIDR policy is present, an
  all-numeric authority that isn't a canonical dotted-quad (leading-zero octet, inet_aton
  short form) is refused with reason `NonCanonicalIP` (#126) so a resolver-expanded
  spelling can't evade the CIDR rules. Decisions carry allow / deny (enforced) / dry-run
  (audit) + reason — the same policy the Envoy RBAC enforces, so evidence and enforcement
  agree.
- Delivery is **at-least-once**: offsets are in-memory, so a restart re-reads the file;
  the controller's status merge dedups (times are pinned from Envoy's `%START_TIME%`).
  Reads are chunk-bounded (256KiB) and gated on delivery (#97): a backlog — restart
  catch-up or a reporter outage — waits in the file, not in memory, so the log growing
  to its 256Mi emptyDir limit cannot OOM this 128Mi-capped container. Transiently
  failed submits retry next poll; the pending queue stays bounded (oldest dropped +
  logged).
- Submit failures are classified per the reporter contract §4.4 (#96): permanent
  rejections (400/403/404/413) split or drop the offending decisions (logged +
  `rejected_decisions_total`) instead of retrying the head batch forever; batches are
  capped by encoded bytes (48KiB) as well as count; and the agent-controlled authority
  is truncated (1KiB target / 2KiB message, deterministic) at decision creation — so a
  prompt-injected agent cannot wedge its own evidence pipeline with an oversized
  CONNECT authority.
- A 429 is flow control, not failure (#100): the reporter allows a per-session burst
  of 5 requests then 1/s sustained; the tailer honors the `Retry-After` hint —
  retrying the same batch and keeping the server's pace for the rest of the flush —
  so a backlog drains without evidence loss or error noise. A reporter that keeps
  429ing after its hint was honored surfaces as a transient poll error (bounded
  retries keep the loop live).
- The access log rotates once its fully-ingested size passes the threshold (#98,
  default 64Mi): rename → Envoy admin `/reopen_logs` (loopback, same pod netns) →
  drain the remainder → delete. **Only ingested bytes are ever removed** — flooding
  cannot erase un-ingested evidence; growth beyond ingest still evicts the pod
  (fail closed, surfaced by the controller per #99). Design:
  [`docs/design/access-log-rotation.md`](../../docs/design/access-log-rotation.md).
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
| `SCRUTINEER_ROTATE_AFTER_BYTES` | Optional access-log rotation threshold (#98); defaults to 64Mi (`envoy.DefaultRotateAfterBytes`); set per-pod by the controller from the manager env `SCRUTINEER_EGRESS_ROTATE_AFTER_BYTES` |
| `AGENT_POLICY_MODE` | `enforced` ⇒ denials classified `deny`; otherwise `dry-run` |
| `AGENT_POLICY_ALLOWED_DOMAINS` / `AGENT_POLICY_DENIED_DOMAINS` | CSV FQDN policy (exact or `*.` wildcard) classified per observed authority |
| `AGENT_POLICY_ALLOWED_CIDRS` / `AGENT_POLICY_DENIED_CIDRS` | CSV IPv4/CIDR policy (#125), matched only against IPv4-literal authorities |

## Operability

Serves Prometheus metrics on `:9903` `/metrics` (container port `metrics`, #55):
decisions by action, malformed access-log lines, submissions by outcome
(`ok`/`error`/`rate_limited` — 429 flow control is not an error, #100) + latency,
dropped decisions (pending-queue overflow = evidence lost), rejected decisions
(permanent reporter rejection by HTTP status = evidence lost, #96), and completed
log-rotation cycles (#98). Dedicated registry in
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
