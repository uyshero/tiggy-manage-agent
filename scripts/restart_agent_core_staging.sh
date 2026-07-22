#!/bin/sh
set -eu

BASE_URL="${TMA_BASE_URL:-http://localhost:18088}"
CLI="${TMA_CLI:-bin/tma}"
PID_FILE="${TMA_AGENT_CORE_STAGING_PID_FILE:-.tma-agent-core-staging.pid}"
SCREEN_NAME="${TMA_AGENT_CORE_STAGING_SCREEN_NAME:-tma-agent-core-staging}"
START_SCRIPT="${TMA_AGENT_CORE_STAGING_START_SCRIPT:-/tmp/tma-agent-core-staging.sh}"
WAIT_SECONDS="${TMA_AGENT_CORE_STAGING_RESTART_WAIT_SECONDS:-30}"
STOP_SIGNAL="${TMA_AGENT_CORE_STAGING_STOP_SIGNAL:-TERM}"

if [ ! -r "$PID_FILE" ]; then
  echo "staging pid file is not readable: $PID_FILE" >&2
  exit 1
fi
if [ ! -x "$START_SCRIPT" ]; then
  echo "staging start script is not executable: $START_SCRIPT" >&2
  exit 1
fi
case "$STOP_SIGNAL" in
  TERM|KILL) ;;
  *)
    echo "unsupported staging stop signal: $STOP_SIGNAL" >&2
    exit 1
    ;;
esac

old_pid="$(cat "$PID_FILE")"
case "$old_pid" in
  ''|*[!0-9]*)
    echo "invalid staging pid: $old_pid" >&2
    exit 1
    ;;
esac
if ! kill -0 "$old_pid" 2>/dev/null; then
  echo "staging process is not running: $old_pid" >&2
  exit 1
fi

echo "Stopping staging process pid=$old_pid signal=$STOP_SIGNAL"
kill "-$STOP_SIGNAL" "$old_pid"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while kill -0 "$old_pid" 2>/dev/null && [ "$(date +%s)" -le "$deadline" ]; do
  sleep 1
done
if kill -0 "$old_pid" 2>/dev/null; then
  echo "staging process did not stop within ${WAIT_SECONDS}s" >&2
  exit 1
fi

echo "Starting staging process in screen=$SCREEN_NAME"
screen -dmS "$SCREEN_NAME" "$START_SCRIPT"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1 && \
    "$CLI" --base-url "$BASE_URL" provider list >/dev/null 2>&1; then
    new_pid="$(cat "$PID_FILE")"
    if [ "$new_pid" = "$old_pid" ]; then
      echo "staging restart kept the old pid: $new_pid" >&2
      exit 1
    fi
    echo "Staging restart passed old_pid=$old_pid new_pid=$new_pid"
    exit 0
  fi
  sleep 1
done

echo "staging process did not become ready within ${WAIT_SECONDS}s" >&2
exit 1
