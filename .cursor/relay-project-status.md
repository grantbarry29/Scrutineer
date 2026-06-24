# Relay Project Status

> **What Relay has shipped, what is in progress, and where it is headed.**
> **Last updated:** 2026-06-23 (per-tool runtime approval **impl slice 2** — reporter approval channel: `ApprovalHandler` serves `POST /v1/approvals` idempotent create-or-lookup keyed by `requestId` + `GET /v1/approvals/{id}?namespace=` poll, reuses TokenReview + pod→session ownership, creates runtime `ApprovalRequest` owner-ref'd to session, reports controller-observed state only, `argDigest`-only, fake-client tests green; per-tool runtime approval **impl slice 1** — controller runtime `ApprovalRequest` variant: `spec.trigger`/`requestId`/`scope.argDigest`, `reconcileRuntimeApprovals` resolves decision→state→audit per held tool call without gating session phase, nil-policy-safe expiry, approver-allowlist/`allOf`/`onTimeout` reused, envtest+unit tests; per-tool runtime approval **design** — `docs/design/phase-5-runtime-tool-approval.md`: cooperative mid-execution gate that holds a tool/MCP call for a scoped human grant, reusing `ApprovalRequest` + reporter approval channel, stamped `self-reported`; resolves approval-workflows open question #4; e2e probe distroless fix: `clusterImageRunnable` checks `node.status.images` instead of a `sh -c` probe pod, so live-evidence specs run against standard distroless images instead of skipping — verified live; tool argument constraints slice 4: live in-cluster e2e — enforced argument rule denies a tool call, redacted violation in status, verified on kind; tool argument constraints slice 3: tool-gateway per-call argument evaluation — path resolver + operator matchers, deny-precedence/allow-allowlist, redacted evidence, JSON propagation; tool argument constraints slice 2: `ToolArgumentRule`/`ArgumentConstraint` schema on `ToolPolicy`+`PolicyRules`, concatenate-merge, merge-time summary decision, sample + manifests; tool/MCP argument-constraints design doc; Phase 6 slice 2b: backend returns normalized `observation`, reconciler owns status mapping via `applyObservation`/`applyRuntimePhase`; Phase 6 slice 2: extracted `runtimeBackend` interface + registry + kubernetes-job backend, reconciler routes all runtime calls through it, behavior-preserving; end-of-task handoff protocol added to workflow rules; approval audit records carry controller assurance; Phase 6 orchestrator-interface design doc; assurance level in violation/runtime-report audit records; approval-decision audit records + at-most-once notify fix; Phase 5 slice 6: multi-approver allOf; approval_queue_depth counts pending ApprovalRequests; reconcile churn fix: idempotent resolution events; observability export design doc; Phase 5 slice 5: approver allowlist; evidence-integrity slice 2: agent SA automount off; `model.baseURL`; Phase 5 slice 4: approval notification hooks; slice 3: `ApprovalRequest` CRD + controller gate/resume; slice 2: `ApprovalPolicy` CRD; slice 1: approval design doc; evidence-integrity slice 1: `assuranceLevel`; 2026-06-16 audit pass — Phase 4 verified complete)
>
> For **how agents should implement tasks** (scope rules, templates, scans, updating this file), see [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

The **roadmap** below is long-term product intent, not a single backlog. **Ready for Cursor Queue** lists the next small implementation slices.

---

## Ready for Cursor Queue

Pick **one task card** per session unless the user asks for a design plan. Implementation rules: [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

> **Critical path:** Phase 3b **closed**. Phase 4 **closed** (observability + audit). **Phase 5 substantively done:** slices 1 (design doc) + 2 (`ApprovalPolicy` CRD) + 3 (`ApprovalRequest` CRD + controller gate/resume) + 4 (notification hooks) + 5 (approver allowlist) + 6 (multi-approver `allOf`) **done**. The approval gate is real, notified, and supports single- or multi-approver: a session matching an `ApprovalPolicy` blocks in `AwaitingApproval` until granted, approvers are webhook-notified, and `allOf` requires every listed approver. **No queue head selected** — pick next from *Discovered Follow-Up Tasks* or remaining Phase 5 polish (per-tool runtime approval **slices 1–2 done** — `docs/design/phase-5-runtime-tool-approval.md`, slices 3–4 queued: gateway hold-and-ask, live e2e; authenticated approver-identity via webhook).

**Runtime evidence loop — ordered sequence** (see *Discovered Follow-Up Tasks* for full cards):

1. ~~Runtime reporter mechanism design~~ — **done**
2. ~~Runtime reporter loop (impl)~~ — **done** (`internal/reporter/`)
3. ~~Structured session events API~~ — **done** (`status.events[]`, reporter `events[]` payload)
4. ~~Reporter pod wiring~~ — **done** (`relay-controller-reporter` Service, projected token, `RELAY_REPORTER_URL`)
5. ~~First-party dns-proxy image MVP~~ — **done** (`cmd/dns-proxy`, `Dockerfile.dns-proxy`, sidecar image ref)
6. ~~First-party tool-gateway image MVP~~ — **done** (`cmd/tool-gateway`, `Dockerfile.tool-gateway`, sidecar image ref)
7. ~~Live network violation population~~ — **done** (`test/e2e/network_violation_test.go`, in-cluster reporter for e2e)
8. ~~File/workspace policy implementation~~ — **done** (`PolicyRules` path fields, `workspace.EvaluateFile`, env propagation)

**Phase 4 observability** (roadmap): ~~usage metrics (control-plane)~~ → **execution plan below** → Prometheus → OTel → audit sink → log/artifact collection.

### Phase 4 execution plan (pick in order)

Agreed sequencing after usage-metrics ship (2026-06-10). Full cards in **Discovered Follow-Up Tasks** unless marked *(queue head)*.

| # | Task | Why this order |
|---|------|----------------|
| ~~**A**~~ | ~~**E2e usage metric assertions**~~ — **done** | Live `networkRequests` / `toolCalls` in violation e2e specs. |
| ~~**B**~~ | ~~**Session timeline model**~~ — **done** | `internal/observability` projection + design doc. |
| ~~**C**~~ | ~~**Usage-only report idempotency (`reportId` cache)**~~ — **done** | In-process seen-cache; 24h TTL. |
| ~~**D**~~ | ~~**FS gateway sidecar MVP**~~ — **done** | First-party image + sidecar injection + integration test. |
| ~~**E**~~ | ~~**File usage metrics**~~ — **done** | `SessionUsage.fileOperations` from `type: file` decisions. |
| ~~**F**~~ | ~~**Live file violation + usage e2e**~~ — **done** | `test/e2e/file_violation_test.go`; `kind-load-fs-gateway` in `test-e2e-images`. |

After A–F: ~~Prometheus exporter~~ **done** → ~~OTel~~ **done** → ~~audit sink~~ **done** → ~~log/artifact collection~~ **done**.

---

### Task: E2e usage metric assertions — Phase 4 · slice A — **done (2026-06-10)**

**Shipped:** `test/e2e/network_violation_test.go` and `tool_violation_test.go` assert `status.usage.networkRequests` / `toolCalls` ≥ 1 alongside runtime violations and decisions.

**Verification:** `make test` (pass 2026-06-10); live specs with `make test-e2e-images && make test-e2e`.

---

### Task: Usage metrics (Phase 4) — **done (2026-06-10)**

**Shipped:** `status.usage` populated via `ApplyUsageFromReport` — novel runtime decisions increment `networkRequests` (`type: network`) and `toolCalls` (`type: tool`); optional `usage` delta on `POST /v1/report` for tokens; idempotent with decision dedup; `mergeUsageInPlace` on reconcile/reporter patches. Tests: `usage_test.go`, `status_test.go`, `reporter/more_test.go`; live e2e usage in slice A.

**Verification:** `make test` (pass 2026-06-10)

### Task: Session timeline model (Phase 4) — slice B — **done (2026-06-10)**

**Shipped:** `internal/observability/timeline.go` — `ProjectTimeline`, `FilterTimeline`, `GroupByCategory`; `TimelineEntry` with severity/title/detail normalization; `docs/design/phase-4-session-timeline.md`; unit tests.

**Verification:** `make test` (pass 2026-06-10)

**Recently completed** (do not re-implement unless regressions): **Log/artifact collection**; **Audit log sink**; **OpenTelemetry**; **Prometheus metrics**; file domain e2e; Phase 3b evidence loop.

---

## Phase 2 — closed (2026-06-03)

**Status:** All roadmap checkboxes and completion tasks (1–6) are **done**. Control-plane policy + runtime profiles ship without data-plane enforcement.

**Verification pass (same session):**

| Check | Result |
|-------|--------|
| `make fmt && make vet && make test` | Pass — **47** envtest specs; controller ~**78%** coverage |
| `make verify-samples` | Pass — 10 `relay_*.yaml` samples (policy, toolpolicy, runtimeprofile refs) |
| `make test-e2e` | Pass — **12/12** specs on kind |

**Phase 2 capability → test coverage:**

| Capability | Envtest | E2e | Samples |
|------------|---------|-----|---------|
| `AgentPolicy` + `policyRefs` merge | Yes | — | `agentpolicy` + `agentsession_policy_ref` |
| `ToolPolicy` in `policyRefs` | Yes | — | `toolpolicy` + `agentsession_toolpolicy_ref` |
| Policy watches + pending Job env sync | Yes | — | — |
| `PolicyPropagated` / `PolicyEnvDrift` | Yes | — | README |
| `status.policyDecisions` (merge) | Yes | — | — |
| `RuntimeProfile` CRD | — | — | `runtimeprofile.yaml` |
| `runtimeProfileRef` + validation | Yes | — | `agentsession_runtimeprofile_ref` |
| Profile → Job pod template | Yes | Yes | — |
| `RuntimeProfile` watch + pending Job replace | Yes | Yes | — |

**Deferred (tracked, not Phase 2 blockers):** ToolPolicy argument constraints, mode enforcement, runtime `policyDecisions` append — see table under Phase 2 roadmap below.

---

## Phase 2 completion tasks (archived — all done 2026-06-03)

Tasks 1–6 below were implemented in sequence; kept for reference. Do not re-run unless regressions.

---

### Task: RuntimeProfile CRD API and manifests

**Goal:**  
Ship a namespace-scoped `RuntimeProfile` CRD with declarative hardening and future sidecar/sandbox hooks.

**Why it matters:**  
Phase 2’s last roadmap item; operators need a reusable profile object before sessions can reference it.

**Scope:**
- Add `api/v1alpha1/runtimeprofile_types.go` with `RuntimeProfileSpec` / `RuntimeProfileStatus` (minimal status: `observedGeneration` reserved).
- Spec fields (declarative only in this task):
  - Container: `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation`, `capabilities` (drop/add lists) — mirror Kubernetes `SecurityContext` subset.
  - Pod: `runtimeClassName` (sandbox selection hook), `seccompProfile` (type + localhostProfile).
  - Sidecars: optional `sidecars[]` with `name`, `type` (e.g. `envoy`, `dns-proxy`), `enabled` — **schema only**, no injection.
- Register in `groupversion_info.go` / scheme; kubebuilder RBAC markers stub if needed later.
- `config/crd/kustomization.yaml` includes `runtimeprofiles`.
- Sample: `config/samples/relay_v1alpha1_runtimeprofile.yaml`; add to `config/samples/kustomization.yaml`.

**Non-goals:**
- Do not add `runtimeProfileRef` on `AgentSession` yet.
- Do not change Job reconciliation or inject sidecars.
- Do not implement gVisor/Kata/Envoy.

**Acceptance criteria:**
- `make manifests` generates `relay.secureai.dev_runtimeprofiles.yaml`.
- `make verify-samples` passes including the new sample.
- OpenAPI describes fields as declarative hooks until Phase 3 enforcement.

**Expected files:**
- `api/v1alpha1/runtimeprofile_types.go`
- `api/v1alpha1/zz_generated.deepcopy.go` (generated)
- `config/crd/bases/relay.secureai.dev_runtimeprofiles.yaml` (generated)
- `config/crd/kustomization.yaml`
- `config/samples/relay_v1alpha1_runtimeprofile.yaml`
- `config/samples/kustomization.yaml`

**Verification command:**  
`make manifests && make verify-samples`

---

### Task: AgentSession runtimeProfileRef and validation

**Goal:**  
Sessions can reference a `RuntimeProfile` in the same namespace; invalid refs fail validation like policy refs.

**Why it matters:**  
Wires the session API to profiles before the reconciler applies them to Jobs.

**Scope:**
- Add `spec.runtimeProfileRef` on `AgentSessionSpec` (name required; kind defaults to `RuntimeProfile`).
- API comments: same-namespace only (match `PolicyRef` / `PromptConfigMapRef` pattern).
- Controller `validateSpec` / resolve path: missing `RuntimeProfile` → `PhaseDenied` with clear reason (mirror `InvalidPolicy`).
- Optional condition stub: `RuntimeProfileResolved` constant only (full wiring in task 3).

**Non-goals:**
- Do not apply profile fields to Job pod template yet.
- Do not add RuntimeProfile watch.
- Do not add cross-namespace refs.

**Acceptance criteria:**
- Valid ref passes validation; missing profile denies session without creating a Job.
- Envtest covers happy ref + missing profile denial.

**Expected files:**
- `api/v1alpha1/agentsession_types.go`
- `internal/controller/agentsession_controller.go` (validation)
- `internal/controller/agentsession_controller_test.go`
- `internal/controller/constants.go` (condition name constant)

**Verification command:**  
`make test`

---

### Task: Apply RuntimeProfile to Job pod template

**Goal:**  
Referenced profiles merge into the owned Job’s pod/container security context and pod-level runtime settings.

**Why it matters:**  
Completes the control-plane loop: declare profile → materialize on the execution surface (Job template).

**Scope:**
- Load `RuntimeProfile` when `spec.runtimeProfileRef` is set (`internal/controller/runtimeprofile.go` or equivalent).
- Merge profile container fields with `defaultContainerSecurityContext()` in `job.go` (profile overrides baseline where set).
- Apply pod-level `runtimeClassName`, `seccompProfile` on Job pod template.
- Status: `status.runtimeProfile` (or `matchedRuntimeProfile`) with name + `resourceVersion`/`generation`.
- Set `RuntimeProfileResolved` condition True/False with reason (e.g. `ProfileApplied`, `ProfileNotFound`).
- Normal event when profile applied (optional, match `PolicyResolved` style).

**Non-goals:**
- Do not inject sidecars from `spec.sidecars` (Phase 3).
- Do not replace running Jobs on profile drift (document immutability; same as policy env on active Jobs).
- Do not change sample images to require `runAsNonRoot` globally (only sessions with explicit profile).

**Acceptance criteria:**
- Envtest: session with profile ref produces Job with expected `securityContext` / `runtimeClassName`.
- Session without ref keeps current baseline behavior (busybox-friendly default).
- Missing profile → denied path from task 2 still works.

**Expected files:**
- `internal/controller/runtimeprofile.go` (new)
- `internal/controller/job.go`
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`
- `api/v1alpha1/agentsession_types.go` (status field if added)

**Verification command:**  
`make test`

---

### Task: RuntimeProfile watch for session re-reconcile

**Goal:**  
Updating or deleting a `RuntimeProfile` re-reconciles sessions that reference it.

**Why it matters:**  
Matches `AgentPolicy` / `ToolPolicy` watch behavior so profile edits propagate to pending Jobs.

**Scope:**
- `Watches(RuntimeProfile)` with map function → sessions in same namespace referencing profile name.
- Reuse list+filter pattern from `internal/controller/policy_watch.go`.
- Envtest: change profile `runAsNonRoot` (or similar) → session reconcile updates desired Job for pending Job; active Job behavior per immutability rules.

**Non-goals:**
- Do not implement profile drift replacement for active Jobs beyond existing immutability.
- Do not watch sidecar ConfigMaps.

**Acceptance criteria:**
- Envtest proves profile update triggers reconcile and updates Job spec when Job is still pending (`Active==0`).
- RBAC includes `runtimeprofiles` get/list/watch if not already present.

**Expected files:**
- `internal/controller/runtimeprofile_watch.go` (new) or extend `policy_watch.go`
- `internal/controller/agentsession_controller.go` (`SetupWithManager`)
- `internal/controller/agentsession_controller_test.go`
- `config/rbac/role.yaml` (generated)

**Verification command:**  
`make manifests && make test`

---

### Task: RuntimeProfile operator docs, samples, and e2e

**Goal:**  
Operators can discover, apply, and verify RuntimeProfile usage without reading controller code.

**Why it matters:**  
Phase 2 parity with policy CRDs (README + samples + verify-samples; e2e where practical).

**Scope:**
- README section: what RuntimeProfile does, same-namespace `runtimeProfileRef`, merge with baseline security context, immutability on running Jobs.
- Update long-term / MVP tables (`RuntimeProfile` row: shipped vs schema-only sidecars).
- Sample session: `config/samples/relay_v1alpha1_agentsession_runtimeprofile_ref.yaml` + kustomization entry.
- External reference scoping table: add `runtimeProfileRef` row.
- E2e (if practical): assert Job pod spec field from applied profile, or document why envtest-only (image constraints).

**Non-goals:**
- Do not document Envoy/gVisor enforcement as shipped.
- Do not add UI.

**Acceptance criteria:**
- `make verify-samples` includes runtime profile + session ref samples.
- README accurately states declarative-only sidecar/sandbox fields.

**Expected files:**
- `README.md`
- `config/samples/relay_v1alpha1_agentsession_runtimeprofile_ref.yaml`
- `config/samples/kustomization.yaml`
- `test/e2e/` (optional new spec)

**Verification command:**  
`make verify-samples` (and `make test-e2e` if e2e added)

---

### Task: Close Phase 2 roadmap and operational state

**Goal:**  
Status file and roadmap reflect Phase 2 as complete after RuntimeProfile ships.

**Why it matters:**  
Prevents agents from re-implementing finished work and clarifies Phase 3 entry point.

**Scope:**
- Mark `[x] RuntimeProfile CRD` on Phase 2 roadmap; add recent-fixes bullet.
- Update **Current Operational State** table (`Additional CRDs (Phase 2)` → done).
- Move completed Phase 2 completion cards to **Recently completed**; clear or repoint **Ready for Cursor Queue**.
- Confirm **Phase 2 deferred** table still accurate (optional polish tasks remain discovered, not Phase 2 blockers).

**Non-goals:**
- Do not implement new code in this task.
- Do not start Phase 3 work.

**Acceptance criteria:**
- No unchecked Phase 2 roadmap bullets except any explicitly deferred items with user approval.
- **Next up** in queue points to Phase 3 planning or a promoted discovered task.

**Expected files:**
- `.cursor/relay-project-status.md`

**Verification command:**  
Review only (no code change required beyond status file)

---

## Discovered Follow-Up Tasks

**Purpose:** Permanent backlog for work noticed but not in the current task scope. Agents **must** add a task card here (or a roadmap bullet) **in the same session** when they discover out-of-scope work — chat summaries and “suggested next picks” alone are not enough; untracked items become project holes.

Scoped tasks found by repository audit or implementation work. **Not in the active queue** until promoted. Pick one at a time into **Ready for Cursor Queue** when appropriate.

**Runtime evidence loop — promote in this order** (rationale in *Ready for Cursor Queue*):

1. ~~Runtime reporter mechanism design~~ — **done** (`docs/design/phase-3-runtime-reporter-contract.md`).
2. ~~Runtime reporter loop (impl)~~ — **done** (`internal/reporter/`).
3. ~~Structured session events API~~ — **done** (`docs/design/phase-4-session-events.md`).
4. ~~Reporter pod wiring~~ — **done** (Service + projected token + `RELAY_REPORTER_URL`).
5. ~~First-party dns-proxy image MVP~~ — **done** (`cmd/dns-proxy`, `Dockerfile.dns-proxy`).
6. ~~First-party tool-gateway image MVP~~ — **done** (`cmd/tool-gateway`, `Dockerfile.tool-gateway`).
7. ~~Live network violation population~~ — **done** (`test/e2e/network_violation_test.go`).
8. ~~File/workspace policy implementation~~ — **done** (`internal/enforcement/workspace/`, `PolicyRules` path fields).

Cards below are grouped: evidence-loop cards first, then unrelated backlog.

### Task: Phase 5 · per-tool runtime approval — **design + impl slices 1–2 done (slice 1 2026-06-21, slice 2 2026-06-23); slices 3–4 queued**

**Design shipped:** `docs/design/phase-5-runtime-tool-approval.md` — a **mid-execution** human gate that holds a specific running tool/MCP call until a scoped, time-bounded human grant, then allows or denies it. Reuses the `ApprovalRequest` CRD (runtime variant keyed by `requestId`, `spec.trigger=runtime`, redacted `argDigest`) and the existing approver/`allOf`/notification/audit machinery; turns `requireHumanApproval` from a surfaced reason into a real hold (ordered **after** hard denies so auto-denied calls are never escalated). Extends the reporter contract with an approval request/lookup channel (`POST /v1/approvals` idempotent by `requestId`, `GET /v1/approvals/{id}`) reusing TokenReview + pod→session ownership; controller stays sole CRD-status writer. **Honest posture:** ships as a **cooperative** gate (gateway shares pod/SA) stamped `assuranceLevel: self-reported`; it does not claim adversarial-grade enforcement. Resolves `phase-5-approval-workflows.md` open question #4.

**Slice 1 shipped (controller runtime variant):** `ApprovalRequest` gained `spec.trigger` (`session`|`runtime`, default `session`), `spec.requestId`, and `spec.scope.argDigest` (redacted digest only — never raw args). New `reconcileRuntimeApprovals`/`reconcileRuntimeApproval` (`internal/controller/agentsession/approval_runtime.go`) resolve each runtime request's lifecycle (decision→state→audit) **without** touching session phase: approver-allowlist + `allOf` + `onTimeout` reused from the session gate; the human decision is mirrored to the audit sink (`audit.ApprovalDecision`); session-level self-reported evidence stays the gateway/reporter's job (slices 3+). Grant expiry is nil-policy-safe via `approvalValidityWindow` (policy `expiresAfter` → request `scope.window`). Wired as a pass in `Reconcile` after the session gate proceeds. Tests: `approval_runtime_test.go` (envtest grant/deny without session gating, unlisted-approver rejection with policy; unit helpers). Verified `go build`, `go vet`, controller/api/policy `go test` green (2026-06-21). **Files:** `api/v1alpha1/approvalrequest_types.go`, `internal/controller/agentsession/{approval.go,approval_runtime.go,reconciler.go}`, generated CRD + tests.

**Slice 2 shipped (reporter approval channel):** new `ApprovalHandler` (`internal/reporter/approvals.go`) serves `POST /v1/approvals` (idempotent create-or-lookup keyed by `requestId` via `RuntimeApprovalName`) and `GET /v1/approvals/{id}?namespace=` (poll), wired into the reporter `NewRunnable` mux. It reuses the reporter's `IdentityVerifier` (TokenReview + pod→Job→session ownership) on both paths (lookup authorizes against the stored request's session), creates the runtime `ApprovalRequest` owner-ref'd to the session (GC), and only reports the controller-observed `.status.state` — controller stays sole status writer. Carries `argDigest` only (no raw args). Request/response types in `types.go`; reporter RBAC marker adds `approvalrequests: get;create` (already covered by the controller role union — no manifest drift). Tests in `approvals_test.go` (idempotent create, lookup state, unauthorized/forbidden, bad-request, session/lookup not-found, deterministic+bounded name). Verified `go build`, `go vet`, `go test ./internal/reporter/...` green (2026-06-23).

**Impl slices remaining (promote one at a time):**
3. Tool-gateway hold-and-ask — reorder `EvaluateTool`, bounded long-poll/`202` blocking model, call the channel, allow/deny + redacted runtime approval evidence. (`make test`)
4. Live e2e — held `requireHumanApproval` call released by a `kubectl patch` grant; redacted runtime approval decision in status. (`make test-e2e`)

**Follow-up (track):** approval-channel abuse controls — per-session rate limit on `POST /v1/approvals` and a cap on outstanding runtime holds per session (the report endpoint rate-limits; the approval endpoint does not yet). Deferred from slice 2.

**Open questions (in design doc):** blocking-model default (long-poll vs client re-poll), per-argument scoping granularity, grant reuse window, notifier fan-out, adversarial upgrade path.

### Task: Phase 6 · slice 2b — normalize `runtimeBackend` to `observation` (reconciler owns status) — **done (2026-06-21)**

**Shipped:** `kubernetesJobBackend.ensure` now returns a backend-neutral `observation` (`phase`/`runtimeName`/`workloadName`/`created`/`replaced`/`policyInSync`/`policyMessage`) and no longer writes to the session. The reconciler's new `applyObservation` + `applyRuntimePhase` (`reconciler.go`) own all status mapping: phase→`AgentSessionPhase`, `RuntimeCreated`/`PolicyPropagated`/`Completed` conditions, lifecycle events (`JobCreated`/`JobRunning`/`JobSucceeded`/`JobFailed`/`PolicyEnvSynced`/`PolicyEnvDrift`), `StartTime`, and `SessionResult`. The backend dropped its event recorder (holds only client/apiReader/scheme); Job-state→phase mapping is the package func `jobRuntimePhase`. This restores the design-doc invariant "reconciler — not the backend — owns status," the prerequisite for a clean second backend.

**Behavior-preserving:** full suite green (agentsession envtests + units). The two former `syncStatusFromJob` unit tests now exercise `applyRuntimePhase(session, jobRuntimePhase(job))`.

**Verification:** `go build ./...`; `go vet ./internal/controller/agentsession/...`; `KUBEBUILDER_ASSETS=… go test ./...` (all pass 2026-06-21).

**Files:** `internal/controller/agentsession/runtime_backend.go`, `reconciler.go`, `reconciler_test.go`.

### Task: Provider-agnostic `model.baseURL` (enables OpenRouter & OpenAI-compatible endpoints) — **done (2026-06-21)**

**Shipped:** Optional `ModelSpec.BaseURL` (`api/v1alpha1/agentsession_types.go`, CRD `Pattern=^https?://.+`); propagated to the agent as `AGENT_MODEL_BASE_URL` (`internal/controller/job/builder.go` + `constants.go`) and tracked in `managedEnvKeys` so a change is a policy-env drift/sync (`sync.go`). Controller defense-in-depth URL check in `validateSpec` (`validation.go`). Stays **provider-agnostic** — no provider allowlist; Relay never calls the model. Tests: `builder_test.go` (env set + empty when unset), `sync_test.go`/`builder_test.go` drift, `validation_test.go` (valid + non-http(s) rejected). README env list updated.

**Auth — still deferred (per user, 2026-06-21):** key delivery belongs to Phase 8 `CredentialProfile` + egress/tool-gateway brokering. An aggregator key is high blast radius → no plaintext key injection. Capturing the **routed** downstream model in evidence (so audit isn't blinded by the aggregator) is tracked under *Runtime evidence integrity*.

**Verification:** `make manifests generate` + `go build`; `go test ./internal/controller/job/... ./internal/controller/agentsession/...` (pass 2026-06-21).

### Task: Investigate AgentSession reconcile churn (repeated PolicyResolved events + status conflicts) — **done (2026-06-21)**

**Discovered:** 2026-06-09 during the test-hardening e2e run. Controller logs show the same `PolicyResolved` / "Merged N referenced policies" event re-emitted many times on the *same* resourceVersion for a single session, plus occasional `update AgentSession status: conflict (will requeue)` errors.

**Findings:**
- **Status writes were already idempotent** — `patchStatus` (`status.go`) unions conditions/decisions/violations/events, then short-circuits via `equalStatus` (`reflect.DeepEqual`) before `Status().Update`. `meta.SetStatusCondition` preserves `LastTransitionTime` when nothing changes, so no-op reconciles do not write. (Conflict errors are normal optimistic-concurrency requeues, not spurious writes.)
- **Event emission was the churn** — `PolicyResolved` and `RuntimeProfileResolved` were recorded on *every* reconcile whenever policies matched / a profile applied, regardless of change, refreshing the aggregated Event's count + lastTimestamp on each requeue.

**Shipped:** added `conditionChanged(snapshot, current, condType)` in `reconciler.go` (compares the reconcile-start snapshot `original` vs the freshly-set condition by status/reason/message). Gated both resolution events on it — emitted once per real transition instead of every requeue. Unit test `TestConditionChanged` (`churn_test.go`) covers absent/new/identical/message/reason/status cases.

**Verification:** `go build ./...`; `go test ./internal/controller/agentsession/` (full envtest suite + unit, pass 2026-06-21).

**Files:** `internal/controller/agentsession/reconciler.go`, `internal/controller/agentsession/churn_test.go`.

### Task: Raise unit coverage on data-plane producer packages — **done (2026-06-10)**

**Shipped:** Unit tests for `internal/controller/job` (status, sync drift, workspace volumes, envoy placeholder), `dnsproxy` (backend, evaluate/report/reporter/proxy allow path), `toolgateway` (runtime env, config, backend), `workspace` (backend, report), `policy` (LoadPolicyLayers fake client, ApplyStatus, caps/network merge decisions). All previously sub-70% packages now ≥73%.

**Coverage (2026-06-10):** job **80.8%**, dnsproxy **73.7%**, toolgateway **85.8%**, workspace **95.8%**, policy **93.0%**.

**Verification:** `make test` (pass 2026-06-10).

### Task: Watch owned Pods for reconcile triggers — **done (2026-06-08)**

**Shipped:** Added `Watches(&corev1.Pod{})` in `SetupWithManager`; Pod event mapper enqueues the labeled AgentSession only for Job-owned Pods; envtest mapping coverage added.

**Verification:** `make test` (pass 2026-06-08)

### Task: Document future-only status fields — **done (2026-06-08)**

**Shipped:** API comments on `usage` / `violations` / `artifacts`; README status table with populated vs reserved (Phase 3/4).

**Verification:** `make manifests && make test` (pass 2026-06-08)

### Task: Document Kubernetes Events emitted by the controller — **done (2026-06-08)**

**Shipped:** README [Kubernetes Events](#kubernetes-events) catalog (all `EventReason*` constants, Normal/Warning, `kubectl describe` examples). Constants already commented in `internal/controller/agentsession/constants.go`.

**Verification:** `make test` (pass 2026-06-08)

### Task: Add AgentSession Ready condition — **done (2026-06-08)**

**Shipped:**
- Added `status.conditions` type `Ready` (`internal/controller/agentsession/constants.go`)
- Reconciler sets `Ready` before every status patch based on `status.phase` (`internal/controller/agentsession/reconciler.go`)
- API comment documents all condition types including `Ready`
- Envtest coverage:
  - Denied path asserts `Ready=False`
  - Job-running path asserts `Ready=True`

**Verification:** `make test` (pass 2026-06-08)

### Task: ToolPolicy MCP argument constraints (schema design) — **design done (2026-06-21)**

**Shipped:** `docs/design/phase-3-tool-argument-constraints.md` — specifies an argument-level governance layer on top of name-level tool rules: a `ToolArgumentRule`/`ArgumentConstraint` schema (dotted arg path + operator enum + `Allow`/`Deny` effect), evaluation order (args checked only after the name gate, deny-precedence, allow-as-allowlist), most-restrictive merge (concatenate; constraints only tighten), the tool-gateway enforcement hook (`ToolRequest.Arguments`, new `ArgumentDenied`/`ArgumentNotAllowed` reasons, `GatewayConfig`), evidence **redaction** (no raw arg values in status/logs), invariants, a 4-slice migration plan, and open questions (CEL vs structured matchers, path syntax, redaction strategy). Indexed in `docs/design/README.md` + `relay-design-docs.mdc`. **Design-only; no code/enforcement.**

**Implementation deferred** to scoped slices — see the two follow-up cards below (schema, then gateway evaluation).

**Verification:** Review only (docs); `make test` unaffected.

### Task: Tool argument constraints — slice 2 (control-plane schema) — **done (2026-06-21)**

**Shipped:** `ToolArgumentRule`/`ArgumentConstraint`/`ArgumentOperator`/`ConstraintEffect` types on `ToolPolicySpec` + `PolicyRules` (`api/v1alpha1`), with kubebuilder validation (operator/effect enums, `MinItems`, `MinLength`). `ToolPolicySpec.ToolPolicyRules()` maps `argumentRules`; `MergeRules` concatenates them across layers with structural dedupe (`concatArgumentRules`/`argumentRuleKey` in `merge.go`) so constraints only tighten; `BuildMergeDecisions` emits one `ArgumentRulesDeclared` `tool`/`argumentRules` summary decision (controller assurance). Regenerated deepcopy + CRDs (toolpolicy/agentpolicy/agentsession). Sample extended (`config/samples/relay_v1alpha1_toolpolicy.yaml`) and validated via `make verify-samples`. README + design-doc status updated. **No enforcement** (slice 3).

**Verification:** `make generate manifests`; `go test ./internal/policy/... ./api/...`; `make verify-samples`; full suite green (2026-06-21). Tests: `merge_test.go` (concat/dedupe/effect-identity), `decisions_test.go` (summary decision).

**Files:** `api/v1alpha1/toolpolicy_types.go`, `api/v1alpha1/policy_types.go`, `internal/policy/merge.go`, `internal/policy/decisions.go`, generated CRDs + deepcopy, sample, `README.md`.

### Task: Tool argument constraints — slice 3 (gateway evaluation + redacted evidence) — **done (2026-06-21)**

**Shipped:** per-call argument evaluation in the tool gateway. `ToolRequest.Arguments` + `invokeRequest.arguments` carry the decoded arg object; `evaluateArgumentRules` (`argconstraints.go`) resolves dotted/`[i]` paths and applies operator matchers (Equals/In/NotIn/Matches/HasPrefix/Exists/… with safe missing-arg semantics), with deny-precedence and Allow-as-allowlist. `EvaluateTool` runs it only after the name gate (name deny still wins); new reasons `ArgumentDenied`/`ArgumentNotAllowed`. `RuntimeReport` emits **redacted** decisions/violations — the matched constraint (arg path, operator, effect, policy operands) only, never the request value (`ArgConstraintMatch`). Rules propagate to the sidecar as JSON via `AGENT_POLICY_ARGUMENT_RULES` (`GatewayConfig.ArgumentRules`, `EnvForConfig`, `LoadRuntimeEnv`). Assurance stays `self-reported`.

**Verification:** `go test ./internal/enforcement/toolgateway/...` + full suite green (2026-06-21). Tests in `argconstraints_test.go`: path resolver, every operator, deny/allowlist/server-scope/wildcard eval, mode behavior, name-deny precedence, redaction (no raw value in message/violation), env round-trip.

**Files:** `internal/enforcement/toolgateway/argconstraints.go` (new), `types.go`, `evaluate.go`, `report.go`, `config.go`, `runtime_env.go`, `gateway.go`, `argconstraints_test.go`.

### Task: Tool argument constraints — slice 4 (live e2e) — **done (2026-06-21)**

**Shipped:** new spec in `test/e2e/tool_violation_test.go` ("populates a redacted argument violation…") + fixtures (`createEnforcedArgumentRuleToolPolicy`, `withArgumentDeniedToolInvokeProbe`). Enforced `argumentRules` (allow `read_file` by name, deny `path` HasPrefix `/etc/`) + tool-gateway sidecar; the agent POSTs `{"tool":"read_file","arguments":{"path":"/etc/shadow-SECRETTOKEN"}}`; the in-cluster reporter populates a runtime decision (`ArgumentDenied`, `type=tool`, `rule=argumentRules`, `action=deny`, `target=read_file`) and a violation. Asserts the request value (`SECRETTOKEN`) never appears in any decision/violation (redaction).

**Verified live** against the `relay-dev` kind cluster (2026-06-21): both tool-violation specs pass (`2 Passed`), no regression. Run: `RELAY_E2E_IMG=<shell-capable relay img> go test -tags=e2e ./test/e2e/... -ginkgo.focus="Live tool violation population"`.

**Files:** `test/e2e/tool_violation_test.go`, `test/e2e/fixtures_test.go`.

### Task: e2e live-evidence image probe assumes a shell (distroless skip) — **done (2026-06-21)**

**Shipped:** `clusterImageRunnable` (`test/e2e/reporter_infra_test.go`) no longer launches a `sh -c` probe pod (which always failed on the `distroless/static` relay + sidecar images, silently skipping every live-evidence spec). It now inspects `node.status.images` for image presence, with `normalizeImageRef` stripping default-registry prefixes so user refs match the runtime's fully-qualified names. Graceful skip preserved when an image is genuinely absent.

**Verified live** (2026-06-21): with the **standard distroless** relay + tool-gateway images and **no** `RELAY_E2E_IMG` override, the two tool-violation specs now run and pass (`2 Passed`) — previously they skipped.

**Files:** `test/e2e/reporter_infra_test.go`.

### Task: Propagate ToolPolicy maxCallsPerMinute to runtime hooks — **done (2026-06-08)**

**Shipped:** `MaxCallsPerMinute` on `PolicyRules`; min-merge semantics; `AGENT_POLICY_MAX_TOOL_CALLS_PER_MINUTE` env + drift detection; merge-time `policyDecisions` cap entry; envtest + README. **Enforcement:** Phase 3 only.

**Verification:** `make test` (pass 2026-06-08)

### Task: Phase 3 enforcement backend contract — **done (2026-06-08)**

**Shipped:** `internal/enforcement/` — `SessionContext`, `Backend`, `Capabilities`, `RuntimeReport`, `EvaluateRestrictive`, `ActionForMode`, `AppendRuntimeDecisions`; unit tests for mode mapping, context build, and truncation.

**Verification:** `make test` (pass 2026-06-08)

### Task: DNS / egress proxy prototype — **done (2026-06-09)**

**Shipped:** `internal/enforcement/dnsproxy/`; sidecar policy env + agent `HTTP_PROXY`; `ApplyEgressProxyRuntimeEvent`; `docs/design/phase-3-dns-proxy-prototype.md`; **`cmd/dns-proxy`** sidecar binary + `Dockerfile.dns-proxy`; HTTP egress proxy with reporter client; sidecar image `ghcr.io/secureai/relay-dns-proxy:latest`.

**Verification:** `make test` (pass 2026-06-09)

### Task: File/workspace policy design — **done (2026-06-08)**

**Shipped:** `docs/design/phase-3-file-workspace-policy.md` — mount + RuntimeProfile MVP; defer path rules and FS gateway; `internal/enforcement/workspace/types.go` stubs.

**Verification:** `make test` (pass 2026-06-08)

### Task: First-party data-plane sidecar images — evidence loop #5–#6 — **done (2026-06-10)**

**Shipped:** dns-proxy (`cmd/dns-proxy`, `Dockerfile.dns-proxy`, `ghcr.io/secureai/relay-dns-proxy:latest`); tool-gateway (`cmd/tool-gateway`, `Dockerfile.tool-gateway`, `ghcr.io/secureai/relay-tool-gateway:latest`); sidecar injection in `job/sidecars.go`; `make docker-build-dns-proxy` / `make docker-build-tool-gateway`; integration tests in `dnsproxy/proxy_test.go` and `toolgateway/gateway_test.go`. Envoy still uses `busybox` placeholder.

**Verification:** `make test` (pass 2026-06-10)

### Task: Runtime reporter loop (impl) — evidence loop #2 — **done (2026-06-08)**

**Shipped:** `internal/reporter/` (`POST /v1/report`, `TokenReview` + pod→Job→session auth, rate limit); `agentsession.PatchRuntimePolicyReport`; idempotent decision/violation append; `--reporter-bind-address` (`:8088`); RBAC `tokenreviews: create`; handler unit tests.

**Verification:** `make test` (pass 2026-06-08)

### Task: Structured session events API — evidence loop #3 — **done (2026-06-08)**

**Shipped:** `SessionEvent` API type; `status.events[]` (max 256); `AppendSessionEvents` + `patchStatus`/`PatchRuntimePolicyReport` merge; reporter `events[]` payload; `docs/design/phase-4-session-events.md`; unit + handler tests.

**Verification:** `make manifests && make test` (pass 2026-06-08)

### Task: Reporter pod wiring (projected token + Service) — **done (2026-06-09)**

**Shipped:** `relay-controller-reporter` Service (`config/manager/reporter_service.yaml`); deployment exposes `:8088`; sidecars get `RELAY_REPORTER_URL`, `RELAY_REPORTER_TOKEN_PATH`, and projected SA token volume (`audience: relay-reporter`); samples + README.

**Verification:** `make test` (pass 2026-06-09)

### Task: Live violation population from network enforcement — evidence loop #7 — **done (2026-06-10)**

**Shipped:** E2e `test/e2e/network_violation_test.go` — enforced `deniedDomains` + dns-proxy sidecar + agent `HTTP_PROXY` wget probe → in-cluster `--reporter-only` deployment → `status.violations` + runtime `policyDecisions`. Infra: `test/e2e/reporter_infra_test.go`; prereq `make test-e2e-images`. Design note in `docs/design/phase-3-dns-proxy-prototype.md`. Kernel/CNI drops still unobserved (defer eBPF).

**Verification:** `make test` (pass 2026-06-10); `make test-e2e-images && make test-e2e` for live spec.

### Task: Live tool violation population (tool-gateway e2e) — **done (2026-06-10)**

**Shipped:** `test/e2e/tool_violation_test.go` — enforced `ToolPolicy` `deniedTools` + tool-gateway sidecar + agent `wget` POST to `/v1/tools/invoke` → in-cluster reporter → `status.violations` + runtime `policyDecisions` (`type: tool`). Fixtures in `fixtures_test.go`; `requireLiveToolEvidenceImages`; `make test-e2e-images` includes `kind-load-tool-gateway`.

**Verification:** `make test` (pass 2026-06-10); `make test-e2e-images && make test-e2e` for live spec.

### Task: Usage-only report idempotency (reportId cache) — Phase 4 · slice C — **done (2026-06-10)**

**Shipped:** `internal/reporter/reportid_cache.go` — in-process seen-cache keyed by session + `reportId` (24h TTL); handler short-circuits duplicate `POST /v1/report` with `202` before status patch; wired in `NewRunnable`. Tests: `reportid_cache_test.go`, `handler_test.go` (usage-only with/without reportId). Contract doc §7 updated.

**Verification:** `make test` (pass 2026-06-10)

### Task: File/workspace policy implementation — evidence loop #8 — **done (2026-06-10)**

**Shipped:** `PolicyRules.allowedPaths` / `deniedPaths` / `maxWorkspaceBytes`; merge in `internal/policy/`; `AGENT_POLICY_ALLOWED_PATHS` / `DENIED_PATHS` / `MAX_WORKSPACE_BYTES` env on agent; `internal/enforcement/workspace/` (`EvaluateFile`, `RuntimeReport`, `ApplyFilePolicyRuntimeEvent`, `Backend`); design doc updated.

**Verification:** `make manifests && make test` (pass 2026-06-10)

### Task: First-party FS gateway sidecar MVP — Phase 4 · slice D — **done (2026-06-10)**

**Shipped:** `cmd/fs-gateway`, `Dockerfile.fs-gateway`, `internal/enforcement/workspace/` gateway (`POST /v1/files/access`), runtime env, reporter client; `job/sidecars.go` injection for `fs-gateway` + `RELAY_FS_GATEWAY_URL` on agent; `make docker-build-fs-gateway` / `kind-load-fs-gateway`; integration test in `gateway_test.go`.

**Verification:** `make test` (pass 2026-06-10)

### Task: File usage metrics — Phase 4 · slice E — **done (2026-06-10)**

**Shipped:** `SessionUsage.fileOperations` on `AgentSession` status; `incrementUsageForDecision` / `addUsageDelta` / `mergeUsageInPlace` for `type: file`; reporter `validateUsageDelta` accepts file counter; CRD regenerated. Tests: `usage_test.go`.

**Verification:** `make manifests && make test` (pass 2026-06-10)

### Task: Live file violation and usage e2e — Phase 4 · slice F — **done (2026-06-10)**

**Shipped:** `test/e2e/file_violation_test.go` — enforced `deniedPaths` + fs-gateway sidecar + file access probe → `status.violations`, runtime `type: file` decisions, `status.usage.fileOperations` ≥ 1; fixtures (`createRuntimeProfileWithFSGateway`, `createEnforcedDeniedPathPolicy`, `withDeniedPathAccessProbe`); `requireLiveFileEvidenceImages`; `make test-e2e-images` includes `kind-load-fs-gateway`.

**Verification:** `make test` (pass 2026-06-10); live spec with `make test-e2e-images && make test-e2e`.

### Task: Prometheus metrics exporter — Phase 4 — **done (2026-06-10)**

**Shipped:** `internal/metrics/` — `relay_agentsessions{namespace,phase}`, `relay_agentsession_violations{namespace}`, `relay_approval_queue_depth`, `relay_policy_violations_observed_total{namespace,type}`, `relay_runtime_reports_total{result}`, `relay_runtime_report_duration_seconds`; `AgentSessionCollector` on manager cache; wired in `cmd/main.go`; violation + reporter hooks. Reconcile latency: use built-in `controller_runtime_reconcile_time_seconds`.

**Verification:** `make test` (pass 2026-06-10). Scrape `:8080/metrics` on the controller manager.

### Task: OpenTelemetry tracing — Phase 4 — **done (2026-06-10)**

**Shipped:** `internal/tracing/` — OTLP HTTP export (disabled when `--otel-exporter-otlp-endpoint` empty); `agentsession.reconcile` spans with session phase/requeue attributes; `runtime.report` spans on reporter with W3C trace context extraction (sidecars can continue agent traces via `traceparent`); flags `--otel-exporter-otlp-endpoint`, `--otel-service-name`, `--otel-exporter-otlp-insecure`. Wired in `cmd/main.go`, `reconciler.go`, `reporter/server.go`, `reporter/handler.go`.

**Verification:** `make test` (pass 2026-06-10). Enable with e.g. `--otel-exporter-otlp-endpoint=http://otel-collector:4318`.

### Task: Audit log sink — Phase 4 — **done (2026-06-10)**

**Shipped:** `internal/audit/` — structured `Record` types (`policy.violation`, `session.phase_change`, `runtime.report`); OTLP HTTP log export via `--audit-log-otlp-endpoint` (disabled by default); hooks in reconciler phase transitions, novel violations, accepted runtime reports. Uses `go.opentelemetry.io/otel/sdk/log` + `otlploghttp`.

**Verification:** `make test` (pass 2026-06-10). Enable with e.g. `--audit-log-otlp-endpoint=http://otel-collector:4318`.

### Task: Log and artifact collection — Phase 4 — **done (2026-06-10)**

**Shipped:** `internal/controller/agentsession/outputs.go` — on terminal phase, when `spec.outputs.collectLogs` / `collectArtifacts`: fetch agent pod logs → owned ConfigMap (`configmap://` URI); tar workspace path (default `<mount>/artifacts`) via pod exec → owned Secret (`secret://` URI); populate `status.artifacts`; lifecycle event + `OutputsCollected` event. Caps 512KiB each; idempotent per artifact name. RBAC: `pods/log`, `pods/exec`, ConfigMap/Secret write. Tests: `outputs_test.go`.

**Verification:** `make manifests && make test` (pass 2026-06-10)

### Task: External artifact storage export (S3 / object store)

**Discovered:** 2026-06-10 post log/artifact collection MVP. Collection stores payloads in owned ConfigMaps/Secrets (`configmap://` / `secret://` URIs) with 512KiB caps.

**Why it matters:** Enterprise retention and forensics typically need durable object storage, not etcd-sized ConfigMaps.

**Scope (proposed):** Pluggable export backend; upload after collection; `status.artifacts` URIs like `s3://bucket/key`; configurable credentials via future `CredentialProfile`.

**Non-goals:** Replacing in-cluster MVP path in the same task.

**Verification:** `make test` + integration test with mock S3 or MinIO.

### Task: Runtime evidence integrity (cooperative → adversarial trust)

**Discovered:** 2026-06-16 repository audit. The reporter (`internal/reporter/auth.go`) authenticates the **pod** via TokenReview + pod→Job→session ownership, but enforcement sidecars and the agent share one pod and ServiceAccount. A compromised/prompt-injected agent could forge or suppress runtime evidence, or starve the sidecar. The reporter contract (`docs/design/phase-3-runtime-reporter-contract.md` §5) names this threat but the residual gap (cooperative, not adversarial) is not surfaced to consumers.

**Why it matters:** Relay is a governance/audit product; trustworthy evidence is core to the value proposition (see product vision *Trust And Threat Model → Evidence integrity*). Audit/UI consumers must not treat self-reported evidence as tamper-proof.

**Slice 1 — assurance level (honesty first) — done (2026-06-21):** Added `EvidenceAssurance` enum (`controller` / `self-reported` / `observed`) + `assuranceLevel` field on `PolicyDecision` and `PolicyViolation`. The cooperative reporter (`internal/reporter/normalize.go`) stamps all ingested runtime decisions/violations `self-reported`, **overriding any client value** (a source can't self-attest trust). Merge-time decisions (`internal/policy/decisions.go`) stamp `controller`. `observed` reserved for future independent sources. Reporter contract §5 updated. Tests: `decisions_test.go`, `reporter/more_test.go`. Verification: `make manifests && make test` (pass 2026-06-21).

**Slice 2 — pod least-privilege hardening — done (2026-06-21):** Reporter token projection was already sidecar-only (the agent never mounts the `relay-reporter` projected token; guarded by `TestBuild_reporterWiringForSidecars`). Added `automountServiceAccountToken: false` on the agent pod (`internal/controller/job/builder.go`) so a compromised agent gets no apiserver-audience SA token by default; enforcement sidecars are unaffected (they carry their own narrowly-scoped projected reporter token). Test: `TestBuild_disablesServiceAccountTokenAutomount`. Verification: `go test ./internal/controller/job/...` (pass 2026-06-21).

**Slice 3 — assurance in audit records — done (2026-06-21):** `policy.violation`, `runtime.report`, and `approval.granted`/`approval.denied` OTLP audit records now carry `relay.audit.assurance` (`internal/audit` `Record.Assurance` + `relay.audit.assurance` attribute). Violations use their `AssuranceLevel` (empty → `self-reported`); runtime reports are `self-reported` (cooperative sidecars); approval decisions are `controller` (control-plane authoritative). Builder tests in `internal/audit/sink_test.go`; observability doc updated. So SIEM/audit consumers now see trust level per record (UI surfacing still Phase 7).

**Remaining (hardening, later — larger, not started):**
- Surface `assuranceLevel` in the future **UI** evidence views (Phase 7) — audit records already carry it.
- Consider out-of-pod / kernel (eBPF) observation as an independent `observed` evidence source.
- Optional `RuntimeProfile` opt-in to re-enable SA token automount for agents that legitimately need apiserver access (none in MVP).

**Non-goals:** Implementing eBPF/Cilium; rewriting the reporter auth model in one pass.

**Verification:** `make test`.

**Files:** `api/v1alpha1/policy_types.go`, `api/v1alpha1/agentsession_types.go`, `internal/reporter/normalize.go`, `internal/policy/decisions.go`, reporter contract doc §5.

### Task: Observability export design doc (Prometheus / OTel / audit) — **done (2026-06-21)**

**Shipped:** `docs/design/phase-4-observability-export.md` — catalogs the `relay_*` Prometheus metrics (6: `agentsessions`, `agentsession_violations`, `approval_queue_depth`, `policy_violations_observed_total`, `runtime_reports_total`, `runtime_report_duration_seconds`) with types/labels/collection model + cardinality rules; OTel spans (`agentsession.reconcile`, `runtime.report`) with attributes + the W3C TraceContext/Baggage propagation contract (sidecars continue traces via `traceparent`); OTLP audit log records (`policy.violation`, `session.phase_change`, `runtime.report`) with `relay.audit.*`/`relay.session.*`/`relay.report.*` attribute namespaces; enable flags; invariants; non-goals (no in-cluster collector, opt-in, no behavior change). Indexed in `docs/design/README.md` + `relay-design-docs.mdc`.

**Verification:** Docs-only; `go build`/`make test` unaffected.

**Discovered follow-ups (noted in the doc):** ~~refine `relay_approval_queue_depth`~~ (done 2026-06-21, below); add approval-decision audit records (`approval.granted`/`approval.denied`) when consumers need them; surface `assuranceLevel` in audit/UI (tracked under *Runtime evidence integrity*).

### Task: Refine `relay_approval_queue_depth` to count pending ApprovalRequests — **done (2026-06-21)**

**Shipped:** `AgentSessionCollector` (`internal/metrics/collector.go`) now lists `ApprovalRequest`s and counts those awaiting a human decision (`status.state` Pending or unset) instead of the prior proxy (running sessions with a runtime `ApprovalRequired` decision). Removed dead `hasApprovalRequiredDecision`/`approvalRequiredReason`; added `isPendingApproval`. Updated metric Help text, observability design doc, and tests (`TestAgentSessionCollector_updatesGauges` now drives queue depth from ApprovalRequests; granted requests excluded; added `TestIsPendingApproval`).

**Verification:** `go build ./...`; `go test ./internal/metrics/` (pass 2026-06-21).

### Task: Phase 6 · slice 1 — orchestrator interface design doc — **done (2026-06-21)**

**Shipped:** `docs/design/phase-6-orchestrator-interface.md` — catalogs every `batchv1.Job` coupling point in the reconciler (`ensureJob`, `syncStatusFromJob`, `findPodName`, `stopRuntimeJob`, `handleDeletion`, `SetupWithManager` `Owns`, the `internal/controller/job` package); proposes a `RuntimeBackend` interface (`Name`/`Ensure`/`Observe`/`Stop`/`OwnedType`) with a normalized `Observation`/`RuntimePhase` the reconciler maps to phase/conditions (governance logic stays backend-neutral); selection via the existing `spec.runtime.orchestrator` field + a startup registry; honest treatment of the data-plane/evidence channel (sidecars are Pod-specific → non-pod backends affect assurance); invariants; a behavior-preserving migration plan (slice 2 = extract interface + make Jobs the first backend); and open questions (status field generality, drift/replace per backend, evidence channels). Indexed in `docs/design/README.md` + `relay-design-docs.mdc`.

**Why design-first:** decoupling from Jobs is the largest architectural item the product vision flags; this defines the boundary before any refactor and matches the design-doc convention (Phase 5 was sequenced the same way).

**Verification:** Review only (docs); `make test` unaffected.

### Task: Phase 6 · slice 2 — extract `runtimeBackend` + kubernetes-job backend — **done (2026-06-21)**

**Shipped:** `internal/controller/agentsession/runtime_backend.go` — a `runtimeBackend` interface (`name`/`ensure`/`stop`/`runtimeGone`/`ownedType`) and a `backendRegistry` keyed by `spec.runtime.orchestrator`, lazily built from the reconciler's client/scheme/recorder. The `kubernetesJobBackend` holds the moved Job mechanics (`ensureJob`, `syncStatusFromJob`, `findPodName`, Job stop, Job observe). The reconciler routes every runtime call through `r.runtimeBackendFor(session)` — main path (`backend.ensure`), cancellation + finalizer cleanup (`backend.stop` + `backend.runtimeGone`), and `SetupWithManager` `Owns(backend.ownedType())`. **Behavior-preserving:** all existing agentsession envtests + the full suite green; no API/CRD change.

**Transitional deviation (tracked):** the backend still mutates AgentSession status/conditions/events directly instead of returning a normalized `Observation` the reconciler maps. This relaxes the "reconciler owns status" invariant on purpose to keep the diff behavior-preserving — see **Discovered Follow-Up Tasks → Phase 6 slice 2b**.

**Why:** decoupling the controller from Jobs is the top architectural item in the product vision. This establishes the seam (interface + registry + per-orchestrator selection + `Owns`) so future backends plug in without touching governance logic.

**Verification:** `go build ./...`; `go vet ./internal/controller/agentsession/...`; `KUBEBUILDER_ASSETS=… go test ./...` (all pass 2026-06-21).

### Phase 5 — approval workflows (ordered task cards)

Decomposed 2026-06-16 from the Phase 5 roadmap (was a capability with no slices). **Promote slice 1 into Ready for Cursor Queue when starting Phase 5.** Implement one slice at a time; do not bundle.

#### Task: Phase 5 · slice 1 — Approval model design doc — **done (2026-06-21)**

**Shipped:** `docs/design/phase-5-approval-workflows.md` — `ApprovalPolicy` (declarative: actions/approvers/expiry/onTimeout) vs `ApprovalRequest` (per-decision, controller-owned, human sets `spec.decision`); controller gate/resume state machine with proposed `PhaseAwaitingApproval` phase + `ApprovalRequired` condition; relationship to existing `requireHumanApproval` + `status.policyDecisions` (`type: approval`, `assuranceLevel: controller`); audit fields (who/when/scope/expiry); open questions (approver authn via RBAC + future webhook, multi-approver, per-tool runtime approval). Index updated in `docs/design/README.md` + `relay-design-docs.mdc`.

**Next:** slice 2 — `ApprovalPolicy` CRD (declarative only).

**Verification:** Review only (docs); `make test` unaffected.

#### Task: Phase 5 · slice 2 — ApprovalPolicy CRD (declarative only) — **done (2026-06-21)**

**Shipped:** `api/v1alpha1/approvalpolicy_types.go` — `ApprovalPolicy` CRD (`approvalpolicies`, short names `appol`/`approvalpol`). Spec: `actions` (required, `minItems: 1`), `approvers[]` (`kind` enum `User`/`Group`/`ServiceAccount` + `name`), `expiresAfter` (duration), `requirement` (`default`/`allOf`, default `default`), `onTimeout` (`deny`/`allow`, default `deny` — fail closed). Status `observedGeneration` reserved. Generated CRD + deepcopy; registered in `config/crd/kustomization.yaml`; sample `config/samples/relay_v1alpha1_approvalpolicy.yaml` + kustomization. Envtest create/validate (defaults + enum + required) in `internal/controller/agentsession/approvalpolicy_test.go`. No controller behavior (slice 3). Note: short name must avoid `ap` (collides with `agentpolicy`).

**Next:** slice 3 — `ApprovalRequest` CRD + controller gate/resume (`PhaseAwaitingApproval`).

**Verification:** `make manifests && make test` (pass 2026-06-21); `make verify-samples` (pass 2026-06-21).

#### Task: Phase 5 · slice 3 — ApprovalRequest CRD + controller gate — **done (2026-06-21)**

**Shipped:** `api/v1alpha1/approvalrequest_types.go` — `ApprovalRequest` CRD (`approvalrequests`, short names `appreq`/`approvalreq`); `spec` = `sessionRef`/`policyRef`/`action`/`scope`/`decision` (enum `""`/`granted`/`denied`); controller-owned `status` = `state`/`decidedBy`/`decidedAt`/`expiresAt`/`reason`. New session phase `PhaseAwaitingApproval` + condition `ApprovalRequired` + events `ApprovalRequested`/`ApprovalGranted`/`ApprovalDenied`. Gate in `internal/controller/agentsession/approval.go` (`reconcileApprovalGate`), wired in `reconciler.go` between the terminal check and `ensureJob`: when effective `requireHumanApproval` matches a namespace `ApprovalPolicy`, it creates an owned `ApprovalRequest` (1:1, name = session name), holds the session in `AwaitingApproval`, and resumes on `granted` / goes terminal `Denied` on `denied` or `onTimeout=deny` expiry. Control-plane approval `policyDecisions` (`type: approval`, `assuranceLevel: controller`) appended idempotently. Watch on `ApprovalRequest` → owning session. When approval is declared but **no** `ApprovalPolicy` matches, the legacy `ApprovalNotEnforced` warning is kept and the session proceeds. RBAC regenerated; sample `config/samples/relay_v1alpha1_approvalrequest.yaml`.

**MVP semantics:** `ApprovalPolicy.expiresAfter` is enforced as the **decision deadline** (from request creation), not a grant-validity window; one request per session; consume-time TOCTOU re-check and multi-scope requests deferred to later slices (noted in design doc).

**Acceptance (met):** Envtest in `approval_gate_test.go` — declared-but-ungated proceeds (warn-only); matching policy holds `AwaitingApproval` + creates request + no Job; grant resumes to Job (+ allow decision); deny → terminal `Denied` + no Job (+ deny decision).

**Verification:** `make manifests` + `go build ./...` + `go vet`; `go test ./internal/controller/... ./api/... ./internal/policy/...` (pass 2026-06-21); `make verify-samples` (pass).

**Next:** slice 4 — approval notification hooks.

#### Task: Phase 5 · slice 4 — Approval notification hooks — **done (2026-06-21)**

**Shipped:** `internal/approval/notifier.go` — pluggable `Notifier` interface with `NoopNotifier` (default) and `WebhookNotifier` (HTTP POST JSON, bounded 5s timeout, non-2xx → error). Reconciler hook `notifyApprovalRequest` (in `approval.go`) fires once when a session opens the gate (`AwaitingApproval` pending branch), guarded by the `relay.secureai.dev/approval-notified` annotation so delivery is **at-most-once and retried** on the pending requeue until it succeeds; failures emit `ApprovalNotifyFailed` (warning) and never block the gate. Success emits `ApprovalNotified`. Enabled via `cmd/main.go` flag `--approval-webhook-url` (empty = disabled, zero behavior change). Slack/PagerDuty are future adapters over the same interface.

**Acceptance (met):** Package unit tests (`notifier_test.go`) cover JSON payload delivery, non-2xx error, transport error, noop. Envtest (`approval_gate_test.go`) asserts exactly-once delivery on gate open + annotation marker (idempotent across requeues).

**Verification:** `go build ./...` + `go vet`; `go test ./internal/approval/... ./internal/controller/agentsession/...` (pass 2026-06-21).

**Next:** Phase 5 substantively complete (gate + notifications + allowlist + multi-approver). Remaining Phase 5 polish (per-tool runtime approval, authenticated approver-identity via webhook) tracked in `docs/design/phase-5-approval-workflows.md` open questions.

#### Task: Phase 5 · slice 5 — approver allowlist (best-effort `decidedBy`) — **done (2026-06-21)**

**Shipped:** `ApprovalRequest.spec.decidedBy` (approver self-declared identity, set alongside `spec.decision`). The gate (`approval.go` `approverAllowed`) honors a grant only when `decidedBy` matches a listed `ApprovalPolicy.approvers[].name` (match by name; Kind advisory); an unlisted/blank grant keeps the session `AwaitingApproval`, sets condition `ApprovalRequired=ApproverNotAuthorized`, and emits `ApprovalUnauthorized` (warning). When the policy lists no approvers, any grant is accepted (RBAC is the gate). `status.decidedBy` is recorded on decision and used as the approval `policyDecisions` actor. Envtest: unlisted grant stays gated; listed approver resumes (`approval_gate_test.go`).

**Honesty note:** `decidedBy` is **not authenticated** — the real boundary is RBAC on who may patch the `ApprovalRequest`. Authenticated capture (record apiserver `userInfo`) needs a validating webhook (deferred; design doc open question #1).

**Verification:** `make manifests generate` + `go build` + `go vet`; `go test ./internal/controller/agentsession/...` (pass 2026-06-21).

#### Task: Phase 5 · slice 6 — multi-approver (`allOf`) — **done (2026-06-21)**

**Shipped:** `requirement: allOf` is now enforced. New controller-owned `ApprovalRequest.status.approvedBy[]` (`+listType=set`) accumulates each valid grant's `spec.decidedBy`; the gate (`approval.go` `requiresAllOf`/`remainingApprovers`/`recordApprover`) holds the session in `AwaitingApproval` and emits `ApprovalPartiallyApproved` until that set covers every listed `approvers[].name`, then finalizes the grant. The approval `policyDecisions` allow-actor is the joined approver set. An `allOf` policy with no listed approvers degenerates to single-approver. Regenerated deepcopy + CRD. Envtest: `alice` then `bob` required before the Job is created (`approval_gate_test.go`).

**Honesty note (fail-closed):** approvers grant sequentially via the single `spec.decidedBy`, so two grants coalesced into one reconcile record only the latest grantor; the missed approver re-submits — the gate never opens early. A list-typed multi-grant spec + authenticated identity (webhook) is future work (design doc open question #3).

**Verification:** `make generate manifests` + `go build`; `go test ./internal/controller/agentsession/` (full envtest suite, pass 2026-06-21).

#### Task: Phase 5 · slice 7 — approval-decision audit records — **done (2026-06-21)**

**Shipped:** approval grants/denials now reach the OTLP audit sink (SIEM/forensics), not just `status.policyDecisions` + Kubernetes events. New `audit.EventApprovalGranted`/`EventApprovalDenied` (`approval.granted`/`approval.denied`) + `audit.ApprovalDecision` builder (`internal/audit/record.go`); `recordApprovalDecision` emits it once per decision (guarded by `hasApprovalDecision`), threading `ctx` through `denyForApproval`. Actor = approver (or joined `allOf` set), target = gated action, type = `approval`. Builder unit-tested (`internal/audit/sink_test.go`); observability + Phase 5 design docs updated. **Also fixed** a pre-existing at-most-once notification race (separate commit): `markApprovalNotified` read the just-created `ApprovalRequest` from cache (informer lag → NotFound → marker not persisted → duplicate notify); now falls back to the in-hand object.

**Verification:** `go build`; `go test ./internal/audit/`; `go test ./internal/controller/agentsession/` (full envtest suite, pass 2026-06-21).

### Task: RuntimeProfile sidecar injection — **done (2026-06-08)**

**Shipped:** `internal/controller/job/sidecars.go` — inject enabled known sidecars; `RELAY_TOOL_GATEWAY_URL` on agent; `RuntimeProfileDrift` includes sidecars; envtest coverage.

**Verification:** `make test` (pass 2026-06-08)

### Task: Tool gateway contract — **done (2026-06-08)**

**Shipped:** `internal/enforcement/toolgateway/` (`ToolRequest`, `EvaluateTool`, `RuntimeReport`, `GatewayConfig`, `Backend`); `docs/design/phase-3-tool-gateway-contract.md`; integration test via `ApplyRuntimePolicyReport`.

**Verification:** `make test` (pass 2026-06-08)

### Task: Runtime policy decision append — **done (2026-06-08)**

**Shipped:** `ApplyPolicyStatus` preserves runtime decisions on policy re-resolve; `AppendRuntimePolicyDecisions` / `ApplyRuntimePolicyReport` for reporters; `patchStatus` merges runtime decisions from stale/live snapshots; unit + envtest coverage.

**Verification:** `make test` (pass 2026-06-08)

### Task: Append runtime policy decisions from enforcement backends — **done (2026-06-08)**

Merged into slice 2 above. Reporters should call `AppendRuntimePolicyDecisions` or `ApplyRuntimePolicyReport`; reconciler preserves runtime via `ApplyPolicyStatus` + `patchStatus`.

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

### Task: Update README current-state section — **done (2026-06-08)**

**Shipped:** README [AgentSession controller reference](#agentsession-controller-reference), updated MVP behavior table, status fields, and “What the MVP does” list.

**Verification:** `make test` (pass 2026-06-08)

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

Relay has shipped an **end-to-end governance MVP** on Kubernetes: control-plane reconciliation, three data-plane enforcement domains (network / tool / file), runtime evidence into CRD status, and observability export (Prometheus, OTel traces, OTLP audit logs). **Not yet shipped:** operational UI, real approval gates, orchestrator adapters beyond Jobs, enterprise identity/credentials.

**Trust posture (read before extending):** data-plane enforcement and the runtime-evidence loop are **cooperative**, not adversarial-proof. Enforcement sidecars and the agent share a pod and ServiceAccount; the reporter authenticates the *pod* (TokenReview + pod→Job→session ownership) but cannot distinguish the agent container from a sidecar. A fully compromised agent could therefore tamper with or starve the data plane. To keep this honest, runtime evidence carries an `assuranceLevel` (`self-reported` for cooperative sidecar reports, stamped by the controller and not client-settable; `controller` for authoritative merge-time decisions; `observed` reserved for future independent sources). As least-privilege hardening, the agent pod runs with `automountServiceAccountToken: false` (no free apiserver token) and the projected `relay-reporter` token is mounted only into enforcement sidecars, never the agent. Adversarial-grade integrity still needs data-plane isolation (kernel/eBPF, separate identity/netns, or out-of-pod enforcement) — tracked under *Discovered Follow-Up Tasks → Runtime evidence integrity*. Do not describe current enforcement as tamper-proof in docs/UI.

**Repository audit (2026-06-16):** Verified the claims in this file against the tree.

| Check | Result |
|-------|--------|
| `go build ./...` / `go vet ./...` | Pass |
| `make test` (envtest, all packages) | Pass — controller `agentsession` 73.9%, others ≥61% |
| `make manifests generate` | No diff (CRD + RBAC in sync with markers) |
| Phase 4 done-claims (metrics/tracing/audit/outputs) | Verified wired in `cmd/main.go` + hooks; spot-checked behavior |
| `requireHumanApproval` | Confirmed warning-only (`reconciler.go` → `ApprovalNotEnforced`); no execution gate |

Gaps found during the audit (now tracked): Phase 5 had no task cards (decomposed below); observability export shipped with no design doc; runtime-evidence integrity is cooperative-only; `relay-design-docs.mdc` index was missing the timeline/observability rows (fixed).

| Area | State | Notes |
|------|-------|-------|
| **AgentSession CRD** | Done | Full spec/status including `usage`, `events`, `violations`, `artifacts` |
| **Policy CRDs** | Done | `AgentPolicy`, `ToolPolicy`, merge + watches + effective policy |
| **RuntimeProfile CRD** | Done | Hardening + sidecar injection (`dns-proxy`, `tool-gateway`, `fs-gateway`) |
| **Controller (kubernetes-job)** | Done | Lifecycle, cancellation, finalizers, NetworkPolicy baseline |
| **Policy enforcement (data plane)** | **MVP done (cooperative)** | Sidecar gateways + reporter → observed violations/decisions/usage; **not** tamper-proof vs a compromised agent (shared pod/SA) |
| **Runtime evidence loop** | Done | `POST /v1/report`, idempotent merge, live e2e (network/tool/file) |
| **Observability export** | Done | Prometheus `:8080/metrics`; OTLP traces + audit logs (opt-in flags) |
| **Log/artifact collection** | Done | Terminal sessions → owned ConfigMap (logs) / Secret (workspace tar); `status.artifacts` |
| **Unit / envtest** | Done | Controller suite; `make test` pass |
| **E2E tests** | Done | `make test-e2e` — live violation specs + usage assertions (incl. file domain) |
| **CI / dev environment** | Done | GitHub Actions; devcontainer + kind |
| **Operational UI** | Not started | Phase 7 |
| **Approval workflows** | Substantively done (Phase 5) | `ApprovalPolicy` + `ApprovalRequest` CRDs; controller gate enforces `requireHumanApproval` when a matching `ApprovalPolicy` exists (`AwaitingApproval` → grant/deny); approvers webhook-notified (`--approval-webhook-url`). Multi-approver/per-tool/approver-identity deferred |
| **Orchestrator adapters** | Interface + normalized observation | `kubernetes-job` backend behind `runtimeBackend`; reconciler owns status mapping; no second adapter yet (Phase 6) |
| **Enterprise platform** | Not started | Per-session identity, CredentialProfile, sandboxes; Phase 8 |

### What works today

- **Session lifecycle:** Create `AgentSession` → validate → Job → `Pending` → `Running` → terminal phases; cancel + finalizer cleanup
- **Policy:** `policyRefs` merge → `status.effectivePolicy` → env propagation; policy CRD watches; merge + runtime `policyDecisions`
- **Enforcement:** Enforced CIDR `NetworkPolicy`; **dns-proxy** egress; **tool-gateway** invokes; **fs-gateway** file access API
- **Observed governance:** Reporter populates `status.violations`, runtime decisions, `status.events`, `status.usage` (network/tool/file counters)
- **Live e2e:** Network, tool, and file violation + usage specs against kind (`make test-e2e-images`)
- **Observability:** `relay_*` Prometheus metrics; OpenTelemetry reconcile/reporter spans; OTLP audit records (`policy.violation`, `session.phase_change`, `runtime.report`)
- **Outputs:** When `spec.outputs.collectLogs` / `collectArtifacts` and session is terminal, controller retains agent pod logs (ConfigMap) and workspace tarball (Secret), refs in `status.artifacts`
- **Timeline model:** `internal/observability` projection over `status.events[]` (library for future UI)

### Known gaps (MVP vs schema / roadmap)

| Capability | In API/schema | Implemented |
|------------|---------------|-------------|
| `status.artifacts` | Yes | **Yes** — ConfigMap/Secret refs on terminal collection (512KiB caps; in-cluster only) |
| `status.usage` | Yes | Yes — runtime reports + token deltas |
| `status.violations` / runtime decisions | Yes | Yes — reporter + sidecars |
| `policy.requireHumanApproval` | Yes | Warning event only; does not block (Phase 5) |
| FQDN egress enforcement | Partial | DNS proxy domain policy; no Cilium/Envoy FQDN |
| FUSE / transparent file intercept | No | Explicit HTTP fs-gateway only |
| S3 / external artifact store | No | `configmap://` / `secret://` URIs only |
| Admission webhook | Scaffold | Controller validation only |
| Orchestrators beyond Job | Enum reserved | Validation rejects others |
| Runtime evidence integrity | Partial | `assuranceLevel` on decisions/violations (`controller` vs `self-reported`), now also on `policy.violation`/`runtime.report`/`approval.*` audit records (`relay.audit.assurance`); reporter token is sidecar-only + agent SA token automount disabled (least privilege); still cooperative — no anti-tamper / `observed` source yet (see Discovered task) |
| Observability export design doc | No | Prometheus/OTel/audit shipped without a `docs/design/` doc (see Discovered task) |

### status.podName selection semantics

Documented in `internal/controller/agentsession/pod.go` and API comments on `status.podName`:

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
| Pod watch for faster `podName` / Running | Done (2026-06-08): Pod watch + mapper in `internal/controller/agentsession/` |
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
| `runtimeProfileRef` | Same namespace | Optional `namespace` when added |
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

- **Phase 2 closed** — reusable policy model + RuntimeProfile complete; verification: 47 envtest + 12 e2e + verify-samples (2026-06-03)
- **RuntimeProfile docs/samples/e2e (Phase 2 · 5/6)** — README section, session sample, verify-samples, e2e runtime profile spec
- **RuntimeProfile watch (Phase 2 · 4/6)** — `Watches(RuntimeProfile)`; pending Job replace on profile pod-template drift; envtest
- **Apply RuntimeProfile to Job (Phase 2 · 3/6)** — merge container/pod security from profile; `status.matchedRuntimeProfile`; `RuntimeProfileResolved` + event; envtest
- **runtimeProfileRef + validation (Phase 2 · 2/6)** — `RuntimeProfileRef` on AgentSession; `validateSpec` + `resolveRuntimeProfile`; `InvalidRuntimeProfile` denial; RBAC for `runtimeprofiles`; envtest
- **RuntimeProfile CRD (Phase 2 · 1/6)** — `runtimeprofile_types.go`, container/pod hardening + declarative `sidecars[]`, CRD manifest, sample (`hardened-agent`); `make verify-samples`
- **README policy docs** — `AgentPolicy`/`ToolPolicy`, merge semantics, scoping, policy change / Job env behavior, MVP table
- **ToolPolicy CRD** — `toolpolicy_types.go`, merge via `LoadPolicyLayers`, watch, samples, envtest
- **Job env sync** — `PolicyPropagated` condition; replace pending Job on drift; `PolicyEnvDrift` when Job active (`job_policy.go`)
- **Policy decision records** — `PolicyDecision` API type, merge-time population, unit + envtest coverage
- **AgentPolicy watch** — `Watches(AgentPolicy)` maps to sessions with matching `spec.policyRefs`; envtest verifies `status.effectivePolicy` updates on policy change (`internal/controller/agentsession/policy_watch.go`)
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

- [x] **Envtest controller tests** — Reconciler unit tests in `internal/controller/agentsession/` + Job helpers in `internal/controller/job/` (validation, Job create, status transitions, condition stability)
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
- [x] **Operator docs** — README policy + RuntimeProfile sections, reference scoping, samples (`make verify-samples`)
- [x] **RuntimeProfile CRD** — Reusable hardening; `runtimeProfileRef`; Job pod template merge; watch; samples + e2e; `spec.sidecars` schema-only (Phase 3 injection)

**Phase 2 deferred / follow-up (tracked, not blocking Phase 3 planning):**

| Item | Where tracked | Notes |
|------|---------------|-------|
| ToolPolicy MCP **argument constraints** | **Done (2026-06-21)** — design + slices 2–4 (schema, gateway eval, live e2e) | `argumentRules` evaluated per-call with redacted evidence; e2e-verified |
| Inline `spec.policy.mode` override | Not planned | Only CRD modes merge today |
| Runtime `policyDecisions` append | **done** — slice 2 (`policy_decisions.go`) | Reporters use `AppendRuntimePolicyDecisions` |
| Active Job env stale after policy change | `PolicyEnvDrift` condition | Documented; immutable Job template |
| Mode **enforcement** (audit/dry-run/enforced behavior) | Phase 3 roadmap | Declared + propagated only |

**Phase 2 is complete** for control-plane policy and runtime profiles. Optional polish (argument constraints) stays in **Discovered Follow-Up Tasks**. Mode enforcement and sidecar injection are **Phase 3**.

---

### Phase 3 — Data-plane enforcement

Real governance beyond env var propagation. Start narrow, prove value, then expand.

**Planning outline:** [`docs/design/phase-3-enforcement-architecture.md`](../docs/design/phase-3-enforcement-architecture.md)

**Phase 3 principle:** the controller declares desired governance state; replaceable data-plane backends enforce and report runtime evidence. Keep each slice backend-neutral until a backend-specific task needs otherwise.

**Ordered implementation slices:**

1. [x] **Enforcement backend contract** — `internal/enforcement/` (`SessionContext`, `Backend`, mode semantics, `AppendRuntimeDecisions`); unit tests; aligns with architecture doc.
2. [x] **Runtime policy decision append** — `ApplyPolicyStatus`, `AppendRuntimePolicyDecisions`, `patchStatus` runtime merge; envtest preserve on policy re-resolve.
3. [x] **NetworkPolicy baseline** — `internal/enforcement/networkpolicy/` + reconciler; enforced CIDR egress; FQDN not covered.
4. [x] **Violation reporting MVP** — `AppendViolations`, `ApplyRuntimePolicyReport` derives `deny`/`dry-run` violations; `patchStatus` merge; README updated.
5. [x] **RuntimeProfile sidecar injection** — `job/sidecars.go`; enabled `dns-proxy`/`tool-gateway`/`envoy`; first-party images for dns-proxy + tool-gateway; envoy placeholder; drift detection.
6. [x] **Tool gateway contract** — `internal/enforcement/toolgateway/` + `docs/design/phase-3-tool-gateway-contract.md`; evaluate + report; first-party gateway image ships in Phase 3b #6.
7. [x] **DNS / egress proxy prototype** — `internal/enforcement/dnsproxy/`; sidecar env; `ApplyEgressProxyRuntimeEvent`; docs.
8. [x] **File/workspace policy design** — `docs/design/phase-3-file-workspace-policy.md`; mount + RuntimeProfile MVP.
9. [x] **File/workspace policy implementation** — path CRD fields + evaluate stub (2026-06-10).

**Phase 3 contract + design slices are complete.** Real enforcement and runtime evidence are **not** yet wired in-cluster — that is **Phase 3b** below, which is the critical path (not "optional hardening").

**Tracked but intentionally later:** Envoy, Cilium/eBPF, gVisor/Kata/Firecracker, multi-backend orchestration, approval gates, and UI timelines.

---

### Phase 3b — Runtime evidence loop (critical path)

Turn declared/propagated governance into **observed** governance. Until this ships, `status.policyDecisions`, `status.violations`, and `status.usage` are empty at runtime. Build this pipeline before the Phase 4 surfaces that consume it. Full cards in **Discovered Follow-Up Tasks**.

**Ordered slices:**

1. [x] **Runtime reporter mechanism design** — `docs/design/phase-3-runtime-reporter-contract.md`; decided: **controller-owned PATCH callback, no new CRD**.
2. [x] **Runtime reporter loop (impl)** — `internal/reporter/`; `POST /v1/report`; `PatchRuntimePolicyReport`; simulated-report handler tests.
3. [x] **Structured session events API** — `status.events[]`; reporter `events[]`; merge/idempotent append; design doc.
4. [x] **Reporter pod wiring** — projected token + Service + `RELAY_REPORTER_URL` for sidecars.
5. [x] **First-party dns-proxy image MVP** — `cmd/dns-proxy`, `Dockerfile.dns-proxy`, HTTP egress proxy + reporter client; integration test.
6. [x] **First-party tool-gateway image MVP** — `cmd/tool-gateway`, `Dockerfile.tool-gateway`, HTTP invoke API + reporter client; integration test.
7. [x] **Live network violation population** — dns-proxy enforced deny → reporter → `status.violations` (e2e).
8. [x] **File/workspace policy implementation** — `PolicyRules` path fields; `workspace.EvaluateFile`; env propagation; FS gateway image deferred.

---

### Phase 4 — Observability and audit

Backend surfaces for the future operational UI and enterprise audit requirements. **Depends on Phase 3b** — these consume the runtime evidence the reporter loop and events API produce.

- [x] **Usage metrics (control-plane)** — `status.usage` from runtime reports (novel network/tool decisions + optional `usage` delta on `POST /v1/report`)
- [x] **E2e usage metric assertions** — live `networkRequests` / `toolCalls` on existing violation specs *(slice A)*
- [x] **Session timeline model** — UI projection/normalization over `status.events[]` *(slice B)*
- [x] **Usage-only report idempotency** — `reportId` seen-cache for token-only reports *(slice C)*
- [x] **FS gateway sidecar MVP** — first-party file enforcement producer *(slice D)*
- [x] **File usage metrics** — `SessionUsage.fileOperations` from `type: file` decisions *(slice E)*
- [x] **Live file violation + usage e2e** — fs-gateway → reporter → status *(slice F)*
- [x] **Prometheus metrics** — sessions by phase, violations, approval queue proxy, reporter outcomes
- [x] **OpenTelemetry** — reconcile + reporter traces; W3C propagation for sidecar/agent continuity
- [x] **Audit log sink** — OTLP HTTP structured audit records
- [x] **Log / artifact collection** — `spec.outputs` → ConfigMap logs + Secret workspace tar; `status.artifacts` *(Phase 4 complete)*

> **Note:** *Structured session events API* moved to Phase 3b (it is the reporter's durable sink). *Session timeline model* and *Usage metrics* stay here but now follow the evidence loop.

**Phase 4 is complete** for the observability roadmap slice (no UI). Next product capabilities: Phase 5 (approvals) or Phase 7 (UI shell).

---

### Phase 5 — Human approval workflows

Scoped, auditable gates — not a boolean env var. Today `requireHumanApproval` only emits an `ApprovalNotEnforced` warning; this phase makes approval real. **Decomposed into ordered task cards** under *Discovered Follow-Up Tasks → Phase 5 approval workflows* (slice 1 = design doc, then ApprovalPolicy CRD, then ApprovalRequest + gate, then notifications).

- [x] **Approval model design doc** — CRD shape + gate/resume state machine *(slice 1 — `docs/design/phase-5-approval-workflows.md`)*
- [x] **ApprovalPolicy CRD** — Define what actions require approval *(slice 2, declarative only — `api/v1alpha1/approvalpolicy_types.go`)*
- [x] **ApprovalRequest CRD + controller gate** — Per-decision approval objects; block in `PhaseAwaitingApproval`, resume on grant *(slice 3 — `approvalrequest_types.go` + `approval.go`)*
- [x] **Approval audit trail** — Who approved, when, scope, expiry *(slice 3 — `ApprovalRequest.status` + `policyDecisions{type: approval}`)*
- [x] **Integration hooks** — generic webhook for approval notifications; Slack/PagerDuty are future adapters over the same `Notifier` *(slice 4 — `internal/approval/notifier.go` + `--approval-webhook-url`)*

---

### Phase 6 — Orchestrator adapters

Stay orchestrator-agnostic; add backends without coupling core reconciler to Jobs.

- [~] **Orchestrator interface** — `runtimeBackend` abstraction in controller. **Design doc done (2026-06-21)** (`docs/design/phase-6-orchestrator-interface.md`); **slices 2 + 2b done (2026-06-21):** interface + `backendRegistry` + `kubernetesJobBackend` extracted (`runtime_backend.go`); backend returns a normalized `observation` and the reconciler (`applyObservation`/`applyRuntimePhase`) owns all status mapping. Behavior-preserving. Remaining: a concrete second backend (Tekton/Argo) + optional status-field generalization.
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
