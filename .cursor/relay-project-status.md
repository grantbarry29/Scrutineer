# Relay Project Status

> Living tracker for operational state and roadmap progress.
> **Last updated:** 2026-05-17 (cancellation e2e)
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

## Out-of-Scope Future Work Handling

When implementing any task, Cursor must distinguish between:

1. **Current task requirements** — work required to satisfy the selected task’s acceptance criteria.
2. **Necessary supporting changes** — small changes required for the selected task to compile, test, or remain consistent.
3. **Future requirements** — important eventual work that is related but not required for the selected task.

Cursor must **not** implement future requirements unless the user explicitly asks.

Instead, Cursor must:

1. Notice the future requirement.
2. Check this file (**Ready for Cursor Queue**, **Discovered Follow-Up Tasks**, roadmap phases).
3. If already tracked, mention it briefly in the task summary.
4. If not tracked, add a scoped task card (use the **Task Execution Template** below) or a roadmap bullet in the appropriate phase.
5. Continue implementing only the original selected task.

Each new task card should include: task name, why it matters, scope, non-goals, acceptance criteria, expected files, and a single verification command.

### End-of-task summary requirement

After every implementation task, Cursor must include:

### Out-of-scope future work noticed

- `None.` — if nothing relevant was noticed.

Or bullets such as:

- **Finalizer-based cleanup** is needed for deletion reliability.
  - **Already tracked:** yes — Ready for Cursor Queue / Finalizers tasks.
  - **Not implemented** because the selected task only covered cancellation Job delete.

### Examples

| While implementing… | Do not… | Instead… |
|---------------------|---------|----------|
| `status.podName` | Redesign Job retry/backoff | Add task if retry semantics are not tracked |
| Cancellation | Add finalizers | Reference or add finalizer tasks |
| Policy env vars | Add Envoy/Cilium/NetworkPolicy | Reference Phase 3 enforcement tasks |
| Events | Build audit backend / UI | Reference Phase 4 structured events |
| `promptConfigMapRef` | Add cross-namespace refs | Add reference-scoping task |
| ServiceAccount fields | Redesign identity | Reference Phase 8 CredentialProfile / per-session identity |

---

## Repository State Scan Rule

Cursor should scan the current repository state when:

- the user asks to tighten project rules or what to work on next
- a task is completed
- a new subsystem is introduced
- the status file may be stale

The scan should compare:

- actual CRD/API fields (`api/v1alpha1/`, `config/crd/bases/`)
- controller behavior (`internal/controller/`)
- tests (`internal/controller/*_test.go`, `test/e2e/`)
- sample manifests (`config/samples/`)
- README and docs
- Makefile targets
- CI files (`.github/workflows/`)
- RBAC (`config/rbac/`, kubebuilder RBAC markers)
- generated manifests vs Go API markers
- TODO / future comments in code
- claims in this status file

Look for mismatches such as:

- status says done but code does not implement it
- code implements behavior not documented in status
- API fields without controller behavior (or without “future-only” documentation)
- controller behavior without tests
- tests without status mentions
- README commands that do not match Makefile targets
- RBAC missing for resources the controller uses
- stale samples relative to the CRD
- status fields never populated
- roadmap items too broad to implement in one session

**During a scan, do not implement fixes unless explicitly asked.** Update this file (and rules) only.

Promote items from **Discovered Follow-Up Tasks** into **Ready for Cursor Queue** when they are the next logical slice of work.

---

## Ready for Cursor Queue

Pick **one task card** per implementation session unless the user explicitly asks for a design plan. These tasks are implementation-sized; broader capabilities remain in the roadmap below.

Before implementing a selected task, Cursor must first restate:
- selected task
- expected files
- non-goals
- verification command

If the task appears to require more than **4 non-generated files**, Cursor must wait for confirmation before editing.

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

**Recently completed** (do not re-implement unless regressions): envtest controller suite, `promptConfigMapRef`, status patch strategy, **`status.podName`**, **`spec.cancelRequested`** + cancellation Job delete + **`PhaseCancelled`** status/events, **cancellation e2e** (2 specs).

---

## Discovered Follow-Up Tasks

Scoped tasks found by repository audit or implementation work. **Not in the active queue** until promoted. Pick one at a time into **Ready for Cursor Queue** when appropriate.

### Task: Terminal phase stability and Job immutability

**Why it matters:**  
`ensureJob` runs before terminal checks; a terminal session whose Job was removed (TTL, manual delete, or cancel without phase update) can get a new Job on a later reconcile.

