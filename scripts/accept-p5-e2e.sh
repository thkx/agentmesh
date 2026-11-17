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

cleanup() { unset TOKEN PASSPHRASE; rm -rf "$TMP_DIR"; }
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }

need curl
need jq
need go
need unzip
[[ -f "$CONFIG" ]] || fail "config file not found: $CONFIG"
# Prompt interactively by default. Environment overrides remain optional for CI only.
if [[ -z "$TOKEN" ]]; then
  [[ -t 0 ]] || fail "interactive terminal required to enter the API token"
  read -r -s -p "AgentMesh API token (32+ characters): " TOKEN
  echo
fi
if [[ -z "$PASSPHRASE" ]]; then
  [[ -t 0 ]] || fail "interactive terminal required to enter the backup passphrase"
  read -r -s -p "Backup encryption passphrase (12+ characters): " PASSPHRASE
  echo
fi
[[ ${#TOKEN} -ge 32 ]] || fail "API token must be at least 32 characters"
[[ ${#PASSPHRASE} -ge 12 ]] || fail "backup passphrase must be at least 12 characters"

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
# Feed the passphrase over stdin; it is never exported or written to a file.
backup_cli() { printf '%s' "$PASSPHRASE" | go run ./cmd/agentmesh-backup "$@"; }
backup_cli create --config "$CONFIG" --output "$BACKUP" --version p5-acceptance
backup_cli verify --archive "$BACKUP"
mkdir -p "$RESTORE_DIR"
backup_cli restore --archive "$BACKUP" --destination "$RESTORE_DIR"
[[ -s "$RESTORE_DIR/manifest.json" ]] || fail "restore manifest missing"
[[ -s "$RESTORE_DIR/database.sqlite" ]] || fail "restored SQLite snapshot missing"
[[ -s "$RESTORE_DIR/credential.key" ]] || fail "restored credential encryption key missing"
pass "Encrypted backup, archive verification and offline restore staging"

echo
echo "P5 automated product smoke test passed."
echo "Not covered: real upstream model invocation, Agent/Workflow execution semantics, and destructive restore apply."
echo "Complete the manual gates in docs/p5-acceptance.md before declaring product acceptance."
