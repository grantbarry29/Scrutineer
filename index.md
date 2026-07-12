# Scrutineer Knowledge Map

The agent-facing index of this repository's knowledge. Humans: start at
[README.md](README.md). Harness entry point and always-on rules: [CLAUDE.md](CLAUDE.md).
All knowledge is [OKF v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
markdown: frontmatter declares each doc's `type`, `status`, `description`, and
`read_when` — filter on `status: historical` before trusting a design doc.

# Start here

* [Scrutineer Architecture & Design](docs/design/architecture.md) - Whole-project architecture and the invariants every change must preserve.
* [Agent rules](dev-agent-rules/index.md) - Always-on and path-scoped engineering rules (`applies_to` globs).
* [Docs bundle](docs/index.md) - Design docs, guides, playbooks, templates.

# Component READMEs

* [agentsession controller](internal/controller/agentsession/README.md) - Core control-plane controller reconciling AgentSession CRs.
* [job builder](internal/controller/job/README.md) - Single source of the agent pod shape for both runtime backends.
* [reporter](internal/reporter/README.md) - Runtime-evidence and approval HTTP service.
* [egress-reporter](cmd/egress-reporter/README.md) - Observed-evidence producer beside Envoy in the egress-proxy pod.
* [Demo manifests](config/samples/demo/README.md) - Self-contained manifests for `make demo`.

# Task state

* [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues) - The only task tracker; repo markdown never holds task state.
* [Project board](https://github.com/users/grantbarry29/projects/1) - Kanban view over the issues (project #1).