**Scope:**
- Short-circuit `ensureJob` (and runtime creation) when `status.phase` is already terminal (`Succeeded`, `Failed`, `Denied`, `TimedOut`, `Cancelled`).
- Ensure `syncStatusFromJob` does not regress terminal phases (e.g. `Succeeded` → `Starting`).
- Add envtest for terminal session + missing Job → no new Job created.

**Non-goals:**
- Do not implement finalizers or AgentSession deletion cleanup.
- Do not change cancellation API shape.
- Do not add new CRDs.

**Acceptance criteria:**
- Terminal AgentSession with no owned Job does not create a replacement Job on reconcile.
- Terminal phase is not overwritten by a non-terminal phase without an explicit spec/status design change.
- Envtest proves both behaviors.

**Expected files:**
- `internal/controller/agentsession_controller.go`
- `internal/controller/agentsession_controller_test.go`

**Verification command:**  
`make test`

### Task: Define pod selection semantics for retried Jobs

**Why it matters:**  
MVP uses `backoffLimit: 0`, but `status.podName` selects the newest Job-owned Pod; if backoff/retries change, selection rules should be explicit and tested.

**Scope:**
- Document expected behavior when multiple Pods exist (retries, Job recreates).
- Align `findPodName` / `newestPodOwnedByJob` with documented semantics.
- Add unit tests for multi-Pod edge cases beyond the current newest-timestamp rule.

**Non-goals:**
- Do not change Job backoff defaults unless explicitly requested.
- Do not implement Pod watch in this task.

**Acceptance criteria:**
- Comments or status-file note define pod selection for retry scenarios.
- Tests cover at least two Pods with different creation timestamps and non-owned Pods ignored.

**Expected files:**
- `internal/controller/pod.go`
- `internal/controller/pod_test.go`
- `.cursor/relay-project-status.md` (behavior note, if needed)

**Verification command:**  
`make test`

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

### Task: Validate sample manifests against current CRD

**Why it matters:**  
Samples are hand-maintained; drift from generated CRD fields breaks copy-paste onboarding.

**Scope:**
- Verify `config/samples/*.yaml` apply cleanly after `make install` on kind.
- Fix invalid fields, document `cancelRequested` sample once cancellation is complete.
- Optionally add a Makefile target or script that dry-runs `kubectl apply --dry-run=server` on samples.

**Non-goals:**
- Do not change CRD schema unless samples expose a real bug.
- Do not add Helm.

**Acceptance criteria:**
- All samples pass server-side dry-run (or apply) against the installed CRD.
- README sample instructions match the validated manifests.

**Expected files:**
- `config/samples/*.yaml`
- `Makefile` or `hack/` script (only if a verify target is added)
- `README.md`

**Verification command:**  
`make install` (on kind) and sample apply/dry-run

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

### Task: Add e2e for TimedOut session

**Why it matters:**  
Controller maps `activeDeadlineSeconds` to `PhaseTimedOut`, but e2e only covers Succeeded, Failed, and Denied paths.

**Scope:**
- Add kind e2e with a short `timeoutSeconds` and a sleep command that exceeds it.
- Assert `status.phase=TimedOut` and terminal condition.

**Non-goals:**
- Do not change timeout logic unless the e2e exposes a bug.
- Do not add cancellation or finalizer coverage in this task.

**Acceptance criteria:**
- `make test-e2e` includes a TimedOut spec that passes reliably on kind.

**Expected files:**
- `test/e2e/agentsession_test.go`
- `test/e2e/helpers_test.go` (only if needed)

**Verification command:**  
`make test-e2e`

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
- `internal/controller/constants.go` (comments only, if helpful)

**Verification command:**  
`make test` (no behavior change; docs-only)

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

When a future feature seems relevant:

1. Check whether it is already tracked in this file.
2. If not, add a scoped task card (or roadmap bullet) — see **Out-of-Scope Future Work Handling**.
3. Do not implement it in the current task.

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

- If a **Ready for Cursor Queue** task is completed, remove it or move a summary to **Recently completed**; update the roadmap checkbox if applicable.
- Add a **Recent fixes** bullet for behavior changes, bug fixes, or user-visible improvements.
- Update **What works today** when a new capability is available.
- Update **Current Operational State** if a whole area changes.
- Update **Known gaps** if a gap is closed or newly discovered.
- Update the **Last updated** date.
- If new work is discovered during implementation, follow **Out-of-Scope Future Work Handling** (add to **Discovered Follow-Up Tasks** or queue; do not implement unless asked).
- Include **### Out-of-scope future work noticed** in the implementation summary to the user.
- Do not remove long-term roadmap items just because they are not being worked on now.
- Do not mark tasks complete unless tests pass or the user explicitly accepts incomplete work.
- After completing a task, consider a lightweight **Repository State Scan** (see above) if the change touched API, reconcile logic, RBAC, or tests.

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
| **E2E tests** | Done | `make test-e2e` — 10 specs against live kind cluster |
| **Unit / envtest** | Done | Controller suite with validation + reconciler specs (~65% coverage) |
| **CI** | Not started | No `.github/workflows` |
| **In-cluster deploy** | Ready | `make dev-deploy` builds image + deploys manager |
| **Additional CRDs** | Not started | AgentPolicy, ToolPolicy, ApprovalPolicy, etc. |
| **Operational UI** | Not started | Vision documented in product rule |
| **Audit / observability backend** | Not started | Status fields exist; not populated by sidecars yet |

