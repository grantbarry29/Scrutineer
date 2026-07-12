---
type: Component README
title: job builder
description: "Builds and compares runtime objects for AgentSessions; BuildPodTemplateSpec is the single source of the agent pod shape consumed by both runtime backends."
status: live
read_when: "Working in internal/controller/job/."
---

# internal/controller/job

Builds and compares the runtime objects for `AgentSession`s. `BuildPodTemplateSpec` is
the single source of the agent pod shape — both runtime backends (`kubernetes-job`,
`kubernetes-pod`) consume it, so governance semantics stay backend-independent; `Build`
wraps it in a `batch/v1` Job. Naming is deterministic (`scrutineer-session-<session>`,
`NameFor`), labels identify ownership (`scrutineer.sh/session`).

## The pod it builds (bring-your-own-agent, capability-empty)

- **Env is advisory input, not control**: session identity (`SCRUTINEER_SESSION_*`),
  task/model (`AGENT_TASK_*`, `AGENT_MODEL_*`), and effective policy (`AGENT_POLICY_*`)
  are injected for the agent image to consume; nothing in the pod cooperates with
  enforcement. Enforcement lives out-of-pod (per-session Envoy + routing lock — see
  [`docs/design/untamperable-enforcement.md`](../../../docs/design/untamperable-enforcement.md)).
- When the profile enables the `envoy` backend, `applyAgentSidecarEnv` points the agent
  at its per-session proxy via standard `HTTP(S)_PROXY` env — a routing convenience;
  the NetworkPolicy lock is what makes the proxy mandatory.
- **Security baseline**: drop-ALL capabilities + no privilege escalation always; the
  `RuntimeProfile` merges on top — container (`runAsNonRoot`/`runAsUser`/`runAsGroup`
  — pair the first with a UID for root-default images, #82 — `readOnlyRootFilesystem`,
  `capabilities`) and pod (`seccompProfile`, `runtimeClassName`, SA-token automount
  opt-in). No ServiceAccount token is mounted unless the profile opts in.

## The drift contract (change-together invariant)

Pending Jobs are replaced, not patched, when their governed shape drifts:
`PolicyEnvDrift` compares the `managedEnvKeys` set; `RuntimeProfileDrift` compares
runtime class, pod seccomp, the agent container's SecurityContext
(`securityContextsEqual`), sidecar containers, and the automount opt-in.

**Anything `Build` adds that policy or profile can change must be visible to the
matching drift comparator** — a merged-but-uncompared field means an edit silently
does not apply to a pending Job. Concretely, these change together:

- `RuntimeProfileContainerSpec`/`RuntimeProfilePodSpec` fields
  ([`api/v1alpha1/runtimeprofile_types.go`](../../../api/v1alpha1/runtimeprofile_types.go))
  ↔ `mergeContainerSecurityContext` / `applyRuntimeProfileToPodSpec` (builder.go)
  ↔ `securityContextsEqual` / `RuntimeProfileDrift` (sync.go) ↔ regenerated CRD
  (`make manifests`) ↔ the root README's RuntimeProfile field table.
- `buildEnv` keys ↔ `managedEnvKeys` (sync.go) ↔ the `AGENT_POLICY_*` contract consumed
  by [`cmd/egress-reporter`](../../../cmd/egress-reporter/) and documented in the root
  README's env table.
- Proxy env / port wiring ↔ [`internal/enforcement/envoy`](../../enforcement/envoy/).

## Status helpers

`DescribePhase`, `TimedOut`, `BackoffExhausted` normalize Job status for the
reconciler's phase mapping (`internal/controller/agentsession`); they read only the
Job, never the cluster.

## Build / test

Pure functions — `make test` covers build, merge, drift, and status mapping; the live
path is exercised by the standard e2e suite and the quickstart/demo smoke.
