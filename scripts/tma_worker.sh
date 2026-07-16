#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

WORKER_BIN="${TMA_WORKER_BIN:-$ROOT_DIR/bin/tma-worker}"
PID_FILE="${TMA_WORKER_PID_FILE:-$ROOT_DIR/.tma-worker.pid}"
LOG_FILE="${TMA_WORKER_LOG_FILE:-$ROOT_DIR/.tma-worker.log}"
START_WAIT_SECONDS="${TMA_WORKER_START_WAIT_SECONDS:-2}"
STOP_WAIT_SECONDS="${TMA_WORKER_STOP_WAIT_SECONDS:-45}"

usage() {
  cat <<'EOF'
Usage: scripts/tma_worker.sh {start|stop|restart|status} [worker-options...]

Worker options passed after start or restart are forwarded to tma-worker.
Worker configuration can also use TMA_BASE_URL and the TMA_WORKER_* variables
supported by the worker binary.

Process manager overrides:
  TMA_WORKER_BIN                worker binary path
  TMA_WORKER_PID_FILE           pid file path
  TMA_WORKER_LOG_FILE           worker log path
  TMA_WORKER_START_WAIT_SECONDS seconds the process must remain alive on start
  TMA_WORKER_STOP_WAIT_SECONDS  seconds to wait for graceful drain on stop
EOF
}

read_pid() {
  if [ ! -f "$PID_FILE" ]; then
    return 1
  fi
  pid="$(tr -d '[:space:]' < "$PID_FILE")"
  if [ -z "$pid" ]; then
    return 1
  fi
  printf '%s\n' "$pid"
}

is_running() {
  pid="$1"
  kill -0 "$pid" 2>/dev/null
}

ensure_worker_bin() {
  if [ -x "$WORKER_BIN" ]; then
    return 0
  fi
  echo "Building $WORKER_BIN"
  go build -o "$WORKER_BIN" ./cmd/tma-worker
}

launch_detached() {
  python3 - "$PID_FILE" "$LOG_FILE" "$WORKER_BIN" "$@" <<'PY'
import os
import subprocess
import sys

pid_file = sys.argv[1]
log_file = sys.argv[2]
worker_bin = sys.argv[3]
worker_args = sys.argv[4:]

with open(log_file, "ab", buffering=0) as log:
    proc = subprocess.Popen(
        [worker_bin, *worker_args],
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=log,
        start_new_session=True,
        close_fds=True,
    )
    temp_pid_file = f"{pid_file}.{proc.pid}.tmp"
    with open(temp_pid_file, "w", encoding="ascii") as output:
        output.write(f"{proc.pid}\n")
    os.replace(temp_pid_file, pid_file)
    log.write(f"spawned pid={proc.pid}\n".encode())
PY
}

wait_for_start() {
  pid="$1"
  deadline=$(( $(date +%s) + START_WAIT_SECONDS ))
  while :; do
    if ! is_running "$pid"; then
      return 1
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 0
    fi
    sleep 1
  done
}

wait_for_exit() {
  pid="$1"
  deadline=$(( $(date +%s) + STOP_WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if ! is_running "$pid"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

start_worker() {
  if pid="$(read_pid 2>/dev/null)" && is_running "$pid"; then
    echo "tma-worker is already running: pid=$pid"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    return 0
  fi

  ensure_worker_bin
  mkdir -p "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")"
  rm -f "$PID_FILE"

  echo "Starting tma-worker"
  launch_detached "$@"
  pid="$(read_pid)"

  if ! wait_for_start "$pid"; then
    rm -f "$PID_FILE"
    echo "tma-worker exited during startup" >&2
    echo "log_file=$LOG_FILE" >&2
    return 1
  fi

  echo "tma-worker started: pid=$pid"
  echo "pid_file=$PID_FILE"
  echo "log_file=$LOG_FILE"
}

stop_worker() {
  if ! pid="$(read_pid 2>/dev/null)"; then
    echo "tma-worker is not running (missing pid file)"
    return 0
  fi
  if ! is_running "$pid"; then
    rm -f "$PID_FILE"
    echo "tma-worker is not running (stale pid file removed)"
    return 0
  fi

  echo "Stopping tma-worker: pid=$pid"
  kill "$pid"
  if ! wait_for_exit "$pid"; then
    echo "tma-worker did not stop within ${STOP_WAIT_SECONDS}s" >&2
    echo "pid_file=$PID_FILE" >&2
    return 1
  fi
  rm -f "$PID_FILE"
  echo "tma-worker stopped"
}

status_worker() {
  if ! pid="$(read_pid 2>/dev/null)"; then
    echo "tma-worker is stopped"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    return 1
  fi
  if ! is_running "$pid"; then
    echo "tma-worker is stopped (stale pid file)"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    return 1
  fi
  echo "tma-worker is running: pid=$pid"
  echo "pid_file=$PID_FILE"
  echo "log_file=$LOG_FILE"
}

restart_worker() {
  stop_worker
  start_worker "$@"
}

command="${1:-}"
if [ "$#" -gt 0 ]; then
  shift
fi
case "$command" in
  start)
    start_worker "$@"
    ;;
  stop)
    if [ "$#" -ne 0 ]; then
      usage >&2
      exit 2
    fi
    stop_worker
    ;;
  restart)
    restart_worker "$@"
    ;;
  status)
    if [ "$#" -ne 0 ]; then
      usage >&2
      exit 2
    fi
    status_worker
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
