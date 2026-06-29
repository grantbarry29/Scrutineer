# tool-gateway

Scrutineer's tool-call enforcement sidecar. A minimal HTTP endpoint that in-pod agents call
before invoking a tool, so per-session tool policy (including mid-execution human
approval) is evaluated and reported at runtime.

## Purpose

Data-plane component for the **tool** enforcement domain. The controller injects it into
the agent pod; the agent submits each intended tool call and the gateway authorizes it
against the session's tool policy, holding for a human decision when required. Policy is
declared in the control plane; this binary only enforces and reports.

## Responsibilities / Non-responsibilities

- **Does:** evaluate tool calls against allowed/denied tools, max call counts/rate, and
  argument rules; allow (`200`) or deny (`403`); run the **mid-execution approval hold**
  for `requireHumanApproval` tools; report non-allow decisions and resolved approvals.
- **Does not:** execute the tool itself (the agent does, only after `200`), decide
  policy, or store decisions. It never sends raw argument values across the
  control-plane boundary — only a redacted `sha256` digest.

## Entry point & execution model

- Entry: [`cmd/tool-gateway/main.go`](./main.go) → `toolgateway.LoadRuntimeEnv()` +
  `toolgateway.Gateway` served by a long-running `http.Server`; SIGINT/SIGTERM shutdown.
- Long-lived sidecar in the agent pod (one per session).

## Control / data flow

Agent → `POST /v1/tools/invoke` (`tool`, `server`, `method`, `requestId`, `arguments`)
→ `EvaluateTool` → allowed `200` / denied `403`. For an approval-required outcome
(enforced mode): register an `ApprovalRequest` via the reporter approval channel and
**bounded long-poll** (default 25s, 1s interval) — granted→`200`, denied/expired→`403`,
still pending→`202` with `Retry-After` + `approvalId` so the agent re-invokes
(idempotent by `requestId`). Fails **closed** when no approval channel is configured.

## Major internal packages / directories

Core logic: [`internal/enforcement/toolgateway`](../../internal/enforcement/toolgateway)
(`gateway.go` HTTP handler + approval hold, `evaluate.go`/`argconstraints.go` policy,
`config.go`/`types.go`/`runtime_env.go` env, `report.go`/`reporter_client.go` evidence
+ approval channel).

## Repository dependencies & related components

- Injected + env-wired by [`internal/controller/job/sidecars.go`](../../internal/controller/job/sidecars.go).
- Approval/evidence channel served by [`internal/reporter`](../../internal/reporter)
  (`approvals.go`); approvals modeled by the `ApprovalRequest` CRD.
- Shares the enforcement contract in [`internal/enforcement`](../../internal/enforcement).
- Design: [`docs/design/phase-3-tool-gateway-contract.md`](../../docs/design/phase-3-tool-gateway-contract.md),
  [`docs/design/phase-3-tool-argument-constraints.md`](../../docs/design/phase-3-tool-argument-constraints.md),
  [`docs/design/phase-5-runtime-tool-approval.md`](../../docs/design/phase-5-runtime-tool-approval.md).

## Interfaces & artifacts

- **Endpoint:** `POST /v1/tools/invoke` on `SCRUTINEER_TOOL_GATEWAY_LISTEN`
  (default `127.0.0.1:19090`).
- **Required env:** `SCRUTINEER_SESSION_NAMESPACE`, `SCRUTINEER_SESSION_NAME`, `SCRUTINEER_REPORTER_URL`,
  `SCRUTINEER_REPORTER_TOKEN_PATH`.
- **Policy env:** `AGENT_POLICY_MODE` (default `audit-only`),
  `AGENT_POLICY_ALLOWED_TOOLS` / `AGENT_POLICY_DENIED_TOOLS`,
  `AGENT_POLICY_REQUIRE_HUMAN_APPROVAL`, `AGENT_POLICY_MAX_TOOL_CALLS`,
  `AGENT_POLICY_MAX_TOOL_CALLS_PER_MINUTE`, and `AGENT_POLICY_ARGUMENT_RULES` (JSON).
- **Image:** `ghcr.io/grantbarry29/scrutineer-tool-gateway` (`toolgateway.DefaultToolGatewayImage`),
  distroless `nonroot` (UID 65532).

## Invariants & files that must change together

- **Cooperative, not tamper-proof:** shares the agent pod + ServiceAccount; evidence is
  `self-reported`. Argument values stay in-pod; only the digest crosses the boundary.
- **Fail closed:** approval-required calls must deny when the channel is unreachable.
- Keep in sync: `internal/enforcement/toolgateway/{types,config,runtime_env}.go` ↔
  injection/env wiring in `internal/controller/job/sidecars.go` ↔ the reporter approval
  endpoints in `internal/reporter` ↔ `Dockerfile.tool-gateway` ↔ the
  `docker-build-tool-gateway` / `kind-load-tool-gateway` Makefile targets.

## Build / test / run / validate

`make docker-build-tool-gateway`, `make kind-load-tool-gateway`; `make test` (unit);
`make test-e2e-images && make test-e2e` for live tool/approval specs.

## Operability

No health endpoint; readiness is process-up. Logs a startup line with listen addr,
session, and mode. Common failure modes: missing required env (fatal), reporter/approval
channel unreachable (approval-required calls fail closed). TODO: verify whether metrics
are exported.
