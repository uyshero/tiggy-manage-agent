#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18190}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18190}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
WORKER_BIN="${TMA_WORKER_BIN:-bin/tma-worker}"
CLI="${TMA_CLI:-bin/tma}"
PLUGIN="${TMA_COMPUTER_PLUGIN:-examples/plugins/computer-use/computer-plugin.py}"
SERVER_LOG="${TMA_VERIFY_SERVER_LOG:-.verify-computer-plugin-server.log}"
WORKER_LOG="${TMA_VERIFY_WORKER_LOG:-.verify-computer-plugin-worker.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
WORKER_TOKEN="${TMA_VERIFY_WORKER_TOKEN:-verify-computer-plugin-token}"

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

if [ ! -x "$PLUGIN" ]; then
  echo "missing computer plugin: $PLUGIN"
  exit 1
fi

plugin_tmp="$(mktemp -d "${TMPDIR:-/tmp}/tma-computer-plugin.XXXXXX")"
fake_cua="$plugin_tmp/fake-cua"
cat >"$fake_cua" <<'FAKECUA'
#!/bin/sh
set -eu
tool="${1:-}"
args_json="${2:-}"
if [ -z "$args_json" ]; then
  args_json="{}"
fi
python3 -c '
import json
import sys

tool = sys.argv[1]
args_text = sys.argv[2]
payload = {"fake_cua_tool": tool, "fake_cua_args_text": args_text}
try:
    args = json.loads(args_text)
except Exception:
    args = {}
if tool == "launch_app":
    payload.update({"pid": 4242, "name": args.get("name") or args.get("app")})
elif tool == "bring_to_front":
    payload.update({"pid": args.get("pid"), "activated": True})
elif tool == "get_accessibility_tree":
    payload.update(
        {
            "apps": [{"name": "Google Chrome", "pid": 4242}],
            "windows": [{"app_name": "Google Chrome", "title": "Chrome", "pid": 4242}],
        }
    )
elif tool in {"hotkey", "type_text", "click"}:
    payload.update({"received_args": args})
if tool == "get_desktop_state":
    payload.update(
        {
            "platform": "fakeos",
            "screen_width": 1,
            "screen_height": 1,
            "screenshot_width": 1,
            "screenshot_height": 1,
            "screenshot_mime_type": "image/png",
            "screenshot_png_b64": "iVBORw0KGgo=",
        }
    )
print(json.dumps(payload, separators=(",", ":")))
' "$tool" "$args_json"
FAKECUA
chmod +x "$fake_cua"

validate_direct_cua_backend() {
  python3 -c '
import json
import sys

payload = json.load(sys.stdin)
if payload.get("success") is not True:
    raise SystemExit(1)
state = payload.get("state") or {}
if state.get("backend") != "cua":
    raise SystemExit(2)
result = state.get("result") or {}
if result.get("fake_cua_tool") != "get_accessibility_tree":
    raise SystemExit(3)
if "capture_mode" not in result.get("fake_cua_args_text", ""):
    raise SystemExit(4)
'
}

validate_direct_cua_screenshot_backend() {
  python3 -c '
import json
import os
import sys

payload = json.load(sys.stdin)
if payload.get("success") is not True:
    raise SystemExit(1)
state = payload.get("state") or {}
if state.get("backend") != "cua" or state.get("cua_tool") != "get_desktop_state":
    raise SystemExit(2)
result = state.get("result") or {}
if result.get("fake_cua_tool") != "get_desktop_state":
    raise SystemExit(3)
if "screenshot_png_b64" in result:
    raise SystemExit(4)
screenshot_path = result.get("screenshot_path")
if not result.get("has_screenshot") or not screenshot_path or not os.path.exists(screenshot_path):
    raise SystemExit(5)
exported = payload.get("exported_files") or []
if not exported or exported[0].get("path") != screenshot_path or exported[0].get("content_type") != "image/png":
    raise SystemExit(6)
'
}

