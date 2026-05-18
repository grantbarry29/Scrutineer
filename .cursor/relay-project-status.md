# Relay Project Status

> Living tracker for operational state and roadmap progress.
> **Last updated:** 2026-05-18 (status.podName)
>
> Update this file when completing roadmap items, changing priorities, or shipping meaningful milestones.

The **roadmap below is long-term product intent**. It is not a single implementation backlog. Use the sections that follow to scope Cursor-assisted work into small, testable tasks.

---

## How To Work On Roadmap Items With Cursor

Roadmap checkboxes describe **capabilities**, not one PR or one prompt. Before coding, decompose a roadmap item into narrow tasks, each with an acceptance criterion and a single verification command.

**Avoid prompts like:**

> Implement session cancellation, finalizers, CI, AgentPolicy, and NetworkPolicy baseline.

**Prefer a sequence like:**

1. Add `spec.cancelRequested` (or equivalent) and kubebuilder validation markers only → `make manifests && make test`
2. Update reconciler to detect cancel and delete the owned Job → `make test`
3. Set `status.phase=Cancelled` and conditions on cancel → `make test`
4. Add e2e spec for cancellation → `make test-e2e`
5. Add finalizer on `AgentSession` that deletes owned Job before remove → `make test`
6. Add GitHub Actions workflow running `make test` only
7. Add a separate GitHub Actions job for kind + `make test-e2e`

Same pattern for other phases: API shape first, then reconciler behavior, then tests, then CI—never multiple phases at once unless the user explicitly requests a design pass first.

---

## Ready for Cursor Queue

Pick **one task card** per implementation session unless the user explicitly asks for a design plan. These tasks are implementation-sized; broader capabilities remain in the roadmap below.

Before implementing a selected task, Cursor must first restate:
- selected task
- expected files
- non-goals
- verification command

If the task appears to require more than **4 non-generated files**, Cursor must wait for confirmation before editing.

### Task: Session cancellation API shape

**Goal:**  
Expose the smallest user-facing mechanism for requesting cancellation.

**Scope:**
- Choose one cancellation request shape: a spec field or annotation convention.
- If using a spec field, add kubebuilder validation/defaulting markers as needed.
- Regenerate CRD YAML if API markers change.
- Document the field/comment in the API type.

**Non-goals:**
- Do not delete Jobs.
- Do not set `PhaseCancelled`.
- Do not emit cancellation events.
- Do not add finalizers.
- Do not add approval workflows or new CRDs.

**Acceptance criteria:**
- The CRD exposes a documented cancellation request mechanism.
- Generated CRD YAML includes the new field/marker if the API type changed.
- Existing tests still pass.

**Expected files:**
- `api/v1alpha1/agentsession_types.go`
- `config/crd/bases/relay.secureai.dev_agentsessions.yaml` (generated, only if API type changes)
- `config/samples/*.yaml` only if sample documentation is necessary

**Verification command:**  
`make manifests && make test`

### Task: Session cancellation controller behavior

**Goal:**  
When cancellation is requested, the controller stops the owned Kubernetes Job.

**Scope:**
- Detect the existing cancellation request mechanism.
- Find the owned Job.
- Delete the owned Job if it exists.
- Treat missing Job as already stopped.

**Non-goals:**
- Do not design the cancellation API shape.
- Do not add finalizers.
- Do not add new CRDs.
- Do not implement approvals or policy changes.
- Do not add e2e coverage in this task.

**Acceptance criteria:**
- A cancelled AgentSession causes its owned Job to be deleted.
- Reconcile is idempotent when the Job is already gone.
- Envtest covers Job deletion and missing-Job behavior.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: Session cancellation status/events

**Goal:**  
Cancellation is visible through `status.phase`, conditions, and Kubernetes Events.

**Scope:**
- Set `status.phase=Cancelled` after cancellation is observed/processed.
- Add or update a `Completed` condition for cancellation.
- Emit a cancellation event.
- Ensure `Cancelled` is terminal.

