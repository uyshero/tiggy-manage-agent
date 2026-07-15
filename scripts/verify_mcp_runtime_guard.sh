#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18194}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18194}"
POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
VERIFY_DATABASE="tma_verify_mcp_runtime_guard_$(date +%Y%m%d%H%M%S)_$$"
RUNTIME_ROLE="tma_mcp_runtime_guard_$$"
RUNTIME_PASSWORD="tma-mcp-runtime-guard-password"
DATABASE_URL="postgres://$RUNTIME_ROLE:$RUNTIME_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$VERIFY_DATABASE?sslmode=disable"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-mcp-runtime-guard-server.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
MCP_FIXTURE="${TMA_VERIFY_MCP_FIXTURE:-scripts/mcp_stdio_fixture.py}"
MCP_MARKER="${TMA_MCP_FIXTURE_MARKER:-tma-mcp-runtime-guard-ok}"
AUTH_SECRET="${TMA_VERIFY_JWT_SECRET:-tma-mcp-runtime-guard-verification-secret}"
WORKSPACE_ID="wksp_runtime_guard"
OPERATOR_LABEL="verify-mcp-runtime-guard"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/tma-mcp-runtime-guard.XXXXXX")"
mode_file="$tmp_dir/fault-mode"
call_file="$tmp_dir/tool-calls"
response_file="$tmp_dir/response.json"
server_pid=""

cleanup() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
    -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$VERIFY_DATABASE' AND pid <> pg_backend_pid();" \
    >/dev/null 2>&1 || true
  docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$VERIFY_DATABASE" >/dev/null 2>&1 || true
  docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
    -c "DROP ROLE IF EXISTS $RUNTIME_ROLE;" >/dev/null 2>&1 || true
  rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

for dependency in "$SERVER_BIN" "$CLI" "$MCP_FIXTURE"; do
  if [ ! -e "$dependency" ]; then
    echo "missing dependency: $dependency" >&2
    exit 1
  fi
done
for command in curl docker python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "missing command: $command" >&2
    exit 1
  fi
done

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

make_jwt() {
  python3 - "$AUTH_SECRET" "$WORKSPACE_ID" <<'PY'
import base64
import hashlib
import hmac
import json
import sys
import time

secret, workspace_id = sys.argv[1:]

def encode(value):
    raw = json.dumps(value, separators=(",", ":"), sort_keys=True).encode()
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()

header = encode({"alg": "HS256", "typ": "JWT"})
claims = encode({
    "sub": "runtime-guard-admin",
    "exp": int(time.time()) + 900,
    "workspace_id": workspace_id,
    "owner_id": "runtime-guard-admin",
    "roles": ["admin"],
})
payload = f"{header}.{claims}"
signature = base64.urlsafe_b64encode(
    hmac.new(secret.encode(), payload.encode(), hashlib.sha256).digest()
).rstrip(b"=").decode()
print(f"{payload}.{signature}")
PY
}

api_json() {
  method="$1"
  path="$2"
  if [ "$#" -ge 3 ]; then
    status="$(curl --silent --show-error -o "$response_file" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $token" \
      -H "X-TMA-Operator: $OPERATOR_LABEL" \
      -H 'Content-Type: application/json' \
      --data "$3" "$BASE_URL$path")"
  else
    status="$(curl --silent --show-error -o "$response_file" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $token" \
      -H "X-TMA-Operator: $OPERATOR_LABEL" \
      "$BASE_URL$path")"
  fi
  case "$status" in
    2??) cat "$response_file" ;;
    *)
      echo "$method $path returned HTTP $status" >&2
      cat "$response_file" >&2 || true
      return 1
      ;;
  esac
}

call_count() {
  if [ ! -f "$call_file" ]; then
    echo 0
    return
  fi
  wc -l < "$call_file" | tr -d '[:space:]'
}

