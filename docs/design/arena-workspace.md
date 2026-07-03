# Arena Workspace — Untamperable File Governance via a Network-POSIX Workspace

**Status:** draft / deferred (design TODO from the pivot; not scheduled)
**Scope:** the out-of-pod successor to the removed in-pod fs-gateway: the agent's governed workspace ("arena" — repos, files, artifacts) lives in a separate per-session pod and is served over a per-operation network protocol, so every file operation crosses a boundary the agent cannot alter — mediated, policy-checked, and `observed`.
**Non-goals:** governing the agent container's own rootfs or scratch space (ungoverned by design — a local tmpfs keeps hot paths fast); syscall-level interception (#29 sandboxes).
**Tracking:** to be filed when scheduled; absorbs #33 (transparent/FUSE file interception); inherits path-rule semantics from the historical `phase-3-file-workspace-policy.md`.

---

## Protocol options (analysis done, decision when scheduled)

| Option | Mount privilege | Traffic origin (netns) | Per-op mediation | Notes |
|---|---|---|---|---|
| **FUSE client (agent pod) → custom Go server (arena pod)** — *leading candidate* | `/dev/fuse` (+ user namespaces for unprivileged) | **agent pod** — routing lock + per-session peer selectors + session-token auth all work | full (we own the protocol) | The fs analog of the Envoy path; policy evaluated server-side in the arena pod; `observed` via arena identity |
| 9p (kernel client) | kernel mount (privileged or CSI) | pod if mounted in-netns | full — every walk/open/read/write is an RPC | Simple embeddable Go servers; slower; some POSIX gaps (shared writable mmap) |
| NFS (`nfs:` volume, kubelet-mounted) | none (kubelet mounts) | **node** — per-session NetworkPolicy/identity cannot see it; AUTH_SYS spoofable | weak | Zero-privilege convenience, wrong trust properties for us |
| gVisor gofer / Kata virtiofs (#29) | RuntimeClass adoption | sandbox boundary | full, for free | "Buy instead of build" if the sandbox track is adopted anyway |

Key facts driving the recommendation: kubelet-performed mounts originate in the **host** netns (breaks per-session attribution and the lock); an in-pod FUSE client keeps traffic pod-originated so the entire existing enforcement stack applies; per-op RPC protocols give the file-domain equivalent of Envoy's access log.

## Honest costs

- Metadata-heavy workloads (`git status`, `npm install`) are pathological over per-op RPC; aggressive attribute/entry caching recovers most of it. Databases in the arena: unsupported.
- FUSE in containers needs `/dev/fuse` and historically `SYS_ADMIN`; Kubernetes user namespaces make unprivileged FUSE viable — a deployment-floor consideration like the CNI requirement.
- Hybrid layout: governed arena mount + ungoverned local tmpfs scratch, so only governed data pays the network cost.

## Open questions (answer when scheduled)

1. FUSE client delivery: init-container installing the client + mount, or a purpose-built agent-pod base layer?
2. Cache coherence needs for multi-writer arenas (agent + tools pod both writing)?
3. Path-rule policy shape: inherit `phase-3-file-workspace-policy.md` rules or reshape around mount-time grants + per-op checks?
4. Snapshot/audit story: arena pod is perfectly placed for copy-on-write session diffs ("what changed") — in scope for v1 of this design?
