#!/usr/bin/env bash
# Live P6 gate: durable Workflow -> Coding Execution -> real model -> Docker Sandbox
# -> tests -> independent review -> P4 Artifact/Evidence provenance.
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
[[ -n "$TEST_COMMAND" ]] || fail 'P6_TEST_COMMAND_JSON is required, for example {"program":"go","args":["test","./..."]}'
docker info >/dev/null 2>&1 || fail "Docker daemon is not reachable"
git -C "$REPOSITORY" rev-parse --show-toplevel >/dev/null 2>&1 || fail "P6_REPOSITORY_PATH is not a Git repository"
git -C "$REPOSITORY" rev-parse --verify HEAD >/dev/null 2>&1 || fail "P6_REPOSITORY_PATH must contain at least one commit"

api() {
  curl --silent --show-error --fail \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" "$@"
}

STAMP="$(date +%s)-$$"
WORKFLOW_ID="p6-live-workflow-$STAMP"
RUN_ID="p6-live-run-$STAMP"
WORKFLOW="$(jq -cn \
  --arg id "$WORKFLOW_ID" \
  --arg repository_path "$REPOSITORY" \
  --arg model_id "$MODEL_ID" \
  --arg prompt "$TASK_PROMPT" \
  --argjson test_command "$TEST_COMMAND" \
  --argjson timeout_seconds "$TIMEOUT_SECONDS" \
  '{id:$id,name:"P6 live Coding Workflow",version:1,entry:"start",
    nodes:[
      {id:"start",type:"start"},
      {id:"implement",type:"coding",coding:{
        repository_path:$repository_path,model_id:$model_id,prompt:$prompt,
        test_command:$test_command,max_repair_attempts:1,timeout_seconds:$timeout_seconds
      }},
      {id:"finish",type:"end"}
    ],
    edges:[{from:"start",to:"implement"},{from:"implement",to:"finish"}]}')"

api -X PUT "$BASE_URL/api/v1/workflows/$WORKFLOW_ID" -d "$WORKFLOW" >/dev/null
echo "PASS: durable Coding Workflow definition saved ($WORKFLOW_ID)"

api -X POST "$BASE_URL/api/v1/workflows/$WORKFLOW_ID/runs" \
  -d "$(jq -cn --arg run_id "$RUN_ID" '{run_id:$run_id,input:{source:"p6-live-acceptance"}}')" >/dev/null
echo "PASS: durable Workflow Run queued ($RUN_ID)"

deadline=$(( $(date +%s) + TIMEOUT_SECONDS + 240 ))
while true; do
  RUN="$(api "$BASE_URL/api/v1/workflows/runs/$RUN_ID")"
  RUN_STATUS="$(jq -r '.status' <<<"$RUN")"
  case "$RUN_STATUS" in
    succeeded) break ;;
    failed|rejected|canceled)
      fail "Workflow Run ended as $RUN_STATUS: $(jq -r '.error // ""' <<<"$RUN")" ;;
  esac
  (( $(date +%s) < deadline )) || fail "timed out waiting for Coding Workflow"
  sleep "$POLL_SECONDS"
done
TASK_ID="$(jq -r '.context.implement.coding.task_id // empty' <<<"$RUN")"
EXECUTION_ID="$(jq -r '.context.implement.coding.execution_id // empty' <<<"$RUN")"
[[ -n "$TASK_ID" && -n "$EXECUTION_ID" ]] || fail "successful Workflow did not return its Coding Task and Execution references"
echo "PASS: Workflow Coding node completed ($TASK_ID / $EXECUTION_ID)"

EXECUTION="$(api "$BASE_URL/api/v1/managed-executions/$EXECUTION_ID")"
[[ "$(jq -r '.status' <<<"$EXECUTION")" == "succeeded" ]] || fail "canonical Coding Execution did not succeed"
TASK="$(api "$BASE_URL/api/v1/coding/tasks/$TASK_ID")"
[[ "$(jq -r '.status' <<<"$TASK")" == "succeeded" ]] || fail "Coding Task is not succeeded"
[[ "$(jq -r '.execution_id' <<<"$TASK")" == "$EXECUTION_ID" ]] || fail "Coding Task is not bound to the canonical Execution"
[[ "$(jq -r '.attempt_id' <<<"$TASK")" == "$EXECUTION_ID-a-1" ]] || fail "Coding Task is not bound to the canonical Attempt"
[[ -n "$(jq -r '.workspace_path // empty' <<<"$TASK")" ]] || fail "Coding Worktree path was not persisted"
[[ -n "$(jq -r '.branch_name // empty' <<<"$TASK")" ]] || fail "Coding Worktree branch was not persisted"
echo "PASS: Coding Task, Worktree and canonical Attempt are linked"

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
jq -e --arg execution "$EXECUTION_ID" --arg attempt "$EXECUTION_ID-a-1" 'any(.artifact_versions[]; .source_execution_id==$execution and .source_attempt_id==$attempt and .media_type=="text/x-diff")' <<<"$DETAIL" >/dev/null || fail "final patch Artifact Version is not bound to the canonical Execution/Attempt"
jq -e --arg execution "$EXECUTION_ID" --arg attempt "$EXECUTION_ID-a-1" 'any(.evidence[]; .execution_id==$execution and .attempt_id==$attempt and .kind=="diff")' <<<"$DETAIL" >/dev/null || fail "diff Evidence provenance is missing"
jq -e --arg execution "$EXECUTION_ID" --arg attempt "$EXECUTION_ID-a-1" 'any(.evidence[]; .execution_id==$execution and .attempt_id==$attempt and .kind=="test")' <<<"$DETAIL" >/dev/null || fail "test Evidence provenance is missing"
jq -e --arg execution "$EXECUTION_ID" --arg attempt "$EXECUTION_ID-a-1" 'any(.evidence[]; .execution_id==$execution and .attempt_id==$attempt and .kind=="tool_log")' <<<"$DETAIL" >/dev/null || fail "review Evidence provenance is missing"
USAGE="$(api "$BASE_URL/api/v1/usage/events?execution_id=$EXECUTION_ID&attempt_id=$EXECUTION_ID-a-1&limit=200")"
jq -e --arg execution "$EXECUTION_ID" --arg attempt "$EXECUTION_ID-a-1" '(.items|length)>=2 and all(.items[]; .execution_id==$execution and .attempt_id==$attempt)' <<<"$USAGE" >/dev/null || fail "Coding model usage is missing canonical Execution/Attempt provenance"

echo "PASS: Workflow, Coding Execution, Attempt, final diff, Artifact/Evidence and Usage provenance verified"
echo "Workflow: $WORKFLOW_ID"
echo "Workflow Run: $RUN_ID"
echo "Coding Task: $TASK_ID"
echo "Execution: $EXECUTION_ID"
echo "Worktree retained for manual inspection; no commit or push was performed."
