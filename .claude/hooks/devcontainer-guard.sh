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
# Quoted strings are stripped before matching so commit messages and grep
# patterns can't false-positive. The strip must handle MULTI-LINE quoted args
# (a `git commit -m "…"` body, a `gh issue comment --body "…"`): sed is
# line-based, so newlines are collapsed to spaces first — otherwise the interior
# lines survive the strip and any `make …`/`go test …` prose in them trips the
# deny (#161). Note this keeps the guard's reach intact for CHAINED host runs:
# `git commit -m "x" && make test` still strips only the quoted "x", leaving the
# real `make test` to match — exempting git/gh wholesale would miss that.
#
# Fail open: any parsing problem allows the command — this guard must never
# break an unrelated tool call.
set -uo pipefail

readonly TRIGGER_RE='(^|[;&|([:space:]])make([[:space:]]|$)|(^|[;&|([:space:]])go[[:space:]]+(test|run|install|mod|generate)([[:space:]]|$)'

# decide COMMAND -> exit 2 to block a host toolchain run, 0 to allow.
decide() {
  local cmd="$1" stripped
  # Containerized runs are the sanctioned path.
  case "$cmd" in
    *"docker exec"*|*"devcontainer exec"*) return 0 ;;
  esac
  # Collapse newlines so a multi-line quoted arg is a single line, then strip
  # quoted runs (single- and double-quoted).
  stripped=$(printf '%s' "$cmd" | tr '\n' ' ' | sed -e "s/'[^']*'//g" -e 's/"[^"]*"//g')
  if printf '%s' "$stripped" | grep -qE "$TRIGGER_RE"; then
    return 2
  fi
  return 0
}

# --self-test: run the case table and report; non-zero exit on any mismatch.
# Verifies the #161 multi-line-quoted-arg regression alongside the #134 table.
if [ "${1:-}" = "--self-test" ]; then
  fails=0
  # Each case: "expected<TAB>command"; expected is "allow" or "block".
  run_case() {
    local expect="$1" cmd="$2" rc got
    decide "$cmd"; rc=$?
    if [ "$rc" -eq 2 ]; then got=block; else got=allow; fi
    if [ "$got" != "$expect" ]; then
      fails=$((fails+1))
      printf 'FAIL  expected=%-5s got=%-5s :: %s\n' "$expect" "$got" "$(printf '%s' "$cmd" | tr '\n' '|')"
    fi
  }
  # #161 regressions: multi-line quoted message/body bodies carrying trigger words.
  run_case allow "$(printf 'git commit -m "fix\nmake test-e2e-net runs in the container\ndone"')"
  run_case allow "$(printf 'gh issue close 9 --comment "closed\ngo test ./... passed\nok"')"
  run_case allow 'git commit -F /tmp/msg.txt'
  run_case allow 'gh issue comment 9 --body-file /tmp/body.md'
  run_case allow 'git commit -m "remember to run make test in the devcontainer"'
  # #134 table: genuine host toolchain runs still blocked.
  run_case block 'make test'
  run_case block 'make'
  run_case block 'go test ./...'
  run_case block 'go mod tidy'
  run_case block 'go generate ./...'
  run_case block 'go run ./cmd/main.go'
  # Chained host toolchain after a git command must still be caught.
  run_case block 'git commit -m "msg" && make test'
  # Allowed host sanity checks and the sanctioned containerized path.
  run_case allow 'go build ./...'
  run_case allow 'go vet ./...'
  run_case allow 'docker exec -w /workspaces/Scrutineer c make test'
  if [ "$fails" -eq 0 ]; then
    echo "devcontainer-guard self-test: all cases passed"
    exit 0
  fi
  echo "devcontainer-guard self-test: $fails case(s) failed" >&2
  exit 1
fi

command -v jq >/dev/null 2>&1 || exit 0
input=$(cat) || exit 0
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null) || exit 0
[ -n "$cmd" ] || exit 0

decide "$cmd"
if [ "$?" -eq 2 ]; then
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
