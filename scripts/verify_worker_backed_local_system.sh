#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18082}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18082}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
WORKER_BIN="${TMA_WORKER_BIN:-bin/tma-worker}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_SERVER_LOG:-.verify-worker-backed-server.log}"
WORKER_LOG="${TMA_VERIFY_WORKER_LOG:-.verify-worker-backed-worker.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
WORKER_TOKEN="${TMA_VERIFY_WORKER_TOKEN:-verify-worker-token}"

if [ ! -x "$SERVER_BIN" ]; then
  echo "missing server binary: $SERVER_BIN"
  echo "run: make build"
  exit 1
fi

if [ ! -x "$WORKER_BIN" ]; then
  echo "missing worker binary: $WORKER_BIN"
  echo "run: make build-worker"
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

validate_worker_online() {
  python3 -c '
import json
import sys

workers = json.load(sys.stdin).get("workers", [])
for worker in workers:
    if worker.get("name") == "verify-local-worker" and worker.get("status") == "online":
        print(worker.get("id", ""))
        raise SystemExit(0)
raise SystemExit(1)
'
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
        raise SystemExit(1)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
content = tool_results[-1].get("payload", {}).get("data", {}).get("content", "")
if "tma-session-tool-ok" not in content:
    raise SystemExit(2)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any("tma-session-tool-ok" in text for text in texts):
    raise SystemExit(3)

print(agent_payload.get("turn_id", ""))
'
}

extract_completed_work_id() {
  python3 -c '
import json
import sys

work_id = ""
try:
    lines = open(sys.argv[1], encoding="utf-8").read().splitlines()
except FileNotFoundError:
    raise SystemExit(1)
for line in lines:
    try:
        entry = json.loads(line)
    except json.JSONDecodeError:
        continue
    if entry.get("msg") == "worker work completed" and entry.get("status") == "completed":
        work_id = entry.get("work_id", work_id)
if not work_id:
    raise SystemExit(1)
print(work_id)
' "$1"
}

validate_completed_work() {
  python3 -c '
import json
import sys

work = json.load(sys.stdin)
if work.get("status") != "completed":
    print("unexpected work status: " + repr(work.get("status")), file=sys.stderr)
    raise SystemExit(1)
if work.get("work_type") != "tool_execution":
    print("unexpected work type: " + repr(work.get("work_type")), file=sys.stderr)
    raise SystemExit(2)
payload = work.get("payload") or {}
if payload.get("protocol_version") != "tma.work.v1" or payload.get("namespace") != "default" or payload.get("api") != "run_command":
    print("unexpected work payload: " + repr(payload), file=sys.stderr)
    raise SystemExit(3)
result = work.get("result") or {}
tool_result = result.get("tool_result") or {}
if "tma-session-tool-ok" not in tool_result.get("content", ""):
    print("worker result missing marker: " + repr(tool_result), file=sys.stderr)
    raise SystemExit(4)
'
}

server_pid=""
worker_pid=""
cleanup() {
  if [ -n "$worker_pid" ]; then
    if kill -0 "$worker_pid" 2>/dev/null; then
      kill "$worker_pid" 2>/dev/null || true
      wait "$worker_pid" 2>/dev/null || true
    fi
  fi
  if [ -n "$server_pid" ]; then
    if kill -0 "$server_pid" 2>/dev/null; then
      kill "$server_pid" 2>/dev/null || true
      wait "$server_pid" 2>/dev/null || true
    fi
  fi
}
trap cleanup EXIT INT TERM

echo "Starting TMA server for worker-backed local_system verification"
echo "base_url=$BASE_URL"
echo "server_log=$SERVER_LOG"
echo "worker_log=$WORKER_LOG"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_TOOL_RUNTIME=cloud_sandbox \
TMA_WORKER_AUTH_TOKEN="$WORKER_TOKEN" \
"$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    cat "$SERVER_LOG" >&2 || true
    exit 1
  fi
  if TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null 2>&1; then
  echo "server did not become healthy within ${WAIT_SECONDS}s" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

echo "Starting local worker"
TMA_WORKER_TOKEN="$WORKER_TOKEN" \
"$WORKER_BIN" \
  --base-url "$BASE_URL" \
  --name verify-local-worker \
  --workspace wksp_default \
  --poll-interval 1s \
  --heartbeat-interval 5s \
  --lease-seconds 30 >"$WORKER_LOG" 2>&1 &
worker_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
worker_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$worker_pid" 2>/dev/null; then
    echo "worker exited before becoming online" >&2
    cat "$WORKER_LOG" >&2 || true
    exit 1
  fi
  workers_json="$("$CLI" --base-url "$BASE_URL" worker list --workspace wksp_default --status online --json)"
  if worker_id="$(printf '%s' "$workers_json" | validate_worker_online 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$worker_id" ]; then
  echo "worker did not become online within ${WAIT_SECONDS}s" >&2
  cat "$WORKER_LOG" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-worker-backed-agent-$suffix" \
  --model "fake-demo" \
  --system "Worker-backed local_system verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Configuring agent tools for local_system"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"tools":["default"],"runtime":"local_system"}' >/dev/null

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-worker-backed-env-$suffix" \
  --config '{"type":"verification","runtime":"local_system"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Worker-backed local_system verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending tool verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_tool_call" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$turn_id" ]; then
  echo "worker-backed local_system verification timed out after ${WAIT_SECONDS}s" >&2
  echo "Last events:" >&2
  printf '%s\n' "$last_events" >&2
  echo "Worker log:" >&2
  cat "$WORKER_LOG" >&2 || true
  echo "Server log:" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

work_id="$(extract_completed_work_id "$WORKER_LOG")"
work_json="$("$CLI" --base-url "$BASE_URL" work get --work "$work_id")"
printf '%s' "$work_json" | validate_completed_work

echo "Worker-backed local_system verification passed"
echo "session_id=$session_id"
echo "turn_id=$turn_id"
echo "worker_id=$worker_id"
echo "work_id=$work_id"
