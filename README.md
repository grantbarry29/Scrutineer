# Scrutineer

**A Kubernetes-native governance layer for autonomous AI agents -- enforcement that lives outside the agent, not inside it.**

Autonomous agents are increasingly trusted to run real work inside enterprise
environments, and that trust is exactly the problem: an agent that gets prompt-injected
or simply goes wrong needs guardrails it cannot reach around. Scrutineer is that guardrail
-- the control plane that **governs** an agent while it runs, wrapping execution with
policy, untamperable egress enforcement, audit, observability, and human approval.

It is deliberately **not** a workflow engine.
[Kubernetes Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/),
[Tekton](https://tekton.dev/), [Argo Workflows](https://argoproj.github.io/argo-workflows/),
and [Temporal](https://temporal.io/) already run work well, so Scrutineer runs none of it
itself -- it delegates the actual *running* of the agent to one of them and concentrates
entirely on governance.

The repository is a Kubebuilder operator built around the `AgentSession` CRD, and it is
**bring-your-own-agent**: your image holds the reasoning loop, the model calls, and the
tool use, while Scrutineer schedules that workload (Job or bare Pod), resolves and
propagates reusable policy, and **enforces network egress from outside the agent's trust
domain** -- a per-session, out-of-pod Envoy chokepoint plus a default-deny routing lock the
agent cannot alter. Runtime evidence is written back into status stamped `observed`, audit
and observability signals are exported, and sensitive actions gate behind human approval.
The governing principle is the strongest one available: enforcement ships **untamperable or
not at all** ([decision record](docs/design/untamperable-enforcement.md)) -- where a
guarantee depends on the cluster, Scrutineer proves it empirically and refuses rather than
degrade in silence.

## Quickstart

One command takes you from a fresh clone to a running Scrutineer on a local
[kind](https://kind.sigs.k8s.io/) cluster. You need Docker, kind, and kubectl; the images
are built from your own checkout, so the controller always matches the manifests it is
deployed with.

```sh
make quickstart
```

The first run takes about **5 minutes**, and later runs are much faster. It creates a
dedicated `scrutineer-quickstart` cluster, loads the first-party images, installs the CRDs,
deploys the controller, and prints the **routing-lock verification verdict**: before it will
run an enforced session, Scrutineer proves empirically that the cluster's CNI enforces
NetworkPolicy (*verified-or-refused*). If that verdict comes back `Refused` on your kind
version, retry with `make quickstart-down && make quickstart QUICKSTART_CNI=calico`.

From there, run the guided demo of the untamperable egress path -- the cluster needs
**internet egress** for this one. It shows a denied request rejected live at the per-session
chokepoint, a bypass attempt killed by the routing lock, and `observed` evidence the agent
could not have forged ([walkthrough](docs/demo.md)):

```sh
make demo
```

Tear everything down with `make demo-down` / `make quickstart-down`.

## What you get today

- **Untamperable network egress governance** -- a per-session, out-of-pod Envoy chokepoint
  (FQDN + IP/CIDR allow/deny, enforce or dry-run) behind a default-deny routing lock;
  evidence is stamped `observed` from the proxy pod's own identity, never taken on the
  agent's word. Exact guarantees and the assumptions they rest on:
  [egress guarantees](docs/reference/egress-guarantees.md).
- **Verified-or-refused** -- a differential canary probe proves the CNI actually enforces
  NetworkPolicy, and an enforced session on an unverified cluster refuses to start, loudly.
- **Reusable policy CRDs** (`AgentPolicy`, `RuntimeProfile`) merged into one per-session
  effective policy, with violations surfaced as cluster events and CRD status.
- **Human approval gates** (`ApprovalPolicy` / `ApprovalRequest`) for sensitive actions.
- **Audit + observability** -- Prometheus metrics (control plane and egress data plane),
  OpenTelemetry traces, an OTLP audit sink, session events/timeline, and log/artifact capture.
- **Orchestrator-agnostic execution** -- Kubernetes Jobs or bare Pods behind a
  backend-neutral `runtimeBackend` interface.

**Honest limitations:** tool and file governance are not enforced yet. Scrutineer ships only
enforcement the agent cannot tamper with, so those policy surfaces were removed until their
out-of-pod chokepoints land ([doctrine](docs/design/untamperable-enforcement.md)). There are
two in-cluster orchestrators today (`kubernetes-job`, `kubernetes-pod`), no operational UI
yet, and no per-session identity or multi-tenancy hardening yet.

## Developing

The repo ships a `.devcontainer/` that gives you a fully wired dev environment (Go 1.23,
Docker-in-Docker, a kind cluster with CRDs) with no host setup beyond Docker and VS Code:
open the folder in VS Code, choose *"Reopen in Container"*, and run `make run`. The full
walkthrough -- dev-cluster Makefile targets, pinned tool versions, host-side runs, samples,
and the acceptance checklist -- is the [development environment](docs/reference/dev-environment.md)
reference. For how CI behaves, see [CI tiers](docs/reference/ci.md).

## Documentation

- **Humans start here**, then the [demo walkthrough](docs/demo.md).
- **Agents** start at the [knowledge map](index.md) -- the repo's knowledge base is
  [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)-formatted
  markdown whose frontmatter (`type`, `status`, `read_when`) is machine-routable.
- **Design docs:** [`docs/design/`](docs/design/index.md) -- start with
  [`architecture.md`](docs/design/architecture.md).
- **Reference:** [AgentSession CRD](docs/reference/agentsession-crd.md) ·
  [controller behavior](docs/reference/controller-reference.md) ·
  [egress guarantees](docs/reference/egress-guarantees.md) ·
  [non-HTTP egress how-to](docs/egress-non-http.md).
- **Task state and roadmap:** [GitHub Issues / Projects](https://github.com/grantbarry29/scrutineer/issues)
  -- the board is the only tracker; product direction lives in
  [`dev-agent-rules/scrutineer-product-vision.md`](dev-agent-rules/scrutineer-product-vision.md).

## Knowledge base (OKF)

All agent-facing knowledge in this repo -- design docs, engineering rules, guides, and
component READMEs -- is written in the
[Open Knowledge Format (OKF)](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md):
plain Markdown with a YAML frontmatter header that makes each document machine-routable, so
an agent can decide what is worth reading without opening every file.

Every knowledge doc declares:

```yaml
---
type: Guide                     # Guide | Reference | Design | Rule | ...
title: Egress Allowlist Example
description: "One-line summary used in index bullets and for relevance triage."
status: live                    # live | draft | historical | ...
read_when: "When to pull this doc into context."
---
```

Rules under [`dev-agent-rules/`](dev-agent-rules/) add two more keys: `applies_to` (path
globs that bind a rule to the files it governs) and `always_load` (whether it enters every
agent session). You navigate the whole base through `index.md` **knowledge maps** -- one at
the repo root and one in each bundle ([`docs/`](docs/index.md),
[`docs/design/`](docs/design/index.md), [`dev-agent-rules/`](dev-agent-rules/index.md)) --
whose bullets are generated from each target's frontmatter. `make lint-docs` is what keeps
this honest: it enforces the frontmatter contract, keeps the index bullets in sync, and caps
the size of the always-on context. Start at the root [knowledge map](index.md), and never
trust a doc marked `status: historical` without following its `superseded_by` target.

## License

Apache 2.0. See [LICENSE](./LICENSE).
