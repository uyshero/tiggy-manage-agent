#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18193}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18193}"
POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_PASSWORD="${TMA_POSTGRES_TEST_PASSWORD:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
VERIFY_DATABASE="tma_verify_mcp_registry_$(date +%Y%m%d%H%M%S)_$$"
RUNTIME_ROLE="tma_mcp_registry_runtime_$$"
RUNTIME_PASSWORD="tma-mcp-registry-runtime-password"
DATABASE_URL="postgres://$RUNTIME_ROLE:$RUNTIME_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$VERIFY_DATABASE?sslmode=disable"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-mcp-registry-server.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
MCP_FIXTURE="${TMA_VERIFY_MCP_FIXTURE:-scripts/mcp_stdio_fixture.py}"
MCP_MARKER="${TMA_MCP_FIXTURE_MARKER:-tma-mcp-registry-ok}"
AUTH_SECRET="${TMA_VERIFY_JWT_SECRET:-tma-mcp-registry-verification-secret}"
ALPHA_WORKSPACE="wksp_registry_alpha"
BETA_WORKSPACE="wksp_registry_beta"
OPERATOR_LABEL="verify-mcp-registry"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/tma-mcp-registry.XXXXXX")"
start_file="$tmp_dir/mcp-host-starts"
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
  python3 - "$AUTH_SECRET" "$1" "$2" <<'PY'
import base64
import hashlib
import hmac
import json
import sys
import time

secret, workspace_id, subject = sys.argv[1:]

def encode(value):
    raw = json.dumps(value, separators=(",", ":"), sort_keys=True).encode()
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()

