# CLAUDE.md

Claude Code's entry point for this repo. **All rule content lives once in
[`dev-agent-rules/`](dev-agent-rules/)** — this file is a thin map that points into
it. Edit rules in `dev-agent-rules/`, never here.

**Scrutineer** is a Kubernetes-native governance and runtime control plane for autonomous
AI agents — not an orchestrator, workflow engine, or agent framework. A Kubebuilder
operator around the `AgentSession` CRD; controllers declare/propagate governance,
out-of-pod per-session chokepoints (Envoy egress proxy + default-deny routing lock) enforce it. Orientation:
[`docs/design/architecture.md`](docs/design/architecture.md).

## The knowledge base is OKF

All agent-facing knowledge (design docs, rules, guides, component READMEs) is
[OKF-formatted](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
markdown: YAML frontmatter declares each doc's `type`, `status`, `description`, and
`read_when`; rules additionally carry `applies_to` path globs and `always_load`.
Navigate via the repo-root [`index.md`](index.md) knowledge map (bundle indexes:
[`docs/index.md`](docs/index.md), [`docs/design/index.md`](docs/design/index.md),
[`dev-agent-rules/index.md`](dev-agent-rules/index.md)). Never trust a design doc with
`status: historical` without reading its `superseded_by` target. `make lint-docs`
enforces the frontmatter contract and keeps index bullets synced to each target's
frontmatter — after editing a `title`/`description`/`applies_to`, run `make gen-index`.

## Always read at the start of any task

These are the always-on rules (`always_load: true`) — read them before doing work in this repo:

- [`dev-agent-rules/devcontainer.md`](dev-agent-rules/devcontainer.md) — **all build/test/codegen runs inside the provided devcontainer, never on the host** (the host's Go version breaks the pinned toolchain); host failures of `make`/codegen/envtest are environment artifacts, not bugs.
- [`dev-agent-rules/test-driven-development.md`](dev-agent-rules/test-driven-development.md) — **build features test-first and environment-first**: ensure the matching test level (unit/envtest/e2e) is runnable before writing feature code; correctness only a running artifact can prove (Envoy config, redirect path, deployed overlay) needs an e2e test — don't claim it done on unit tests alone.
- [`dev-agent-rules/scrutineer-product-vision.md`](dev-agent-rules/scrutineer-product-vision.md) — product direction, threat model, scope boundaries (one slice at a time; don't overstate enforcement strength; control-plane/data-plane split).
- [`dev-agent-rules/task-management.md`](dev-agent-rules/task-management.md) — GitHub Issues/Projects are the sole source of task state; claim one issue before editing; out-of-scope work → an issue in the same session.
- [`dev-agent-rules/component-docs.md`](dev-agent-rules/component-docs.md) — every component keeps a local README (with OKF frontmatter); update it in the same change.

When implementing any task, also follow
[`dev-agent-rules/scrutineer-workflow.md`](dev-agent-rules/scrutineer-workflow.md)
(implementation contract, Issue Body Template, End-of-Task Handoff).

## Read before non-trivial work in the matching area

Scoped rules bind by path: a rule applies when a file you touch matches its
frontmatter `applies_to` globs — see the rule list in
[`dev-agent-rules/index.md`](dev-agent-rules/index.md). For any non-trivial change in
`api/` or `internal/{controller,enforcement,policy,reporter}`, also route to the
matching design doc via [`docs/design/index.md`](docs/design/index.md) (or each doc's
`read_when`).

## After making code changes — self-review against the rules

These rules are **not** auto-attached, so a relevant one can stay out of context if
the task didn't obviously look like its area. Before declaring a code change done,
**re-derive which rules apply from the files you actually touched** and audit the
diff against them — even if you didn't read them up front:

1. List the changed paths (`git status --short` / `git diff --name-only`).
2. Match each path against the `applies_to` globs in
   [`dev-agent-rules/index.md`](dev-agent-rules/index.md) (a path can match several —
   e.g. a file under `internal/controller/**` is governed by **kubernetes-controller**,
   **distributed-systems-networking**, and possibly **crd-api-design** /
   **component-binaries**).
3. Open each matching rule and check the diff against its "Anti-Patterns To Reject"
   and "Highest Priority" sections. Fix violations, or call them out explicitly if
   intentional.
4. Confirm the always-on rules held: component README updated in the same change
   (component-docs), scope stayed narrow with out-of-scope work filed as issues
   (task-management / scrutineer-product-vision), and `make lint-docs` passes if any
   docs changed.
