#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BROWSER_BASE_URL:-http://localhost:18089}"
HTTP_ADDR="${TMA_VERIFY_BROWSER_HTTP_ADDR:-:18089}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_BROWSER_SERVER_LOG:-.verify-browser-server.log}"
WAIT_SECONDS="${TMA_VERIFY_BROWSER_WAIT_SECONDS:-90}"
BROWSER_IMAGE="${TMA_BROWSER_SANDBOX_IMAGE:-tma-browser-sandbox:playwright}"

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

if [ "${TMA_VERIFY_BROWSER_SKIP_IMAGE_CHECK:-false}" != "true" ]; then
  if ! docker image inspect "$BROWSER_IMAGE" >/dev/null 2>&1; then
    echo "missing browser sandbox image: $BROWSER_IMAGE" >&2
    echo "run: TMA_BROWSER_SANDBOX_IMAGE=\"$BROWSER_IMAGE\" make build-browser-sandbox" >&2
    exit 1
  fi
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

browser_data_url() {
  python3 - <<'PY'
from urllib.parse import quote

html = """<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>TMA browser verification</title>
  </head>
  <body>
    <main>
      <h1>tma-browser-fixture</h1>
      <label for="verify-input">Value</label>
      <input id="verify-input" name="verify-input" />
      <button id="verify-button" type="button">Verify</button>
      <p id="status">waiting</p>
    </main>
    <script>
      document.getElementById("verify-button").addEventListener("click", () => {
        const value = document.getElementById("verify-input").value || "empty";
        document.getElementById("status").textContent = "tma-browser-flow-ok " + value;
      });
    </script>
  </body>
</html>"""
print("data:text/html;charset=utf-8," + quote(html))
PY
}

validate_events() {
  EXPECTED_MARKER="tma-browser-flow-ok" EXPECTED_TYPED="tma-browser-typed-ok" python3 -c '
import json
import os
import sys

expected = os.environ["EXPECTED_MARKER"]
typed = os.environ["EXPECTED_TYPED"]
data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]
for event_type in ["runtime.tool_call", "runtime.tool_result", "agent.message", "session.status_idle"]:
    if event_type not in types:
        raise SystemExit(1)

calls = [
    event.get("payload", {}).get("data", {})
    for event in events
    if event.get("type") == "runtime.tool_call"
]
seen = {
    call.get("api_name")
    for call in calls
    if call.get("identifier") == "browser"
}
for api in ["open", "screenshot", "type", "click"]:
    if api not in seen:
        print("missing browser tool call " + api, file=sys.stderr)
        raise SystemExit(2)

results = [
    event.get("payload", {}).get("data", {})
    for event in events
    if event.get("type") == "runtime.tool_result"
    and event.get("payload", {}).get("data", {}).get("identifier") == "browser"
]
if not any(expected in result.get("content", "") for result in results):
    print("browser results missing marker", file=sys.stderr)
    raise SystemExit(3)
if not any(typed in result.get("content", "") for result in results):
    print("browser results missing typed value", file=sys.stderr)
    raise SystemExit(8)

screenshot_results = [result for result in results if result.get("api_name") == "screenshot"]
if not screenshot_results:
    print("missing screenshot result", file=sys.stderr)
    raise SystemExit(4)
if any(result.get("artifact_error") for result in screenshot_results):
    print("screenshot artifact error: " + repr(screenshot_results), file=sys.stderr)
    raise SystemExit(5)
if not any(result.get("artifacts") for result in screenshot_results):
    print("screenshot result missing artifact ref", file=sys.stderr)
    raise SystemExit(6)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any(expected in text for text in texts):
    print("agent message missing marker", file=sys.stderr)
    print(repr(texts), file=sys.stderr)
    raise SystemExit(7)
if not any(typed in text for text in texts):
    print("agent message missing typed value", file=sys.stderr)
    print(repr(texts), file=sys.stderr)
    raise SystemExit(9)

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

echo "Starting TMA server for browser tools verification"
echo "base_url=$BASE_URL"
echo "browser_image=$BROWSER_IMAGE"
echo "server_log=$SERVER_LOG"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_TOOL_RUNTIME=cloud_sandbox \
TMA_CLOUD_SANDBOX_ALLOW_NETWORK=false \
TMA_BROWSER_SANDBOX_IMAGE="$BROWSER_IMAGE" \
"$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    cat "$SERVER_LOG" >&2 || true
    exit 1
  fi
  if "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1; then
  echo "server did not become healthy within ${WAIT_SECONDS}s" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"
target_url="$(browser_data_url)"

echo "Creating browser verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-browser-agent-$suffix" \
  --model "fake-demo" \
  --system "Browser tools verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Configuring browser tools"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"tools":["browser"],"runtime":"cloud_sandbox"}' >/dev/null

echo "Creating browser verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-browser-env-$suffix" \
  --config '{"type":"verification","browser":{"mode":"headless_sandbox"}}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating browser verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Browser tools verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me \
  --cloud-sandbox-allow-network=false >/dev/null

echo "Sending browser verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_browser_flow $target_url" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events 2>/dev/null)"; then
    echo "Browser tools verification passed"
    echo "session_id=$session_id"
    echo "turn_id=$turn_id"
    exit 0
  fi
  sleep 1
done

echo "browser tools verification timed out after ${WAIT_SECONDS}s" >&2
echo "Last events:" >&2
printf '%s\n' "$last_events" >&2
echo "server log:" >&2
cat "$SERVER_LOG" >&2 || true
exit 1
