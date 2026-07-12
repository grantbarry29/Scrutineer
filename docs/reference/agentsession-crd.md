---
type: Reference
title: The AgentSession CRD
description: "User-facing CRD reference: spec fields, cancellation, reference scoping, RuntimeProfile and AgentPolicy semantics, an inline sample, status fields, and the injected environment variables."
status: live
read_when: "Authoring AgentSession/AgentPolicy/RuntimeProfile manifests, or changing their user-facing semantics."
---

# The `AgentSession` CRD

An `AgentSession` is **one governed autonomous AI agent execution**. It is *not*
a generic workflow task. The spec captures four things:

| Field      | Meaning                                                              |
|------------|----------------------------------------------------------------------|
| `task`     | What the agent should do (description / prompt / prompt ConfigMapRef) |
| `model`    | Which provider/model the agent should call                            |
| `runtime`  | Where/how it should execute (orchestrator, image, command, resources) |
| `policy`   | Inline governance overrides (domains, tools, approvals, quotas)     |
| `policyRefs` | Reusable `AgentPolicy` objects (same namespace)                   |
| `workspace`| Per-session workspace volume (ephemeral for MVP)                      |
| `outputs`  | Whether to retain logs/artifacts                                      |
| `cancelRequested` | When `true`, stop the owned Job and reach terminal `Cancelled` |

### Cancelling a running session

Set `spec.cancelRequested: true` on an existing `AgentSession` (or create one with it already set). The controller:

1. Deletes the owned Job `scrutineer-session-<session-name>` (and child Pods via `Background` propagation).
2. Sets `status.phase` to `Cancelled`, `status.result.outcome` to `cancelled`, and a `Completed` condition with reason `SessionCancelled`.
3. Emits a `SessionCancelled` Kubernetes Event.
4. Does **not** create a new Job while cancellation remains requested.

**Cancel a session that is already running:**

```bash
kubectl patch agentsession my-session --type=merge -p '{"spec":{"cancelRequested":true}}'
kubectl get agentsession my-session -w
kubectl describe agentsession my-session   # Event: SessionCancelled
```

**Create an already-cancelled session** (no Job is started):

```bash
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_cancel.yaml
```

Cancellation stops the **Kubernetes runtime** (Job/Pod). It does not send a graceful shutdown signal to agent logic inside the container; stronger teardown belongs in future runtime profiles.

### Reference scoping (MVP)

External references resolve in the **same namespace** as the `AgentSession`:

| Ref | Kind | Namespace behavior |
|-----|------|-------------------|
| `spec.task.promptConfigMapRef` | ConfigMap | Same namespace as session |
| `spec.policyRefs[]` | AgentPolicy | Same namespace as session |
| `spec.runtimeProfileRef` | RuntimeProfile | Same namespace as session |

Cross-namespace refs are not supported in the MVP. Future CRDs may add an explicit `namespace` field on refs.

### Reusable runtime profile (`RuntimeProfile`)

Platform teams can publish opt-in runtime hardening once; sessions reference a profile via `spec.runtimeProfileRef`.

**Applied to the Job pod template today:**

