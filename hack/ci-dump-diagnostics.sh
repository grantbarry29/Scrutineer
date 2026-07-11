#!/usr/bin/env bash
# ci-dump-diagnostics.sh — post-mortem dump for cluster CI jobs (#110).
#
# Run from an `if: failure()` workflow step BEFORE teardown: once the kind cluster
# is deleted, the controller logs, pod states, and events are unrecoverable (the
# #109 diagnosis had to be reconstructed from timing side-channels). Everything here
# is best-effort — the cluster may be half-created or already gone — so every command
# tolerates failure and the script always exits 0.
#
# Targets the current kube-context (kind sets it on cluster create); override with
# KUBE_CONTEXT=<ctx> for jobs that create more than one cluster.

set -u

KUBECTL=(kubectl)
if [ -n "${KUBE_CONTEXT:-}" ]; then
  KUBECTL+=(--context "${KUBE_CONTEXT}")
fi

section() {
  echo
  echo "===== $* ====="
}

section "kube context / nodes"
if [ -n "${KUBE_CONTEXT:-}" ]; then
  echo "KUBE_CONTEXT=${KUBE_CONTEXT}"
else
  kubectl config current-context 2>/dev/null || echo "(no current context)"
fi
"${KUBECTL[@]}" get nodes -o wide 2>/dev/null || true

section "pods (all namespaces)"
"${KUBECTL[@]}" get pods -A -o wide 2>/dev/null || true

section "controller-manager logs (last 400 lines)"
"${KUBECTL[@]}" -n scrutineer-system logs deployment/scrutineer-controller-manager \
  --tail=400 2>/dev/null || echo "(controller deployment absent or unreadable)"

section "controller-manager logs (previous container, if restarted)"
"${KUBECTL[@]}" -n scrutineer-system logs deployment/scrutineer-controller-manager \
  --previous --tail=200 2>/dev/null || echo "(no previous container)"

section "events: scrutineer-system (by lastTimestamp)"
"${KUBECTL[@]}" -n scrutineer-system get events --sort-by=.lastTimestamp 2>/dev/null || true

section "events: default (by lastTimestamp, last 80)"
"${KUBECTL[@]}" -n default get events --sort-by=.lastTimestamp 2>/dev/null | tail -80 || true

section "AgentSessions (all namespaces)"
"${KUBECTL[@]}" get agentsessions -A 2>/dev/null || echo "(CRD not installed)"

# Full describe per session: phase, conditions, policyDecisions, events — the
# controller's whole verdict about why a session is stuck.
for ns_name in $("${KUBECTL[@]}" get agentsessions -A \
  -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null); do
  ns=${ns_name%%/*}
  name=${ns_name##*/}
  section "describe agentsession ${ns_name}"
  "${KUBECTL[@]}" -n "${ns}" describe agentsession "${name}" 2>/dev/null || true
done

# The per-session egress-proxy pods carry the evidence pipeline (envoy +
# egress-reporter containers); their logs explain enforcement/evidence failures.
for ns_name in $("${KUBECTL[@]}" get pods -A -l app.kubernetes.io/component=egress-proxy \
  -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null); do
  ns=${ns_name%%/*}
  name=${ns_name##*/}
  section "describe egress-proxy pod ${ns_name}"
  "${KUBECTL[@]}" -n "${ns}" describe pod "${name}" 2>/dev/null || true
  section "egress-proxy pod ${ns_name} logs (all containers, last 100 lines each)"
  "${KUBECTL[@]}" -n "${ns}" logs "${name}" --all-containers --tail=100 2>/dev/null || true
done

# Agent-side workloads (demo/e2e session Jobs) — what the agent itself saw.
for ns_name in $("${KUBECTL[@]}" get pods -A -l scrutineer.sh/session \
  -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null); do
  ns=${ns_name%%/*}
  name=${ns_name##*/}
  section "agent pod ${ns_name} logs (last 100 lines)"
  "${KUBECTL[@]}" -n "${ns}" logs "${name}" --all-containers --tail=100 2>/dev/null || true
done

section "diagnostics dump complete"
exit 0