**Non-goals:**
- Do not choose a new cancellation API shape.
- Do not add finalizers.
- Do not add e2e coverage in this task.
- Do not add approval workflows or new CRDs.

**Acceptance criteria:**
- Cancelled AgentSession reaches terminal `Cancelled` phase.
- Status condition and event clearly explain cancellation.
- Envtest verifies phase, condition, and event-related behavior where practical.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `internal/controller/constants.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: Session cancellation envtest coverage

**Goal:**  
Unit/envtest coverage proves cancellation behavior without requiring kind.

**Scope:**
- Add focused envtest specs for the cancellation API/controller behavior already implemented.
- Cover idempotency when the owned Job is missing.
- Cover terminal status after cancellation.

**Non-goals:**
- Do not change API shape unless tests expose a bug.
- Do not change reconciler behavior unless tests expose a bug.
- Do not add e2e coverage.

**Acceptance criteria:**
- Envtest fails before the implemented cancellation behavior and passes after it.
- `make test` covers Job deletion, missing Job, and terminal `Cancelled` status.

**Expected files:**
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: Session cancellation e2e coverage

**Goal:**  
Prove cancellation works against a real kind cluster.

**Scope:**
- Add a kind e2e test that creates an AgentSession, requests cancellation, and observes terminal cancellation.
- Verify the owned Job is deleted or no longer present.

**Non-goals:**
- Do not change API shape unless the e2e exposes a bug.
- Do not change reconciler behavior unless the e2e exposes a bug.
- Do not add finalizers or approval workflows.

**Acceptance criteria:**
- `make test-e2e` proves cancellation end-to-end.
- The e2e test verifies `PhaseCancelled` and owned Job cleanup.

**Expected files:**
- `test/e2e/agentsession_test.go`
- `test/e2e/helpers_test.go` only if a helper is needed

**Verification command:**  
`make test-e2e`

### Task: Session cancellation docs/status update

**Goal:**  
Document the cancellation behavior once implementation and tests exist.

**Scope:**
- Update user-facing docs or samples only if cancellation is implemented.
- Update this status file to mark cancellation subtasks complete.

**Non-goals:**
- Do not implement cancellation behavior.
- Do not add new API fields.
- Do not add finalizers.

**Acceptance criteria:**
- Documentation accurately describes how to request cancellation.
- `.cursor/relay-project-status.md` reflects completed cancellation subtasks.

**Expected files:**
- `README.md`
- `.cursor/relay-project-status.md`
- `config/samples/*.yaml` only if a cancellation sample is useful

**Verification command:**  
`make test`

### Task: Add finalizer constant and attach finalizer

**Goal:**  
New AgentSessions receive a Relay finalizer so cleanup can run before deletion.

**Scope:**
- Add a finalizer constant.
- Add the finalizer to non-deleting AgentSessions.
- Update RBAC markers if needed for finalizers.
- Add envtest coverage that a created AgentSession receives the finalizer.

**Non-goals:**
- Do not delete Jobs on finalization.
- Do not remove the finalizer.
- Do not implement cancellation.
- Do not add new CRDs.

**Acceptance criteria:**
- Reconciled AgentSessions have the Relay finalizer.
- Reconcile remains idempotent if the finalizer already exists.

**Expected files:**
- `internal/controller/constants.go`
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`
- `config/rbac/role.yaml` (generated, only if RBAC markers change)

**Verification command:**  
`make manifests && make test`

### Task: Cleanup owned Job on deletion

**Goal:**  
When an AgentSession is deleting, the controller deletes the owned Job before finalizer removal.

**Scope:**
- Detect `DeletionTimestamp`.
- Find the deterministic owned Job.
- Delete the Job if present.
- Treat missing Job as cleanup complete.

**Non-goals:**
- Do not add the finalizer constant/attach behavior if not already present.
- Do not remove the finalizer in this task unless cleanup is already complete by existing code.
- Do not implement cancellation.
- Do not change policy behavior.

**Acceptance criteria:**
- Deleting an AgentSession causes the owned Job to be deleted.
- Missing Job does not block cleanup.
- Envtest covers delete path behavior.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: Remove finalizer after cleanup

**Goal:**  
AgentSession deletion completes after owned Job cleanup succeeds.

**Scope:**
- Remove the Relay finalizer after cleanup is complete.
- Preserve finalizer if cleanup returns an error.
- Keep reconcile idempotent across repeated deletion reconciles.

**Non-goals:**
- Do not implement cancellation.
- Do not add new cleanup targets beyond the owned Job.
- Do not add new CRDs or policy behavior.

**Acceptance criteria:**
- AgentSession is removed after cleanup completes.
- Cleanup failures do not remove the finalizer.
- Envtest covers finalizer removal.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: Envtest delete-path coverage

**Goal:**  
Prove finalizer cleanup behavior in envtest.

**Scope:**
- Add focused tests for delete flow after finalizer implementation.
- Cover Job exists, Job missing, and finalizer removal.

**Non-goals:**
- Do not implement finalizer behavior unless tests expose a bug.
- Do not add e2e coverage.
- Do not implement cancellation.

**Acceptance criteria:**
- `make test` proves AgentSession deletion cleans up owned Jobs before CR removal.

**Expected files:**
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: GitHub Actions unit/envtest workflow

**Goal:**  
Pull requests run the normal unit/envtest suite.

**Scope:**
- Add a GitHub Actions workflow for `make test`.
- Configure Go version matching the repo.
- Cache Go modules if simple.

**Non-goals:**
- Do not add kind e2e in this task.
- Do not add lint-only workflow unless required by `make test`.
- Do not add release, image publish, or deployment automation.

**Acceptance criteria:**
- PR workflow runs `make test`.
- Local `make test` passes.

**Expected files:**
- `.github/workflows/test.yaml`

**Verification command:**  
`make test`

### Task: GitHub Actions e2e kind workflow

**Goal:**  
CI can run the kind-backed e2e suite separately from unit tests.

**Scope:**
- Add a separate workflow or job that creates kind and runs `make test-e2e`.
- Install required tools or use existing Makefile targets.
- Keep it independent from release/publish automation.

**Non-goals:**
- Do not combine with image publishing.
- Do not add deployment/release workflows.
- Do not change e2e test behavior unless required for CI reliability.

**Acceptance criteria:**
- Workflow includes kind setup and runs `make test-e2e`.
- Local `make test-e2e` still passes.

**Expected files:**
- `.github/workflows/e2e.yaml` or `.github/workflows/test.yaml`

**Verification command:**  
`make test-e2e`

### Task: Optional GitHub Actions lint workflow

**Goal:**  
Add a separate lightweight CI check for formatting/vet/lint if needed.

**Scope:**
- Add or extend CI to run `make fmt`/`make vet` equivalent only if not already covered.
- Keep lint checks separate from e2e.

**Non-goals:**
- Do not add kind e2e.
- Do not add image publishing.
- Do not introduce a new linter unless explicitly selected.

**Acceptance criteria:**
- CI has a clear lint/format check, or the status file records that `make test` already covers it.

**Expected files:**
- `.github/workflows/lint.yaml` or existing workflow file

**Verification command:**  
`make test`

**Recently completed** (do not re-implement unless regressions): envtest controller suite, `promptConfigMapRef`, status patch strategy (live read + condition union + `Status().Update`), **`status.podName`** (newest Job-owned Pod by creation timestamp; envtest + e2e).

---

## Task Sizing Rules

- A good Cursor task usually touches **1–4 files** (plus generated CRD YAML when API markers change).
- Every task should have a **clear acceptance criterion** (one sentence, testable).
- Every task should be verifiable with **one primary command** (e.g. `make test`, `make test-e2e`, or `kubectl apply` + assert).
- **Avoid** tasks that span multiple roadmap phases (e.g. Phase 1 hardening + Phase 3 NetworkPolicy in one pass).
- **Avoid** inventing new architecture (new CRDs, sidecars, enforcement backends) unless the prompt explicitly asks for design first.
- If a task needs more than one subsystem (API + controller + CI + docs), **propose a short plan** and wait for confirmation instead of coding immediately.

---

## Task Execution Template

Every implementation task should be written in this format before code changes begin:

### Task: `<short name>`

**Goal:**  
One sentence describing the user-visible capability.

**Scope:**  
What should be implemented.

**Non-goals:**  
What must not be implemented as part of this task.

**Acceptance criteria:**  
- Specific observable result
- Specific status/behavior/result
- Specific test expectation

**Expected files:**  
- likely files to change

**Verification command:**  
`make test`, `make test-e2e`, or another single primary command.

### Example: Populate `status.podName` reliably

**Goal:**  
AgentSession status should show the Pod created by the owned Job.

**Scope:**  
Find Pods owned by the AgentSession Job and set `status.podName` once a Pod exists.

**Non-goals:**  
Do not implement cancellation, finalizers, new CRDs, policy enforcement, or UI.

**Acceptance criteria:**  
- When a Job creates a Pod, `status.podName` is populated.
- Reconcile succeeds if no Pod exists yet.
- If multiple Pods exist, controller chooses the newest Pod by creation timestamp.
- Envtest or e2e coverage verifies this.

**Expected files:**  
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go` or `test/e2e/agentsession_test.go`

**Verification command:**  
`make test`

---

## Scope Boundaries

Unless the user explicitly asks for design work or selects a task that requires it, do not:

- add new CRDs
- add a UI
- add Envoy
- add Cilium/eBPF
- add NetworkPolicy generation
- add gVisor/Kata/Firecracker
- add a tool gateway
- add real policy enforcement
- add approval workflows
- add multi-cluster support
- refactor the project structure
- replace Kubernetes Job reconciliation
- introduce a new orchestrator adapter

When a future feature seems relevant, add a TODO or a status-file task instead of implementing it.

---

## Explain As You Go

For each implementation task, Cursor should concisely explain:

- why the change is needed
- what Kubernetes/controller-runtime concept is involved
- what invariant the code must preserve
- how the test proves the behavior

Keep explanations short and educational. Prefer concrete invariants over vague guidance.

---

## Status File Self-Update Rules

After completing a task, Cursor must update this file.

- If a **Current Recommended Next Task** is completed, move it to **Recently completed** or mark it done in the roadmap.
- If it corresponds to a roadmap checkbox, update the roadmap checkbox.
- Add a **Recent fixes** bullet for behavior changes, bug fixes, or user-visible improvements.
- Update **What works today** when a new capability is available.
- Update **Current Operational State** if a whole area changes.
- Update **Known gaps** if a gap is closed or newly discovered.
- Update the **Last updated** date.
- If new work is discovered, add it as a new task card with scope, non-goals, acceptance criterion, and verification command.
- Do not remove long-term roadmap items just because they are not being worked on now.
- Do not mark tasks complete unless tests pass or the user explicitly accepts incomplete work.

---

## Current Operational State

Relay is in **early MVP / vertical-slice** stage. The core control-plane loop works end-to-end on a local kind cluster, but most governance is **declared and propagated**, not **enforced**.

| Area | State | Notes |
|------|-------|-------|
| **AgentSession CRD** | Done | `relay.secureai.dev/v1alpha1`, full spec/status schema |
| **Controller (kubernetes-job)** | Done | Reconciles to `batch/v1` Job, lifecycle phases, conditions, events |
| **Policy propagation** | Done | Inline policy → env vars in agent container |
| **Policy enforcement** | Not started | Env vars are hooks only; no network/tool/file gates |
| **Dev environment** | Done | Devcontainer + kind (`relay-dev`) + bootstrap scripts |
| **E2E tests** | Done | `make test-e2e` — 8 specs against live kind cluster |
| **Unit / envtest** | Done | Controller suite with validation + reconciler specs (~65% coverage) |
| **CI** | Not started | No `.github/workflows` |
| **In-cluster deploy** | Ready | `make dev-deploy` builds image + deploys manager |
| **Additional CRDs** | Not started | AgentPolicy, ToolPolicy, ApprovalPolicy, etc. |
| **Operational UI** | Not started | Vision documented in product rule |
| **Audit / observability backend** | Not started | Status fields exist; not populated by sidecars yet |

### What works today

- Create `AgentSession` → controller validates → creates owned Job → tracks `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied`
- CRD admission rejects invalid `temperature` (string + Pattern)
- Controller validation denies bad specs (e.g. empty task) without creating a Job
- `task.promptConfigMapRef` loads prompt from ConfigMap into `AGENT_TASK_PROMPT`
- Policy fields injected as `AGENT_POLICY_*` / `RELAY_*` env vars
- Workspace emptyDir mount, resource limits, timeout, basic container hardening
- Kubernetes Events on validation, Job create, running, success, failure
- `status.podName` set to the newest Pod owned by the session's Job (when a Pod exists)
- Sample manifests (success + failing) and README documentation

### Known gaps (MVP vs schema)

| Capability | In API/schema | Implemented in controller |
|------------|---------------|---------------------------|
| `task.promptConfigMapRef` | Yes | Done — loads key from same-namespace ConfigMap |
| `status.usage` | Yes | No — reserved for future sidecar/audit |
| `status.podName` | Yes | Done — newest Job-owned Pod by creation timestamp |
| `status.violations` | Yes | No — no enforcement backend yet |
| `status.artifacts` | Yes | No — `outputs.collectArtifacts` not implemented |
| `policy.requireHumanApproval` | Yes | Surfaced only; does not block execution |
| `PhaseCancelled` | Yes | Terminal phase exists; no cancel flow |
| Orchestrators beyond `kubernetes-job` | Enum reserved | Rejected at validation |
| PVC-backed workspace | Commented future | emptyDir only |
| Webhook validation | Generated scaffold | Not wired |

### Recent fixes

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

Phases are ordered by product maturity. **Implement incrementally** using the decomposition guidance above—not as a single effort.

---

### Phase 0 — MVP foundation (mostly complete)

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
- [ ] **Session cancellation** — `PhaseCancelled` via spec change, annotation, or delete policy; delete/stop underlying Job
- [ ] **Finalizers** — Graceful cleanup of Jobs on AgentSession delete
- [ ] **CI pipeline** — GitHub Actions: `make test`, `make test-e2e` (kind), lint, build image
- [ ] **Admission webhook** (optional) — Move duplicate validation to validating webhook for earlier rejection
- [ ] **Helm chart or improved kustomize overlays** — Easier install than raw kustomize for early adopters

---

### Phase 2 — Reusable policy model

Extract inline policy into composable, versioned CRDs without breaking AgentSession.

- [ ] **AgentPolicy CRD** — Reusable network/tool/file/approval rules; reference from AgentSession
- [ ] **Policy composition** — Merge order: AgentPolicy → session inline overrides; record matched policies in status
- [ ] **Policy modes** — `audit-only`, `dry-run`, `enforced` (declared vs enforced distinction)
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

## How to update this file

Use **Status File Self-Update Rules** above as the authoritative update workflow.

At minimum:

1. Move the task from `[ ]` or `[~]` to `[x]` only after tests pass or the user accepts incomplete work.
2. Add a **Recent fixes** line for behavior changes, bug fixes, or user-visible improvements.
3. Update **What works today**, **Known gaps**, and **Current Operational State** when those sections change.
4. Update the **Last updated** date at the top.
5. Add newly discovered work as a scoped task card with scope, non-goals, acceptance criterion, and verification command.
