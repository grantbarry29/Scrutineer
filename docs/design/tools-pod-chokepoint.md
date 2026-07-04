# Tools-Pod Chokepoint — Untamperable Tool Governance

**Status:** draft / deferred (design TODO from the pivot; not scheduled)
**Scope:** the out-of-pod successor to the cooperative in-pod tool tier removed in the pivot (#71): a per-session tools pod that *executes* tool calls the agent can only reach through the session's Envoy, holding the credentials the agent never sees. Restores tool policy, argument rules, and mid-execution approval holds — this time `observed` and mandatory.
**Non-goals:** local (non-network) tool interception — that is the arena/sandbox track ([`arena-workspace.md`](arena-workspace.md), #29); node-level transparency (#64).
**Tracking:** to be filed when scheduled; absorbs #25 (CredentialProfile / credential mediation). The dormant `ApprovalRequest` runtime variant + reporter approval channel are live in the tree (`internal/reporter`, `internal/controller/agentsession/approval_runtime.go`); the tool/argument policy schema was removed in the #75 clean break and lives in git history (pre-#75 `api/v1alpha1/`), along with the pre-pivot cooperative-tier designs (deleted in #74).

---

## Shape (agreed direction, detail TBD)

- **Placement:** per-session pod (Envoy-pod template: owner-referenced, dedicated SA, deterministic name, config-hash drift handling). Runs the MCP servers / tool executors for the session.
- **Mandatory-ness (two independent locks):**
  1. *Network:* tools-pod ingress admits only the session's Envoy pod; the agent's egress lock admits only Envoy. The agent→tools hop is plain HTTP through the explicit proxy, so Envoy sees method/path/body — full L7 filtering, no CONNECT opacity.
  2. *Capability:* the tools pod holds all tool credentials (#25). The agent pod ships credential-empty, so bypassing the chokepoint yields requests the upstream provider itself rejects.
- **Policy point:** Envoy `ext_authz` (or in-pod-at-the-tools-pod admission) evaluating the inherited tool policy engine — allow/deny lists, argument constraints, rate/count limits — server-side, out of the agent's trust domain.
- **Approval holds:** reuse the dormant `ApprovalRequest` CRD + reporter approval channel unchanged; the hold moves from the removed cooperative tier to the chokepoint, where the agent cannot skip it.
- **Evidence:** tool-call payloads are *intent, observed at the chokepoint* — recovering the intent-vs-observed signal the pivot temporarily dropped, at `observed` assurance, stamped via a dedicated caller class.

## Open questions (answer when scheduled)

1. Policy CRD surface: reintroduce the pre-pivot tool/argument fields (git history, pre-#75) as shipped, or reshape around the executor model (tool *catalog* + per-tool grants)?
2. ext_authz at Envoy vs. admission inside the tools pod (or both — defense in depth vs. config sprawl)?
3. Tool runtime packaging: one image with declared MCP servers, per-tool containers, or user-supplied image with an injected supervisor?
4. Result-path governance: filter/annotate tool *responses* (e.g. secret-scanning results returning to the agent)?
5. Credential issuance: static mounts into the tools pod vs. short-TTL per-approved-call minting (the full #25 design).