validate_direct_cua_hotkey_pid_resolution() {
  python3 -c '
import json
import sys

payload = json.load(sys.stdin)
if payload.get("success") is not True:
    raise SystemExit(1)
state = payload.get("state") or {}
if state.get("backend") != "cua" or state.get("cua_tool") != "hotkey":
    raise SystemExit(2)
args = (state.get("result") or {}).get("received_args") or {}
if args.get("pid") != 4242:
    raise SystemExit(3)
if args.get("keys") != ["cmd", "l"]:
    raise SystemExit(4)
'
}

validate_direct_cua_search_web_backend() {
  python3 -c '
import json
import sys

payload = json.load(sys.stdin)
if payload.get("success") is not True:
    raise SystemExit(1)
state = payload.get("state") or {}
if state.get("backend") != "cua" or state.get("cua_tool") != "search_web":
    raise SystemExit(2)
result = state.get("result") or {}
if result.get("pid") != 4242 or result.get("engine") != "baidu" or result.get("query") != "李国庆":
    raise SystemExit(3)
if result.get("url") != "https://www.baidu.com/s?wd=%E6%9D%8E%E5%9B%BD%E5%BA%86":
    raise SystemExit(4)
typed_args = (result.get("type_text") or {}).get("received_args") or {}
if typed_args.get("pid") != 4242 or typed_args.get("delivery_mode") != "foreground":
    raise SystemExit(5)
submit_args = (result.get("submit") or {}).get("received_args") or {}
if submit_args.get("keys") != ["enter"]:
    raise SystemExit(6)
'
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

validate_worker_online() {
  python3 -c '
import json
import sys

workers = json.load(sys.stdin).get("workers", [])
for worker in workers:
    if worker.get("name") != "verify-computer-plugin-worker" or worker.get("status") != "online":
        continue
    capabilities = worker.get("capabilities") or {}
    manifests = capabilities.get("manifests") or []
    manifest = next((item for item in manifests if item.get("identifier") == "computer"), None)
    if not manifest:
        continue
    api_names = {api.get("name") for api in manifest.get("api") or []}
    if "get_state" not in api_names or "click" not in api_names:
        continue
    if "computer.get_state" not in (capabilities.get("apis") or []):
        continue
    if "computer.ax.read" not in (capabilities.get("capabilities") or []):
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
if not any((event.get("payload") or {}).get("data", {}).get("identifier") == "computer" and (event.get("payload") or {}).get("data", {}).get("api_name") == "get_state" for event in tool_calls):
    raise SystemExit(2)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
result_payload = tool_results[-1].get("payload", {}).get("data", {})
content = result_payload.get("content", "")
state = result_payload.get("state", {})
if "computer.get_state completed via cua" not in content:
    raise SystemExit(3)
if not isinstance(state, dict) or state.get("backend") != "cua" or state.get("cua_tool") != "get_accessibility_tree" or "ui_tree" not in state:
    raise SystemExit(4)
result = state.get("result", {})
if not isinstance(result, dict) or result.get("fake_cua_tool") != "get_accessibility_tree":
    raise SystemExit(6)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any("computer.get_state completed via cua" in text for text in texts):
    raise SystemExit(5)

print(agent_payload.get("turn_id", ""))
'
}

validate_screenshot_events() {
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
if not any((event.get("payload") or {}).get("data", {}).get("identifier") == "computer" and (event.get("payload") or {}).get("data", {}).get("api_name") == "screenshot" for event in tool_calls):
    raise SystemExit(2)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
result_payload = tool_results[-1].get("payload", {}).get("data", {})
content = result_payload.get("content", "")
if "computer.screenshot completed via cua" not in content:
    raise SystemExit(3)
if result_payload.get("artifact_error"):
    raise SystemExit(4)
if not result_payload.get("artifacts"):
    raise SystemExit(5)
state = result_payload.get("state", {})
if not isinstance(state, dict):
    raise SystemExit(6)
result = state.get("result", {})
if not isinstance(result, dict) or result.get("has_screenshot") is not True:
    raise SystemExit(7)
if "screenshot_png_b64" in result:
    raise SystemExit(8)
if not result.get("screenshot_path"):
    raise SystemExit(9)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any("computer.screenshot completed via cua" in text for text in texts):
    raise SystemExit(10)

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
if payload.get("protocol_version") != "tma.work.v1" or payload.get("namespace") != "computer" or payload.get("api") != "get_state":
    print("unexpected work payload: " + repr(payload), file=sys.stderr)
    raise SystemExit(3)
expected_capabilities = ["computer.state.read", "computer.ax.read"]
if payload.get("capabilities") != expected_capabilities or payload.get("risk") != "read" or payload.get("runtime") != "local_system":
    print("unexpected work metadata: " + repr(payload), file=sys.stderr)
    raise SystemExit(4)
input_payload = payload.get("input") or {}
if input_payload.get("capture_mode") != "ax":
    print("expected ax capture mode: " + repr(input_payload), file=sys.stderr)
    raise SystemExit(5)
result = work.get("result") or {}
tool_result = result.get("tool_result") or {}
if "computer.get_state completed via cua" not in tool_result.get("content", ""):
    print("computer plugin result missing marker: " + repr(tool_result), file=sys.stderr)
    raise SystemExit(6)
state = tool_result.get("state") or {}
if state.get("backend") != "cua" or state.get("cua_tool") != "get_accessibility_tree" or "ui_tree" not in state:
    print("computer plugin state missing ui_tree marker: " + repr(state), file=sys.stderr)
    raise SystemExit(7)
result_state = state.get("result") or {}
if result_state.get("fake_cua_tool") != "get_accessibility_tree":
    print("computer plugin state missing fake CUA marker: " + repr(result_state), file=sys.stderr)
    raise SystemExit(8)
'
}

validate_screenshot_completed_work() {
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
if payload.get("protocol_version") != "tma.work.v1" or payload.get("namespace") != "computer" or payload.get("api") != "screenshot":
    print("unexpected work payload: " + repr(payload), file=sys.stderr)
    raise SystemExit(3)
expected_capabilities = ["computer.screen.capture"]
if payload.get("capabilities") != expected_capabilities or payload.get("risk") != "read" or payload.get("runtime") != "local_system":
    print("unexpected work metadata: " + repr(payload), file=sys.stderr)
    raise SystemExit(4)
result = work.get("result") or {}
tool_result = result.get("tool_result") or {}
if "computer.screenshot completed via cua" not in tool_result.get("content", ""):
    print("computer screenshot result missing marker: " + repr(tool_result), file=sys.stderr)
    raise SystemExit(5)
state = tool_result.get("state") or {}
if state.get("backend") != "cua" or state.get("cua_tool") != "get_desktop_state":
    print("computer screenshot state missing backend marker: " + repr(state), file=sys.stderr)
    raise SystemExit(6)
result_state = state.get("result") or {}
if result_state.get("has_screenshot") is not True or result_state.get("screenshot_png_b64") is not None:
    print("computer screenshot state missing sanitized screenshot fields: " + repr(result_state), file=sys.stderr)
    raise SystemExit(7)
if not result_state.get("screenshot_path"):
    print("computer screenshot state missing screenshot path: " + repr(result_state), file=sys.stderr)
    raise SystemExit(8)
if not tool_result.get("artifacts"):
    print("computer screenshot result missing artifact refs: " + repr(tool_result), file=sys.stderr)
    raise SystemExit(9)
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
  rm -rf "$plugin_tmp"
}
trap cleanup EXIT INT TERM

printf '%s' '{"protocol_version":"tma.plugin.v1","call":{"id":"call_direct_cua","identifier":"computer","api_name":"get_state","arguments":{"capture_mode":"ax"}},"context":{"workspace_id":"wksp_default"}}' \
  | TMA_COMPUTER_BACKEND=cua TMA_COMPUTER_CUA_TEMPLATE="$fake_cua {tool} {args_json}" "$PLUGIN" execute \
  | validate_direct_cua_backend

printf '%s' '{"protocol_version":"tma.plugin.v1","call":{"id":"call_direct_cua_screenshot","identifier":"computer","api_name":"screenshot","arguments":{}},"context":{"workspace_id":"wksp_default"}}' \
  | TMA_COMPUTER_BACKEND=cua TMA_COMPUTER_CUA_TEMPLATE="$fake_cua {tool} {args_json}" TMA_COMPUTER_OUTPUT_DIR="$plugin_tmp" "$PLUGIN" execute \
  | validate_direct_cua_screenshot_backend

printf '%s' '{"protocol_version":"tma.plugin.v1","call":{"id":"call_direct_cua_hotkey","identifier":"computer","api_name":"hotkey","arguments":{"keys":["Command","L"]}},"context":{"workspace_id":"wksp_default"}}' \
  | TMA_COMPUTER_BACKEND=cua TMA_COMPUTER_CUA_TEMPLATE="$fake_cua {tool} {args_json}" "$PLUGIN" execute \
  | validate_direct_cua_hotkey_pid_resolution

printf '%s' '{"protocol_version":"tma.plugin.v1","call":{"id":"call_direct_cua_search","identifier":"computer","api_name":"search_web","arguments":{"query":"李国庆","engine":"baidu","browser":"Google Chrome"}},"context":{"workspace_id":"wksp_default"}}' \
  | TMA_COMPUTER_BACKEND=cua TMA_COMPUTER_CUA_TEMPLATE="$fake_cua {tool} {args_json}" "$PLUGIN" execute \
  | validate_direct_cua_search_web_backend

echo "Starting TMA server for computer plugin verification"
echo "base_url=$BASE_URL"
echo "server_log=$SERVER_LOG"
echo "worker_log=$WORKER_LOG"
echo "plugin=$PLUGIN"

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

echo "Starting computer plugin worker"
TMA_WORKER_TOKEN="$WORKER_TOKEN" \
TMA_COMPUTER_BACKEND=cua \
TMA_COMPUTER_CUA_TEMPLATE="$fake_cua {tool} {args_json}" \
TMA_COMPUTER_OUTPUT_DIR="$plugin_tmp" \
"$WORKER_BIN" \
  --base-url "$BASE_URL" \
  --name verify-computer-plugin-worker \
  --workspace wksp_default \
  --plugin "$PLUGIN" \
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
  echo "worker did not publish computer plugin capabilities within ${WAIT_SECONDS}s" >&2
  cat "$WORKER_LOG" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-computer-plugin-agent-$suffix" \
  --model "fake-demo" \
  --system "Computer plugin verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Configuring agent tools for computer plugin"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"tools":["computer"],"runtime":"local_system"}' >/dev/null

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-computer-plugin-env-$suffix" \
  --config '{"type":"verification","runtime":"local_system"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Computer plugin verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending computer plugin verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_computer_plugin_tool" >/dev/null

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
  echo "computer plugin verification timed out after ${WAIT_SECONDS}s" >&2
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

echo "Creating screenshot verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Computer plugin screenshot verification $suffix")"
screenshot_session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring screenshot verification session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$screenshot_session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending computer plugin screenshot verification message to $screenshot_session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$screenshot_session_id" \
  --text "tma.verify_computer_plugin_screenshot" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_screenshot_events=""
screenshot_turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_screenshot_events="$("$CLI" --base-url "$BASE_URL" event list --session "$screenshot_session_id" --after 0)"
  if screenshot_turn_id="$(printf '%s' "$last_screenshot_events" | validate_screenshot_events 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$screenshot_turn_id" ]; then
  echo "computer screenshot verification timed out after ${WAIT_SECONDS}s" >&2
  echo "Last screenshot events:" >&2
  printf '%s\n' "$last_screenshot_events" >&2
  echo "Worker log:" >&2
  cat "$WORKER_LOG" >&2 || true
  echo "Server log:" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

screenshot_work_id="$(extract_completed_work_id "$WORKER_LOG")"
screenshot_work_json="$("$CLI" --base-url "$BASE_URL" work get --work "$screenshot_work_id")"
printf '%s' "$screenshot_work_json" | validate_screenshot_completed_work

echo "Computer plugin verification passed"
echo "session_id=$session_id"
echo "turn_id=$turn_id"
echo "worker_id=$worker_id"
echo "work_id=$work_id"
echo "screenshot_session_id=$screenshot_session_id"
echo "screenshot_turn_id=$screenshot_turn_id"
echo "screenshot_work_id=$screenshot_work_id"
