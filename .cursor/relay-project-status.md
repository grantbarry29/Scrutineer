# Relay Project Status

> **What Relay has shipped, what is in progress, and where it is headed.**
> **Last updated:** 2026-06-24
>
> For **how agents should implement tasks** (scope rules, templates, scans, updating this file), see [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md). Older completed-work detail lives in git history and the per-phase design docs — this file keeps completed items terse and open work in full.

## Recent changes (newest first)

- **Phase 6 · slice 8 DONE (2026-06-27)** — live e2e for the `kubernetes-pod` backend (`test/e2e/pod_backend_test.go` + `withOrchestrator` fixture): a session with `orchestrator: kubernetes-pod` runs as a **Pod** (no Job), reaches `Succeeded`, with `status.runtimeRef.kind==Pod`, `status.podName` set, and a controller owner ref. Verified live on kind: the new spec **plus all 14 core busybox AgentSession e2e specs pass (0 failed)**. (The 8 sidecar live-evidence specs need first-party images built via Docker, which is unavailable in the current sandbox — not exercised here; unrelated to this change.) Next: slice 9 (docs alignment).
- **Phase 6 · slice 7 DONE (2026-06-27)** — closed out backend watch wiring: the generic `SetupWithManager` Owns-loop now dedupes owned types; generalized `needsBlockOwnerDeletionPatch` to any object and gave Pod `stop()` the same defensive `blockOwnerDeletion=false` patch as Jobs (GC parity, no teardown deadlock). Envtest asserts a Pod-backed session reaches Failed via the Pod watch (no manual reconcile) and that the agent Pod carries a controller owner ref with `blockOwnerDeletion=false`. Next: slice 8 (live e2e).
- **Phase 6 · slice 6 DONE (2026-06-27)** — Pod backend lifecycle correctness: `podRuntimePhase` now distinguishes `status.reason: DeadlineExceeded` → timed-out (vs generic failed) and maps Pending/empty → starting; added policy/profile drift handling (`reconcileExisting`) that delete+recreates a not-yet-started Pod and surfaces drift (`policyInSync=false`) on a running Pod, reusing the Job backend's tested drift detection via a thin Pod→template wrapper. New table-driven + fake-client unit tests (`backend_pod_test.go`); core logic at parity with the Job backend. Next: slice 7.
- **Phase 6 · slice 5 DONE (2026-06-27)** — added the `kubernetes-pod` reference backend (`backend_pod.go`): runs the agent as a bare Pod from the shared `job.BuildPodTemplateSpec`, registered next to the Job backend and selectable via `spec.runtime.orchestrator: kubernetes-pod` (CRD enum + `validateSpec` accept it). Reports `runtimeRef{kind:Pod}`/`podName`; envtest covers create-Pod-not-Job + Running→Succeeded.
- **Phase 6 · slice 4 DONE (2026-06-27)** — added backend-neutral `status.runtimeRef` (`apiVersion`/`kind`/`name`/`uid`) on `AgentSessionStatus`; the `observation` carries a `runtimeRef` and `applyObservation` populates it generically. Job backend sets `runtimeRef{kind:Job}` and still mirrors `jobName`/`podName` (back-compat; `jobName` now documented as a deprecated alias).
- **Phase 6 · slice 3 DONE (2026-06-27)** — extracted the shared agent pod-template into exported `job.BuildPodTemplateSpec`; `Build` now only adds the Job wrapper (no behavior change). Unblocks the `kubernetes-pod` backend.
- **Phase 6 second backend planned** — `kubernetes-pod` reference adapter + `status.runtimeRef` generalization designed (`docs/design/phase-6-orchestrator-interface.md`); ordered task cards under *Discovered Follow-Up Tasks → Phase 6*.
- **Phase 5 COMPLETE (slice 8)** — authenticated approver identity via opt-in mutating webhook (`internal/webhook/v1alpha1/`, `--enable-webhooks`) stamps `ApprovalRequest.spec.decidedBy` from apiserver `userInfo`; `failurePolicy: Fail`; webhook-mode envtest + live cert-manager verification done.
- **Per-tool runtime approval COMPLETE** — controller runtime variant, reporter approval channel, tool-gateway hold-and-ask, live e2e, abuse controls, and `status.pendingApprovals` surface (all redacted to `argDigest`).
- **dns-proxy egress-bypass fix** — controller injects lowercase `http_proxy`/`https_proxy`/`no_proxy` too (BusyBox wget/curl/Go now routed through the proxy); regression guard + full e2e 21/21.
- **Tool argument constraints COMPLETE** — `ToolArgumentRule`/`ArgumentConstraint` schema → gateway per-call eval → redacted evidence → live e2e.
- **Phase 6 interface** — `runtimeBackend` + registry + `kubernetesJobBackend` extracted; backend returns a neutral `observation`; reconciler owns status mapping.

