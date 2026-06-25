# fs-gateway

Relay's file-access enforcement sidecar. A minimal HTTP endpoint that in-pod agents call
before touching a workspace path, so per-session file policy is evaluated and reported at
runtime.

> Note: the binary is `fs-gateway` but its core logic package is
> [`internal/enforcement/workspace`](../../internal/enforcement/workspace) (not `fsgateway`).

## Purpose

Data-plane component for the **file/workspace** enforcement domain. The controller
injects it into the agent pod; the agent checks each intended file operation and the
gateway authorizes it against the session's file policy. Policy is declared in the
control plane; this binary only enforces and reports.

## Responsibilities / Non-responsibilities

- **Does:** evaluate a path + operation against allowed/denied paths (and workspace size
  limits); allow (`200`) or deny (`403`); report non-allow decisions to the reporter.
- **Does not:** mediate the actual filesystem I/O (the agent performs it after `200`),
  decide policy, or persist state.

## Entry point & execution model

- Entry: [`cmd/fs-gateway/main.go`](./main.go) → `workspace.LoadRuntimeEnv()` +
  `workspace.Gateway` served by a long-running `http.Server`; SIGINT/SIGTERM shutdown.
- Long-lived sidecar in the agent pod (one per session).

## Control / data flow

Agent → `POST /v1/files/access` (`path`, `operation`) → `EvaluateFile` → allowed `200`
/ denied `403`; non-allow outcomes are submitted to the reporter as self-reported
evidence.

## Major internal packages / directories

Core logic: [`internal/enforcement/workspace`](../../internal/enforcement/workspace)
(`gateway.go` HTTP handler, `evaluate.go` policy, `config.go`/`types.go`/`runtime_env.go`
env + defaults, `report.go`/`reporter_client.go` evidence).

## Repository dependencies & related components

- Injected + env-wired by [`internal/controller/job/sidecars.go`](../../internal/controller/job/sidecars.go).
- Reports to the manager's reporter ([`internal/reporter`](../../internal/reporter)).
- Shares the enforcement contract in [`internal/enforcement`](../../internal/enforcement).
- Design: [`docs/design/phase-3-enforcement-architecture.md`](../../docs/design/phase-3-enforcement-architecture.md),
  [`docs/design/phase-3-file-workspace-policy.md`](../../docs/design/phase-3-file-workspace-policy.md).

## Interfaces & artifacts

- **Endpoint:** `POST /v1/files/access` on `RELAY_FS_GATEWAY_LISTEN`
  (default `127.0.0.1:19191`).
- **Required env:** `RELAY_SESSION_NAMESPACE`, `RELAY_SESSION_NAME`, `RELAY_REPORTER_URL`,
  `RELAY_REPORTER_TOKEN_PATH`.
- **Policy env:** `AGENT_POLICY_MODE` (default `audit-only`),
  `AGENT_POLICY_ALLOWED_PATHS` / `AGENT_POLICY_DENIED_PATHS`, and
  `AGENT_POLICY_MAX_WORKSPACE_BYTES`.
- **Image:** `ghcr.io/secureai/relay-fs-gateway` (`workspace.DefaultFSGatewayImage`),
  distroless `nonroot` (UID 65532).

## Invariants & files that must change together

- **Cooperative, not tamper-proof:** shares the agent pod + ServiceAccount; evidence is
  `self-reported`. It is an advisory check, not a kernel-level mount guard.
- Keep in sync: `internal/enforcement/workspace/{types,config,runtime_env}.go` ↔
  injection/env wiring in `internal/controller/job/sidecars.go` ↔ `Dockerfile.fs-gateway`
  ↔ the `docker-build-fs-gateway` / `kind-load-fs-gateway` Makefile targets.

## Build / test / run / validate

`make docker-build-fs-gateway`, `make kind-load-fs-gateway`; `make test` (unit);
`make test-e2e-images && make test-e2e` for live file-evidence specs.

## Operability

No health endpoint; readiness is process-up. Logs a startup line with listen addr,
session, and mode. Common failure modes: missing required env (fatal), unreachable
reporter (decisions still enforced; evidence dropped). TODO: verify whether metrics are
exported.
