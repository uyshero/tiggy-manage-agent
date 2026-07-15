#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18191}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18191}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-mcp-stdio-server.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-20}"
MCP_FIXTURE="${TMA_VERIFY_MCP_FIXTURE:-scripts/mcp_stdio_fixture.py}"
MCP_MARKER="${TMA_MCP_FIXTURE_MARKER:-tma-mcp-filesystem-ok}"
MCP_START_FILE="${TMA_VERIFY_MCP_START_FILE:-.verify-mcp-stdio-host-starts}"

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

if [ ! -f "$MCP_FIXTURE" ]; then
  echo "missing MCP fixture script: $MCP_FIXTURE"
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

validate_agent_config() {
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
versions = data.get("config_versions") or []
if not versions:
    raise SystemExit(1)
config = versions[-1]
mcp = config.get("mcp") or {}
servers = mcp.get("servers") or []
if len(servers) != 1:
    raise SystemExit(2)
server = servers[0]
if server.get("identifier") != "filesystem":
    raise SystemExit(3)
if server.get("command") != "python3":
    raise SystemExit(4)
env = server.get("env") or {}
marker = env.get("TMA_MCP_FIXTURE_MARKER") or {}
if marker.get("env_ref") != "TMA_MCP_FIXTURE_MARKER":
    raise SystemExit(5)
start_file = env.get("TMA_MCP_FIXTURE_START_FILE") or {}
if start_file.get("env_ref") != "TMA_MCP_FIXTURE_START_FILE":
    raise SystemExit(7)
tools = config.get("tools") or {}
enabled = tools.get("enabled_tools") or tools.get("tools") or []
if enabled != ["filesystem"]:
    raise SystemExit(6)
'
}

validate_events() {
  python3 -c '
import json
import sys

marker = sys.argv[1]
data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]
for event_type in ["runtime.tool_call", "runtime.tool_result", "agent.message", "session.status_idle"]:
    if event_type not in types:
        raise SystemExit(1)

tool_calls = [event for event in events if event.get("type") == "runtime.tool_call"]
if not any((event.get("payload") or {}).get("data", {}).get("identifier") == "filesystem" and (event.get("payload") or {}).get("data", {}).get("api_name") == "read_file" for event in tool_calls):
    raise SystemExit(2)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
result_data = (tool_results[-1].get("payload") or {}).get("data") or {}
content = result_data.get("content", "")
if marker not in content:
    raise SystemExit(3)
state = result_data.get("state") or {}
if state.get("tool_name") != "readFile":
    raise SystemExit(4)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload") or {}
texts = [item.get("text", "") for item in agent_payload.get("content", []) if item.get("type") == "text"]
if not any(marker in text for text in texts):
    raise SystemExit(5)

print(agent_payload.get("turn_id", ""))
' "$1"
}

server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then
    if kill -0 "$server_pid" 2>/dev/null; then
      kill "$server_pid" 2>/dev/null || true
      wait "$server_pid" 2>/dev/null || true
    fi
  fi
  rm -f "$MCP_START_FILE"
}
trap cleanup EXIT INT TERM

rm -f "$MCP_START_FILE"

echo "Starting TMA server for MCP stdio verification"
echo "base_url=$BASE_URL"
echo "server_log=$LOG_FILE"
echo "mcp_fixture=$MCP_FIXTURE"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_ENV=development \
TMA_AUTH_MODE=disabled \
TMA_AUTH_OIDC_WEB_LOGIN_ENABLED=false \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_MCP_FIXTURE_MARKER="$MCP_MARKER" \
TMA_MCP_FIXTURE_START_FILE="$MCP_START_FILE" \
"$SERVER_BIN" >"$LOG_FILE" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
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
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-mcp-agent-$suffix" \
  --model "fake-demo" \
  --system "MCP stdio verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

mcp_json="$(python3 - "$MCP_FIXTURE" <<'PY'
import json
import sys

fixture = sys.argv[1]
print(json.dumps({
    "mcpServers": {
        "filesystem": {
            "command": "python3",
            "args": [fixture],
			"stdio_framing": "content_length",
            "env": {
                "TMA_MCP_FIXTURE_MARKER": {"env_ref": "TMA_MCP_FIXTURE_MARKER"},
                "TMA_MCP_FIXTURE_START_FILE": {"env_ref": "TMA_MCP_FIXTURE_START_FILE"}
            }
        }
    }
}, separators=(",", ":")))
PY
)"

echo "Binding MCP stdio server to agent"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"enabled_tools":["filesystem"],"runtime":"auto"}' \
  --mcp "$mcp_json" >/dev/null

config_json="$("$CLI" --base-url "$BASE_URL" agent config list --agent "$agent_id")"
printf '%s' "$config_json" | validate_agent_config

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-mcp-env-$suffix" \
  --config '{"type":"verification","transport":"mcp-stdio"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "MCP stdio verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending MCP verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_mcp_tool" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events "$MCP_MARKER" 2>/dev/null)"; then
    if [ ! -f "$MCP_START_FILE" ]; then
      echo "MCP stdio host start marker was not created" >&2
      exit 1
    fi
    start_count="$(wc -l < "$MCP_START_FILE" | tr -d '[:space:]')"
    if [ "$start_count" != "1" ]; then
      echo "expected one long-lived MCP stdio process, got $start_count starts" >&2
      cat "$MCP_START_FILE" >&2 || true
      exit 1
    fi
    echo "MCP stdio verification passed"
    echo "session_id=$session_id"
    echo "turn_id=$turn_id"
    exit 0
  fi
  sleep 1
done

echo "MCP stdio verification timed out after ${WAIT_SECONDS}s" >&2
echo "Last events:" >&2
printf '%s\n' "$last_events" >&2
echo "Server log:" >&2
cat "$LOG_FILE" >&2 || true
exit 1
