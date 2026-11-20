#!/usr/bin/env bash
# Live P6 gate: real Coding Execution + real model + Docker Sandbox + tests + reviewer.
# The repository is changed only inside AgentMesh's task Worktree; this script never commits or pushes.
set -euo pipefail

BASE_URL="${AGENTMESH_BASE_URL:-http://127.0.0.1:8080}"
TOKEN="${AGENTMESH_API_TOKEN:-}"
if [[ -z "$TOKEN" && -n "${AGENTMESH_API_TOKEN_FILE:-}" ]]; then
  TOKEN="$(cat "$AGENTMESH_API_TOKEN_FILE")"
fi
REPOSITORY="${P6_REPOSITORY_PATH:-}"
MODEL_ID="${P6_MODEL_ID:-}"
TEST_COMMAND="${P6_TEST_COMMAND_JSON:-}"
TASK_PROMPT="${P6_TASK_PROMPT:-Create a file named p6-live-acceptance.txt containing exactly this line: AgentMesh P6 live coding acceptance passed. Do not change any other files.}"
TIMEOUT_SECONDS="${P6_TIMEOUT_SECONDS:-1800}"
POLL_SECONDS="${P6_POLL_SECONDS:-5}"

fail() { echo "FAIL: $*" >&2; exit 1; }
command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
command -v docker >/dev/null || fail "Docker CLI is required"
[[ -n "$TOKEN" ]] || fail "set AGENTMESH_API_TOKEN or AGENTMESH_API_TOKEN_FILE"
[[ -n "$REPOSITORY" && -d "$REPOSITORY" ]] || fail "P6_REPOSITORY_PATH must point to a repository visible to the AgentMesh server"
[[ -n "$MODEL_ID" ]] || fail "P6_MODEL_ID is required"
[[ -n "$TEST_COMMAND" ]] || fail "P6_TEST_COMMAND_JSON is required, for example {\"program\":\"go\",\"args\":[\"test\",\"./...\"]}"
docker info >/dev/null 2>&1 || fail "Docker daemon is not reachable"
git -C "$REPOSITORY" rev-parse --show-toplevel >/dev/null 2>&1 || fail "P6_REPOSITORY_PATH is not a Git repository"

api() {
  curl --silent --show-error --fail \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" "$@"
}

TASK_ID="p6-live-$(date +%s)-$$"
INPUT="$(jq -cn \
  --arg id "$TASK_ID" \
  --arg title "P6 live Coding acceptance" \
  --arg repository_path "$REPOSITORY" \
  --arg model_id "$MODEL_ID" \
  --arg prompt "$TASK_PROMPT" \
  --argjson test_command "$TEST_COMMAND" \
  --argjson max_repair_attempts 1 \
  --argjson timeout_seconds "$TIMEOUT_SECONDS" \
  '{id:$id,title:$title,repository_path:$repository_path,model_id:$model_id,prompt:$prompt,test_command:$test_command,max_repair_attempts:$max_repair_attempts,timeout_seconds:$timeout_seconds}')"

SUBMITTED="$(api -X POST "$BASE_URL/api/v1/managed-executions" \
  -d "$(jq -cn --arg task_id "$TASK_ID" --argjson input "$INPUT" \
    '{kind:"coding",runtime:"coding",task_id:$task_id,input:$input,idempotency_key:("p6-live:"+$task_id)}')")"
EXECUTION_ID="$(jq -r '.id' <<<"$SUBMITTED")"
[[ -n "$EXECUTION_ID" && "$EXECUTION_ID" != "null" ]] || fail "managed execution was not created: $SUBMITTED"
echo "PASS: canonical Coding Execution created ($EXECUTION_ID)"

deadline=$(( $(date +%s) + TIMEOUT_SECONDS + 180 ))
while true; do
  EXECUTION="$(api "$BASE_URL/api/v1/managed-executions/$EXECUTION_ID")"
  STATUS="$(jq -r '.status' <<<"$EXECUTION")"
  case "$STATUS" in
    succeeded) break ;;
    failed|canceled|timed_out|interrupted|needs_reconciliation)
      fail "Coding Execution ended as $STATUS: $(jq -r '.error // ""' <<<"$EXECUTION")" ;;
  esac
  (( $(date +%s) < deadline )) || fail "timed out waiting for Coding Execution"
  sleep "$POLL_SECONDS"
done
echo "PASS: Coding Execution succeeded"

TASK="$(api "$BASE_URL/api/v1/coding/tasks/$TASK_ID")"
[[ "$(jq -r '.status' <<<"$TASK")" == "succeeded" ]] || fail "Coding Task is not succeeded"
[[ "$(jq -r '.execution_id' <<<"$TASK")" == "$EXECUTION_ID" ]] || fail "Coding Task is not bound to the canonical Execution"
[[ "$(jq -r '.attempt_id' <<<"$TASK")" == "$EXECUTION_ID-a-1" ]] || fail "Coding Task is not bound to the canonical Attempt"
echo "PASS: Coding Task is linked to Execution and Attempt"

STEPS="$(api "$BASE_URL/api/v1/coding/tasks/$TASK_ID/steps")"
jq -e 'any(.[]; .kind=="test.run" and .status=="succeeded" and .exit_code==0)' <<<"$STEPS" >/dev/null || fail "successful Docker test evidence is missing"
jq -e 'any(.[]; .kind=="review.inspect" and .status=="approve")' <<<"$STEPS" >/dev/null || fail "independent reviewer approval is missing"
echo "PASS: Docker test and independent review evidence persisted"

DIFF="$(curl --silent --show-error --fail -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/v1/coding/tasks/$TASK_ID/diff")"
grep -q "p6-live-acceptance.txt" <<<"$DIFF" || fail "expected live acceptance patch is missing"
SNAPSHOTS="$(api "$BASE_URL/api/v1/coding/tasks/$TASK_ID/snapshots")"
jq -e 'any(.[]; .label=="final" and (.diff|length)>0)' <<<"$SNAPSHOTS" >/dev/null || fail "final diff snapshot is missing"
DETAIL="$(api "$BASE_URL/api/v1/managed-executions/$EXECUTION_ID/detail")"
jq -e --arg id "$EXECUTION_ID" 'any(.attempts[]; .id==($id+"-a-1") and .status=="succeeded")' <<<"$DETAIL" >/dev/null || fail "canonical Attempt is not succeeded"
jq -e 'any(.usage_events[]?; .execution_id != null)' <<<"$DETAIL" >/dev/null 2>&1 || true

echo "PASS: final diff, snapshot and canonical Attempt verified"
echo "Coding Task: $TASK_ID"
echo "Execution: $EXECUTION_ID"
echo "Worktree retained for manual inspection; no commit or push was performed."
