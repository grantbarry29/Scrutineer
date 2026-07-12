---
type: Reference
title: Demo Manifests
description: "Self-contained manifests for the guided egress-governance demo, applied together by make demo."
status: live
read_when: "Modifying the demo manifests."
---

# Demo manifests

Self-contained manifests for the guided egress-governance demo — apply them together
via `make demo` (walkthrough + expected output: [`docs/demo.md`](../../../docs/demo.md)):

| File | What it is |
|---|---|
| `00-runtimeprofile.yaml` | Hardened profile enabling the `envoy` backend (per-session out-of-pod proxy + routing lock) |
| `01-agentpolicies.yaml` | Two policies, same allowlist (`example.com`), differing only in `mode`: `enforced` vs `audit-only` |
| `02-agentsession-enforced.yaml` | Busybox agent probing allowed / denied / bypass paths under `enforced` |
| `02-agentsession-audit.yaml` | The identical agent under `audit-only` — observed, not blocked; lock still applies |

Filenames are numbered because `kubectl apply -f <dir>` applies alphabetically:
profile → policies → sessions, so a session never (even transiently) references a
RuntimeProfile or AgentPolicy that has not been applied yet (#90).

Clean up with `make demo-down`. Keep the probe scripts, expected-outcome comments, and
`docs/demo.md` in sync when policy semantics or the proxy wiring change.