header = encode({"alg": "HS256", "typ": "JWT"})
claims = encode({
    "sub": subject,
    "exp": int(time.time()) + 900,
    "workspace_id": workspace_id,
    "owner_id": subject,
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
  token="$2"
  path="$3"
  if [ "$#" -ge 4 ]; then
    status="$(curl --silent --show-error -o "$response_file" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $token" \
      -H "X-TMA-Operator: $OPERATOR_LABEL" \
      -H 'Content-Type: application/json' \
      --data "$4" "$BASE_URL$path")"
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

api_status() {
  method="$1"
  token="$2"
  path="$3"
  if [ "$#" -ge 4 ]; then
    curl --silent --show-error -o "$response_file" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $token" \
      -H "X-TMA-Operator: $OPERATOR_LABEL" \
      -H 'Content-Type: application/json' \
      --data "$4" "$BASE_URL$path"
  else
    curl --silent --show-error -o "$response_file" -w '%{http_code}' -X "$method" \
      -H "Authorization: Bearer $token" \
      -H "X-TMA-Operator: $OPERATOR_LABEL" \
      "$BASE_URL$path"
  fi
}

validate_created_server() {
  python3 - "$ALPHA_WORKSPACE" 3<&0 <<'PY'
import json
import os
import sys
data = json.load(os.fdopen(3))
if data.get("workspace_id") != sys.argv[1]:
    raise SystemExit(f"workspace spoofing was not rejected: {data}")
if data.get("current_version") != 1 or data.get("status") != "active":
    raise SystemExit(f"unexpected created server: {data}")
if not (data.get("identifier") or "").startswith("registry_filesystem_"):
    raise SystemExit(f"unexpected canonical identifier: {data}")
PY
}

validate_pinned_config() {
  python3 - "$1" 3<&0 <<'PY'
import json
import os
import sys
server_id = sys.argv[1]
data = json.load(os.fdopen(3))
versions = data.get("config_versions") or []
if not versions:
    raise SystemExit("agent has no config versions")
current = versions[-1]
bindings = (current.get("mcp") or {}).get("bindings") or []
if len(bindings) != 1:
    raise SystemExit(f"unexpected bindings: {bindings}")
binding = bindings[0]
if binding.get("server_id") != server_id or binding.get("version") != 1 or binding.get("identifier") != "filesystem":
    raise SystemExit(f"binding was not pinned to v1: {binding}")
if "command" in binding or "config" in binding:
    raise SystemExit(f"binding copied central config: {binding}")
print(current.get("version"))
PY
}

validate_v2_server() {
  python3 - 3<&0 <<'PY'
import json
import os
import sys
data = json.load(os.fdopen(3))
if data.get("current_version") != 2 or data.get("usage_count") != 1:
    raise SystemExit(f"unexpected v2 server state: {data}")
args = (data.get("config") or {}).get("args") or []
if not args or args[-1] != "v2":
    raise SystemExit(f"v2 config was not published: {data}")
PY
}

validate_events() {
  python3 - "$MCP_MARKER" 3<&0 <<'PY'
import json
import os
import sys
marker = sys.argv[1]
data = json.load(os.fdopen(3))
events = data.get("events") or []
types = [event.get("type") for event in events]
for expected in ["runtime.tool_call", "runtime.tool_result"]:
    if expected not in types:
        raise SystemExit(1)
calls = [event for event in events if event.get("type") == "runtime.tool_call"]
if not any(((event.get("payload") or {}).get("data") or {}).get("identifier") == "filesystem" for event in calls):
    raise SystemExit(2)
results = [event for event in events if event.get("type") == "runtime.tool_result"]
content = (((results[-1].get("payload") or {}).get("data") or {}).get("content") or "")
if marker not in content:
    raise SystemExit(3)
print(((results[-1].get("payload") or {}).get("turn_id") or ""))
PY
}

validate_restore() {
  python3 - 3<&0 <<'PY'
import json
import os
import sys
data = json.load(os.fdopen(3))
if (data.get("source_version"), data.get("previous_version"), data.get("new_version")) != (1, 2, 3):
    raise SystemExit(f"unexpected restore result: {data}")
if (data.get("server") or {}).get("current_version") != 3:
    raise SystemExit(f"restore did not select v3: {data}")
PY
}

validate_versions() {
  python3 - 3<&0 <<'PY'
import json
import os
import sys
items = json.load(os.fdopen(3)).get("versions") or []
if [item.get("version") for item in items] != [3, 2, 1]:
    raise SystemExit(f"unexpected version order: {items}")
if items[0].get("checksum_sha256") != items[2].get("checksum_sha256"):
    raise SystemExit(f"restored checksum differs from v1: {items}")
if items[1].get("checksum_sha256") == items[2].get("checksum_sha256"):
    raise SystemExit(f"v2 checksum should differ from v1: {items}")
if "v2" in ((items[0].get("config") or {}).get("args") or []):
    raise SystemExit(f"v3 did not copy v1 config: {items[0]}")
PY
}

validate_agent_unchanged() {
  python3 - "$1" "$2" 3<&0 <<'PY'
import json
import os
import sys
expected_config_version = int(sys.argv[1])
server_id = sys.argv[2]
data = json.load(os.fdopen(3))
if data.get("current_config_version") != expected_config_version:
    raise SystemExit(f"registry restore changed Agent config version: {data}")
bindings = ((data.get("config_version") or {}).get("mcp") or {}).get("bindings") or []
if len(bindings) != 1 or bindings[0].get("server_id") != server_id or bindings[0].get("version") != 1:
    raise SystemExit(f"registry restore changed pinned binding: {bindings}")
PY
}

validate_disabled_health() {
  python3 - 3<&0 <<'PY'
import json
import os
import sys
data = json.load(os.fdopen(3))
items = data.get("mcp") or []
if len(items) != 1 or items[0].get("status") != "configuration_error":
    raise SystemExit(f"disabled Registry server did not trigger kill switch: {data}")
if "disabled" not in (items[0].get("detail") or "").lower():
    raise SystemExit(f"disabled health error is unclear: {data}")
PY
}

validate_registry_health() {
  python3 - 3<&0 <<'PY'
import json
import os
import sys
data = json.load(os.fdopen(3))
result = data.get("result") or {}
if data.get("version") != 3 or result.get("status") != "online" or result.get("tool_count") != 1:
    raise SystemExit(f"restored Registry server is not healthy: {data}")
PY
}

validate_restore_audit() {
  python3 - "$1" "$ALPHA_WORKSPACE" "$OPERATOR_LABEL" 3<&0 <<'PY'
import json
import os
import sys
server_id, workspace_id, operator_label = sys.argv[1:]
records = json.load(os.fdopen(3)).get("audit_records") or []
if len(records) != 1:
    raise SystemExit(f"expected one restore audit record: {records}")
record = records[0]
if record.get("resource_id") != server_id or record.get("workspace_id") != workspace_id or record.get("outcome") != "succeeded":
    raise SystemExit(f"unexpected restore audit record: {record}")
if record.get("operator_label") != operator_label or record.get("role") != "admin":
    raise SystemExit(f"unexpected restore operator: {record}")
details = record.get("details") or {}
if (details.get("source_version"), details.get("previous_version"), details.get("new_version")) != (1, 2, 3):
    raise SystemExit(f"unexpected restore audit details: {record}")
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
  -c "INSERT INTO workspaces (id, org_id, name) VALUES ('$ALPHA_WORKSPACE', 'org_default', 'MCP Registry Alpha'), ('$BETA_WORKSPACE', 'org_default', 'MCP Registry Beta') ON CONFLICT (id) DO NOTHING;" \
  >/dev/null
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
  -c "CREATE ROLE $RUNTIME_ROLE LOGIN PASSWORD '$RUNTIME_PASSWORD' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS; GRANT CONNECT ON DATABASE $VERIFY_DATABASE TO $RUNTIME_ROLE;" \
  >/dev/null
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$VERIFY_DATABASE" \
  -c "GRANT USAGE ON SCHEMA public TO $RUNTIME_ROLE; GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $RUNTIME_ROLE; GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO $RUNTIME_ROLE;" \
  >/dev/null

alpha_token="$(make_jwt "$ALPHA_WORKSPACE" registry-alpha-admin)"
beta_token="$(make_jwt "$BETA_WORKSPACE" registry-beta-admin)"

echo "Starting authenticated TMA server for MCP Registry verification"
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
TMA_MCP_FIXTURE_START_FILE="$start_file" \
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
registry_identifier="registry-filesystem-$suffix"
create_payload="$(python3 - "$registry_identifier" "$MCP_FIXTURE" "$BETA_WORKSPACE" <<'PY'
import json
import sys
identifier, fixture, spoofed_workspace = sys.argv[1:]
print(json.dumps({
    "workspace_id": spoofed_workspace,
    "identifier": identifier,
    "name": "MCP Registry Verification",
    "description": "Isolated end-to-end Registry verification.",
    "config": {
        "transport": "stdio",
        "command": "python3",
        "args": [fixture],
		"stdio_framing": "content_length",
        "env": {
            "TMA_MCP_FIXTURE_MARKER": {"env_ref": "TMA_MCP_FIXTURE_MARKER"},
            "TMA_MCP_FIXTURE_START_FILE": {"env_ref": "TMA_MCP_FIXTURE_START_FILE"},
        },
    },
}, separators=(",", ":")))
PY
)"

echo "Creating Registry v1 and verifying workspace scope"
created_json="$(api_json POST "$alpha_token" /v1/mcp-servers "$create_payload")"
printf '%s' "$created_json" | validate_created_server
server_id="$(printf '%s' "$created_json" | json_field id)"

beta_status="$(api_status GET "$beta_token" "/v1/mcp-servers/$server_id")"
if [ "$beta_status" != "404" ]; then
  echo "expected cross-workspace Registry lookup to return 404, got $beta_status" >&2
  cat "$response_file" >&2 || true
  exit 1
fi
beta_list="$(api_json GET "$beta_token" "/v1/mcp-servers?workspace_id=$ALPHA_WORKSPACE")"
if ! printf '%s' "$beta_list" | python3 -c 'import json,sys; data=json.load(sys.stdin); raise SystemExit(0 if not data.get("servers") else 1)'; then
  echo "beta workspace can list alpha Registry servers" >&2
  exit 1
fi

echo "Creating Agent and pinning version: 0 to Registry v1"
agent_json="$($CLI --base-url "$BASE_URL" --auth-token "$alpha_token" agent create \
  --name "verify-mcp-registry-$suffix" --model fake-demo --system "MCP Registry verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"
mcp_binding="$(python3 - "$server_id" <<'PY'
import json
import sys
print(json.dumps({"bindings": [{"server_id": sys.argv[1], "version": 0, "identifier": "filesystem"}]}, separators=(",", ":")))
PY
)"
$CLI --base-url "$BASE_URL" --auth-token "$alpha_token" agent config update \
  --agent "$agent_id" --tools '{"enabled_tools":["filesystem"],"runtime":"auto"}' --mcp "$mcp_binding" >/dev/null
