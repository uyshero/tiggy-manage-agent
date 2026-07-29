async function request(path, options = {}) {
  const response = await fetch(path, {
    credentials: "include",
    headers: options.body instanceof FormData ? undefined : { "Content-Type": "application/json" },
    ...options
  });
  if (response.status === 401 && path.startsWith("/v2/knowledge/")) {
    const returnTo = encodeURIComponent("/knowledge");
    window.location.assign(`/auth/login?return_to=${returnTo}`);
    throw new Error("登录已过期，正在跳转登录页");
  }
  const contentType = response.headers.get("Content-Type") || "";
  const payload = contentType.includes("application/json") ? await response.json() : null;
  if (!response.ok) {
    throw new Error(payload?.error?.message || payload?.error || response.statusText);
  }
  return payload;
}

export const listBases = () => request("/v2/knowledge/bases");
export const createBase = (body) => request("/v2/knowledge/bases", { method: "POST", body: JSON.stringify(body) });
export const deleteBase = (baseID) => request(`/v2/knowledge/bases/${encodeURIComponent(baseID)}`, { method: "DELETE" });
export const listDocuments = (baseID) => request(`/v2/knowledge/bases/${encodeURIComponent(baseID)}/documents`);
export const deleteDocument = (documentID) => request(`/v2/knowledge/documents/${encodeURIComponent(documentID)}`, { method: "DELETE" });
export const listServices = () => request("/v2/knowledge/services");
export const createService = (body) => request("/v2/knowledge/services", { method: "POST", body: JSON.stringify(body) });
export const updateService = (serviceID, body) => request(`/v2/knowledge/services/${encodeURIComponent(serviceID)}`, { method: "PATCH", body: JSON.stringify(body) });
export const deleteService = (serviceID) => request(`/v2/knowledge/services/${encodeURIComponent(serviceID)}`, { method: "DELETE" });
export const askService = (serviceID, question) => request(`/v2/knowledge/services/${encodeURIComponent(serviceID)}/ask`, { method: "POST", body: JSON.stringify({ question }) });
export const listShares = (serviceID) => request(`/v2/knowledge/services/${encodeURIComponent(serviceID)}/shares`);
export const createShare = (serviceID, expiresIn) => request(`/v2/knowledge/services/${encodeURIComponent(serviceID)}/shares`, { method: "POST", body: JSON.stringify({ expires_in: expiresIn }) });
export const revokeShare = (shareID) => request(`/v2/knowledge/shares/${encodeURIComponent(shareID)}/revoke`, { method: "POST" });
export const deleteShare = (shareID) => request(`/v2/knowledge/shares/${encodeURIComponent(shareID)}`, { method: "DELETE" });
export const getPublicShare = (token) => request(`/v2/public/knowledge-shares/${encodeURIComponent(token)}`);
export const askPublicShare = (token, question) => request(`/v2/public/knowledge-shares/${encodeURIComponent(token)}/ask`, { method: "POST", body: JSON.stringify({ question }) });

export function uploadDocument(baseID, file) {
  const form = new FormData();
  form.append("file", file);
  form.append("name", file.name);
  return request(`/v2/knowledge/bases/${encodeURIComponent(baseID)}/documents`, { method: "POST", body: form });
}
