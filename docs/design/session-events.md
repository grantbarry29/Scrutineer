---
type: Design Doc
title: Structured Session Events API
description: "status.events[] — the durable, ordered, capped runtime timeline stream: schema, ingestion via POST /v1/report, preservation across reconciler status patches."
status: implemented
read_when: "status.events[], timeline ingestion, reporter event payloads."
---

# Structured Session Events API

> **Note:** Populated via `POST /v1/report` `events[]` and preserved across reconciler status patches.

## Purpose

`status.events[]` is the durable, ordered, capped timeline stream for AgentSession runtimes. It complements:

- **Kubernetes Events** — ephemeral, not structured for UI timelines.
- **`status.policyDecisions`** — audit-oriented policy evaluations (merge + runtime).
- **`status.violations`** — flagged deny/dry-run outcomes only.

Events are optimized for **operational visibility**: what happened, when, from which backend, against what target.

## Schema (`SessionEvent`)

| Field | Required | Description |
|-------|----------|-------------|
| `time` | yes (server fills if missing) | Observation timestamp |
| `type` | yes | `policy` \| `network` \| `tool` \| `lifecycle` \| `system` |
| `source` | no (defaults to report `backend`) | e.g. `egress-proxy` |
| `action` | no | Short verb: `allow`, `deny`, `call`, `block`, … |
| `target` | no | Domain, tool name, path |
| `message` | no | Human-readable detail |
| `eventId` | no | Idempotency key unique within the session |

CRD: `api/v1alpha1/agentsession_types.go` — max **256** events per session.

## Ingestion

Reporters send events in `POST /v1/report`:

```json
{
  "session": {"namespace": "team-a", "name": "my-session"},
  "backend": "egress-proxy",
  "events": [{
    "type": "network",
    "action": "deny",
    "target": "evil.example.com",
    "message": "egress blocked",
    "eventId": "net-001"
  }]
}
```

Merge path: `ValidateAndNormalizeReport` → `enforcement.RuntimeReport.Events` → `ApplyRuntimePolicyReport` → `AppendSessionEvents` → `PatchRuntimePolicyReport`.

Per-report cap: **64** events (`MaxEventsPerReport`).

## Merge semantics

- **Append-only** at runtime; reconciler does not replace events.
- **Idempotent** by `eventId` when set; otherwise by composite key `(time, type, source, action, target, message)`.
- **Truncation:** when exceeding 256 entries, oldest dropped and a `system` summary event appended.
- **`patchStatus` / reporter patch:** union-merge with live status so concurrent reporter + reconciler writes do not clobber events (same pattern as `policyDecisions` / `violations`).

## Non-goals (this slice)

- OTLP/SIEM export
- UI timeline rendering
- Auto-deriving events from every `PolicyDecision` (reporters may send both explicitly)

## Related

- [`runtime-reporter-contract.md`](runtime-reporter-contract.md)
- [`architecture.md`](architecture.md) §6.3 status merge strategy
