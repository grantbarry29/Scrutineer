# Phase 3 Tool Gateway Contract

Relay governs MCP and tool calls through a **tool gateway** data-plane component. Phase 3 defines the contract in `internal/enforcement/toolgateway/`; the first-party sidecar image (`ghcr.io/secureai/relay-tool-gateway:latest`, `cmd/tool-gateway`, `Dockerfile.tool-gateway`) implements the MVP HTTP invoke API and reporter client.

## Role

- Agents route tool/MCP traffic to an in-pod gateway (`http://127.0.0.1:19090` by default).
- The gateway evaluates each request against `status.effectivePolicy` tool rules and effective mode.
- Outcomes are reported back through `enforcement.RuntimeReport` → `ApplyRuntimePolicyReport`.

## Request metadata (`toolgateway.ToolRequest`)

| Field | Purpose |
|-------|---------|
| `tool` | Stable tool identifier (required) |
| `server` | MCP server / provider id (optional) |
| `method` | MCP method name when distinct from tool (optional) |
| `requestID` | Correlation id for logs and traces (optional) |

## Authorization (`toolgateway.EvaluateTool`)

Evaluates `allowedTools`, `deniedTools`, and `requireHumanApproval` against effective policy mode:

| Mode | Denied / not-allowed tool | Allowed tool |
|------|---------------------------|--------------|
| `enforced` | Block (`deny`) | Allow |
| `dry-run` | Allow through, record `dry-run` | Allow |
| `audit-only` | Allow through, record `audit` | Allow |

**Not enforced in slice 6:** `maxToolCalls`, `maxCallsPerMinute` (rate limits need gateway state). Human approval is surfaced as `ApprovalRequired` but full gates are Phase 5.

## Reporting (`toolgateway.RuntimeReport`)

Produces `phase: runtime` policy decisions and violations (for `deny` / `dry-run`) suitable for `agentsession.ApplyRuntimePolicyReport`.

## Control-plane config (`toolgateway.GatewayConfig`)

`enforcement.Backend` implementation returns `GatewayConfig` when tool policy hints exist or a `tool-gateway` sidecar is enabled on the matched RuntimeProfile. The reconciler does not consume this yet; sidecar injection (slice 5) and gateway images wire it later.

## Sidecar HTTP API (MVP)

- Listen: `127.0.0.1:19090` (override via `RELAY_TOOL_GATEWAY_LISTEN`).
- `POST /v1/tools/invoke` with JSON `{"tool":"read_file",...}` — evaluates policy, returns `403` on enforced deny, posts runtime evidence to `POST /v1/report`.
- Agents use `RELAY_TOOL_GATEWAY_URL=http://127.0.0.1:19090` (injected on the agent container).

## Live evidence path (e2e)

With `relay-controller-reporter` reachable from session pods (`make deploy` or e2e in-cluster reporter), enforced tool denies flow:

1. Agent calls `POST ${RELAY_TOOL_GATEWAY_URL}/v1/tools/invoke` with `{"tool":"kubectl"}`.
2. Tool-gateway evaluates policy, returns `403`, POSTs to `/v1/report`.
3. Controller merges into `status.policyDecisions` and `status.violations`.

E2e: `test/e2e/tool_violation_test.go` (requires `make test-e2e-images`).

## Implementation

See [`internal/enforcement/toolgateway/`](../internal/enforcement/toolgateway/) and [`cmd/tool-gateway/`](../../cmd/tool-gateway/).
