#!/usr/bin/env bash
# P5 product-level smoke test against an already running AgentMesh instance.
# This script is intentionally non-destructive: it never creates model executions
# and never shuts down the service. Real provider calls are a separate manual gate.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASE_URL="${AGENTMESH_BASE_URL:-http://127.0.0.1:8080}"
CONFIG="${AGENTMESH_CONFIG:-configs/config.yaml}"
TOKEN="${AGENTMESH_API_TOKEN:-}"
PASSPHRASE="${AGENTMESH_BACKUP_PASSPHRASE:-}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/agentmesh-p5-acceptance.XXXXXX")"
COOKIE_JAR="$TMP_DIR/cookies.txt"
BACKUP="$TMP_DIR/agentmesh-e2e.backup"
RESTORE_DIR="$TMP_DIR/restore"

cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }

need curl
need jq
need go
[[ -f "$CONFIG" ]] || fail "config file not found: $CONFIG"
[[ -n "$TOKEN" && ${#TOKEN} -ge 32 ]] || fail "set AGENTMESH_API_TOKEN (at least 32 characters)"
[[ -n "$PASSPHRASE" && ${#PASSPHRASE} -ge 12 ]] || fail "set AGENTMESH_BACKUP_PASSPHRASE (at least 12 characters)"

BASE_URL="${BASE_URL%/}"
AUTH=(-H "Authorization: Bearer $TOKEN")
request() { curl --silent --show-error --fail --max-time 10 "${AUTH[@]}" "$@"; }
expect_status() {
  local expected="$1"; shift
  local actual
  actual="$(curl --silent --show-error --output "$TMP_DIR/response" --write-out '%{http_code}' --max-time 10 "$@")"
  [[ "$actual" == "$expected" ]] || {
    echo "Expected HTTP $expected, got $actual: $*" >&2
    cat "$TMP_DIR/response" >&2 || true
    exit 1
  }
}

echo "== AgentMesh P5 product acceptance =="
echo "Base URL: $BASE_URL"
echo "Config: $CONFIG"

echo "== Runtime readiness =="
request "$BASE_URL/healthz" | jq -e '.status == "ok"' >/dev/null
request "$BASE_URL/readyz" | jq -e '.status == "ready"' >/dev/null
pass "HTTP health and dependency readiness"

echo "== API authentication boundary =="
expect_status 401 "$BASE_URL/api/v1/operations/health"
request "$BASE_URL/api/v1/operations/health" | jq -e '.database_available == true and .migration_dirty == false' >/dev/null
pass "Bearer authentication and operational health"

echo "== Dashboard login and navigation =="
curl --silent --show-error --fail --max-time 10 "$BASE_URL/dashboard/login" >/dev/null
curl --silent --show-error --output /dev/null --max-time 10 \
  --cookie-jar "$COOKIE_JAR" --cookie "$COOKIE_JAR" \
  -H "Origin: $BASE_URL" -H "Referer: $BASE_URL/dashboard/login" \
  --data-urlencode "token=$TOKEN" "$BASE_URL/dashboard/login"
grep -q "agentmesh_dashboard" "$COOKIE_JAR" || fail "Dashboard login did not issue a session cookie"
for page in /dashboard /dashboard/providers /dashboard/models /dashboard/agents /dashboard/tasks /dashboard/workflows /dashboard/executions /dashboard/usage /dashboard/operations/metrics; do
  status="$(curl --silent --show-error --output "$TMP_DIR/page" --write-out '%{http_code}' --max-time 10 --cookie "$COOKIE_JAR" "$BASE_URL$page")"
  [[ "$status" == "200" ]] || fail "Dashboard page $page returned HTTP $status"
done
pass "Dashboard session and primary pages"

echo "== Diagnostics and metrics =="
request "$BASE_URL/api/v1/diagnostics/report" | jq -e '.report_id != null and .generated_at != null' >/dev/null
request "$BASE_URL/api/v1/diagnostics/bundle" -o "$TMP_DIR/support.zip"
unzip -t "$TMP_DIR/support.zip" >/dev/null
request "$BASE_URL/metrics" | grep -q 'agentmesh_'
pass "Redacted diagnostics, support bundle and Prometheus metrics"

echo "== Usage read model =="
request "$BASE_URL/api/v1/usage/summary" | jq -e 'type == "object"' >/dev/null
request "$BASE_URL/api/v1/usage/by-model" | jq -e 'type == "object"' >/dev/null
pass "Usage summary endpoints"

echo "== Encrypted backup, verification and isolated restore staging =="
export AGENTMESH_BACKUP_PASSPHRASE="$PASSPHRASE"
go run ./cmd/agentmesh-backup create --config "$CONFIG" --output "$BACKUP" --version p5-acceptance
go run ./cmd/agentmesh-backup verify --archive "$BACKUP"
mkdir -p "$RESTORE_DIR"
go run ./cmd/agentmesh-backup restore --archive "$BACKUP" --destination "$RESTORE_DIR"
[[ -s "$RESTORE_DIR/manifest.json" ]] || fail "restore manifest missing"
[[ -s "$RESTORE_DIR/database.sqlite" || -s "$RESTORE_DIR/agentmesh.db" ]] || {
  # The archive's manifest defines the exact database filename; require at least
  # one extracted SQLite database without assuming a platform-specific name.
  find "$RESTORE_DIR" -type f -name '*.db' -o -name '*.sqlite' | grep -q . || fail "restored SQLite snapshot missing"
}
pass "Encrypted backup, archive verification and offline restore staging"

echo
echo "P5 automated product smoke test passed."
echo "Not covered: real upstream model invocation, Agent/Workflow execution semantics, and destructive restore apply."
echo "Complete the manual gates in docs/p5-acceptance.md before declaring product acceptance."