config_json="$($CLI --base-url "$BASE_URL" --auth-token "$alpha_token" agent config list --agent "$agent_id")"
agent_config_version="$(printf '%s' "$config_json" | validate_pinned_config "$server_id")"

echo "Publishing Registry v2 without changing the pinned Agent"
v2_payload="$(python3 - "$MCP_FIXTURE" <<'PY'
import json
import sys
print(json.dumps({"config": {
    "transport": "stdio",
    "command": "python3",
    "args": [sys.argv[1], "v2"],
	"stdio_framing": "content_length",
    "env": {
        "TMA_MCP_FIXTURE_MARKER": {"env_ref": "TMA_MCP_FIXTURE_MARKER"},
        "TMA_MCP_FIXTURE_START_FILE": {"env_ref": "TMA_MCP_FIXTURE_START_FILE"},
    },
}}, separators=(",", ":")))
PY
)"
v2_json="$(api_json PATCH "$alpha_token" "/v1/mcp-servers/$server_id" "$v2_payload")"
printf '%s' "$v2_json" | validate_v2_server

echo "Executing a real MCP tool call through the Agent pinned to v1"
env_json="$($CLI --base-url "$BASE_URL" --auth-token "$alpha_token" env create \
  --name "verify-mcp-registry-env-$suffix" --config '{"type":"verification","transport":"mcp-registry"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"
