---
type: Template
title: Component README Template
description: "Reusable skeleton for component READMEs — copy, fill, delete inapplicable sections. Starts with the required OKF frontmatter block."
status: live
read_when: "Creating or restructuring a component README."
---

# <Component name>

> Reusable template for a Scrutineer component README. Copy this into the component's
> directory as `README.md`, fill in what applies, and **delete sections that don't**.
> Keep it concise and scannable — architectural intent, boundaries, and invariants,
> not a line-by-line code summary. Infer only from code, build files, manifests,
> config, tests, and `docs/design/`. Never invent: mark anything unverified as
> `TODO: verify`. Delete this quote block in the real README.

Every component README **begins with this OKF frontmatter block** (required by
`dev-agent-rules/component-docs.md`; enforced by `make lint-docs`):

```yaml
---
type: Component README
title: <component name>
description: <one line — what this component is and where it sits>
status: live
read_when: "Working in <component path>/."
---
```

One or two sentences: what this component is and why it exists.

## Purpose

What problem this component solves and where it sits in Scrutineer (control plane vs. data
plane; binary vs. in-process subsystem).

## Responsibilities / Non-responsibilities

- **Does:** the few things this component owns.
- **Does not:** explicit boundaries (what callers/other components own instead).

## Entry point & execution model

- Entry point (e.g. `cmd/<x>/main.go`, or the package's exported constructor / `SetupWithManager`).
- How it runs: long-running controller / HTTP server / sidecar / one-shot, leader election, reconcile loop, etc.

## Control / data flow

High-level flow (a few bullets or a small diagram). E.g. what triggers it, what it
calls, what it writes. Prefer cross-component relationships over internals.

## Major internal packages / directories

- `path/` — one line each, only the non-obvious ones.

## Repository dependencies & related components

- Depends on: `internal/...`, `api/v1alpha1`, other components (repo-relative links).
- Related: link the relevant [`docs/design/`](../../docs/design/) doc(s).

## Interfaces & artifacts

Only what applies: CLI flags, env vars, ports, HTTP endpoints, CRDs/API types,
schemas, emitted Kubernetes events, generated artifacts (CRD YAML, deepcopy, RBAC),
and the command that regenerates them.

## Invariants & files that must change together

Non-obvious rules and ownership (e.g. "the reconciler — not the backend — owns
status"), and sets of files that must be edited in the same change (e.g. a Dockerfile
↔ its `make` target ↔ sidecar injection site; an API marker ↔ regenerated CRD).

## Build / test / run / validate

The component-specific commands (e.g. `make docker-build-<x>`, `make kind-load-<x>`,
`make test`, `make test-e2e`, `make manifests`, `make verify-samples`).

## Operability

Where applicable: health/readiness, metrics names, key log lines, and common
operational failure modes.
