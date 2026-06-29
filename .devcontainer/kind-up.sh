#!/usr/bin/env bash
#
# Bring up a kind cluster for Scrutineer, working around two known issues with the
# combination of kind v0.31.0 + Docker Desktop linuxkit on arm64:
#
#   1. `kind create cluster` aborts at the "remove control-plane taint" step
#      because kube-apiserver hasn't bound its advertise IP yet by the time
#      kind probes it. The pods *do* come up a second later, but kind has
#      already errored out and (without --retain) destroyed the node.
#
#   2. Because kind aborts during "Starting control-plane", the subsequent
#      "Installing CNI" step never runs, so the node stays NotReady forever.
#
# This script:
#   * Creates the cluster with `--retain` so the node survives a flaky abort.
#   * Verifies kube-apiserver is actually serving (via the in-node socket).
#   * Installs kindnet manually if kind never got to that step.
#   * Removes the control-plane taint so workloads can schedule on the
#     single-node cluster.
#   * Calls kind-attach.sh to wire the dev container onto kind's docker
#     network and export the --internal kubeconfig.
#
# Idempotent. Safe to re-run.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
WORKSPACE="${WORKSPACE:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-scrutineer-dev}"
KIND_CONFIG="${KIND_CONFIG:-${WORKSPACE}/.devcontainer/kind-config.yaml}"
KIND_ATTACH="${SCRIPT_DIR}/kind-attach.sh"
KINDNET_MANIFEST="${KINDNET_MANIFEST:-https://raw.githubusercontent.com/aojea/kindnet/v1.7.0/install-kindnet.yaml}"

log()  { printf '\033[1;34m[kind-up]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[kind-up]\033[0m %s\n' "$*" >&2; }

require() {
  command -v "$1" >/dev/null 2>&1 || { warn "missing required tool: $1"; exit 1; }
}

require kind
require docker
require kubectl

node_name() { echo "${KIND_CLUSTER_NAME}-control-plane"; }

apiserver_healthy_inside_node() {
  # Probe the in-node kube-apiserver directly. This bypasses any host port
  # mapping and any docker-network reachability concerns.
  docker exec "$(node_name)" \
    curl -sk --max-time 5 https://localhost:6443/healthz 2>/dev/null \
    | grep -q '^ok$'
}

create_cluster_if_missing() {
  if kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    log "kind cluster '${KIND_CLUSTER_NAME}' already exists."
    return 0
  fi
  log "creating kind cluster '${KIND_CLUSTER_NAME}' (--retain so a flaky abort leaves the node up)..."
  # We intentionally tolerate non-zero exit; kind sometimes errors on the
  # taint-removal step even though the control-plane is healthy.
  set +e
  kind create cluster \
    --name "${KIND_CLUSTER_NAME}" \
    --config "${KIND_CONFIG}" \
    --wait 90s \
    --retain
  local rc=$?
  set -e
  if (( rc != 0 )); then
    warn "kind create cluster exited non-zero (rc=${rc}); will verify and finalize manually."
  fi
}

verify_apiserver() {
  log "verifying kube-apiserver is responding inside node '$(node_name)'..."
  local tries=0
  while (( tries < 30 )); do
    tries=$((tries + 1))
    if apiserver_healthy_inside_node; then
      log "kube-apiserver healthy after ${tries} probe(s)."
      return 0
    fi
    sleep 2
  done
  warn "kube-apiserver never returned /healthz=ok inside the node container."
  warn "Last 30 lines of kubelet logs:"
  docker exec "$(node_name)" journalctl -u kubelet --no-pager -n 30 >&2 || true
  exit 1
}

ensure_cni() {
  # If kind aborted during "Starting control-plane", the CNI install never ran.
  # Detect by checking whether any kindnet pod exists on the node.
  if kubectl get ds -n kube-system kindnet >/dev/null 2>&1; then
    log "kindnet DaemonSet already present."
    return 0
  fi
  log "installing kindnet CNI manually (kind aborted before it could)..."
  kubectl apply -f "${KINDNET_MANIFEST}"
}

remove_control_plane_taint() {
  local node; node="$(node_name)"
  if kubectl get node "${node}" -o jsonpath='{.spec.taints[*].key}' 2>/dev/null \
       | tr ' ' '\n' | grep -qx 'node-role.kubernetes.io/control-plane'; then
    log "removing control-plane taint from '${node}' (kind would normally do this)..."
    kubectl taint nodes "${node}" node-role.kubernetes.io/control-plane- || true
  else
    log "control-plane taint already absent."
  fi
}

attach_and_export() {
  if [[ -x "${KIND_ATTACH}" ]]; then
    "${KIND_ATTACH}"
  else
    warn "${KIND_ATTACH} not executable; skipping dev-container network attach."
  fi
}

wait_for_node_ready() {
  log "waiting for node '$(node_name)' to become Ready..."
  kubectl wait --for=condition=Ready "node/$(node_name)" --timeout=90s
}

main() {
  create_cluster_if_missing
  verify_apiserver

  # The internal kubeconfig is what makes `kubectl` work from inside the dev
  # container. We attach + export it now so the rest of the script (CNI install,
  # taint removal, node-ready wait) can use plain kubectl.
  attach_and_export

  ensure_cni
  remove_control_plane_taint
  wait_for_node_ready
  log "kind cluster '${KIND_CLUSTER_NAME}' is ready."
}

main "$@"
