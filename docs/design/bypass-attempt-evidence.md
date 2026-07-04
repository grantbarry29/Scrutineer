# Bypass-Attempt Evidence for the Egress Lock

**Status:** design note / **decided — defer wholly to #64** (no interim implementation scheduled; approved contingency shape recorded below)
**Scope:** how a bypass *attempt* — a locked agent trying to connect around the Envoy chokepoint — could become `observed` evidence before the #64 node interceptor exists; compare the interim options and decide.
**Non-goals:** implementation; any hard CNI dependency in the baseline install; any in-pod/cooperative signal (doctrine forbids it); weakening the lock to make attempts visible.
**Tracking:** [#72](https://github.com/grantbarry29/scrutineer/issues/72) (this note) · [#64](https://github.com/grantbarry29/scrutineer/issues/64) (mechanism of record) · gap row in [`untamperable-pivot.md`](untamperable-pivot.md) §6.

---

## 1. The gap

The routing lock is default-deny: a locked agent's direct connect is dropped **silently by the CNI**. Envoy's access log — the `observed` evidence source — only ever sees traffic that *cooperated* with the proxy env. So the single highest-signal event a governed session can produce ("the agent tried to go around the chokepoint") currently leaves **no record anywhere**.

This is structural, not an oversight: the Kubernetes NetworkPolicy API has no log/redirect/deny-event action. A drop verdict exists only inside each CNI's dataplane — which is exactly why every interim option below is CNI-coupled, and why the pivot filed this as a documented gap rather than a quick fix.

**Evidence bar** (pivot doctrine): any signal here must be produced outside the agent's trust domain (`observed`-grade), and its absence must degrade to *absent* — never to false confidence. Attempt evidence is supplementary telemetry; "no attempt records" must never be read as "no attempts" (coverage varies by cluster).

## 2. Options compared

| Option | How | Assurance | Portability | Cost | Verdict |
|---|---|---|---|---|---|
| **CNI drop telemetry** | Subscribe to the CNI's drop/flow events for session-pod sources: Cilium **Hubble** (gRPC flow API, `verdict=DROPPED`, pod-attributed, OSS); Calico OSS only via per-rule `Log` action → kernel-log scraping (flow logs are Enterprise); kube-network-policies (kindnet, our dev CNI): nothing exposed | Genuinely `observed` (kernel/CNI, agent can't forge or suppress) | Poor — one adapter per CNI, zero coverage on the dev CNI, syslog scraping on Calico OSS is fragile | Optional watcher Deployment + per-CNI adapter + reporter submit path | **Best interim shape** if a deployment target runs Cilium; never baseline |
| **Per-session tripwire listener** | A bait destination (planted endpoint/credentials) the lock deliberately allows; any touch is reported by the tripwire pod's own identity | `observed` | Full (pure Kubernetes) | Small | **Different signal** — detects bait-taking, not bypass attempts; an attempt to an arbitrary IP still leaves nothing. Complementary honeypot idea, not a fix for this gap; file separately if ever wanted |
| **Conntrack sampling** | Privileged DaemonSet sampling conntrack for half-open flows from agent-pod IPs | nominally observed | Medium | Privileged node agent + polling | **Reject** — policy-dropped packets generally never confirm a conntrack entry (filter-table drops discard unconfirmed entries; eBPF CNIs drop before the stack), so the events we want are mostly invisible to it |
| **In-pod observation** | Sidecar sampling the agent's own netns (`ss`/netstat) | self-reported | Full | Small | **Reject on doctrine** — in-pod is the cooperative tier the pivot removed; a compromised agent suppresses it |
| **#64 node interceptor** | Transparent per-node interception sees every SYN from the agent pod *before* policy, with pod attribution; "connection not destined to the session's Envoy" **is** the attempt record | `observed`, unforgeable | Full (independent of the CNI's policy engine) | The #64 epic itself | **Mechanism of record** — subsumes every interim option with strictly better properties |

## 3. Decision

**Defer wholly to #64.** Rationale:

1. It is the only option with portable, `observed`-grade coverage; bypass-attempt evidence falls out of its design for free rather than as a bolt-on.
2. Every interim either measures the wrong thing (tripwire), cannot see the events (conntrack), is doctrinally dead (in-pod), or covers one CNI at real adapter cost (Hubble) — cost that #64 makes disposable.
3. The pivot doctrine tolerates a *stated* gap; it does not tolerate a false-confidence channel. A partially-covered interim that operators mistake for "we detect bypass attempts" is worse than the documented absence.

**Approved contingency** (build only if a deployment target on Cilium needs the signal before #64 lands): an off-by-default watcher Deployment subscribing to Hubble flows filtered to session-pod labels with `verdict=DROPPED`, submitting runtime `network` decisions (`action: deny`, reason `BypassAttempt`) through the standard reporter channel under its own ServiceAccount identity (⇒ `observed` by the identity-derived assurance rule). Coverage must be labeled per-cluster — sessions on clusters without the watcher get no attempt evidence and no claim of it.

## 4. Posture until closed

Unchanged, now explicit: the lock-verification gate (#70) proves *deny works*; Envoy evidence proves *what cooperated*. The space between them — attempts that were denied — is a **documented blind spot** until #64. Operator guidance: a session whose task plainly required egress but produced no egress evidence at all is a weak anomaly signal worth review; it is not, and must not be presented as, attempt detection.
