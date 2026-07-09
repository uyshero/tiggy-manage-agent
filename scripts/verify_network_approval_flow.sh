#!/bin/sh
set -eu

if [ -z "${TMA_VERIFY_NETWORK_BASE_URL:-}" ] && [ -z "${TMA_VERIFY_NETWORK_HTTP_ADDR:-}" ]; then
  VERIFY_PORT="$(python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
  BASE_URL="http://localhost:$VERIFY_PORT"
  HTTP_ADDR=":$VERIFY_PORT"
else
  BASE_URL="${TMA_VERIFY_NETWORK_BASE_URL:-http://localhost:18086}"
  HTTP_ADDR="${TMA_VERIFY_NETWORK_HTTP_ADDR:-:18086}"
fi
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_NETWORK_SERVER_LOG:-.verify-network-approval-server.log}"
WAIT_SECONDS="${TMA_VERIFY_NETWORK_WAIT_SECONDS:-45}"
SANDBOX_ROOT="${TMA_CLOUD_SANDBOX_ROOT:-.}"
SANDBOX_IMAGE="${TMA_CLOUD_SANDBOX_IMAGE:-coolfan1024/onlyboxes-runtime:default}"
DOWNLOAD_URL="${TMA_VERIFY_NETWORK_DOWNLOAD_URL:-https://example.com/}"

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

pending_field() {
  python3 -c '
import json
import sys

items = json.load(sys.stdin).get("interventions", [])
if not items:
    raise SystemExit(2)
print(items[0].get(sys.argv[1], ""))
' "$1"
}

validate_pending_network_access() {
  python3 -c '
import json
import sys

items = json.load(sys.stdin).get("interventions", [])
if not items:
    print("no pending interventions", file=sys.stderr)
    raise SystemExit(1)
item = items[0]
checks = {
    "tool_identifier": "default",
    "api_name": "execute_code",
    "intervention_mode": "request_approval",
    "reason": "network_access",
    "status": "pending",
}
for key, expected in checks.items():
    if item.get(key) != expected:
        print(f"unexpected pending {key}: {item.get(key)!r}, expected {expected!r}", file=sys.stderr)
        raise SystemExit(2)
'
}

validate_events() {
  expected_intervention="$1"
  expected_marker="$2"
  EXPECTED_INTERVENTION="$expected_intervention" EXPECTED_MARKER="$expected_marker" python3 -c '
import json
import os
import sys

expected_intervention = os.environ["EXPECTED_INTERVENTION"]
expected_marker = os.environ["EXPECTED_MARKER"]
data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]
for event_type in ["runtime.tool_call", "runtime.tool_result", "agent.message", "session.status_idle"]:
    if event_type not in types:
        raise SystemExit(1)

tool_calls = [event for event in events if event.get("type") == "runtime.tool_call"]
if not any(
    event.get("payload", {}).get("data", {}).get("id") == "call_verify_network_download"
    and event.get("payload", {}).get("data", {}).get("identifier") == "default"
    and event.get("payload", {}).get("data", {}).get("api_name") == "execute_code"
    for event in tool_calls
):
    print("missing default.execute_code network download tool call", file=sys.stderr)
    raise SystemExit(2)

tool_results = [
    event for event in events
    if event.get("type") == "runtime.tool_result"
    and event.get("payload", {}).get("data", {}).get("id") == "call_verify_network_download"
]
if not tool_results:
    print("missing network download tool result", file=sys.stderr)
    raise SystemExit(3)
content = tool_results[-1].get("payload", {}).get("data", {}).get("content", "")
has_marker = "tma-network-download-ok" in content
if expected_marker == "yes" and not has_marker:
    print("tool result missing network download marker", file=sys.stderr)
    print(content, file=sys.stderr)
    raise SystemExit(4)
if expected_marker == "no" and has_marker:
    print("network-disabled case unexpectedly downloaded successfully", file=sys.stderr)
    print(content, file=sys.stderr)
    raise SystemExit(5)

network_events = [
    event for event in events
    if event.get("type") in ("runtime.tool_intervention_required", "runtime.tool_intervention_approved")
    and event.get("payload", {}).get("data", {}).get("reason") == "network_access"
]
required = [event for event in network_events if event.get("type") == "runtime.tool_intervention_required"]
approved = [event for event in network_events if event.get("type") == "runtime.tool_intervention_approved"]
if expected_intervention == "required" and not required:
    print("missing network_access required event", file=sys.stderr)
    raise SystemExit(6)
if expected_intervention == "approved" and not approved:
    print("missing network_access auto-approved event", file=sys.stderr)
    raise SystemExit(7)
if expected_intervention == "none" and network_events:
    print("unexpected network_access intervention event", file=sys.stderr)
    print(json.dumps(network_events, ensure_ascii=False), file=sys.stderr)
    raise SystemExit(8)

agent_events = [event for event in events if event.get("type") == "agent.message"]
print(agent_events[-1].get("payload", {}).get("turn_id", ""))
'
}

wait_for_health() {
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      echo "server exited before becoming healthy" >&2
      cat "$SERVER_LOG" >&2 || true
      exit 1
    fi
    if "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1 && kill -0 "$server_pid" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "server did not become healthy within ${WAIT_SECONDS}s" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
}

