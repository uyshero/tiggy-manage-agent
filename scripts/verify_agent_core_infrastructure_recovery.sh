#!/bin/sh
set -eu

BASE_URL="${TMA_BASE_URL:-http://localhost:18088}"
CLI="${TMA_CLI:-bin/tma}"
DOTENV_PATH="${TMA_DOTENV_PATH:-.env}"
WAIT_SECONDS="${TMA_AGENT_CORE_STAGING_WAIT_SECONDS:-180}"
POSTGRES_CONTAINER="${TMA_AGENT_CORE_STAGING_POSTGRES_CONTAINER:-tma-postgres}"
OUTAGE_SECONDS="${TMA_AGENT_CORE_DATABASE_OUTAGE_SECONDS:-18}"
MODE="${TMA_AGENT_CORE_INFRASTRUCTURE_MODE:-all}"
MAIN_PID_FILE="${TMA_AGENT_CORE_STAGING_PID_FILE:-.tma-agent-core-staging.pid}"
COMPETITOR_BASE_URL="${TMA_AGENT_CORE_COMPETITOR_BASE_URL:-http://localhost:18089}"
COMPETITOR_HTTP_ADDR="${TMA_AGENT_CORE_COMPETITOR_HTTP_ADDR:-:18089}"

database_stopped=false
main_stopped=false
main_pid=""
competitor_pid=""
competitor_pid_file=""
competitor_screen_name=""

cleanup() {
  if [ "$main_stopped" = true ] && [ -n "$main_pid" ]; then
    kill -CONT "$main_pid" 2>/dev/null || true
  fi
  if [ -n "$competitor_pid" ] && kill -0 "$competitor_pid" 2>/dev/null; then
    kill -TERM "$competitor_pid" 2>/dev/null || true
  fi
  if [ -n "$competitor_screen_name" ]; then
    screen -S "$competitor_screen_name" -X quit >/dev/null 2>&1 || true
  fi
  if [ "$database_stopped" = true ]; then
    docker start "$POSTGRES_CONTAINER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

env_value() {
  key="$1"
  python3 - "$key" "$DOTENV_PATH" <<'PY'
import os
import sys

key = sys.argv[1]
path = sys.argv[2]
if os.environ.get(key):
    print(os.environ[key])
    raise SystemExit
for line in open(path, encoding="utf-8"):
    line = line.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    name, value = line.split("=", 1)
    if name.strip() == key:
        print(value.strip().strip("\"'"))
        break
PY
}

json_field() {
  python3 -c 'import json,sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"
}

create_session() {
  name="$1"
  system_prompt="$2"
  suffix="$(date +%Y%m%d%H%M%S)"
  agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
    --name "$name-agent-$suffix" \
    --llm-provider "$PROVIDER" \
    --model "$MODEL" \
    --system "$system_prompt")"
  agent_id="$(printf '%s' "$agent_json" | json_field id)"
  env_json="$("$CLI" --base-url "$BASE_URL" env create --name "$name-env-$suffix" --config "{\"type\":\"$name\"}")"
  env_id="$(printf '%s' "$env_json" | json_field id)"
  session_json="$("$CLI" --base-url "$BASE_URL" session create --agent "$agent_id" --env "$env_id" --title "$name $suffix")"
  printf '%s' "$session_json" | json_field id
}

database_state() {
  session_id="$1"
  docker exec "$POSTGRES_CONTAINER" psql -U tma -d tma -Atc "
    SELECT concat_ws('|',
      als.state_json->>'phase',
      als.revision::text,
      COALESCE(als.state_json->'pending_model'->>'number', ''),
      COALESCE(als.state_json->'tool_journal'->0->>'status', ''),
      COALESCE(als.state_json->'tool_journal'->0->>'attempt', ''),
      COALESCE(als.state_json->'tool_journal'->0->>'idempotency', ''),
      COALESCE(als.state_json->'tool_journal'->0->'reconciliation'->>'outcome', ''),
      st.attempt_count::text,
      COALESCE(st.lease_owner, ''),
      st.status)
    FROM agent_loop_states als
    JOIN session_turns st ON st.session_id=als.session_id AND st.id=als.turn_id
    WHERE als.session_id='$session_id' AND als.turn_id='turn_000001';
  "
}

wait_for_state() {
  session_id="$1"
  expected="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  state=""
  while [ "$(date +%s)" -le "$deadline" ]; do
    state="$(database_state "$session_id")"
    case "$state" in
      "$expected"*)
        printf '%s\n' "$state"
        return 0
        ;;
    esac
    sleep 1
  done
  echo "timed out waiting for durable state prefix $expected, last=$state" >&2
  return 1
}

