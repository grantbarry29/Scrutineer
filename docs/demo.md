# Demo — Untamperable Egress Governance in Two Sessions

What this shows, in one run: an agent whose **only** network path is a per-session Envoy
chokepoint it cannot alter, a denied request rejected live at that chokepoint, a bypass
attempt killed by the routing lock, and the whole thing recorded as **`observed`**
evidence the agent could not have forged — plus the same workload under `audit-only`
mode, where nothing is blocked but everything is still seen.

## 0. Prerequisites

```sh
make quickstart     # kind cluster + Scrutineer deployed; ends with the lock-gate verdict
```

Quickstart refuses to declare success unless the **verified-or-refused** gate proves the
cluster's CNI actually enforces NetworkPolicy (differential canary probe — see
[`docs/design/untamperable-enforcement.md`](design/untamperable-enforcement.md) §4). That is itself
the first demo: Scrutineer never *assumes* its lock works. Internet egress from the
cluster is required (the demo fetches `example.com`).

## 1. Run it

```sh
make demo
```

The demo targets the quickstart cluster's kube-context (`kind-scrutineer-quickstart`)
and refuses to run against anything else, so a stale context can never point it at a
real cluster — running it elsewhere is a deliberate act:
`DEMO_KUBE_CONTEXT=<ctx> make demo` (same for `demo-down`). If the lock gate is not
`Verified` it cleans up and refuses (running `demo-audit` without an effective routing
lock would contradict the table below), and a session that cannot succeed fails fast
with its diagnosis instead of sitting out the full wait.

This applies [`config/samples/demo/`](../config/samples/demo/): one hardened
`RuntimeProfile` enabling the `envoy` backend, two `AgentPolicy` objects that differ only
in `mode`, and two `AgentSession`s running the **same plain-busybox agent**
(bring-your-own-agent: nothing in the image cooperates with enforcement). Each session
gets its own out-of-pod Envoy proxy pod, its own ServiceAccount identities, a
default-deny routing lock on the agent pod, and an egress backstop on the proxy pod.

The agent probes three paths and prints what *it* experienced:

| probe | `demo-enforced` | `demo-audit` |
|---|---|---|
| `http://example.com` via the governed path (allowlisted) | `SUCCEEDED` | `SUCCEEDED` |
| `http://example.net` via the governed path (not allowlisted) | `BLOCKED` — Envoy RBAC rejects it | `SUCCEEDED` — but recorded as `dry-run` evidence |
| direct DNS lookup, skipping the proxy (bypass attempt) | `BLOCKED` | `BLOCKED` |

Two things to notice:

- **The denial is real, not advisory.** The agent's `HTTP(S)_PROXY` env is a convenience,
  not the control: the default-deny NetworkPolicy is what makes Envoy the only reachable
  egress. Unsetting the env just breaks the agent's own networking (bypass row).
- **The lock holds in audit-only mode too.** Observation is only trustworthy if the
  chokepoint is the agent's only path — so `audit-only` relaxes L7 blocking, never the
  routing lock.

## 2. Read the evidence

`make demo` ends by printing `status.policyDecisions` for both sessions. Look at three
columns:

- **action** — `allow` for `example.com`; `deny` (enforced) vs `dry-run` (audit-only) for
  `example.net`. Mode changed what *happened*; it never changed what was *seen*.
- **assurance** — every runtime entry says `observed`: it was reported by the
  egress-reporter running beside Envoy in the proxy pod and authenticated by that pod's
  own ServiceAccount. The reporter stamps assurance from the caller's *identity*, never
  from the payload — the agent has no path to inject or launder evidence.
- what's **absent** — the bypass attempt left no decision entry. The CNI drops those
  packets silently, and Scrutineer states that blind spot rather than papering over it
  ([`design/bypass-attempt-evidence.md`](design/bypass-attempt-evidence.md); closing it
  unforgeably is the #64 node-interceptor track).

Dig further with:

```sh
kubectl get agentsession demo-enforced -o yaml       # violations, events, conditions
kubectl get events --field-selector involvedObject.name=demo-enforced
kubectl get pods -l scrutineer.sh/session            # the per-session proxy pods
```

The proxy pod also exposes Prometheus metrics (`:9902` Envoy stats, `:9903`
`scrutineer_egress_reporter_*` — see
[`design/phase-4-observability-export.md`](design/phase-4-observability-export.md)).

## 3. What this is not (honest boundaries)

- TLS to external hosts is CONNECT-tunneled: filtering is by **authority** (domain), not
  request bodies. L7 body visibility exists only for plain HTTP and future in-cluster
  chokepoint hops.
- Tool and file governance have **no enforcement backend yet** — Scrutineer removed the
  cooperative in-pod tier rather than ship advisory controls dressed as governance
  (untamperable or absent). They return out-of-pod: tools-pod epic #76, LLM-gateway
  epic #77.
- The guarantee assumes an enforcing CNI (which the gate *proved*, not assumed) and an
  uncompromised node — spelled out in
  [`design/evidence-integrity.md`](design/evidence-integrity.md).

## 4. Clean up

```sh
make demo-down        # remove the demo sessions/policies/profile
make quickstart-down  # delete the kind cluster entirely
```
