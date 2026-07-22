#!/bin/sh
set -eu

BASE_URL="${TMA_BASE_URL:-http://localhost:18088}"
CLI="${TMA_CLI:-bin/tma}"
DOTENV_PATH="${TMA_DOTENV_PATH:-.env}"
WAIT_SECONDS="${TMA_AGENT_CORE_STAGING_WAIT_SECONDS:-180}"
POSTGRES_CONTAINER="${TMA_AGENT_CORE_STAGING_POSTGRES_CONTAINER:-tma-postgres}"
MODE="${TMA_AGENT_CORE_CRASH_MODE:-all}"

env_value() {
  key="$1"
  python3 - "$key" "$DOTENV_PATH" <<'PY'
import os
import sys

key = sys.argv[1]
path = sys.argv[2]
if os.environ.get(key):
    print(os.environ[key])
    raise SystemExit
for line in open(path, encoding="utf-8"):
    line = line.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    name, value = line.split("=", 1)
    if name.strip() == key:
        print(value.strip().strip("\"'"))
        break
PY
}

json_field() {
  python3 -c 'import json,sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"
}

create_session() {
  name="$1"
  system_prompt="$2"
  suffix="$(date +%Y%m%d%H%M%S)"
  agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
    --name "$name-agent-$suffix" \
    --llm-provider "$PROVIDER" \
    --model "$MODEL" \
    --system "$system_prompt")"
  agent_id="$(printf '%s' "$agent_json" | json_field id)"
  env_json="$("$CLI" --base-url "$BASE_URL" env create --name "$name-env-$suffix" --config "{\"type\":\"$name\"}")"
  env_id="$(printf '%s' "$env_json" | json_field id)"
  session_json="$("$CLI" --base-url "$BASE_URL" session create --agent "$agent_id" --env "$env_id" --title "$name $suffix")"
  printf '%s' "$session_json" | json_field id
}

database_state() {
  session_id="$1"
  docker exec "$POSTGRES_CONTAINER" psql -U tma -d tma -Atc "
    SELECT concat_ws('|',
      state_json->>'phase',
      revision::text,
      COALESCE(state_json->'pending_model'->>'number', ''),
      COALESCE(state_json->'tool_journal'->0->>'status', ''),
      COALESCE(state_json->'tool_journal'->0->>'attempt', ''),
      COALESCE(state_json->'tool_journal'->0->>'idempotency', ''),
      COALESCE(state_json->'tool_journal'->0->'reconciliation'->>'outcome', ''))
    FROM agent_loop_states
    WHERE session_id='$session_id' AND turn_id='turn_000001';
  "
}

wait_for_state() {
  session_id="$1"
  expected="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    state="$(database_state "$session_id")"
    case "$state" in
      "$expected"*)
        printf '%s\n' "$state"
        return 0
        ;;
    esac
    sleep 1
  done
  echo "timed out waiting for durable state prefix $expected, last=$state" >&2
  return 1
}

wait_for_recovery() {
  session_id="$1"
  recovery_type="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    events_json="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" 2>/dev/null || true)"
    if printf '%s' "$events_json" | python3 -c '
import json
import sys

kind = sys.argv[1]
events = json.load(sys.stdin).get("events", [])
types = [event.get("type") for event in events]
common = "runtime.completed" in types and "agent.message" in types and "session.status_idle" in types
if kind == "model":
    valid = common and types.count("model.abandoned") == 1 and types.count("model.requested") == 2 and types.count("model.responded") == 1
else:
    tool_events = [event for event in events if event.get("type") == "tool.call_result"]
    valid = common and types.count("tool.call_started") == 1 and len(tool_events) == 1 and tool_events[0].get("payload", {}).get("data", {}).get("status") == "indeterminate" and types.count("tool.call_reconciled") == 1
sys.exit(0 if valid else 1)
' "$recovery_type" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for $recovery_type crash recovery" >&2
  printf '%s\n' "$events_json" >&2
  return 1
}