wait_for_database() {
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    health=$(docker inspect "$POSTGRES_CONTAINER" --format '{{.State.Health.Status}}' 2>/dev/null || true)
    if [ "$health" = healthy ] && "$CLI" --base-url "$BASE_URL" provider list >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "database did not recover within ${WAIT_SECONDS}s" >&2
  return 1
}

wait_for_events() {
  base_url="$1"
  session_id="$2"
  recovery_type="$3"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  events_json=""
  while [ "$(date +%s)" -le "$deadline" ]; do
    events_json="$("$CLI" --base-url "$base_url" event list --session "$session_id" 2>/dev/null || true)"
    if printf '%s' "$events_json" | python3 -c '
import json
import sys

kind = sys.argv[1]
events = json.load(sys.stdin).get("events", [])
types = [event.get("type") for event in events]
common = "runtime.completed" in types and "agent.message" in types and "session.status_idle" in types and "runtime.failed" not in types
if kind == "database":
    results = [event for event in events if event.get("type") == "tool.call_result"]
    valid = common and len(results) == 1 and results[0].get("payload", {}).get("data", {}).get("status") == "indeterminate" and types.count("tool.call_reconciled") == 1
else:
    valid = common and types.count("model.abandoned") == 1 and types.count("model.requested") == 2 and types.count("model.responded") == 1
sys.exit(0 if valid else 1)
' "$recovery_type" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for $recovery_type infrastructure recovery" >&2
  printf '%s\n' "$events_json" >&2
  return 1
}

wait_for_reconciliation() {
  session_id="$1"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    interventions_json="$("$CLI" --base-url "$BASE_URL" session intervention list --session "$session_id" --status pending 2>/dev/null || true)"
    call_id="$(printf '%s' "$interventions_json" | python3 -c '
import json
import sys

for item in json.load(sys.stdin).get("interventions", []):
    request = item.get("request") or {}
    if request.get("purpose") == "tool_reconciliation":
        print(item.get("call_id", ""))
        break
' 2>/dev/null || true)"
    if [ -n "$call_id" ]; then
      printf '%s\n' "$call_id"
      return 0
    fi
    sleep 1
  done
  echo "timed out waiting for tool reconciliation intervention" >&2
  return 1
}

stop_competitor() {
  if [ -z "$competitor_pid" ]; then
    return
  fi
  if kill -0 "$competitor_pid" 2>/dev/null; then
    kill -TERM "$competitor_pid"
    deadline=$(( $(date +%s) + 30 ))
    while kill -0 "$competitor_pid" 2>/dev/null && [ "$(date +%s)" -le "$deadline" ]; do
      sleep 1
    done
    if kill -0 "$competitor_pid" 2>/dev/null; then
      echo "competitor did not stop within 30s" >&2
      return 1
    fi
  fi
  competitor_pid=""
  competitor_screen_name=""
}

PROVIDER="$(env_value TMA_LLM_PROVIDER)"
MODEL="$(env_value TMA_LLM_MODEL)"
if [ -z "$PROVIDER" ] || [ -z "$MODEL" ]; then
  echo "TMA_LLM_PROVIDER and TMA_LLM_MODEL are required in environment or $DOTENV_PATH" >&2
  exit 1
fi

verify_database_outage() {
  system_prompt='When the user says DATABASE_OUTAGE_TOOL, call default.run_command exactly once with command "sh" and args ["-c","sleep 300; printf DATABASE_TOOL_COMPLETED"]. Do not answer before calling the tool. After receiving the tool result, explain whether execution was confirmed and do not call more tools.'
  session_id="$(create_session agent-core-database-outage "$system_prompt")"
  "$CLI" --base-url "$BASE_URL" session runtime update --session "$session_id" --intervention-mode full_access >/dev/null
  "$CLI" --base-url "$BASE_URL" event send --session "$session_id" --text DATABASE_OUTAGE_TOOL >/dev/null
  state="$(wait_for_state "$session_id" 'executing_tools|5||started|1|unknown||1|')"
  server_pid="$(cat "$MAIN_PID_FILE")"
  echo "Stopping database during tool execution session=$session_id state=$state"
  docker stop "$POSTGRES_CONTAINER" >/dev/null
  database_stopped=true
  sleep "$OUTAGE_SECONDS"
  docker start "$POSTGRES_CONTAINER" >/dev/null
  database_stopped=false
  wait_for_database
  reconciliation_call_id="$(wait_for_reconciliation "$session_id")"
  "$CLI" --base-url "$BASE_URL" session intervention reconcile \
    --session "$session_id" \
    --turn turn_000001 \
    --call "$reconciliation_call_id" \
    --outcome compensated \
    --summary "The database outage drill canceled the test process; no persistent external side effect remains." \
    --evidence "agent-core-database-outage-drill" >/dev/null
  wait_for_events "$BASE_URL" "$session_id" database
  state="$(database_state "$session_id")"
  case "$state" in
    completed*'|failed|1|unknown|compensated|'*'||completed') ;;
    *)
      echo "unexpected database recovery state: $state" >&2
      exit 1
      ;;
  esac
  attempt_count="$(printf '%s\n' "$state" | awk -F'|' '{print $8}')"
  case "$attempt_count" in
    ''|*[!0-9]*)
      echo "invalid database recovery attempt count: $attempt_count" >&2
      exit 1
      ;;
  esac
  if [ "$attempt_count" -lt 2 ]; then
    echo "database recovery did not require a new Worker attempt: $attempt_count" >&2
    exit 1
  fi
  if [ "$(cat "$MAIN_PID_FILE")" != "$server_pid" ]; then
    echo "database recovery unexpectedly restarted the Server" >&2
    exit 1
  fi
  echo "Database outage recovery passed session=$session_id state=$state"
}

