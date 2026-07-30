export interface BiographyAuthConfig {
  enabled: boolean;
  mode: "disabled" | "oidc" | string;
  oidc?: {
    issuer: string;
    audience: string;
    client_id?: string;
    scopes?: string[];
  };
}

export interface BiographyUser {
  id: string;
  subject: string;
  display_name?: string;
}

export interface BiographyProgressSnapshot {
  project: unknown;
  lastInterview?: {
    id: string;
    startedAt: string;
    endedAt?: string;
    durationSeconds: number;
    lastChapterTitle?: string;
    transcriptCount: number;
    todayRecordingSaved: boolean;
  };
  activeChapterTitles: string[];
  pendingConfirmation?: string;
  pendingTranscripts?: string[];
  recentQuestions?: string[];
  updatedAt: string;
}

interface OIDCDiscovery {
  authorization_endpoint: string;
  token_endpoint?: string;
}

interface OIDCTokenResponse {
  access_token?: string;
  id_token?: string;
  token_type?: string;
  error?: string;
  error_description?: string;
}

const authTokenStorageKey = "tma.biography.auth.oidc_token";
const authUserStorageKey = "tma.biography.auth.user";
const oidcStateStorageKey = "tma.biography.auth.oidc_state";
const oidcVerifierStorageKey = "tma.biography.auth.oidc_code_verifier";
const oidcRedirectStorageKey = "tma.biography.auth.oidc_redirect_uri";
const resumeTokenStorageKey = "tma.biography.resume_token";

function readStorage<T>(key: string): T | null {
  try {
    const value = uni.getStorageSync(key) as T | string | null;
    if (typeof value === "string") return value ? JSON.parse(value) as T : null;
    return value || null;
  } catch {
    return null;
  }
}

function writeStorage(key: string, value: unknown) {
  try {
    if (value) uni.setStorageSync(key, value);
    else uni.removeStorageSync(key);
  } catch {
    // 登录缓存失败不能阻断当前流程。
  }
}

function readStringStorage(key: string): string {
  try {
    return String(uni.getStorageSync(key) || "").trim();
  } catch {
    return "";
  }
}

export function biographyAuthBaseURL(): string {
  const explicit = String(import.meta.env.VITE_BIOGRAPHY_AUTH_BASE_URL || "").trim();
  if (explicit) return explicit.replace(/\/+$/u, "");
  const gateway = String(import.meta.env.VITE_BIOGRAPHY_VOICE_GATEWAY_URL || "").trim();
  if (!gateway) return "";
  try {
    const url = new URL(gateway);
    url.protocol = url.protocol === "wss:" ? "https:" : "http:";
    url.pathname = "";
    url.search = "";
    url.hash = "";
    return url.toString().replace(/\/+$/u, "");
  } catch {
    return "";
  }
}

export function currentBiographyAccessToken(): string {
  return readStringStorage(authTokenStorageKey);
}

export function currentBiographyUser(): BiographyUser | null {
  return readStorage<BiographyUser>(authUserStorageKey);
}

export function currentBiographyUserID(): string {
  return currentBiographyUser()?.id || "anonymous";
}

export function biographyScopedStorageKey(suffix: string): string {
  return `tma.biography.${currentBiographyUserID()}.${suffix}`;
}

function saveBiographyAuth(token: string, user: BiographyUser) {
  writeStorage(authTokenStorageKey, token.trim());
  writeStorage(authUserStorageKey, user);
}

export function clearBiographyAuth() {
  writeStorage(authTokenStorageKey, "");
  writeStorage(authUserStorageKey, null);
}

export function logoutBiography() {
  clearBiographyAuth();
  writeStorage(oidcStateStorageKey, "");
  writeStorage(oidcVerifierStorageKey, "");
  writeStorage(oidcRedirectStorageKey, "");
  writeStorage(resumeTokenStorageKey, "");
}

export async function fetchBiographyAuthConfig(): Promise<BiographyAuthConfig> {
  const baseURL = biographyAuthBaseURL();
  if (!baseURL) return { enabled: false, mode: "disabled" };
  try {
    const response = await fetch(`${baseURL}/v1/auth/config`);
    if (!response.ok) return { enabled: true, mode: "unavailable" };
    const payload = await response.json() as BiographyAuthConfig;
    if (typeof payload.enabled !== "boolean" || !payload.mode) return { enabled: true, mode: "unavailable" };
    return payload;
  } catch {
    return { enabled: true, mode: "unavailable" };
  }
}

export async function saveBiographyOIDCToken(token: string): Promise<BiographyUser> {
  const cleanToken = token.trim();
  if (!cleanToken) throw new Error("请先完成统一身份认证");
  writeStorage(authTokenStorageKey, cleanToken);
  const user = await fetchBiographyCurrentUser();
  if (!user) {
    clearBiographyAuth();
    throw new Error("统一身份认证已失效，请重新登录");
  }
  saveBiographyAuth(cleanToken, user);
  return user;
}

