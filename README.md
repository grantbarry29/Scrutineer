# Scrutineer

**Scrutineer is a Kubernetes-native governance layer for autonomous AI agent execution.**

Scrutineer is **not** a workflow engine. It is not trying to replace
[Kubernetes Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/),
[Tekton](https://tekton.dev/), [Argo Workflows](https://argoproj.github.io/argo-workflows/),
or [Temporal](https://temporal.io/) — those systems already run work.

Scrutineer's job is different. It is an control plane that governs autonomous AI
agents while they run inside enterprise environments. It wraps execution with
policy, untamperable egress enforcement, audit, observability, and human approval,
then delegates the actual running of the agent to an orchestrator.

This repository is a Kubebuilder-based Kubernetes operator built around the
`AgentSession` CRD. It is **bring-your-own-agent**: your image holds the reasoning
loop, model calls, and tool use; Scrutineer schedules that workload (Job or bare Pod),
resolves and propagates reusable policy, and **enforces network egress from outside
the agent's trust domain** -- a per-session, out-of-pod Envoy chokepoint plus a
default-deny routing lock the agent cannot alter. Runtime evidence is recorded back
into status stamped `observed`, observability and audit signals are exported, and
sensitive actions gate behind human approval. Enforcement ships **untamperable or
not at all** ([decision record](docs/design/untamperable-enforcement.md)): where a guarantee
depends on cluster behavior, Scrutineer proves it empirically and refuses rather
than degrading silently.

## Quickstart

One command from a fresh clone to a running Scrutineer on a local
[kind](https://kind.sigs.k8s.io/) cluster (needs Docker, kind, kubectl; builds the
first-party images from your checkout so the controller always matches the
manifests it is deployed with):

```sh
make quickstart
```

The first run takes about **5 minutes**; repeat runs are much faster. It creates a
dedicated `scrutineer-quickstart` cluster, loads the first-party images, installs the
CRDs, deploys the controller, and prints the **routing-lock verification verdict** —
Scrutineer empirically proves the cluster's CNI enforces NetworkPolicy before it will
run enforced sessions (*verified-or-refused*). If the verdict comes back `Refused` on
your kind version, retry with `make quickstart-down && make quickstart QUICKSTART_CNI=calico`.

Then run the guided demo of the untamperable egress path (the cluster needs
**internet egress**) — a denied request rejected live at the per-session chokepoint,
a bypass attempt killed by the routing lock, and `observed` evidence the agent could
not have forged ([walkthrough](docs/demo.md)):

```sh
make demo
```

Tear down with `make demo-down` / `make quickstart-down`.

## What you get today

- **Untamperable network egress governance** — per-session out-of-pod Envoy chokepoint
  (FQDN + IP/CIDR allow/deny, enforce or dry-run) + default-deny routing lock; evidence
  stamped `observed` from the proxy pod's own identity, never the agent's word.
  Exact guarantees and their assumptions: [egress guarantees](docs/reference/egress-guarantees.md).
- **Verified-or-refused** — a differential canary probe proves the CNI actually enforces
  NetworkPolicy; enforced sessions on an unverified cluster refuse to start, loudly.
- **Reusable policy CRDs** (`AgentPolicy`, `RuntimeProfile`) merged into per-session
  effective policy, with violations as cluster events and CRD status.
- **Human approval gates** (`ApprovalPolicy`/`ApprovalRequest`) for sensitive actions.
- **Audit + observability** — Prometheus metrics (control plane and egress data plane),
  OpenTelemetry traces, OTLP audit sink, session events/timeline, log/artifact capture.
- **Orchestrator-agnostic execution** — Kubernetes Jobs or bare Pods behind a
  backend-neutral `runtimeBackend` interface.

**Honest limitations:** tool and file governance are not enforced yet — Scrutineer
ships only enforcement the agent cannot tamper with, so those policy surfaces were
removed until their out-of-pod chokepoints land ([doctrine](docs/design/untamperable-enforcement.md)).
Two in-cluster orchestrators today (`kubernetes-job`, `kubernetes-pod`); no
operational UI yet; no per-session identity / multi-tenancy hardening yet.

## Developing

The repo ships a `.devcontainer/` giving a fully wired dev environment (Go 1.23,
Docker-in-Docker, kind cluster with CRDs) with zero host setup beyond Docker + VS Code:
open the folder in VS Code → *"Reopen in Container"* → `make run`. Full walkthrough —
dev-cluster Makefile targets, pinned tool versions, host-side runs, samples, and the
acceptance checklist: [development environment](docs/reference/dev-environment.md).
CI behavior: [CI tiers](docs/reference/ci.md).

## Documentation

- **Humans start here**, then the [demo walkthrough](docs/demo.md).
- **Agents** start at the [knowledge map](index.md) — the repo's knowledge base is
  [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)-formatted
  markdown whose frontmatter (`type`, `status`, `read_when`) is machine-routable.
- **Design docs:** [`docs/design/`](docs/design/index.md) — start with
  [`architecture.md`](docs/design/architecture.md).
- **Reference:** [AgentSession CRD](docs/reference/agentsession-crd.md) ·
  [controller behavior](docs/reference/controller-reference.md) ·
  [egress guarantees](docs/reference/egress-guarantees.md) ·
  [non-HTTP egress how-to](docs/egress-non-http.md).
- **Task state and roadmap:** [GitHub Issues / Projects](https://github.com/grantbarry29/scrutineer/issues)
  — the board is the only tracker; product direction lives in
  [`dev-agent-rules/scrutineer-product-vision.md`](dev-agent-rules/scrutineer-product-vision.md).

## Knowledge base (OKF)

All agent-facing knowledge in this repo — design docs, engineering rules, guides, and
component READMEs — is written in the
[Open Knowledge Format (OKF)](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md):
plain Markdown with a YAML frontmatter header that makes each document machine-routable, so
an agent can decide what to read without opening every file.

Every knowledge doc declares:

```yaml
---
type: Guide                     # Guide | Reference | Design | Rule | …
title: Example — Egress Allowlist
description: "One-line summary used in index bullets and for relevance triage."
status: live                    # live | draft | historical | …
read_when: "When to pull this doc into context."
---
```

Rules (under [`dev-agent-rules/`](dev-agent-rules/)) add two keys: `applies_to` (path globs
that bind the rule to the files it governs) and `always_load` (whether it enters every agent
session). Navigation is via `index.md` **knowledge maps** at the repo root and in each bundle
([`docs/`](docs/index.md), [`docs/design/`](docs/design/index.md),
[`dev-agent-rules/`](dev-agent-rules/index.md)), whose bullets are generated from each target's
frontmatter. `make lint-docs` enforces the frontmatter contract, keeps the index bullets in
sync, and caps the size of the always-on context. Start at the root
[knowledge map](index.md); never trust a doc marked `status: historical` without following its
`superseded_by` target.

## License

Apache 2.0. See [LICENSE](./LICENSE).
