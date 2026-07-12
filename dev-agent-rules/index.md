---
okf_version: "0.1"
okf_spec_rev: "ee67a5ca27044ebe7c38385f5b6cffc2305a9c1a"
---

# Scrutineer Agent Rules

Every rule's frontmatter declares `read_when`, `always_load`, and — for scoped rules —
`applies_to` path globs. A rule applies to a change when any changed path matches its
`applies_to`; a path can match several rules.

# Always-on rules (load at the start of every task)

* [Devcontainer](devcontainer.md) - All build/test/codegen runs inside the provided devcontainer (pinned Go 1.23 + tools), never on the host. Host runs of make/codegen/envtest silently break the toolchain and produce false "bugs".
* [Test-Driven Development](test-driven-development.md) - Build features test-first and environment-first: the matching test level (unit/envtest/e2e) must be runnable before feature code, and correctness only a running artifact can prove needs an e2e test.
* [Scrutineer Product Vision](scrutineer-product-vision.md) - Product direction, threat model, enforcement doctrine, and scope boundaries — a governance control plane, not an orchestrator or agent framework.
* [Task Management](task-management.md) - GitHub Issues/Projects are the sole source of task state; repo markdown holds durable technical context. How to pick up, file, work, and close tasks — and keep the board live.
* [Component Docs](component-docs.md) - Every independently built/deployed/operated component keeps a concise local README (with OKF frontmatter); create or update it in the same change as the code.

# Scoped rules (load when `applies_to` matches your changed paths)

* [Kubernetes Controller](kubernetes-controller.md) - Strict Kubernetes controller/reconciler standards — idempotency, finalizers, status, retries, RBAC, tests. Applies to: `internal/controller/**/*.go`, `cmd/**/*.go`.
* [CRD / API Design](crd-api-design.md) - Strict CRD/API design standards — declarative spec, rich status, validation, immutability, versioning, safe defaults. Applies to: `api/**/*.go`, `config/crd/**/*.yaml`, `config/samples/**/*.yaml`.
* [Distributed Systems & Networking](distributed-systems-networking.md) - Strict distributed-systems and networking standards — partial failure, timeouts, idempotency, fail-closed policy, deterministic rules. Applies to: `internal/**/*.go`, `cmd/**/*.go`.
* [Component Binaries](component-binaries.md) - Build/deploy conventions for Scrutineer's independently built binaries (manager + egress-reporter) and what their component READMEs must cover. Applies to: `cmd/**`, `internal/enforcement/**`, `Dockerfile*`.
* [Scrutineer Design Docs — When To Read](scrutineer-design-docs.md) - Routes non-trivial work to the matching design doc in docs/design/ — read the specific doc during planning, do not paste whole docs into context. Applies to: `api/v1alpha1/**`, `internal/controller/**`, `internal/enforcement/**`, `internal/policy/**`, `internal/reporter/**`.

# Workflow

* [Scrutineer Agent Workflow](scrutineer-workflow.md) - Implementation contract, scope rules, task sizing, the Issue Body Template, and the End-of-Task Handoff Protocol for agent work on Scrutineer.
