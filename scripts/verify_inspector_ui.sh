#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_INSPECTOR_BASE_URL:-http://localhost:18089}"
HTTP_ADDR="${TMA_VERIFY_INSPECTOR_HTTP_ADDR:-:18089}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
SERVER_LOG="${TMA_VERIFY_INSPECTOR_SERVER_LOG:-.verify-inspector-server.log}"
WAIT_SECONDS="${TMA_VERIFY_INSPECTOR_WAIT_SECONDS:-25}"

if [ ! -x "$SERVER_BIN" ]; then
  echo "missing server binary: $SERVER_BIN"
  echo "run: make build"
  exit 1
fi

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

wait_for_http() {
  url="$1"
  label="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if python3 - "$url" <<'PY' >/dev/null 2>&1
import sys
import urllib.request

with urllib.request.urlopen(sys.argv[1], timeout=3) as response:
    if 200 <= response.status < 500:
        raise SystemExit(0)
raise SystemExit(1)
PY
    then
      echo "$label is reachable"
      return 0
    fi
    sleep 1
  done
  echo "$label did not become reachable within ${WAIT_SECONDS}s: $url" >&2
  return 1
}

echo "Starting TMA server for Inspector UI verification"
TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
"$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
server_pid="$!"

if ! wait_for_http "$BASE_URL/health" "TMA server"; then
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

echo "Checking Inspector HTML"
python3 - "$BASE_URL/inspector" <<'PY'
import sys
import urllib.request

url = sys.argv[1]
with urllib.request.urlopen(url, timeout=10) as response:
    body = response.read().decode("utf-8")

expected = [
    "TMA Inspector",
    "Timeline",
    "Artifacts",
    "Download",
    "Copy CLI",
    "data-copy",
    "bin/tma session artifact download --session",
    "/v1/sessions/",
    "/artifacts/",
    "/download",
]
missing = [item for item in expected if item not in body]
if missing:
    print("Inspector HTML missing expected content:", ", ".join(missing), file=sys.stderr)
    raise SystemExit(1)
PY

echo "Inspector UI verification passed"
