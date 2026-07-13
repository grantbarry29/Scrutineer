#!/usr/bin/env bash
# PreToolUse(Bash): deny host-side toolchain runs that the devcontainer rule
# forbids (dev-agent-rules/devcontainer.md) — the pinned Go 1.23 toolchain
# lives in the devcontainer, and host runs of make/codegen/envtest produce
# failures that look like project bugs but are not (#54).
#
# Matcher is deliberately conservative: any `make` target (the rule requires
# every one to run containerized) plus the toolchain-sensitive go subcommands
# (test|run|install|mod|generate). Host `go build` / `go vet` stay allowed as
# quick sanity checks, exactly as the rule permits. `go test` cannot be told
# apart from an envtest suite here, so it routes to the container too.
#
# Fail open: any parsing problem allows the command — this guard must never
# break an unrelated tool call.
set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0
input=$(cat) || exit 0
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null) || exit 0
[ -n "$cmd" ] || exit 0

# Containerized runs are the sanctioned path.
case "$cmd" in
  *"docker exec"*|*"devcontainer exec"*) exit 0 ;;
esac

# Strip quoted strings so commit messages and grep patterns can't false-positive.
stripped=$(printf '%s' "$cmd" | sed -e "s/'[^']*'//g" -e 's/"[^"]*"//g')

if printf '%s' "$stripped" | grep -qE '(^|[;&|([:space:]])make([[:space:]]|$)|(^|[;&|([:space:]])go[[:space:]]+(test|run|install|mod|generate)([[:space:]]|$)'; then
  container=$(docker ps --format '{{.Names}}\t{{.Image}}' 2>/dev/null | awk -F'\t' '$2 ~ /^vsc-scrutineer/ {print $1; exit}')
  {
    echo "devcontainer-guard: host-side toolchain run blocked (see dev-agent-rules/devcontainer.md)."
    echo "Run it in the devcontainer instead:"
    if [ -n "$container" ]; then
      echo "  docker exec -w /workspaces/Scrutineer $container <command>"
    else
      echo "  docker exec -w /workspaces/Scrutineer <container> <command>"
      echo "  (find it: docker ps --format '{{.Names}} {{.Image}}' | grep vsc-scrutineer — if none, start the devcontainer first)"
    fi
  } >&2
  exit 2
fi
exit 0