The **roadmap** below is long-term product intent, not a single backlog. **Ready for Cursor Queue** lists the next small implementation slices.

---

## Ready for Cursor Queue

Pick **one task card** per session unless the user asks for a design plan. Implementation rules: [`.cursor/relay-cursor-workflow.md`](relay-cursor-workflow.md).

> **Critical path:** Phases 0–5 **closed** — control-plane reconciliation, three data-plane enforcement domains (network/tool/file), the runtime-evidence loop, observability/audit export, and human approval workflows (including per-tool runtime holds + authenticated approver identity) all ship. **Phase 6 (orchestrator adapters) is the active phase:** the `runtimeBackend` interface + `kubernetes-job` backend + normalized `observation` are done; the next work is the **`kubernetes-pod` reference backend + `status.runtimeRef` generalization**, decomposed into ordered slices 3–10 under *Discovered Follow-Up Tasks → Phase 6*. Design: [`docs/design/phase-6-orchestrator-interface.md`](../docs/design/phase-6-orchestrator-interface.md).

**Queue head:** *Phase 6 · slice 9 — docs + status alignment for the second backend* (see card below). Slices 3 (shared pod-template builder), 4 (`status.runtimeRef`), 5 (`kubernetes-pod` backend create/observe/stop), 6 (Pod lifecycle/drift correctness), 7 (backend watch wiring + GC parity), and 8 (live e2e) shipped 2026-06-27. Slices 9 → 10 are dependency-ordered; do one per session, in order.

**Other ready picks** (independent of Phase 6): *Audit controller RBAC for least privilege*, *Pin dev tool versions in README*, *External artifact storage export (S3)* — all under *Discovered Follow-Up Tasks*.

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

The six Phase 2 completion tasks (RuntimeProfile CRD → `runtimeProfileRef` + validation → apply profile to Job pod template → RuntimeProfile watch → operator docs/samples/e2e → roadmap close-out) shipped in sequence and are **done**. Full task templates live in git history; the capability/coverage table above summarizes the result. Do not re-run unless regressions.

---

## Discovered Follow-Up Tasks

**Purpose:** Permanent backlog for work noticed but not in the current task scope. Agents **must** add a task card here (or a roadmap bullet) **in the same session** when they discover out-of-scope work — chat summaries and “suggested next picks” alone are not enough; untracked items become project holes.

Scoped tasks found by repository audit or implementation work. **Not in the active queue** until promoted. Pick one at a time into **Ready for Cursor Queue** when appropriate.

---

### Phase 6 — orchestrator adapters (ordered task cards)

**Goal of the phase:** prove Relay's governance is orchestrator-agnostic by adding a second `runtimeBackend` behind the existing interface, without coupling the reconciler to any one orchestrator. Design: [`docs/design/phase-6-orchestrator-interface.md`](../docs/design/phase-6-orchestrator-interface.md) (read it before starting any slice).

**Decision (2026-06-24):** the concrete second backend is an in-tree **`kubernetes-pod`** backend (a bare Pod — the *reference adapter*), **not** Tekton-first. It is dependency-free, fully testable in the existing envtest + kind e2e harness, and exercises every generalization point a real adapter needs (different object kind, completion/timeout/drift semantics, `ownedType`, and `status.runtimeRef`). It de-risks the **external** adapters (Tekton → Argo → Temporal), which become slice 10+ design slices on top of the proven interface.

