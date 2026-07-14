---
okf_version: "0.1"
okf_spec_rev: "ee67a5ca27044ebe7c38385f5b6cffc2305a9c1a"
---

# Scrutineer Docs Bundle

Agent-primary knowledge in [OKF v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
format (`okf_spec_rev` above pins the spec revision this bundle conforms to). Humans
start at the repository [README](../README.md); agents start at the repository-root
[knowledge map](../index.md).

# Design

* [Design docs](design/index.md) - Canonical architecture and design: doctrine, chokepoints, evidence model, deferred designs.

# Reference

* [The AgentSession CRD](reference/agentsession-crd.md) - User-facing CRD reference: spec fields, cancellation, reference scoping, RuntimeProfile and AgentPolicy semantics, an inline sample, status fields, and the injected environment variables.
* [AgentSession Controller Reference](reference/controller-reference.md) - The full controller behavior catalog: reconcile triggers and flow, validation, task/policy/profile resolution, Job lifecycle, phase mapping, conditions, Kubernetes events, inspection commands, and the shipped-capability quick reference.
* [Egress Enforcement — Guarantees & Assumptions](reference/egress-guarantees.md) - Exactly what the envoy enforcement backend guarantees (proxy-only egress, untamperable chokepoint, FQDN + CIDR policy, independent observed evidence) and the assumptions those guarantees rest on.
* [Development Environment & Local Runs](reference/dev-environment.md) - The devcontainer (recommended), dev-cluster Makefile targets, pinned tool versions, the step-by-step host-side MVP walkthrough with samples, in-cluster controller deployment, and the sample-verified acceptance checklist.
* [CI Tiers](reference/ci.md) - Which workflows run when: Lint/Test always, cluster-heavy E2E + Quickstart Smoke skip docs-only pushes, Nightly Networking cross-checks Calico/dual-stack, Release Smoke is the post-publish gate on the published ghcr images; pre-release cluster jobs build first-party images from the checkout and all dump diagnostics on failure.

# Guides

* [Egress Governance Demo](demo.md) - Guided two-session demo (make demo): a denied request rejected live at the per-session Envoy chokepoint, a bypass attempt killed by the routing lock, all recorded as observed evidence.
* [Non-HTTP Egress via CONNECT](egress-non-http.md) - Operator guide: reaching non-HTTP TCP services (databases, SSH, brokers) through the per-session Envoy egress proxy with CONNECT tunnels.

# Playbooks

* [GitHub Project Board Setup](github-project-board-setup.md) - One-time setup of the GitHub Projects v2 board over Scrutineer issues (project #1), including PAT scope gotchas.

# Templates

* [Component README Template](templates/component-readme.md) - Reusable skeleton for component READMEs — copy, fill, delete inapplicable sections. Starts with the required OKF frontmatter block.