### What works today

- Create `AgentSession` → controller validates → creates owned Job → tracks `Pending` → `Starting` → `Running` → `Succeeded` / `Failed` / `TimedOut` / `Denied` / `Cancelled`
- CRD admission rejects invalid `temperature` (string + Pattern)
- Controller validation denies bad specs (e.g. empty task) without creating a Job
- `task.promptConfigMapRef` loads prompt from ConfigMap into `AGENT_TASK_PROMPT`
- Policy fields injected as `AGENT_POLICY_*` / `RELAY_*` env vars
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
| `status.podName` | Yes | Done — newest Job-owned Pod by creation timestamp |
| `status.violations` | Yes | No — no enforcement backend yet |
| `status.artifacts` | Yes | No — `outputs.collectArtifacts` not implemented |
| `policy.requireHumanApproval` | Yes | Surfaced only; does not block execution |
| `spec.cancelRequested` | Yes | Done — deletes Job; sets `PhaseCancelled`, condition, event |
| `PhaseCancelled` | Yes | Done — terminal via cancel reconcile path |
| Terminal session + missing Job | — | **Gap:** reconcile may recreate Job via `ensureJob` (see Discovered Follow-Up Tasks) |
| AgentSession delete | — | **Gap:** `DeletionTimestamp` returns early; no Job cleanup until finalizers |
| Orchestrators beyond `kubernetes-job` | Enum reserved | Rejected at validation |
| PVC-backed workspace | Commented future | emptyDir only |
| Webhook validation | Generated scaffold | Not wired |

### Recent fixes

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
- [~] **Session cancellation** — API + Job delete + status/events + e2e (done); docs pending
- [ ] **Finalizers** — Graceful cleanup of Jobs on AgentSession delete
- [ ] **CI pipeline** — GitHub Actions: `make test`, `make test-e2e` (kind), lint, build image
- [ ] **Admission webhook** (optional) — Move duplicate validation to validating webhook for earlier rejection
- [ ] **Helm chart or improved kustomize overlays** — Easier install than raw kustomize for early adopters
- [ ] **Terminal phase stability** — Do not recreate Jobs or regress phase for terminal sessions (see Discovered Follow-Up Tasks)
- [ ] **Reference scoping documentation** — Same-namespace rules for ConfigMap/policy/credential refs
- [ ] **E2e TimedOut path** — Prove `activeDeadlineSeconds` → `PhaseTimedOut` on kind

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

## Repository Audit (2026-05-17)

One-time scan performed while tightening Cursor rules. **No product code changed.**

| Area | Finding | Tracking |
|------|---------|----------|
| Cancellation | API + Job delete + status/events + e2e done; docs pending | Ready for Cursor Queue |
| Finalizers | Not implemented; delete path no-ops today | Ready for Cursor Queue |
| CI | No `.github/workflows/` | Ready for Cursor Queue |
| Terminal + missing Job | `ensureJob` may recreate Job for terminal sessions | Discovered Follow-Up Tasks |
| E2e | 10 specs incl. cancellation; TimedOut pending | Discovered Follow-Up Tasks |
| Envtest cancel | Job delete, idempotent missing Job, `PhaseCancelled`/condition/event | Done in controller tests |
| RBAC | Matches current controller; audit not documented | Discovered Follow-Up Tasks |
| Samples / README | Samples valid; README lacks cancel + future-only status clarity | Discovered Follow-Up Tasks |
| Enforcement / UI / extra CRDs | Not implemented (expected) | Roadmap Phases 2–7 |

---

## How to update this file

Use **Status File Self-Update Rules** above as the authoritative update workflow.

At minimum:

1. Move the task from `[ ]` or `[~]` to `[x]` only after tests pass or the user accepts incomplete work.
2. Add a **Recent fixes** line for behavior changes, bug fixes, or user-visible improvements.
3. Update **What works today**, **Known gaps**, and **Current Operational State** when those sections change.
4. Update the **Last updated** date at the top.
5. Add newly discovered work as a scoped task card with scope, non-goals, acceptance criterion, and verification command.
