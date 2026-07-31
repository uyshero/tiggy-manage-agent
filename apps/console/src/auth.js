export function createAuthenticatedFetch(options = {}) {
  const baseURL = options.baseURL || globalThis.location?.origin || "http://localhost";
  const location = options.location || globalThis.location;
  const fetchImpl = options.fetch || globalThis.fetch;
  if (!fetchImpl) throw new TypeError("A Fetch API implementation is required");
  const nativeFetch = fetchImpl.bind(globalThis);
  let refreshRequest = null;

  const refreshSession = () => {
    if (!refreshRequest) {
      refreshRequest = nativeFetch(new URL("/auth/refresh", baseURL), {
        method: "POST",
        credentials: "same-origin"
      }).finally(() => {
        refreshRequest = null;
      });
    }
    return refreshRequest;
  };

  return async function authenticatedFetch(input, init) {
    const request = new Request(input, init);
    const retryRequest = request.clone();
    const response = await nativeFetch(request);
    if (response.status !== 401 || isAuthenticationRoute(request, baseURL)) return response;

    const refreshed = await refreshSession();
    if (refreshed.ok) return nativeFetch(retryRequest);

    if (location?.assign) {
      const returnTo = `${location.pathname || "/"}${location.search || ""}${location.hash || ""}`;
      const loginURL = new URL("/auth/login", baseURL);
      loginURL.searchParams.set("return_to", returnTo);
      location.assign(loginURL.toString());
    }
    return response;
  };
}

function isAuthenticationRoute(request, baseURL) {
  try {
    const url = new URL(request.url, baseURL);
    return url.pathname.startsWith("/auth/");
  } catch {
    return false;
  }
}
