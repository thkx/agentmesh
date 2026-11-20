#!/usr/bin/env bash
# Live process-level graceful-shutdown acceptance.
# Stops the currently running loopback AgentMesh process via the authenticated
# operations endpoint. It never deletes or replaces the database.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASE_URL="${AGENTMESH_BASE_URL:-http://127.0.0.1:8080}"
TIMEOUT_SECONDS="${AGENTMESH_SHUTDOWN_WAIT_SECONDS:-45}"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }

need go
need curl
need lsof
need ps
[[ "$BASE_URL" == http://127.0.0.1:* || "$BASE_URL" == http://localhost:* ]] || fail "this acceptance script only sends API tokens to loopback HTTP"
BASE_URL="${BASE_URL%/}"
PORT="${BASE_URL##*:}"
PORT="${PORT%%/*}"
[[ "$PORT" =~ ^[0-9]+$ ]] || fail "could not determine TCP port from AGENTMESH_BASE_URL"

echo "== AgentMesh P5 graceful-shutdown acceptance =="
echo "Target: $BASE_URL"

listener_pids() {
  lsof -nP -tiTCP:"$PORT" -sTCP:LISTEN 2>/dev/null | sort -u || true
}

PIDS="$(listener_pids)"
[[ -n "$PIDS" ]] || fail "no process is listening on port $PORT"
[[ "$(printf '%s\n' "$PIDS" | wc -l | tr -d ' ')" == "1" ]] || fail "multiple processes are listening on port $PORT; refusing to stop an ambiguous target"
PID="$PIDS"
COMMAND="$(ps -p "$PID" -o command= 2>/dev/null || true)"
[[ "$COMMAND" == *agentmesh* ]] || fail "listener PID $PID does not look like AgentMesh; refusing to stop it"

curl --silent --show-error --fail --max-time 5 "$BASE_URL/healthz" | grep -q '"status":"ok"' || fail "AgentMesh health check failed before shutdown"
pass "identified one healthy AgentMesh listener (PID $PID)"

TOKEN="${AGENTMESH_API_TOKEN:-}"
if [[ -z "$TOKEN" ]]; then
  [[ -t 0 ]] || fail "interactive terminal required to enter API token"
  read -r -s -p "AgentMesh API token: " TOKEN
  echo
fi
[[ "${#TOKEN}" -ge 32 ]] || fail "API token must be at least 32 characters"

echo "== Request authenticated graceful shutdown =="
printf '%s' "$TOKEN" | go run ./cmd/agentmesh-ops shutdown --url "$BASE_URL" --timeout 10s
unset TOKEN
pass "operations API accepted graceful shutdown"

echo "== Wait for the HTTP listener and process to exit =="
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  CURRENT="$(listener_pids)"
  if [[ -z "$CURRENT" ]] && ! kill -0 "$PID" 2>/dev/null; then
    break
  fi
  sleep 1
done
[[ -z "$(listener_pids)" ]] || fail "HTTP listener still exists after ${TIMEOUT_SECONDS}s"
kill -0 "$PID" 2>/dev/null && fail "AgentMesh process PID $PID has not exited after ${TIMEOUT_SECONDS}s"
pass "AgentMesh process exited and released its listening port"

echo
echo "Graceful shutdown live acceptance passed."
echo "Next: restart AgentMesh with the same config, then verify /readyz and confirm"
echo "the existing P5 Live Acceptance Workflow Run is still visible in Dashboard."
echo "No database, credential, provider, model, agent, workspace or workflow data was deleted."
