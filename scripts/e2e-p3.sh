#!/usr/bin/env bash
set -Eeuo pipefail

# Mutating P3 end-to-end acceptance. Creates uniquely named test records and
# intentionally keeps them for inspection; it never deletes user data.
#
# Usage:
#   bash scripts/e2e-p3.sh
#   API_BASE_URL=http://127.0.0.1:8080 API_TOKEN=... bash scripts/e2e-p3.sh

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8080}"
API_TOKEN="${API_TOKEN:-}"
RUN_ID="$(date +%s)-$$"
PREFIX="p3-e2e-$RUN_ID"
TMP_DIR="$(mktemp -d)"
RESP="$TMP_DIR/response.json"
PASS_COUNT=0
FAIL_COUNT=0

cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

require_command() {
  command -v "$1" >/dev/null 2>&1 || { echo "Required command not found: $1" >&2; exit 2; }
}
require_command curl
require_command jq
require_command base64

pass() { PASS_COUNT=$((PASS_COUNT + 1)); printf '[PASS] %s\n' "$1"; }
fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); printf '[FAIL] %s\n' "$1"; }

request() {
  method="$1"
  path="$2"
  body="${3:-}"
  expected="$4"
  if [ -n "$API_TOKEN" ]; then
    status="$(curl --silent --show-error --connect-timeout 3 --max-time 30 \
      -X "$method" -H "Authorization: Bearer $API_TOKEN" -H 'Content-Type: application/json' \
      --data-binary "$body" -o "$RESP" -w '%{http_code}' "$API_BASE_URL$path")"
  elif [ -n "$body" ]; then
    status="$(curl --silent --show-error --connect-timeout 3 --max-time 30 \
      -X "$method" -H 'Content-Type: application/json' \
      --data-binary "$body" -o "$RESP" -w '%{http_code}' "$API_BASE_URL$path")"
  else
    status="$(curl --silent --show-error --connect-timeout 3 --max-time 30 \
      -X "$method" -o "$RESP" -w '%{http_code}' "$API_BASE_URL$path")"
  fi
  if [ "$status" != "$expected" ]; then
    echo "[FAIL] $method $path: expected HTTP $expected, got $status" >&2
    cat "$RESP" >&2 || true
    exit 1
  fi
  if ! jq empty "$RESP" >/dev/null 2>&1; then
    echo "[FAIL] $method $path returned invalid JSON" >&2
    cat "$RESP" >&2 || true
    exit 1
  fi
}

assert_jq() {
  name="$1"
  expression="$2"
  if jq -e "$expression" "$RESP" >/dev/null; then
    pass "$name"
  else
    fail "$name"
    cat "$RESP" >&2
    exit 1
  fi
}

echo "== AgentMesh P3 end-to-end acceptance =="
echo "API: $API_BASE_URL"
echo "Run: $RUN_ID"
echo "Test records are retained in the local database."
echo

# 1. Workspace and Agent Identity lifecycle.
workspace_body="$(jq -n --arg name "$PREFIX workspace" --arg slug "$PREFIX" \
  '{name:$name,slug:$slug,description:"Automated P3 acceptance workspace",autonomy_mode:"directed",max_agent_concurrency:4,max_delegation_depth:3}')"
request POST /api/v1/collaboration/workspaces "$workspace_body" 201
WORKSPACE_ID="$(jq -r '.id' "$RESP")"
[ -n "$WORKSPACE_ID" ] && [ "$WORKSPACE_ID" != null ] || { echo "Workspace ID missing"; exit 1; }
pass "Create Workspace"

create_agent() {
  name="$1"
  body="$(jq -n --arg workspace "$WORKSPACE_ID" --arg name "$name" \
    '{workspace_id:$workspace,name:$name,role:"worker",capabilities:["test.execute","artifact.write"]}')"
  request POST /api/v1/collaboration/agents "$body" 201
  jq -r '.id' "$RESP"
}
AGENT_A="$(create_agent "$PREFIX agent-a")"
AGENT_B="$(create_agent "$PREFIX agent-b")"
AGENT_C="$(create_agent "$PREFIX lifecycle-agent")"
[ -n "$AGENT_A" ] && [ -n "$AGENT_B" ] && [ -n "$AGENT_C" ] || { echo "Agent ID missing"; exit 1; }
pass "Create three Agent Identities"

request POST "/api/v1/collaboration/agents/$AGENT_A/enable" '{}' 200
assert_jq "Enable Agent A" '.status == "idle"'
request POST "/api/v1/collaboration/agents/$AGENT_A/disable" '{}' 200
assert_jq "Disable Agent A" '.status == "disabled"'
request POST "/api/v1/collaboration/agents/$AGENT_A/enable" '{}' 200
assert_jq "Re-enable Agent A" '.status == "idle"'

