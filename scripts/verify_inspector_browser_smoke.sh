#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_INSPECTOR_BASE_URL:-http://localhost:18089}"
HTTP_ADDR="${TMA_VERIFY_INSPECTOR_HTTP_ADDR:-:18089}"
DATABASE_URL="${TMA_DATABASE_URL:-postgres://tma:tma@localhost:5432/tma?sslmode=disable}"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
SERVER_LOG="${TMA_VERIFY_INSPECTOR_SERVER_LOG:-.verify-inspector-browser-server.log}"
WAIT_SECONDS="${TMA_VERIFY_INSPECTOR_WAIT_SECONDS:-25}"
CHROME_BIN="${TMA_VERIFY_CHROME_BIN:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
CHROME_DEBUG_PORT="${TMA_VERIFY_CHROME_DEBUG_PORT:-9223}"
CHROME_PROFILE="${TMA_VERIFY_CHROME_PROFILE:-/tmp/tma-inspector-smoke-chrome}"

if [ ! -x "$SERVER_BIN" ]; then
  echo "missing server binary: $SERVER_BIN"
  echo "run: make build"
  exit 1
fi

if [ ! -x "$CHROME_BIN" ]; then
  echo "missing Chrome binary: $CHROME_BIN"
  exit 1
fi

server_pid=""
chrome_pid=""
cleanup() {
  if [ -n "$chrome_pid" ]; then
    if kill -0 "$chrome_pid" 2>/dev/null; then
      kill "$chrome_pid" 2>/dev/null || true
      wait "$chrome_pid" 2>/dev/null || true
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

wait_for_http() {
  url="$1"
  label="$2"
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
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
  echo "$label did not become reachable within ${WAIT_SECONDS}s: $url" >&2
  return 1
}

echo "Starting TMA server for Inspector browser smoke"
TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
"$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
server_pid="$!"

if ! wait_for_http "$BASE_URL/health" "TMA server"; then
  cat "$SERVER_LOG" >&2 || true
  exit 1
fi

rm -rf "$CHROME_PROFILE"
echo "Starting headless Chrome"
"$CHROME_BIN" \
  --headless=new \
  --disable-gpu \
  --no-first-run \
  --no-default-browser-check \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port="$CHROME_DEBUG_PORT" \
  --user-data-dir="$CHROME_PROFILE" \
  about:blank >/tmp/tma-inspector-smoke-chrome.log 2>&1 &
chrome_pid="$!"

if ! wait_for_http "http://127.0.0.1:${CHROME_DEBUG_PORT}/json/version" "Chrome DevTools"; then
  cat /tmp/tma-inspector-smoke-chrome.log >&2 || true
  exit 1
fi

BASE_URL="$BASE_URL" CHROME_DEBUG_PORT="$CHROME_DEBUG_PORT" node <<'NODE'
const baseURL = process.env.BASE_URL;
const debugPort = process.env.CHROME_DEBUG_PORT;

async function api(path, options = {}) {
  const response = await fetch(`${baseURL}${path}`, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options
  });
  if (!response.ok) {
    throw new Error(`${options.method || "GET"} ${path} failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

async function post(path, body) {
  return api(path, { method: "POST", body: JSON.stringify(body) });
}

async function uploadArtifact(sessionID, turnID) {
  const text = `${"artifact-preview-smoke\n".repeat(800)}tail-marker`;
  const form = new FormData();
  form.append("file", new Blob([text], { type: "text/plain" }), "large-smoke.txt");
  form.append("turn_id", turnID);
  form.append("name", "large-smoke.txt");
  form.append("artifact_type", "file");
  const response = await fetch(`${baseURL}/v1/sessions/${encodeURIComponent(sessionID)}/artifacts/upload`, {
    method: "POST",
    body: form
  });
  if (!response.ok) {
    throw new Error(`artifact upload failed: ${response.status} ${await response.text()}`);
  }
  return response.json();
}

function turnIDFromEvents(response) {
  for (const event of response.events || []) {
    if (event?.payload?.turn_id) return event.payload.turn_id;
  }
  throw new Error(`turn_id not found in response: ${JSON.stringify(response)}`);
}

async function appendEvents(sessionID, events) {
  return post(`/v1/sessions/${encodeURIComponent(sessionID)}/events`, { events });
}

async function seedTrace(agentID, environmentID, index, spanCount = 2, withArtifact = false) {
  const session = await post("/v1/sessions", {
    agent_id: agentID,
    environment_id: environmentID,
    title: `Inspector smoke ${String(index).padStart(2, "0")}`
  });
  const start = await appendEvents(session.id, [{
    type: "user.message",
    payload: { content: [{ type: "text", text: `inspector smoke ${index}` }] }
  }]);
  const turnID = turnIDFromEvents(start);
  const traceID = `smoke-trace-${session.id}-${turnID}`;
  const events = [{
    type: "runtime.span_started",
    payload: {
      turn_id: turnID,
      trace_id: traceID,
      span_id: "span_root",
      span_name: "tma.interaction",
      span_kind: "interaction",
      message: "root started"
    }
  }];
  for (let span = 0; span < spanCount; span += 1) {
    const spanID = `span_child_${String(span).padStart(2, "0")}`;
    events.push({
      type: "runtime.span_started",
      payload: {
        turn_id: turnID,
        trace_id: traceID,
        span_id: spanID,
        parent_span_id: "span_root",
        span_name: `tma.smoke.span.${String(span).padStart(2, "0")}`,
        span_kind: "smoke",
        message: "span started"
      }
    });
    events.push({
      type: "runtime.span_event",
      payload: {
        turn_id: turnID,
        trace_id: traceID,
        span_id: spanID,
        parent_span_id: "span_root",
        span_name: `tma.smoke.span.${String(span).padStart(2, "0")}`,
        span_kind: "smoke",
        message: "span checkpoint"
      }
    });
    events.push({
      type: "runtime.span_ended",
      payload: {
        turn_id: turnID,
        trace_id: traceID,
        span_id: spanID,
        parent_span_id: "span_root",
        span_name: `tma.smoke.span.${String(span).padStart(2, "0")}`,
        span_kind: "smoke",
        span_status: "ok",
        message: "span ended"
      }
    });
  }
  let artifact = null;
  if (withArtifact) {
    const uploaded = await uploadArtifact(session.id, turnID);
    artifact = uploaded.artifact;
    events.push({
      type: "runtime.tool_result",
      payload: {
        turn_id: turnID,
        message: "Received truncated smoke tool result.",
        data: {
          id: "call_smoke_big",
          identifier: "default",
          api_name: "run_command",
          success: true,
          content: "preview omitted",
          context: {
            content_truncated: true,
            original_content_chars: 24000,
            visible_content_chars: 12000,
            state_truncated: true,
            original_state_bytes: 32000
          },
          artifacts: [{
            artifact_id: artifact.id,
            object_ref_id: artifact.object_ref_id,
            name: artifact.name,
            artifact_type: artifact.artifact_type,
            download_path: `/v1/sessions/${session.id}/artifacts/${artifact.id}/download`
          }]
        }
      }
    });
  }
  events.push({
    type: "runtime.span_ended",
    payload: {
      turn_id: turnID,
      trace_id: traceID,
      span_id: "span_root",
      span_name: "tma.interaction",
      span_kind: "interaction",
      span_status: "ok",
      message: "root ended"
    }
  });
  events.push({
    type: "runtime.completed",
    payload: { turn_id: turnID, message: "smoke completed" }
  });
  await appendEvents(session.id, events);
  await fetch(`${baseURL}/v1/sessions/${encodeURIComponent(session.id)}/trace?turn_id=${encodeURIComponent(turnID)}`);
  return { session, turnID, traceID, artifact };
}

class CDP {
  constructor(wsURL) {
    this.nextID = 1;
    this.pending = new Map();
    this.ws = new WebSocket(wsURL);
  }
  async open() {
    await new Promise((resolve, reject) => {
      this.ws.addEventListener("open", resolve, { once: true });
      this.ws.addEventListener("error", reject, { once: true });
    });
    this.ws.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (!message.id) return;
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(JSON.stringify(message.error)));
      else pending.resolve(message.result);
    });
  }
  send(method, params = {}) {
    const id = this.nextID++;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }
  async eval(expression) {
    const result = await this.send("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true
    });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result?.value;
  }
  close() {
    this.ws.close();
  }
}

async function waitFor(cdp, expression, label, timeout = 15000) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    last = await cdp.eval(expression);
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`timeout waiting for ${label}; last=${JSON.stringify(last)}`);
}

async function main() {
  const agent = await post("/v1/agents", {
    name: `Inspector Smoke Agent ${Date.now()}`,
    llm_provider: "fake",
    llm_model: "fake-demo"
  });
  const environment = await post("/v1/environments", {
    name: `Inspector Smoke Env ${Date.now()}`,
    config: { type: "smoke" }
  });

  const featured = await seedTrace(agent.id, environment.id, 0, 25, true);
  for (let index = 1; index < 25; index += 1) {
    await seedTrace(agent.id, environment.id, index, 1, false);
  }

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((r) => r.json());
  const page = pages[0];
  const cdp = new CDP(page.webSocketDebuggerUrl);
  await cdp.open();
  try {
    await cdp.send("Page.enable");
    await cdp.send("Runtime.enable");
    await cdp.send("Page.addScriptToEvaluateOnNewDocument", {
      source: `
        window.__tmaSmokeConsoleErrors = 0;
        window.addEventListener("error", () => { window.__tmaSmokeConsoleErrors += 1; });
        window.addEventListener("unhandledrejection", () => { window.__tmaSmokeConsoleErrors += 1; });
        const originalConsoleError = console.error.bind(console);
        console.error = (...args) => {
          window.__tmaSmokeConsoleErrors += 1;
          originalConsoleError(...args);
        };
      `
    });
    await cdp.send("Page.navigate", { url: `${baseURL}/inspector` });
    await waitFor(cdp, `Boolean(document.querySelector("#traceCatalog"))`, "Inspector root");
    await waitFor(cdp, `document.querySelectorAll("#traceCatalog .turn-item").length >= 20`, "initial trace page");
    await waitFor(cdp, `Boolean(document.querySelector("#moreTraces"))`, "trace load more button");
    const traceCountBefore = await cdp.eval(`document.querySelectorAll("#traceCatalog .turn-item").length`);
    await cdp.eval(`document.querySelector("#moreTraces").click(); true`);
    await waitFor(cdp, `document.querySelectorAll("#traceCatalog .turn-item").length > ${traceCountBefore}`, "trace load more append");

    await cdp.eval(`(() => {
      const input = document.querySelector("#session");
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
      setter.call(input, ${JSON.stringify(featured.session.id)});
      input.dispatchEvent(new Event("input", { bubbles: true }));
      document.querySelector("#filterTraces").click();
      return true;
    })()`);
    await waitFor(cdp, `document.querySelectorAll("#spanCatalog .turn-item").length >= 20`, "filtered span page");
    await waitFor(cdp, `Boolean(document.querySelector("#moreSpans"))`, "span load more button");
    const spanCountBefore = await cdp.eval(`document.querySelectorAll("#spanCatalog .turn-item").length`);
    await cdp.eval(`document.querySelector("#moreSpans").click(); true`);
    await waitFor(cdp, `document.querySelectorAll("#spanCatalog .turn-item").length > ${spanCountBefore}`, "span load more append");

    await cdp.eval(`document.querySelector("#traceCatalog .turn-item").click(); true`);
    await waitFor(cdp, `document.querySelector("#timeline") && document.querySelector("#timeline").innerText.includes("tool result preview truncated")`, "timeline truncation metadata");
    await waitFor(cdp, `Boolean(document.querySelector("#artifacts [data-preview]"))`, "artifact preview button");
    await cdp.eval(`document.querySelector("#artifacts [data-preview]").click(); true`);
    await waitFor(cdp, `document.querySelector("#artifactPreview") && document.querySelector("#artifactPreview").innerText.includes("preview truncated to 10240")`, "artifact preview truncation metadata");
    const consoleErrors = await cdp.eval(`window.__tmaSmokeConsoleErrors || 0`);
    if (consoleErrors) throw new Error(`browser console errors observed: ${consoleErrors}`);
  } finally {
    cdp.close();
  }
}

main().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exit(1);
});
NODE

echo "Inspector browser smoke passed"
