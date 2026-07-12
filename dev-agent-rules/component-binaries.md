---
type: Agent Rule
title: Component Binaries
description: "Build/deploy conventions for Scrutineer's independently built binaries (manager + egress-reporter) and what their component READMEs must cover."
status: live
read_when: "Binaries / sidecars work (cmd/**, internal/enforcement/**, Dockerfile*)."
applies_to: ["cmd/**", "internal/enforcement/**", "Dockerfile*"]
always_load: false
---

# Component Binaries (cmd/** + sidecars)

Applies on top of the always-on `component-docs` rule (general README requirement +
[`docs/templates/component-readme.md`](../docs/templates/component-readme.md)). This
rule adds the conventions specific to Scrutineer's independently built images.

Each of these is a separately built/deployed component and keeps a README at its
`cmd/<binary>/` root (the manager's overview is the root [`README.md`](../README.md)):

| Binary | Entry | Dockerfile | Image | Build / load | Core logic |
|--------|-------|-----------|-------|--------------|------------|
| manager (controller + webhook + reporter + lock-probe) | `cmd/main.go` | `Dockerfile` | `ghcr.io/grantbarry29/scrutineer` | `make docker-build` / `kind-load` | `internal/controller/...`, `internal/webhook/...`, `internal/reporter`, `internal/enforcement/lockverify` |
| egress-reporter (runs beside Envoy in the per-session egress-proxy pod, out-of-agent-pod) | `cmd/egress-reporter` | `Dockerfile.egress-reporter` | `ghcr.io/grantbarry29/scrutineer-egress-reporter` | `make docker-build-egress-reporter` / `kind-load-egress-reporter` | `internal/enforcement/envoy` |

> The cooperative in-pod sidecar tier was removed (#71);
> enforcement is out-of-pod only. See
> [`docs/design/untamperable-enforcement.md`](../docs/design/untamperable-enforcement.md).

## Conventions a binary README must capture

- Entry point, run mode (long-running server vs. one-shot), flags/env, and ports.
- The data-plane/evidence contract for sidecars: they are **cooperative** (share the
  agent pod/ServiceAccount), report via the reporter channel, and stamp evidence
  `self-reported` — never overstate as tamper-proof (see
  [`docs/design/enforcement-architecture.md`](../docs/design/enforcement-architecture.md)
  and the runtime-reporter contract).

## Files that must change together (call these out in the README)

- A sidecar's behavior spans: `cmd/<x>/main.go` → `internal/enforcement/<pkg>` →
  its **injection + env wiring** in `internal/controller/job/sidecars.go` →
  `Dockerfile.<x>` → the `docker-build-<x>` / `kind-load-<x>` Makefile targets (and
  `test-e2e-images`). Changing the contract usually touches several of these — keep
  the README and these sites consistent.
- Adding/removing a binary also means updating the **release matrix** in
  [`.github/workflows/release.yaml`](../.github/workflows/release.yaml) (publishes
  every first-party image on a `v*` tag) — otherwise the new image is referenced in
  code but never published. Image references derive from the binary's build version
  (`internal/version`, ldflags-injected — #112): make targets bake a `dev-<describe>`
  tag, the release workflow bakes the `v*` tag, and the `verify-version` guard checks
  the Makefile `VERSION` + overlay `newTag` pins against the pushed tag. Dev builds
  must never produce a bare `vX.Y.Z` image tag.

## Validation

`make test` (unit/envtest); `make test-e2e-images && make test-e2e` for live sidecar
evidence specs. Treat the Dockerfiles, Makefile targets, and `sidecars.go` as the
source of truth if a README disagrees.