assert_call_count() {
  expected="$1"
  actual="$(call_count)"
  if [ "$actual" != "$expected" ]; then
    echo "expected $expected fixture tools/call invocation(s), got $actual" >&2
    cat "$call_file" >&2 2>/dev/null || true
    exit 1
  fi
}

wait_for_idle_count() {
  expected="$1"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  last_events=""
  while [ "$(date +%s)" -le "$deadline" ]; do
    last_events="$($CLI --base-url "$BASE_URL" --auth-token "$token" event list --session "$session_id" --after 0)"
    if printf '%s' "$last_events" | python3 - "$expected" 3<&0 <<'PY'
import json
import os
import sys
expected = int(sys.argv[1])
events = json.load(os.fdopen(3)).get("events") or []
actual = sum(item.get("type") == "session.status_idle" for item in events)
raise SystemExit(0 if actual >= expected else 1)
PY
    then
      return 0
    fi
    sleep 1
  done
  echo "session did not reach idle event count $expected" >&2
  printf '%s\n' "$last_events" >&2
  tail -120 "$LOG_FILE" >&2 || true
  exit 1
}

assert_timeout_results() {
  expected="$1"
  $CLI --base-url "$BASE_URL" --auth-token "$token" event list --session "$session_id" --after 0 |
    python3 - "$expected" 3<&0 <<'PY'
import json
import os
import sys
expected = int(sys.argv[1])
events = json.load(os.fdopen(3)).get("events") or []
errors = []
for event in events:
    if event.get("type") != "runtime.tool_result":
        continue
    data = (event.get("payload") or {}).get("data") or {}
    error = data.get("error") or {}
    if error.get("type") == "mcp_timeout":
        errors.append(error)
if len(errors) != expected:
    raise SystemExit(f"expected {expected} mcp_timeout result(s), got {errors}")
for error in errors:
    if error.get("message") != "MCP call timed out.":
        raise SystemExit(f"runtime error was not redacted: {error}")
PY
}

assert_runtime_status() {
  expected_state="$1"
  expected_failures="$2"
  expected_class="$3"
  api_json GET /v1/mcp-servers/runtime-status |
    python3 - "$server_id" "$expected_state" "$expected_failures" "$expected_class" 3<&0 <<'PY'
import json
import os
import sys
server_id, expected_state, expected_failures, expected_class = sys.argv[1:]
data = json.load(os.fdopen(3))
states = data.get("states") or []
if len(states) != 1:
    raise SystemExit(f"expected one runtime state, got {states}")
state = states[0]
if state.get("server_id") != server_id or state.get("version") != 1:
    raise SystemExit(f"unexpected runtime partition: {state}")
if state.get("state") != expected_state:
    raise SystemExit(f"expected state {expected_state}, got {state}")
if state.get("consecutive_failures") != int(expected_failures):
    raise SystemExit(f"unexpected failure count: {state}")
if expected_class and state.get("last_failure_class") != expected_class:
    raise SystemExit(f"unexpected failure class: {state}")
if expected_state == "open" and state.get("cooldown_remaining_seconds", 0) <= 0:
    raise SystemExit(f"open circuit has no cooldown: {state}")
if any(key in state for key in ("key", "workspace_id", "identifier", "url", "headers", "arguments", "content")):
    raise SystemExit(f"runtime status exposed an internal field: {state}")
PY
}

