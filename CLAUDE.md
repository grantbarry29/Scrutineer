# CLAUDE.md

Claude Code's entry point for this repo. **All canonical rule content lives once in
[`dev-agent-rules/`](dev-agent-rules/)**. This file and the
[`.cursor/rules/*.mdc`](.cursor/rules/) files are thin maps into those rules
(Cursor uses `.mdc` frontmatter for glob auto-attach; Claude uses the table below).
Edit rules in `dev-agent-rules/`, never in the pointer files.

**Scrutineer** is a Kubernetes-native governance and runtime control plane for autonomous
AI agents — not an orchestrator, workflow engine, or agent framework. It is a Kubebuilder
operator around the `AgentSession` CRD: controllers declare and propagate governance;
cooperative in-pod sidecars enforce it. Orientation:
[`docs/design/architecture.md`](docs/design/architecture.md).

## Operating contract

Before editing:
1. Restate the task in repo-specific terms.
2. Identify the likely files/components involved.
3. Read this file, the always-on rules, and any matching area rules below.
4. Inspect existing patterns before introducing new structure.
5. **Ensure the matching test environment is runnable first (unit / envtest / e2e), then work test-first** — write the failing test, build, run in the devcontainer, iterate (see `dev-agent-rules/test-driven-development.md`).
6. Make the smallest coherent change that satisfies the task.

Do not:
- Invent new architecture unless the issue explicitly asks for it.
- Broaden the task without filing follow-up issues.
- Run build, test, codegen, lint, or envtest commands on the host.
- Declare completion without running relevant checks or explaining why they could not be run.

When uncertain about product scope, enforcement guarantees, API semantics, or whether
work belongs in the current issue, ask for clarification or file a follow-up issue rather
than guessing.

## Always read at the start of implementation work

For any implementation task, bug fix, refactor, codegen, test, or documentation change
that affects project behavior, read these rules before doing work:

- [`dev-agent-rules/devcontainer.md`](dev-agent-rules/devcontainer.md) — **all build/test/codegen runs inside the provided devcontainer, never on the host**. The host's Go version breaks the pinned toolchain; host failures of `make`/codegen/envtest are environment artifacts, not bugs.
- [`dev-agent-rules/test-driven-development.md`](dev-agent-rules/test-driven-development.md) — **build features test-first and environment-first**: ensure the matching test level (unit/envtest/e2e) is runnable before writing feature code; correctness only a running artifact can prove (Envoy config, redirect path, deployed overlay) needs an e2e test — don't claim it done on unit tests alone.
- [`dev-agent-rules/scrutineer-product-vision.md`](dev-agent-rules/scrutineer-product-vision.md) — product direction, threat model, scope boundaries: one slice at a time; do not overstate enforcement strength; preserve the control-plane/data-plane split.
- [`dev-agent-rules/task-management.md`](dev-agent-rules/task-management.md) — GitHub Issues/Projects are the sole source of task state; claim one issue before editing; out-of-scope work becomes an issue in the same session.
- [`dev-agent-rules/component-docs.md`](dev-agent-rules/component-docs.md) — every component keeps a local README; update it in the same change.

## Read before non-trivial work in the matching area

| Working on… | Read |
|---|---|
| Controller / reconciler code (`internal/controller/**`, `cmd/**`) | [`kubernetes-controller.md`](dev-agent-rules/kubernetes-controller.md) |
| CRD / API types (`api/**`, `config/crd/**`, `config/samples/**`) | [`crd-api-design.md`](dev-agent-rules/crd-api-design.md) |
| Distributed-systems / networking code (`internal/**`, `cmd/**`) | [`distributed-systems-networking.md`](dev-agent-rules/distributed-systems-networking.md) |
| Binaries / sidecars (`cmd/**`, `internal/enforcement/**`, `Dockerfile*`) | [`component-binaries.md`](dev-agent-rules/component-binaries.md) |
| Any non-trivial change in `api/`, `internal/{controller,enforcement,policy,reporter}` — find the matching design doc | [`scrutineer-design-docs.md`](dev-agent-rules/scrutineer-design-docs.md) → routes into [`docs/design/`](docs/design/) |
| **How** to implement: contract, issue body template, end-of-task handoff | [`scrutineer-cursor-workflow.md`](dev-agent-rules/scrutineer-cursor-workflow.md) |

## Command execution

All build, test, codegen, lint, and envtest commands must run inside the provided
devcontainer. If a command fails on the host, treat it as an environment mistake, not a
repository failure. Re-run inside the devcontainer before diagnosing code.

## After making code changes — self-review against the rules

Unlike Cursor, these rules are **not** auto-attached for Claude by file glob, so a
relevant rule can stay out of context if the task did not obviously look like its area.
Before declaring a code change done, **re-derive which rules apply from the files you
actually touched** and audit the diff against them — even if you did not read them up
front:

1. List the changed paths with `git status --short` and `git diff --name-only`.
2. Map each path to its rule via the table above. A path can match several rules; for
   example, a file under `internal/controller/**` may be governed by
   **kubernetes-controller**, **distributed-systems-networking**, and possibly
   **crd-api-design** or **component-binaries**.
3. Open each matching rule and check the diff against its "Anti-Patterns To Reject"
   and "Highest Priority" sections. Fix violations, or call them out explicitly if
   intentional.
4. Confirm the always-on rules held: component README updated in the same change,
   scope stayed narrow, and out-of-scope required work was filed as GitHub issues.

## Done means

A task is not complete until:
- The requested change is implemented.
- Relevant checks have been run inside the devcontainer, or the reason they could not be run is documented.
- Applicable component README/design docs are updated.
- Any out-of-scope required work discovered during implementation is filed as a GitHub issue.
- The final response includes changed files, checks run, and follow-up issues created.