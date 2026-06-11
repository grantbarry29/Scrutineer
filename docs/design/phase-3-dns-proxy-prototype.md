# Phase 3 DNS / Egress Proxy Prototype

Relay enforces domain and CIDR egress policy through an in-pod **dns-proxy** sidecar. Phase 3 slice 7 ships the **contract and configuration propagation**; the first-party **`cmd/dns-proxy`** sidecar image performs HTTP(S) egress evaluation and reports to `POST /v1/report`.

## Role

- NetworkPolicy (slice 3) enforces **CIDR only** at the CNI layer.
- **dns-proxy** sidecars enforce **FQDN + CIDR** at an HTTP(S) egress proxy using effective policy and mode semantics.
- Agents use `HTTP_PROXY` / `HTTPS_PROXY` pointing at `http://127.0.0.1:15053` when a dns-proxy sidecar is enabled.

## Sidecar configuration

When `RuntimeProfile.spec.sidecars[]` includes an enabled `dns-proxy` entry, the Job builder injects:

| Env var | Purpose |
|---------|---------|
| `RELAY_EGRESS_PROXY_LISTEN` | Listen address (`127.0.0.1:15053`) |
| `RELAY_EGRESS_PROXY_HTTP` | HTTP proxy URL for agents |
| `AGENT_POLICY_*` domain/CIDR lists | Effective policy propagation |
| `AGENT_POLICY_MODE` | `audit-only` / `dry-run` / `enforced` |

The agent container receives `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY=localhost,127.0.0.1`.

## Authorization (`dnsproxy.EvaluateEgress`)

Evaluates `allowedDomains`, `deniedDomains`, `allowedCIDRs`, and `deniedCIDRs` with shared mode semantics:

| Mode | Would-deny | Runtime action |
|------|------------|----------------|
| `enforced` | Block | `deny` + violation |
| `dry-run` | Allow through | `dry-run` + violation |
| `audit-only` | Allow through | `audit`, no violation |

## Runtime reporting handshake

1. Sidecar observes egress to `host` (domain or IP).
2. Sidecar evaluates policy locally (`dnsproxy.EvaluateEgress`) and POSTs `RuntimeReport` JSON to `{RELAY_REPORTER_URL}/v1/report` with the projected token (`RELAY_REPORTER_TOKEN_PATH`, audience `relay-reporter`).
3. Controller reporter merges via `ApplyRuntimePolicyReport` into `status.policyDecisions` / `status.violations`.

In-process helper (tests / controller integration):

```go
ApplyEgressProxyRuntimeEvent(session, profile, dnsproxy.RuntimeEvent{Host: "evil.example"}, time.Now())
```

## Implementation

See [`internal/enforcement/dnsproxy/`](../internal/enforcement/dnsproxy/).
