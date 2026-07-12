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

* [Design docs](design/index.md) - Canonical architecture and design: doctrine, phases, chokepoints, evidence model.

# Reference

* [The AgentSession CRD](reference/agentsession-crd.md) - Spec fields, cancellation, RuntimeProfile/AgentPolicy semantics, status fields, injected env vars.
* [AgentSession Controller Reference](reference/controller-reference.md) - Reconcile triggers/flow, validation, conditions, events, phase mapping, capability quick reference.
* [Egress Enforcement — Guarantees & Assumptions](reference/egress-guarantees.md) - Exactly what the envoy backend guarantees and the assumptions those guarantees rest on.
* [Development Environment & Local Runs](reference/dev-environment.md) - Devcontainer, dev Makefile targets, pinned tool versions, host-side walkthrough, acceptance checklist.
* [CI Tiers](reference/ci.md) - Which workflows run when, path scoping, first-party image policy, failure diagnostics.

# Guides

* [Egress Governance Demo](demo.md) - Guided two-session demo (`make demo`): live deny at the chokepoint, bypass attempt killed by the lock, observed evidence.
* [Non-HTTP Egress via CONNECT](egress-non-http.md) - Reaching non-HTTP TCP services (databases, SSH, brokers) through the per-session Envoy proxy.

# Playbooks

* [GitHub Project Board Setup](github-project-board-setup.md) - One-time setup of the Projects v2 board over Scrutineer issues.

# Templates

* [Component README Template](templates/component-readme.md) - Skeleton for component READMEs, starting with the required OKF frontmatter block.
