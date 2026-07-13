---
type: Reference
title: CI Tiers
description: "Which workflows run when: Lint/Test always, cluster-heavy E2E + Quickstart Smoke skip docs-only pushes, Nightly Networking cross-checks Calico/dual-stack; all cluster jobs build first-party images from the checkout and dump diagnostics on failure."
status: live
read_when: "Changing workflows, path filters, or debugging CI behavior."
---

# CI Tiers

Per push/PR: **Lint** and **Test** (unit + envtest) always run. Lint gates every
derived artifact as "regeneration is a no-op": `make fmt`, controller-gen output
(`make manifests generate` must produce no diff — CRDs, RBAC, deepcopy),
`go mod tidy`, and the OKF docs/index sync (`make lint-docs`) all fail on drift
(#128, #133). The cluster-heavy
workflows — **E2E** (standard + kindnet networking enforcement suite) and
**Quickstart Smoke** (`make quickstart && make demo` end-to-end) — skip docs-only
changes (#86). Nightly (+ manual dispatch): **Nightly Networking** cross-checks the
enforcement suite on Calico and a dual-stack cluster (#93). All cluster jobs build
the first-party images from the checkout under test — never registry pulls, which
can silently predate the checkout's behavior (#109). On failure, every cluster job
dumps post-mortem diagnostics (controller logs, pods, events, AgentSession
describes, proxy/agent pod logs) into the job log before the cluster vanishes with
the runner (`hack/ci-dump-diagnostics.sh`, #110).