**Implement slices 3 → 10 in order, one per session.** Slices 3 and 4 are prerequisites for the Pod backend; 5–8 build and verify it; 9–10 close out docs. Each card has one acceptance criterion and one verification command. Do **not** bundle slices, and do **not** add an external orchestrator dependency (Tekton/Argo CRDs) — those are explicitly out of scope until slice 10's design slice.

#### Task: Phase 6 · slice 9 — docs + status alignment for the second backend

**Goal:** keep the docs honest once `kubernetes-pod` ships.

**Scope:**
- Flip the slice statuses in `docs/design/phase-6-orchestrator-interface.md` to *done* as each lands; update `architecture.md` (mention `status.runtimeRef` and the two backends; keep diagrams parser-safe — no parentheses/slashes in mermaid class members).
- README: document `spec.runtime.orchestrator: kubernetes-pod` and `status.runtimeRef`.
- Update this status file (operational state, roadmap `[x]`, recent changes).

**Non-goals:** No code changes.

**Acceptance criteria:** Docs match shipped behavior; `relay-design-docs.mdc` / `README.md` index rows still accurate.

**Expected files:** `docs/design/phase-6-orchestrator-interface.md`, `docs/design/architecture.md`, `README.md`, `.cursor/relay-project-status.md`

**Verification command:** Review only (docs).

#### Task: Phase 6 · slice 10 — external orchestrator adapter design (Tekton first) — **design slice**

**Goal:** design (not implement) the first external adapter now that the interface is proven by `kubernetes-pod`.

