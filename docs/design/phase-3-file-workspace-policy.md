# Phase 3 File / Workspace Policy Design

Relay Phase 3 slice 8 defines how **file and workspace access** should be governed for AgentSession runtimes. This is a **design document only** — no file enforcement ships in slice 8.

## Current state (MVP)

| Mechanism | What it does today |
|-----------|-------------------|
| `spec.workspace` | Optional ephemeral `emptyDir` at `/workspace` (size-validated) |
| `RuntimeProfile` container | `readOnlyRootFilesystem`, dropped capabilities, optional seccomp |
| `RuntimeProfile` pod | `runtimeClassName` (schema only; sandbox not enforced by Relay) |
| `PolicyRules` | Network + tool fields only; **no file/path rules** |

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
**Cons:** Not path-specific; cluster dependency; ops overhead; Relay does not own the sandbox runtime.

**Fit:** Complements file policy but **does not replace** path governance. Already schema-supported; enforcement is cluster/platform concern.

### 4. Policy CRD fields (`allowedPaths` / `deniedPaths`)

**How:** Extend `PolicyRules` (or future `FilePolicy` CRD) with path globs; merge like domains/tools; propagate via env or sidecar config.

**Pros:** Consistent with Phase 2 policy model.  
**Cons:** Requires API change, merge semantics, and a backend (mount or FS proxy) to enforce.

**Fit:** **Defer schema** until mount baseline + reporter path are proven; document proposed shape in this file.

## Recommendation

### Phase 3 close-out (no new enforcement code)

1. **Treat mount + RuntimeProfile hardening as the file governance MVP** for Kubernetes Job runtimes.
2. **Defer path-level `PolicyRules`** and FS proxy sidecar to a post–Phase 3 slice (see status tracker).
3. **Reuse existing reporting** when file enforcement ships: `type: file` decisions, `ApplyRuntimePolicyReport`, `enforcement.EvaluateRestrictive` mode semantics.

### Proposed future API shape (not implemented)

```yaml
# Illustrative — not in CRDs today
policyRules:
  allowedPaths:
    - /workspace/**
    - /tmp/runtime-*
  deniedPaths:
    - /etc/**
    - /root/.ssh/**
  maxWorkspaceBytes: 5368709120  # optional cap
```

Merge semantics (when implemented): union allow lists, union deny lists, min numeric caps — same spirit as network/tool merge.

### Proposed future backend order

1. **Mount enforcement** — controller rejects unsafe volume mounts; document required RuntimeProfile defaults for production.
2. **FS gateway sidecar** — path checks + `RuntimeReport` (mirror `dnsproxy` / `toolgateway`).
3. **Sandbox profile** — platform team sets `runtimeClassName`; Relay records matched profile only.

## Reporting contract (future)

When file enforcement exists, data-plane components should emit:

| Field | Value |
|-------|--------|
| `PolicyDecision.type` | `file` |
| `PolicyDecision.target` | path or glob matched |
| `PolicyDecision.reason` | `DeniedPaths`, `NotInAllowedPaths`, etc. |
| `PolicyViolation.type` | `file` |

Controller entry point (future): `ApplyFilePolicyRuntimeEvent` mirroring `ApplyEgressProxyRuntimeEvent`.

## Non-goals (slice 8)

- No FS proxy sidecar implementation
- No new CRDs (`FilePolicy`, `WorkspacePolicy`)
- No changes to Job volume builder beyond documentation
- No syscall/eBPF monitoring

## Related docs

- [`phase-3-enforcement-architecture.md`](phase-3-enforcement-architecture.md)
- [`phase-3-dns-proxy-prototype.md`](phase-3-dns-proxy-prototype.md)
- [`phase-3-tool-gateway-contract.md`](phase-3-tool-gateway-contract.md)
