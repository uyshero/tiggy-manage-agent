#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_REAP_BASE_URL:-http://localhost:18090}"
HTTP_ADDR="${TMA_VERIFY_REAP_HTTP_ADDR:-:18090}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_REAP_SERVER_LOG:-.verify-worker-work-reap-server.log}"
WAIT_SECONDS="${TMA_VERIFY_REAP_WAIT_SECONDS:-25}"
WORKER_TOKEN="${TMA_VERIFY_WORKER_TOKEN:-verify-worker-token}"
CONTROL_TOKEN="${TMA_VERIFY_WORKER_CONTROL_TOKEN:-verify-worker-control-token}"
WORKER_REAPER_INTERVAL_MS="${TMA_VERIFY_WORKER_REAP_INTERVAL_MS:-1000}"
REAPER_INTERVAL_MS="${TMA_VERIFY_REAP_INTERVAL_MS:-1000}"

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

validate_polled_work() {
  python3 -c '
import json
import sys

expected = sys.argv[1]
data = json.load(sys.stdin)
work = data.get("work")
if not work:
    print("poll returned no work", file=sys.stderr)
    raise SystemExit(1)
if work.get("id") != expected:
    print(f"poll returned unexpected work id: {work.get('id')!r}", file=sys.stderr)
    raise SystemExit(2)
if work.get("status") != "leased":
    print(f"poll returned unexpected status: {work.get('status')!r}", file=sys.stderr)
    raise SystemExit(3)
' "$1"
}

validate_running_work() {
  python3 -c '
import json
import sys

expected = sys.argv[1]
work = json.load(sys.stdin)
if work.get("id") != expected:
    print(f"ack returned unexpected work id: {work.get('id')!r}", file=sys.stderr)
    raise SystemExit(1)
if work.get("status") != "running":
    print(f"ack returned unexpected status: {work.get('status')!r}", file=sys.stderr)
    raise SystemExit(2)
' "$1"
}

validate_failed_work() {
  python3 -c '
import json
import sys

expected = sys.argv[1]
work = json.load(sys.stdin)
if work.get("id") != expected:
    print(f"get returned unexpected work id: {work.get('id')!r}", file=sys.stderr)
    raise SystemExit(1)
if work.get("status") != "failed":
    print(f"get returned unexpected status: {work.get('status')!r}", file=sys.stderr)
    raise SystemExit(2)
if "worker work lease expired" not in (work.get("error_message") or ""):
    print(f"get returned unexpected error: {work.get('error_message')!r}", file=sys.stderr)
    raise SystemExit(3)
if not work.get("completed_at"):
    print("failed work missing completed_at", file=sys.stderr)
    raise SystemExit(4)
' "$1"
}

validate_canceled_work() {
  python3 -c '
import json
import sys

expected = sys.argv[1]
reason = sys.argv[2]
work = json.load(sys.stdin)
if work.get("id") != expected:
    print(f"cancel returned unexpected work id: {work.get('id')!r}", file=sys.stderr)
    raise SystemExit(1)
if work.get("status") != "canceled":
    print(f"cancel returned unexpected status: {work.get('status')!r}", file=sys.stderr)
    raise SystemExit(2)
if work.get("error_message") != reason:
    print(f"cancel returned unexpected reason: {work.get('error_message')!r}", file=sys.stderr)
    raise SystemExit(3)
if not work.get("completed_at"):
    print("canceled work missing completed_at", file=sys.stderr)
    raise SystemExit(4)
' "$1" "$2"
}

validate_offline_worker() {
  python3 -c '
import json
import sys

expected = sys.argv[1]
worker = json.load(sys.stdin)
if worker.get("id") != expected:
    print(f"get returned unexpected worker id: {worker.get('id')!r}", file=sys.stderr)
    raise SystemExit(1)
if worker.get("status") != "offline":
    print(f"get returned unexpected worker status: {worker.get('status')!r}", file=sys.stderr)
    raise SystemExit(2)
if not worker.get("lease_expires_at"):
    print("offline worker missing lease_expires_at", file=sys.stderr)
    raise SystemExit(3)
' "$1"
}

validate_running_diagnosis() {
  output="$1"
  printf '%s' "$output" | grep -q "work diagnose " || {
    echo "diagnose output missing header: $output" >&2
    return 1
  }
  printf '%s' "$output" | grep -q "status: running" || {
    echo "diagnose output missing running status: $output" >&2
    return 1
  }
  printf '%s' "$output" | grep -q "worker should heartbeat while running" || {
    echo "diagnose output missing running action: $output" >&2
    return 1
  }
}