**Scope:**
- Extend `phase-6-orchestrator-interface.md` (or a new `phase-6-tekton-adapter.md`) with the Tekton `PipelineRun`/`TaskRun` mapping: how `runtimeBackend.ensure`/`observe`/`stop` map to Tekton objects, how Relay sidecars + reporter token are injected (Tekton `sidecars`/pod template), completion/timeout/cancel mapping, the new `go.mod` dependency + e2e install cost, and the evidence/assurance statement (pods still co-located → `self-reported`, no regression).
- Decompose Tekton implementation into its own ordered slices (API/validation → backend → tests → e2e), as task cards here. Note Argo + Temporal as subsequent adapters (Temporal has no co-located pod → needs its own evidence channel, open questions #3/#4).

**Non-goals:** No Tekton code, no dependency added in this slice.

**Acceptance criteria:** A reviewer can implement the Tekton adapter from the doc + cards without rediscovering the interface; assurance posture stated explicitly.

**Expected files:** `docs/design/phase-6-orchestrator-interface.md` (or new doc + index rows), `.cursor/relay-project-status.md`

**Verification command:** Review only (docs).

---

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

### Task: live e2e for the ApprovalRequest identity webhook — **discovered 2026-06-24; envtest DONE + live verified 2026-06-24 (committed spec optional)**

Slice 8 ships the identity-stamping webhook with thorough **unit** coverage (pure stamp logic + `Handle` patch/no-op via constructed `admission.Request`), opt-in manager wiring, generated `MutatingWebhookConfiguration`, and a cert-manager `config/webhooks` overlay that renders cleanly. **Webhook-mode envtest — DONE (2026-06-24):** a dedicated suite (`internal/webhook/v1alpha1/{suite_test.go,approvalrequest_envtest_test.go}`) starts the webhook server against an envtest control plane with the generated `MutatingWebhookConfiguration` installed (`WebhookInstallOptions`), provisions authenticated users via `testEnv.AddUser` (+cluster-admin binding so the test exercises the webhook, not RBAC), and asserts a forged `spec.decidedBy` is corrected to the authenticated identity on both create and update while pending creates are untouched. The suite skips when `KUBEBUILDER_ASSETS` is unset so the pure-unit tests still run standalone. **Live e2e — VERIFIED manually (2026-06-24):** in the `relay-dev` kind cluster, installed cert-manager v1.16.2, deployed the `config/webhooks` overlay (controller image `IfNotPresent`), confirmed the `serving-cert` Certificate went `Ready`, the caBundle (1488 B) was injected into the `MutatingWebhookConfiguration`, and the manager served the webhook on :9443. Then `kubectl --as=alice@example.com apply` of an `ApprovalRequest{decision: granted, decidedBy: mallory}` stored `spec.decidedBy=alice@example.com` (forged value corrected to the authenticated identity), while a pending create left `decidedBy` empty. Torn down afterward (the `failurePolicy: Fail` webhook is removed so it can't block `ApprovalRequest` writes when no controller runs; cert-manager left installed). **Remaining (optional):** fold this into a committed, opt-in ginkgo e2e spec — deliberately not added to the shared suite yet because it would make every `make test-e2e` run depend on cert-manager + an in-cluster controller deploy (the dev harness runs the controller out-of-cluster via `make run`). Test-only; no product code changes expected.

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

## Completed follow-ups (archived)

Shipped work, kept as a terse index. Full per-task detail (scope/files/verification) is in git history and the relevant `docs/design/` doc. Do not re-implement unless a regression is found.

**Component docs (2026-06-27):** `component-docs` (always-on) + `component-binaries` Cursor rules + `docs/templates/component-readme.md`; component READMEs authored for all five components — `cmd/{dns-proxy,tool-gateway,fs-gateway}`, `internal/controller/agentsession`, `internal/reporter` (manager overview is the root `README.md`).

**Phase 6 — orchestrator interface:** slice 1 (design doc) · slice 2 (`runtimeBackend` + registry + `kubernetesJobBackend` extracted) · slice 2b (backend returns neutral `observation`; reconciler `applyObservation`/`applyRuntimePhase` own status).

**Phase 5 — approval workflows:** slice 1 (design doc) · slice 2 (`ApprovalPolicy` CRD) · slice 3 (`ApprovalRequest` CRD + controller gate/resume, `AwaitingApproval`) · slice 4 (notification hooks, `--approval-webhook-url`) · slice 5 (approver allowlist via `decidedBy`) · slice 6 (multi-approver `allOf`, `status.approvedBy[]`) · slice 7 (approval-decision OTLP audit records + at-most-once notify fix) · slice 8 (authenticated approver identity mutating webhook). Per-tool **runtime approval** (design + impl slices 1–4 + abuse controls + `status.pendingApprovals`).

**Phase 4 — observability & audit:** usage metrics + e2e assertions (slice A) · session timeline model (slice B) · usage-only `reportId` idempotency cache (slice C) · FS gateway sidecar MVP (slice D) · file usage metrics (slice E) · live file violation+usage e2e (slice F) · Prometheus exporter · OpenTelemetry tracing · OTLP audit log sink · log/artifact collection · observability export design doc · `relay_approval_queue_depth` refine (counts pending `ApprovalRequest`s).

**Phase 3 / 3b — enforcement & evidence loop:** enforcement backend contract · DNS/egress proxy prototype + dns-proxy image · tool-gateway contract + image · file/workspace policy design + implementation · first-party sidecar images · runtime reporter loop (`POST /v1/report`) · structured session events API · reporter pod wiring (projected token + Service) · live network/tool violation e2e · runtime policy-decision append. Tool **argument constraints**: schema design + slice 2 (schema) + slice 3 (gateway eval + redacted evidence) + slice 4 (live e2e). e2e distroless image-probe fix. **dns-proxy egress-bypass fix** (inject lowercase proxy env too; regression guard).

**Phase 2 — reusable policy model:** `AgentPolicy`/`ToolPolicy` + `policyRefs` merge + watches + effective policy · `RuntimeProfile` CRD + hardening + sidecar injection + watch · `ToolPolicy maxCallsPerMinute` propagation.

**Controller hardening & docs:** AgentSession `Ready` condition · watch owned Pods · reconcile-churn fix (idempotent resolution events) · provider-agnostic `model.baseURL` · document future-only status fields · document controller Kubernetes Events · README current-state update · data-plane unit-coverage raise.

**Runtime evidence integrity (partial — slices 1–3 done):** `assuranceLevel` enum on decisions/violations + audit records (`relay.audit.assurance`) · agent SA-token automount disabled + reporter token sidecar-only. Remaining hardening is the still-open *Runtime evidence integrity* card above.

## Current Operational State

Relay has shipped an **end-to-end governance MVP** on Kubernetes: control-plane reconciliation, three data-plane enforcement domains (network / tool / file), runtime evidence into CRD status, observability export (Prometheus, OTel traces, OTLP audit logs), and **human approval workflows** (session gate + per-tool runtime holds + authenticated approver identity). **Not yet shipped:** operational UI, orchestrator adapters beyond Jobs (Phase 6 in progress — `kubernetes-pod` next), enterprise identity/credentials.

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
| **Approval workflows** | **Complete (Phase 5, slices 1–8)** | `ApprovalPolicy` + `ApprovalRequest` CRDs; controller gate enforces `requireHumanApproval` (`AwaitingApproval` → grant/deny); approvers webhook-notified (`--approval-webhook-url`); multi-approver `allOf`; per-tool runtime holds (`pendingApprovals` surface); **authenticated approver identity** via opt-in mutating webhook (`--enable-webhooks`, `config/webhooks` overlay). Remaining: list-typed concurrent multi-grant; webhook-mode envtest/live-e2e |
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

Recent user-visible changes are summarized in **Recent changes (newest first)** at the top of this file; shipped work is indexed under **Discovered Follow-Up Tasks → Completed follow-ups (archived)**. Full per-change detail (including the Phase 0–2 foundation: lifecycle, cancellation, finalizers, policy CRDs, RuntimeProfile, CI) lives in git history.

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

**Complete (slices 1–8).** Scoped, auditable gates — not a boolean env var. `requireHumanApproval` is now a real gate, not just a warning. Ordered task cards archived under *Discovered Follow-Up Tasks → Completed follow-ups*.

- [x] **Approval model design doc** — CRD shape + gate/resume state machine *(slice 1)*
- [x] **ApprovalPolicy CRD** — what actions require approval *(slice 2, declarative)*
- [x] **ApprovalRequest CRD + controller gate** — block in `AwaitingApproval`, resume on grant *(slice 3)*
- [x] **Approval audit trail** — who/when/scope/expiry (`ApprovalRequest.status` + `policyDecisions{type:approval}` + OTLP `approval.*` records) *(slices 3, 7)*
- [x] **Notification hooks** — generic webhook (`--approval-webhook-url`); Slack/PagerDuty future adapters *(slice 4)*
- [x] **Approver allowlist + multi-approver `allOf`** *(slices 5, 6)*
- [x] **Per-tool runtime approval** — mid-execution hold of a running tool call; `status.pendingApprovals` surface *(runtime-approval design + impl)*
- [x] **Authenticated approver identity** — opt-in mutating webhook stamps `spec.decidedBy` from apiserver `userInfo` *(slice 8)*

---

### Phase 6 — Orchestrator adapters

Stay orchestrator-agnostic; add backends without coupling the core reconciler to Jobs. **Active phase.** Ordered slices 3–10 under *Discovered Follow-Up Tasks → Phase 6*; design: `docs/design/phase-6-orchestrator-interface.md`.

- [x] **Orchestrator interface** — `runtimeBackend` + `backendRegistry` + `kubernetesJobBackend`; backend returns a normalized `observation` and the reconciler (`applyObservation`/`applyRuntimePhase`) owns status mapping *(design + slices 2/2b done 2026-06-21)*
- [~] **`kubernetes-pod` reference backend** — second in-tree backend proving orchestrator-agnosticism. Shared pod-template builder (slice 3) + `status.runtimeRef` (slice 4) + create/observe/stop backend (slice 5) + lifecycle/drift correctness (slice 6) + watch wiring/GC parity (slice 7) + live e2e (slice 8) **done 2026-06-27**; remaining: docs alignment + Tekton adapter design (slices 9–10).
- [ ] **Tekton adapter** — `runtime.orchestrator: tekton` *(design slice 10, then impl)*
- [ ] **Argo Workflows adapter**
- [ ] **Temporal adapter** (or external worker handshake) — no co-located pod → needs its own evidence channel/assurance
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
