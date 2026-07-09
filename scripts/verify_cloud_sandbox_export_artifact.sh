#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18086}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18086}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-onlyboxes-export-artifact-server.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-30}"
SANDBOX_ROOT="${TMA_CLOUD_SANDBOX_ROOT:-.}"
SANDBOX_IMAGE="${TMA_CLOUD_SANDBOX_IMAGE:-coolfan1024/onlyboxes-runtime:default}"
SANDBOX_DATA_ROOT="${TMA_CLOUD_SANDBOX_DATA_ROOT:-/private/tmp/tma-cloud-sandbox-data}"
OBJECT_STORAGE_PROVIDER="${TMA_VERIFY_OBJECT_STORAGE_PROVIDER:-localfs}"
OBJECT_STORAGE_ROOT_DIR="${TMA_VERIFY_OBJECT_STORAGE_ROOT_DIR:-/private/tmp/tma-export-artifact-object-store}"
OBJECT_STORAGE_BUCKET="${TMA_VERIFY_OBJECT_STORAGE_BUCKET:-tma-artifacts}"

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
    if part.isdigit():
        value = value[int(part)]
    else:
        value = value[part]
print(value)
' "$1"
}

validate_export_events() {
  python3 -c '
import json
import sys

data = json.load(sys.stdin)
events = data.get("events", [])
tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
if not tool_results:
    sys.exit(2)
payload = tool_results[-1].get("payload", {}).get("data", {})
content = payload.get("content", "")
if "tma-session-output-exported" not in content:
    print("tool result missing export marker: " + repr(content), file=sys.stderr)
    sys.exit(3)
artifacts = payload.get("artifacts", [])
file_artifacts = [artifact for artifact in artifacts if artifact.get("artifact_type") == "file"]
if not file_artifacts:
    print("tool result missing file artifact: " + repr(artifacts), file=sys.stderr)
    sys.exit(4)
print(file_artifacts[0].get("artifact_id", ""))
'
}

server_pid=""
upload_file=""
upload_response_file=""
download_file=""
cleanup() {
  if [ -n "$server_pid" ]; then
    if kill -0 "$server_pid" 2>/dev/null; then
      kill "$server_pid" 2>/dev/null || true
      wait "$server_pid" 2>/dev/null || true
    fi
  fi
  for path in "$upload_file" "$upload_response_file" "$download_file"; do
    if [ -n "$path" ] && [ -f "$path" ]; then
      rm -f "$path" || true
    fi
  done
}
trap cleanup EXIT INT TERM

echo "Starting TMA server for cloud_sandbox export artifact verification"
echo "base_url=$BASE_URL"
echo "sandbox_root=$SANDBOX_ROOT"
echo "sandbox_image=$SANDBOX_IMAGE"
echo "sandbox_data_root=$SANDBOX_DATA_ROOT"
echo "object_storage_provider=$OBJECT_STORAGE_PROVIDER"
echo "object_storage_root_dir=$OBJECT_STORAGE_ROOT_DIR"
echo "object_storage_bucket=$OBJECT_STORAGE_BUCKET"
echo "server_log=$LOG_FILE"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_CLOUD_SANDBOX_ROOT="$SANDBOX_ROOT" \
TMA_CLOUD_SANDBOX_IMAGE="$SANDBOX_IMAGE" \
TMA_CLOUD_SANDBOX_DATA_ROOT="$SANDBOX_DATA_ROOT" \
TMA_OBJECT_STORAGE_PROVIDER="$OBJECT_STORAGE_PROVIDER" \
TMA_OBJECT_STORAGE_ROOT_DIR="$OBJECT_STORAGE_ROOT_DIR" \
TMA_OBJECT_STORAGE_BUCKET="$OBJECT_STORAGE_BUCKET" \
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
  --name "verify-export-artifact-agent-$suffix" \
  --model "fake-demo" \
  --system "Cloud sandbox export artifact verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-export-artifact-env-$suffix" \
  --config '{"type":"verification","sandbox":"cloud_sandbox"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Cloud sandbox export artifact verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session intervention mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

upload_file="$(mktemp "${TMPDIR:-/tmp}/tma-export-artifact.XXXXXX")"
download_file="$(mktemp "${TMPDIR:-/tmp}/tma-export-artifact-down.XXXXXX")"
printf 'tma-upload-sync-ok\n' >"$upload_file"

echo "Uploading session artifact"
upload_response_file="$(mktemp "${TMPDIR:-/tmp}/tma-export-artifact-response.XXXXXX")"
upload_status="$(curl -sS -w "%{http_code}" -o "$upload_response_file" \
  -F "file=@${upload_file};filename=input.txt;type=text/plain" \
  -F "artifact_type=file" \
  -F "name=input.txt" \
  -F "description=cloud_sandbox export artifact verification" \
  "$BASE_URL/v1/sessions/$session_id/artifacts/upload")"
upload_json="$(cat "$upload_response_file")"
rm -f "$upload_response_file"
upload_response_file=""
if [ "$upload_status" != "201" ]; then
  echo "upload failed with HTTP $upload_status" >&2
  printf '%s\n' "$upload_json" >&2
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

echo "Sending export message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_uploaded_file_export" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
export_artifact_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if export_artifact_id="$(printf '%s' "$last_events" | validate_export_events 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$export_artifact_id" ]; then
  echo "export verification timed out after ${WAIT_SECONDS}s" >&2
  printf '%s\n' "$last_events" >&2
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

echo "Downloading exported artifact $export_artifact_id"
"$CLI" --base-url "$BASE_URL" session artifact download \
  --session "$session_id" \
  --artifact "$export_artifact_id" \
  --output "$download_file"

if ! grep -q "tma-upload-sync-ok" "$download_file"; then
  echo "downloaded exported artifact is missing uploaded file marker" >&2
  cat "$download_file" >&2 || true
  cat "$LOG_FILE" >&2 || true
  exit 1
fi
if ! grep -q "tma-session-output-exported" "$download_file"; then
  echo "downloaded exported artifact is missing export marker" >&2
  cat "$download_file" >&2 || true
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

echo "cloud_sandbox export artifact verification passed"
