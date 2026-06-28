#!/usr/bin/env bash
# PostToolUse(Write|Edit): drop a sentinel when a *code* file is edited, so the
# Stop hook knows a rule self-review is owed this turn. Never blocks.
set -euo pipefail

input=$(cat)
fp=$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty')
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')

[ -n "$fp" ] || exit 0
[ -n "$cwd" ] || exit 0

case "$fp" in
  *.go|*/api/*|*/config/crd/*|*/config/samples/*|*/cmd/*|*/internal/*|*Dockerfile*|*/go.mod|*/go.sum)
    mkdir -p "$cwd/.claude"
    : > "$cwd/.claude/.rules-review-pending"
    ;;
esac
exit 0
