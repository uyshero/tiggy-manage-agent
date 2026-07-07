#!/bin/sh
set -eu

BASE_URL="${TMA_BASE_URL:-http://localhost:18081}"
CLI="${TMA_CLI:-bin/tma}"
MESSAGE="${TMA_VERIFY_LLM_MESSAGE:-hello from tma llm provider verification}"
WAIT_SECONDS="${TMA_VERIFY_LLM_WAIT_SECONDS:-90}"
DOTENV_PATH="${TMA_DOTENV_PATH:-.env}"

if [ ! -x "$CLI" ]; then
  echo "missing CLI: $CLI"
  echo "run: make build-cli"
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

validate_events() {
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]

required = [
    "session.status_running",
    "user.message",
    "runtime.started",
    "runtime.thinking",
    "runtime.llm_request",
    "runtime.llm_response",
    "runtime.completed",
    "agent.message",
    "session.status_idle",
]
missing = [event_type for event_type in required if event_type not in types]
if missing:
    failed = [event for event in events if event.get("type") == "runtime.failed"]
    if failed:
        print("runtime.failed: " + repr(failed[-1].get("payload", {})), file=sys.stderr)
    print("missing event types: " + ", ".join(missing), file=sys.stderr)
    sys.exit(2)

agent_events = [event for event in events if event.get("type") == "agent.message"]
if not agent_events:
    print("agent.message not found", file=sys.stderr)
    sys.exit(3)

agent_payload = agent_events[-1].get("payload", {})
turn_id = agent_payload.get("turn_id", "")
if not turn_id:
    print("agent.message missing payload.turn_id", file=sys.stderr)
    sys.exit(4)

content = agent_payload.get("content", [])
texts = [item.get("text", "") for item in content if item.get("type") == "text"]
if not any(text.strip() for text in texts):
    print("agent.message has no non-empty text", file=sys.stderr)
    print("actual payload: " + repr(agent_payload), file=sys.stderr)
    sys.exit(5)

same_turn_events = [
    event for event in events
    if event.get("payload", {}).get("turn_id") == turn_id
]
same_turn_types = [event.get("type") for event in same_turn_events]
for event_type in required:
    if event_type not in same_turn_types:
        print(f"turn {turn_id} missing {event_type}", file=sys.stderr)
        sys.exit(6)

delta_count = same_turn_types.count("runtime.llm_delta")
print(json.dumps({
    "turn_id": turn_id,
    "delta_count": delta_count,
    "text_preview": texts[0][:120],
}, ensure_ascii=False))
'
}

provider="$(env_value TMA_LLM_PROVIDER)"
provider_type="$(env_value TMA_LLM_PROVIDER_TYPE)"
model="$(env_value TMA_LLM_MODEL)"
base_url="$(env_value TMA_LLM_BASE_URL)"

if [ -z "$provider" ]; then
  echo "TMA_LLM_PROVIDER is required for verify-llm-provider" >&2
  exit 1
fi
if [ -z "$model" ]; then
  echo "TMA_LLM_MODEL is required for verify-llm-provider" >&2
  exit 1
fi

echo "Checking health: $BASE_URL"
"$CLI" --base-url "$BASE_URL" health >/dev/null

echo "Verifying LLM provider"
echo "provider=$provider"
echo "provider_type=${provider_type:-<default>}"
echo "model=$model"
echo "base_url=${base_url:-<default>}"

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-llm-provider-agent-$suffix" \
  --llm-provider "$provider" \
  --llm-model "$model" \
  --system "You are a concise TMA LLM provider verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-llm-provider-env-$suffix" \
  --config '{"type":"llm-provider-verification"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "LLM Provider verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Sending message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "$MESSAGE" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
last_error=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if result="$(printf '%s' "$last_events" | validate_events 2>/tmp/tma-verify-llm-provider.err)"; then
    echo "LLM provider verification passed"
    echo "session_id=$session_id"
    printf '%s\n' "$result"
    exit 0
  fi
  last_error="$(cat /tmp/tma-verify-llm-provider.err 2>/dev/null || true)"
  sleep 1
done

echo "LLM provider verification timed out after ${WAIT_SECONDS}s" >&2
echo "Last validation error:" >&2
printf '%s\n' "$last_error" >&2
echo "Last events:" >&2
printf '%s\n' "$last_events" >&2
exit 1
