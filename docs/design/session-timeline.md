---
type: Design Doc
title: Session Timeline Projection Model
description: "Normalizes status.events[] into stable, UI-ready timeline entries (internal/observability) — sorting, severity, titles, and filter semantics for future UI/API consumers."
status: implemented
read_when: "UI timeline projection over status.events[]."
---

# Session Timeline Projection Model

> **Note:** Pure projection over `status.events[]` for future UI and API consumers.

## Purpose

`status.events[]` is the durable runtime stream ([`session-events.md`](session-events.md)). The **timeline model** normalizes that stream into stable, UI-ready entries so operational surfaces do not reimplement sorting, severity, titles, or filter semantics.

```
status.events[]  →  observability.ProjectTimeline  →  []TimelineEntry
                              ↓
                    FilterTimeline / GroupByCategory
```

## Projection (`TimelineEntry`)

| Field | Description |
|-------|-------------|
| `id` | Stable list key; prefers `eventId`, else composite of type/source/action/target/time |
| `time` | Observation timestamp from `SessionEvent.time` |
| `category` | Same as `SessionEvent.type` (`policy`, `network`, `tool`, `lifecycle`, `system`) |
| `severity` | `info` \| `warning` \| `critical` — derived from type + action |
| `title` | Short headline for timeline rows |
| `detail` | Body text (`message` when set, else synthesized from action/target/source) |
| `source`, `action`, `target`, `eventId` | Pass-through from `SessionEvent` |

Implementation: `internal/observability/timeline.go`.

## Sort order

`ProjectTimeline` returns entries **ascending by time** (oldest first). Ties break on `id`. Events with zero `time` sort after timed events.

Events **without `type`** are skipped (invalid for stored status; reporters reject them at ingest).

## Severity rules

| Condition | Severity |
|-----------|----------|
| `action` is `deny`, `block`, or `blocked` | `critical` |
| `action` is `dry-run`, `audit`, `would-deny`, or `truncate` | `warning` |
| `type` is `system` (other actions) | `warning` |
| default | `info` |

## Title synthesis (examples)

| Type | Action | Target | Title |
|------|--------|--------|-------|
| `network` | `deny` | `evil.example.com` | `Network deny: evil.example.com` |
| `tool` | `allow` | `shell` | `Tool allow: shell` |
| `system` | `truncate` | — | `Event history truncated` |
| `lifecycle` | `start` | — | `Lifecycle: start` |

## Filtering and grouping

- **`FilterTimeline(entries, categories, severities)`** — optional category and/or severity filters; nil/empty filter lists mean no restriction on that axis.
- **`GroupByCategory(entries)`** — map of category → entries, preserving per-category order from the input slice.

## Non-goals (this slice)

- HTTP API or CRD changes
- Web UI components
- Auto-deriving timeline entries from `policyDecisions` or `violations` (reporters/events remain the source)
- Prometheus metrics

## Related

- [`session-events.md`](session-events.md) — event schema and ingestion
- [`architecture.md`](architecture.md) — observability as a product surface
