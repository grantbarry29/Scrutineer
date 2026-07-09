# Access-Log Rotation vs. Tamper Evidence

**Status:** decided and implemented (#98).
**Scope:** why the egress access log rotates, why rotation is gated on ingest, and the
failure semantics of the rotation protocol in `internal/enforcement/envoy/tailer.go`.

## The tension

The per-session Envoy proxy writes an append-only JSON access log into a size-bounded
(256Mi) emptyDir; the egress-reporter tails it and submits each entry as `observed`
evidence. The bound is a tamper-evidence property: the log is the durable buffer between
"the request happened" and "the controller recorded it", and overflowing it fails closed
(kubelet evicts the pod; the routing lock leaves the agent with no egress at all rather
than ungoverned egress).

Without rotation, that property is also a time bomb: the log only grows, so **every
sufficiently busy or long-running session is guaranteed to eventually evict its own
proxy** (~1.3M requests at ~200B/line; a 10 req/s agent hits it in ~36h) — even with a
perfectly healthy reporter that ingested every line long ago.

Naive rotation (delete/truncate on size) would resolve the time bomb by destroying the
tamper-evidence property: an agent that floods the log could force rotation and erase
requests **before** the reporter ingested them.

## The invariant

> **Only fully-ingested bytes are ever removed.** Rotation is gated on ingest progress:
> nothing is renamed until the tailer has read to EOF *and* delivered everything to the
> reporter, and nothing is deleted until the renamed remainder has also been fully
> drained. Growth beyond what the reporter can ingest — reporter outage, or deliberate
> flooding — still ends in fail-closed eviction, and #99 refuses to recreate the pod
> after an evidence-volume overflow eviction (a fresh empty volume is exactly what a
> flooding agent wants).

An agent therefore gains nothing from flooding: rotate-away requires its requests to be
ingested first (they become evidence), and out-running the reporter ends the session's
egress entirely.

## The protocol

One rotation cycle (`Tailer.startRotation` / `finishRotation`), triggered only when the
live log exceeds the threshold (`SCRUTINEER_ROTATE_AFTER_BYTES`, default 64Mi) **and**
is fully drained:

1. **Rename** `access.json` → `access.json.rotating`. Envoy keeps writing to the same
   inode through its open fd, so no line is lost by the rename itself.
2. **Reopen**: POST the Envoy admin `/reopen_logs` (loopback `:9901`; reachable because
   the reporter container shares the pod netns — never exposed on the pod IP). Envoy
   reopens its configured path, creating a fresh `access.json`. If the reopen fails, the
   rename is reverted and retried next cycle.
3. **Drain** the renamed remainder — including anything Envoy wrote between the rename
   and the reopen — through the normal chunk/flush loop.
4. **Grace**: one further fully-drained poll cycle, so Envoy's access-log flusher (flush
   interval ≪ the 2s poll interval) can land anything still buffered for the old fd.
5. **Delete** the remainder and switch tailing to the fresh live file at offset zero.

## Failure semantics

| Failure | Behavior |
|---|---|
| Reporter down / rejecting | No drain ⇒ no rotation. The log buffers the backlog; past the emptyDir cap the pod is evicted — fail closed, surfaced by #99. |
| `/reopen_logs` fails | Rename reverted, retried next cycle; log keeps growing toward the fail-closed cap in the worst case. |
| Reporter restart mid-rotation | The leftover `.rotating` file is detected and drained first (from offset 0 — at-least-once, absorbed by server-side dedup), then deleted; order old-before-new is preserved. |
| Crash between rename and reopen | On resume: no live file exists ⇒ the reopen is retried until Envoy recreates it; the remainder is drained meanwhile. |
| Agent floods the log | Rotation removes only ingested bytes (they are already evidence); growth beyond ingest evicts the pod, and #99 holds it down (`EgressProxyHealthy=False` / `EvidenceVolumeOverflow`). |

## Residual assumptions (documented, not proven)

- Envoy flushes buffered access-log writes to the old fd within one poll cycle of
  acking `/reopen_logs` (its flusher runs sub-second; the grace step covers it). A write
  landing later than that on the old inode would be deleted un-ingested — bounded to at
  most one flusher interval of lines, and only ever after a reopen.
- The reporter container can rename/delete in the shared emptyDir: both containers run
  as the same non-root UID and the mount is writable for the reporter (#98 changed it
  from read-only). The tamper surface is unchanged — the reporter *is* the ingest
  component, first-party and outside the agent's trust domain.

## Relations

- **#94 / [`long-running-agents.md`](long-running-agents.md)** — rotation removes the
  log-growth ceiling on session lifetime; the remaining long-running blockers are
  lifecycle/evidence-model questions, not disk.
- **#99** — the recovery half: cause-aware handling of the evicted proxy pod; overflow
  evictions are never silently recreated.
- **[`evidence-integrity.md`](evidence-integrity.md)** — the trust-boundary reasoning
  this note builds on.