request DELETE "/api/v1/collaboration/agents/$AGENT_C" '' 200
assert_jq "Soft-delete lifecycle Agent" '.deleted_at != null and .status == "disabled"'
request GET "/api/v1/collaboration/workspaces/$WORKSPACE_ID/agents" '' 200
assert_jq "Deleted Agent hidden from active list" --arg id "$AGENT_C" '[.items[].id] | index($id) == null'
request POST "/api/v1/collaboration/agents/$AGENT_C/recover" '{}' 200
assert_jq "Recover deleted Agent" '.deleted_at == null and .status == "idle"'

# 2. Conversation and channel membership.
channel_body="$(jq -n --arg workspace "$WORKSPACE_ID" --arg name "$PREFIX channel" \
  --arg a "$AGENT_A" --arg b "$AGENT_B" \
  '{workspace_id:$workspace,name:$name,kind:"team",member_ids:[$a,$b]}')"
request POST /api/v1/collaboration/channels "$channel_body" 201
CHANNEL_ID="$(jq -r '.id' "$RESP")"
[ -n "$CHANNEL_ID" ] && [ "$CHANNEL_ID" != null ] || { echo "Channel ID missing"; exit 1; }
pass "Create member-scoped Channel"

message_body='{"role":"user","name":"P3 acceptance","content":"Verify channel message persistence and member routing."}'
request POST "/api/v1/collaboration/channels/$CHANNEL_ID/messages" "$message_body" 201
MESSAGE_ID="$(jq -r '.id' "$RESP")"
request GET "/api/v1/collaboration/channels/$CHANNEL_ID/messages?limit=20" '' 200
assert_jq "Persist Channel message" --arg id "$MESSAGE_ID" '[.items[].id] | index($id) != null'

# 3. Task, assignment, approval gate, rejection and reassignment.
task_body="$(jq -n --arg workspace "$WORKSPACE_ID" --arg title "$PREFIX governed task" \
  --arg creator "$AGENT_B" --arg key "$PREFIX-task" \
  '{workspace_id:$workspace,title:$title,description:"Exercise approval, assignment and recovery.",type:"custom",priority:3,created_by:$creator,idempotency_key:$key,required_capabilities:["test.execute"]}')"
request POST /api/v1/collaboration/tasks "$task_body" 201
TASK_ID="$(jq -r '.id' "$RESP")"
request POST /api/v1/collaboration/tasks "$task_body" 201
assert_jq "Task creation idempotency" --arg id "$TASK_ID" '.id == $id'

assign_body="$(jq -n --arg agent "$AGENT_A" --arg by "$AGENT_B" '{agent_id:$agent,assigned_by:$by}')"
request POST "/api/v1/collaboration/tasks/$TASK_ID/assignments" "$assign_body" 201
ASSIGNMENT_ID="$(jq -r '.id' "$RESP")"
[ -n "$ASSIGNMENT_ID" ] && [ "$ASSIGNMENT_ID" != null ] || { echo "Assignment ID missing"; exit 1; }
pass "Assign capability-compatible Agent"

decision_body="$(jq -n --arg agent "$AGENT_A" --arg assignment "$ASSIGNMENT_ID" --arg event "$PREFIX-claim-decision" \
  '{agent_id:$agent,event_id:$event,action:"claim",resource_type:"assignment",resource_id:$assignment,reason:"P3 E2E approval gate"}')"
request POST /api/v1/collaboration/decisions/evaluate "$decision_body" 200
assert_jq "Directed autonomy requires approval" '.outcome == "approval_required" and (.approval_id | length) > 0'
APPROVAL_ID="$(jq -r '.approval_id' "$RESP")"

request POST /api/v1/collaboration/decisions/evaluate "$decision_body" 200
assert_jq "Pending approval request is deduplicated" --arg id "$APPROVAL_ID" '.approval_id == $id'

resolve_body="$(jq -n --arg reviewer "$AGENT_B" '{reviewer_id:$reviewer,status:"rejected",note:"Expected rejection in automated lifecycle test."}')"
request POST "/api/v1/collaboration/approvals/$APPROVAL_ID/resolve" "$resolve_body" 200
assert_jq "Resolve approval as rejected" '.status == "rejected"'

# Dispatcher + Wakeup are asynchronous; wait for the rejected approval to fence
# and block the first assignment before assigning the replacement Agent.
blocked=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  request GET "/api/v1/collaboration/tasks/$TASK_ID" '' 200
  if [ "$(jq -r '.status' "$RESP")" = "blocked" ]; then blocked=1; break; fi
  sleep 1
done
if [ "$blocked" -ne 1 ]; then
  echo "[FAIL] Rejected approval did not block the Assignment/Task within 15 seconds" >&2
  cat "$RESP" >&2
  exit 1
fi
pass "Approval rejection reaches Wakeup and blocks Task"

