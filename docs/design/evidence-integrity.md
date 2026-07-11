# Evidence Integrity — Per-Session Egress Chokepoint (Envoy)

**Status:** shipped (#8/#32/#62: per-session Envoy chokepoint, FQDN RBAC, caller-class `observed` stamping); the cooperative tier this doc hardened against was removed entirely ([`untamperable-enforcement.md`](untamperable-enforcement.md), #71)
**Scope:** the remaining hardening of runtime-evidence integrity — moving egress governance from *cooperative* to *adversarial-grade* (`observed`) by routing all agent egress through a per-session **Envoy** proxy that lives outside the agent's trust domain and cannot be bypassed. **Interim routing mechanism: explicit proxy** (portable, zero cluster dependency). **Transparent interception is a separate future epic** ([#64](https://github.com/grantbarry29/scrutineer/issues/64)).
**Non-goals:** transparent redirect / node data-plane now (future epic #64); a hard Cilium/CNI dependency as the baseline (optional backend only); full eBPF/syscall/file observation; node-compromise defense (see Assumptions); rich FQDN policy content (that is [#32](https://github.com/grantbarry29/scrutineer/issues/32), realized *at* this Envoy).
**Tracking:** [#8](https://github.com/grantbarry29/scrutineer/issues/8) (slices 1–3 shipped; this doc covers the remaining "independent `observed` source" hardening).

---

## 1. The crux

Scrutineer is only trustworthy if **the agent it governs cannot forge or suppress the evidence of what it did.** Everything else — audit, observability, the UI's credibility — leans on this.

When this doc was written, egress governance was **cooperative**: an in-pod proxy sidecar shared the agent's network namespace and (historically) ServiceAccount, reached via `HTTP_PROXY`. A compromised or prompt-injected agent could **bypass** it (raw socket to anywhere), **tamper/starve** it (adjacent process), or **forge** evidence (`self-reported` from within its own trust domain). #71 removed that tier outright.

Slices 1–3 (shipped) made this *honest and least-privilege* (`EvidenceAssurance` enum, `self-reported` stamping, SA-token isolation, audit assurance) but do not close the bypass/tamper gap. This doc does.

## 2. Principle

> Integrity never comes from *which tool* we use. It comes from the enforcement/observation point living in a **trust domain the agent has no privilege to alter**, and from making that point the **only path** for the governed traffic.

Two properties, both required: **out of the agent's control** (separate pod, own identity/netns) and **mandatory** (the agent's *only* working egress path, enforced by a layer it can't modify).

## 3. Architecture

All agent egress is forced through a **per-session Envoy proxy** running as its **own pod** — own ServiceAccount/identity/netns — created and owner-referenced by the controller and torn down with the session. Envoy handles **both L4 (TCP) and L7 (HTTP / TLS-SNI)** in one filter chain that future L4 features (IDS, packet capture, per-flow metrics) extend — none of which a `NetworkPolicy` could ever host.

**Placement is behind an interface.** Per-session is the first implementation (trivial attribution, tightest blast radius, no control-plane machinery). The same trust model + Envoy config generation can later back a shared per-node gateway for scale, without changing the guarantee.

**Routing enforcement is behind a backend interface.** Interim baseline = **explicit proxy**:
- The controller injects `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY`/`NO_PROXY` into the agent container, pointing at the session's Envoy; Envoy terminates HTTP and tunnels HTTPS via `CONNECT`.
- A CNI-enforced **default-deny egress `NetworkPolicy`** makes it mandatory: the *only* reachable egress is the session's Envoy — direct attempts are dropped at the pod boundary, outside the agent netns.
- **The agent pod adds nothing privileged** — no init container, no `NET_ADMIN`, no transparent redirect. It stays fully unprivileged; the only moving parts are injected env, the NetworkPolicy, and the separate Envoy pod. (This is a real security win over transparent redirect, which needs a privileged init container.)

**DNS is resolved at Envoy.** With `CONNECT`/proxying, the agent hands Envoy the *hostname* and Envoy does the DNS. So the agent needs **no direct DNS** → NetworkPolicy denies direct DNS egress entirely → closes DNS tunneling/exfil, and Envoy sees clean hostnames for FQDN policy (#32).

**NetworkPolicy = routing lock + hard backstops** (not the L4 policy engine):
1. **Routing lock** — permit egress only to the session's Envoy (+ reporter); drop everything else. Holds even if the injected env is tampered with.
2. **Hard backstops** — deny cloud metadata (`169.254.169.254`), cluster-internal/API ranges, known-bad CIDRs. These must hold **even if Envoy is compromised**, so they live in the kernel/CNI, not in Envoy.

**Interim coverage & fail-closed.** Explicit proxy via Envoy `CONNECT` cleanly covers **HTTP/HTTPS** — the dominant agent egress (LLM APIs, web, git/pip/npm, cloud SDKs). **Non-HTTP TCP** (databases, SSH, custom TCP) is reachable *today* by tunnelling it over `CONNECT` to the same proxy — policed by the same authority RBAC, under the same routing lock, recorded as `observed` (recipe: [`../egress-non-http.md`](../egress-non-http.md)); only proxy/`CONNECT`-**oblivious** tools that can't be pointed at a proxy are **fail-closed** (dropped). What #64 still adds is *unforgeable node-observed* L4 destination and bypass-*attempt* evidence, not basic non-HTTP reachability. If Envoy/policy isn't ready, the agent has **no egress** (start ordering gates the agent behind Envoy readiness) — an outage denies rather than bypasses.

**Evidence.** Because Envoy is outside the agent's trust domain, its egress evidence is stamped **`observed`** (authenticated by Envoy's own identity to the reporter) — the independent evidence source #8 requires. Agent-adjacent sources stay `self-reported`.

**Future routing backends (behind the same interface):** `node-interceptor` (#64 — portable transparent L4/L7 with unforgeable, node-observed destination) and optionally `cilium` (transparent for clusters that already run Cilium). Explicit proxy remains the zero-dependency fallback.

```mermaid
flowchart LR
  subgraph agentpod["Agent Pod (unprivileged; nothing added but env)"]
    agent["agent container\nHTTP_PROXY/ALL_PROXY -> session Envoy"]
  end
  subgraph cni["CNI / NetworkPolicy (kernel — agent cannot modify)"]
    lock["default-deny egress:\nallow only Envoy + reporter\n+ hard backstops (metadata, cluster-internal)\n+ deny direct DNS"]
  end
  envoy["Per-session Envoy Pod\n(own identity/netns; L4+L7 + filters; does DNS)"]
  reporter["Reporter (controller-owned)"]
  up["Allowed upstreams"]

  agent -->|via proxy| envoy
  agent -.->|direct / non-proxy attempt| lock
  lock -.->|dropped| x((✗))
  envoy -->|policy allow| up
  envoy -->|observed evidence| reporter
```

## 4. Why the agent cannot bypass or tamper

- **Bypass:** the only reachable egress is the session's Envoy; direct or non-proxy connections are dropped by the CNI at the pod boundary (outside its netns). A non-cooperating agent gets *no* egress, not a way out.
- **Tamper with routing:** the injected env is only for *routing well-behaved traffic*; rewriting/unsetting it just self-sabotages (still only Envoy is reachable). The `NetworkPolicy` is a k8s object the agent has no RBAC for and (SA-token off) can't reach the API to change.
- **Tamper with Envoy:** separate pod, own netns/identity — not an adjacent process the agent can signal/ptrace/starve.
- **Forge evidence:** Envoy authenticates to the reporter with its own token; the reporter stamps `observed` on that identity; the agent can't impersonate it.

## 5. Assumptions and honest boundaries

Adversarial-grade **for governed egress** under these stated assumptions — never claim more:

1. **Agent workloads use the injected proxy for HTTP/S** — standard tooling honors `HTTP_PROXY` automatically; non-HTTP tools can be `CONNECT`-tunnelled to reach TCP services under the same policy ([`../egress-non-http.md`](../egress-non-http.md)); only proxy-*oblivious* tools that honor no proxy config **fail closed** (no leak — they need wrapping or wait for the transparent backend #64). This is the interim usability cost.
2. **The agent pod is unprivileged** — `drop ALL`, `seccomp: RuntimeDefault`, no `CAP_NET_ADMIN`. (Explicit proxy adds nothing privileged, so this is easy to hold.) The SA token is not mounted unless a profile opts in via `spec.pod.automountServiceAccountToken` (Slice D); when it does, that agent's apiserver traffic still transits Envoy under the lock.
3. **DNS is governed** — resolved at Envoy; direct DNS egress denied.
4. **The node / CNI is not compromised** — a strictly higher threat tier, explicitly out of scope. The guarantee is "the agent can't tamper without escaping to the node."
5. **The CNI enforces egress `NetworkPolicy`** — required; Calico/Cilium and kindnet all do. The routing lock is verified against a CNI matrix (kindnet + Calico) by the generic networking e2e suite. Document the requirement for operators.
6. **Coverage is HTTP/S (L7) + client-`CONNECT`-tunneled TCP** — arbitrary TCP over `CONNECT`, policed by the same authority RBAC ([`../egress-non-http.md`](../egress-non-http.md)); only proxy/`CONNECT`-**oblivious** raw L4 is fail-closed until #64.

This closes the *cooperative → adversarial* gap for governed egress. Syscall/file observation (eBPF/Tetragon) remains a separate future `observed` source, out of scope here.

## 6. Relationship to #32 (FQDN egress) and #125 (CIDR egress) — ✅ delivered

Envoy is the shared substrate. #8 delivered the non-bypassable per-session Envoy chokepoint + trust boundary; [#32](https://github.com/grantbarry29/scrutineer/issues/32) then implemented FQDN allow/deny **as Envoy RBAC config** at that chokepoint. As built: the effective `allowedDomains`/`deniedDomains` render into an HTTP RBAC filter chain (deny-list before allow-list/default-deny) matching the request `:authority` — so it covers plain HTTP and HTTPS `CONNECT` (host-level; HTTPS is CONNECT-tunnelled, so no path/method matching). Matching uses `enforcement.MatchDomain` (exact + `*.` wildcard, apex-excluded, port-insensitive), shared with the egress-reporter's evidence classification. The egress-reporter classifies each observed authority against the same policy, so evidence records allow/deny (enforced) or dry-run (audit) as `observed`. Policy changes re-render the ConfigMap and recreate the Envoy pod (config-hash drift).

`allowedCIDRs`/`deniedCIDRs` are enforced at the **same chokepoint, by the same mechanism** ([#125](https://github.com/grantbarry29/scrutineer/issues/125)): each CIDR renders into an equivalent authority regex (per-octet ranges) matching the **canonical dotted-quad** spelling of the addresses it contains, merged into the same two filters — ONE deny filter (deniedDomains ∪ deniedCIDRs) before ONE allow filter (allowedDomains ∪ allowedCIDRs; default-deny when any allow-list exists — union semantics, so under a CIDR-only allow-list hostname dials are default-denied). Evidence classification uses the shared `enforcement.MatchIPCIDR` (parsing via `net/netip`, which likewise accepts only canonical dotted-quad), so the two agree by construction.

**Honest limits — this polices *canonical* IPv4-literal dials only:**
- A `CONNECT`/`Host` authority must be a canonical IPv4 literal (`10.2.3.4`). A **hostname that resolves into** a denied CIDR is NOT matched — resolved-address enforcement (post-DNS) is a separate future design. Envoy L4 `destination_ip` filters cannot express it in explicit-proxy mode: the downstream connection's destination is Envoy itself, and the real upstream IP materializes only post-DNS inside the dynamic_forward_proxy, after all filters have run.
- **Non-canonical numeric authorities are refused (#126).** A permissive resolver (Envoy 1.31 / c-ares, inet_aton-style) expands spellings the canonical regex doesn't match into a real address — leading-zero octets (`010.0.0.1` → `10.0.0.1`) and short forms (`10.1`/`10.0.1` → `10.0.0.1`) — which would otherwise evade a `deniedCIDRs` deny-list. So **when CIDR policy is active, any all-numeric dotted authority that is not a canonical dotted-quad is denied fail-closed** at the proxy (a constant extra RBAC permission, mirrored in evidence classification with reason `NonCanonicalIP`; verified live on Envoy 1.31). This makes deny-lists robust against numeric spelling tricks. What remains out of scope is a **hostname that resolves into a range** (a DNS name, not a numeric authority) — governed by the domain fields, with resolved-address enforcement tracked in #64; crown-jewel ranges (cloud metadata) are additionally denied by resolved IP at the kernel backstop regardless of spelling.

## 7. Increment plan

Each increment is an independently reviewable, `make test`-verifiable GitHub issue under #8.

- **Slice A ([#60]) — per-session Envoy egress proxy. ✅ landed.** Controller creates a per-session Envoy pod (own SA/identity, owner-referenced, torn down with the session) behind the `egressBackend` interface; inject explicit-proxy env into the agent container. Routing mechanism behind a backend interface (interim: explicit proxy). Envoy emits a stdout access log (traversal evidence + Slice C seed).
- **Slice B ([#61]) — mandatory routing (default-deny egress NetworkPolicy). ✅ landed.** Agent-pod routing lock (allow only the session's Envoy pod; deny direct DNS — the agent reaches Envoy by ClusterIP via `status.egressProxyEndpoint`, Envoy resolves), plus a hard backstop on the Envoy pod (allow DNS + internet EXCEPT cloud-metadata/operator CIDRs, configurable via `--egress-backstop-cidrs`). No privileged init container. Proven on kindnet + Calico via the CNI-generic networking e2e suite. Dual-stack posture (#66): the egress path is IPv4-only — no rendered policy contains an IPv6 allow, so on dual-stack clusters both the lock and the backstop deny ALL v6 egress by construction (v6 backstop entries are satisfied wholesale, never placed in the IPv4 except list). Proven on a dual-stack kind cluster (`make test-e2e-net-dual`).
- **Slice C ([#62]) — `observed` evidence. ✅ landed.** Envoy writes a JSON file access log into a shared, size-bounded emptyDir; the first-party **egress-reporter** container beside it (`cmd/egress-reporter`, tailer in `internal/enforcement/envoy`) submits each entry as a runtime `network` decision using the proxy pod's projected per-session SA token. The reporter derives assurance **from the authenticated identity, never the payload** (`CallerIdentity.Assurance()`): pod→Job→session callers stay `self-reported`; only the AgentSession-controller-owned egress-proxy pod (deterministic name + dedicated SA) yields `observed`. Audit records carry the same identity-derived assurance. The backstop policy always allows the Envoy pod → reporter so operator backstop CIDRs cannot sever the evidence channel. Proven live on kindnet + Calico (networking e2e: egress → access log → egress-reporter → `status.policyDecisions` with `assuranceLevel: observed`).
- **Slice D ([#63]) — opt-in + docs. ✅ landed.** The mandatory-egress opt-in is the settled `RuntimeProfile.spec.enforcement[{type: envoy}]` toggle (renamed from `spec.sidecars` in #65). Added `RuntimeProfile.spec.pod.automountServiceAccountToken` to re-enable the agent's SA token for API-needing agents (default off; drift-replaces a pending Job; pair with a scoped `spec.runtime.serviceAccountName`). Networking e2e proves live on kindnet + Calico that with the opt-in the token is mounted and apiserver access transits Envoy under the lock. Precise "guarantees & assumptions" section added to the root README; `observed` documented as "independent of the agent," not "tamper-proof."
- **Future epic ([#64]) — transparent node interceptor.** A portable, node-level (DaemonSet/eBPF) transparent-redirect backend that preserves the original destination unforgeably and removes the explicit-proxy app-compat gap for *all* protocols. Same trust model, added behind the routing backend interface. Not required for the #8 guarantee.

Order: A → B → C → D; #64 later. Slice A is the first code increment.

## 8. Open questions / design gaps

Resolved: routing mechanism (explicit proxy interim; transparency via the portable node-interceptor epic #64, not a Cilium baseline); placement (per-session first, per-node later behind the interface). **Hard-backstop CIDR list (Slice B)** — resolved: the Envoy-pod backstop denies `169.254.0.0/16` (cloud metadata) by default and is extended with environment-specific cluster/service/API CIDRs via `--egress-backstop-cidrs`; `ipBlock` `except` targets cluster-external IPs (per the NetworkPolicy spec), so pod-to-pod egress is governed by the routing lock's podSelector rules, not the backstop.

Remaining, smaller:
1. **Non-HTTP L4** — resolved: reachable *today* by tunnelling over `CONNECT` to the same proxy (documented recipe [`../egress-non-http.md`](../egress-non-http.md); policed by the authority RBAC, recorded as `observed`). Proxy/`CONNECT`-**oblivious** raw L4 remains fail-closed by default; #64's residual charter is exactly that, plus *unforgeable node-observed* destination (vs. today's client-declared `CONNECT` authority) and bypass-*attempt* evidence (the #72 blind spot — the CNI drops direct connects silently, and the proxy only ever sees traffic that reaches it).
2. **L4 evidence schema** — does the existing `PolicyDecision`/`PolicyViolation`/event schema capture connection-level evidence (5-tuple, bytes, duration), or does #64 need a schema extension? Assess during Slice C / #64.
