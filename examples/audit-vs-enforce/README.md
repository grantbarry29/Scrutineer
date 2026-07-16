---
type: Guide
title: Example — Audit vs Enforce
description: "Self-contained scenario contrasting enforced and audit-only egress policy over the same allowlist; the manifests behind make demo."
status: live
read_when: "Learning how policy mode changes what the chokepoint does, or modifying the demo manifests."
---

# Example: audit vs enforce

A self-contained scenario showing how **policy `mode` changes what the per-session egress
chokepoint does** to the *same* allowlist. Two `AgentSession`s run the identical plain-busybox
agent against the identical one-domain allowlist (`example.com`); the only difference is the
`AgentPolicy.spec.mode` each references:

| Session | Mode | `example.com` | `example.net` (unlisted) | direct DNS (bypass) |
|---|---|---|---|---|
| `demo-enforced` | `enforced` | SUCCEEDED | **BLOCKED** (Envoy RBAC 403) | **BLOCKED** (routing lock) |
| `demo-audit` | `audit-only` | SUCCEEDED | SUCCEEDED, recorded `dry-run` | **BLOCKED** (routing lock) |

The routing lock (default-deny egress `NetworkPolicy`) applies in **both** modes — observation
is only trustworthy if the chokepoint is the agent's only path — so the direct-DNS bypass stays
blocked either way. `audit-only` differs only at L7: nothing is denied, but every would-be
denial is still recorded as `observed` `dry-run` evidence.

## Files

| File | What it is |
|---|---|
| `00-runtimeprofile.yaml` | Hardened profile enabling the `envoy` backend (per-session out-of-pod proxy + routing lock) |
| `01-agentpolicies.yaml` | Two policies, same allowlist (`example.com`), differing only in `mode`: `enforced` vs `audit-only` |
| `02-agentsession-enforced.yaml` | Busybox agent probing allowed / denied / bypass paths under `enforced` |
| `02-agentsession-audit.yaml` | The identical agent under `audit-only` — observed, not blocked; lock still applies |

Filenames are numbered because `kubectl apply -f <dir>` applies alphabetically:
profile → policies → sessions, so a session never (even transiently) references a
RuntimeProfile or AgentPolicy that has not been applied yet (#90). `kubectl` ignores this
README (only `.yaml`/`.yml`/`.json` are applied).

## Run it

The guided walkthrough (expected output, what to look at in `status`, the L7-visibility
rationale) is [`docs/demo.md`](../../docs/demo.md), driven by `make demo` — which applies this
folder against the quickstart cluster:

```sh
make quickstart      # once: build + deploy the control plane on a local kind cluster
make demo            # applies this folder, waits for both sessions, prints the comparison
make demo-down       # cleanup
```

To apply it directly against any cluster that already runs the Scrutineer control plane:

```sh
kubectl apply -f examples/audit-vs-enforce/
kubectl get agentsession demo-enforced demo-audit -o wide
kubectl logs job/scrutineer-session-demo-enforced   # the agent's own DEMO_* probe verdicts
kubectl get agentsession demo-enforced -o jsonpath='{.status.policyDecisions}' | jq .
```

Keep the probe scripts, the expected-outcome comments, and `docs/demo.md` in sync when policy
semantics or the proxy wiring change.
