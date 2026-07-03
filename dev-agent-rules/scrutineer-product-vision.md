
# Scrutineer Product Vision

Scrutineer is a Kubernetes-native governance and runtime control plane for autonomous AI agents. Its purpose is secure governance and runtime control, not orchestration, task running, workflow execution, or a generic agent framework.

## Core Direction

- Prioritize governance, security, policy, runtime control, observability, auditability, approval workflows, identity isolation, and runtime isolation.
- Treat Kubernetes Jobs, Tekton, Argo Workflows, Temporal, and external orchestrators as execution backends Scrutineer can govern, not systems Scrutineer should replace.
- Keep Scrutineer orchestrator-agnostic. Avoid coupling APIs or controllers permanently to Kubernetes Jobs.
- Use Kubernetes-native patterns: CRDs, controllers, reconciliation, status subresources, declarative APIs, events, owner references, and idempotent behavior.
- Prefer clean, extensible APIs over feature count. Preserve room for future CRDs such as AgentPolicy, ToolPolicy, ApprovalPolicy, RuntimeProfile, ToolGateway, CredentialProfile, and SessionTemplate.
- Keep a clear control-plane / data-plane distinction. Scrutineer APIs and controllers declare policy and desired governance state; enforcement happens **only at out-of-trust-domain chokepoints** — per-session proxy pods, tool/workspace chokepoint pods, node-level interceptors, sandboxes — never through components the governed agent could tamper with or bypass (see `docs/design/untamperable-pivot.md`).

## MVP Scope

The current MVP vertical slice is AgentSession reconciliation into Kubernetes Jobs with inline policy, runtime status tracking, Kubernetes events, workspace mounting, basic resource/time constraints, and policy propagation via environment variables.

The MVP should establish the API shape, lifecycle model, policy model, extensible reconciliation architecture, and future enforcement hooks. It does not need real network enforcement, real sandboxing, dashboards, distributed execution, multi-cluster support, or sophisticated isolation yet.

## Trust And Threat Model

- Scrutineer protects enterprises from unsafe autonomous behavior: prompt-injected agents, malicious or overbroad tools, credential exfiltration, unexpected network access, unsafe file writes, runaway loops, unapproved production actions, and weak auditability.
- Assume multi-tenant enterprise clusters over time. Design for namespace isolation, per-session identity, scoped service accounts, scoped credentials, impersonation boundaries, least privilege, and auditable delegation.
- Do not make agents smarter, choose prompts, optimize reasoning, or become an agent SDK. Scrutineer governs agents regardless of implementation.
- **Enforcement doctrine: untamperable or absent — never advisory.** A governance and audit product is only trustworthy if the evidence it records cannot be forged or suppressed, and the controls it claims cannot be bypassed, by the very agent it governs. Scrutineer therefore ships **only adversarial-grade enforcement**: every enforcement/observation point lives in a trust domain the agent has no privilege to alter (separate pod, own identity and network namespace) and is made mandatory by a layer the agent cannot modify (default-deny routing lock). The cooperative in-pod tier was deliberately removed (`docs/design/untamperable-pivot.md`): a control the agent must opt into is advisory, and advisory controls presented as governance are the failure mode this product exists to eliminate. Corollaries: (1) **verified or refused** — where a guarantee depends on cluster behavior (e.g. the CNI enforcing NetworkPolicy), Scrutineer empirically verifies it and refuses to run enforced sessions otherwise; silent degradation is prohibited. (2) A governance domain with no untamperable chokepoint yet (tools, files) gets **no policy surface** until its chokepoint exists — no CRD fields without an enforcement backend. (3) Evidence keeps its **assurance label** so any future weaker signal is visibly weaker; never overstate enforcement strength in docs, status, or UI.

## Policy And Enforcement Model

- Distinguish declared policy, propagated policy, observed behavior, and enforced behavior. Environment variables are propagation hooks, not real enforcement.
- **Near-term focus (the untamperable pivot):** the per-session out-of-pod Envoy egress path + default-deny routing lock is the sole enforcement plane. In order: (1) the **verified-or-refused** lock gate, (2) removal of the in-pod enforcement tier and its policy surface, (3) L4/L7 hardening, then extending the same chokepoint pattern to tools and files. See `docs/design/untamperable-pivot.md` (and its deferred designs `tools-pod-chokepoint.md`, `arena-workspace.md`).
- Policies should eventually be reusable, versioned, composable, explainable, and support modes such as dry-run, audit-only, and enforced.
- Human approval should become scoped and auditable: approve one tool call, domain, file write, deployment, credential use, or bounded time window rather than a broad boolean.
- Audit and evidence are first-class outputs. Scrutineer should explain who authorized a run, what identity acted, what policy matched, what was allowed or denied, what changed, and what runtime evidence was observed.
- Observed evidence should carry its **assurance level**: distinguish self-reported (cooperative sidecar/agent) evidence from independently-observed (kernel, gateway, or out-of-pod) evidence so audit consumers know how much to trust each record.

