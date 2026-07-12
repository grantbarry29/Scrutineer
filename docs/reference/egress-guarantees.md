---
type: Reference
title: Egress Enforcement — Guarantees & Assumptions
description: "Exactly what the envoy enforcement backend guarantees (proxy-only egress, untamperable chokepoint, FQDN + CIDR policy, independent observed evidence) and the assumptions those guarantees rest on."
status: live
read_when: "Assessing or documenting egress enforcement strength; writing user-facing enforcement claims."
---

# Egress Enforcement — Guarantees & Assumptions

When a `RuntimeProfile` enables the `envoy` enforcement backend, agent egress is
governed **adversarial-grade** — the agent cannot bypass it or forge its record —
**under the assumptions below**. This is deliberately narrower than "tamper-proof";
we never claim more than these boundaries support. (Design:
[`docs/design/evidence-integrity.md`](../design/evidence-integrity.md).)

**What holds:**

- **Egress goes only through the proxy.** A per-session Envoy runs in its **own pod**
  (own identity/netns), and a default-deny egress `NetworkPolicy` on the agent pod
  permits **only** that Envoy. The agent reaches it by ClusterIP, so **direct DNS is
  denied** — no DNS-exfil path.
- **The agent can't tamper with the enforcement point.** It lives outside the agent's
  pod, so a compromised agent can't reconfigure, restart, or read the proxy's identity.
- **FQDN allow/deny is enforced at the proxy.** The policy's `allowedDomains`/
  `deniedDomains` render into Envoy RBAC on the `:authority`, so denied (or not-allowed)
  hosts are blocked at the chokepoint for both plain HTTP and HTTPS `CONNECT` — not just
  recorded. Matching is exact or `*.`-wildcard (subdomains, apex excluded), shared with
  the egress-reporter's evidence classification so enforcement and evidence agree.
- **Evidence is independent.** Egress is recorded as `observed` — stamped by the
  reporter from the **proxy pod's authenticated identity**, never from the payload, so
  the agent cannot forge `observed` records or suppress them by lying. In enforced mode a
  blocked host is an `observed` deny; in audit mode it flows but is recorded as an
  `observed` dry-run.
- **Defense in depth.** Even the proxy pod is denied cloud-metadata (and operator-set
  cluster) CIDRs, so a compromised Envoy still can't reach `169.254.169.254`.

**What it assumes (and does not claim beyond):**

- **The CNI enforces egress `NetworkPolicy`.** Required. Verified against a CNI matrix
  (kindnet + Calico) by the networking e2e suite; operators on other CNIs must confirm
  egress-policy support.
- **The agent pod is unprivileged** (`drop ALL`, `seccomp: RuntimeDefault`, no
  `CAP_NET_ADMIN`) and **the node/CNI is not compromised** — node-level escape is a
  higher threat tier, explicitly out of scope.
- **Coverage is HTTP/S + client-`CONNECT`-tunneled TCP.** Standard tooling honors the
  injected proxy env; non-HTTP TCP (databases, SSH, custom TCP) is reachable by tunnelling
  it over `CONNECT` to the same proxy — same FQDN policy, same routing lock, `observed`
  evidence ([how-to](../egress-non-http.md)). Only proxy/`CONNECT`-**oblivious** tools
  (honor no proxy config, can't be wrapped) **fail closed** (no leak; they wait for the
  transparent node interceptor, [#64](https://github.com/grantbarry29/scrutineer/issues/64)).
  FQDN matching is **host-level** (SNI/`:authority`) — HTTPS and tunnelled TCP are both
  CONNECT-opaque, so there is no path/method or payload matching.
- **IP/CIDR policy covers canonical IP-literal dials only.** `allowedCIDRs`/`deniedCIDRs`
  are enforced at the same proxy chokepoint
  ([#125](https://github.com/grantbarry29/scrutineer/issues/125)) by matching the request
  authority when it is a canonical IPv4 literal (e.g. `CONNECT 10.2.3.4:5432`).
  Non-canonical numeric spellings a resolver would expand into a range (leading-zero
  octets like `010.2.3.4`, short forms like `10.1`) are **refused fail-closed** when CIDR
  policy is active ([#126](https://github.com/grantbarry29/scrutineer/issues/126)), so they
  can't evade a deny-list. What is **not** matched is a *hostname* that resolves into a
  range — govern hostnames with the domain fields; resolved-address enforcement is a
  separate future design, and cloud metadata is denied by resolved IP at the kernel
  backstop regardless of spelling. IPv6 egress is denied by construction
  ([#66](https://github.com/grantbarry29/scrutineer/issues/66) posture); under any
  allow-list a request must match the domain allow-list **or** the CIDR allow-list.
- **`observed` means "independent of the agent," not "tamper-proof."** It is only as
  strong as the assumptions above.

Agents that legitimately need the Kubernetes API can opt into
`spec.pod.automountServiceAccountToken: true` (see
[RuntimeProfile](agentsession-crd.md#reusable-runtime-profile-runtimeprofile)); that traffic still transits
the proxy.

