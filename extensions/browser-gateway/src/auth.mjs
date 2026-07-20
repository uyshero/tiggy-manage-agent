import { createHash, createHmac, timingSafeEqual } from "node:crypto";

function header(req, name) {
  return String(req.headers[name] || "").trim();
}

function equalSecret(left, right) {
  const a = Buffer.from(String(left || ""));
  const b = Buffer.from(String(right || ""));
  return a.length === b.length && timingSafeEqual(a, b);
}

export function serviceSignature({ secret, timestamp, method, pathname, workspaceID, sessionID, body }) {
  const bodyHash = createHash("sha256").update(body || Buffer.alloc(0)).digest("hex");
  const canonical = [timestamp, method.toUpperCase(), pathname, workspaceID, sessionID, bodyHash].join("\n");
  return createHmac("sha256", secret).update(canonical).digest("hex");
}

function serviceScope(req, rawBody, pathname, options) {
  const signature = header(req, "x-tma-browser-signature");
  if (!signature) return null;
  if (!options.serviceSecret) throw Object.assign(new Error("browser gateway service authentication is not configured"), { statusCode: 503 });

  const timestamp = header(req, "x-tma-browser-timestamp");
  const workspaceID = header(req, "x-tma-workspace-id");
  const sessionID = header(req, "x-tma-session-id");
  const seconds = Number(timestamp);
  if (!workspaceID || !sessionID || !Number.isFinite(seconds) || Math.abs(Date.now() / 1000 - seconds) > 60) {
    throw Object.assign(new Error("invalid browser gateway service authentication context"), { statusCode: 401 });
  }
  const expected = serviceSignature({
    secret: options.serviceSecret,
    timestamp,
    method: req.method,
    pathname,
    workspaceID,
    sessionID,
    body: rawBody
  });
  if (!equalSecret(signature, expected)) {
    throw Object.assign(new Error("invalid browser gateway service signature"), { statusCode: 401 });
  }
  return {
    kind: "service",
    workspaceID,
    ownerID: `session:${sessionID}`,
    tmaSessionID: sessionID,
    forwardedHeaders: {}
  };
}

function forwardedAuthHeaders(req) {
  const headers = {};
  for (const name of ["authorization", "cookie", "origin", "user-agent"]) {
    const value = header(req, name);
    if (value) headers[name] = value;
  }
  return headers;
}

function principalValue(payload, names) {
  for (const name of names) {
    const value = payload?.[name] ?? payload?.principal?.[name] ?? payload?.data?.[name];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

async function userScope(req, options) {
  if (options.authMode === "disabled") {
    return {
      kind: "user",
      workspaceID: header(req, "x-tma-dev-workspace-id") || "wksp_default",
      ownerID: header(req, "x-tma-dev-owner-id") || "browser-dev-user",
      forwardedHeaders: forwardedAuthHeaders(req)
    };
  }
  if (!options.tmaServerBaseURL) {
    throw Object.assign(new Error("TMA server base URL is required for browser user authentication"), { statusCode: 503 });
  }
  if (!["GET", "HEAD", "OPTIONS"].includes(String(req.method || "GET").toUpperCase())) {
    const origin = header(req, "origin");
    const forwardedHost = header(req, "x-forwarded-host") || header(req, "host");
    const forwardedProto = header(req, "x-forwarded-proto") || "https";
    const sameOrigin = forwardedHost ? `${forwardedProto}://${forwardedHost}` : "";
    const allowedOrigins = new Set([sameOrigin, ...(options.trustedOrigins || [])].filter(Boolean));
    if (!origin || !allowedOrigins.has(origin)) {
      throw Object.assign(new Error("browser gateway origin check failed"), { statusCode: 403 });
    }
  }
  const forwardedHeaders = forwardedAuthHeaders(req);
  const response = await fetch(`${options.tmaServerBaseURL}/v2/auth/me`, { headers: forwardedHeaders });
  if (!response.ok) throw Object.assign(new Error("browser gateway authentication failed"), { statusCode: 401 });
  const principal = await response.json();
  const workspaceID = principalValue(principal, ["workspace_id", "workspaceId"]);
  const ownerID = principalValue(principal, ["owner_id", "ownerId", "subject", "sub"]);
  if (!workspaceID || !ownerID) {
    throw Object.assign(new Error("authenticated principal is missing workspace or owner scope"), { statusCode: 403 });
  }
  return { kind: "user", workspaceID, ownerID, forwardedHeaders };
}

export async function authenticate(req, rawBody, pathname, options) {
  return serviceScope(req, rawBody, pathname, options) || await userScope(req, options);
}

export async function authorizeTmaSession(scope, tmaSessionID, options) {
  if (!tmaSessionID || scope.kind === "service" || options.authMode === "disabled") return;
  const response = await fetch(`${options.tmaServerBaseURL}/v2/sessions/${encodeURIComponent(tmaSessionID)}`, {
    headers: scope.forwardedHeaders
  });
  if (!response.ok) {
    throw Object.assign(new Error("TMA session is not accessible to the current principal"), { statusCode: response.status === 404 ? 404 : 403 });
  }
}
