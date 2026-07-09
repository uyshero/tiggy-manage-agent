#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18081}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18081}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-onlyboxes-session-server.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-20}"
SANDBOX_ROOT="${TMA_CLOUD_SANDBOX_ROOT:-.}"
SANDBOX_IMAGE="${TMA_CLOUD_SANDBOX_IMAGE:-coolfan1024/onlyboxes-runtime:default}"

if [ ! -x "$SERVER_BIN" ]; then
  echo "missing server binary: $SERVER_BIN"
  echo "run: make build"
  exit 1
fi

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
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]
for event_type in ["runtime.tool_call", "runtime.tool_result", "agent.message", "session.status_idle"]:
    if event_type not in types:
        print("missing event type: " + event_type, file=sys.stderr)
        sys.exit(2)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
if not tool_results:
    print("runtime.tool_result not found", file=sys.stderr)
    sys.exit(3)
result_data = tool_results[-1].get("payload", {}).get("data", {})
content = result_data.get("content", "")
if "tma-session-tool-ok" not in content:
    print("tool result missing expected marker: " + repr(content), file=sys.stderr)
    sys.exit(4)
if "/workspace" not in content:
    print("tool result did not run inside cloud_sandbox /workspace: " + repr(content), file=sys.stderr)
    sys.exit(5)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any("tma-session-tool-ok" in text for text in texts):
    print("agent.message missing expected marker: " + repr(texts), file=sys.stderr)
    sys.exit(6)

print(agent_payload.get("turn_id", ""))
'
}

server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then
    if kill -0 "$server_pid" 2>/dev/null; then
      kill "$server_pid" 2>/dev/null || true
      wait "$server_pid" 2>/dev/null || true
    fi
  fi
}
trap cleanup EXIT INT TERM

echo "Starting TMA server for Onlyboxes session verification"
echo "base_url=$BASE_URL"
echo "sandbox_root=$SANDBOX_ROOT"
echo "sandbox_image=$SANDBOX_IMAGE"
echo "server_log=$LOG_FILE"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_TOOL_RUNTIME=cloud_sandbox \
TMA_CLOUD_SANDBOX_ROOT="$SANDBOX_ROOT" \
TMA_CLOUD_SANDBOX_IMAGE="$SANDBOX_IMAGE" \
"$SERVER_BIN" >"$LOG_FILE" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    echo "server log:" >&2
    cat "$LOG_FILE" >&2 || true
    exit 1
  fi

  if TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null 2>&1; then
    break
  fi

  sleep 1
done

if ! TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null 2>&1; then
  echo "server did not become healthy within ${WAIT_SECONDS}s" >&2
  echo "server log:" >&2
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-onlyboxes-agent-$suffix" \
  --model "fake-demo" \
  --system "Cloud sandbox Onlyboxes session verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-onlyboxes-env-$suffix" \
  --config '{"type":"verification","sandbox":"cloud_sandbox"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Cloud sandbox Onlyboxes session verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session runtime"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending tool verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_tool_call" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events 2>/dev/null)"; then
    echo "Onlyboxes session verification passed"
    echo "session_id=$session_id"
    echo "turn_id=$turn_id"
    exit 0
  fi
  sleep 1
done

echo "Onlyboxes session verification timed out after ${WAIT_SECONDS}s" >&2
echo "Last events:" >&2
printf '%s\n' "$last_events" >&2
echo "Server log:" >&2
cat "$LOG_FILE" >&2 || true
exit 1
