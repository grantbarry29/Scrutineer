---
type: Reference
title: AgentSession Controller Reference
description: "The full controller behavior catalog: reconcile triggers and flow, validation, task/policy/profile resolution, Job lifecycle, phase mapping, conditions, Kubernetes events, inspection commands, and the shipped-capability quick reference."
status: live
read_when: "Changing reconciler behavior, or looking up conditions/events/phase semantics."
---

# AgentSession Controller Reference

The controller lives in [`internal/controller/agentsession/`](../../internal/controller/agentsession/) (reconcile loop, policy/runtime watches, validation) and delegates pod/Job construction to [`internal/controller/job/`](../../internal/controller/job/) (build, explicit-proxy env injection, drift detection, status helpers — see its [package README](../../internal/controller/job/README.md)). Orchestrator-specific work goes through a backend-neutral `runtimeBackend` interface with two backends today — `kubernetes-job` and `kubernetes-pod`, both built from the shared `job.BuildPodTemplateSpec`; the reconciler maps each backend's normalized observation onto status, so governance semantics stay backend-independent. See the [package README](../../internal/controller/agentsession/README.md).

### Reconcile triggers

| Source | Mechanism | Effect |
|--------|-----------|--------|
| `AgentSession` | Primary `For()` watch | Any spec/status change on the session |
| Owned `Job` | `Owns(&batchv1.Job{})` | Job status transitions re-queue the parent session |
| Session `Pod` | `Watches(&corev1.Pod{})` | Job-owned Pods labeled `scrutineer.sh/session=<name>` re-queue the session (faster `podName` / Running updates) |
| `AgentPolicy` | Secondary watch | Sessions with matching `spec.policyRefs` re-reconcile |
| `RuntimeProfile` | Secondary watch | Sessions with matching `spec.runtimeProfileRef` re-reconcile |
| `ApprovalRequest` | Secondary watch | Approval grant/deny for a session re-reconciles it (gate resume + per-tool holds) |
| Timer | `RequeueAfter: 15s` | Backstop poll while Job is in flight (non-terminal sessions) |

### Reconcile flow

```
Fetch AgentSession
    │
    ├─ deleting? ──► stop owned Job ──► remove finalizer ──► return
    │
    ├─ ensure finalizer scrutineer.sh/finalizer
    │
    ├─ phase = Pending (first observation); observedGeneration = generation
    │
    ├─ validateSpec ──fail──► Denied, Validated=False, Ready=False, events ──► return
    │
    ├─ resolveTask (inline prompt or ConfigMap ref) ──fail──► Denied ──► return
    │
    ├─ resolvePolicy (policyRefs merge + inline overrides) ──fail──► Denied ──► return
    │       └── status.effectivePolicy, matchedPolicies, policyDecisions, PolicyResolved
    │
    ├─ resolveRuntimeProfile (optional ref) ──fail──► Denied ──► return
    │       └── matchedRuntimeProfile, RuntimeProfileResolved
    │
    ├─ cancelRequested? ──► delete Job ──► Cancelled, Completed, Ready=False ──► return
    │
    ├─ requireHumanApproval matches an ApprovalPolicy? ──► AwaitingApproval (create ApprovalRequest)
    │       ├── granted ──► proceed         ├── denied / onTimeout=deny ──► Denied
    │       └── no matching ApprovalPolicy ──► ApprovalNotEnforced warning (no gate)
    │
    ├─ already terminal? ──► patch status ──► return (no new Job)
    │
    ├─ ensureJob (create or sync owned Job)
    │       ├── policy env drift / runtime profile drift on pending Job → replace Job
    │       └── active Job with stale policy env → PolicyEnvDrift condition + warning event
    │
    ├─ syncStatusFromJob (Running / Succeeded / Failed / TimedOut)
    ├─ findPodName (newest Pod owned by current Job UID)
    ├─ set Ready condition from phase
    └─ patch status; requeue after 15s if non-terminal
```

Reconciliation is **idempotent**. Status updates use the status subresource with condition merging so concurrent writes do not drop condition types. The owned Job is named deterministically `scrutineer-session-<session-name>`; a foreign Job at that name causes `Phase=Denied` (`JobConflict`).

### Validation (`validateSpec`)

Controller-side checks (in addition to CRD OpenAPI validation):

| Check | Denial reason |
|-------|---------------|
| Task: description, prompt, or `promptConfigMapRef` required | `InvalidSpec` |
| `runtime.image` and `model.provider` / `model.name` non-empty | `InvalidSpec` |
| `runtime.orchestrator` must be `kubernetes-job` or `kubernetes-pod` | `InvalidSpec` |
| Temperature in `[0, 2]`; `maxTokens >= 1`; `timeoutSeconds >= 1` | `InvalidSpec` |
| `policyRefs[].kind` is `AgentPolicy` | `InvalidSpec` |
| `runtimeProfileRef` shape | `InvalidSpec` |
| Workspace `size` parseable as quantity | `InvalidSpec` |
| Missing ConfigMap / key (task resolution) | `InvalidTask` |
| Missing or invalid `policyRefs` target (policy resolution) | `InvalidPolicy` |
| Missing `RuntimeProfile` (profile resolution) | `InvalidRuntimeProfile` |
| Foreign Job occupies deterministic name | `JobConflict` |

