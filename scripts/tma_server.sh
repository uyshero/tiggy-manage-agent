#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

SERVER_BIN="${TMA_SERVER_BIN:-$ROOT_DIR/bin/tma-server}"
CLI_BIN="${TMA_CLI_BIN:-$ROOT_DIR/bin/tma}"
PID_FILE="${TMA_SERVER_PID_FILE:-$ROOT_DIR/.tma-server.pid}"
LOG_FILE="${TMA_SERVER_LOG_FILE:-$ROOT_DIR/.tma-server.log}"
START_WAIT_SECONDS="${TMA_SERVER_START_WAIT_SECONDS:-15}"
STOP_WAIT_SECONDS="${TMA_SERVER_STOP_WAIT_SECONDS:-15}"

usage() {
  cat <<'EOF'
Usage: scripts/tma_server.sh {start|stop|restart|status}

Environment overrides:
  TMA_SERVER_BIN               server binary path
  TMA_CLI_BIN                  CLI binary path used for health checks
  TMA_SERVER_PID_FILE          pid file path
  TMA_SERVER_LOG_FILE          server log path
  TMA_SERVER_START_WAIT_SECONDS seconds to wait for health after start
  TMA_SERVER_STOP_WAIT_SECONDS  seconds to wait for graceful stop
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

ensure_server_bin() {
  if [ -x "$SERVER_BIN" ]; then
    return 0
  fi
  echo "Building $SERVER_BIN"
  go build -o "$SERVER_BIN" ./cmd/tma-server
}

ensure_cli_bin() {
  if [ -x "$CLI_BIN" ]; then
    return 0
  fi
  echo "Building $CLI_BIN"
  go build -o "$CLI_BIN" ./cmd/tma
}

launch_detached() {
  mode="$1"
  shift
  python3 - "$PID_FILE" "$LOG_FILE" "$SERVER_BIN" "$mode" "$@" <<'PY'
import os
import subprocess
import sys

pid_file = sys.argv[1]
log_file = sys.argv[2]
server_bin = sys.argv[3]
mode = sys.argv[4]
extra_args = sys.argv[5:]

cmd = [server_bin]
if mode == "restart":
    cmd.extend(["--pid-file", pid_file, "--restart"])
else:
    cmd.extend(["--pid-file", pid_file])
    cmd.extend(extra_args)

with open(log_file, "ab", buffering=0) as log:
    proc = subprocess.Popen(
        cmd,
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=log,
        start_new_session=True,
        close_fds=True,
    )
    log.write(f"spawned pid={proc.pid}\n".encode())
PY
}

wait_for_health() {
  ensure_cli_bin
  deadline=$(( $(date +%s) + START_WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if "$CLI_BIN" health >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
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

start_server() {
  if pid="$(read_pid 2>/dev/null)" && is_running "$pid"; then
    echo "tma-server is already running: pid=$pid"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    return 0
  fi

  ensure_server_bin
  mkdir -p "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")"

  echo "Starting tma-server"
  launch_detached start

  if ! wait_for_health; then
    echo "tma-server did not become healthy within ${START_WAIT_SECONDS}s" >&2
    echo "log_file=$LOG_FILE" >&2
    exit 1
  fi

  pid="$(read_pid)"
  echo "tma-server started: pid=$pid"
  echo "pid_file=$PID_FILE"
  echo "log_file=$LOG_FILE"
}

stop_server() {
  if ! pid="$(read_pid 2>/dev/null)"; then
    echo "tma-server is not running (missing pid file)"
    return 0
  fi
  if ! is_running "$pid"; then
    rm -f "$PID_FILE"
    echo "tma-server is not running (stale pid file removed)"
    return 0
  fi

  echo "Stopping tma-server: pid=$pid"
  kill "$pid"
  if ! wait_for_exit "$pid"; then
    echo "tma-server did not stop within ${STOP_WAIT_SECONDS}s" >&2
    echo "pid_file=$PID_FILE" >&2
    exit 1
  fi
  rm -f "$PID_FILE"
  echo "tma-server stopped"
}

status_server() {
  if ! pid="$(read_pid 2>/dev/null)"; then
    echo "tma-server is stopped"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    return 1
  fi
  if ! is_running "$pid"; then
    echo "tma-server is stopped (stale pid file)"
    echo "pid_file=$PID_FILE"
    echo "log_file=$LOG_FILE"
    return 1
  fi
  echo "tma-server is running: pid=$pid"
  echo "pid_file=$PID_FILE"
  echo "log_file=$LOG_FILE"
}

restart_server() {
  ensure_server_bin
  mkdir -p "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")"

  echo "Restarting tma-server"
  launch_detached restart

  if ! wait_for_health; then
    echo "tma-server did not become healthy within ${START_WAIT_SECONDS}s after restart" >&2
    echo "log_file=$LOG_FILE" >&2
    exit 1
  fi

  pid="$(read_pid)"
  echo "tma-server restarted: pid=$pid"
  echo "pid_file=$PID_FILE"
  echo "log_file=$LOG_FILE"
}

command="${1:-}"
case "$command" in
  start)
    start_server
    ;;
  stop)
    stop_server
    ;;
  restart)
    restart_server
    ;;
  status)
    status_server
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
