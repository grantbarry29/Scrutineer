# reporter

Scrutineer's runtime-evidence and approval HTTP service. Ingests evidence from data-plane
callers — `self-reported` from cooperative in-agent-pod sidecars, `observed` from the
per-session out-of-pod egress proxy — and serves the per-tool approval channel, turning
*propagated* governance into *observed* governance by writing `AgentSession.status`.
Compiled into the manager binary ([`cmd/main.go`](../../cmd/main.go)); can run standalone
via `--reporter-only`.

## Purpose

Without it, `status.policyDecisions`/`violations`/`usage` stay empty at runtime. It is the
trusted boundary between in-pod sidecars and the control plane: it authenticates each
caller pod and merges only validated evidence into the session status owned by the
[agentsession controller](../controller/agentsession).

## Responsibilities / Non-responsibilities

- **Does:** serve `POST /v1/report` (authenticate, rate-limit, dedup, validate/normalize,
  merge into status via `agentsession.PatchRuntimePolicyReport`, emit `RuntimeViolation`
  events + audit); serve the approval channel (`POST /v1/approvals`, `GET /v1/approvals/{id}`)
  creating runtime `ApprovalRequest`s idempotently and reporting controller-observed state;
  cap outstanding holds per session.
- **Does not:** decide policy; **write `ApprovalRequest.status`** (the controller is the
  sole writer); enforce anything. Evidence assurance is stamped **from the caller's
  authenticated identity, never the payload**: in-agent-pod sidecars are `self-reported`
  (cooperative, not adversarial-proof); only the session's egress-proxy pod identity
  yields `observed` (Slice C, [#62](https://github.com/grantbarry29/scrutineer/issues/62) —
  see [`docs/design/evidence-integrity.md`](../../docs/design/evidence-integrity.md) §4/§5
  for what `observed` does and does not guarantee).

## Entry point & execution model

- `reporter.NewRunnable(Options)` returns a `manager.Runnable` (`server.go`) serving an
  HTTP mux; wired into the manager in [`cmd/main.go`](../../cmd/main.go). Graceful shutdown
  on context cancel.
- Bind address: `--reporter-bind-address` (default `reporter.DefaultBindAddress`).

## Control / data flow

Sidecar → `POST /v1/report` (bearer token) → `IdentityVerifier.Verify` → rate-limit →
load session → `ValidateAndNormalizeReport` → reportId dedup → `PatchRuntimePolicyReport`
(status merge, retry-on-conflict) → emit `RuntimeViolation` event + `audit.RuntimeReport`
→ `202`. Approval channel registers/polls holds reusing the same identity model.

## Major internal files

- `server.go` — `Options`, `NewRunnable`, mux/routes, RBAC markers.
- `handler.go` — `POST /v1/report` pipeline + result/metrics/trace tagging.
- `approvals.go` — approval channel (idempotent by `requestId`, outstanding-hold cap).
- `auth.go` — `KubeIdentityVerifier`: TokenReview + two authorization paths returning a
  `CallerClass`: pod→Job→session ownership (`agent-sidecar`) or AgentSession-controller-
  owned egress-proxy pod with the deterministic name + dedicated SA (`egress-proxy`).
- `normalize.go` — payload validation/normalization; `ratelimit.go`,
  `reportid_cache.go` — abuse controls + dedup; `errors.go`, `types.go`.

## Repository dependencies & related components

- Depends on [`api/v1alpha1`](../../api/v1alpha1),
  [`internal/controller/agentsession`](../controller/agentsession)
  (`PatchRuntimePolicyReport`), [`internal/controller/job`](../controller/job)
  (`LabelSessionRef`), [`internal/audit`](../audit), [`internal/metrics`](../metrics),
  [`internal/tracing`](../tracing).
- Clients: the data-plane sidecars POST here; the controller reconciles the
  `ApprovalRequest`s this service creates.
- Design: [`docs/design/phase-3-runtime-reporter-contract.md`](../../docs/design/phase-3-runtime-reporter-contract.md),
  [`docs/design/phase-5-runtime-tool-approval.md`](../../docs/design/phase-5-runtime-tool-approval.md).

## Interfaces & artifacts

- **Endpoints:** `POST /v1/report`; `POST /v1/approvals` (register/lookup);
  `GET /v1/approvals/{id}` (poll).
- **Auth:** `Authorization: Bearer <projected SA token>` with audience `TokenAudience`;
  pod identity from the token's `pod-name` extra or the `X-Scrutineer-Pod` header; the pod's
  ServiceAccount must match the token, and the pod must be either owned by the session's
  Job (label `LabelSessionRef` → `self-reported`) or the session's egress-proxy pod
  (AgentSession controller owner-ref + `envoy.ResourceName` name + dedicated SA →
  `observed`).
- **Limits:** `MaxReportBytes` / `MaxApprovalBodyBytes`; per-session rate limiting;
  `DefaultMaxOutstandingApprovals` undecided holds; reportId dedup TTL.
- RBAC from `+kubebuilder:rbac` markers in `server.go` (`tokenreviews: create`;
  `agentsessions: get` + `agentsessions/status: get;update;patch`; `approvalrequests:
  get;list;create`; `jobs`/`pods: get`) via `make manifests`. These render into a
  **dedicated least-privilege `reporter-role`** (`config/rbac/reporter/role.yaml`),
  scoped by a separate path-restricted `controller-gen` invocation so the reporter's
  permissions are *not* aggregated into the broad `manager-role`. The role is bound by
  `config/rbac/reporter_role_binding.yaml`.
  > **Deployment modes:** by default the reporter runs **in-process** in the manager
  > (`--enable-reporter=true`), under the `scrutineer-controller-manager` ServiceAccount — so
  > that SA holds both roles and the split is RBAC hygiene only. The opt-in
  > [`config/reporter-standalone`](../../config/reporter-standalone) overlay runs the
  > reporter as its **own Deployment** (`--reporter-only`) under a dedicated
  > `scrutineer-reporter` ServiceAccount and sets `--enable-reporter=false` on the manager, so
  > the manager SA keeps only `manager-role` and the reporter's RBAC lives solely with the
  > reporter identity. That overlay is what actually reduces the manager SA's runtime
  > privilege (issue #34).

## Read consistency & caching (decisions: #47, #53)

Every hot-path read in this package goes through the **uncached** `APIReader`
(`mgr.GetAPIReader()`), never the cached manager client. This is deliberate:

- **Consistency-critical (must stay uncached):** the status-merge path
  `agentsession.PatchRuntimePolicyReport` does an optimistic-concurrency `Update`
  inside a retry loop. A cached read returns a stale `resourceVersion`, so the
  `Update` would conflict, retry, re-read the same stale version, and exhaust its
  retries — turning every report under contention into a spurious `409`.
- **Not consistency-critical, still uncached on purpose:** the session-existence
  pre-read (`handler.go`), the identity pod/Job lookups (`auth.go`), and the
  `countOutstandingHolds` cap List (`approvals.go`). A cached client is
  informer-backed, so using it would require `list;watch` on
  AgentSessions/Pods/Jobs/ApprovalRequests **and** hold namespace-wide informer
  caches in memory. That directly undermines the reporter's dedicated **get-only
  `reporter-role`** and the standalone `--reporter-only` Deployment's 128Mi budget
  (issue #34). Reads are bounded by per-session rate limiting instead.

If the standalone reporter's RBAC/footprint constraints are ever relaxed, the
non-consistency-critical reads could move to the cached client; the status-merge
read must remain uncached regardless.

**GET count on the report path (#53):** a successful `POST /v1/report` does a
**single** AgentSession GET. The handler's pre-read is passed to
`PatchRuntimePolicyReport` as a *seed*, so the merge reuses it instead of issuing
its own read, and the merge no longer does a redundant second read before the
optimistic-concurrency `Update`. On a conflict, the retry re-reads fresh (the seed
is only used on the first attempt), so consistency is unchanged.

## Invariants & files that must change together

- The **controller is the sole writer of `ApprovalRequest.status`**; this service only
  creates holds and reports observed state.
- Evidence assurance is stamped **server-side from `CallerIdentity.Assurance()`**, never
  client-settable: `self-reported` for agent-sidecar callers, `observed` only for the
  egress-proxy identity; an empty/unknown class degrades to `self-reported`. The audit
  `RuntimeReport` record carries the same identity-derived assurance.
- Unknown session / failed ownership check ⇒ fail closed (`404`/`401`/`403`).
- Status-merge logic lives in `agentsession.PatchRuntimePolicyReport` — changes to status
  shape must stay consistent across the reporter and the controller; RBAC marker changes
  require `make manifests`.

## Build / test / run / validate

`make test` (unit/envtest); `make test-e2e-images && make test-e2e` for live evidence and
approval specs (the in-cluster reporter runs as a separate Deployment/Service —
TODO: verify the exact `config/` overlay path).

## Operability

Metrics via `metrics.ObserveRuntimeReport(result, latency)`; traces `runtime.report` /
`runtime.approval`; `RuntimeViolation` Kubernetes events; `audit.RuntimeReport` records.
Response/result codes: `accepted`, `duplicate`, `rate_limited`, `unauthorized`,
`forbidden`, `not_found`, `conflict`, `payload_too_large`, `bad_request`,
`internal_error`.
