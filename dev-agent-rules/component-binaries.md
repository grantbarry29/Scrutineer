
# Component Binaries (cmd/** + sidecars)

Applies on top of the always-on `component-docs` rule (general README requirement +
[`docs/templates/component-readme.md`](../docs/templates/component-readme.md)). This
rule adds the conventions specific to Scrutineer's independently built images.

Each of these is a separately built/deployed component and keeps a README at its
`cmd/<binary>/` root (the manager's overview is the root [`README.md`](../README.md)):

| Binary | Entry | Dockerfile | Image | Build / load | Core logic |
|--------|-------|-----------|-------|--------------|------------|
| manager (controller + webhook + reporter) | `cmd/main.go` | `Dockerfile` | `ghcr.io/grantbarry29/scrutineer` | `make docker-build` / `kind-load` | `internal/controller/...`, `internal/webhook/...`, `internal/reporter` |
| dns-proxy sidecar | `cmd/dns-proxy` | `Dockerfile.dns-proxy` | `ghcr.io/grantbarry29/scrutineer-dns-proxy` | `make docker-build-dns-proxy` / `kind-load-dns-proxy` | `internal/enforcement/dnsproxy` |
| tool-gateway sidecar | `cmd/tool-gateway` | `Dockerfile.tool-gateway` | `ghcr.io/grantbarry29/scrutineer-tool-gateway` | `make docker-build-tool-gateway` / `kind-load-tool-gateway` | `internal/enforcement/toolgateway` |
| fs-gateway sidecar | `cmd/fs-gateway` | `Dockerfile.fs-gateway` | `ghcr.io/grantbarry29/scrutineer-fs-gateway` | `make docker-build-fs-gateway` / `kind-load-fs-gateway` | `internal/enforcement/workspace` |
| egress-reporter (runs beside Envoy in the egress-proxy pod, NOT an in-agent-pod sidecar) | `cmd/egress-reporter` | `Dockerfile.egress-reporter` | `ghcr.io/grantbarry29/scrutineer-egress-reporter` | `make docker-build-egress-reporter` / `kind-load-egress-reporter` | `internal/enforcement/envoy` |

## Conventions a binary README must capture

- Entry point, run mode (long-running server vs. one-shot), flags/env, and ports.
- The data-plane/evidence contract for sidecars: they are **cooperative** (share the
  agent pod/ServiceAccount), report via the reporter channel, and stamp evidence
  `self-reported` — never overstate as tamper-proof (see
  [`docs/design/phase-3-enforcement-architecture.md`](../docs/design/phase-3-enforcement-architecture.md)
  and the runtime-reporter contract).

## Files that must change together (call these out in the README)

- A sidecar's behavior spans: `cmd/<x>/main.go` → `internal/enforcement/<pkg>` →
  its **injection + env wiring** in `internal/controller/job/sidecars.go` →
  `Dockerfile.<x>` → the `docker-build-<x>` / `kind-load-<x>` Makefile targets (and
  `test-e2e-images`). Changing the contract usually touches several of these — keep
  the README and these sites consistent.

## Validation

`make test` (unit/envtest); `make test-e2e-images && make test-e2e` for live sidecar
evidence specs. Treat the Dockerfiles, Makefile targets, and `sidecars.go` as the
source of truth if a README disagrees.