wait_for_pending() {
  session_id="$1"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    pending_json="$("$CLI" --base-url "$BASE_URL" session intervention list --session "$session_id" --status pending)"
    if printf '%s' "$pending_json" | validate_pending_network_access >/dev/null 2>&1; then
      printf '%s\n' "$pending_json"
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_valid_events() {
  session_id="$1"
  expected_intervention="$2"
  expected_marker="$3"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  last_events=""
  while [ "$(date +%s)" -le "$deadline" ]; do
    last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
    if turn_id="$(printf '%s' "$last_events" | validate_events "$expected_intervention" "$expected_marker" 2>/dev/null)"; then
      printf '%s\n' "$turn_id"
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for valid events on session $session_id" >&2
  echo "last events:" >&2
  printf '%s\n' "$last_events" >&2
  return 1
}

approve_intervention() {
  session_id="$1"
  turn_id="$2"
  call_id="$3"
  python3 - "$BASE_URL" "$session_id" "$turn_id" "$call_id" <<'PY'
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

base_url, session_id, turn_id, call_id = sys.argv[1:5]
path = "/v1/sessions/{}/interventions/{}/{}/approve".format(
    urllib.parse.quote(session_id, safe=""),
    urllib.parse.quote(turn_id, safe=""),
    urllib.parse.quote(call_id, safe=""),
)
body = json.dumps({"reason": "network approval verification"}).encode("utf-8")
request = urllib.request.Request(
    base_url.rstrip("/") + path,
    data=body,
    method="POST",
    headers={"Content-Type": "application/json"},
)
try:
    with urllib.request.urlopen(request, timeout=180) as response:
        response.read()
except urllib.error.HTTPError as error:
    print(error.read().decode("utf-8", errors="replace"), file=sys.stderr)
    raise
PY
}

create_session() {
  title="$1"
  session_json="$("$CLI" --base-url "$BASE_URL" session create \
    --agent "$agent_id" \
    --env "$env_id" \
    --title "$title")"
  printf '%s' "$session_json" | json_field id
}

configure_session_runtime() {
  session_id="$1"
  intervention_mode="$2"
  allow_network="$3"
  "$CLI" --base-url "$BASE_URL" session runtime update \
    --session "$session_id" \
    --intervention-mode "$intervention_mode" \
    --tool-runtime cloud_sandbox \
    --cloud-sandbox-root "$SANDBOX_ROOT" \
    --cloud-sandbox-image "$SANDBOX_IMAGE" \
    --cloud-sandbox-allow-network="$allow_network" >/dev/null
}

run_download_turn() {
  session_id="$1"
  "$CLI" --base-url "$BASE_URL" event send \
    --session "$session_id" \
    --text "tma.verify_network_download $DOWNLOAD_URL" >/dev/null
}

run_request_approval_case() {
  echo "Case: request_approval + network enabled"
  session_id="$(create_session "Network approval required verification $suffix")"
  configure_session_runtime "$session_id" request_approval true
  run_download_turn "$session_id"

  if ! pending_json="$(wait_for_pending "$session_id")"; then
    echo "timed out waiting for network_access pending intervention" >&2
    "$CLI" --base-url "$BASE_URL" event list --session "$session_id" >&2 || true
    exit 2
  fi
  printf '%s' "$pending_json" | validate_pending_network_access
  turn_id="$(printf '%s' "$pending_json" | pending_field turn_id)"
  call_id="$(printf '%s' "$pending_json" | pending_field call_id)"
  approve_intervention "$session_id" "$turn_id" "$call_id"

  completed_turn_id="$(wait_for_valid_events "$session_id" required yes)"
  echo "  passed session_id=$session_id turn_id=$completed_turn_id"
}

run_auto_approval_case() {
  echo "Case: approve_for_me + network enabled"
  session_id="$(create_session "Network auto approval verification $suffix")"
  configure_session_runtime "$session_id" approve_for_me true
  run_download_turn "$session_id"
  completed_turn_id="$(wait_for_valid_events "$session_id" approved yes)"
  echo "  passed session_id=$session_id turn_id=$completed_turn_id"
}

run_full_access_case() {
  echo "Case: full_access + network enabled"
  session_id="$(create_session "Network full access verification $suffix")"
  configure_session_runtime "$session_id" full_access true
  run_download_turn "$session_id"
  completed_turn_id="$(wait_for_valid_events "$session_id" none yes)"
  echo "  passed session_id=$session_id turn_id=$completed_turn_id"
}

run_network_disabled_case() {
  echo "Case: full_access + network disabled"
  session_id="$(create_session "Network disabled verification $suffix")"
  configure_session_runtime "$session_id" full_access false
  run_download_turn "$session_id"
  completed_turn_id="$(wait_for_valid_events "$session_id" none no)"
  echo "  passed session_id=$session_id turn_id=$completed_turn_id"
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

echo "Starting TMA server for network approval verification"
echo "base_url=$BASE_URL"
echo "sandbox_root=$SANDBOX_ROOT"
echo "sandbox_image=$SANDBOX_IMAGE"
echo "download_url=$DOWNLOAD_URL"
echo "server_log=$SERVER_LOG"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_TOOL_RUNTIME=cloud_sandbox \
TMA_CLOUD_SANDBOX_ROOT="$SANDBOX_ROOT" \
TMA_CLOUD_SANDBOX_IMAGE="$SANDBOX_IMAGE" \
TMA_CLOUD_SANDBOX_ALLOW_NETWORK=true \
"$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
server_pid="$!"

wait_for_health

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating network approval verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-network-approval-agent-$suffix" \
  --model "fake-demo" \
  --system "Network approval verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating network approval verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-network-approval-env-$suffix" \
  --config '{"type":"verification","network":"approval"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

run_request_approval_case
run_auto_approval_case
run_full_access_case
run_network_disabled_case

echo "Network approval verification passed"
