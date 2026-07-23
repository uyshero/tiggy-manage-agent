#!/bin/sh
set -eu

CLI="${TMA_CLI:-bin/tma}"
DOTENV_PATH="${TMA_DOTENV_PATH:-.env}"
WAIT_SECONDS="${TMA_APPROVAL_TEST_WAIT_SECONDS:-90}"
MESSAGE="${TMA_APPROVAL_TEST_MESSAGE:-APPROVAL_TEST_RUN_COMMAND}"
DECISION="${TMA_APPROVAL_TEST_DECISION:-manual}"
PRINT_EVENTS="${TMA_APPROVAL_TEST_PRINT_EVENTS:-true}"
BEFORE_DECISION_HOOK="${TMA_APPROVAL_TEST_BEFORE_DECISION_HOOK:-}"

if [ ! -x "$CLI" ]; then
  echo "missing CLI: $CLI" >&2
  echo "run: make build-cli" >&2
  exit 1
fi

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

try:
    lines = open(path, encoding="utf-8").read().splitlines()
except FileNotFoundError:
    raise SystemExit

for line in lines:
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    if line.startswith("export "):
        line = line[len("export "):].strip()
    if "=" not in line:
        continue
    name, value = line.split("=", 1)
    if name.strip() != key:
        continue
    print(value.strip().strip("\"'"))
    break
PY
}

json_field() {
  python3 -c '
import json
import sys

value = json.load(sys.stdin)
for part in sys.argv[1].split("."):
    value = value[part]
print(value)
' "$1"
}

pending_field() {
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
items = data.get("interventions", [])
if not items:
    raise SystemExit(2)
print(items[0].get(sys.argv[1], ""))
' "$1"
}

event_summary() {
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]
agent_events = [event for event in events if event.get("type") == "agent.message"]
idle_events = [event for event in events if event.get("type") == "session.status_idle"]
tool_results = [event for event in events if event.get("type") == "tool.call_result"]
print(json.dumps({
    "event_count": len(events),
    "has_agent_message": bool(agent_events),
    "has_idle": bool(idle_events),
    "has_intervention_resolved": "intervention.resolved" in types,
    "tool_result_count": len(tool_results),
    "last_types": types[-8:],
}, ensure_ascii=False))
'
}

provider_exists() {
  provider_id="$1"
  "$CLI" --base-url "$BASE_URL" provider list | python3 -c '
import json
import sys

provider_id = sys.argv[1]
providers = json.load(sys.stdin).get("providers", [])
sys.exit(0 if any(item.get("id") == provider_id for item in providers) else 1)
' "$provider_id"
}

base_url_from_http_addr() {
  value="$1"
  case "$value" in
    http://*|https://*) printf '%s\n' "$value" ;;
    :*) printf 'http://localhost%s\n' "$value" ;;
    "") printf 'http://localhost:8080\n' ;;
    *) printf 'http://%s\n' "$value" ;;
  esac
}

wait_for_pending() {
  session_id="$1"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    pending_json="$("$CLI" --base-url "$BASE_URL" session intervention list --session "$session_id" --status pending)"
    if printf '%s' "$pending_json" | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("interventions") else 1)'; then
      printf '%s\n' "$pending_json"
      return 0
    fi
    sleep 2
  done
  return 1
}

wait_for_idle_agent() {
  session_id="$1"
  turn_id="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    events_json="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id")"
    if printf '%s' "$events_json" | python3 -c '
import json
import sys

turn_id = sys.argv[1]
events = json.load(sys.stdin).get("events", [])
turn_events = [
    event for event in events
    if event.get("turn_id") == turn_id or event.get("payload", {}).get("turn_id") == turn_id
]
types = [event.get("type") for event in turn_events]
required = [
    "intervention.resolved",
    "tool.call_result",
    "runtime.completed",
    "agent.message",
    "session.status_idle",
]
sys.exit(0 if all(event_type in types for event_type in required) else 1)
' "$turn_id"; then
      printf '%s\n' "$events_json"
      return 0
    fi
    sleep 2
  done
  return 1
}

BASE_URL="${TMA_BASE_URL:-}"
if [ -z "$BASE_URL" ]; then
  BASE_URL="$(base_url_from_http_addr "$(env_value TMA_HTTP_ADDR)")"
fi

provider="$(env_value TMA_LLM_PROVIDER)"
provider_type="$(env_value TMA_LLM_PROVIDER_TYPE)"
model="$(env_value TMA_LLM_MODEL)"
base_url="$(env_value TMA_LLM_BASE_URL)"
api_key_env="$(env_value TMA_LLM_API_KEY_ENV)"

if [ -z "$provider" ]; then
  echo "TMA_LLM_PROVIDER is required in environment or $DOTENV_PATH" >&2
  exit 1
fi
if [ -z "$model" ]; then
  echo "TMA_LLM_MODEL is required in environment or $DOTENV_PATH" >&2
  exit 1
fi
if [ "$provider" = "fake" ]; then
  echo "fake provider cannot trigger real tool approval; configure an openai-compatible provider in $DOTENV_PATH" >&2
  exit 1
fi
if [ -z "$provider_type" ]; then
  provider_type="openai"
