import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  biographyScopedStorageKey,
  completeBiographyOIDCCallbackFromURL,
  currentBiographyAccessToken,
  currentBiographyUser,
  logoutBiography,
  startBiographyOIDCLogin,
  withBiographyAccessTokenForWebSocket,
} from "./auth";

const storage = new Map<string, unknown>();

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json" } });
}

function installUniStorage() {
  vi.stubGlobal("uni", {
    getStorageSync: vi.fn((key: string) => storage.get(key) || ""),
    setStorageSync: vi.fn((key: string, value: unknown) => storage.set(key, value)),
    removeStorageSync: vi.fn((key: string) => storage.delete(key)),
  });
}

function installWindow(search = "", hash = "") {
  vi.stubGlobal("window", {
    location: {
      origin: "https://app.example",
      pathname: "/pages/login/index",
      search,
      hash,
    },
    history: {
      replaceState: vi.fn(),
    },
  });
}

describe("biography OIDC auth", () => {
  beforeEach(() => {
    storage.clear();
    installUniStorage();
    installWindow();
    vi.stubEnv("VITE_BIOGRAPHY_AUTH_BASE_URL", "https://bio.example");
    vi.stubEnv("VITE_BIOGRAPHY_AUTH_REDIRECT_URL", "https://app.example/pages/login/index");
    vi.stubEnv("VITE_BIOGRAPHY_AUTH_LOGIN_URL", "");
  });

  it("builds a discovery-based authorization code PKCE URL", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "https://bio.example/v1/auth/config") {
        return jsonResponse({
          enabled: true,
          mode: "oidc",
          oidc: {
            issuer: "https://issuer.example",
            audience: "biography-mobile",
            client_id: "biography-app",
            scopes: ["openid", "profile"],
          },
        });
      }
      if (url === "https://issuer.example/.well-known/openid-configuration") {
        return jsonResponse({
          authorization_endpoint: "https://issuer.example/authorize",
          token_endpoint: "https://issuer.example/token",
        });
      }
      return jsonResponse({ error: "not found" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock);

    const loginURL = await startBiographyOIDCLogin();
    const parsed = new URL(loginURL);

    expect(parsed.origin + parsed.pathname).toBe("https://issuer.example/authorize");
    expect(parsed.searchParams.get("response_type")).toBe("code");
    expect(parsed.searchParams.get("client_id")).toBe("biography-app");
    expect(parsed.searchParams.get("redirect_uri")).toBe("https://app.example/pages/login/index");
    expect(parsed.searchParams.get("scope")).toBe("openid profile");
    expect(parsed.searchParams.get("code_challenge_method")).toBe("S256");
    expect(parsed.searchParams.get("state")).toBeTruthy();
    expect(parsed.searchParams.get("code_challenge")).toBeTruthy();
    expect(String(storage.get("tma.biography.auth.oidc_state") || "")).toBe(parsed.searchParams.get("state"));
    expect(String(storage.get("tma.biography.auth.oidc_code_verifier") || "")).toBeTruthy();
  });

  it("exchanges an OIDC callback code, saves the user, and cleans callback parameters", async () => {
    storage.set("tma.biography.auth.oidc_state", "state-1");
    storage.set("tma.biography.auth.oidc_code_verifier", "verifier-1");
    storage.set("tma.biography.auth.oidc_redirect_uri", "https://app.example/pages/login/index");
    installWindow("?code=code-1&state=state-1&foo=bar");

    const replaceState = vi.mocked(window.history.replaceState);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "https://bio.example/v1/auth/config") {
        return jsonResponse({
          enabled: true,
          mode: "oidc",
          oidc: {
            issuer: "https://issuer.example",
            audience: "biography-mobile",
            client_id: "biography-app",
          },
        });
      }
      if (url === "https://issuer.example/.well-known/openid-configuration") {
        return jsonResponse({
          authorization_endpoint: "https://issuer.example/authorize",
          token_endpoint: "https://issuer.example/token",
        });
      }
      if (url === "https://issuer.example/token") {
        const body = new URLSearchParams(String(init?.body || ""));
        expect(body.get("grant_type")).toBe("authorization_code");
        expect(body.get("client_id")).toBe("biography-app");
        expect(body.get("redirect_uri")).toBe("https://app.example/pages/login/index");
        expect(body.get("code")).toBe("code-1");
        expect(body.get("code_verifier")).toBe("verifier-1");
        return jsonResponse({ id_token: "header.payload.signature", token_type: "Bearer" });
      }
      if (url === "https://bio.example/v1/auth/me") {
        expect(new Headers(init?.headers).get("authorization")).toBe("Bearer header.payload.signature");
        return jsonResponse({
          authenticated: true,
          user: { id: "usr_a", subject: "user-a", display_name: "王老师" },
        });
      }
      return jsonResponse({ error: "not found" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock);

    const user = await completeBiographyOIDCCallbackFromURL();

    expect(user).toEqual({ id: "usr_a", subject: "user-a", display_name: "王老师" });
    expect(currentBiographyAccessToken()).toBe("header.payload.signature");
    expect(currentBiographyUser()).toEqual(user);
    expect(storage.has("tma.biography.auth.oidc_state")).toBe(false);
    expect(storage.has("tma.biography.auth.oidc_code_verifier")).toBe(false);
    expect(replaceState).toHaveBeenCalledWith({}, expect.any(String), "/pages/login/index?foo=bar");
  });

  it("scopes local keys by OIDC user and clears credentials on logout", () => {
    storage.set("tma.biography.auth.oidc_token", "header.payload.signature");
    storage.set("tma.biography.auth.user", { id: "usr_a", subject: "user-a" });
    storage.set("tma.biography.resume_token", "resume-secret");

    expect(biographyScopedStorageKey("last_interview_session")).toBe("tma.biography.usr_a.last_interview_session");
    expect(withBiographyAccessTokenForWebSocket("wss://voice.example/v1/voice/session"))
      .toBe("wss://voice.example/v1/voice/session?access_token=header.payload.signature");

    logoutBiography();

    expect(currentBiographyAccessToken()).toBe("");
    expect(currentBiographyUser()).toBeNull();
    expect(storage.has("tma.biography.resume_token")).toBe(false);
    expect(biographyScopedStorageKey("last_interview_session")).toBe("tma.biography.anonymous.last_interview_session");
  });
});
