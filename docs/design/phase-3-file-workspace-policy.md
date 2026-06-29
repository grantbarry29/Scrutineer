# Phase 3 File / Workspace Policy Design

Scrutineer Phase 3 slice 8 defines how **file and workspace access** should be governed for AgentSession runtimes. **API fields, merge semantics, env propagation, evaluate/report helpers, and the first-party fs-gateway sidecar** ship in `internal/enforcement/workspace/`.

## Current state (MVP)

| Mechanism | What it does today |
|-----------|-------------------|
| `spec.workspace` | Optional ephemeral `emptyDir` at `/workspace` (size-validated) |
| `RuntimeProfile` container | `readOnlyRootFilesystem`, dropped capabilities, optional seccomp |
| `RuntimeProfile` pod | `runtimeClassName` (schema only; sandbox not enforced by Scrutineer) |
| `PolicyRules` | `allowedPaths`, `deniedPaths`, `maxWorkspaceBytes` + network/tool fields |

Agents can still write anywhere the container filesystem allows outside the workspace mount. There is no path-level allow/deny policy in CRDs today.

## Threat model (file governance)

Enterprises need to limit autonomous agents from:

- Reading secrets or host paths outside the workspace
- Writing binaries or config to sensitive locations (`/etc`, `~/.ssh`, project roots)
- Exfiltrating data via unrestricted workspace writes
- Persisting malware across sessions (mitigated partly by ephemeral workspace)

File policy must be **auditable**, **mode-aware** (`audit-only` / `dry-run` / `enforced`), and fit the existing `enforcement` contract (`SessionContext`, `RuntimeReport`, `ApplyRuntimePolicyReport`).

## Options considered

### 1. Mount strategy only (control-plane)

**How:** Restrict what is mounted and how:

- Ephemeral workspace only (already MVP)
- `readOnlyRootFilesystem: true` via RuntimeProfile (already supported)
- No hostPath / PVC until explicitly scoped per session
- Optional future: read-only ConfigMap/Secret mounts for known inputs only

**Pros:** Simple, Kubernetes-native, no data-plane code.  
**Cons:** Coarse — cannot express per-path allow/deny inside the workspace; cannot block writes to `/tmp` unless root FS is read-only.

**Fit:** Immediate hardening layer; **recommended as Phase 3 baseline** alongside existing RuntimeProfile fields.

### 2. FS proxy / gateway sidecar (data-plane)

**How:** Inject an `fs-gateway` (or extend tool-gateway) sidecar; agent I/O goes through a governed FUSE/bind mount or intercepted SDK.

**Pros:** Path-level policy, unified reporting with `type: file` decisions/violations.  
**Cons:** High complexity, agent integration burden, performance cost, new first-party image.

**Fit:** Post–Phase 3 implementation slice after network/tool gateways prove the reporter pattern.

### 3. Sandbox runtime (gVisor / Kata / seccomp)

**How:** `RuntimeProfile.spec.pod.runtimeClassName` + syscall profiles; optional eBPF process monitoring later.

**Pros:** Strong isolation for hostile agents.  
**Cons:** Not path-specific; cluster dependency; ops overhead; Scrutineer does not own the sandbox runtime.

**Fit:** Complements file policy but **does not replace** path governance. Already schema-supported; enforcement is cluster/platform concern.

### 4. Policy CRD fields (`allowedPaths` / `deniedPaths`)

**How:** Extend `PolicyRules` (or future `FilePolicy` CRD) with path globs; merge like domains/tools; propagate via env or sidecar config.

**Pros:** Consistent with Phase 2 policy model.  
**Cons:** Requires API change, merge semantics, and a backend (mount or FS proxy) to enforce.

**Fit:** **Shipped** on `PolicyRules` (2026-06-10); FS gateway sidecar deferred.

## Recommendation

### Shipped (2026-06-10)

1. **`PolicyRules.allowedPaths` / `deniedPaths` / `maxWorkspaceBytes`** — merge + `AGENT_POLICY_*` env propagation on agent containers.
2. **`workspace.EvaluateFile` + `RuntimeReport` + fs-gateway sidecar** — path evaluation, `POST /v1/files/access`, reporter client (`cmd/fs-gateway`).
3. **Mount + RuntimeProfile hardening** remain the coarse control-plane baseline (no hostPath enforcement in this slice).

### API shape

```yaml
policyRules:
  allowedPaths:
    - /workspace/**
  deniedPaths:
    - /etc/**
    - /root/.ssh/**
  maxWorkspaceBytes: 5368709120  # optional cap; propagated, not enforced yet
```

Merge semantics: union allow/deny lists; min `maxWorkspaceBytes` — same spirit as network/tool merge.

### Proposed future backend order

1. **Mount enforcement** — controller rejects unsafe volume mounts; document required RuntimeProfile defaults for production.
2. **FUSE / syscall hardening** — optional future path for transparent interception (MVP uses explicit HTTP gateway).
3. **Sandbox profile** — platform team sets `runtimeClassName`; Scrutineer records matched profile only.

## Reporting contract

FS gateway sidecars (future) and controller helpers emit:

| Field | Value |
|-------|--------|
| `PolicyDecision.type` | `file` |
| `PolicyDecision.target` | path or glob matched |
| `PolicyDecision.reason` | `DeniedPaths`, `NotInAllowedPaths`, etc. |
| `PolicyViolation.type` | `file` |

Controller entry point: `ApplyFilePolicyRuntimeEvent` (mirrors `ApplyEgressProxyRuntimeEvent`).

## Non-goals (remaining)

- No FUSE transparent filesystem interception
- No new CRDs (`FilePolicy`, `WorkspacePolicy`)
- No mount-strategy validation beyond existing RuntimeProfile fields
- No syscall/eBPF monitoring

## Implementation

See [`internal/enforcement/workspace/`](../internal/enforcement/workspace/).

## Related docs

- [`phase-3-enforcement-architecture.md`](phase-3-enforcement-architecture.md)
- [`phase-3-dns-proxy-prototype.md`](phase-3-dns-proxy-prototype.md)
- [`phase-3-tool-gateway-contract.md`](phase-3-tool-gateway-contract.md)