assign_body="$(jq -n --arg agent "$AGENT_B" --arg by "$AGENT_A" '{agent_id:$agent,assigned_by:$by}')"
request POST "/api/v1/collaboration/tasks/$TASK_ID/assignments" "$assign_body" 201
REASSIGNMENT_ID="$(jq -r '.id' "$RESP")"
assert_jq "Reassign blocked Task to another Agent" --arg agent "$AGENT_B" '.agent_id == $agent and .status == "pending"'
request GET "/api/v1/collaboration/tasks/$TASK_ID/progress" '' 200
pass "Read parent/child Task progress"

request GET "/api/v1/collaboration/agents/$AGENT_A/decision-audit" '' 200
assert_jq "Decision audit is queryable" '(.items | length) > 0'
request GET "/api/v1/collaboration/workspaces/$WORKSPACE_ID/approvals?status=rejected" '' 200
assert_jq "Approval history is queryable" --arg id "$APPROVAL_ID" '[.items[].id] | index($id) != null'

# 4. Artifact -> Evidence -> deterministic Verification -> Submission -> Review.
artifact_content='AgentMesh P3 E2E artifact content.'
content_b64="$(printf '%s' "$artifact_content" | base64 | tr -d '\n')"
artifact_body="$(jq -n --arg workspace "$WORKSPACE_ID" --arg task "$TASK_ID" --arg creator "$AGENT_B" \
  --arg title "$PREFIX artifact" --arg content "$content_b64" \
  '{workspace_id:$workspace,source_task_id:$task,creator_agent_id:$creator,kind:"document",title:$title,content:$content,media_type:"text/plain",metadata:{test:"p3-e2e"}}')"
request POST /api/v1/collaboration/artifacts "$artifact_body" 201
ARTIFACT_ID="$(jq -r '.artifact.id' "$RESP")"
VERSION_ID="$(jq -r '.version.id' "$RESP")"
VERSION_SHA="$(jq -r '.version.content_sha256' "$RESP")"
[ -n "$ARTIFACT_ID" ] && [ "$ARTIFACT_ID" != null ] || { echo "Artifact ID missing"; exit 1; }
pass "Create immutable Artifact version"

evidence_body="$(jq -n --arg version "$VERSION_ID" --arg digest "$VERSION_SHA" --arg creator "$AGENT_B" \
  '{version_id:$version,kind:"test",content_sha256:$digest,statement:"P3 E2E artifact content created and digest recorded.",created_by:$creator}')"
request POST "/api/v1/collaboration/artifacts/$ARTIFACT_ID/evidence" "$evidence_body" 201
pass "Attach version-scoped Evidence"

verify_body='{"version":1,"policy":{"required_evidence_kinds":["test"],"require_content_digest":true}}'
request POST "/api/v1/collaboration/artifacts/$ARTIFACT_ID/verify" "$verify_body" 200
assert_jq "Deterministic Artifact verification passes" '.outcome == "passed"'

request POST "/api/v1/collaboration/artifacts/$ARTIFACT_ID/submit" '{}' 200
assert_jq "Submit verified Artifact version" '.status == "submitted"'

review_body="$(jq -n --arg reviewer "$AGENT_A" '{reviewer_id:$reviewer,outcome:"accepted",note:"P3 E2E acceptance review."}')"
request POST "/api/v1/collaboration/artifacts/$ARTIFACT_ID/review" "$review_body" 200
pass "Human review accepts submitted Artifact"

request GET "/api/v1/collaboration/artifacts/$ARTIFACT_ID" '' 200
assert_jq "Artifact lifecycle reaches accepted" '.status == "accepted"'
request GET "/api/v1/collaboration/artifacts/$ARTIFACT_ID/versions/1" '' 200
assert_jq "Artifact version is retrievable by exact version" '.version == 1'
request GET "/api/v1/collaboration/artifacts/$ARTIFACT_ID/evidence" '' 200
assert_jq "Artifact Evidence history is queryable" '(.items | length) == 1'
request GET "/api/v1/collaboration/artifacts/$ARTIFACT_ID/verifications" '' 200
assert_jq "Artifact Verification history is queryable" '(.items | length) == 1'
request GET "/api/v1/collaboration/artifacts/$ARTIFACT_ID/reviews" '' 200
assert_jq "Artifact Review history is queryable" '(.items | length) == 1'

# 5. Operational queues and final records.
request GET /api/v1/collaboration/outbox '' 200
pass "Outbox inspection API"
request GET /api/v1/collaboration/outbox/dead-letters '' 200
pass "Outbox dead-letter inspection API"
request GET "/api/v1/collaboration/workspaces/$WORKSPACE_ID" '' 200
assert_jq "Workspace record remains readable" --arg id "$WORKSPACE_ID" '.id == $id'

echo
echo "== P3 E2E summary =="
echo "Passed checks: $PASS_COUNT"
echo "Failed checks: $FAIL_COUNT"
echo "Workspace: $WORKSPACE_ID"
echo "Agent A: $AGENT_A"
echo "Agent B: $AGENT_B"
echo "Task: $TASK_ID"
echo "Artifact: $ARTIFACT_ID"
echo "Run prefix: $PREFIX"
echo "P3 E2E acceptance passed."