session_json="$($CLI --base-url "$BASE_URL" --auth-token "$alpha_token" session create \
  --agent "$agent_id" --env "$env_id" --title "MCP Registry verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"
$CLI --base-url "$BASE_URL" --auth-token "$alpha_token" session runtime update \
  --session "$session_id" --intervention-mode approve_for_me >/dev/null
$CLI --base-url "$BASE_URL" --auth-token "$alpha_token" event send \
  --session "$session_id" --text "tma.verify_mcp_tool" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$($CLI --base-url "$BASE_URL" --auth-token "$alpha_token" event list --session "$session_id" --after 0)"
  if turn_id="$(printf '%s' "$last_events" | validate_events 2>/dev/null)"; then
    break
  fi
  sleep 1
done
if [ -z "$turn_id" ]; then
  echo "Registry-backed MCP tool call timed out" >&2
  printf '%s' "$last_events" | python3 -c '
import collections, json, sys
events = json.load(sys.stdin).get("events") or []
print("event_counts=" + repr(dict(collections.Counter(item.get("type") for item in events))))
print("last_events=" + json.dumps(events[-10:], ensure_ascii=False))
' >&2 || true
  tail -120 "$LOG_FILE" >&2 || true
  exit 1
fi
if [ ! -f "$start_file" ] || [ "$(wc -l < "$start_file" | tr -d '[:space:]')" != "1" ]; then
  echo "expected one session-scoped MCP process for manifest and tool execution" >&2
  cat "$start_file" >&2 2>/dev/null || true
  exit 1
fi

echo "Restoring Registry v1 as immutable v3"
restore_json="$(api_json POST "$alpha_token" "/v1/mcp-servers/$server_id/versions/1/restore" '{}')"
printf '%s' "$restore_json" | validate_restore
versions_json="$(api_json GET "$alpha_token" "/v1/mcp-servers/$server_id/versions")"
printf '%s' "$versions_json" | validate_versions
agent_after_restore="$(api_json GET "$alpha_token" "/v1/agents/$agent_id")"
printf '%s' "$agent_after_restore" | validate_agent_unchanged "$agent_config_version" "$server_id"

archive_status="$(api_status DELETE "$alpha_token" "/v1/mcp-servers/$server_id")"
if [ "$archive_status" != "409" ]; then
  echo "expected bound Registry archive to return 409, got $archive_status" >&2
  cat "$response_file" >&2 || true
  exit 1
fi

echo "Verifying disable kill switch and restored v3 health"
api_json POST "$alpha_token" "/v1/mcp-servers/$server_id/disable" '{}' >/dev/null
disabled_health="$(api_json POST "$alpha_token" "/v1/agents/$agent_id/tooling-health" '{"kind":"mcp","identifier":"filesystem"}')"
printf '%s' "$disabled_health" | validate_disabled_health
api_json POST "$alpha_token" "/v1/mcp-servers/$server_id/enable" '{}' >/dev/null
registry_health="$(api_json POST "$alpha_token" "/v1/mcp-servers/$server_id/test" '{}')"
printf '%s' "$registry_health" | validate_registry_health

audit_json="$(api_json GET "$alpha_token" '/v1/operator-audit?action=mcp_registry.version.restore&limit=10')"
printf '%s' "$audit_json" | validate_restore_audit "$server_id"

echo "MCP Registry verification passed"
echo "database=$VERIFY_DATABASE"
echo "server_id=$server_id"
echo "agent_id=$agent_id"
echo "session_id=$session_id"
echo "turn_id=$turn_id"
