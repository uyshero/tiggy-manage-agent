#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18189}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18189}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
WORKER_BIN="${TMA_WORKER_BIN:-bin/tma-worker}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_SERVER_LOG:-.verify-worker-plugin-server.log}"
WORKER_LOG="${TMA_VERIFY_WORKER_LOG:-.verify-worker-plugin-worker.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
WORKER_TOKEN="${TMA_VERIFY_WORKER_TOKEN:-verify-worker-plugin-token}"

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

plugin_dir="$(mktemp -d "${TMPDIR:-/tmp}/tma-worker-plugin.XXXXXX")"
plugin="$plugin_dir/robot-plugin"
cat >"$plugin" <<'PLUGIN'
#!/bin/sh
set -eu

case "${1:-}" in
  manifest)
    printf '%s' '{"identifier":"robot","type":"process_plugin","meta":{"title":"Robot","description":"Robot verification plugin."},"system_role":"Use robot.* tools only for robot verification tasks.","api":[{"name":"get_state","namespace":"robot","api":"get_state","description":"Read robot state.","parameters":{"type":"object","properties":{}},"capabilities":["robot.state"],"risk":"read","runtime":{"allowed":["local_system"],"preferred":"local_system"},"implementation":"worker_capability"}]}'
    ;;
  execute)
    cat >/dev/null
    printf '%s' '{"protocol_version":"tma.plugin_result.v1","success":true,"content":"tma-worker-plugin-ok","state":{"status":"idle","marker":"tma-worker-plugin-ok"}}'
    ;;
  *)
    echo "unknown plugin command: ${1:-}" >&2
    exit 64
    ;;
esac
PLUGIN
chmod +x "$plugin"

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
    if worker.get("name") != "verify-plugin-worker" or worker.get("status") != "online":
        continue
    capabilities = worker.get("capabilities") or {}
    manifests = capabilities.get("manifests") or []
    if not any(manifest.get("identifier") == "robot" for manifest in manifests):
        continue
    if "robot.get_state" not in (capabilities.get("apis") or []):
        continue
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

tool_calls = [event for event in events if event.get("type") == "runtime.tool_call"]
if not any((event.get("payload") or {}).get("data", {}).get("identifier") == "robot" and (event.get("payload") or {}).get("data", {}).get("api_name") == "get_state" for event in tool_calls):
    raise SystemExit(2)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
content = tool_results[-1].get("payload", {}).get("data", {}).get("content", "")
if "tma-worker-plugin-ok" not in content:
    raise SystemExit(3)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any("tma-worker-plugin-ok" in text for text in texts):
    raise SystemExit(4)

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
if payload.get("protocol_version") != "tma.work.v1" or payload.get("namespace") != "robot" or payload.get("api") != "get_state":
    print("unexpected work payload: " + repr(payload), file=sys.stderr)
    raise SystemExit(3)
if payload.get("capabilities") != ["robot.state"] or payload.get("risk") != "read":
    print("unexpected work metadata: " + repr(payload), file=sys.stderr)
    raise SystemExit(4)
result = work.get("result") or {}
tool_result = result.get("tool_result") or {}
if "tma-worker-plugin-ok" not in tool_result.get("content", ""):
    print("worker plugin result missing marker: " + repr(tool_result), file=sys.stderr)
    raise SystemExit(5)
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
  rm -rf "$plugin_dir"
}
trap cleanup EXIT INT TERM

echo "Starting TMA server for worker plugin verification"
echo "base_url=$BASE_URL"
echo "server_log=$SERVER_LOG"
echo "worker_log=$WORKER_LOG"
echo "plugin=$plugin"

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
if ! kill -0 "$server_pid" 2>/dev/null; then
  echo "server exited after health check" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

echo "Starting plugin worker"
TMA_WORKER_TOKEN="$WORKER_TOKEN" \
"$WORKER_BIN" \
  --base-url "$BASE_URL" \
  --name verify-plugin-worker \
  --workspace wksp_default \
  --plugin "$plugin" \
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
  echo "worker did not publish robot plugin capabilities within ${WAIT_SECONDS}s" >&2
  cat "$WORKER_LOG" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-worker-plugin-agent-$suffix" \
  --model "fake-demo" \
  --system "Worker plugin verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Configuring agent tools for robot plugin"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"tools":["robot"],"runtime":"local_system"}' >/dev/null

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-worker-plugin-env-$suffix" \
  --config '{"type":"verification","runtime":"local_system"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Worker plugin verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending plugin verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_worker_plugin_tool" >/dev/null

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
  echo "worker plugin verification timed out after ${WAIT_SECONDS}s" >&2
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

echo "Worker plugin verification passed"
echo "session_id=$session_id"
echo "turn_id=$turn_id"
echo "worker_id=$worker_id"
echo "work_id=$work_id"
