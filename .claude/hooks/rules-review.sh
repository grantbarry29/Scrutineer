#!/usr/bin/env bash
# Stop: if code was edited this turn (sentinel present), block the stop once and
# require the post-change rule self-review described in CLAUDE.md. Removing the
# sentinel guarantees termination — the next stop is allowed unless new code edits
# re-create it.
set -euo pipefail

input=$(cat)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] || exit 0

sentinel="$cwd/.claude/.rules-review-pending"
[ -f "$sentinel" ] || exit 0
rm -f "$sentinel"

files=$(cd "$cwd" 2>/dev/null && git status --porcelain 2>/dev/null \
  | cut -c4- \
  | grep -E '\.go$|^api/|^config/crd/|^config/samples/|^cmd/|^internal/|^Dockerfile|^go\.(mod|sum)$' \
  | sed 's/^/  - /' || true)

reason="Code changed this turn — complete the post-change rule self-review from CLAUDE.md before finishing.

Changed code files (uncommitted):
${files:-  (run: git status --porcelain)}

For each changed path, open the matching rule(s) in dev-agent-rules/ via the CLAUDE.md table — kubernetes-controller, crd-api-design, distributed-systems-networking, component-binaries — and audit the diff against each rule's \"Anti-Patterns To Reject\" and \"Highest Priority\" sections. Also confirm the always-on rules held: the component README was updated in the same change (component-docs), and scope stayed narrow with any out-of-scope work filed as a GitHub issue (task-management).

Report what you checked, then fix or explicitly call out any violation. This gate has cleared for this turn — after the review you may stop normally."

jq -n --arg r "$reason" '{decision: "block", reason: $r}'
exit 0
