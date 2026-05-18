ARG VARIANT=1-1.23-bookworm
FROM mcr.microsoft.com/devcontainers/go:${VARIANT}

# Upstream image ships with a pre-configured Yarn APT source whose GPG signing
# key was rotated upstream; `apt-get update` therefore exits non-zero, which
# breaks every devcontainer Feature that runs apt during install
# (docker-in-docker, kubectl-helm-minikube, ...).
#
# Relay does not use Yarn, so we simply remove the offending list before any
# Feature scripts run. Also refresh the APT cache once so Features start clean.
USER root
RUN rm -f /etc/apt/sources.list.d/yarn.list \
 && apt-get update \
 && rm -rf /var/lib/apt/lists/*