echo "Creating isolated PostgreSQL database: $VERIFY_DATABASE"
docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$VERIFY_DATABASE"
docker compose exec -T postgres sh -c '
  set -eu
  for file in /migrations/*.sql; do
    psql -v ON_ERROR_STOP=1 --single-transaction -U "$1" -d "$2" -f "$file" >/dev/null
  done
' sh "$POSTGRES_USER" "$VERIFY_DATABASE"
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$VERIFY_DATABASE" \
  -c "INSERT INTO workspaces (id, org_id, name) VALUES ('$WORKSPACE_ID', 'org_default', 'MCP Runtime Guard') ON CONFLICT (id) DO NOTHING;" \
  >/dev/null
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
  -c "CREATE ROLE $RUNTIME_ROLE LOGIN PASSWORD '$RUNTIME_PASSWORD' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; GRANT CONNECT ON DATABASE $VERIFY_DATABASE TO $RUNTIME_ROLE;" \
  >/dev/null
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$VERIFY_DATABASE" \
  -c "GRANT USAGE ON SCHEMA public TO $RUNTIME_ROLE; GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $RUNTIME_ROLE; GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO $RUNTIME_ROLE;" \
  >/dev/null

printf '%s\n' timeout > "$mode_file"
: > "$call_file"
token="$(make_jwt)"

echo "Starting authenticated TMA server for RuntimeGuard verification"
TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_ENV=development \
TMA_AUTH_MODE=jwt \
TMA_AUTH_JWT_SECRET="$AUTH_SECRET" \
TMA_AUTH_OIDC_WEB_LOGIN_ENABLED=false \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_MAX_TOOL_ROUNDS=1 \
TMA_MCP_FIXTURE_MARKER="$MCP_MARKER" \
TMA_MCP_FAULT_MODE_FILE="$mode_file" \
TMA_MCP_FAULT_CALL_FILE="$call_file" \
TMA_MCP_FAULT_DELAY_SECONDS=5 \
"$SERVER_BIN" >"$LOG_FILE" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    cat "$LOG_FILE" >&2 || true
    exit 1
  fi
  if curl --silent --show-error --fail "$BASE_URL/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --silent --show-error --fail "$BASE_URL/health" >/dev/null

suffix="$(date +%Y%m%d%H%M%S)"
create_payload="$(python3 - "$MCP_FIXTURE" <<'PY'
import json
import sys
print(json.dumps({
    "identifier": "runtime-guard-fixture",
    "name": "MCP RuntimeGuard Verification",
    "config": {
        "transport": "stdio",
        "command": "python3",
        "args": [sys.argv[1]],
		"stdio_framing": "content_length",
        "env": {
            "TMA_MCP_FIXTURE_MARKER": {"env_ref": "TMA_MCP_FIXTURE_MARKER"},
            "TMA_MCP_FAULT_MODE_FILE": {"env_ref": "TMA_MCP_FAULT_MODE_FILE"},
            "TMA_MCP_FAULT_CALL_FILE": {"env_ref": "TMA_MCP_FAULT_CALL_FILE"},
            "TMA_MCP_FAULT_DELAY_SECONDS": {"env_ref": "TMA_MCP_FAULT_DELAY_SECONDS"},
        },
        "runtime": {
            "timeout_seconds": 1,
            "max_concurrency": 1,
            "failure_threshold": 2,
            "cooldown_seconds": 3,
        },
    },
}, separators=(",", ":")))
PY
)"

echo "Creating Registry server with a 1s timeout and two-failure circuit"
created_json="$(api_json POST /v1/mcp-servers "$create_payload")"
server_id="$(printf '%s' "$created_json" | json_field id)"

agent_json="$($CLI --base-url "$BASE_URL" --auth-token "$token" agent create \
  --name "verify-mcp-runtime-guard-$suffix" --model fake-demo --system "MCP RuntimeGuard verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"
mcp_binding="$(python3 - "$server_id" <<'PY'
import json
import sys
print(json.dumps({"bindings": [{"server_id": sys.argv[1], "version": 0, "identifier": "filesystem"}]}, separators=(",", ":")))
PY
)"
$CLI --base-url "$BASE_URL" --auth-token "$token" agent config update \
  --agent "$agent_id" --tools '{"enabled_tools":["filesystem"],"runtime":"auto"}' --mcp "$mcp_binding" >/dev/null

env_json="$($CLI --base-url "$BASE_URL" --auth-token "$token" env create \
  --name "verify-mcp-runtime-guard-env-$suffix" --config '{"type":"verification","transport":"mcp-runtime-guard"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"
session_json="$($CLI --base-url "$BASE_URL" --auth-token "$token" session create \
  --agent "$agent_id" --env "$env_id" --title "MCP RuntimeGuard verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"
$CLI --base-url "$BASE_URL" --auth-token "$token" session runtime update \
  --session "$session_id" --intervention-mode approve_for_me >/dev/null

echo "Triggering two real tools/call timeouts"
$CLI --base-url "$BASE_URL" --auth-token "$token" event send \
  --session "$session_id" --text "tma.verify_mcp_tool timeout one" >/dev/null
wait_for_idle_count 2
assert_timeout_results 1
assert_call_count 1

$CLI --base-url "$BASE_URL" --auth-token "$token" event send \
  --session "$session_id" --text "tma.verify_mcp_tool timeout two" >/dev/null
wait_for_idle_count 3
assert_timeout_results 2
assert_call_count 2
assert_runtime_status open 2 timeout

echo "Verifying the open circuit rejects work without replaying tools/call"
$CLI --base-url "$BASE_URL" --auth-token "$token" event send \
  --session "$session_id" --text "tma.verify_mcp_tool open circuit" >/dev/null
wait_for_idle_count 4
assert_call_count 2
assert_runtime_status open 2 timeout

echo "Waiting for the single half-open probe"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if assert_runtime_status half_open 2 timeout 2>/dev/null; then
    break
  fi
  sleep 1
done
assert_runtime_status half_open 2 timeout

printf '%s\n' success > "$mode_file"
$CLI --base-url "$BASE_URL" --auth-token "$token" event send \
  --session "$session_id" --text "tma.verify_mcp_tool recovery" >/dev/null
wait_for_idle_count 5
assert_call_count 3
assert_runtime_status closed 0 timeout

$CLI --base-url "$BASE_URL" --auth-token "$token" event list --session "$session_id" --after 0 |
  python3 - "$MCP_MARKER" 3<&0 <<'PY'
import json
import os
import sys
marker = sys.argv[1]
events = json.load(os.fdopen(3)).get("events") or []
results = [
    (event.get("payload") or {}).get("data") or {}
    for event in events if event.get("type") == "runtime.tool_result"
]
if not any(item.get("success") is True and marker in (item.get("content") or "") for item in results):
    raise SystemExit(f"recovery tool result did not contain marker: {results}")
PY

metrics="$(curl --silent --show-error --fail -H "Authorization: Bearer $token" "$BASE_URL/metrics")"
printf '%s' "$metrics" | python3 3<&0 <<'PY'
import os
import re
import sys
text = os.fdopen(3).read()

def value(pattern):
    match = re.search(pattern, text, re.MULTILINE)
    if not match:
        raise SystemExit(f"missing metric: {pattern}")
    return float(match.group(1))

if value(r'^tma_mcp_runtime_guard_open_circuits ([0-9.]+)$') != 0:
    raise SystemExit("circuit did not close after recovery")
if value(r'^tma_mcp_runtime_guard_calls_total ([0-9.]+)$') < 6:
    raise SystemExit("too few admitted RuntimeGuard calls")
if value(r'^tma_mcp_runtime_guard_failures_total\{class="timeout"\} ([0-9.]+)$') != 2:
    raise SystemExit("expected exactly two timeout failures")
if value(r'^tma_mcp_runtime_guard_rejections_total\{reason="circuit_open"\} ([0-9.]+)$') < 1:
    raise SystemExit("open circuit did not reject a call")
PY

python3 - "$call_file" <<'PY'
import sys
with open(sys.argv[1], encoding="utf-8") as source:
    modes = [line.strip() for line in source if line.strip()]
if modes != ["timeout", "timeout", "success"]:
    raise SystemExit(f"unexpected fixture call sequence: {modes}")
PY

echo "MCP RuntimeGuard verification passed"
echo "database=$VERIFY_DATABASE"
echo "server_id=$server_id"
echo "agent_id=$agent_id"
echo "session_id=$session_id"
echo "fixture_calls=$(call_count)"
