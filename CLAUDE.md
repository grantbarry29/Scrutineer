# CLAUDE.md

Claude Code's entry point for this repo. **All rule content lives once in
[`dev-agent-rules/`](dev-agent-rules/)** — this file and the
[`.cursor/rules/*.mdc`](.cursor/rules/) files are just thin maps that point into it
(Cursor uses the `.mdc` frontmatter for glob auto-attach; Claude uses the table
below). Edit rules in `dev-agent-rules/`, never in the pointers.

**Scrutineer** is a Kubernetes-native governance and runtime control plane for autonomous
AI agents — not an orchestrator, workflow engine, or agent framework. A Kubebuilder
operator around the `AgentSession` CRD; controllers declare/propagate governance,
cooperative in-pod sidecars enforce it. Orientation:
[`docs/design/architecture.md`](docs/design/architecture.md).

## Always read at the start of any task

These are the always-on rules — read them before doing work in this repo:

- [`dev-agent-rules/scrutineer-product-vision.md`](dev-agent-rules/scrutineer-product-vision.md) — product direction, threat model, scope boundaries (one slice at a time; don't overstate enforcement strength; control-plane/data-plane split).
- [`dev-agent-rules/task-management.md`](dev-agent-rules/task-management.md) — GitHub Issues/Projects are the sole source of task state; claim one issue before editing; out-of-scope work → an issue in the same session.
- [`dev-agent-rules/component-docs.md`](dev-agent-rules/component-docs.md) — every component keeps a local README; update it in the same change.

## Read before non-trivial work in the matching area

| Working on… | Read |
|---|---|
| Controller / reconciler code (`internal/controller/**`, `cmd/**`) | [`kubernetes-controller.md`](dev-agent-rules/kubernetes-controller.md) |
| CRD / API types (`api/**`, `config/crd/**`, `config/samples/**`) | [`crd-api-design.md`](dev-agent-rules/crd-api-design.md) |
| Distributed-systems / networking code (`internal/**`, `cmd/**`) | [`distributed-systems-networking.md`](dev-agent-rules/distributed-systems-networking.md) |
| Binaries / sidecars (`cmd/**`, `internal/enforcement/**`, `Dockerfile*`) | [`component-binaries.md`](dev-agent-rules/component-binaries.md) |
| Any non-trivial change in `api/`, `internal/{controller,enforcement,policy,reporter}` — find the matching design doc | [`scrutineer-design-docs.md`](dev-agent-rules/scrutineer-design-docs.md) → routes into [`docs/design/`](docs/design/) |
| **How** to implement (contract, Issue Body Template, end-of-task handoff) | [`scrutineer-cursor-workflow.md`](dev-agent-rules/scrutineer-cursor-workflow.md) |

## After making code changes — self-review against the rules

Unlike Cursor, these rules are **not** auto-attached for Claude by file glob, so a
relevant one can stay out of context if the task didn't obviously look like its area.
Before declaring a code change done, **re-derive which rules apply from the files you
actually touched** and audit the diff against them — even if you didn't read them up
front:

1. List the changed paths (`git status --short` / `git diff --name-only`).
2. Map each path to its rule via the table above (a path can match several — e.g. a
   file under `internal/controller/**` is governed by **kubernetes-controller**,
   **distributed-systems-networking**, and possibly **crd-api-design** /
   **component-binaries**).
3. Open each matching rule and check the diff against its "Anti-Patterns To Reject"
   and "Highest Priority" sections. Fix violations, or call them out explicitly if
   intentional.
4. Confirm the always-on rules held: component README updated in the same change
   (component-docs), scope stayed narrow with out-of-scope work filed as issues
   (task-management / scrutineer-product-vision).