## Operational UI Vision

- Scrutineer should eventually include a first-class operational UI for visibility, governance, auditability, runtime observability, approvals, and debugging autonomous AI systems.
- The UI must not become a chatbot UI, ChatGPT clone, conversational frontend, or consumer AI product. It should feel closer to Kubernetes dashboards, Datadog, Grafana, Argo UI, Lens, security operations dashboards, and runtime observability platforms.
- The UI should answer operational questions: what agents are running, what an agent is doing now, which tools/domains/files/credentials it used, which actions were blocked, why policy violations happened, what needs approval, which sessions failed, and what token/tool/network usage occurred.
- Long-term views should include session timelines, live policy and network activity, tool governance, scoped approvals, runtime topology, audit and forensics, replayable sessions, traces, violations, usage, and historical analytics.
- Backend APIs and controllers should be designed for future UI consumption: emit structured timestamped events, maintain normalized session state, store policy decisions and violations cleanly, keep status and conditions consistent, and model observability as a product surface.

## Design Guidance

- Do not rebuild schedulers, workflow engines, container runtimes, or generic agent frameworks.
- Leverage existing infrastructure such as Kubernetes, Envoy, Cilium/eBPF, gVisor/Kata/Firecracker, NetworkPolicy, network-served workspaces (FUSE/9p), and AI provider APIs.
- Future enforcement extends the out-of-pod chokepoint pattern: per-session Envoy egress with FQDN policy (shipped), the tools-pod chokepoint with credential mediation, the arena workspace, node-level transparent interception (eBPF), process/syscall observation, and secure sandboxes.
- Future observability should support timelines, network traces, tool call logs, policy violations, replayable sessions, audit trails, token/tool metrics, and runtime analytics.
- Preserve extension points for orchestrator adapters, enforcement backends, policy engines, tool gateways, identity providers, audit sinks, and observability exporters.
- Scrutineer should feel closer to Kubernetes, Envoy, Cilium, Istio, Vault, Boundary, and Tailscale than chatbot frameworks, prompt wrappers, or consumer AI tooling.
- Scrutineer is evolving toward a runtime governance platform, observability platform for AI agents, secure execution control plane, and Datadog/Splunk-like product for autonomous AI systems.

## Cursor / AI Development Guidance

This file describes **product direction**, not permission to implement the full roadmap in one pass.

- Work on **one narrow task or vertical slice** at a time. Task state lives in **GitHub Issues** (see `dev-agent-rules/task-management.md`); read `docs/design/` (+ component READMEs/code comments) for durable technical context and `dev-agent-rules/scrutineer-cursor-workflow.md` for implementation rules.
- Follow the **Cursor Implementation Contract** in `dev-agent-rules/task-management.md` and `dev-agent-rules/scrutineer-cursor-workflow.md` before coding.
- Follow **Out-of-Scope Future Work Handling** in `dev-agent-rules/scrutineer-cursor-workflow.md`: do not silently implement adjacent future work; **every** out-of-scope item must become a **GitHub Issue** (label `agent-discovered`) in the **same session** — chat-only notes are not tracking.
- End implementation summaries with **### Out-of-scope future work noticed**; each bullet must cite the tracking issue or confirm the issue was created (see project-status rules).
- **Do not** implement multiple roadmap phases in a single change. Do not bundle unrelated capabilities.
- **Do not** add new CRDs, sidecars, webhooks, dashboards, policy engines, Envoy, Cilium, eBPF, gVisor/Kata, or tool gateways unless the user explicitly requests them.
- Preserve **control-plane / data-plane separation**: controllers declare and propagate governance; enforcement belongs in future data-plane components.
- Prefer **small, reviewable, incremental** diffs. A good change usually touches a few files and has a clear acceptance criterion.
- **Do not** turn Scrutineer into a generic workflow engine, task runner, or agent framework.
- **Do not** overfit APIs or reconciler logic to Kubernetes Jobs; keep orchestrator adapters and extension points in mind.
- When uncertain about long-term design, file **scoped GitHub Issues** (or TODOs in code) instead of speculative architecture.
- Maintain Kubernetes-native controller discipline: idempotent reconciliation, owner references, status subresources, conditions, events, and least-privilege RBAC.

The central product question is: How can enterprises safely allow autonomous AI systems to operate inside real environments?

A successful Scrutineer deployment should let a platform or security team answer, for every autonomous agent run: who authorized it, what it could access, what it actually did, what was blocked, what changed, and how to reproduce or audit the decision.