| Source | Fields merged into Job |
|--------|------------------------|
| Scrutineer baseline | Capability drops (`ALL`), `allowPrivilegeEscalation: false` (busybox-friendly; no forced `runAsNonRoot`) |
| `RuntimeProfile.spec.container` | `runAsNonRoot`, `runAsUser`, `runAsGroup`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation`, `capabilities` (profile wins when set; pair `runAsNonRoot` with `runAsUser` for root-default images like busybox) |
| `RuntimeProfile.spec.pod` | `runtimeClassName`, `seccompProfile`, `automountServiceAccountToken` |

By default the agent pod's ServiceAccount token is **not** mounted, so a compromised agent gets no apiserver credential. Set `spec.pod.automountServiceAccountToken: true` **only** for agents that legitimately call the Kubernetes API, and pair it with a dedicated, minimally-scoped ServiceAccount via `spec.runtime.serviceAccountName` — the token grants whatever that SA's RBAC allows. Under the egress lock the agent's apiserver traffic still transits the session's Envoy proxy (and is recorded as `observed` evidence).

**Status written on reconcile:**

| Field | Meaning |
|-------|---------|
| `status.matchedRuntimeProfile` | Which `RuntimeProfile` was applied (name, UID, resourceVersion) |
| `RuntimeProfileResolved` condition | `ProfileApplied` when a ref resolves; `NoProfileRef` when unset |

**Enforcement backends (`spec.enforcement[]`):** the only type today is **envoy** — a per-session out-of-pod egress proxy (upstream `envoyproxy/envoy` + the first-party `scrutineer-egress-reporter` for `observed` evidence) plus the default-deny routing lock on the agent pod — see `docs/design/evidence-integrity.md` and `docs/design/untamperable-enforcement.md`. Future out-of-pod chokepoints (tools pod, arena workspace) will add new types. Per-binary READMEs live under [`cmd/`](../../cmd/).

**Runtime reporter wiring:** the egress-proxy pod (not the agent pod) carries:

| Injected into the egress-reporter | Value |
|-----------------------------------|-------|
| `SCRUTINEER_REPORTER_URL` | `http://scrutineer-controller-reporter.scrutineer-system.svc:8088` (base URL; append `/v1/report`) |
| `SCRUTINEER_REPORTER_TOKEN_PATH` | `/var/run/secrets/scrutineer/reporter-token/token` |
| Projected volume | ServiceAccount token with audience `scrutineer-reporter` (600s expiry, kubelet-refreshed) |

The agent container does **not** receive the reporter URL or token — evidence comes only from outside its trust domain. Deploy the controller with `make deploy` (or `make dev-deploy`) so the `scrutineer-controller-reporter` Service exposes port `:8088`. Contract: [`docs/design/phase-3-runtime-reporter-contract.md`](../design/phase-3-runtime-reporter-contract.md).

**Profile change behavior:**

- Updating a referenced `RuntimeProfile` re-reconciles affected sessions (controller watch).
- If the owned Job has **not** started pods yet (`Active==0`), the controller **replaces** the Job so the pod template matches.
- If the Job is **already running**, pod templates are immutable — the running pod may retain the old security context until the Job is replaced manually or the session ends.

**Samples:**

```bash
kubectl apply -f config/samples/scrutineer_v1alpha1_runtimeprofile.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_runtimeprofile_ref.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_runtimeprofile_enforcement.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_runtimeprofile_enforcement.yaml
```

### Reusable policy (`AgentPolicy`)

Platform teams can publish baseline governance once; sessions reference policies and add inline overrides.

**Merge order** (implemented in [`internal/policy/`](../../internal/policy/)):

1. `spec.policyRefs` in list order
2. `spec.policy` inline overrides last (wins on conflict)
3. List fields are unioned
4. Effective **mode** = strictest across matched policies (`enforced` > `dry-run` > `audit-only`)

**Status written on reconcile:**

| Field | Meaning |
|-------|---------|
| `status.effectivePolicy` | Merged rules + mode propagated to the Job |
| `status.matchedPolicies` | Which policy CRDs contributed |
| `status.policyDecisions` | Bounded merge-time audit log (max 64) |