wait_for_reconciliation() {
  session_id="$1"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    interventions_json="$("$CLI" --base-url "$BASE_URL" session intervention list --session "$session_id" --status pending 2>/dev/null || true)"
    call_id="$(printf '%s' "$interventions_json" | python3 -c '
import json
import sys

for item in json.load(sys.stdin).get("interventions", []):
    request = item.get("request") or {}
    if request.get("purpose") == "tool_reconciliation":
        print(item.get("call_id", ""))
        break
' 2>/dev/null || true)"
    if [ -n "$call_id" ]; then
      printf '%s\n' "$call_id"
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for tool reconciliation intervention" >&2
  return 1
}

crash_and_restart() {
  TMA_BASE_URL="$BASE_URL" \
  TMA_CLI="$CLI" \
  TMA_AGENT_CORE_STAGING_STOP_SIGNAL=KILL \
    scripts/restart_agent_core_staging.sh
}

PROVIDER="$(env_value TMA_LLM_PROVIDER)"
MODEL="$(env_value TMA_LLM_MODEL)"
if [ -z "$PROVIDER" ] || [ -z "$MODEL" ]; then
  echo "TMA_LLM_PROVIDER and TMA_LLM_MODEL are required in environment or $DOTENV_PATH" >&2
  exit 1
fi

verify_model_crash() {
  system_prompt='This is a crash-recovery test. Never call tools. Produce the requested long response without abbreviating it.'
  session_id="$(create_session agent-core-model-crash "$system_prompt")"
  "$CLI" --base-url "$BASE_URL" event send --session "$session_id" --text 'Write 120 numbered lines. Each line must contain a distinct short sentence.' >/dev/null
  state="$(wait_for_state "$session_id" 'awaiting_model|2|1|')"
  echo "Crashing during model request session=$session_id state=$state"
  crash_and_restart
  wait_for_recovery "$session_id" model
  state="$(database_state "$session_id")"
  echo "Model crash recovery passed session=$session_id state=$state"
}

verify_tool_crash() {
  system_prompt='When the user says CRASH_TOOL, call default.run_command exactly once with command "sh" and args ["-c","sleep 300; printf TOOL_CRASH_COMPLETED"]. Do not answer before calling the tool. After receiving the tool result, explain whether execution was confirmed and do not call more tools.'
  session_id="$(create_session agent-core-tool-crash "$system_prompt")"
  "$CLI" --base-url "$BASE_URL" session runtime update --session "$session_id" --intervention-mode full_access >/dev/null
  "$CLI" --base-url "$BASE_URL" event send --session "$session_id" --text CRASH_TOOL >/dev/null
  state="$(wait_for_state "$session_id" 'executing_tools|5||started|1|unknown')"
  echo "Crashing during tool execution session=$session_id state=$state"
  crash_and_restart
  reconciliation_call_id="$(wait_for_reconciliation "$session_id")"
  "$CLI" --base-url "$BASE_URL" session intervention reconcile \
    --session "$session_id" \
    --turn turn_000001 \
    --call "$reconciliation_call_id" \
    --outcome compensated \
    --summary "The staging crash drill terminated the test process; no persistent external side effect remains." \
    --evidence "agent-core-crash-drill" >/dev/null
  wait_for_recovery "$session_id" tool
  state="$(database_state "$session_id")"
  case "$state" in
    completed*'|failed|1|unknown|compensated') ;;
    *)
      echo "unexpected recovered tool state: $state" >&2
      exit 1
      ;;
  esac
  echo "Tool crash recovery passed session=$session_id state=$state"
}

case "$MODE" in
  model) verify_model_crash ;;
  tool) verify_tool_crash ;;
  all)
    verify_model_crash
    verify_tool_crash
    ;;
  *)
    echo "unsupported TMA_AGENT_CORE_CRASH_MODE=$MODE; use model, tool, or all" >&2
    exit 1
    ;;
esac

echo "Agent Core in-flight crash recovery verification passed"