validate_failed_diagnosis() {
  output="$1"
  printf '%s' "$output" | grep -q "work diagnose " || {
    echo "diagnose output missing header: $output" >&2
    return 1
  }
  printf '%s' "$output" | grep -q "status: failed" || {
    echo "diagnose output missing failed status: $output" >&2
    return 1
  }
  printf '%s' "$output" | grep -q "work failed" || {
    echo "diagnose output missing failed reason: $output" >&2
    return 1
  }
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

echo "Starting TMA server for worker work reap-expired verification"
echo "base_url=$BASE_URL"
echo "server_log=$SERVER_LOG"
echo "worker_reaper_interval_ms=$WORKER_REAPER_INTERVAL_MS"
echo "reaper_interval_ms=$REAPER_INTERVAL_MS"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_WORKER_AUTH_TOKEN="$WORKER_TOKEN" \
TMA_WORKER_CONTROL_AUTH_TOKEN="$CONTROL_TOKEN" \
TMA_WORKER_REAPER_ENABLED=true \
TMA_WORKER_REAPER_INTERVAL_MS="$WORKER_REAPER_INTERVAL_MS" \
TMA_WORKER_REAPER_LIMIT=20 \
TMA_WORKER_WORK_REAPER_ENABLED=true \
TMA_WORKER_WORK_REAPER_INTERVAL_MS="$REAPER_INTERVAL_MS" \
TMA_WORKER_WORK_REAPER_LIMIT=20 \
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

suffix="$(date +%Y%m%d%H%M%S)"
worker_name="verify-reap-worker-$suffix"

echo "Registering worker"
worker_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$WORKER_TOKEN" worker register \
  --name "$worker_name" \
  --workspace wksp_default \
  --type local \
  --lease-seconds 30)"
worker_id="$(printf '%s' "$worker_json" | json_field id)"

echo "Enqueueing work"
work_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work enqueue \
  --workspace wksp_default \
  --worker "$worker_id" \
  --type sandbox_command \
  --payload '{"command":"sh","args":["-c","sleep 100"]}')"
work_id="$(printf '%s' "$work_json" | json_field id)"

echo "Polling work with a short lease"
poll_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$WORKER_TOKEN" work poll \
  --worker "$worker_id" \
  --lease-seconds 3)"
printf '%s' "$poll_json" | validate_polled_work "$work_id"

echo "Acknowledging work as running"
ack_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$WORKER_TOKEN" work ack \
  --worker "$worker_id" \
  --work "$work_id")"
printf '%s' "$ack_json" | validate_running_work "$work_id"

echo "Diagnosing running work"
diagnose_running="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work diagnose --work "$work_id")"
validate_running_diagnosis "$diagnose_running"

echo "Waiting for lease to expire"
sleep 4

echo "Waiting for automatic reaper to fail the expired work"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
fetched_json=""
while [ "$(date +%s)" -le "$deadline" ]; do
  fetched_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work get --work "$work_id")"
  if printf '%s' "$fetched_json" | validate_failed_work "$work_id" 2>/dev/null; then
    break
  fi
  sleep 1
done

if ! printf '%s' "$fetched_json" | validate_failed_work "$work_id" 2>/dev/null; then
  echo "automatic worker work reaper did not fail work within ${WAIT_SECONDS}s" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

echo "Diagnosing failed work"
diagnose_failed="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work diagnose --work "$work_id")"
validate_failed_diagnosis "$diagnose_failed"

echo "Checking manual work cancel control command"
cancel_work_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work enqueue \
  --workspace wksp_default \
  --worker "$worker_id" \
  --type sandbox_command \
  --payload '{"command":"sh","args":["-c","printf should-not-run"]}')"
cancel_work_id="$(printf '%s' "$cancel_work_json" | json_field id)"
cancel_reason="verify manual cancel"
canceled_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work cancel \
  --work "$cancel_work_id" \
  --reason "$cancel_reason")"
printf '%s' "$canceled_json" | validate_canceled_work "$cancel_work_id" "$cancel_reason"

echo "Registering short-lived worker for automatic worker reaper"
expired_worker_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$WORKER_TOKEN" worker register \
  --name "verify-expired-worker-$suffix" \
  --workspace wksp_default \
  --type local \
  --lease-seconds 1)"
expired_worker_id="$(printf '%s' "$expired_worker_json" | json_field id)"

echo "Waiting for automatic worker reaper to mark worker offline"
sleep 2
deadline=$(( $(date +%s) + WAIT_SECONDS ))
expired_worker_get_json=""
while [ "$(date +%s)" -le "$deadline" ]; do
  expired_worker_get_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" worker get --id "$expired_worker_id")"
  if printf '%s' "$expired_worker_get_json" | validate_offline_worker "$expired_worker_id" 2>/dev/null; then
    break
  fi
  sleep 1
done

if ! printf '%s' "$expired_worker_get_json" | validate_offline_worker "$expired_worker_id" 2>/dev/null; then
  echo "automatic worker reaper did not mark worker offline within ${WAIT_SECONDS}s" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

echo "Checking manual worker reap-expired control command"
"$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" worker reap-expired --limit 20 >/dev/null

echo "Worker work reap-expired verification passed"
echo "worker_id=$worker_id"
echo "work_id=$work_id"
echo "canceled_work_id=$cancel_work_id"
echo "expired_worker_id=$expired_worker_id"
