# egress-reporter

Tails the per-session Envoy egress proxy's JSON access log and submits each entry as
runtime egress evidence to the controller-owned reporter. It runs **beside Envoy in the
egress-proxy pod** — *not* in the agent pod — so, unlike the cooperative in-pod sidecars
(dns-proxy/tool-gateway/fs-gateway), its evidence originates outside the agent's trust
domain and is stamped **`observed`** by the reporter (Slice C,
[#62](https://github.com/grantbarry29/scrutineer/issues/62); design:
[`docs/design/evidence-integrity.md`](../../docs/design/evidence-integrity.md)).

## Behavior

- Long-running: polls the access-log file (default `/var/log/envoy/access.json`, the
  shared emptyDir written by the Envoy bootstrap) every 2s, parses JSON lines
  (`envoy.ParseAccessLogLine`), and POSTs batches (≤128) of `type: network` runtime
  decisions to `POST /v1/report`.
- Delivery is **at-least-once**: offsets are in-memory, so a restart re-reads the file;
  the controller's status merge dedups (times are pinned from Envoy's `%START_TIME%`).
  Failed submits retry next poll; the pending queue is bounded (oldest dropped + logged).
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

## Files that change together

`cmd/egress-reporter/main.go` → core logic in `internal/enforcement/envoy`
(`accesslog.go` parser + `tailer.go`; the bootstrap's `json_format` keys must stay in
sync with `AccessLogEntry`) → pod wiring in `internal/enforcement/envoy/resources.go` +
`internal/controller/agentsession/egress_envoy.go` → `Dockerfile.egress-reporter` →
`docker-build-egress-reporter` / `kind-load-egress-reporter` Makefile targets (and
`test-e2e-images`).

## Build / test / validate

`make test` (unit); `make docker-build-egress-reporter` / `kind-load-egress-reporter`;
live path via the networking e2e suite (`make test-e2e-net`).
