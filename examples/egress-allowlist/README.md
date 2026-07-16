---
type: Guide
title: Example — Egress Allowlist
description: "Self-contained scenario locking one agent session to a named set of FQDNs, enforced at the per-session Envoy chokepoint."
status: live
read_when: "Restricting an agent to a specific set of destination hosts."
---

# Example: egress allowlist

A self-contained scenario for the common case: **restrict an agent to a named set of
destination hosts**, and nothing else. One `AgentSession` runs a plain-busybox agent under an
`enforced` `AgentPolicy` whose `allowedDomains` names exactly the hosts it may reach. Everything
outside that list is denied at the per-session Envoy chokepoint (RBAC 403), and the default-deny
routing lock keeps the proxy the agent's only path out.

| Probe | Host | Result |
|---|---|---|
| allow | `example.com` (listed) | SUCCEEDED — proxied via the session Envoy |
| deny | `example.net` (unlisted) | **BLOCKED** — Envoy RBAC 403 |
| bypass | direct DNS | **BLOCKED** — routing lock, no egress except the proxy |

Matching is host-level: exact names plus a single leading `*.` wildcard, port-insensitive. The
policy allowlists `example.com`, `example.org`, and `*.github.io` (which covers `pages.github.io`
but not the apex `github.io`).

## Files

| File | What it is |
|---|---|
| `00-runtimeprofile.yaml` | Hardened profile enabling the `envoy` backend (per-session out-of-pod proxy + routing lock) |
| `01-agentpolicy.yaml` | One `enforced` policy whose `allowedDomains` is the named FQDN set |
| `02-agentsession.yaml` | Busybox agent probing an allowed host, an unlisted host, and a direct-DNS bypass |

Filenames are numbered because `kubectl apply -f <dir>` applies alphabetically:
profile → policy → session, so the session never references a RuntimeProfile or AgentPolicy that
has not been applied yet (#90). `kubectl` ignores this README (only `.yaml`/`.yml`/`.json` are
applied).

## Run it

Against any cluster already running the Scrutineer control plane (e.g. after `make quickstart`):

```sh
kubectl apply -f examples/egress-allowlist/

# what the agent itself experienced:
kubectl logs job/scrutineer-session-allowlist-session

# the observed evidence the chokepoint recorded (allow for example.com, deny for example.net):
kubectl get agentsession allowlist-session -o jsonpath='{.status.policyDecisions}' | jq .

kubectl delete -f examples/egress-allowlist/   # cleanup
```

To govern your own agent, swap `spec.runtime.image`/`command` for your workload and edit
`allowedDomains` to the hosts it legitimately needs — nothing in the image has to cooperate;
the chokepoint and routing lock enforce the list from outside.
