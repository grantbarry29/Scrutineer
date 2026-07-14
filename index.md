# Scrutineer Knowledge Map

The agent-facing index of this repository's knowledge. Humans: start at
[README.md](README.md). Harness entry point and always-on rules: [CLAUDE.md](CLAUDE.md).
All knowledge is [OKF v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
markdown: frontmatter declares each doc's `type`, `status`, `description`, and
`read_when` — filter on `status: historical` before trusting a design doc.

# Start here

* [Scrutineer Architecture & Design](docs/design/architecture.md) - Whole-project architecture: control/data-plane split, CRD model, lifecycle, reconciliation, policy/evidence model, code map, invariants.
* [Agent rules](dev-agent-rules/index.md) - Always-on and path-scoped engineering rules (`applies_to` globs).
* [Docs bundle](docs/index.md) - Design docs, guides, playbooks, templates.

# Reference

* [The AgentSession CRD](docs/reference/agentsession-crd.md) - User-facing CRD reference: spec fields, cancellation, reference scoping, RuntimeProfile and AgentPolicy semantics, an inline sample, status fields, and the injected environment variables.
* [AgentSession Controller Reference](docs/reference/controller-reference.md) - The full controller behavior catalog: reconcile triggers and flow, validation, task/policy/profile resolution, Job lifecycle, phase mapping, conditions, Kubernetes events, inspection commands, and the shipped-capability quick reference.
* [Egress Enforcement — Guarantees & Assumptions](docs/reference/egress-guarantees.md) - Exactly what the envoy enforcement backend guarantees (proxy-only egress, untamperable chokepoint, FQDN + CIDR policy, independent observed evidence) and the assumptions those guarantees rest on.
* [Development Environment & Local Runs](docs/reference/dev-environment.md) - The devcontainer (recommended), dev-cluster Makefile targets, pinned tool versions, the step-by-step host-side MVP walkthrough with samples, in-cluster controller deployment, and the sample-verified acceptance checklist.
* [CI Tiers](docs/reference/ci.md) - Which workflows run when: Lint/Test always, cluster-heavy E2E + Quickstart Smoke skip docs-only pushes, Nightly Networking cross-checks Calico/dual-stack, Release Smoke is the post-publish gate on the published ghcr images; pre-release cluster jobs build first-party images from the checkout and all dump diagnostics on failure.

# Component READMEs

* [agentsession controller](internal/controller/agentsession/README.md) - Core control-plane controller: reconciles AgentSession CRs into a governed runtime workload and tracks observed governance status; compiled into the manager binary.
* [job builder](internal/controller/job/README.md) - Builds and compares runtime objects for AgentSessions; BuildPodTemplateSpec is the single source of the agent pod shape consumed by both runtime backends.
* [reporter](internal/reporter/README.md) - Runtime-evidence and approval HTTP service: ingests data-plane evidence (self-reported and observed) and serves the per-tool approval channel.
* [egress-reporter](cmd/egress-reporter/README.md) - Tails the per-session Envoy JSON access log and submits each entry as observed egress evidence to the controller-owned reporter; runs beside Envoy in the egress-proxy pod, outside the agent's trust domain.
* [Demo Manifests](config/samples/demo/README.md) - Self-contained manifests for the guided egress-governance demo, applied together by make demo.

# Task state

* [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues) - The only task tracker; repo markdown never holds task state.
* [Project board](https://github.com/users/grantbarry29/projects/1) - Kanban view over the issues (project #1).
