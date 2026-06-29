#!/usr/bin/env bash
#
# Scrutineer devcontainer bootstrap.
#
# Runs once on container create (postCreateCommand) and is safe to re-run.
#
# Responsibilities:
#   1. Verify the host Docker daemon is reachable via the bind-mounted socket.
#   2. Pre-pull Go module dependencies.
#   3. Create a local kind cluster named $KIND_CLUSTER_NAME if missing.
#   4. Attach the dev container to kind's docker network and rewrite kubeconfig
#      so the API server is reachable from inside this container.
#   5. Install the Scrutineer CRD into that cluster.
#   6. Print next steps.
#
# It does NOT build the controller image or run the controller; that flow lives
# in `make dev-up` / `make dev-deploy` so it can be invoked on demand.
#
set -euo pipefail

WORKSPACE="${WORKSPACE:-/workspaces/Scrutineer}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-scrutineer-dev}"
KIND_CONFIG="${WORKSPACE}/.devcontainer/kind-config.yaml"
KIND_UP="${WORKSPACE}/.devcontainer/kind-up.sh"

log()  { printf '\033[1;34m[scrutineer-bootstrap]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[scrutineer-bootstrap]\033[0m %s\n' "$*" >&2; }

cd_workspace() {
  if [[ -d "${WORKSPACE}" ]]; then
    cd "${WORKSPACE}"
  else
    # Devcontainer mount path can vary; fall back to the script's parent.
    cd "$(dirname "$(readlink -f "$0")")/.."
  fi
}

# With docker-outside-of-docker the docker.sock is bind-mounted from the host
# and dockerd is already running there. We just have to confirm we can reach it.
verify_docker() {
  log "verifying host docker daemon is reachable via /var/run/docker.sock..."
  if [[ ! -S /var/run/docker.sock ]]; then
    warn "/var/run/docker.sock is missing. Is the bind-mount declared in devcontainer.json?"
    exit 1
  fi
  if ! timeout 10 docker info >/dev/null 2>&1; then
    warn "docker info failed. The current user may not have permission on the socket."
    warn "Socket permissions: $(ls -l /var/run/docker.sock 2>&1)"
    warn "Current user groups: $(id)"
    exit 1
  fi
  log "docker is reachable: $(timeout 5 docker version --format '{{.Server.Version}}')"
}

go_mod_download() {
  log "pre-downloading Go module dependencies..."
  go mod download
}

ensure_kind_cluster() {
  if [[ -x "${KIND_UP}" ]]; then
    "${KIND_UP}"
  else
    warn "${KIND_UP} not executable; falling back to bare kind create."
    kind create cluster --name "${KIND_CLUSTER_NAME}" --config "${KIND_CONFIG}" --wait 90s
  fi
}

install_crd() {
  log "installing Scrutineer CRD..."
  kubectl apply -f config/crd/bases/scrutineer.sh_agentsessions.yaml
  kubectl wait --for=condition=Established \
    crd/agentsessions.scrutineer.sh --timeout=60s
}

print_next_steps() {
  cat <<EOF

\033[1;32m[scrutineer-bootstrap]\033[0m Scrutineer dev environment is ready.

  Cluster:     kind-${KIND_CLUSTER_NAME}
  CRD:         agentsessions.scrutineer.sh (installed)
  Context:     $(kubectl config current-context)

Next steps:

  # Run the controller against the kind cluster (from your host workspace):
  make run

  # In a second terminal, apply a sample AgentSession:
  kubectl apply -f config/samples/scrutineer_v1alpha1_agentsession.yaml
  kubectl get agentsessions -w

  # Or build + load + deploy the controller as a Pod in-cluster:
  make dev-deploy

  # Tear it all down:
  make dev-down

EOF
}

main() {
  cd_workspace
  verify_docker
  go_mod_download
  ensure_kind_cluster
  install_crd
  print_next_steps
}

main "$@"
