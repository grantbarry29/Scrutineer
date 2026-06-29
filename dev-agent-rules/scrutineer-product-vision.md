
# Scrutineer Product Vision

Scrutineer is a Kubernetes-native governance and runtime control plane for autonomous AI agents. Its purpose is secure governance and runtime control, not orchestration, task running, workflow execution, or a generic agent framework.

## Core Direction

- Prioritize governance, security, policy, runtime control, observability, auditability, approval workflows, identity isolation, and runtime isolation.
- Treat Kubernetes Jobs, Tekton, Argo Workflows, Temporal, and external orchestrators as execution backends Scrutineer can govern, not systems Scrutineer should replace.
- Keep Scrutineer orchestrator-agnostic. Avoid coupling APIs or controllers permanently to Kubernetes Jobs.
- Use Kubernetes-native patterns: CRDs, controllers, reconciliation, status subresources, declarative APIs, events, owner references, and idempotent behavior.
- Prefer clean, extensible APIs over feature count. Preserve room for future CRDs such as AgentPolicy, ToolPolicy, ApprovalPolicy, RuntimeProfile, ToolGateway, CredentialProfile, and SessionTemplate.
- Keep a clear control-plane / data-plane distinction. Scrutineer APIs and controllers declare policy and desired governance state; enforcement may happen through sidecars, gateways, eBPF agents, DNS proxies, sandboxes, or orchestrator adapters.

## MVP Scope

The current MVP vertical slice is AgentSession reconciliation into Kubernetes Jobs with inline policy, runtime status tracking, Kubernetes events, workspace mounting, basic resource/time constraints, and policy propagation via environment variables.

The MVP should establish the API shape, lifecycle model, policy model, extensible reconciliation architecture, and future enforcement hooks. It does not need real network enforcement, real sandboxing, dashboards, distributed execution, multi-cluster support, or sophisticated isolation yet.

## Trust And Threat Model

- Scrutineer protects enterprises from unsafe autonomous behavior: prompt-injected agents, malicious or overbroad tools, credential exfiltration, unexpected network access, unsafe file writes, runaway loops, unapproved production actions, and weak auditability.
- Assume multi-tenant enterprise clusters over time. Design for namespace isolation, per-session identity, scoped service accounts, scoped credentials, impersonation boundaries, least privilege, and auditable delegation.
- Do not make agents smarter, choose prompts, optimize reasoning, or become an agent SDK. Scrutineer governs agents regardless of implementation.
- **Evidence integrity is part of the threat model.** A governance and audit product is only trustworthy if the evidence it records cannot be forged or suppressed by the very agent it governs. Scrutineer must be honest about its current guarantee level: today's sidecar enforcement/reporting is **cooperative** — enforcement components and the agent share a pod and ServiceAccount, so a fully compromised agent could still tamper with or starve the data plane. Adversarial-grade integrity requires isolating the data plane from the agent (separate identity/network namespace, kernel/eBPF observation the agent cannot bypass, or out-of-pod enforcement). Track the gap between *cooperative* and *adversarial* integrity explicitly; never overstate enforcement strength in docs, status, or UI.

## Policy And Enforcement Model

- Distinguish declared policy, propagated policy, observed behavior, and enforced behavior. Environment variables are propagation hooks, not real enforcement.
- **Near-term focus (Phase 3b):** close the gap between *propagated* and *observed* by building the **runtime evidence loop** — a controller-owned reporter that lets data-plane backends populate `status.policyDecisions`, `status.violations`, and structured events. Observed governance (what actually happened) is the prerequisite for the observability, audit, and UI surfaces; build the evidence pipeline before the surfaces that consume it. See `docs/design/phase-3-enforcement-architecture.md`.
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
- Leverage existing infrastructure such as Kubernetes, Envoy, Cilium/eBPF, gVisor/Kata/Firecracker, NetworkPolicy, DNS proxies, tool gateways, and AI provider APIs.
- Future enforcement may include Envoy sidecars, DNS/FQDN egress control, Cilium/eBPF, process/syscall monitoring, secure sandboxes, tool gateways, MCP governance, and policy violation reporting.
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
