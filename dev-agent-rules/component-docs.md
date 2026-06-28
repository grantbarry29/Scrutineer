
# Component Documentation

Every **independently built, deployed, or operated** Relay component keeps a
**concise local `README.md`** next to its code. This includes binaries, controllers,
services, sidecars/daemons, workers, and CLIs — not libraries, generated code,
vendored deps, fixtures, or build output.

## Rules

- **Exists:** if such a component lacks a README, create one (use the template below).
- **Same-change updates:** whenever a change alters a component's responsibilities,
  boundaries, architecture, dependencies, interfaces (flags/env/ports/APIs/CRDs/
  schemas), configuration, generated artifacts, runtime/operational behavior, or
  build/test/run commands, **update its README in the same change**.
- **Staleness check:** before finishing a task, check whether any nearby README became
  stale and fix it.
- **Source of truth:** infer docs only from code, build files, manifests, config, tests,
  and [`docs/design/`](../docs/design/). When docs conflict with code/deployment
  config, the code/config wins — fix the doc.
- **Never invent:** mark anything you cannot confirm as `TODO: verify`. Do not guess.
- **Preserve & improve:** keep useful existing docs; refine rather than rewrite. No
  boilerplate-only READMEs; omit sections that don't apply.
- **Concise & scannable:** capture purpose, boundaries, invariants, and cross-component
  relationships over line-by-line summaries. Use repo-relative links.

## Template

Start from [`docs/templates/component-readme.md`](../docs/templates/component-readme.md)
and delete the sections that don't apply.

## Where component READMEs live

- A built binary → its `cmd/<binary>/` dir (the manager binary is `cmd/main.go`; the
  root [`README.md`](../README.md) is its overview).
- An operated controller/service that isn't its own binary dir → its package root
  (e.g. `internal/controller/agentsession/`, `internal/reporter/`).

`cmd/**` has extra build/deploy conventions — see the `component-binaries` rule.
