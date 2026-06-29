#!/usr/bin/env bash
#
# Attach the running dev container to kind's docker network and rewrite
# ~/.kube/config so the API server is reachable from inside the dev container.
#
# Background: with docker-outside-of-docker, `kind create cluster` runs on the
# host's Docker daemon. The default kubeconfig emitted by kind points at
# https://127.0.0.1:<random-port>, which is the *host* loopback — not reachable
# from inside the dev container. `kind get kubeconfig --internal` emits a
# kubeconfig pointing at https://<cluster-name>-control-plane:6443, but that
# hostname only resolves on the `kind` docker network, so we also have to
# attach this container to that network.
#
# Idempotent: safe to run repeatedly. No-op when not in a devcontainer.
set -euo pipefail

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-scrutineer-dev}"
KIND_NETWORK="${KIND_NETWORK:-kind}"

log()  { printf '\033[1;34m[kind-attach]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[kind-attach]\033[0m %s\n' "$*" >&2; }

if [[ "${SCRUTINEER_DEVCONTAINER:-0}" != "1" ]]; then
  log "not running inside the Scrutineer dev container (SCRUTINEER_DEVCONTAINER!=1); skipping."
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  warn "docker CLI not found on PATH; skipping."
  exit 0
fi

if ! docker info >/dev/null 2>&1; then
  warn "cannot reach docker daemon via /var/run/docker.sock; skipping."
  exit 0
fi

if ! kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
  warn "kind cluster '${KIND_CLUSTER_NAME}' does not exist yet; nothing to attach."
  exit 0
fi

if ! docker network inspect "${KIND_NETWORK}" >/dev/null 2>&1; then
  warn "docker network '${KIND_NETWORK}' not found; kind may not have created it yet."
  exit 0
fi

# The devcontainer's hostname is the short form of its docker container ID,
# which is a valid container reference for `docker {inspect,network connect}`.
SELF="${HOSTNAME:-$(cat /etc/hostname)}"

# Resolve the container's full ID so we can compare against the keys of the
# network's Containers map (those keys are full container IDs, not names).
SELF_ID="$(docker inspect --format '{{.Id}}' "${SELF}" 2>/dev/null || true)"
if [[ -z "${SELF_ID}" ]]; then
  warn "could not resolve container ID for '${SELF}'; is this really the devcontainer?"
  exit 1
fi

if docker network inspect "${KIND_NETWORK}" \
    --format '{{range $id, $c := .Containers}}{{$id}}{{"\n"}}{{end}}' \
    | grep -qx "${SELF_ID}"; then
  log "dev container '${SELF}' already attached to '${KIND_NETWORK}' network."
else
  log "attaching dev container '${SELF}' to '${KIND_NETWORK}' network..."
  docker network connect "${KIND_NETWORK}" "${SELF_ID}"
fi

log "exporting --internal kubeconfig for kind cluster '${KIND_CLUSTER_NAME}'..."
mkdir -p "${HOME}/.kube"
kind export kubeconfig --name "${KIND_CLUSTER_NAME}" --internal >/dev/null

# Sanity check: the API server should now be reachable.
if kubectl --request-timeout=5s cluster-info >/dev/null 2>&1; then
  log "kube-apiserver reachable via internal kubeconfig."
else
  warn "kubectl cluster-info failed after attach + internal kubeconfig export."
  warn "Try: docker network inspect ${KIND_NETWORK} && kubectl cluster-info"
  exit 1
fi