### Task resolution

- Inline `spec.task.description` and `spec.task.prompt` pass through to Job env vars.
- `spec.task.promptConfigMapRef` loads the prompt from a ConfigMap key in the **same namespace** as the session.

### Policy resolution and propagation

Merge order:

1. `spec.policyRefs` in list order
2. `spec.policy` inline overrides last
3. List fields union
4. Effective mode = strictest (`enforced` > `dry-run` > `audit-only`)

Written to status each reconcile: `effectivePolicy`, `matchedPolicies`, `policyDecisions` (merge-time + runtime, max 64). Propagated to the Job as `AGENT_POLICY_*` env vars (a hook, not enforcement); FQDN rules are enforced at the session's out-of-pod Envoy proxy, whose egress-reporter reports runtime evidence.

When a referenced policy changes:

- `status.effectivePolicy` updates immediately
- **Pending** Job (`Active==0`): controller **replaces** the Job (`PolicyEnvSynced` event)
- **Active** Job: pod template is immutable; `PolicyPropagated=False` / `PolicyEnvDrift` surfaces stale env

### Runtime profile resolution

When `spec.runtimeProfileRef` is set, the controller loads the `RuntimeProfile` (same namespace) and merges container/pod security fields plus enabled `spec.enforcement[]` into the Job template (`envoy` — the only type — provisions the out-of-pod proxy and points the agent at it). Profile drift (including enforcement changes) follows the same pending-Job-replace rules as policy env drift.

### Job lifecycle (`internal/controller/job`)

| Setting | Value |
|---------|-------|
| Name | `scrutineer-session-<session-name>` |
| Labels | `scrutineer.sh/session`, `app.kubernetes.io/name=scrutineer`, `app.kubernetes.io/component=agent-session` |
| `backoffLimit` | `0` |
| `ttlSecondsAfterFinished` | `300` |
| Container | `agent`; baseline drops `ALL` capabilities, `allowPrivilegeEscalation=false` |
| Workspace | Optional `emptyDir` when `spec.workspace.ephemeral=true` |

### Phase mapping from Job status

| Job observation | Session `phase` | `Completed` condition |
|-----------------|---------------|----------------------|
| `status.succeeded > 0` | `Succeeded` | `True` / `JobSucceeded` |
| `status.active > 0` | `Running` | (unchanged) |
| `DeadlineExceeded` condition | `TimedOut` | `False` / `JobTimedOut` |
| `status.failed > backoffLimit` | `Failed` | `False` / `JobFailed` |
| Job created, not yet active | `Starting` | (unchanged) |

### `status.podName` selection

1. List Pods in the session namespace with label `scrutineer.sh/session=<session.Name>`
2. Keep only Pods whose ownerReference points at the **current** Job UID
3. Pick the Pod with the latest `creationTimestamp` (name breaks ties lexicographically)

### Cancellation and deletion

**Cancellation** (`spec.cancelRequested: true`): deletes the owned Job, sets `phase=Cancelled`, `result.outcome=cancelled`, `Completed=True` / `SessionCancelled`, `Ready=False`. Idempotent when the Job is already gone.

**Deletion**: finalizer `scrutineer.sh/finalizer` blocks AgentSession removal until the owned Job is deleted. `blockOwnerDeletion` is cleared on the Job so deletion cannot deadlock.

### Conditions

| Type | When `True` | When `False` | Common reasons |
|------|-------------|--------------|----------------|
| `Validated` | Spec accepted | Validation / resolution failed | `SpecValid`, `InvalidSpec`, `InvalidTask`, `InvalidPolicy`, `InvalidRuntimeProfile`, `JobConflict` |
| `PolicyResolved` | Policies merged | — | `PoliciesMerged` |
| `PolicyPropagated` | Job env matches effective policy | Active Job has stale env | `EnvCurrent`, `PolicyEnvDrift` |
| `RuntimeProfileResolved` | Profile applied or not referenced | — | `ProfileApplied`, `NoProfileRef` |
| `RuntimeCreated` | Owned Job exists | — | `JobCreated` |
| `Completed` | Terminal success or cancel | Terminal failure / timeout | `JobSucceeded`, `JobFailed`, `JobTimedOut`, `SessionCancelled` |
| `Ready` | Session running or succeeded | Not yet running, denied, failed, timed out, or cancelled | `JobRunning`, `JobSucceeded`, `NotReady`, `SessionDenied`, `JobFailed`, `JobTimedOut`, `SessionCancelled` |

`Ready` is an **aggregate** summary derived from `status.phase` — not a Pod readiness probe. It answers: “Is this session actively running or successfully finished?”

### Kubernetes Events

Inspect with:

```bash
kubectl describe agentsession <name> -n <namespace>
kubectl get events -n <namespace> --field-selector involvedObject.kind=AgentSession
```

