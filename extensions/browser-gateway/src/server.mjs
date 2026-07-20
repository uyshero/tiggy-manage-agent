import http from "node:http";
import { chromium } from "playwright";

import { authenticate, authorizeTmaSession } from "./auth.mjs";
import { BrowserSessionManager } from "./session-manager.mjs";

const prefix = "/v2/extensions/browser";
const port = Number(process.env.TMA_BROWSER_GATEWAY_PORT || 8090);
const host = process.env.TMA_BROWSER_GATEWAY_HOST || "0.0.0.0";
const authOptions = {
  authMode: process.env.TMA_BROWSER_AUTH_MODE || "tma",
  serviceSecret: process.env.TMA_BROWSER_GATEWAY_SERVICE_SECRET || "",
  tmaServerBaseURL: String(process.env.TMA_SERVER_BASE_URL || "http://tma-server:8080").replace(/\/$/, ""),
  trustedOrigins: String(process.env.TMA_BROWSER_TRUSTED_ORIGINS || "").split(",").map((value) => value.trim()).filter(Boolean)
};
const allowedWorkspaceID = String(process.env.TMA_BROWSER_ALLOWED_WORKSPACE_ID || "").trim();
const manager = new BrowserSessionManager({
  chromium,
  profileRoot: process.env.TMA_BROWSER_PROFILE_ROOT || "/tmp/tma-browser-gateway",
  executablePath: process.env.TMA_BROWSER_EXECUTABLE_PATH || "",
  maxSessionsPerWorkspace: process.env.TMA_BROWSER_MAX_SESSIONS_PER_WORKSPACE,
  idleTTLMS: Number(process.env.TMA_BROWSER_IDLE_TTL_SECONDS || 300) * 1000
});

function json(res, statusCode, value) {
  const body = Buffer.from(JSON.stringify(value));
  res.writeHead(statusCode, {
    "content-type": "application/json; charset=utf-8",
    "content-length": body.length,
    "cache-control": "no-store",
    "x-content-type-options": "nosniff"
  });
  res.end(body);
}

function errorResponse(res, error) {
  const statusCode = Number(error?.statusCode) || 500;
  json(res, statusCode, {
    error: statusCode >= 500 ? "browser gateway request failed" : String(error?.message || "request failed"),
    code: error?.code || (statusCode === 404 ? "not_found" : statusCode === 403 ? "forbidden" : "browser_gateway_error")
  });
}

async function readBody(req, limit = 1024 * 1024) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limit) throw Object.assign(new Error("request body is too large"), { statusCode: 413 });
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

function parseJSON(rawBody) {
  if (!rawBody.length) return {};
  try {
    return JSON.parse(rawBody.toString("utf8"));
  } catch {
    throw Object.assign(new Error("request body must be valid JSON"), { statusCode: 400 });
  }
}

function route(pathname) {
  if (pathname === `${prefix}/health`) return { name: "health" };
  if (pathname === `${prefix}/sessions`) return { name: "sessions" };
  const match = pathname.match(/^\/v2\/extensions\/browser\/sessions\/([^/]+)(?:\/([^/]+))?$/);
  if (!match) return null;
  return { name: "session", id: decodeURIComponent(match[1]), action: match[2] || "read" };
}

async function requestHandler(req, res) {
  try {
    const url = new URL(req.url, `http://${req.headers.host || "localhost"}`);
    const matched = route(url.pathname);
    if (!matched) throw Object.assign(new Error("browser gateway route not found"), { statusCode: 404 });
    if (matched.name === "health") return json(res, 200, { status: "ok", sessions: manager.sessions.size });

    const rawBody = await readBody(req);
    const scope = await authenticate(req, rawBody, url.pathname, authOptions);
    if (allowedWorkspaceID && scope.workspaceID !== allowedWorkspaceID) {
      throw Object.assign(new Error("browser gateway is not assigned to this workspace"), { statusCode: 403 });
    }
    const input = parseJSON(rawBody);

    if (matched.name === "sessions") {
      if (req.method !== "POST") throw Object.assign(new Error("method not allowed"), { statusCode: 405 });
      const tmaSessionID = String(input.tma_session_id || scope.tmaSessionID || "").trim();
      await authorizeTmaSession(scope, tmaSessionID, authOptions);
      const state = await manager.create(scope, { ...input, tma_session_id: tmaSessionID });
      return json(res, 201, state);
    }

    const session = await manager.get(scope, matched.id);
    await authorizeTmaSession(scope, session.tmaSessionID, authOptions);
    if (req.method === "DELETE" && matched.action === "read") {
      return json(res, 200, await manager.close(scope, matched.id));
    }
    if (matched.action === "frame") {
      if (req.method !== "GET") throw Object.assign(new Error("method not allowed"), { statusCode: 405 });
      const frame = await manager.screenshot(scope, matched.id, {
        full_page: url.searchParams.get("full_page") === "true",
        quality: url.searchParams.get("quality")
      });
      res.writeHead(200, {
        "content-type": "image/jpeg",
        "content-length": frame.length,
        "cache-control": "no-store, max-age=0",
        "x-content-type-options": "nosniff"
      });
      return res.end(frame);
    }
    if (matched.action === "read" && req.method === "GET") return json(res, 200, await manager.observe(session));
    if (req.method !== "POST") throw Object.assign(new Error("method not allowed"), { statusCode: 405 });
    return json(res, 200, await manager.action(scope, matched.id, matched.action, input));
  } catch (error) {
    errorResponse(res, error);
  }
}

const server = http.createServer(requestHandler);
server.listen(port, host, () => {
  process.stdout.write(`TMA browser gateway listening on http://${host}:${port}${prefix}\n`);
});

async function shutdown(signal) {
  process.stdout.write(`TMA browser gateway received ${signal}; shutting down\n`);
  server.close();
  await manager.shutdown();
  process.exit(0);
}

process.on("SIGINT", () => shutdown("SIGINT"));
process.on("SIGTERM", () => shutdown("SIGTERM"));
