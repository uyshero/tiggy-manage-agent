#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
DOTENV_PATH="${TMA_DOTENV_PATH:-$ROOT_DIR/.env}"

if [ -f "$DOTENV_PATH" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$DOTENV_PATH"
  set +a
fi

BASE_URL="${1:-${TMA_LLM_BASE_URL:-https://ark.cn-beijing.volces.com/api/plan/v3}}"
MODEL="${2:-${TMA_LLM_MODEL:-doubao-seed-2.0-pro}}"
API_KEY_ENV="${TMA_LLM_API_KEY_ENV:-TMA_LLM_API_KEY}"
API_KEY="$(eval "printf '%s' \"\${$API_KEY_ENV:-}\"")"

if [ -z "$API_KEY" ]; then
  echo "missing API key: set $API_KEY_ENV or TMA_LLM_API_KEY" >&2
  exit 1
fi

endpoint="$(printf '%s' "$BASE_URL" | sed 's:/*$::')/chat/completions"

echo "endpoint=$endpoint"
echo "model=$MODEL"
echo "api_key_env=$API_KEY_ENV"
echo

request() {
  name="$1"
  body="$2"
  tmp_body="$(mktemp "${TMPDIR:-/tmp}/tma-volcengine-response.XXXXXX")"
  tmp_headers="$(mktemp "${TMPDIR:-/tmp}/tma-volcengine-headers.XXXXXX")"

  echo "==> $name"
  status="$(curl -sS \
    -D "$tmp_headers" \
    -o "$tmp_body" \
    -w "%{http_code}" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    "$endpoint" \
    -d "$body" || true)"

  echo "HTTP $status"
  if command -v jq >/dev/null 2>&1; then
    jq . "$tmp_body" 2>/dev/null || sed -n '1,80p' "$tmp_body"
  else
    python3 -m json.tool "$tmp_body" 2>/dev/null || sed -n '1,80p' "$tmp_body"
  fi
  echo

  rm -f "$tmp_body" "$tmp_headers"
}

request "plain chat completions request" "$(cat <<JSON
{
  "model": "$MODEL",
  "messages": [
    {
      "role": "user",
      "content": "hello"
    }
  ],
  "stream": false
}
JSON
)"

request "chat completions request with one minimal tool" "$(cat <<JSON
{
  "model": "$MODEL",
  "messages": [
    {
      "role": "user",
      "content": "Call the inspect_screen tool."
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "inspect_screen",
        "description": "Inspect the current screen.",
        "parameters": {
          "type": "object",
          "properties": {},
          "additionalProperties": false
        }
      }
    }
  ],
  "stream": false
}
JSON
)"