fi
if [ -z "$api_key_env" ]; then
  api_key_env="TMA_LLM_API_KEY"
fi

echo "Checking health: $BASE_URL"
"$CLI" --base-url "$BASE_URL" health >/dev/null

echo "Using provider=$provider type=$provider_type model=$model"

echo "Ensuring provider/model from $DOTENV_PATH"
if provider_exists "$provider"; then
  echo "Reusing existing provider=$provider"
else
  "$CLI" --base-url "$BASE_URL" provider create \
    --id "$provider" \
    --type "$provider_type" \
    --base-url "$base_url" \
    --api-key-env "$api_key_env" >/dev/null
fi

"$CLI" --base-url "$BASE_URL" model upsert \
  --provider "$provider" \
  --model "$model" \
  --context-window "${TMA_APPROVAL_TEST_CONTEXT_WINDOW:-128000}" >/dev/null

suffix="$(date +%Y%m%d%H%M%S)"

system_prompt='You are testing TMA tool approval. When the user says exactly APPROVAL_TEST_RUN_COMMAND, you must call the tool default_run_command with command "sh" and args ["-c","pwd"]. Do not answer in text before calling the tool.'

echo "Creating agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "approval-test-agent-$suffix" \
  --llm-provider "$provider" \
  --llm-model "$model" \
  --system "$system_prompt")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "approval-test-env-$suffix" \
  --config '{"type":"approval-test"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Approval flow verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Session: $session_id"

echo "Setting intervention_mode=request_approval"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode request_approval >/dev/null

echo "Sending trigger message: $MESSAGE"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "$MESSAGE" >/dev/null

echo "Waiting for pending approval..."
if ! pending_json="$(wait_for_pending "$session_id")"; then
  echo "Timed out waiting for pending approval." >&2
  echo "Recent events:" >&2
  "$CLI" --base-url "$BASE_URL" event list --session "$session_id" >&2 || true
  exit 2
fi

turn_id="$(printf '%s' "$pending_json" | pending_field turn_id)"
call_id="$(printf '%s' "$pending_json" | pending_field call_id)"
tool_identifier="$(printf '%s' "$pending_json" | pending_field tool_identifier)"
api_name="$(printf '%s' "$pending_json" | pending_field api_name)"

echo "Pending approval:"
echo "  turn=$turn_id"
echo "  call=$call_id"
echo "  tool=$tool_identifier.$api_name"

if [ -n "$BEFORE_DECISION_HOOK" ]; then
  if [ ! -x "$BEFORE_DECISION_HOOK" ]; then
    echo "before-decision hook is not executable: $BEFORE_DECISION_HOOK" >&2
    exit 4
  fi
  echo "Running before-decision hook: $BEFORE_DECISION_HOOK"
  TMA_APPROVAL_TEST_SESSION_ID="$session_id" \
  TMA_APPROVAL_TEST_TURN_ID="$turn_id" \
  TMA_APPROVAL_TEST_CALL_ID="$call_id" \
    "$BEFORE_DECISION_HOOK"
fi

case "$DECISION" in
  manual)
    echo
    echo "Manual attach command:"
    echo "  $CLI --base-url \"$BASE_URL\" session attach --session \"$session_id\" --after 999999"
    echo
    echo "Inside attach, enter:"
    echo "  a"
    echo
    echo "Direct approve command:"
    echo "  $CLI --base-url \"$BASE_URL\" session intervention approve --session \"$session_id\" --turn \"$turn_id\" --call \"$call_id\" --reason \"manual approval test\""
    echo
    echo "Direct reject command:"
    echo "  $CLI --base-url \"$BASE_URL\" session intervention reject --session \"$session_id\" --turn \"$turn_id\" --call \"$call_id\" --reason \"manual reject test\""
    ;;
  approve)
    echo "Approving pending call"
    "$CLI" --base-url "$BASE_URL" session intervention approve \
      --session "$session_id" \
      --turn "$turn_id" \
      --call "$call_id" \
      --reason "automated approval verification" >/dev/null

    echo "Waiting for Agent Core approval continuation"
    if ! events_json="$(wait_for_idle_agent "$session_id" "$turn_id")"; then
      echo "Timed out waiting for Agent Core approval continuation." >&2
      echo "Recent events:" >&2
      "$CLI" --base-url "$BASE_URL" event list --session "$session_id" >&2 || true
      exit 5
    fi
    printf '%s' "$events_json" | event_summary
    ;;
  reject)
    echo "Rejecting pending call"
    "$CLI" --base-url "$BASE_URL" session intervention reject \
      --session "$session_id" \
      --turn "$turn_id" \
      --call "$call_id" \
      --reason "automated reject verification" >/dev/null

    echo "Rejected. Current rejected interventions:"
    "$CLI" --base-url "$BASE_URL" session intervention list --session "$session_id" --status rejected
    ;;
  *)
    echo "unsupported TMA_APPROVAL_TEST_DECISION=$DECISION; use manual, approve, or reject" >&2
    exit 3
    ;;
esac

if [ "$PRINT_EVENTS" = "true" ]; then
  echo
  echo "Session events:"
  "$CLI" --base-url "$BASE_URL" event list --session "$session_id"
fi
