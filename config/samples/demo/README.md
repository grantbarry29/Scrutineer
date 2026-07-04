# Demo manifests

Self-contained manifests for the guided egress-governance demo — apply them together
via `make demo` (walkthrough + expected output: [`docs/demo.md`](../../../docs/demo.md)):

| File | What it is |
|---|---|
| `runtimeprofile.yaml` | Hardened profile enabling the `envoy` backend (per-session out-of-pod proxy + routing lock) |
| `agentpolicies.yaml` | Two policies, same allowlist (`example.com`), differing only in `mode`: `enforced` vs `audit-only` |
| `agentsession_enforced.yaml` | Busybox agent probing allowed / denied / bypass paths under `enforced` |
| `agentsession_audit.yaml` | The identical agent under `audit-only` — observed, not blocked; lock still applies |

Clean up with `make demo-down`. Keep the probe scripts, expected-outcome comments, and
`docs/demo.md` in sync when policy semantics or the proxy wiring change.
