#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18087}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18087}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
WORKER_BIN="${TMA_WORKER_BIN:-bin/tma-worker}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_SERVER_LOG:-.verify-worker-export-server.log}"
WORKER_LOG="${TMA_VERIFY_WORKER_LOG:-.verify-worker-export-worker.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-25}"
WORKER_TOKEN="${TMA_VERIFY_WORKER_TOKEN:-verify-worker-token}"
VERIFY_TEXT="${TMA_VERIFY_WORKER_EXPORT_TEXT:-tma.verify_worker_export}"
VERIFY_MARKER="${TMA_VERIFY_WORKER_EXPORT_MARKER:-tma-worker-export-ok}"

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
    if part.isdigit():
        value = value[int(part)]
    else:
        value = value[part]
print(value)
' "$1"
}

validate_worker_online() {
  python3 -c '
import json
import sys

target_name = sys.argv[1]
workers = json.load(sys.stdin).get("workers", [])
for worker in workers:
    if worker.get("name") == target_name and worker.get("status") == "online":
        print(worker.get("id", ""))
        raise SystemExit(0)
raise SystemExit(1)
' "$1"
}

validate_events() {
  python3 -c '
import json
import sys

marker = sys.argv[1]
data = json.load(sys.stdin)
events = data.get("events", [])
tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
if not tool_results:
    raise SystemExit(1)
payload = tool_results[-1].get("payload", {}).get("data", {})
if marker not in payload.get("content", ""):
    raise SystemExit(2)
artifacts = payload.get("artifacts") or []
file_artifacts = [artifact for artifact in artifacts if artifact.get("artifact_type") == "file"]
if not file_artifacts:
    raise SystemExit(3)
print(file_artifacts[0].get("artifact_id", ""))
' "$1"
}

server_pid=""
worker_pid=""
download_file=""
cleanup() {
  if [ -n "$download_file" ] && [ -f "$download_file" ]; then
    rm -f "$download_file" || true
  fi
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

echo "Starting TMA server for worker-backed local export verification"
suffix="$(date +%Y%m%d%H%M%S)"
worker_name="verify-local-worker-$suffix"

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
  --name "$worker_name" \
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
  if worker_id="$(printf '%s' "$workers_json" | validate_worker_online "$worker_name" 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$worker_id" ]; then
  echo "worker did not become online within ${WAIT_SECONDS}s" >&2
  cat "$WORKER_LOG" >&2 || true
  exit 1
fi

echo "Creating verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-worker-export-agent-$suffix" \
  --model "fake-demo" \
  --system "Worker-backed local export verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Configuring agent tools for local_system"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"tools":["default"],"runtime":"local_system"}' >/dev/null

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-worker-export-env-$suffix" \
  --config '{"type":"verification","runtime":"local_system"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Worker-backed local export verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending worker export verification message"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "$VERIFY_TEXT" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
artifact_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if artifact_id="$(printf '%s' "$last_events" | validate_events "$VERIFY_MARKER" 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$artifact_id" ]; then
  echo "worker-backed local export verification timed out after ${WAIT_SECONDS}s" >&2
  printf '%s\n' "$last_events" >&2
  echo "Worker log:" >&2
  cat "$WORKER_LOG" >&2 || true
  echo "Server log:" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

download_file="$(mktemp "${TMPDIR:-/tmp}/tma-worker-export.XXXXXX")"
"$CLI" --base-url "$BASE_URL" session artifact download \
  --session "$session_id" \
  --artifact "$artifact_id" \
  --output "$download_file"

if ! grep -q "$VERIFY_MARKER" "$download_file"; then
  echo "downloaded worker export artifact is missing marker" >&2
  cat "$download_file" >&2 || true
  exit 1
fi

echo "Worker-backed local export verification passed"
echo "session_id=$session_id"
echo "worker_id=$worker_id"
echo "artifact_id=$artifact_id"
