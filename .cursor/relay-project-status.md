# Relay Project Status

> **What Relay has shipped, what is in progress, and where it is headed.**
> **Last updated:** 2026-06-03 (Phase 2 work-item audit)
>
> For **how agents should implement tasks** (scope rules, templates, scans, updating this file), see [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

The **roadmap** below is long-term product intent, not a single backlog. **Ready for Cursor Queue** lists the next small implementation slices.

---

## Ready for Cursor Queue

Pick **one task card** per session unless the user asks for a design plan. Implementation rules: [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

_(Queue empty — promote a task from **Discovered Follow-Up Tasks** or Phase 1 roadmap when ready.)_

**Recently completed** (do not re-implement unless regressions): **README policy docs**, **ToolPolicy CRD**, **Job env sync** (`PolicyPropagated` / pending Job replace / `PolicyEnvDrift`), policy decision records, AgentPolicy watch, Phase 2 policy slice, verify-samples, e2e, finalizers, CI, cancellation.

**Next suggested queue picks:** Watch owned Pods · RuntimeProfile CRD · Document Kubernetes Events · Add Ready condition.

---

## Discovered Follow-Up Tasks

**Purpose:** Permanent backlog for work noticed but not in the current task scope. Agents **must** add a task card here (or a roadmap bullet) **in the same session** when they discover out-of-scope work — chat summaries and “suggested next picks” alone are not enough; untracked items become project holes.

Scoped tasks found by repository audit or implementation work. **Not in the active queue** until promoted. Pick one at a time into **Ready for Cursor Queue** when appropriate.

### Task: Watch owned Pods for reconcile triggers

**Why it matters:**  
`status.podName` and Running transitions currently depend on Job reconcile and `RequeueAfter`; watching Pods reduces latency and unnecessary requeues.

**Scope:**
- Register a controller-runtime watch on Pods owned by session Jobs (label or owner reference filter).
- Map Pod events to AgentSession reconcile requests.
- Keep RBAC markers aligned (`pods` get/list/watch already granted).

**Non-goals:**
- Do not implement log/artifact collection.
- Do not add a new CRD or UI.

**Acceptance criteria:**
- Pod creation/update triggers AgentSession reconcile without relying solely on `RequeueAfter`.
- Envtest or integration test demonstrates earlier `status.podName` population where practical.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `config/rbac/role.yaml` (generated, only if markers change)

**Verification command:**  
`make test`

### Task: Document future-only status fields

**Why it matters:**  
`status.usage`, `status.violations`, and `status.artifacts` exist in the API but are not populated; operators should not expect them in MVP.

**Scope:**
- Add kubebuilder/API comments marking fields as reserved for future phases.
- Add a README table: field → populated? → which phase owns it.

**Non-goals:**
- Do not implement sidecars, enforcement, or artifact collection.

**Acceptance criteria:**
- CRD OpenAPI descriptions state MVP population status.
- README lists future-only status fields explicitly.

**Expected files:**
- `api/v1alpha1/agentsession_types.go`
- `config/crd/bases/relay.secureai.dev_agentsessions.yaml` (generated)
- `README.md`

**Verification command:**  
`make manifests && make test`

### Task: Document Kubernetes Events emitted by the controller

**Why it matters:**  
Events are the primary MVP observability surface; operators need a stable catalog before Phase 4 structured events.

**Scope:**
- Document `EventReason*` constants and when each fires (validation, Job create, running, success, failure, denial, cancellation once added).
- Cross-link to README “inspect events” section.

**Non-goals:**
- Do not add OTLP, audit sinks, or UI.
- Do not change event text unless incorrect.

**Acceptance criteria:**
- README (or `docs/`) lists all current event reasons and types (Normal/Warning).

**Expected files:**
- `README.md`
- `internal/controller/constants.go` (inline comments on constants — partial done 2026-06-04)

**Verification command:**  
`make test` (no behavior change; docs-only)

### Task: Add AgentSession Ready condition

**Why it matters:**  
`kubernetes-controller.mdc` expects a `Ready` condition summarizing whether the session can proceed; today `Validated`, `PolicyResolved`, `PolicyPropagated`, `RuntimeCreated`, and `Completed` exist but no aggregate `Ready`.

**Scope:**
- Define `Ready` condition semantics (e.g. True when Job running or terminal success path; False when denied/validation failed).
- Set/update in reconciler alongside existing conditions.
- Envtest assertions on happy path and denial.

**Non-goals:**
- Do not add approval blocking or enforcement.
- Do not add new CRDs.

**Acceptance criteria:**
- `status.conditions` includes `Ready` with documented meaning in API comments.
- Envtest covers at least Running and Denied.

**Expected files:**
- `api/v1alpha1/agentsession_types.go` (comments)
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: ToolPolicy MCP argument constraints (schema design)

**Why it matters:**  
Phase 2 roadmap mentioned argument-level MCP governance; initial `ToolPolicy` slice ships allow/deny lists and caps only.

**Scope:**
- Design API fields for per-tool argument allow/deny patterns (or defer explicitly to Phase 3 tool gateway).
- Document non-goals until enforcement exists.

**Non-goals:**
- Do not implement tool gateway enforcement in this task.
- Do not break existing `ToolPolicy` samples.

**Acceptance criteria:**
- Either CRD fields + merge semantics defined, or explicit deferral recorded in README and this file.

**Expected files:**
- `api/v1alpha1/toolpolicy_types.go` (if implementing schema)
- `README.md`, `.cursor/relay-project-status.md`

**Verification command:**  
`make manifests && make test`

### Task: Propagate ToolPolicy maxCallsPerMinute to runtime hooks

**Why it matters:**  
`ToolPolicy.spec.maxCallsPerMinute` is stored in the CRD but not merged into `PolicyRules` or env vars; operators may assume it is active.

**Scope:**
- Decide propagation path (env var and/or `status.effectivePolicy` extension).
- Document until enforced in Phase 3.

**Non-goals:**
- Do not implement rate limiting enforcement.

**Acceptance criteria:**
- Field is visible in effective policy or documented as schema-only until Phase 3.

**Expected files:**
- `api/v1alpha1/toolpolicy_types.go`, `internal/policy/`, `README.md`

**Verification command:**  
`make test`

### Task: Append runtime policy decisions from enforcement backends

**Why it matters:**  
`status.policyDecisions` is merge-time only today; Phase 3 sidecars/gateways need to append `phase: runtime` entries without wiping merge decisions.

**Scope:**
- Define append/merge strategy for runtime decisions (cap total list, preserve merge summary).
- Extension point for enforcement backends to report allow/deny/dry-run at request time.

**Non-goals:**
- Do not implement Envoy/tool gateway in this task.

**Acceptance criteria:**
- Documented contract for runtime decision producers.
- Status update path supports bounded append.

**Expected files:**
- `api/v1alpha1/policy_types.go`
- `internal/controller/` or enforcement adapter stub

**Verification command:**  
`make test`

### Task: Audit controller RBAC for least privilege

**Why it matters:**  
RBAC must match kubebuilder markers and actual client calls (Jobs delete, ConfigMaps get, Events create, status update).

**Scope:**
- Compare `+kubebuilder:rbac` markers in `agentsession_controller.go` to `config/rbac/role.yaml`.
- Remove unused verbs/resources; add missing ones.
- Note any permissions reserved for future controllers.

**Non-goals:**
- Do not add RBAC for unimplemented CRDs.
- Do not deploy OPA or admission policies.

**Acceptance criteria:**
- `make manifests` regenerates role YAML consistent with markers.
- Short permission matrix in README or this file.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `config/rbac/role.yaml` (generated)

**Verification command:**  
`make manifests && make test`

### Task: Update README current-state section

**Why it matters:**  
README mixes vision and MVP reality; `cancelRequested`, declared-vs-enforced policy, and unimplemented `outputs` should be obvious.

**Scope:**
- Add/update a “Current MVP behavior” section aligned with **What works today** and **Known gaps** here.
- Document `spec.cancelRequested` once cancellation status/events are done.
- Clarify env vars are propagation hooks, not enforcement.

**Non-goals:**
- Do not document unimplemented features as shipped.
- Do not add UI or enforcement guides.

**Acceptance criteria:**
- README accurately reflects controller behavior and explicit non-goals.
- Cancellation and policy sections match the status file.

**Expected files:**
- `README.md`
- `.cursor/relay-project-status.md`

**Verification command:**  
`make test` (docs-only)

### Task: Pin dev tool versions in README

**Why it matters:**  
`Makefile` pins `controller-gen` and `setup-envtest`; README should match to avoid Go/envtest version skew.

**Scope:**
- Document pinned `controller-gen`, `setup-envtest`, and kindest node image versions from Makefile / devcontainer.
- Mention `ENVTEST_K8S_VERSION` expectation.

**Non-goals:**
- Do not upgrade tool versions unless broken.

**Acceptance criteria:**
- README dev-setup section lists the same versions as Makefile/devcontainer.

**Expected files:**
- `README.md`

**Verification command:**  
`make test` (docs-only)

## Current Operational State

Relay is in **early MVP / vertical-slice** stage. The core control-plane loop works end-to-end on a local kind cluster, but most governance is **declared and propagated**, not **enforced**.

| Area | State | Notes |
|------|-------|-------|
| **AgentSession CRD** | Done | `relay.secureai.dev/v1alpha1`, spec/status + `policyRefs` |
| **AgentPolicy CRD** | Done | Reusable rules + `mode`; `spec.policyRefs`; watch → re-reconcile |
| **ToolPolicy CRD** | Done | Tool/MCP rules; merge + watch; `maxCallsPerMinute` schema-only until follow-up |
| **Controller (kubernetes-job)** | Done | Reconciles to `batch/v1` Job, lifecycle phases, conditions, events |
| **Policy propagation** | Done | Merge `policyRefs` + inline → `status.effectivePolicy` → `AGENT_POLICY_*` env |
| **Policy enforcement** | Not started | Env vars are hooks only; no network/tool/file gates |
| **Dev environment** | Done | Devcontainer + kind (`relay-dev`) + bootstrap scripts |
| **E2E tests** | Done | `make test-e2e` — 11 specs against live kind cluster |
| **Unit / envtest** | Done | Controller suite with validation + reconciler specs (~65% coverage) |
| **CI** | Done | `.github/workflows/test.yaml`, `e2e.yaml`, `lint.yaml` |
| **In-cluster deploy** | Ready | `make dev-deploy` builds image + deploys manager |
| **Additional CRDs (Phase 2)** | Nearly complete | `AgentPolicy` + `ToolPolicy` done; **RuntimeProfile** remains on Phase 2 roadmap |
| **Additional CRDs (later)** | Not started | ApprovalPolicy, CredentialProfile, SessionTemplate, ToolGateway |
| **Operational UI** | Not started | Vision documented in product rule |
| **Audit / observability backend** | Not started | Status fields exist; not populated by sidecars yet |

### What works today

- Create `AgentSession` → controller validates → creates owned Job → tracks `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied` / `Cancelled`
- CRD admission rejects invalid `temperature` (string + Pattern)
- Controller validation denies bad specs (empty task, empty model fields, invalid workspace size) without creating a Job
- Foreign Job name collision → `PhaseDenied` with `JobConflict` (no adoption of unowned Jobs)
- `task.promptConfigMapRef` loads prompt from ConfigMap into `AGENT_TASK_PROMPT`
- `AgentPolicy` + `ToolPolicy` CRDs + `spec.policyRefs` — merge referenced policies with inline overrides → `status.effectivePolicy`, `status.matchedPolicies`, `AGENT_POLICY_MODE` env
- Policy CRD watches — `AgentPolicy` / `ToolPolicy` update/delete re-reconciles affected AgentSessions (same namespace)
- Job env sync — pending Job replaced on policy drift; active Job → `PolicyPropagated=False` / `PolicyEnvDrift` warning
- `status.policyDecisions` — merge-time audit entries (mode, matched policies, allow/deny lists, caps); max 64 per session
- Policy fields injected as `AGENT_POLICY_*` / `RELAY_*` env vars (from effective merged policy)
- Workspace emptyDir mount, resource limits, timeout, basic container hardening
- Kubernetes Events on validation, Job create, running, success, failure, cancellation
- `spec.cancelRequested: true` deletes the owned Job and reaches terminal `PhaseCancelled` with `Completed` condition
- `status.podName` set to the newest Pod owned by the session's Job (when a Pod exists)
- Sample manifests (success + failing) and README documentation

### Known gaps (MVP vs schema)

| Capability | In API/schema | Implemented in controller |
|------------|---------------|---------------------------|
| `task.promptConfigMapRef` | Yes | Done — loads key from same-namespace ConfigMap |
| `status.usage` | Yes | No — reserved for future sidecar/audit |
| `status.podName` | Yes | Done — labeled session Pods, current Job UID, newest `CreationTimestamp` (name tie-break); see `internal/controller/pod.go` |
| `status.violations` | Yes | No — no enforcement backend yet |
| `status.artifacts` | Yes | No — `outputs.collectArtifacts` not implemented |
| `spec.policyRefs` / `AgentPolicy` / `ToolPolicy` | Yes | Done — same-namespace refs; merge order refs → inline; missing ref → `InvalidPolicy` |
| `PolicyPropagated` / Job env sync | Yes | Pending Job replaced on policy drift; active Job → `PolicyEnvDrift` condition + warning event |
| `status.effectivePolicy` / `matchedPolicies` | Yes | Done — populated on reconcile |
| `status.policyDecisions` | Yes | Done — merge-time only (`phase: merge`); replaced each reconcile; capped at 64 |
| `policy.requireHumanApproval` | Yes | Warning event `ApprovalNotEnforced` on effective policy; does not block execution |
| `spec.cancelRequested` | Yes | Done — deletes Job; sets `PhaseCancelled`, condition, event |
| `PhaseCancelled` | Yes | Done — terminal via cancel reconcile path |
| Terminal session + missing Job | — | Done — terminal phases skip `ensureJob`; `syncStatusFromJob` does not regress phase |
| AgentSession delete | — | Done — finalizer blocks delete; owned Job removed; finalizer cleared |
| Orchestrators beyond `kubernetes-job` | Enum reserved | Rejected at validation |
| PVC-backed workspace | Commented future | emptyDir only |
| Webhook validation | Generated scaffold | Not wired |

### status.podName selection semantics

Documented in `internal/controller/pod.go` and API comments on `status.podName`:

- List Pods in the session namespace with `relay.secureai.dev/session=<session.name>`.
- Keep only Pods whose `ownerReference` matches the **current** Job UID (`Kind=Job`).
- Select the newest by `CreationTimestamp`; ties break on lexicographic Pod name.
- Empty when no match yet. Stale Pods from a replaced Job (new UID) are ignored.

### Rules compliance audit (2026-06-04)

Scan against `.cursor/rules/` (product vision, workflow, `kubernetes-controller`, `crd-api-design`, `distributed-systems-networking`). **Fixed in code:**

| Finding | Fix |
|---------|-----|
| `cmd/main.go` missing uncached `APIReader` | Wired `mgr.GetAPIReader()` for deletion/finalizer paths |
| `ensureJob` adopted foreign Jobs by name | `metav1.IsControlledBy` → `PhaseDenied` / `JobConflict` |
| `syncStatusFromJob` missed `FailureTarget` before `Failed>0` | Dedicated `jobTimedOut` case → `PhaseTimedOut` |
| Empty `model.provider` / `model.name` | Controller validation + CRD `MinLength=1` |
| Invalid `workspace.size` silently ignored | `validateSpec` rejects bad quantities |
| `requireHumanApproval` invisible | Warning event `ApprovalNotEnforced` |
| Event reason catalog | Comments on `EventReason*` in `constants.go` |

**Queued (not implemented — promote when ready):**

| Finding | Suggested task |
|---------|----------------|
| No `Ready` condition on AgentSession | New queue card: align with controller-rules pattern |
| Pod watch for faster `podName` / Running | **Watch owned Pods** (discovered) |
| Task one-of only in controller | Optional CRD CEL; controller path sufficient for MVP |
| `PhaseValidating` unused | Defer or wire on first reconcile |
| README reconciler diagram / events table | **Document Events** + **README current-state** |
| RBAC permission matrix | **Audit controller RBAC** |

Cursor rules in `.cursor/rules/`: `relay-product-vision.mdc`, `relay-project-status.mdc` (always apply); `kubernetes-controller.mdc`, `crd-api-design.mdc`, `distributed-systems-networking.mdc` (path-scoped).

### Policy merge semantics (Phase 2)

Documented in `internal/policy/`, `README.md`, and API comments:

- `spec.policyRefs` resolved in **declaration order** (same namespace; kinds: `AgentPolicy`, `ToolPolicy`).
- Recommended order: AgentPolicy entries → ToolPolicy → `spec.policy` inline overrides.
- List fields unioned across layers; numeric caps take the minimum (strictest).
- `spec.policy` inline overrides merged last.
- Effective `mode` = strictest across matched policies (`enforced` > `dry-run` > `audit-only`).
- Propagated to Job via `AGENT_POLICY_*` env vars + `AGENT_POLICY_MODE`.
- Policy CRD updates watched → affected sessions re-reconcile; pending Jobs replaced on env drift.

### External reference scoping

| Ref | MVP behavior | Future pattern |
|-----|--------------|----------------|
| `promptConfigMapRef` | Same namespace as `AgentSession` | Optional explicit `namespace` field |
| `policyRefs` (`AgentPolicy`, `ToolPolicy`) | Same namespace | Optional `namespace` on `PolicyRef` |
| `CredentialProfile` / `SessionTemplate` (planned) | — | Same-namespace default; explicit namespace when added |

Cross-namespace reads are **not** implemented in MVP.

### Policy decision records (Phase 2)

`status.policyDecisions` — bounded audit log (`MaxItems: 64`), rewritten on each reconcile:

| Field | Purpose |
|-------|---------|
| `time`, `phase` (`merge`) | When / control-plane vs runtime (runtime = Phase 3) |
| `type` | `mode`, `policy`, `network`, `tool`, `approval`, `cap`, `summary` |
| `action` | `allow`, `deny`, `audit`, `dry-run` (restrictive rules follow effective mode) |
| `actor` | `relay-controller` for merge-time |
| `target`, `rule`, `reason`, `message` | What was evaluated and why |
| `policyRef` | Set on matched `AgentPolicy` / `ToolPolicy` entries |

Built in `internal/policy/decisions.go` via `BuildMergeDecisions`.

### Recent fixes

- **README policy docs** — `AgentPolicy`/`ToolPolicy`, merge semantics, scoping, policy change / Job env behavior, MVP table
- **ToolPolicy CRD** — `toolpolicy_types.go`, merge via `LoadPolicyLayers`, watch, samples, envtest
- **Job env sync** — `PolicyPropagated` condition; replace pending Job on drift; `PolicyEnvDrift` when Job active (`job_policy.go`)
- **Policy decision records** — `PolicyDecision` API type, merge-time population, unit + envtest coverage
- **AgentPolicy watch** — `Watches(AgentPolicy)` maps to sessions with matching `spec.policyRefs`; envtest verifies `status.effectivePolicy` updates on policy change (`internal/controller/policy_watch.go`)
- **Phase 2 reusable policy (slice)** — `AgentPolicy` CRD, `PolicyRules` shared type, `policyRefs`, `internal/policy` merge/resolve, `PolicyResolved` condition, samples, envtest (38 specs)
- **Rules compliance audit** — Job ownership denial (`JobConflict`), main `APIReader`, model/workspace validation, TimedOut sync without `Failed>0`, `ApprovalNotEnforced` warning event, terminal `Denied` preserves validation reason; envtest coverage (36 specs)
- **validate sample manifests** — `make verify-samples` (server dry-run on `config/samples/relay_*.yaml`); prompt CM sample in kustomization; README sample list
- **e2e TimedOut** — short `timeoutSeconds` + long sleep; `PhaseTimedOut` and `JobTimedOut` condition; `jobTimedOut` recognizes `FailureTarget`/`DeadlineExceeded` on Kubernetes 1.31+
- **status.podName selection semantics** — documented retry/recreate behavior; deterministic name tie-break; unit tests for stale Job UID and equal timestamps
- **AgentSession finalizers** — `AgentSessionFinalizer` attached on reconcile; `handleDeletion` deletes owned Job (clears `blockOwnerDeletion` when needed), removes finalizer; uncached `APIReader` for delete detection; envtest delete-path specs
- **GitHub Actions CI** — `.github/workflows/test.yaml` (`make test`), `e2e.yaml` (kind + `make test-e2e`), `lint.yaml` (`make fmt` + `make vet`)
- **Terminal phase stability** — terminal sessions do not get a replacement Job; `syncStatusFromJob` preserves terminal phase; envtest coverage
- **Cancellation docs** — README cancel section, MVP table, `relay_v1alpha1_agentsession_cancel.yaml` sample
- **Cancellation e2e** — cancel running session → Job deleted + `PhaseCancelled`; cancel at create → no Job
- **Session cancellation (status/events)** — `applyCancellationStatus`: `PhaseCancelled`, `Completed`/`SessionCancelled`, result outcome `cancelled`, `SessionCancelled` event; envtest coverage
- **Session cancellation (controller)** — `spec.cancelRequested` deletes owned Job via `stopRuntimeJob`; envtest for delete + idempotent missing Job
- **`spec.cancelRequested`** — declarative cancellation request on `AgentSessionSpec`; CRD default `false`
- **`status.podName`** — select newest Pod owned by the Job; list errors fail reconcile; envtest + e2e coverage on success/failure paths
- **Envtest controller tests** — validation, denial, Job create, succeeded transition, promptConfigMapRef
- **PromptConfigMapRef** — `resolveTask` loads prompt; missing CM/key → `PhaseDenied`
- **Status patch strategy** — `patchStatus` unions conditions from reconcile snapshot + live object before update; avoids JSON merge patch array replacement on CRDs
- **RuntimeCreated condition race** — re-assert condition on every `ensureJob` to survive stale-cache JSON-merge-patch overwrites (found by e2e happy-path test)
- **Model temperature** — `*string` with CRD Pattern instead of `float64` / `allowDangerousTypes`
- **Devcontainer** — Docker-outside-of-Docker + resilient `kind-up.sh`

---

## Roadmap

Status key: `[ ]` not started · `[~]` in progress · `[x]` done · `[-]` deferred

Phases are ordered by product maturity. **Implement incrementally** — decompose per [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md), not as a single effort.

---

### Phase 0 — MVP foundation

- [x] AgentSession CRD + kubebuilder scaffold
- [x] Reconcile to Kubernetes Job with owner references
- [x] Lifecycle phases, conditions (`Validated`, `RuntimeCreated`, `Completed`), events
- [x] Inline policy spec + env var propagation
- [x] Workspace emptyDir, resources, timeout, security context baseline
- [x] Sample manifests + README
- [x] Devcontainer + kind local cluster
- [x] E2E test suite (`make test-e2e`)

---

### Phase 1 — MVP hardening

Complete the vertical slice so the API and controller behavior match, and the project is safe to extend.

- [x] **Envtest controller tests** — Reconciler unit tests in `internal/controller/` (validation, Job create, status transitions, condition stability)
- [x] **PromptConfigMapRef** — Load prompt from ConfigMap in reconciler; validate ref exists
- [x] **Status patch strategy** — Live read + condition union + `Status().Update` (CRDs do not support strategic merge patch on status)
- [x] **Populate `status.podName` reliably** — Newest Job-owned Pod by creation timestamp; envtest + e2e coverage
- [x] **Session cancellation** — API, Job delete, `PhaseCancelled`, events, e2e, README + sample
- [x] **Finalizers** — `relay.secureai.dev/finalizer`; owned Job cleanup on delete; envtest coverage
- [x] **CI pipeline** — GitHub Actions: `make test`, `make test-e2e` (kind), lint (`test`/`e2e`/`lint` workflows); image build/publish not yet in CI
- [ ] **Admission webhook** (optional) — Move duplicate validation to validating webhook for earlier rejection
- [ ] **Helm chart or improved kustomize overlays** — Easier install than raw kustomize for early adopters
- [x] **Terminal phase stability** — Terminal phases skip Job creation; `syncStatusFromJob` does not regress phase; envtest
- [x] **Reference scoping documentation** — Same-namespace rules for ConfigMap/policy refs in README + API comments
- [x] **E2e TimedOut path** — `timeoutSeconds` + sleep; assert `PhaseTimedOut` / `JobTimedOut`

---

### Phase 2 — Reusable policy model

Extract inline policy into composable, versioned CRDs without breaking AgentSession.

- [x] **AgentPolicy CRD** — Reusable network/tool/approval rules; `spec.policyRefs` on AgentSession
- [x] **Policy composition** — Merge refs in order → inline overrides; `status.matchedPolicies` + `status.effectivePolicy`
- [x] **Policy modes** — `audit-only` / `dry-run` / `enforced`; strictest mode in status + `AGENT_POLICY_MODE` env (declared only until Phase 3)
- [x] **Policy decision records** — `status.policyDecisions[]` merge-time entries; max 64; runtime append = Phase 3/4
- [x] **ToolPolicy CRD** — Tool/MCP allowlists + caps; `policyRefs` + watch + samples + README
- [x] **Policy watches** — `AgentPolicy` + `ToolPolicy` changes re-reconcile referencing sessions
- [x] **Job env sync (partial)** — Replace pending Job on policy drift; `PolicyPropagated` / `PolicyEnvDrift` when Job active
- [x] **Operator docs** — README policy section, reference scoping, samples (`make verify-samples`)
- [ ] **RuntimeProfile CRD** — Stricter security contexts, sandbox selection, sidecar profiles (**last Phase 2 item**)

**Phase 2 deferred / follow-up (tracked, not blocking Phase 3 planning):**

| Item | Where tracked | Notes |
|------|---------------|-------|
| `ToolPolicy.maxCallsPerMinute` not in effective policy/env | Discovered: *Propagate ToolPolicy maxCallsPerMinute* | Schema exists; not merged yet |
| ToolPolicy MCP **argument constraints** | Discovered: *ToolPolicy MCP argument constraints* | Roadmap mentioned; out of initial ToolPolicy slice |
| Inline `spec.policy.mode` override | Not planned | Only CRD modes merge today |
| Runtime `policyDecisions` append | Discovered: *Append runtime policy decisions* | Phase 3 enforcement |
| Active Job env stale after policy change | `PolicyEnvDrift` condition | Documented; immutable Job template |
| Mode **enforcement** (audit/dry-run/enforced behavior) | Phase 3 roadmap | Declared + propagated only |

**Phase 2 is complete for control-plane policy** once `RuntimeProfile` ships (or is explicitly deferred to Phase 3). Everything else above is polish or Phase 3+.

---

### Phase 3 — Data-plane enforcement

Real governance beyond env var propagation. Start narrow, prove value, then expand.

- [ ] **Enforcement architecture** — Define control-plane vs data-plane interfaces (sidecar, gateway, eBPF agent contracts)
- [ ] **NetworkPolicy baseline** — Auto-generate namespace-scoped NetworkPolicy from session policy (CIDR/domain hints)
- [ ] **DNS / egress proxy** — FQDN allow/deny enforcement (Envoy or dedicated DNS proxy sidecar)
- [ ] **Envoy sidecar injection** — Optional per-session sidecar via RuntimeProfile; egress filter config from policy
- [ ] **Tool gateway integration** — Route tool/MCP calls through governed gateway; log + enforce
- [ ] **Violation reporting** — Populate `status.violations` from enforcement backends in real time
- [ ] **File/workspace policy** — Read/write path restrictions (volume mounts, seccomp, or FS proxy)

---

### Phase 4 — Observability and audit

Backend surfaces for the future operational UI and enterprise audit requirements.

- [ ] **Structured session events API** — Timestamped event stream beyond Kubernetes Events (tool call, network, policy decision)
- [ ] **Session timeline model** — Normalized events suitable for UI timeline view
- [ ] **Audit log sink** — Export to OTLP, S3, or SIEM-compatible format
- [ ] **Usage metrics** — Populate `status.usage` (tokens, tool calls, network requests) from sidecar/agent reports
- [ ] **OpenTelemetry** — Traces for reconcile loop + optional agent runtime traces
- [ ] **Prometheus metrics** — Sessions by phase, violations, approval queue depth, reconcile latency
- [ ] **Log / artifact collection** — Implement `outputs.collectLogs` / `collectArtifacts`

---

### Phase 5 — Human approval workflows

Scoped, auditable gates — not a boolean env var.

- [ ] **ApprovalPolicy CRD** — Define what actions require approval
- [ ] **ApprovalRequest CRD** — Per-action approval objects (tool, domain, file write, deploy, credential use)
- [ ] **Controller approval gate** — Block execution until approved; resume on approval
- [ ] **Approval audit trail** — Who approved, when, scope, expiry
- [ ] **Integration hooks** — Slack, PagerDuty, or generic webhook for approval notifications

---

### Phase 6 — Orchestrator adapters

Stay orchestrator-agnostic; add backends without coupling core reconciler to Jobs.

- [ ] **Orchestrator interface** — `CreateRuntime`, `GetStatus`, `Cancel` abstraction in controller
- [ ] **Tekton adapter** — `runtime.orchestrator: tekton`
- [ ] **Argo Workflows adapter**
- [ ] **Temporal adapter** (or external worker handshake)
- [ ] **SessionTemplate CRD** — Parameterized session blueprints for platform teams

---

### Phase 7 — Operational UI

Governance/observability dashboard — not a chatbot.

- [ ] **UI architecture** — SPA + backend API reading CRDs, events, audit store
- [ ] **Session list / detail** — Phase, Job, policy summary, conditions, violations
- [ ] **Session timeline view** — Tool, network, policy events chronologically
- [ ] **Live policy / network view** — Active connections, blocks, violations (requires Phase 3–4)
- [ ] **Tool governance view** — Allowed/denied tools, call history
- [ ] **Approval inbox** — Pending approvals with approve/deny actions
- [ ] **Runtime topology view** — Agent → gateway → sidecar → APIs graph
- [ ] **Audit / forensics** — Replay, traces, historical search

---

### Phase 8 — Enterprise platform

Multi-tenant, identity, credentials — production-grade control plane.

- [ ] **Per-session identity** — Dedicated ServiceAccount provisioning, RBAC scoping
- [ ] **CredentialProfile CRD** — Scoped secrets/KMS references; no broad secret mounts
- [ ] **Multi-tenancy** — Namespace isolation patterns, quota, policy boundaries
- [ ] **High availability** — Leader election (scaffold exists), multiple replicas, graceful shutdown
- [ ] **Multi-cluster** — Fleet-level policy and session visibility (future)
- [ ] **Secure sandboxes** — gVisor/Kata/Firecracker via RuntimeProfile

---