export async function startBiographyOIDCLogin(returnTo?: string): Promise<string> {
  const redirectURI = returnTo || biographyOIDCRedirectURL();
  const explicitLoginURL = biographyOIDCLoginURL(redirectURI);
  if (explicitLoginURL) return explicitLoginURL;

  const config = await fetchBiographyAuthConfig();
  const oidc = config.oidc;
  if (!config.enabled) throw new Error("自传服务尚未启用统一身份认证");
  if (!oidc?.issuer) throw new Error("统一身份认证配置暂时不可用，请稍后再试");
  if (!oidc.client_id) throw new Error("请先配置 OIDC client_id");
  const discovery = await discoverBiographyOIDC(oidc.issuer);
  const state = randomBase64URL(24);
  const verifier = randomBase64URL(64);
  const challenge = await sha256Base64URL(verifier);

  writeStorage(oidcStateStorageKey, state);
  writeStorage(oidcVerifierStorageKey, verifier);
  writeStorage(oidcRedirectStorageKey, redirectURI);

  const authURL = new URL(discovery.authorization_endpoint);
  authURL.searchParams.set("response_type", "code");
  authURL.searchParams.set("client_id", oidc.client_id);
  authURL.searchParams.set("redirect_uri", redirectURI);
  authURL.searchParams.set("scope", (oidc.scopes?.length ? oidc.scopes : ["openid", "profile", "email"]).join(" "));
  authURL.searchParams.set("state", state);
  authURL.searchParams.set("code_challenge", challenge);
  authURL.searchParams.set("code_challenge_method", "S256");
  return authURL.toString();
}

export async function completeBiographyOIDCCallbackFromURL(): Promise<BiographyUser | null> {
  const callback = readBiographyOIDCCallbackFromURL();
  if (callback.error) {
    cleanBiographyOIDCCallbackURL();
    throw new Error(callback.errorDescription || "统一身份认证已取消");
  }
  if (callback.token) {
    cleanBiographyOIDCCallbackURL();
    return saveBiographyOIDCToken(callback.token);
  }
  if (!callback.code) {
    cleanBiographyOIDCCallbackURL();
    return null;
  }
  const expectedState = readStringStorage(oidcStateStorageKey);
  if (!expectedState || callback.state !== expectedState) {
    cleanBiographyOIDCCallbackURL();
    logoutBiography();
    throw new Error("统一身份认证状态不一致，请重新登录");
  }
  const config = await fetchBiographyAuthConfig();
  const oidc = config.oidc;
  if (!oidc?.issuer || !oidc.client_id) throw new Error("OIDC 配置不完整");
  const verifier = readStringStorage(oidcVerifierStorageKey);
  const redirectURI = readStringStorage(oidcRedirectStorageKey) || biographyOIDCRedirectURL();
  if (!verifier) throw new Error("登录会话已过期，请重新登录");
  const discovery = await discoverBiographyOIDC(oidc.issuer);
  if (!discovery.token_endpoint) throw new Error("OIDC Provider 未提供 token_endpoint");

  const body = new URLSearchParams();
  body.set("grant_type", "authorization_code");
  body.set("client_id", oidc.client_id);
  body.set("redirect_uri", redirectURI);
  body.set("code", callback.code);
  body.set("code_verifier", verifier);

  const response = await fetch(discovery.token_endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });
  const payload = await response.json().catch(() => ({})) as OIDCTokenResponse;
  if (!response.ok) {
    cleanBiographyOIDCCallbackURL();
    logoutBiography();
    throw new Error(payload.error_description || payload.error || "统一身份认证换取 token 失败");
  }
  const token = chooseOIDCBearerToken(payload);
  if (!token) {
    cleanBiographyOIDCCallbackURL();
    logoutBiography();
    throw new Error("统一身份认证没有返回可用 token");
  }

  cleanBiographyOIDCCallbackURL();
  writeStorage(oidcStateStorageKey, "");
  writeStorage(oidcVerifierStorageKey, "");
  writeStorage(oidcRedirectStorageKey, "");
  return saveBiographyOIDCToken(token);
}

export async function fetchBiographyProgress(): Promise<BiographyProgressSnapshot | null> {
  const config = await fetchBiographyAuthConfig();
  if (config.enabled && !currentBiographyAccessToken()) return null;
  const response = await biographyFetch("/v1/progress", { method: "GET" }, config.enabled);
  if (!response.ok) {
    if (response.status === 401) clearBiographyAuth();
    return null;
  }
  return await response.json() as BiographyProgressSnapshot;
}

export async function ensureBiographyAuthenticated(): Promise<boolean> {
  const config = await fetchBiographyAuthConfig();
  if (!config.enabled) return true;
  if (!currentBiographyAccessToken()) return false;
  const user = await fetchBiographyCurrentUser();
  return Boolean(user);
}

