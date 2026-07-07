#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_LLM_BASE_URL:-http://localhost:18081}"
HTTP_ADDR="${TMA_VERIFY_LLM_HTTP_ADDR:-:18081}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
LOG_FILE="${TMA_VERIFY_LLM_SERVER_LOG:-.verify-llm-provider-server.log}"
WAIT_SECONDS="${TMA_VERIFY_LLM_SERVER_WAIT_SECONDS:-30}"

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

echo "Starting TMA server for real LLM provider verification"
echo "base_url=$BASE_URL"
echo "server_log=$LOG_FILE"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
"$SERVER_BIN" >"$LOG_FILE" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    echo "server log:" >&2
    cat "$LOG_FILE" >&2 || true
    exit 1
  fi

  if TMA_BASE_URL="$BASE_URL" "$CLI" health >/dev/null 2>&1; then
    echo "Server is healthy"
    TMA_BASE_URL="$BASE_URL" TMA_CLI="$CLI" scripts/verify_llm_provider.sh
    exit 0
  fi

  sleep 1
done

echo "server did not become healthy within ${WAIT_SECONDS}s" >&2
echo "server log:" >&2
cat "$LOG_FILE" >&2 || true
exit 1
