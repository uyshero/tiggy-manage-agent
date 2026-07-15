#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18192}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18192}"
FIXTURE_PORT="${TMA_VERIFY_MCP_HTTP_PORT:-18443}"
FIXTURE_URL="https://localhost:${FIXTURE_PORT}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-mcp-http-server.log}"
FIXTURE_LOG="${TMA_VERIFY_MCP_HTTP_LOG:-.verify-mcp-http-fixture.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
MARKER="${TMA_MCP_HTTP_FIXTURE_MARKER:-tma-mcp-filesystem-ok}"
CLIENT_ID="${TMA_MCP_HTTP_CLIENT_ID:-tma-mcp-http-client}"
CLIENT_SECRET="${TMA_MCP_HTTP_CLIENT_SECRET:-tma-mcp-http-secret}"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/tma-mcp-http.XXXXXX")"
cert_file="$tmp_dir/fixture.crt"
key_file="$tmp_dir/fixture.key"
server_pid=""
fixture_pid=""

cleanup() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ -n "$fixture_pid" ] && kill -0 "$fixture_pid" 2>/dev/null; then
    kill "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

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

validate_health() {
  python3 -c '
import json
import sys
data = json.load(sys.stdin)
items = data.get("mcp") or []
if len(items) != 1:
    raise SystemExit("expected one MCP health item")
item = items[0]
if item.get("identifier") != "filesystem" or item.get("status") != "online":
    raise SystemExit(f"unexpected MCP health item: {item}")
if item.get("transport") != "streamable_http" or item.get("tool_count") != 1:
    raise SystemExit(f"unexpected transport/tool count: {item}")
if item.get("resource_count") != 1 or item.get("prompt_count") != 1:
    raise SystemExit(f"unexpected context catalog counts: {item}")
expected = ["logging", "prompts", "resources", "tools"]
if item.get("capabilities") != expected:
    raise SystemExit("unexpected capabilities: %r" % item.get("capabilities"))
host = data.get("mcp_http_host") or {}
if not host.get("egress_policy_enabled") or host.get("egress_allow_http"):
    raise SystemExit(f"unexpected egress policy: {host}")
if host.get("egress_allowed_host_count") != 1 or host.get("egress_allowed_cidr_count") != 2:
    raise SystemExit(f"unexpected egress allowlist summary: {host}")
'
}

validate_events() {
  python3 -c '
import json
import sys
marker = sys.argv[1]
data = json.load(sys.stdin)
events = data.get("events") or []
types = [item.get("type") for item in events]
for expected in ["runtime.tool_call", "runtime.tool_result", "agent.message", "session.status_idle"]:
    if expected not in types:
        raise SystemExit(1)
results = [item for item in events if item.get("type") == "runtime.tool_result"]
content = (((results[-1].get("payload") or {}).get("data") or {}).get("content") or "")
if marker not in content:
    raise SystemExit(2)
messages = [item for item in events if item.get("type") == "agent.message"]
payload = messages[-1].get("payload") or {}
if not any(marker in item.get("text", "") for item in payload.get("content", []) if item.get("type") == "text"):
    raise SystemExit(3)
print(payload.get("turn_id", ""))
' "$1"
}

validate_fixture_state() {
  python3 -c '
import json
import sys
state = json.load(sys.stdin)
minimums = {
    "token_requests": 1,
    "initializes": 2,
    "session_headers": 1,
    "protocol_headers": 1,
    "post_sse_responses": 1,
    "listener_connections": 2,
    "listener_reconnects": 1,
    "tools_calls": 1,
    "resources_lists": 1,
    "prompts_lists": 1,
    "logging_set_level": 1,
    "deletes": 1,
}
missing = {key: (state.get(key, 0), value) for key, value in minimums.items() if state.get(key, 0) < value}
if missing:
    raise SystemExit(f"fixture counters below minimum: {missing}; state={state}")
print(json.dumps(state, sort_keys=True))
'
}

for dependency in "$SERVER_BIN" "$CLI" scripts/mcp_http_fixture.py; do
  if [ ! -e "$dependency" ]; then
    echo "missing dependency: $dependency" >&2
    exit 1
  fi
done

openssl req -x509 -newkey rsa:2048 -sha256 -days 1 -nodes \
  -keyout "$key_file" -out "$cert_file" -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1

