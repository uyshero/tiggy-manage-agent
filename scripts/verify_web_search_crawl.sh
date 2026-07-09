#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_WEB_BASE_URL:-http://localhost:18083}"
HTTP_ADDR="${TMA_VERIFY_WEB_HTTP_ADDR:-:18083}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
CLI="${TMA_CLI:-bin/tma}"
SERVER_LOG="${TMA_VERIFY_WEB_SERVER_LOG:-.verify-web-server.log}"
FIXTURE_ADDR="${TMA_VERIFY_WEB_FIXTURE_ADDR:-127.0.0.1:18084}"
FIXTURE_LOG="${TMA_VERIFY_WEB_FIXTURE_LOG:-.verify-web-fixture.log}"
MOCK_SEARXNG_ADDR="${TMA_VERIFY_WEB_MOCK_SEARXNG_ADDR:-127.0.0.1:18085}"
MOCK_SEARXNG_URL="http://$MOCK_SEARXNG_ADDR"
MOCK_SEARXNG_LOG="${TMA_VERIFY_WEB_MOCK_SEARXNG_LOG:-.verify-web-mock-searxng.log}"
WAIT_SECONDS="${TMA_VERIFY_WEB_WAIT_SECONDS:-30}"
SEARXNG_URL="${TMA_WEB_SEARXNG_BASE_URL:-http://localhost:8180}"
SEARXNG_WAIT_SECONDS="${TMA_VERIFY_SEARXNG_WAIT_SECONDS:-45}"

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

wait_for_http() {
  url="$1"
  label="$2"
  wait_seconds="$3"
  deadline=$(( $(date +%s) + wait_seconds ))
  while [ "$(date +%s)" -le "$deadline" ]; do
    if python3 - "$url" <<'PY' >/dev/null 2>&1
import sys
import urllib.request

with urllib.request.urlopen(sys.argv[1], timeout=3) as response:
    if 200 <= response.status < 500:
        raise SystemExit(0)
raise SystemExit(1)
PY
    then
      echo "$label is reachable"
      return 0
    fi
    sleep 1
  done
  echo "$label did not become reachable within ${wait_seconds}s: $url" >&2
  return 1
}

probe_searxng_json() {
  python3 - "$SEARXNG_URL/search?q=tma-web-verify&format=json" <<'PY'
import json
import sys
import urllib.request

with urllib.request.urlopen(sys.argv[1], timeout=10) as response:
    payload = json.load(response)
if not isinstance(payload.get("results", []), list):
    print("SearXNG JSON response missing results list", file=sys.stderr)
    raise SystemExit(1)
PY
}

validate_events() {
  expected_api="$1"
  expected_marker="$2"
  EXPECTED_WEB_API="$expected_api" EXPECTED_WEB_MARKER="$expected_marker" python3 -c '
import json
import os
import sys

expected_api = os.environ["EXPECTED_WEB_API"]
expected_marker = os.environ["EXPECTED_WEB_MARKER"]
data = json.load(sys.stdin)
events = data.get("events", [])
types = [event.get("type") for event in events]
for event_type in ["runtime.tool_call", "runtime.tool_result", "agent.message", "session.status_idle"]:
    if event_type not in types:
        raise SystemExit(1)

tool_calls = [event for event in events if event.get("type") == "runtime.tool_call"]
if not any(
    event.get("payload", {}).get("data", {}).get("identifier") == "web"
    and event.get("payload", {}).get("data", {}).get("api_name") == expected_api
    for event in tool_calls
):
    raise SystemExit(2)

tool_results = [event for event in events if event.get("type") == "runtime.tool_result"]
content = tool_results[-1].get("payload", {}).get("data", {}).get("content", "")
if expected_marker not in content:
    print("tool result missing marker", file=sys.stderr)
    print(content, file=sys.stderr)
    raise SystemExit(3)

agent_events = [event for event in events if event.get("type") == "agent.message"]
agent_payload = agent_events[-1].get("payload", {})
texts = [
    item.get("text", "")
    for item in agent_payload.get("content", [])
    if item.get("type") == "text"
]
if not any(expected_marker in text for text in texts):
    print("agent message missing marker", file=sys.stderr)
    print(repr(texts), file=sys.stderr)
    raise SystemExit(4)

print(agent_payload.get("turn_id", ""))
'
}

server_pid=""
fixture_pid=""
mock_searxng_pid=""
fixture_dir=""
cleanup() {
  if [ -n "$server_pid" ]; then
    if kill -0 "$server_pid" 2>/dev/null; then
      kill "$server_pid" 2>/dev/null || true
      wait "$server_pid" 2>/dev/null || true
    fi
  fi
  if [ -n "$fixture_pid" ]; then
    if kill -0 "$fixture_pid" 2>/dev/null; then
      kill "$fixture_pid" 2>/dev/null || true
      wait "$fixture_pid" 2>/dev/null || true
    fi
  fi
  if [ -n "$mock_searxng_pid" ]; then
    if kill -0 "$mock_searxng_pid" 2>/dev/null; then
      kill "$mock_searxng_pid" 2>/dev/null || true
      wait "$mock_searxng_pid" 2>/dev/null || true
    fi
  fi
  if [ -n "$fixture_dir" ] && [ -d "$fixture_dir" ]; then
    rm -rf "$fixture_dir"
  fi
}
trap cleanup EXIT INT TERM

echo "Starting SearXNG for web verification"
docker compose up -d searxng
wait_for_http "$SEARXNG_URL/healthz" "SearXNG" "$SEARXNG_WAIT_SECONDS"
probe_searxng_json
echo "SearXNG JSON probe passed"

