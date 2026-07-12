---
okf_version: "0.1"
okf_spec_rev: "ee67a5ca27044ebe7c38385f5b6cffc2305a9c1a"
---

# Scrutineer Agent Rules

Every rule's frontmatter declares `read_when`, `always_load`, and — for scoped rules —
`applies_to` path globs. A rule applies to a change when any changed path matches its
`applies_to`; a path can match several rules.

# Always-on rules (load at the start of every task)

* [Devcontainer](devcontainer.md) - All build/test/codegen runs inside the provided devcontainer, never on the host.
* [Test-Driven Development](test-driven-development.md) - Test-first and environment-first; runtime-provable behavior needs an e2e test.
* [Scrutineer Product Vision](scrutineer-product-vision.md) - Product direction, threat model, enforcement doctrine, scope boundaries.
* [Task Management](task-management.md) - GitHub Issues/Projects are the sole source of task state; claim one issue before editing.
* [Component Docs](component-docs.md) - Every component keeps a local README (with OKF frontmatter), updated in the same change.

# Scoped rules (load when `applies_to` matches your changed paths)

* [Kubernetes Controller](kubernetes-controller.md) - Controller/reconciler standards. Applies to: `internal/controller/**/*.go`, `cmd/**/*.go`.
* [CRD / API Design](crd-api-design.md) - CRD/API design standards. Applies to: `api/**/*.go`, `config/crd/**/*.yaml`, `config/samples/**/*.yaml`.
* [Distributed Systems & Networking](distributed-systems-networking.md) - Partial failure, timeouts, idempotency, fail-closed policy. Applies to: `internal/**/*.go`, `cmd/**/*.go`.
* [Component Binaries](component-binaries.md) - Build/deploy conventions for independently built binaries. Applies to: `cmd/**`, `internal/enforcement/**`, `Dockerfile*`.
* [Scrutineer Design Docs — When To Read](scrutineer-design-docs.md) - Routes non-trivial changes to the matching design doc. Applies to: `api/v1alpha1/**`, `internal/{controller,enforcement,policy,reporter}/**`.

# Workflow

* [Scrutineer Agent Workflow](scrutineer-workflow.md) - Implementation contract, task sizing, Issue Body Template, End-of-Task Handoff Protocol.