**Propagation today:** `AGENT_POLICY_*` and `AGENT_POLICY_MODE` env vars on the agent container (a propagation hook, not enforcement). Network FQDN rules are **enforced** at the per-session out-of-pod Envoy proxy when the `envoy` backend is enabled; `audit-only` / `dry-run` / `enforced` modes govern whether a matched rule blocks or only records. There is no tool/file policy surface until the tools/arena chokepoints land (#75 clean break).

**Policy change behavior:**

- Updating a referenced `AgentPolicy` re-reconciles affected sessions (controller watch).
- `status.effectivePolicy` updates immediately.
- If the owned Job has **not** started pods yet (`Active==0`), the controller **replaces** the Job so env vars match.
- If the Job is **already running**, pod templates are immutable — env inside the pod may be stale; `PolicyPropagated=False` / `PolicyEnvDrift` surfaces the gap.

**Samples:**

```bash
kubectl apply -f config/samples/scrutineer_v1alpha1_agentpolicy.yaml
kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession_policy_ref.yaml

# prod-agents-baseline must exist for the combined sample:
```

### Inline sample

```yaml
apiVersion: scrutineer.sh/v1alpha1
kind: AgentSession
metadata:
  name: github-readme-update
  namespace: default
spec:
  task:
    description: "Update the README with installation instructions"
    prompt: "Clone the repo, inspect the README, and propose an updated version."
  model:
    provider: openai
    name: gpt-4.1
    temperature: "0.2"
    maxTokens: 4096
  runtime:
    orchestrator: kubernetes-job
    image: busybox:latest
    command:
    - sh
    - -c
    - "echo Running governed agent session; echo $AGENT_TASK_PROMPT; sleep 5; echo Done"
    timeoutSeconds: 900
    serviceAccountName: default
    resources:
      requests:
        cpu: "500m"
        memory: "512Mi"
      limits:
        cpu: "2"
        memory: "2Gi"
  policy:
    # Domains match exactly, or use a "*." wildcard for a subdomain tree
    # (e.g. "*.github.com" covers api.github.com but not github.com itself).
    allowedDomains: ["github.com", "*.github.com"]
    deniedDomains:  ["dropbox.com", "*.gmail.com"]
    requireHumanApproval: [production_deploy, external_write]
  workspace:
    ephemeral: true
    size: 5Gi
    mountPath: /workspace
  outputs:
    collectLogs: true
    collectArtifacts: false
    artifactPath: /workspace/artifacts
```

### Status fields

| Field | Populated? | Meaning |
|-------|------------|---------|
| `phase` | Yes | `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied` / `Cancelled` |
| `observedGeneration` | Yes | Last spec generation reconciled |
| `startTime` | Yes | Set when the owned Job is first created |
| `completionTime` | Yes | Set when the session reaches a terminal phase |
| `conditions` | Yes | See [Conditions](controller-reference.md#conditions) |
| `runtimeRef` | Yes | Backend-neutral identity of the runtime object created (`apiVersion`/`kind`/`name`/`uid`); `kind` is `Job` or `Pod`. Prefer this over `jobName`. |
| `jobName` / `podName` | Yes | `jobName`: owned Job name (**deprecated** alias of `runtimeRef.name`; empty for the `kubernetes-pod` backend). `podName`: the agent Pod (newest Job-owned Pod, or the Pod itself for `kubernetes-pod`). |
| `matchedPolicies` | Yes | Policy CRDs that contributed to `effectivePolicy` |
| `effectivePolicy` | Yes | Merged rules + mode propagated to the Job |
| `policyDecisions` | Yes | Merge-time audit entries plus runtime decisions reported by the data plane (egress-reporter → reporter, max 64) |
| `matchedRuntimeProfile` | Yes | Applied `RuntimeProfile` ref (when set) |
| `result` | Yes | Terminal outcome / summary (on success, failure, timeout, cancel) |
| `usage` | **Yes** (from runtime reports) | Network/tool decisions increment counters; optional `usage` delta in `POST /v1/report` for tokens |
| `violations` | **Yes** (runtime reports) | Bounded list; `deny` and `dry-run` outcomes via `ApplyRuntimePolicyReport` |
| `events` | **Yes** (runtime reports) | Structured timeline stream (max 256); appended via `POST /v1/report` `events[]` |
| `artifacts` | **Yes** (when `spec.outputs` enabled) | Collected logs (ConfigMap) + workspace tar (Secret) references |

### Environment variables injected into the agent container

Scrutineer always injects these (empty when not set):

```
SCRUTINEER_SESSION_NAME
SCRUTINEER_SESSION_NAMESPACE
AGENT_TASK_DESCRIPTION
AGENT_TASK_PROMPT
AGENT_MODEL_PROVIDER
AGENT_MODEL_NAME
AGENT_MODEL_BASE_URL                 # optional; OpenAI-compatible endpoint override (e.g. OpenRouter)
AGENT_POLICY_ALLOWED_DOMAINS         # comma-separated
AGENT_POLICY_DENIED_DOMAINS          # comma-separated
AGENT_POLICY_ALLOWED_CIDRS           # comma-separated
AGENT_POLICY_DENIED_CIDRS            # comma-separated
AGENT_POLICY_REQUIRE_HUMAN_APPROVAL  # comma-separated
AGENT_POLICY_MODE
```

`AGENT_POLICY_*` values are propagated from merged policy to the agent container as a propagation/debugging hook. Enforcement happens out-of-pod: the session's Envoy proxy applies FQDN allow/deny per the effective mode.

Plus any `spec.runtime.env` entries the user adds.

