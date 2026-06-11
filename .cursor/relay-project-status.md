# Relay Project Status

> **What Relay has shipped, what is in progress, and where it is headed.**
> **Last updated:** 2026-06-10 (Phase 4 slice C done: usage-only reportId idempotency cache)
>
> For **how agents should implement tasks** (scope rules, templates, scans, updating this file), see [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

The **roadmap** below is long-term product intent, not a single backlog. **Ready for Cursor Queue** lists the next small implementation slices.

---

## Ready for Cursor Queue

Pick **one task card** per session unless the user asks for a design plan. Implementation rules: [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

> **Critical path:** Phase 3b **closed**. Phase 4 in progress — **reportId cache done**; next: **FS gateway sidecar MVP** (slice D).

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
| **D** | **FS gateway sidecar MVP** *(queue head)* | Unblocks file runtime evidence (violations + metrics). |
| **E** | **File usage metrics** | `SessionUsage` field + `type: file` decision increment; **depends on D** (or ship in same slice). |
| **F** | **Live file violation + usage e2e** | Mirror network/tool specs; **depends on D** (and E if asserting file counters). |

After A–F: Prometheus exporter, OTel, audit sink (Phase 4 roadmap bullets).

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

**Recently completed** (do not re-implement unless regressions): **Session timeline model**; **E2e usage metric assertions**; **Usage metrics (control-plane)**; **Phase 3b evidence loop**.

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

### Task: Investigate AgentSession reconcile churn (repeated PolicyResolved events + status conflicts)

**Discovered:** 2026-06-09 during the test-hardening e2e run. Controller logs show the same `PolicyResolved` / "Merged N referenced policies" event re-emitted many times on the *same* resourceVersion for a single session, plus occasional `update AgentSession status: conflict (will requeue)` errors. Suggests the reconciler re-records events and/or re-writes status when nothing changed, causing avoidable requeues.

**Why it matters:** Event spam and status write churn hurt observability signal, add apiserver load, and can mask real changes in the UI/timeline surfaces. Not a correctness bug (tests pass) but a reconcile-discipline gap.

**Scope (proposed):**
- Make policy-resolution event emission idempotent (only record on actual change / transition, e.g. dedupe by resolved hash or guard with a condition).
- Confirm status writes are no-ops when desired == observed (avoid spurious `Status().Update`).

**Non-goals:** New CRDs, changing the policy model, or reworking the reconcile architecture.

**Verification:** `make test`; manually confirm event count per session drops to ~1 per real transition (e2e log inspection).

**Files (likely):** `internal/controller/agentsession/reconciler.go`, `policy_decisions.go`, event-recording helpers.

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

### Task: First-party FS gateway sidecar MVP — Phase 4 · slice D

**Discovered:** 2026-06-10 after file/workspace policy API + evaluate stub (#8). Mirror dns-proxy/tool-gateway: sidecar type `fs-gateway`, path evaluation at runtime, `POST /v1/report` for deny events.

**Why it matters:**  
File policy is evaluate-only until a data-plane producer reports `type: file` evidence.

**Scope:**
- `cmd/fs-gateway` or in-pod hook; RuntimeProfile sidecar injection; integration test.
- Reporter client; `make docker-build-fs-gateway` / kind-load (mirror dns-proxy/tool-gateway).

**Non-goals:** FUSE production hardening in one slice.

**Acceptance criteria:** Integration test proves deny → reporter payload; sidecar replaces placeholder when enabled.

**Verification:** `make test` (+ image build smoke documented)

**Blocks:** slice E (file usage metrics), slice F (live file violation e2e).

### Task: File usage metrics — Phase 4 · slice E

**Discovered:** 2026-06-10 post-usage-metrics discussion. `SessionUsage` has no file counter; `incrementUsageForDecision` ignores `type: file`.

**Why it matters:**  
Parity with network/tool usage once FS gateway reports file access.

**Scope:**
- Add `SessionUsage` field (e.g. `fileOperations` or `fileAccesses` — pick one in implementation).
- Increment from novel `type: file` runtime decisions; optional explicit delta on `usage` wire field.
- Unit tests; merge/idempotency same as network/tool.

**Non-goals:** FS gateway image (slice D). Prometheus.

**Depends on:** slice D (or implement API increment in same PR as gateway).

**Acceptance criteria:** `make manifests && make test` passes.

**Verification:** `make manifests && make test`

### Task: Live file violation and usage e2e — Phase 4 · slice F

**Discovered:** 2026-06-10. Network and tool have live violation e2e; file has unit tests only.

**Why it matters:**  
End-to-end proof for the third governance domain (path policy).

**Scope:**
- E2e spec: enforced `deniedPaths` + fs-gateway sidecar + file access probe → `status.violations` + runtime decisions.
- Assert file usage counter if slice E shipped.

**Non-goals:** Full MCP / FUSE stack.

**Depends on:** slice D (required); slice E (for usage assertions).

**Acceptance criteria:** `make test-e2e-images && make test-e2e` (fs-gateway image in prereq target).

**Verification:** `make test-e2e`

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

Relay is in **early MVP / vertical-slice** stage. The core control-plane loop works end-to-end on a local kind cluster, but most governance is **declared and propagated**, not **enforced**.

| Area | State | Notes |
|------|-------|-------|
| **AgentSession CRD** | Done | `relay.secureai.dev/v1alpha1`, spec/status + `policyRefs` |
| **AgentPolicy CRD** | Done | Reusable rules + `mode`; `spec.policyRefs`; watch → re-reconcile |
| **ToolPolicy CRD** | Done | Tool/MCP rules; merge + watch; `maxCallsPerMinute` propagated to effective policy + env |
| **Controller (kubernetes-job)** | Done | Reconciles to `batch/v1` Job, lifecycle phases, conditions, events |
| **Policy propagation** | Done | Merge `policyRefs` + inline → `status.effectivePolicy` → `AGENT_POLICY_*` env |
| **Policy enforcement** | Not started | Env vars are hooks only; no network/tool/file gates |
| **Dev environment** | Done | Devcontainer + kind (`relay-dev`) + bootstrap scripts |
| **E2E tests** | Done | `make test-e2e` — **12** specs against live kind cluster |
| **Unit / envtest** | Done | Controller suite — **47** envtest specs; ~**78%** coverage |
| **CI** | Done | `.github/workflows/test.yaml`, `e2e.yaml`, `lint.yaml` |
| **In-cluster deploy** | Ready | `make dev-deploy` builds image + deploys manager |
| **RuntimeProfile CRD** | Done | CRD + `runtimeProfileRef` + Job apply + watch + README/samples/e2e |
| **Additional CRDs (Phase 2)** | **Done** | `AgentPolicy`, `ToolPolicy`, `RuntimeProfile` — control-plane complete |
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
- `RuntimeProfile` + `spec.runtimeProfileRef` — merge profile into Job pod template; `status.matchedRuntimeProfile`; `RuntimeProfileResolved` condition; watch + pending Job replace on profile drift
- Sample manifests (success, failing, policy/toolpolicy/runtimeprofile refs) and README documentation

### Known gaps (MVP vs schema)

| Capability | In API/schema | Implemented in controller |
|------------|---------------|---------------------------|
| `task.promptConfigMapRef` | Yes | Done — loads key from same-namespace ConfigMap |
| `status.usage` | Yes | Yes — runtime reports (network/tool decision counts + optional token deltas) |
| `status.podName` | Yes | Done — labeled session Pods, current Job UID, newest `CreationTimestamp` (name tie-break); see `internal/controller/agentsession/pod.go` |
| `status.violations` | Yes | Yes — via `ApplyRuntimePolicyReport` (`deny` / `dry-run` outcomes) |
| `status.artifacts` | Yes | No — `outputs.collectArtifacts` not implemented |
| `spec.policyRefs` / `AgentPolicy` / `ToolPolicy` | Yes | Done — same-namespace refs; merge order refs → inline; missing ref → `InvalidPolicy` |
| `spec.runtimeProfileRef` | Yes | Done — profile merges into Job container/pod spec; `matchedRuntimeProfile`; `RuntimeProfileResolved` |
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
| ToolPolicy MCP **argument constraints** | Discovered: *ToolPolicy MCP argument constraints* | Roadmap mentioned; out of initial ToolPolicy slice |
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
- [ ] **FS gateway sidecar MVP** — first-party file enforcement producer *(slice D — queue head)*
- [ ] **File usage metrics** — `SessionUsage` file counter from `type: file` decisions *(slice E; depends D)*
- [ ] **Live file violation + usage e2e** — fs-gateway → reporter → status *(slice F; depends D,E)*
- [ ] **Prometheus metrics** — Sessions by phase, violations, approval queue depth, reconcile latency
- [ ] **OpenTelemetry** — Traces for reconcile loop + optional agent runtime traces
- [ ] **Audit log sink** — Export to OTLP, S3, or SIEM-compatible format
- [ ] **Log / artifact collection** — Implement `outputs.collectLogs` / `collectArtifacts`

> **Note:** *Structured session events API* moved to Phase 3b (it is the reporter's durable sink). *Session timeline model* and *Usage metrics* stay here but now follow the evidence loop.

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
