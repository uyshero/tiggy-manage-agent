#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_WORK_CANCEL_BASE_URL:-http://localhost:18093}"
HTTP_ADDR="${TMA_VERIFY_WORK_CANCEL_HTTP_ADDR:-:18093}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
WORKER_BIN="${TMA_WORKER_BIN:-bin/tma-worker}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_WORK_CANCEL_SERVER_LOG:-.verify-worker-work-cancel-server.log}"
WORKER_LOG="${TMA_VERIFY_WORK_CANCEL_WORKER_LOG:-.verify-worker-work-cancel-worker.log}"
WAIT_SECONDS="${TMA_VERIFY_WORK_CANCEL_WAIT_SECONDS:-30}"
WORKER_TOKEN="${TMA_VERIFY_WORKER_TOKEN:-verify-worker-token}"
CONTROL_TOKEN="${TMA_VERIFY_WORKER_CONTROL_TOKEN:-verify-worker-control-token}"
WORKER_LEASE_SECONDS="${TMA_VERIFY_WORK_CANCEL_LEASE_SECONDS:-20}"
WORK_HEARTBEAT_INTERVAL="${TMA_VERIFY_WORK_CANCEL_HEARTBEAT_INTERVAL:-1s}"
WORK_COMMAND_SECONDS="${TMA_VERIFY_WORK_CANCEL_COMMAND_SECONDS:-20}"
CANCEL_REASON="${TMA_VERIFY_WORK_CANCEL_REASON:-verify user canceled work}"

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
    value = value[part]
print(value)
' "$1"
}

validate_worker_online() {
  python3 -c '
import json
import sys

expected_name = sys.argv[1]
workers = json.load(sys.stdin).get("workers", [])
for worker in workers:
    if worker.get("name") == expected_name and worker.get("status") == "online":
        print(worker.get("id", ""))
        raise SystemExit(0)
raise SystemExit(1)
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
    print(f"unexpected work id: {work.get('id')!r}", file=sys.stderr)
    raise SystemExit(1)
if work.get("status") != "canceled":
    print(f"unexpected work status: {work.get('status')!r}", file=sys.stderr)
    raise SystemExit(2)
if work.get("error_message") != reason:
    print(f"unexpected cancel reason: {work.get('error_message')!r}", file=sys.stderr)
    raise SystemExit(3)
if not work.get("completed_at"):
    print("canceled work missing completed_at", file=sys.stderr)
    raise SystemExit(4)
' "$1" "$2"
}

validate_worker_cancel_log() {
  python3 -c '
import json
import sys

path = sys.argv[1]
work_id = sys.argv[2]
seen_canceled = False
seen_completed = False
try:
    lines = open(path, encoding="utf-8").read().splitlines()
except FileNotFoundError:
    print(f"worker log not found: {path}", file=sys.stderr)
    raise SystemExit(1)
for line in lines:
    try:
        entry = json.loads(line)
    except json.JSONDecodeError:
        continue
    if entry.get("msg") == "worker work canceled" and entry.get("work_id") == work_id:
        seen_canceled = True
    if entry.get("msg") == "worker work completed" and entry.get("work_id") == work_id:
        seen_completed = True
if not seen_canceled:
    print("worker did not log work cancellation", file=sys.stderr)
    raise SystemExit(2)
if seen_completed:
    print("worker logged completed after cancellation", file=sys.stderr)
    raise SystemExit(3)
' "$1" "$2"
}

server_pid=""
worker_pid=""
cleanup() {
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

echo "Starting TMA server for worker work cancel verification"
echo "base_url=$BASE_URL"
echo "server_log=$SERVER_LOG"
echo "worker_log=$WORKER_LOG"
echo "work_heartbeat_interval=$WORK_HEARTBEAT_INTERVAL"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_WORKER_AUTH_TOKEN="$WORKER_TOKEN" \
TMA_WORKER_CONTROL_AUTH_TOKEN="$CONTROL_TOKEN" \
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
worker_name="verify-work-cancel-worker-$suffix"

echo "Starting local worker"
TMA_WORKER_TOKEN="$WORKER_TOKEN" \
"$WORKER_BIN" \
  --base-url "$BASE_URL" \
  --name "$worker_name" \
  --workspace wksp_default \
  --poll-interval 250ms \
  --heartbeat-interval 1s \
  --work-heartbeat-interval "$WORK_HEARTBEAT_INTERVAL" \
  --lease-seconds "$WORKER_LEASE_SECONDS" >"$WORKER_LOG" 2>&1 &
worker_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
worker_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$worker_pid" 2>/dev/null; then
    echo "worker exited before becoming online" >&2
    cat "$WORKER_LOG" >&2 || true
    exit 1
  fi
  workers_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" worker list --workspace wksp_default --status online --json)"
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

echo "Enqueueing long-running work"
payload=$(printf '{"command":"sh","args":["-c","sleep %s; printf should-not-complete"]}' "$WORK_COMMAND_SECONDS")
work_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work enqueue \
  --workspace wksp_default \
  --worker "$worker_id" \
  --type sandbox_command \
  --payload "$payload")"
work_id="$(printf '%s' "$work_json" | json_field id)"

echo "Waiting for work to become running"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  fetched_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work get --work "$work_id")"
  status="$(printf '%s' "$fetched_json" | json_field status)"
  if [ "$status" = "running" ]; then
    break
  fi
  if [ "$status" = "failed" ] || [ "$status" = "completed" ] || [ "$status" = "canceled" ]; then
    echo "work reached unexpected status before cancel: $status" >&2
    printf '%s\n' "$fetched_json" >&2
    cat "$WORKER_LOG" >&2 || true
    exit 1
  fi
  sleep 1
done

fetched_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work get --work "$work_id")"
if [ "$(printf '%s' "$fetched_json" | json_field status)" != "running" ]; then
  echo "work did not become running within ${WAIT_SECONDS}s" >&2
  printf '%s\n' "$fetched_json" >&2
  cat "$WORKER_LOG" >&2 || true
  exit 1
fi

echo "Canceling running work"
cancel_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work cancel --work "$work_id" --reason "$CANCEL_REASON")"
printf '%s' "$cancel_json" | validate_canceled_work "$work_id" "$CANCEL_REASON"

echo "Waiting for worker to observe cancellation"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if validate_worker_cancel_log "$WORKER_LOG" "$work_id" 2>/dev/null; then
    break
  fi
  sleep 1
done
validate_worker_cancel_log "$WORKER_LOG" "$work_id"

fetched_json="$("$CLI" --base-url "$BASE_URL" --auth-token "$CONTROL_TOKEN" work get --work "$work_id")"
printf '%s' "$fetched_json" | validate_canceled_work "$work_id" "$CANCEL_REASON"

echo "Worker work cancel verification passed"
echo "worker_id=$worker_id"
echo "work_id=$work_id"
