#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_BASE_URL:-http://localhost:18085}"
HTTP_ADDR="${TMA_VERIFY_HTTP_ADDR:-:18085}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_SERVER_LOG:-.verify-objectstore-s3-server.log}"
WAIT_SECONDS="${TMA_VERIFY_SERVER_WAIT_SECONDS:-30}"

OBJECT_STORAGE_ENDPOINT="${TMA_OBJECT_STORAGE_ENDPOINT:-http://localhost:9000}"
OBJECT_STORAGE_REGION="${TMA_OBJECT_STORAGE_REGION:-local}"
OBJECT_STORAGE_BUCKET="${TMA_OBJECT_STORAGE_BUCKET:-tma-artifacts}"
OBJECT_STORAGE_ACCESS_KEY="${TMA_OBJECT_STORAGE_ACCESS_KEY:-tma}"
OBJECT_STORAGE_SECRET_KEY="${TMA_OBJECT_STORAGE_SECRET_KEY:-tma-secret}"
OBJECT_STORAGE_USE_PATH_STYLE="${TMA_OBJECT_STORAGE_USE_PATH_STYLE:-true}"

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

server_pid=""
upload_file=""
download_file=""
upload_response_file=""
cleanup() {
  if [ -n "$server_pid" ]; then
    if kill -0 "$server_pid" 2>/dev/null; then
      kill "$server_pid" 2>/dev/null || true
      wait "$server_pid" 2>/dev/null || true
    fi
  fi
  for path in "$upload_file" "$download_file" "$upload_response_file"; do
    if [ -n "$path" ] && [ -f "$path" ]; then
      rm -f "$path" || true
    fi
  done
}
trap cleanup EXIT INT TERM

echo "Starting TMA server for S3 objectstore verification"
echo "base_url=$BASE_URL"
echo "object_storage_provider=s3"
echo "object_storage_endpoint=$OBJECT_STORAGE_ENDPOINT"
echo "object_storage_region=$OBJECT_STORAGE_REGION"
echo "object_storage_bucket=$OBJECT_STORAGE_BUCKET"
echo "object_storage_use_path_style=$OBJECT_STORAGE_USE_PATH_STYLE"
echo "server_log=$LOG_FILE"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_OBJECT_STORAGE_PROVIDER=s3 \
TMA_OBJECT_STORAGE_ENDPOINT="$OBJECT_STORAGE_ENDPOINT" \
TMA_OBJECT_STORAGE_REGION="$OBJECT_STORAGE_REGION" \
TMA_OBJECT_STORAGE_BUCKET="$OBJECT_STORAGE_BUCKET" \
TMA_OBJECT_STORAGE_ACCESS_KEY="$OBJECT_STORAGE_ACCESS_KEY" \
TMA_OBJECT_STORAGE_SECRET_KEY="$OBJECT_STORAGE_SECRET_KEY" \
TMA_OBJECT_STORAGE_USE_PATH_STYLE="$OBJECT_STORAGE_USE_PATH_STYLE" \
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
  --name "verify-objectstore-s3-agent-$suffix" \
  --model "fake-demo" \
  --system "S3 objectstore verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Creating verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-objectstore-s3-env-$suffix" \
  --config '{"type":"verification","objectstore":"s3"}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "S3 objectstore verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

upload_file="$(mktemp "${TMPDIR:-/tmp}/tma-objectstore-s3-upload.XXXXXX")"
download_file="$(mktemp "${TMPDIR:-/tmp}/tma-objectstore-s3-download.XXXXXX")"
printf 'tma-objectstore-s3-ok %s\n' "$suffix" >"$upload_file"

echo "Uploading session artifact through TMA proxy"
upload_response_file="$(mktemp "${TMPDIR:-/tmp}/tma-objectstore-s3-response.XXXXXX")"
upload_status="$(curl -sS -w "%{http_code}" -o "$upload_response_file" \
  -F "file=@${upload_file};filename=input.txt;type=text/plain" \
  -F "artifact_type=file" \
  -F "name=input.txt" \
  -F "description=S3 objectstore verification" \
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
artifact_id="$(printf '%s' "$upload_json" | json_field artifact.id)"
object_ref_id="$(printf '%s' "$upload_json" | json_field object_ref.id)"
echo "artifact_id=$artifact_id"
echo "object_ref_id=$object_ref_id"

echo "Downloading session artifact through TMA proxy"
"$CLI" --base-url "$BASE_URL" session artifact download \
  --session "$session_id" \
  --artifact "$artifact_id" \
  --output "$download_file"

if ! cmp -s "$upload_file" "$download_file"; then
  echo "downloaded artifact does not match uploaded file" >&2
  echo "uploaded:" >&2
  cat "$upload_file" >&2 || true
  echo "downloaded:" >&2
  cat "$download_file" >&2 || true
  cat "$LOG_FILE" >&2 || true
  exit 1
fi

echo "Cleaning verification metadata"
"$CLI" --base-url "$BASE_URL" session artifact delete \
  --session "$session_id" \
  --artifact "$artifact_id" >/dev/null
"$CLI" --base-url "$BASE_URL" object delete \
  --id "$object_ref_id" >/dev/null

echo "S3 objectstore verification passed"
