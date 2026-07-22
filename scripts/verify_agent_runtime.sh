#!/bin/sh
set -eu

BASE_URL="${TMA_BASE_URL:-http://localhost:8080}"
CLI="${TMA_CLI:-bin/tma}"
MESSAGE="${TMA_VERIFY_MESSAGE:-agent runtime verify}"
EXPECTED_TEXT="${TMA_VERIFY_EXPECTED_TEXT-Agent runtime received: agent runtime verify}"
EXPECTED_PROTOCOL="${TMA_VERIFY_EXPECTED_PROTOCOL:-tma.agent_loop.message.v1}"
REQUIRED_EVENTS="${TMA_VERIFY_REQUIRED_EVENTS:-session.status_running,user.message,runtime.started,model.requested,model.responded,completion.started,completion.validated,runtime.completed,agent.message,session.status_idle}"
AGENT_MODEL="${TMA_VERIFY_AGENT_MODEL:-verify-model}"
AGENT_SYSTEM="${TMA_VERIFY_AGENT_SYSTEM:-AgentRuntime verification agent.}"
WAIT_SECONDS="${TMA_VERIFY_WAIT_SECONDS:-10}"

if [ ! -x "$CLI" ]; then
  echo "missing CLI: $CLI"
  echo "run: make build-cli"
  exit 1
fi

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
  EXPECTED_TEXT="$EXPECTED_TEXT" EXPECTED_PROTOCOL="$EXPECTED_PROTOCOL" REQUIRED_EVENTS="$REQUIRED_EVENTS" python3 -c '
import json
import os
import sys

expected_text = os.environ["EXPECTED_TEXT"]
expected_protocol = os.environ["EXPECTED_PROTOCOL"]
required = [value.strip() for value in os.environ["REQUIRED_EVENTS"].split(",") if value.strip()]
data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]

missing = [event_type for event_type in required if event_type not in types]
if missing:
    print("missing event types: " + ", ".join(missing), file=sys.stderr)
    sys.exit(2)

agent_events = [event for event in events if event.get("type") == "agent.message"]
if not agent_events:
    print("agent.message not found", file=sys.stderr)
    sys.exit(3)

agent_payload = agent_events[-1].get("payload", {})
if expected_protocol and agent_payload.get("protocol_version") != expected_protocol:
    print("expected protocol_version not found: " + expected_protocol, file=sys.stderr)
    print("actual payload: " + repr(agent_payload), file=sys.stderr)
    sys.exit(4)

content = agent_payload.get("content", [])
texts = [item.get("text", "") for item in content if item.get("type") == "text"]
if expected_text:
    if expected_text not in texts:
        print("expected agent text not found: " + expected_text, file=sys.stderr)
        print("actual texts: " + repr(texts), file=sys.stderr)
        sys.exit(5)
elif not any(text.strip() for text in texts):
    print("agent.message does not contain non-empty text", file=sys.stderr)
    print("actual texts: " + repr(texts), file=sys.stderr)
    sys.exit(5)

turn_id = agent_payload.get("turn_id", "")
if not turn_id:
    print("agent.message missing payload.turn_id", file=sys.stderr)
    sys.exit(6)

same_turn_events = [
    event for event in events
    if event.get("payload", {}).get("turn_id") == turn_id
]
same_turn_types = [event.get("type") for event in same_turn_events]
for event_type in required:
    if event_type not in same_turn_types:
        print(f"turn {turn_id} missing {event_type}", file=sys.stderr)
        sys.exit(7)

print(turn_id)
'
}

echo "Checking health: $BASE_URL"
"$CLI" --base-url "$BASE_URL" health >/dev/null

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-agent-runtime-agent-$suffix" \
  --model "$AGENT_MODEL" \
  --system "$AGENT_SYSTEM")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-agent-runtime-env-$suffix" \
  --config '{"type":"verification"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "AgentRuntime verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Sending message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "$MESSAGE" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events 2>/dev/null)"; then
    echo "AgentRuntime verification passed"
    echo "session_id=$session_id"
    echo "turn_id=$turn_id"
    exit 0
  fi
  sleep 1
done

echo "AgentRuntime verification timed out after ${WAIT_SECONDS}s" >&2
if [ -n "$EXPECTED_TEXT" ]; then
  echo "Expected agent text: $EXPECTED_TEXT" >&2
else
  echo "Expected non-empty agent text" >&2
fi
echo "Expected protocol version: ${EXPECTED_PROTOCOL:-<skip>}" >&2
echo "Last events:" >&2
printf '%s\n' "$last_events" >&2
exit 1
