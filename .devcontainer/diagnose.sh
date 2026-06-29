#!/usr/bin/env bash
#
# .devcontainer/diagnose.sh — runtime diagnostics for the Scrutineer dev container.
#
# Run this from a second integrated terminal in Cursor *inside* the running
# devcontainer when the postCreateCommand is hanging. It probes the inner
# dockerd from multiple angles and appends NDJSON evidence to the agreed
# debug log file so the assistant can attribute the hang.
#
# Safe to run repeatedly. Read-only; never restarts dockerd or kills anything.
set -u

LOG_PATH="/workspaces/Scrutineer/.cursor/debug-a89927.log"
SESSION_ID="a89927"
RUN_ID="diagnose"

mkdir -p "$(dirname "${LOG_PATH}")" 2>/dev/null || true

emit() {
  local hypothesis="$1"; local message="$2"; local data_json="${3:-{}}"
  local ts; ts=$(date +%s%3N 2>/dev/null || echo $(($(date +%s)*1000)))
  printf '{"sessionId":"%s","runId":"%s","hypothesisId":"%s","location":"diagnose.sh","message":"%s","data":%s,"timestamp":%s}\n' \
    "${SESSION_ID}" "${RUN_ID}" "${hypothesis}" "${message}" "${data_json}" "${ts}" \
    >> "${LOG_PATH}"
}

# Helper: emit a "field" with a free-form string value (newlines flattened,
# double-quotes escaped) without relying on jq.
sanitize() {
  python3 -c 'import sys,json; print(json.dumps(sys.stdin.read()))' 2>/dev/null \
    || python -c 'import sys,json; print(json.dumps(sys.stdin.read()))'
}

section() { echo; echo "=== $* ==="; }

section "0. environment"
uname -a
whoami
id
echo "SCRUTINEER_DEVCONTAINER=${SCRUTINEER_DEVCONTAINER:-unset}"
emit "ENV" "uname" "{\"uname\":$(uname -a | sanitize),\"user\":$(whoami | sanitize),\"id\":$(id | sanitize)}"

section "1. docker socket"
ls -l /var/run/docker.sock 2>&1 || true
emit "H20" "docker_sock_ls" "{\"out\":$(ls -l /var/run/docker.sock 2>&1 | sanitize)}"

section "2. dockerd process"
ps -ef | grep -E 'dockerd|containerd|moby' | grep -v grep || echo "(no dockerd-related processes)"
emit "H21" "dockerd_processes" "{\"ps\":$(ps -ef | grep -E 'dockerd|containerd|moby' | grep -v grep 2>&1 | sanitize)}"

section "3. capabilities and cgroups"
echo "--- /proc/self/status caps ---"
grep -E '^Cap(Inh|Prm|Eff|Bnd)' /proc/self/status || true
echo "--- privileged? ---"
[[ -w /proc/sys/net/ipv4/ip_forward ]] && echo "ip_forward writable: YES" || echo "ip_forward writable: NO"
echo "--- cgroup ---"
cat /proc/self/cgroup | head -5
emit "H21" "caps_and_cgroups" "{\"caps\":$(grep -E '^Cap(Inh|Prm|Eff|Bnd)' /proc/self/status 2>&1 | sanitize),\"ip_forward_writable\":\"$([[ -w /proc/sys/net/ipv4/ip_forward ]] && echo yes || echo no)\",\"cgroup\":$(cat /proc/self/cgroup 2>&1 | head -5 | sanitize)}"

section "4. timed docker info (5s budget)"
docker_info_out=$(timeout 5 docker info 2>&1; echo "EXIT:$?")
echo "${docker_info_out}" | tail -30
emit "H20" "docker_info_timed" "{\"out\":$(echo "${docker_info_out}" | sanitize)}"

section "5. timed docker version (5s budget)"
docker_ver_out=$(timeout 5 docker version 2>&1; echo "EXIT:$?")
echo "${docker_ver_out}"
emit "H20" "docker_version_timed" "{\"out\":$(echo "${docker_ver_out}" | sanitize)}"

section "6. dockerd logs (last 60 lines if present)"
for f in /var/log/dockerd.log /var/log/docker.log /tmp/dockerd.log; do
  if [[ -f "$f" ]]; then
    echo "--- $f ---"
    tail -n 60 "$f"
    emit "H21-H24" "dockerd_log" "{\"path\":\"$f\",\"tail\":$(tail -n 60 "$f" | sanitize)}"
  fi
done

section "7. journalctl docker (if available)"
if command -v journalctl >/dev/null 2>&1; then
  journalctl -u docker --no-pager 2>&1 | tail -n 60
  emit "H21-H24" "journalctl_docker" "{\"out\":$(journalctl -u docker --no-pager 2>&1 | tail -n 60 | sanitize)}"
else
  echo "(journalctl not available)"
fi

section "8. dockerd-rootless / DinD start scripts"
ls -l /usr/local/share/docker-init.sh 2>/dev/null || true
ls -l /usr/local/bin/dockerd-entrypoint.sh 2>/dev/null || true
emit "H21" "dind_scripts" "{\"docker_init\":$(ls -l /usr/local/share/docker-init.sh 2>&1 | sanitize),\"dockerd_entrypoint\":$(ls -l /usr/local/bin/dockerd-entrypoint.sh 2>&1 | sanitize)}"

section "9. iptables sanity"
iptables -L >/dev/null 2>&1 && echo "iptables OK" || echo "iptables FAILED"
emit "H23" "iptables_probe" "{\"ok\":\"$(iptables -L >/dev/null 2>&1 && echo yes || echo no)\"}"

echo
echo "Diagnostics complete. Findings appended to: ${LOG_PATH}"
