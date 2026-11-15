#!/usr/bin/env bash
set -Eeuo pipefail

# AgentMesh P3 verification: static checks and read-only runtime API smoke tests.
# Usage:
#   bash scripts/test-p3.sh
#   API_BASE_URL=http://127.0.0.1:8080 API_TOKEN=... bash scripts/test-p3.sh
#   P3_WORKSPACE_ID=... P3_AGENT_ID=... bash scripts/test-p3.sh
#   SKIP_CODE_CHECKS=1 bash scripts/test-p3.sh
#   SKIP_API_CHECKS=1 bash scripts/test-p3.sh
#
# Runtime checks do not create, update, or delete business data.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_BASE_URL="\${API_BASE_URL:-http://127.0.0.1:8080}"
API_TOKEN="\${API_TOKEN:-}"
SKIP_CODE_CHECKS="\${SKIP_CODE_CHECKS:-0}"
SKIP_API_CHECKS="\${SKIP_API_CHECKS:-0}"
PASS_COUNT=0
FAIL_COUNT=0
WARN_COUNT=0
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '\033[32m[PASS]\033[0m %s\n' "$1"
}
fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    printf '\033[31m[FAIL]\033[0m %s\n' "$1"
}
warn() {
    WARN_COUNT=$((WARN_COUNT + 1))
    printf '\033[33m[WARN]\033[0m %s\n' "$1"
}
section() {
    printf '\n\033[1;36m== %s ==\033[0m\n' "$1"
}
require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "Required command not found: $1" >&2
        exit 2
    fi
}
run_check() {
    name="$1"
    shift
    printf '\n-- %s --\n' "$name"
    if "$@"; then pass "$name"; else fail "$name"; fi
}
api_request() {
    method="$1"
    path="$2"
    output="$3"
    if [ -n "$API_TOKEN" ]; then
        curl --silent --show-error --connect-timeout 3 --max-time 15 \
            -X "$method" -H "Authorization: Bearer $API_TOKEN" \
            -o "$output" -w "%{http_code}" "$API_BASE_URL$path"
    else
        curl --silent --show-error --connect-timeout 3 --max-time 15 \
            -X "$method" -o "$output" -w "%{http_code}" "$API_BASE_URL$path"
    fi
}
check_http() {
    name="$1"; path="$2"; expected="$3"
    output="$TMP_DIR/response.json"
    if ! status="$(api_request GET "$path" "$output")"; then
        fail "$name: request failed"; return
    fi
    if [ "$status" != "$expected" ]; then
        fail "$name: expected HTTP $expected, got $status"
        cat "$output"; return
    fi
    if ! jq empty "$output" >/dev/null 2>&1; then
        fail "$name: response is not valid JSON"
        cat "$output"; return
    fi
    pass "$name (HTTP $status)"
}
check_array() {
    name="$1"; path="$2"; expression="$3"
    output="$TMP_DIR/response.json"
    if ! status="$(api_request GET "$path" "$output")"; then
        fail "$name: request failed"; return
    fi
    if [ "$status" != "200" ]; then
        fail "$name: expected HTTP 200, got $status"
        cat "$output"; return
    fi
    if ! jq -e "$expression | type == \"array\"" "$output" >/dev/null 2>&1; then
        fail "$name: expected JSON array at $expression"
        cat "$output"; return
    fi
    count="$(jq -r "$expression | length" "$output")"
    pass "$name ($count items)"
}

section "Environment"
require_command go
require_command curl
require_command jq
require_command find
echo "Go: $(go version)"
echo "API: $API_BASE_URL"

if [ "$SKIP_CODE_CHECKS" != "1" ]; then
    section "1. Code verification"
    unformatted="$(find . -type f -name '*.go' -not -path './.git/*' -print0 | xargs -0 gofmt -l)"
    if [ -z "$unformatted" ]; then pass "gofmt"; else
        fail "gofmt"; printf '%s\n' "$unformatted"
    fi
    if command -v sqlc >/dev/null 2>&1; then
        run_check "sqlc generate" sqlc generate
    else
        warn "sqlc is not installed; generation skipped"
    fi
    run_check "go build ./..." go build ./...
    run_check "go vet ./..." go vet ./...
    run_check "go test -race ./..." go test -race ./...
