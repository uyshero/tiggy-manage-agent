#!/bin/sh
set -eu

BASE_URL="${TMA_BASE_URL:-http://localhost:18088}"
CLI="${TMA_CLI:-bin/tma}"
MODEL="${TMA_AGENT_CORE_STAGING_MODEL:-doubao-seed-2.0-pro}"
WAIT_SECONDS="${TMA_AGENT_CORE_STAGING_WAIT_SECONDS:-180}"
DOTENV_PATH="${TMA_DOTENV_PATH:-.env}"
RESTART_DRILL="${TMA_AGENT_CORE_STAGING_RESTART_DRILL:-false}"
CRASH_DRILL="${TMA_AGENT_CORE_STAGING_CRASH_DRILL:-false}"
INFRASTRUCTURE_DRILL="${TMA_AGENT_CORE_STAGING_INFRASTRUCTURE_DRILL:-false}"

if [ ! -x "$CLI" ]; then
  echo "missing CLI: $CLI" >&2
  echo "run: make build-cli" >&2
  exit 1
fi

echo "Checking staging health: $BASE_URL"
"$CLI" --base-url "$BASE_URL" health >/dev/null
if ! "$CLI" --base-url "$BASE_URL" provider list >/dev/null; then
  echo "staging database dependency is unavailable" >&2
  exit 1
fi

echo "Verifying Agent Core model completion"
TMA_BASE_URL="$BASE_URL" \
TMA_CLI="$CLI" \
TMA_VERIFY_AGENT_MODEL="$MODEL" \
TMA_VERIFY_AGENT_SYSTEM="You are a production smoke-test agent. Never call tools. Follow the user response-format instruction exactly." \
TMA_VERIFY_MESSAGE="Reply with exactly: AGENT_CORE_OK" \
TMA_VERIFY_EXPECTED_TEXT="AGENT_CORE_OK" \
TMA_VERIFY_WAIT_SECONDS="$WAIT_SECONDS" \
  scripts/verify_agent_runtime.sh

echo "Verifying durable approval continuation"
before_decision_hook=""
if [ "$RESTART_DRILL" = "true" ]; then
  before_decision_hook="scripts/restart_agent_core_staging.sh"
fi
TMA_BASE_URL="$BASE_URL" \
TMA_CLI="$CLI" \
TMA_DOTENV_PATH="$DOTENV_PATH" \
TMA_APPROVAL_TEST_DECISION="approve" \
TMA_APPROVAL_TEST_PRINT_EVENTS="false" \
TMA_APPROVAL_TEST_BEFORE_DECISION_HOOK="$before_decision_hook" \
TMA_APPROVAL_TEST_WAIT_SECONDS="$WAIT_SECONDS" \
  scripts/verify_intervention_flow.sh

if [ "$CRASH_DRILL" = "true" ]; then
  echo "Verifying in-flight model and tool crash recovery"
  TMA_BASE_URL="$BASE_URL" \
  TMA_CLI="$CLI" \
  TMA_DOTENV_PATH="$DOTENV_PATH" \
  TMA_AGENT_CORE_STAGING_WAIT_SECONDS="$WAIT_SECONDS" \
    scripts/verify_agent_core_crash_recovery.sh
fi

if [ "$INFRASTRUCTURE_DRILL" = "true" ]; then
  echo "Verifying database outage and lease fencing recovery"
  TMA_BASE_URL="$BASE_URL" \
  TMA_CLI="$CLI" \
  TMA_DOTENV_PATH="$DOTENV_PATH" \
  TMA_AGENT_CORE_STAGING_WAIT_SECONDS="$WAIT_SECONDS" \
    scripts/verify_agent_core_infrastructure_recovery.sh
fi

echo "Verifying Agent Core metrics"
metrics="$(curl -fsS "$BASE_URL/metrics")"
for metric in tma_agent_core_events_total tma_worker_lease_events_total; do
  if ! printf '%s\n' "$metrics" | grep -q "^# TYPE $metric counter$"; then
    echo "missing counter metric: $metric" >&2
    exit 1
  fi
done

echo "Agent Core staging verification passed"
