---
type: Guide
title: Non-HTTP Egress via CONNECT
description: "Operator guide: reaching non-HTTP TCP services (databases, SSH, brokers) through the per-session Envoy egress proxy with CONNECT tunnels."
status: live
read_when: "Agents needing non-HTTP TCP egress through the proxy."
---

# Reaching non-HTTP services through the egress proxy (CONNECT tunnel)

**Audience:** operators/agent authors whose agent needs to reach a **non-HTTP** TCP service
— a database, SSH, a message broker, any custom TCP protocol — from a session governed by
the per-session Envoy egress proxy.

**TL;DR:** the proxy already tunnels arbitrary TCP over HTTP `CONNECT`, under the same
routing lock and the same FQDN allow/deny policy as HTTPS. A `CONNECT`-capable client (or a
tiny `socat`/`ssh` shim) reaches those services with **no extra privilege and no node data
plane**. Tools that *cannot* be pointed at a proxy stay fail-closed — that residual gap is
what the transparent node interceptor ([#64](https://github.com/grantbarry29/scrutineer/issues/64))
exists to close.

---

## Why this is needed

The routing lock (default-deny egress `NetworkPolicy`) permits the agent pod **only** one
destination: its session's Envoy, on port `15001`. Direct DNS is denied. So:

- **HTTP/HTTPS "just works"** — standard tooling honors the injected `HTTP_PROXY`/`HTTPS_PROXY`
  env; HTTPS is already an HTTP `CONNECT` tunnel to `:443`.
- **A raw TCP tool** (e.g. `psql`, `redis-cli`, `mysql`, `ssh`) doesn't speak HTTP proxying,
  so left alone it opens a direct socket — which the lock drops. It **fails closed**, not open.

The escape hatch: wrap that raw TCP in an HTTP `CONNECT` to the proxy. Envoy terminates the
`CONNECT`, resolves the target itself (the agent still needs no DNS), applies FQDN policy to
the `CONNECT` authority, and then tunnels raw bytes end to end.

## What still holds when you use it

- **The routing lock still holds.** All traffic goes to Envoy on `15001`; there is no other path.
- **FQDN policy still applies.** `allowedDomains`/`deniedDomains` are matched against the
  `CONNECT` authority (`host:port`) by the *same* Envoy RBAC that governs HTTPS. A host that
  isn't allowed is blocked at the chokepoint, and the block is recorded as `observed` evidence
  — exactly like a denied HTTPS request.
- **CIDR policy applies to IP-literal dials.** A tunnel target that is an IPv4 literal
  (`CONNECT 10.2.3.4:5432`) is matched against `allowedCIDRs`/`deniedCIDRs` by the same RBAC
  ([#125](https://github.com/grantbarry29/scrutineer/issues/125)); under any allow-list, a
  target matching *neither* the domain nor the CIDR allow-list is default-denied.
- **Evidence is still `observed`.** Each tunnel shows up in the egress access log →
  egress-reporter → `status.policyDecisions`, stamped from the proxy pod's identity.

## What does *not* change (honest limits)

- **Host-level only.** FQDN matching strips the port (`MatchDomain`), so allowing
  `db.example.com` allows a `CONNECT` to it on **any** port. There is no port-level egress
  policy at this layer; if you need per-port control, keep the allow-list tight to exactly the
  hosts the agent should reach.
- **The tunnel is L7-opaque.** Once `CONNECT` is established Envoy forwards raw bytes and does
  **not** inspect them — same posture as HTTPS. You get host-level allow/deny and connection
  evidence, not payload inspection.
- **The tool must be steerable.** This works only for clients you can point at a proxy (or
  wrap in `socat`/`ssh`). A statically-linked binary that ignores all proxy configuration and
  can't be wrapped still fails closed — that is the [#64](https://github.com/grantbarry29/scrutineer/issues/64)
  case, not this one.

---

## Recipes

**Prerequisite:** the agent image must contain a `CONNECT`-capable client — `socat`, `ncat`
(nmap), or a tool with a native `CONNECT`/`ALL_PROXY` mode (`curl`, `ssh`). Scrutineer ships
no tunnel binary of its own (that would add a component to the agent's trust domain); these
are stock tools you add to your own image.

All recipes derive Envoy's address from the injected proxy env (it is the proxy **ClusterIP**,
because the lock denies DNS — never hardcode a Service name):

```sh
# Envoy ClusterIP that the routing lock permits, taken from the injected proxy env.
ENVOY_IP=$(printf '%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
ENVOY_PORT=15001
```

### 1. Arbitrary TCP via a local `socat` forwarder

`socat`'s `PROXY` address speaks HTTP `CONNECT`. Run a local listener that tunnels to the
target, then point the tool at `127.0.0.1` (which `NO_PROXY=localhost,127.0.0.1` keeps direct):

```sh
# Expose db.example.com:5432 on localhost:5432 via the proxy.
socat TCP-LISTEN:5432,fork,reuseaddr \
      PROXY:"$ENVOY_IP":db.example.com:5432,proxyport="$ENVOY_PORT" &

psql -h 127.0.0.1 -p 5432 -U app appdb      # db.example.com must be in allowedDomains
```

### 2. SSH via `ProxyCommand`

```sh
ssh -o ProxyCommand="socat - PROXY:$ENVOY_IP:%h:%p,proxyport=$ENVOY_PORT" \
    user@git.example.com                    # git.example.com must be in allowedDomains
```

(`ncat --proxy "$ENVOY_IP:$ENVOY_PORT" --proxy-type http %h %p` works equally well as the
`ProxyCommand` if `ncat` is present instead of `socat`.)

### 3. Clients that speak `CONNECT` natively

Many HTTP-aware clients tunnel arbitrary TCP through an `ALL_PROXY`/`--proxy` when told to.
For example `curl` to a non-HTTP port:

```sh
curl --proxytunnel -x "http://$ENVOY_IP:$ENVOY_PORT" telnet://mail.example.com:25
```

---

## Verifying it worked

The tunnel is `observed` like any other egress. After the agent runs:

```sh
kubectl get agentsession <name> -o jsonpath='{.status.policyDecisions}' | jq .
```

You should see a `network` decision whose target is `db.example.com:5432` (allow when the
host is allow-listed, deny when it is not), with `assuranceLevel: observed`. A denial also
appears as a `403` in the Envoy access log (`kubectl logs <envoy-pod>`).

## Related

- Trust model and why this is `observed`, not cooperative: [`design/evidence-integrity.md`](design/evidence-integrity.md).
- The residual gap this does **not** close (oblivious raw L4, unforgeable node-observed
  destination, bypass-*attempt* evidence): the transparent node interceptor,
  [#64](https://github.com/grantbarry29/scrutineer/issues/64).