| Reason | Type | When emitted |
|--------|------|--------------|
| `ValidationFailed` | Warning | Spec validation or task/policy/profile resolution failed |
| `SessionDenied` | Warning | Session reached `Phase=Denied` |
| `JobCreated` | Normal | Owned Job created |
| `JobRunning` | Normal | Job has active pods (`Phase=Running`) |
| `JobSucceeded` | Normal | Job completed successfully |
| `JobFailed` | Warning | Job failed or timed out |
| `SessionCancelled` | Normal | `spec.cancelRequested` processed |
| `ApprovalNotEnforced` | Warning | `requireHumanApproval` declared but no `ApprovalPolicy` gates it |
| `ApprovalRequested` | Normal | Session is blocked on a human approval gate (`AwaitingApproval`) |
| `ApprovalGranted` | Normal | Approval granted; session resumes |
| `ApprovalDenied` | Warning | Approval denied or timed out; session `Denied` |
| `ApprovalNotified` | Normal | Approvers notified of an open gate (`--approval-webhook-url`) |
| `ApprovalNotifyFailed` | Warning | Approval notification delivery failed (will retry) |
| `ApprovalUnauthorized` | Warning | Grant set by a subject not listed in the policy's approvers; not honored |
| `ApprovalPartiallyApproved` | Normal | `allOf` gate received a valid grant but still needs more approvers |
| `PolicyResolved` | Normal | Referenced policies merged |
| `RuntimeProfileResolved` | Normal | RuntimeProfile applied to Job template |
| `PolicyEnvDrift` | Warning | Effective policy changed but active Job env is stale |
| `PolicyEnvSynced` | Normal | Pending Job replaced to sync policy env |

### Inspecting a session

```bash
# High-level phase and conditions
kubectl get agentsession <name> -o jsonpath='{.status.phase}{"\n"}{range .status.conditions[*]}{.type}={.status} ({.reason}){"\n"}{end}'

# Effective policy and Job linkage
kubectl get agentsession <name> -o jsonpath='{.status.effectivePolicy.mode}{"\n"}{.status.jobName}{"\n"}{.status.podName}{"\n"}'

# Owned Job and labeled Pods
kubectl get job scrutineer-session-<name> -o wide
kubectl get pods -l scrutineer.sh/session=<name>
```

---

## Current behavior (quick reference)

| Capability | Shipped? | Notes |
|------------|----------|-------|
| Reconcile to Kubernetes runtime | Yes | `runtime.orchestrator: kubernetes-job` (default) or `kubernetes-pod`, via the `runtimeBackend` interface; `status.runtimeRef` records the object created |
| Task prompt / ConfigMap prompt | Yes | `spec.task` or `promptConfigMapRef` (same namespace) |
| `AgentPolicy` + `spec.policyRefs` | Yes | Same-namespace merge → `status.effectivePolicy`; inline `spec.policy` overrides win |
| Policy modes (`audit-only` / `dry-run` / `enforced`) | Yes | Strictest mode in status + `AGENT_POLICY_MODE`; **enforced** at the egress chokepoint |
| `status.policyDecisions` | Yes | Merge-time + runtime decisions (max 64) |
| Policy / profile change → Job env sync | Partial | Replaces **pending** Jobs; `PolicyEnvDrift` if Job already active |
| `RuntimeProfile` + `runtimeProfileRef` | Yes | Same-namespace; merges into Job pod template; watch + pending Job replace |
| Enforcement backends (`spec.enforcement`) | Yes | Out-of-pod `envoy` egress proxy with `observed` evidence (the only type; the cooperative in-pod tier was removed, #71) |
| **Network egress enforcement** | Yes | Out-of-pod `envoy` egress proxy + default-deny routing lock, gated verified-or-refused by the lock probe (`observed` evidence) |
| **Tool-call governance** | Not yet | No policy surface until the tools-pod chokepoint lands (deferred design; #75 clean break) |
| **File-access governance** | Not yet | No policy surface until the arena workspace lands (deferred design; #75 clean break) |
| **Runtime evidence loop** | Yes | [reporter](../../internal/reporter/) merges `policyDecisions`/`violations`/`usage`/`events` from the egress-reporter |
| Human approval gate | Yes | `ApprovalPolicy` → `AwaitingApproval` → grant/deny; per-tool runtime holds; authenticated-approver webhook (opt-in) |
| Observability & audit | Yes | Prometheus metrics, OTel traces, OTLP audit sink |
| `status.usage` / `status.violations` / `status.events` | Yes (runtime) | Populated from egress-reporter reports — see [Status fields](agentsession-crd.md#status-fields) |
| `status.artifacts` | Yes | Logs (ConfigMap) + workspace tar (Secret) when `spec.outputs` enabled |
| Pod watch · `Ready` condition · finalizer cleanup · cancellation | Yes | See controller reference above |

Live task state & roadmap: [GitHub Issues](https://github.com/grantbarry29/scrutineer/issues). Durable context: [`docs/design/`](../design/).
