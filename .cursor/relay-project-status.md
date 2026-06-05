# Relay Project Status

> **What Relay has shipped, what is in progress, and where it is headed.**
> **Last updated:** 2026-06-05 (Phase 2 slice: AgentPolicy + policyRefs)
>
> For **how agents should implement tasks** (scope rules, templates, scans, updating this file), see [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

The **roadmap** below is long-term product intent, not a single backlog. **Ready for Cursor Queue** lists the next small implementation slices.

---

## Ready for Cursor Queue

Pick **one task card** per session unless the user asks for a design plan. Implementation rules: [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

_(Queue empty — promote a task from **Discovered Follow-Up Tasks** or Phase 1 roadmap when ready.)_

**Recently completed** (do not re-implement unless regressions): **Phase 2 slice** (`AgentPolicy` CRD, `spec.policyRefs`, merge + `status.effectivePolicy` / `status.matchedPolicies`, `AGENT_POLICY_MODE` env, `PolicyResolved` condition), rules compliance fixes, **validate sample manifests**, e2e TimedOut, finalizers, CI, session cancellation, envtest suite.

**Next suggested queue picks:** Policy decision records · watch `AgentPolicy` for session re-reconcile · ToolPolicy CRD · README policy-ref docs.

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

### Task: Define reference scoping rules for external refs

**Why it matters:**  
`promptConfigMapRef` only loads ConfigMaps in the same namespace; policy, credentials, and templates will need documented scoping before cross-namespace support.

**Scope:**
- Document same-namespace requirement for `promptConfigMapRef` in API comments and README.
- Add a short design note in this file for future refs (AgentPolicy, CredentialProfile, SessionTemplate): same-namespace default, optional explicit namespace field later.

**Non-goals:**
- Do not implement cross-namespace ConfigMap reads.
- Do not add new CRDs.

**Acceptance criteria:**
- API/kubebuilder comments and README state current scoping rules.
- Status file records the intended future pattern for namespaced refs.

**Expected files:**
- `api/v1alpha1/agentsession_types.go`
- `README.md`
- `.cursor/relay-project-status.md`

**Verification command:**  
`make manifests && make test`

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
`kubernetes-controller.mdc` expects a `Ready` condition summarizing whether the session can proceed; today only `Validated`, `RuntimeCreated`, and `Completed` exist.

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

### Task: Watch AgentPolicy changes for session re-reconcile

**Why it matters:**  
Sessions only pick up `AgentPolicy` updates on their own reconcile (e.g. `RequeueAfter`). Watching referenced policies lets platform teams roll out baseline changes without waiting.

**Scope:**
- Register a controller-runtime watch on `AgentPolicy` (or index policy refs → sessions).
- Map policy create/update/delete to reconcile requests for AgentSessions listing that ref in `spec.policyRefs`.
- Envtest: update AgentPolicy → session `status.effectivePolicy` / Job env reflects change.

**Non-goals:**
- Do not implement ToolPolicy watch until ToolPolicy CRD exists.
- Do not add cross-namespace ref resolution.

**Acceptance criteria:**
- Changing a referenced `AgentPolicy` triggers affected AgentSession reconcile.
- Envtest or integration test demonstrates updated `status.effectivePolicy`.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `config/rbac/role.yaml` (generated, only if markers change)

**Verification command:**  
`make test`

### Task: Document policyRefs and merge semantics in README

**Why it matters:**  
Phase 2 introduced `AgentPolicy`, `spec.policyRefs`, and merge rules; operators need README guidance beyond the status file.

**Scope:**
- Document `AgentPolicy` + `policyRefs` usage with the existing samples.
- Explain merge order (refs in order → inline overrides), mode strictest-wins, and same-namespace scoping.
- Note `AGENT_POLICY_MODE` and that modes are declared only until Phase 3 enforcement.

**Non-goals:**
- Do not document ToolPolicy or enforcement backends as shipped.

**Acceptance criteria:**
- README section matches **Policy merge semantics** in this file.
- Sample manifests cross-linked.

**Expected files:**
- `README.md`
- `.cursor/relay-project-status.md` (only if aligning wording)

**Verification command:**  
`make verify-samples` (docs-only)

### Task: Policy decision records in AgentSession status

**Why it matters:**  
Phase 2 records merged policy but not structured allow/deny decisions; audit and future UI need a bounded decision log.

**Scope:**
- Add `status.policyDecisions[]` (or extend existing types) with timestamp, type, target, allow/deny, reason, matched policy ref.
- Populate merge-time decisions in Phase 2 (e.g. effective deny list summary); reserve runtime decisions for Phase 3.
- Keep list bounded (cap entries per session).

**Non-goals:**
- Do not implement enforcement-side decision streaming yet.
- Do not build UI.

**Acceptance criteria:**
- API types and CRD schema include decision records.
- Controller writes at least one merge-time decision when policy refs resolve.
- Envtest asserts presence on policy-ref session.

**Expected files:**
- `api/v1alpha1/agentsession_types.go`
- `internal/policy/` or `internal/controller/policy.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make manifests && make test`

### Task: ToolPolicy CRD and policyRefs kind support

**Why it matters:**  
Network/tool rules are split across AgentPolicy today; ToolPolicy enables MCP-specific constraints and a dedicated merge layer.

**Scope:**
- Add `ToolPolicy` CRD (allowlists, rate limits, argument constraints — narrow MVP fields).
- Extend `spec.policyRefs` resolution for `kind: ToolPolicy`.
- Document merge order: AgentPolicy → ToolPolicy → inline.

**Non-goals:**
- Do not implement tool gateway enforcement (Phase 3).
- Do not add RuntimeProfile in the same task.

**Acceptance criteria:**
- `make verify-samples` includes ToolPolicy + referencing session sample.
- Envtest merge test covers AgentPolicy + ToolPolicy + inline.

**Expected files:**
- `api/v1alpha1/toolpolicy_types.go`
- `internal/policy/resolve.go`
- `config/samples/`, `config/crd/kustomization.yaml`

**Verification command:**  
`make manifests && make test`

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
| **AgentPolicy CRD** | Done (slice) | Reusable rules + `mode`; referenced from AgentSession |
| **Controller (kubernetes-job)** | Done | Reconciles to `batch/v1` Job, lifecycle phases, conditions, events |
| **Policy propagation** | Done | Inline policy → env vars in agent container |
| **Policy enforcement** | Not started | Env vars are hooks only; no network/tool/file gates |
| **Dev environment** | Done | Devcontainer + kind (`relay-dev`) + bootstrap scripts |
| **E2E tests** | Done | `make test-e2e` — 11 specs against live kind cluster |
| **Unit / envtest** | Done | Controller suite with validation + reconciler specs (~65% coverage) |
| **CI** | Done | `.github/workflows/test.yaml`, `e2e.yaml`, `lint.yaml` |
| **In-cluster deploy** | Ready | `make dev-deploy` builds image + deploys manager |
| **Additional CRDs** | In progress | `AgentPolicy` done; ToolPolicy, ApprovalPolicy, RuntimeProfile not started |
| **Operational UI** | Not started | Vision documented in product rule |
| **Audit / observability backend** | Not started | Status fields exist; not populated by sidecars yet |

### What works today

- Create `AgentSession` → controller validates → creates owned Job → tracks `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied` / `Cancelled`
- CRD admission rejects invalid `temperature` (string + Pattern)
- Controller validation denies bad specs (empty task, empty model fields, invalid workspace size) without creating a Job
- Foreign Job name collision → `PhaseDenied` with `JobConflict` (no adoption of unowned Jobs)
- `task.promptConfigMapRef` loads prompt from ConfigMap into `AGENT_TASK_PROMPT`
- `AgentPolicy` CRD + `spec.policyRefs` — merge referenced policies with inline overrides → `status.effectivePolicy`, `status.matchedPolicies`, `AGENT_POLICY_MODE` env
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
| `spec.policyRefs` / `AgentPolicy` | Yes | Done — same-namespace refs; merge order refs → inline; missing ref → `InvalidPolicy` |
| `status.effectivePolicy` / `matchedPolicies` | Yes | Done — populated on reconcile |
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

Documented in `internal/policy/`:

- `spec.policyRefs` resolved in order (same namespace; `AgentPolicy` only in MVP).
- List fields unioned across layers; numeric caps take the minimum (strictest).
- `spec.policy` inline overrides merged last.
- Effective `mode` = strictest across matched policies (`enforced` > `dry-run` > `audit-only`).
- Propagated to Job via existing `AGENT_POLICY_*` env vars + `AGENT_POLICY_MODE`.

### Recent fixes

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
- [ ] **Reference scoping documentation** — Same-namespace rules for ConfigMap/policy/credential refs
- [x] **E2e TimedOut path** — `timeoutSeconds` + sleep; assert `PhaseTimedOut` / `JobTimedOut`

---

### Phase 2 — Reusable policy model

Extract inline policy into composable, versioned CRDs without breaking AgentSession.

- [x] **AgentPolicy CRD** — Reusable network/tool/approval rules; `spec.policyRefs` on AgentSession
- [x] **Policy composition** — Merge refs in order → inline overrides; `status.matchedPolicies` + `status.effectivePolicy`
- [x] **Policy modes** — `audit-only` / `dry-run` / `enforced` on AgentPolicy; strictest mode in status + `AGENT_POLICY_MODE` env (declared only until Phase 3)
- [ ] **Policy decision records** — Structured status entries: who/what/when/allow/deny/reason
- [ ] **ToolPolicy CRD** — Tool/MCP allowlists, rate limits, argument constraints
- [ ] **RuntimeProfile CRD** — Stricter security contexts, sandbox selection, sidecar profiles

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

## Repository Audit (2026-05-17)

One-time scan performed while tightening Cursor rules. **No product code changed.**

| Area | Finding | Tracking |
|------|---------|----------|
| Cancellation | Complete (API, controller, e2e, README, sample) | Done |
| Finalizers | Implemented — Job cleanup on delete | Done |
| CI | `test.yaml`, `e2e.yaml`, `lint.yaml` | Done (image publish not in CI) |
| Terminal + missing Job | Fixed — terminal guard in reconciler | Done |
| E2e | 11 specs incl. cancellation + TimedOut | Done |
| Envtest cancel | Job delete, idempotent missing Job, `PhaseCancelled`/condition/event | Done in controller tests |
| RBAC | Matches current controller; audit not documented | Discovered Follow-Up Tasks |
| Samples / README | `make verify-samples`; all `relay_*.yaml` dry-run clean | Done |
| Enforcement / UI / extra CRDs | Not implemented (expected) | Roadmap Phases 2–7 |
