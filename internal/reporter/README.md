# reporter

Relay's runtime-evidence and approval HTTP service. Ingests self-reported evidence from
cooperative data-plane sidecars and serves the per-tool approval channel, turning
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
  sole writer); enforce anything. All ingested evidence is stamped `self-reported`
  assurance — it is **cooperative**, not adversarial-proof.

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
- `auth.go` — `KubeIdentityVerifier`: TokenReview + pod→Job→session ownership.
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
  pod identity from the token's `pod-name` extra or the `X-Relay-Pod` header; the pod's
  ServiceAccount must match the token and the pod must be owned by the session's Job
  (label `LabelSessionRef`).
- **Limits:** `MaxReportBytes` / `MaxApprovalBodyBytes`; per-session rate limiting;
  `DefaultMaxOutstandingApprovals` undecided holds; reportId dedup TTL.
- RBAC from `+kubebuilder:rbac` markers in `server.go` (`tokenreviews: create`;
  `agentsessions: get` + `agentsessions/status: get;update;patch`; `approvalrequests:
  get;list;create`; `jobs`/`pods: get`) via `make manifests`. These render into a
  **dedicated least-privilege `reporter-role`** (`config/rbac/reporter/role.yaml`),
  scoped by a separate path-restricted `controller-gen` invocation so the reporter's
  permissions are *not* aggregated into the broad `manager-role`. The role is bound by
  `config/rbac/reporter_role_binding.yaml`.
  > **Note:** the reporter runs in-process under the `relay-controller-manager`
  > ServiceAccount today, so that SA is the binding subject and its *effective* runtime
  > privilege is unchanged. Running the reporter under its own ServiceAccount — the change
  > that actually reduces the manager SA's privilege — is tracked separately (issue #34).

## Invariants & files that must change together

- The **controller is the sole writer of `ApprovalRequest.status`**; this service only
  creates holds and reports observed state.
- Evidence assurance is stamped **server-side** (`self-reported`), never client-settable.
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