export async function fetchBiographyCurrentUser(): Promise<BiographyUser | null> {
  const response = await biographyFetch("/v1/auth/me", { method: "GET" }, true);
  if (!response.ok) {
    if (response.status === 401) clearBiographyAuth();
    return null;
  }
  const payload = await response.json() as { authenticated?: boolean; user?: BiographyUser };
  if (!payload.authenticated || !payload.user?.id) {
    clearBiographyAuth();
    return null;
  }
  saveBiographyAuth(currentBiographyAccessToken(), payload.user);
  return payload.user;
}

export function withBiographyAccessTokenForWebSocket(rawURL: string): string {
  const token = currentBiographyAccessToken();
  if (!token) return rawURL;
  try {
    const url = new URL(rawURL);
    url.searchParams.set("access_token", token);
    return url.toString();
  } catch {
    return rawURL;
  }
}

export function biographyOIDCLoginURL(returnTo?: string): string {
  const explicit = String(import.meta.env.VITE_BIOGRAPHY_AUTH_LOGIN_URL || "").trim();
  if (!explicit) return "";
  try {
    const url = new URL(explicit);
    if (returnTo) url.searchParams.set("return_to", returnTo);
    return url.toString();
  } catch {
    return explicit;
  }
}

export function biographyOIDCRedirectURL(): string {
  const configured = String(import.meta.env.VITE_BIOGRAPHY_AUTH_REDIRECT_URL || "").trim();
  if (configured) return configured;
  // #ifdef H5
  return `${window.location.origin}${window.location.pathname}`;
  // #endif
  return "tma-biography://auth/callback";
}

export function biographyDevTokenInputEnabled(): boolean {
  return import.meta.env.VITE_BIOGRAPHY_AUTH_DEV_TOKEN_INPUT === "true";
}

async function biographyFetch(path: string, init: RequestInit, authenticated: boolean): Promise<Response> {
  const baseURL = biographyAuthBaseURL();
  if (!baseURL) throw new Error("请先配置自传服务地址");
  const headers = new Headers(init.headers);
  headers.set("Content-Type", "application/json");
  if (authenticated) {
    const token = currentBiographyAccessToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);
  }
  return fetch(`${baseURL}${path}`, { ...init, headers });
}

async function discoverBiographyOIDC(issuer: string): Promise<OIDCDiscovery> {
  const base = issuer.replace(/\/+$/u, "");
  const response = await fetch(`${base}/.well-known/openid-configuration`);
  if (!response.ok) throw new Error("无法读取统一身份认证配置");
  const discovery = await response.json() as OIDCDiscovery;
  if (!discovery.authorization_endpoint) throw new Error("OIDC Provider 未提供 authorization_endpoint");
  return discovery;
}

function chooseOIDCBearerToken(payload: OIDCTokenResponse): string {
  const accessToken = String(payload.access_token || "").trim();
  const idToken = String(payload.id_token || "").trim();
  if (accessToken.split(".").length === 3) return accessToken;
  if (idToken) return idToken;
  return accessToken;
}

function readBiographyOIDCCallbackFromURL(): { code: string; state: string; token: string; error: string; errorDescription: string } {
  // #ifdef H5
  const search = new URLSearchParams(window.location.search);
  const hash = new URLSearchParams(window.location.hash.replace(/^#/u, ""));
  return {
    code: search.get("code") || hash.get("code") || "",
    state: search.get("state") || hash.get("state") || "",
    token: search.get("oidc_token") || search.get("access_token") || search.get("id_token") ||
      hash.get("oidc_token") || hash.get("access_token") || hash.get("id_token") || "",
    error: search.get("error") || hash.get("error") || "",
    errorDescription: search.get("error_description") || hash.get("error_description") || "",
  };
  // #endif
  return { code: "", state: "", token: "", error: "", errorDescription: "" };
}

function cleanBiographyOIDCCallbackURL() {
  // #ifdef H5
  const search = new URLSearchParams(window.location.search);
  for (const key of ["code", "state", "error", "error_description", "oidc_token", "access_token", "id_token", "session_state", "iss"]) {
    search.delete(key);
  }
  const cleanPath = `${window.location.pathname}${search.toString() ? `?${search.toString()}` : ""}`;
  const title = typeof document === "undefined" ? "" : document.title;
  window.history.replaceState({}, title, cleanPath);
  // #endif
}

function randomBase64URL(byteLength: number): string {
  const bytes = new Uint8Array(byteLength);
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256);
  }
  return base64URLFromBytes(bytes);
}

async function sha256Base64URL(value: string): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error("当前环境不支持安全登录，请使用 App 原生 OIDC 登录");
  const digest = await globalThis.crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return base64URLFromBytes(new Uint8Array(digest));
}

function base64URLFromBytes(bytes: Uint8Array): string {
  let binary = "";
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return btoa(binary).replace(/\+/gu, "-").replace(/\//gu, "_").replace(/=+$/u, "");
}