python3 scripts/mcp_http_fixture.py \
  --port "$FIXTURE_PORT" --cert "$cert_file" --key "$key_file" \
  --marker "$MARKER" --client-id "$CLIENT_ID" --client-secret "$CLIENT_SECRET" \
  >"$FIXTURE_LOG" 2>&1 &
fixture_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if curl --silent --show-error --fail --cacert "$cert_file" "$FIXTURE_URL/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --silent --show-error --fail --cacert "$cert_file" "$FIXTURE_URL/health" >/dev/null

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_ENV=development \
TMA_AUTH_MODE=disabled \
TMA_AUTH_OIDC_WEB_LOGIN_ENABLED=false \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_MAX_TOOL_ROUNDS=4 \
TMA_MCP_HTTP_CLIENT_ID="$CLIENT_ID" \
TMA_MCP_HTTP_CLIENT_SECRET="$CLIENT_SECRET" \
TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS=localhost \
TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS=127.0.0.0/8,::1/128 \
TMA_MCP_HTTP_CA_BUNDLE="$cert_file" \
"$SERVER_BIN" >"$LOG_FILE" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$LOG_FILE" >&2 || true
    exit 1
  fi
  sleep 1
done
TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null

suffix="$(date +%Y%m%d%H%M%S)"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create --name "verify-mcp-http-$suffix" --model fake-demo --system "MCP HTTPS verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

mcp_json="$(python3 - "$FIXTURE_URL" <<'PY'
import json
import sys
base = sys.argv[1]
print(json.dumps({"mcpServers": {"filesystem": {
    "transport": "streamable_http",
    "url": base + "/mcp",
    "listen": True,
    "logging": {"level": "info"},
    "roots": [{"path": "/workspace", "name": "Workspace"}],
    "oauth": {
        "grant_type": "client_credentials",
        "token_url": base + "/oauth/token",
        "client_id": {"env_ref": "TMA_MCP_HTTP_CLIENT_ID"},
        "client_secret": {"secret_ref": "env:TMA_MCP_HTTP_CLIENT_SECRET"},
        "token_endpoint_auth_method": "client_secret_post"
    }
}}}, separators=(",", ":")))
PY
)"

"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" --tools '{"enabled_tools":["filesystem"],"runtime":"auto"}' --mcp "$mcp_json" >/dev/null

health_json="$(curl --silent --show-error --fail -X POST -H 'Content-Type: application/json' -d '{"kind":"mcp","identifier":"filesystem"}' "$BASE_URL/v1/agents/$agent_id/tooling-health")"
printf '%s' "$health_json" | validate_health

env_json="$("$CLI" --base-url "$BASE_URL" env create --name "verify-mcp-http-$suffix" --config '{"type":"verification","transport":"mcp-streamable-http"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"
session_json="$("$CLI" --base-url "$BASE_URL" session create --agent "$agent_id" --env "$env_id" --title "MCP HTTP verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"
"$CLI" --base-url "$BASE_URL" session runtime update --session "$session_id" --intervention-mode approve_for_me >/dev/null
"$CLI" --base-url "$BASE_URL" event send --session "$session_id" --text "tma.verify_mcp_tool" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events "$MARKER" 2>/dev/null)"; then
    break
  fi
  sleep 1
done
if [ -z "$turn_id" ]; then
  printf '%s\n' "$last_events" >&2
  curl --silent --show-error --cacert "$cert_file" "$FIXTURE_URL/state" >&2 || true
  printf '\n' >&2
  curl --silent --show-error -X POST -H 'Content-Type: application/json' -d '{"kind":"mcp","identifier":"filesystem"}' "$BASE_URL/v1/agents/$agent_id/tooling-health" >&2 || true
  printf '\n' >&2
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

sleep 1
kill "$server_pid"
wait "$server_pid"
server_pid=""

fixture_state="$(curl --silent --show-error --fail --cacert "$cert_file" "$FIXTURE_URL/state")"
validated_state="$(printf '%s' "$fixture_state" | validate_fixture_state)"

echo "MCP Streamable HTTP verification passed"
echo "session_id=$session_id"
echo "turn_id=$turn_id"
echo "fixture_state=$validated_state"
