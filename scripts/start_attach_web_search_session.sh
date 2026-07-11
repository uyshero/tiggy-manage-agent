#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

CLI="${TMA_ATTACH_CLI:-$ROOT_DIR/bin/tma}"
QUERY="${1:-阿里千问 最新 新闻}"
LLM_PROVIDER="${TMA_ATTACH_LLM_PROVIDER:-volcengine-agent-plan}"
LLM_MODEL="${TMA_ATTACH_LLM_MODEL:-doubao-seed-2.0-pro}"
INTERVENTION_MODE="${TMA_ATTACH_INTERVENTION_MODE:-approve_for_me}"
SUFFIX="$(date +%Y%m%d%H%M%S)"
AGENT_NAME="attach-web-search-agent-$SUFFIX"
ENV_NAME="attach-web-search-env-$SUFFIX"
SESSION_TITLE="Attach web search $SUFFIX"
SYSTEM_PROMPT="You are a verification agent. When the user asks for latest news or explicitly asks to use web.search, call web.search first and then answer briefly."

json_field() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

payload = json.loads(sys.argv[1])
print(payload[sys.argv[2]])
PY
}

if [ ! -x "$CLI" ]; then
  echo "Building bin/tma"
  go build -o "$ROOT_DIR/bin/tma" ./cmd/tma
  CLI="$ROOT_DIR/bin/tma"
fi

echo "Checking server health"
"$CLI" health >/dev/null

echo "Creating agent"
agent_json="$("$CLI" agent create \
  --name "$AGENT_NAME" \
  --llm-provider "$LLM_PROVIDER" \
  --llm-model "$LLM_MODEL" \
  --system "$SYSTEM_PROMPT")"
agent_id="$(json_field "$agent_json" id)"

echo "Creating environment"
env_json="$("$CLI" env create \
  --name "$ENV_NAME" \
  --config '{"type":"verification","networking":{"type":"local"}}')"
env_id="$(json_field "$env_json" id)"

echo "Creating session"
session_json="$("$CLI" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "$SESSION_TITLE")"
session_id="$(json_field "$session_json" id)"

echo "Configuring session runtime"
"$CLI" session runtime update \
  --session "$session_id" \
  --intervention-mode "$INTERVENTION_MODE" >/dev/null

echo
echo "session_id=$session_id"
echo "agent_id=$agent_id"
echo "env_id=$env_id"
echo
echo "Suggested prompt inside attach:"
echo "Use the web.search tool to search for $QUERY, then briefly summarize the top 3 freshest results."
echo
echo "Starting session attach"
exec "$CLI" session attach --session "$session_id" --after 0
