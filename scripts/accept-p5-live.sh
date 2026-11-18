#!/usr/bin/env bash
# Live P5 acceptance: real OpenAI-compatible provider -> Gateway -> Agent -> Workflow.
# Creates uniquely named acceptance resources and leaves them in place for inspection.
# No API key or API token is written to the repository or acceptance report.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASE_URL="${AGENTMESH_BASE_URL:-http://127.0.0.1:8080}"
BASE_URL="${BASE_URL%/}"
if [[ "$BASE_URL" != https://* && "$BASE_URL" != http://127.0.0.1:* && "$BASE_URL" != http://localhost:* ]]; then
  echo "Refusing to send acceptance credentials to non-HTTPS or non-loopback URL: $BASE_URL" >&2
  exit 1
fi
for tool in curl jq openssl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "Missing required command: $tool" >&2; exit 1; }
done

echo "== AgentMesh P5 Live Acceptance =="
echo "Target: $BASE_URL"
read -r -s -p "AgentMesh API token: " API_TOKEN
echo
if [[ ! "$API_TOKEN" =~ ^[A-Za-z0-9._~-]{32,}$ ]]; then
  echo "API token must contain at least 32 characters and no line breaks." >&2
  exit 1
fi
read -r -s -p "OpenAI-compatible provider API key: " PROVIDER_KEY
echo
if [[ -z "$PROVIDER_KEY" ]]; then
  echo "Provider API key must not be empty." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
CURL_CONFIG="$TMP_DIR/curl.conf"
REPORT="$TMP_DIR/report.json"
umask 077
printf 'header = "Authorization: Bearer %s"\n' "$API_TOKEN" > "$CURL_CONFIG"
unset API_TOKEN
cleanup() { rm -rf "$TMP_DIR"; unset PROVIDER_KEY; }
trap cleanup EXIT

request() {
  local method="$1" path="$2" data="${3:-}" output="$TMP_DIR/response.json" status
  if [[ -n "$data" ]]; then
    status="$(printf '%s' "$data" | curl --silent --show-error --max-time 180 --config "$CURL_CONFIG" \
      -H 'Content-Type: application/json' -X "$method" --data-binary @- \
      -o "$output" -w '%{http_code}' "$BASE_URL$path")"
  else
    status="$(curl --silent --show-error --max-time 180 --config "$CURL_CONFIG" \
      -H 'Content-Type: application/json' -X "$method" \
      -o "$output" -w '%{http_code}' "$BASE_URL$path")"
  fi
  if [[ ! "$status" =~ ^2 ]]; then
    echo "FAIL: $method $path returned HTTP $status" >&2
    jq -c 'del(.message,.error,.details)' "$output" 2>/dev/null >&2 || true
    exit 1
  fi
  cat "$output"
}

assert_eq() {
  local actual="$1" expected="$2" label="$3"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $label (got '$actual', expected '$expected')" >&2
    exit 1
  fi
}

echo "== Runtime readiness =="
request GET /healthz >/dev/null
ready="$(request GET /readyz | jq -r '.status')"
assert_eq "$ready" "ready" "runtime readiness"
echo "PASS: service and database ready"

STARTED_AT="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
STAMP="$(date +%s)-$(openssl rand -hex 3)"
PROVIDER_ID="p5-live-$STAMP"
MODEL_ID="p5-live-model-$STAMP"
WORKSPACE_ID="p5-live-workspace-$STAMP"
AGENT_ID="p5-live-agent-$STAMP"
WORKFLOW_ID="p5-live-workflow-$STAMP"
RUN_ID="p5-live-run-$STAMP"
UPSTREAM_MODEL="${AGENTMESH_LIVE_MODEL:-}"

if [[ -z "$UPSTREAM_MODEL" ]]; then
  read -r -p "Upstream model identifier (for example gpt-4.1-mini): " UPSTREAM_MODEL
fi
if [[ -z "$UPSTREAM_MODEL" ]]; then
  echo "An upstream model identifier is required." >&2
  exit 1
fi
read -r -p "Provider base URL [https://api.openai.com/v1]: " BASE_PROVIDER_URL
BASE_PROVIDER_URL="${BASE_PROVIDER_URL:-https://api.openai.com/v1}"
if [[ "$BASE_PROVIDER_URL" != https://* && "$BASE_PROVIDER_URL" != http://127.0.0.1:* && "$BASE_PROVIDER_URL" != http://localhost:* ]]; then
  echo "Provider base URL must use HTTPS unless it is loopback." >&2
  exit 1
fi

echo "== Configure isolated live provider/model =="
provider_json="$(jq -n --arg id "$PROVIDER_ID" --arg url "$BASE_PROVIDER_URL" \
  '{id:$id,name:"P5 Live Acceptance",type:"openai",kind:"cloud",base_url:$url,api_key_env:"p5-live-acceptance",enabled:false}')"
request POST /api/v1/providers "$provider_json" >/dev/null
printf '%s' "$PROVIDER_KEY" > "$TMP_DIR/provider-key"
credential_json="$(jq -n --rawfile key "$TMP_DIR/provider-key" '{api_key:$key}')"
request PUT "/api/v1/providers/$PROVIDER_ID/credential" "$credential_json" >/dev/null
rm -f "$TMP_DIR/provider-key"
unset PROVIDER_KEY credential_json
enable_json='{"enabled":true}'
request PATCH "/api/v1/providers/$PROVIDER_ID/enabled" "$enable_json" >/dev/null
model_json="$(jq -n --arg id "$MODEL_ID" --arg pid "$PROVIDER_ID" --arg name "$UPSTREAM_MODEL" \
  '{id:$id,provider_id:$pid,name:$name,display_name:"P5 Live Acceptance Model",context_window:32768,max_output_tokens:512,supports_streaming:false,enabled:true}')"
request POST /api/v1/models "$model_json" >/dev/null
echo "PASS: provider, encrypted credential and model registered"

echo "== Real upstream provider connectivity =="
connectivity="$(request POST "/api/v1/providers/$PROVIDER_ID/test" '{}')"
assert_eq "$(jq -r '.success' <<<"$connectivity")" "true" "provider connectivity"
echo "PASS: actual upstream request completed ($(jq -r '.latency_ms' <<<"$connectivity") ms)"

echo "== Real Model Gateway generation =="
generate_json="$(jq -n --arg model "$MODEL_ID" \
  '{model_id:$model,messages:[{role:"user",content:"Reply with exactly: AGENTMESH_LIVE_MODEL_OK"}],max_output_tokens:48}')"
generation="$(request POST /api/v1/generate "$generate_json")"
if [[ "$(jq -r '.content // empty' <<<"$generation")" != *"AGENTMESH_LIVE_MODEL_OK"* ]]; then
  echo "FAIL: model response did not contain the requested acceptance marker." >&2
  exit 1
fi
echo "PASS: Model Gateway returned a real upstream completion"

echo "== Real Agent execution =="
workspace_json="$(jq -n --arg id "$WORKSPACE_ID" '{id:$id}')"
request POST /api/v1/workspaces "$workspace_json" >/dev/null
agent_json="$(jq -n --arg id "$AGENT_ID" --arg model "$MODEL_ID" \
  '{id:$id,name:"P5 Live Acceptance Agent",description:"Temporary live acceptance agent",system_prompt:"You are a concise test agent. Follow the user instruction exactly.",model_id:$model,temperature:0,max_steps:2,max_tool_calls:0,max_output_tokens:96,timeout_seconds:120,approved_tools:[],enabled:true}')"
request POST /api/v1/agent-definitions "$agent_json" >/dev/null
agent_run_json="$(jq -n --arg workspace "$WORKSPACE_ID" \
  '{workspace_id:$workspace,prompt:"Reply with exactly: AGENTMESH_LIVE_AGENT_OK"}')"
agent_result="$(request POST "/api/v1/agent-definitions/$AGENT_ID/run" "$agent_run_json")"
assert_eq "$(jq -r '.status' <<<"$agent_result")" "succeeded" "Agent terminal status"
if [[ "$(jq -r '.content // empty' <<<"$agent_result")" != *"AGENTMESH_LIVE_AGENT_OK"* ]]; then
  echo "FAIL: Agent result did not contain the requested acceptance marker." >&2
  exit 1
fi
AGENT_RUN_ID="$(jq -r '.task_id // empty' <<<"$agent_result")"
echo "PASS: real Agent completed (task $AGENT_RUN_ID)"

echo "== Real two-Agent-node Workflow execution =="
workflow_json="$(jq -n --arg id "$WORKFLOW_ID" --arg model "$MODEL_ID" --arg workspace "$WORKSPACE_ID" '
  {id:$id,name:"P5 Live Acceptance Workflow",version:1,entry:"draft",
   nodes:[
     {id:"draft",type:"agent",name:"Draft",agent:{model_id:$model,workspace_id:$workspace,prompt:"Create a concise two-sentence explanation of why durable workflow checkpoints matter."}},
     {id:"review",type:"agent",name:"Review",agent:{model_id:$model,workspace_id:$workspace,prompt:"Review and improve the draft in the workflow context. Return the improved explanation and explicitly mention one reliability benefit."}},
     {id:"finish",type:"end",name:"Finish"}
   ],
   edges:[{from:"draft",to:"review"},{from:"review",to:"finish"}] }')"
request PUT "/api/v1/workflows/$WORKFLOW_ID" "$workflow_json" >/dev/null
workflow_readback="$(request GET "/api/v1/workflows/$WORKFLOW_ID")"
assert_eq "$(jq -r '.id' <<<"$workflow_readback")" "$WORKFLOW_ID" "workflow persistence"
request_body="$(jq -n --arg id "$RUN_ID" '{run_id:$id,input:{acceptance:"p5-live",topic:"durable workflow checkpoints"}}')"
request POST "/api/v1/workflows/$WORKFLOW_ID/runs" "$request_body" >/dev/null

deadline=$((SECONDS + 360))
run_json=""
while (( SECONDS < deadline )); do
  run_json="$(request GET "/api/v1/workflows/runs/$RUN_ID")"
  status="$(jq -r '.status' <<<"$run_json")"
  case "$status" in
    succeeded) break ;;
    failed|rejected|canceled)
      echo "FAIL: Workflow ended with status $status" >&2
      jq '{status,error,nodes}' <<<"$run_json" >&2
      exit 1
      ;;
  esac
  sleep 2
done
assert_eq "$(jq -r '.status' <<<"$run_json")" "succeeded" "Workflow terminal status"
assert_eq "$(jq -r '.nodes.draft.status' <<<"$run_json")" "succeeded" "draft node status"
assert_eq "$(jq -r '.nodes.review.status' <<<"$run_json")" "succeeded" "review node status"
history="$(request GET "/api/v1/workflows/runs/$RUN_ID/history")"
event_count="$(jq '.events | length' <<<"$history")"
node_count="$(jq '.nodes | length' <<<"$history")"
if (( event_count < 6 || node_count < 2 )); then
  echo "FAIL: Workflow history/evidence incomplete (events=$event_count nodes=$node_count)" >&2
  exit 1
fi
echo "PASS: two real Agent nodes completed in one durable Workflow run ($RUN_ID)"
echo "PASS: workflow history contains $event_count events and $node_count node observations"

echo "== Usage ledger and provenance =="
usage_events="$(request GET "/api/v1/usage/events?since=$STARTED_AT&provider_id=$PROVIDER_ID&model_id=$MODEL_ID&limit=100")"
usage_count="$(jq '.items | length' <<<"$usage_events")"
if (( usage_count < 4 )); then
  echo "FAIL: expected at least 4 real model usage events for this provider/model; found $usage_count" >&2
  jq '{items:(.items|map({provider_id,model_id,usage_source,input_tokens,output_tokens,total_tokens,request_id}))}' <<<"$usage_events" >&2
  exit 1
fi
if ! jq -e --arg p "$PROVIDER_ID" --arg m "$MODEL_ID" '.items | all(.[]; .provider_id == $p and .model_id == $m)' <<<"$usage_events" >/dev/null; then
  echo "FAIL: Usage Ledger provider/model provenance mismatch." >&2
  exit 1
fi
echo "PASS: $usage_count real usage events persisted with provider/model provenance"

jq -n --arg created_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
  --arg base_url "$BASE_URL" --arg provider "$PROVIDER_ID" --arg model "$MODEL_ID" \
  --arg upstream "$UPSTREAM_MODEL" --arg agent "$AGENT_ID" --arg task "$AGENT_RUN_ID" \
  --arg workflow "$WORKFLOW_ID" --arg run "$RUN_ID" \
  --argjson events "$event_count" --argjson nodes "$node_count" --argjson usage "$usage_count" \
  '{acceptance:"P5 Live Acceptance",created_at:$created_at,base_url:$base_url,provider_id:$provider,model_id:$model,upstream_model:$upstream,agent_definition_id:$agent,agent_task_id:$task,workflow_id:$workflow,workflow_run_id:$run,workflow_status:"succeeded",workflow_event_count:$events,workflow_node_observation_count:$nodes,usage_event_count:$usage,secrets_recorded:false}' > "$REPORT"
mkdir -p data
REPORT_PATH="data/p5-live-acceptance-$STAMP.json"
cp "$REPORT" "$REPORT_PATH"
chmod 600 "$REPORT_PATH"

echo
echo "P5 Live Acceptance passed."
echo "Evidence report: $REPORT_PATH"
echo "Keep the acceptance resources until you have inspected the Dashboard and report."
echo "After sign-off, remove the temporary provider/model/agent/workspace/workflow and stored credential manually."
