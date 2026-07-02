# dns-proxy

Scrutineer's network-egress enforcement sidecar. A minimal HTTP(S) forward proxy that the
agent container routes outbound traffic through, so per-session network policy can be
evaluated and reported at runtime.

## Purpose

Data-plane component for the **network** enforcement domain. The controller injects it
into the agent pod and points the agent's `HTTP_PROXY`/`HTTPS_PROXY` at it; the proxy
authorizes each egress connection against the session's network policy and emits
evidence. Control-plane policy lives in the manager; this binary only enforces and
reports.

## Responsibilities / Non-responsibilities

- **Does:** terminate the agent's outbound HTTP/HTTPS (incl. `CONNECT` tunnels),
  evaluate destination host/port against allowed/denied domains and CIDRs, block
  denied egress (`403`), forward allowed egress, and report non-allow decisions to the
  reporter. Domain matching uses the shared `enforcement.MatchDomain` semantics (exact or
  `*.` wildcard), so it agrees with the out-of-pod Envoy egress path (#32).
- **Does not:** decide policy (the controller propagates it via env), persist state,
  authenticate the agent, or provide adversarial-grade isolation (see Invariants).

## Entry point & execution model

- Entry: [`cmd/dns-proxy/main.go`](./main.go) → `dnsproxy.LoadRuntimeEnv()` +
  `dnsproxy.Proxy` served by a long-running `http.Server`; shuts down on SIGINT/SIGTERM.
- Runs as a long-lived sidecar in the agent pod (one per session).

## Control / data flow

Agent → `HTTP_PROXY`/`HTTPS_PROXY` → this proxy → `EvaluateEgress(host:port)` →
allowed forwards upstream / denied returns `403`; non-allow outcomes are submitted to
the reporter (`SCRUTINEER_REPORTER_URL`) as self-reported evidence.

For an allowed `CONNECT`, both tunnel directions are copied concurrently and each
direction half-closes (`CloseWrite`) its peer when its source hits EOF, so a one-sided
close propagates and the tunnel tears down promptly instead of lingering half-open.

## Major internal packages / directories

Core logic: [`internal/enforcement/dnsproxy`](../../internal/enforcement/dnsproxy)
(`proxy.go` handler, `evaluate.go` policy decision, `config.go`/`types.go`/`runtime_env.go`
env + defaults, `report.go`/`reporter_client.go` evidence).

## Repository dependencies & related components

- Injected + env-wired by [`internal/controller/job/sidecars.go`](../../internal/controller/job/sidecars.go).
- Reports to the manager's reporter ([`internal/reporter`](../../internal/reporter)).
- Shares the enforcement contract in [`internal/enforcement`](../../internal/enforcement).
- Design: [`docs/design/phase-3-enforcement-architecture.md`](../../docs/design/phase-3-enforcement-architecture.md),
  [`docs/design/phase-3-dns-proxy-prototype.md`](../../docs/design/phase-3-dns-proxy-prototype.md).

## Interfaces & artifacts

- **Listen:** `SCRUTINEER_EGRESS_PROXY_LISTEN` (default `127.0.0.1:15053`); proxy URL handed
  to the agent via `SCRUTINEER_EGRESS_PROXY_HTTP` (default `http://127.0.0.1:15053`).
- **Required env:** `SCRUTINEER_SESSION_NAMESPACE`, `SCRUTINEER_SESSION_NAME`, `SCRUTINEER_REPORTER_URL`,
  `SCRUTINEER_REPORTER_TOKEN_PATH`.
- **Policy env:** `AGENT_POLICY_MODE` (default `audit-only`),
  `AGENT_POLICY_ALLOWED_DOMAINS` / `AGENT_POLICY_DENIED_DOMAINS` /
  `AGENT_POLICY_ALLOWED_CIDRS` / `AGENT_POLICY_DENIED_CIDRS` (CSV).
- **Image:** `ghcr.io/grantbarry29/scrutineer-dns-proxy` (`dnsproxy.DefaultDNSProxyImage`),
  distroless `nonroot` (UID 65532).

## Invariants & files that must change together

- **Cooperative, not tamper-proof:** shares the agent pod + ServiceAccount; evidence is
  stamped `self-reported`. Do not describe it as adversarial-grade.
- Env propagation must stay in sync across `internal/enforcement/dnsproxy/{types,config,runtime_env}.go`
  ↔ the injection/env wiring in `internal/controller/job/sidecars.go` ↔ `Dockerfile.dns-proxy`
  ↔ the `docker-build-dns-proxy` / `kind-load-dns-proxy` Makefile targets.

## Build / test / run / validate

`make docker-build-dns-proxy`, `make kind-load-dns-proxy`; `make test` (unit);
`make test-e2e-images && make test-e2e` for live egress-evidence specs.

## Operability

No health endpoint; readiness is process-up. Logs a startup line with listen addr,
session, and mode. Common failure modes: missing required env (fatal at start),
unreachable reporter (decisions still enforced; evidence dropped). No Prometheus
metrics are exported (the binary serves only the proxy handler) — tracked in issue #55.
