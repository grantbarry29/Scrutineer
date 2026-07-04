# LLM-Gateway Chokepoint — Untamperable Model-Call Governance

**Status:** draft / deferred — strong feature direction, not scheduled. Sibling to [`tools-pod-chokepoint.md`](tools-pod-chokepoint.md): same chokepoint doctrine ([`untamperable-pivot.md`](untamperable-pivot.md)), different governed action — the model call itself.
**Scope:** an out-of-pod, credential-locked gateway for the agent's LLM API calls that turns the currently-advisory `spec.model` surface into enforced, `observed` governance — provider/model allowlisting, token/cost limits, and prompt/response evidence — at a point the agent cannot bypass or forge.
**Non-goals:** not an inference layer, model router, load balancer, cache, or dev-convenience proxy (LiteLLM / Portkey / Cloudflare AI Gateway territory) — the angle is *governance*, not routing/ops. Not prompt engineering. Local / in-process inference is out of scope (agent/arena track, [`arena-workspace.md`](arena-workspace.md)). Node-level transparency (#64) unchanged.
**Tracking:** epic #77 (deliberately unscheduled; Phase 0 = elevating this draft to a full design answering the open questions below). Absorbs the *enforcement* half of the `spec.model` / `AGENT_MODEL_*` surface (today propagation-only, `internal/controller/job`). Shares credential mediation with #25 (CredentialProfile). Reuses the per-session Envoy-pod chokepoint template (#8) and the reporter identity/assurance machinery.

---

## Why this exists

Today `spec.model` is **advisory**: the controller injects `AGENT_MODEL_PROVIDER/NAME/BASE_URL` as env and the agent *should* honor them, but nothing enforces or observes the actual call. The model request leaves as ordinary TLS egress governed only at the domain level (is `api.openai.com` allowed?) — provider, model, prompt, and token count are CONNECT-opaque. An agent can silently switch models, switch providers, exhaust a token budget, or ship sensitive context to an unapproved endpoint, and Scrutineer neither prevents nor knows.

The model call is the *one universal agent action* — every agent makes it — which makes it arguably the highest-value governance surface, and its advisory status is the last "should" in a product whose doctrine is *untamperable or absent*.

## Shape (agreed direction, detail TBD)

- **Placement:** per-session, on the Envoy-pod template (owner-referenced, dedicated SA, deterministic name, config-hash drift handling). Possibly a filter/route on the *existing* session Envoy rather than a new pod, since model calls already egress through it (§ open questions).
- **Plaintext-boundary trick (already half-wired):** the controller points `AGENT_MODEL_BASE_URL` at the gateway and terminates the agent→gateway leg as first-party TLS / plain HTTP, so the gateway sees the full request (provider, model, messages, params) — no CONNECT opacity. It then makes the real upstream TLS call with the real key. Same "move the plaintext to a point we control" move as the tools pod — and `model.baseURL` + `AGENT_MODEL_BASE_URL` already exist as the configuration seam.
- **Mandatory-ness (two independent locks):**
  1. *Network:* the egress routing lock denies the agent direct reach to provider domains; the only path to a model provider is the gateway.
  2. *Capability:* the gateway holds the provider API keys (#25); the agent ships **credential-empty**. Bypassing the gateway yields unauthenticated requests the provider itself rejects. This is what makes `spec.model` *enforced* rather than advisory — the agent cannot call a model except through the governed path.
- **Policy point (server-side, out of the agent's trust domain):**
  - **Provider/model allowlist** — enforce `spec.model` (e.g. only `openai/gpt-4.1`); deny unapproved models/providers. The field stops being a suggestion.
  - **Token & cost limits** — enforce `spec.model.maxTokens` (today validated, not enforced) plus per-session / aggregate token and cost caps; count *observed* tokens.
  - **Input DLP (optional)** — scan outbound prompts for secrets/PII before they reach the provider.
- **Approval holds:** reuse the dormant `ApprovalRequest` runtime variant — e.g. hold a call that exceeds a cost ceiling or targets a higher-tier model until a human approves. Same channel as the tools pod.
- **Evidence:** provider, model, token usage, and prompt/response recorded at `observed` assurance via a dedicated caller class — the model-call intent, observed at the chokepoint. Prompt/response stored as redacted digests by default (privacy/storage), full capture opt-in (mirrors the tools pod's `ArgDigest`).

## Relationship to the rest of the system

- **To the egress path:** the L7 upgrade for the one known, first-party-terminable endpoint (the model API) — exactly as the tools pod is the L7 upgrade for tool calls. Raw egress still governs everything else at domain level.
- **To `spec.model`:** resolves the advisory-field tension. The field becomes the gateway's config surface (allowlist + caps), and `AGENT_MODEL_BASE_URL` becomes controller-owned (points at the gateway) rather than user free-form.
- **To the tools pod:** siblings. If sequenced, the LLM gateway may be the *stronger first* chokepoint — universal to every agent, crisp cost/DLP/provider-control value prop, smaller surface than a general tool executor.

## Open questions (answer when scheduled)

1. **Separate gateway pod vs. filter on the session Envoy?** Model calls already traverse Envoy; an `ext_authz` + Lua/WASM filter there may suffice, avoiding a second pod — trade against isolating credential handling in its own capability-lock blast radius.
2. **Provider schema coverage.** OpenAI-compatible first (broadest); Anthropic Messages and Bedrock have distinct schemas — normalize to one internal shape, or per-provider adapters?
3. **Prompt/response evidence.** Full capture vs. hash/digest vs. policy-redacted — the audit value of prompts vs. the risk of sensitive prompt data in `status`/audit sinks. Default to digests, opt-in to full.
4. **Streaming (SSE).** Token accounting and evidence for streamed completions; enforce caps mid-stream (cut off) vs. record post-hoc.
5. **Credential mediation (#25).** Static provider keys mounted to the gateway vs. short-TTL per-approved-call minting.
6. **`maxTokens` / `temperature` enforcement.** Today validated but advisory — do they become gateway-enforced, and is the violation behavior clamp or deny?
7. **User-supplied `baseURL` / self-hosted models.** If the user points at their own in-cluster model, does the gateway still mediate (proxy-through) or step aside (and lose enforcement)?

## Honest boundaries (state, don't hide)

- Governs **networked model API calls** routed through the configured endpoint. An agent bundling a **local / in-process model** bypasses this entirely — that is the arena/sandbox track, not this chokepoint.
- The guarantee holds only under the same two-lock rigor as egress/tools: the egress lock must deny direct provider reach *and* the agent must be credential-empty. Absent either, the gateway degrades to advisory — so it ships with the **verified-or-refused** posture wherever the guarantee depends on cluster behavior.
- TLS to the *provider* stays normal outbound TLS from the gateway; only the agent→gateway leg is first-party-terminated. The gateway sees plaintext because the agent is configured to send it there — not because provider TLS is broken.
