const nativeFetch = window.fetch.bind(window);
let refreshRequest = null;

function isAuthenticationRoute(request) {
  try {
    const url = new URL(request.url, window.location.origin);
    return url.origin === window.location.origin && url.pathname.startsWith("/auth/");
  } catch {
    return false;
  }
}

async function refreshSession() {
  if (!refreshRequest) {
    refreshRequest = nativeFetch("/auth/refresh", {
      method: "POST",
      credentials: "same-origin"
    }).finally(() => {
      refreshRequest = null;
    });
  }
  return refreshRequest;
}

window.fetch = async function authenticatedFetch(input, init) {
  const request = new Request(input, init);
  const response = await nativeFetch(request.clone());
  if (response.status !== 401 || isAuthenticationRoute(request)) return response;

  const refreshed = await refreshSession();
  if (refreshed.ok) return nativeFetch(request.clone());

  const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  window.location.assign(`/auth/login?return_to=${encodeURIComponent(returnTo)}`);
  return response;
};
