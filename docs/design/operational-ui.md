---
type: Design Doc
title: Operational UI Vision
description: "Vision for the deferred operational-UI epic (#11): a governance/observability dashboard — never a chatbot — the operational questions it must answer, and the backend surfaces it will consume. A nice-to-have feature epic, not core product."
status: draft
tracking_issue: 11
read_when: "Any operational-UI work (epic #11, design slice #12) — scope, anti-chatbot guardrails, and what the backend owes the UI."
---

# Operational UI Vision

> **Positioning:** the operational UI is a **deferred feature epic** (#11), a
> nice-to-have — not a core piece of the product. Scrutineer's core is the
> governance control plane and its untamperable enforcement chokepoints; a UI
> surfaces that value but adds none of it. This vision was extracted from the
> always-loaded product-vision rule (#130) so it enters agent context only when
> UI work is actually picked up. Sequencing: API-first — a read-only MVP
> (design slice #12) before any write surfaces.

## What it is

A first-class operational UI for visibility, governance, auditability, runtime
observability, approvals, and debugging autonomous AI systems.

## What it must never become

A chatbot UI, ChatGPT clone, conversational frontend, or consumer AI product.
It should feel closer to Kubernetes dashboards, Datadog, Grafana, Argo UI,
Lens, security-operations dashboards, and runtime observability platforms.

Enforcement-strength honesty binds the UI like every other surface
([`untamperable-enforcement.md`](untamperable-enforcement.md)): evidence keeps
its **assurance label**, and the UI must never present self-reported evidence
as independently observed or advisory controls as enforcement.

## Operational questions it answers

What agents are running; what an agent is doing now; which
tools/domains/files/credentials it used; which actions were blocked; why
policy violations happened; what needs approval; which sessions failed; and
what token/tool/network usage occurred.

## Long-term views

Session timelines, live policy and network activity, tool governance, scoped
approvals, runtime topology, audit and forensics, replayable sessions, traces,
violations, usage, and historical analytics.

## What the backend owes the UI

Backend APIs and controllers should be designed for future UI consumption:
emit structured timestamped events, maintain normalized session state, store
policy decisions and violations cleanly, keep status and conditions
consistent, and model observability as a product surface. This constraint
binds backend work **today** and is already encoded in the shipped designs the
UI will consume:

- [`session-events.md`](session-events.md) — `status.events[]`, the durable ordered timeline stream.
- [`session-timeline.md`](session-timeline.md) — stable, UI-ready timeline projection.
- [`observability-export.md`](observability-export.md) — Prometheus / OTel / OTLP audit export.
- [`approval-workflows.md`](approval-workflows.md) — the approval gates a UI would surface as an inbox.