verify_lease_fencing() {
  system_prompt='This is a lease fencing test. Never call tools. Produce the requested long response without abbreviating it.'
  session_id="$(create_session agent-core-lease-fencing "$system_prompt")"
  "$CLI" --base-url "$BASE_URL" event send --session "$session_id" --text 'Write 200 numbered lines. Each line must contain a distinct short sentence.' >/dev/null
  main_pid="$(cat "$MAIN_PID_FILE")"
  state="$(wait_for_state "$session_id" 'awaiting_model|2|1|||||1|')"
  case "$state" in
    *"-$main_pid-"*) ;;
    *)
      echo "main Server did not own the initial lease: $state" >&2
      exit 1
      ;;
  esac

  echo "Pausing main Server pid=$main_pid session=$session_id state=$state"
  kill -STOP "$main_pid"
  main_stopped=true
  docker exec "$POSTGRES_CONTAINER" psql -U tma -d tma -v ON_ERROR_STOP=1 -c "
    UPDATE session_turns
    SET lease_expires_at=now()-interval '1 second'
    WHERE session_id='$session_id' AND id='turn_000001';
  " >/dev/null

  suffix="$(date +%Y%m%d%H%M%S)"
  competitor_pid_file="/tmp/tma-agent-core-competitor-$suffix.pid"
  competitor_screen_name="tma-agent-core-competitor-$suffix"
  TMA_DOTENV_PATH="$DOTENV_PATH" \
  TMA_AGENT_CORE_COMPETITOR_HTTP_ADDR="$COMPETITOR_HTTP_ADDR" \
  TMA_AGENT_CORE_COMPETITOR_PID_FILE="$competitor_pid_file" \
  TMA_AGENT_CORE_STAGING_POSTGRES_CONTAINER="$POSTGRES_CONTAINER" \
    screen -dmS "$competitor_screen_name" scripts/start_agent_core_competitor.sh

  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if "$CLI" --base-url "$COMPETITOR_BASE_URL" health >/dev/null 2>&1 && [ -r "$competitor_pid_file" ]; then
      competitor_pid="$(cat "$competitor_pid_file")"
      state="$(database_state "$session_id")"
      abandoned=$(docker exec "$POSTGRES_CONTAINER" psql -U tma -d tma -Atc "SELECT count(*) FROM session_events WHERE session_id='$session_id' AND type='model.abandoned';")
      case "$state|$abandoned" in
        *'|2|'*"-$competitor_pid-"*'|running|1')
          break
          ;;
      esac
    fi
    sleep 1
  done
  if [ -z "$competitor_pid" ]; then
    echo "competitor did not claim the expired lease" >&2
    exit 1
  fi

  echo "Resuming stale main Server pid=$main_pid after competitor pid=$competitor_pid claimed attempt 2"
  kill -CONT "$main_pid"
  main_stopped=false
  wait_for_events "$COMPETITOR_BASE_URL" "$session_id" fencing
  sleep 3
  state="$(database_state "$session_id")"
  case "$state" in
    completed'|7||||||2||completed') ;;
    *)
      echo "unexpected lease fencing state: $state" >&2
      exit 1
      ;;
  esac
  if ! "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1; then
    echo "main Server did not recover after SIGCONT" >&2
    exit 1
  fi
  stop_competitor
  echo "Lease fencing recovery passed session=$session_id state=$state"
}

case "$MODE" in
  database) verify_database_outage ;;
  fencing) verify_lease_fencing ;;
  all)
    verify_database_outage
    verify_lease_fencing
    ;;
  *)
    echo "unsupported TMA_AGENT_CORE_INFRASTRUCTURE_MODE=$MODE; use database, fencing, or all" >&2
    exit 1
    ;;
esac

echo "Agent Core infrastructure recovery verification passed"