fixture_dir="$(mktemp -d "${TMPDIR:-/tmp}/tma-web-verify.XXXXXX")"
cat >"$fixture_dir/index.html" <<'HTML'
<!doctype html>
<html>
  <head><title>TMA web verification</title></head>
  <body>
    <main>
      <h1>tma-web-crawl-ok</h1>
      <p>This local page is intentionally long enough for the crawler success threshold. tma-web-crawl-ok confirms that web.crawl fetched and normalized readable content through the AgentRuntime tool loop.</p>
    </main>
  </body>
</html>
HTML

echo "Starting local web fixture at http://$FIXTURE_ADDR/"
python3 -m http.server "${FIXTURE_ADDR##*:}" --bind "${FIXTURE_ADDR%:*}" --directory "$fixture_dir" >"$FIXTURE_LOG" 2>&1 &
fixture_pid="$!"
wait_for_http "http://$FIXTURE_ADDR/" "web fixture" "$WAIT_SECONDS"

echo "Starting SearXNG-compatible mock at $MOCK_SEARXNG_URL"
python3 - "$MOCK_SEARXNG_ADDR" <<'PY' >"$MOCK_SEARXNG_LOG" 2>&1 &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

host, port = sys.argv[1].rsplit(":", 1)

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        if parsed.path == "/search":
            query = parse_qs(parsed.query).get("q", [""])[0]
            payload = {
                "query": query,
                "results": [
                    {
                        "title": "tma-web-search-ok",
                        "url": "http://example.test/tma-web-search-ok",
                        "content": "tma-web-search-ok deterministic search result for " + query,
                        "engine": "mock-searxng",
                    }
                ],
            }
            body = json.dumps(payload).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, fmt, *args):
        return

ThreadingHTTPServer((host, int(port)), Handler).serve_forever()
PY
mock_searxng_pid="$!"
wait_for_http "$MOCK_SEARXNG_URL/healthz" "SearXNG mock" "$WAIT_SECONDS"

echo "Starting TMA server for web search/crawl verification"
echo "base_url=$BASE_URL"
echo "server_log=$SERVER_LOG"

TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
TMA_WEB_SEARCH_PROVIDERS="${TMA_WEB_SEARCH_PROVIDERS:-searxng}" \
TMA_WEB_SEARXNG_BASE_URL="$MOCK_SEARXNG_URL" \
"$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    cat "$SERVER_LOG" >&2 || true
    exit 1
  fi
  if "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! "$CLI" --base-url "$BASE_URL" health >/dev/null 2>&1; then
  echo "server did not become healthy within ${WAIT_SECONDS}s" >&2
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

suffix="$(date +%Y%m%d%H%M%S)"

echo "Creating web verification agent"
agent_json="$("$CLI" --base-url "$BASE_URL" agent create \
  --name "verify-web-agent-$suffix" \
  --model "fake-demo" \
  --system "Web search/crawl verification agent.")"
agent_id="$(printf '%s' "$agent_json" | json_field id)"

echo "Configuring agent tools for web crawl"
"$CLI" --base-url "$BASE_URL" agent config update \
  --agent "$agent_id" \
  --tools '{"tools":["web"],"runtime":"auto"}' >/dev/null

echo "Creating web verification environment"
env_json="$("$CLI" --base-url "$BASE_URL" env create \
  --name "verify-web-env-$suffix" \
  --config '{"type":"verification","networking":{"type":"local"}}')"
env_id="$(printf '%s' "$env_json" | json_field id)"

echo "Creating web verification session"
session_json="$("$CLI" --base-url "$BASE_URL" session create \
  --agent "$agent_id" \
  --env "$env_id" \
  --title "Web search/crawl verification $suffix")"
session_id="$(printf '%s' "$session_json" | json_field id)"

echo "Configuring session approval mode"
"$CLI" --base-url "$BASE_URL" session runtime update \
  --session "$session_id" \
  --intervention-mode approve_for_me >/dev/null

echo "Sending web crawl verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_web_crawl http://$FIXTURE_ADDR/" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
last_events=""
crawl_turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if crawl_turn_id="$(printf '%s' "$last_events" | validate_events crawl tma-web-crawl-ok 2>/dev/null)"; then
    break
  fi
  sleep 1
done

if [ -z "$crawl_turn_id" ]; then
  echo "web crawl verification timed out after ${WAIT_SECONDS}s" >&2
  echo "Last events:" >&2
  printf '%s\n' "$last_events" >&2
  echo "server log:" >&2
  cat "$SERVER_LOG" >&2 || true
  echo "fixture log:" >&2
  cat "$FIXTURE_LOG" >&2 || true
  echo "mock searxng log:" >&2
  cat "$MOCK_SEARXNG_LOG" >&2 || true
  exit 1
fi

echo "Sending web search verification message to $session_id"
"$CLI" --base-url "$BASE_URL" event send \
  --session "$session_id" \
  --text "tma.verify_web_search tma-web-search-ok" >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
search_turn_id=""
while [ "$(date +%s)" -le "$deadline" ]; do
  last_events="$("$CLI" --base-url "$BASE_URL" event list --session "$session_id" --after 0)"
  if search_turn_id="$(printf '%s' "$last_events" | validate_events search tma-web-search-ok 2>/dev/null)"; then
    echo "Web search/crawl verification passed"
    echo "session_id=$session_id"
    echo "crawl_turn_id=$crawl_turn_id"
    echo "search_turn_id=$search_turn_id"
    exit 0
  fi
  sleep 1
done

echo "web search verification timed out after ${WAIT_SECONDS}s" >&2
echo "Last events:" >&2
printf '%s\n' "$last_events" >&2
echo "server log:" >&2
cat "$SERVER_LOG" >&2 || true
echo "fixture log:" >&2
cat "$FIXTURE_LOG" >&2 || true
echo "mock searxng log:" >&2
cat "$MOCK_SEARXNG_LOG" >&2 || true
exit 1
