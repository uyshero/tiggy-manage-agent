const basePath = "/v2/extensions/browser";

export function browserSessionPath(sessionID, action = "") {
  const base = `${basePath}/sessions/${encodeURIComponent(String(sessionID || ""))}`;
  return action ? `${base}/${action}` : base;
}

export function createBrowserAPI(http) {
  return Object.freeze({
    create(input) {
      return http.request(`${basePath}/sessions`, { method: "POST", body: input });
    },
    read(sessionID) {
      return http.request(browserSessionPath(sessionID));
    },
    action(sessionID, action, input = {}) {
      return http.request(browserSessionPath(sessionID, action), { method: "POST", body: input });
    },
    close(sessionID) {
      return http.request(browserSessionPath(sessionID), { method: "DELETE" });
    },
    frameURL(sessionID, revision = Date.now()) {
      return `${browserSessionPath(sessionID, "frame")}?v=${encodeURIComponent(revision)}`;
    }
  });
}