else
    warn "Code checks skipped by SKIP_CODE_CHECKS=1"
fi

if [ "$SKIP_API_CHECKS" != "1" ]; then
    section "2. Runtime API smoke tests"
    health_output="$TMP_DIR/health.json"
    if ! health_status="$(api_request GET "/healthz" "$health_output")"; then
        warn "AgentMesh is not reachable; runtime API checks skipped"
    elif [ "$health_status" != "200" ]; then
        fail "Health endpoint: expected HTTP 200, got $health_status"
        cat "$health_output"
    else
        check_http "Health endpoint" "/healthz" "200"
        check_http "Readiness endpoint" "/readyz" "200"

        section "3. Provider and model catalog"
        check_array "Model catalog" "/api/v1/models" '.data'
        check_array "Provider catalog" "/api/v1/providers" '.data'

        models_output="$TMP_DIR/models.json"
        if status="$(api_request GET "/api/v1/models" "$models_output")" && [ "$status" = "200" ]; then
            dynamic_count="$(jq '[.data[]? | select(.source == "ai_resource")] | length' "$models_output")"
            if [ "$dynamic_count" -gt 0 ]; then
                pass "AI Resource dynamic model catalog ($dynamic_count models)"
            else
                warn "No AI Resource dynamic models are configured"
            fi
        fi

        section "4. Collaboration API"
        check_array "Workspace list" "/api/v1/collaboration/workspaces" '.items'
        check_array "Outbox list" "/api/v1/collaboration/outbox" '.items'
        check_array "Outbox dead letters" "/api/v1/collaboration/outbox/dead-letters" '.items'

        section "5. Optional Workspace and Agent checks"
        if [ -n "\${P3_WORKSPACE_ID:-}" ]; then
            check_http "Workspace detail" "/api/v1/collaboration/workspaces/$P3_WORKSPACE_ID" "200"
            check_array "Workspace agents" "/api/v1/collaboration/workspaces/$P3_WORKSPACE_ID/agents" '.items'
            check_array "Workspace channels" "/api/v1/collaboration/workspaces/$P3_WORKSPACE_ID/channels" '.items'
            check_array "Workspace tasks" "/api/v1/collaboration/workspaces/$P3_WORKSPACE_ID/tasks" '.items'
            check_array "Workspace artifacts" "/api/v1/collaboration/workspaces/$P3_WORKSPACE_ID/artifacts" '.items'
            check_array "Workspace approvals" "/api/v1/collaboration/workspaces/$P3_WORKSPACE_ID/approvals" '.items'
        else
            warn "P3_WORKSPACE_ID not set; workspace-specific checks skipped"
        fi
        if [ -n "\${P3_AGENT_ID:-}" ]; then
            check_http "Agent detail" "/api/v1/collaboration/agents/$P3_AGENT_ID" "200"
            check_http "Agent session" "/api/v1/collaboration/agents/$P3_AGENT_ID/session" "200"
            check_http "Agent transcript" "/api/v1/collaboration/agents/$P3_AGENT_ID/session/transcript" "200"
            check_array "Agent decision audit" "/api/v1/collaboration/agents/$P3_AGENT_ID/decision-audit" '.items'
        else
            warn "P3_AGENT_ID not set; agent-specific checks skipped"
        fi
    fi
else
    warn "Runtime API checks skipped by SKIP_API_CHECKS=1"
fi

section "Summary"
printf 'Passed:  %s\nFailed:  %s\nWarnings: %s\n' "$PASS_COUNT" "$FAIL_COUNT" "$WARN_COUNT"
if [ "$FAIL_COUNT" -gt 0 ]; then
    echo "P3 verification FAILED"
    exit 1
fi
echo "P3 verification completed without failed checks."
