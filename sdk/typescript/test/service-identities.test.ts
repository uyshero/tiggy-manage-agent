import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("ServiceIdentitiesService", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("manages identities and one-time credentials with escaped identifiers", async () => {
    const requests: Array<{ method: string; url: string; body: unknown }> = [];
    server = await startServer(async (request, response) => {
      const body = await readBody(request);
      requests.push({ method: request.method ?? "", url: request.url ?? "", body: body.length === 0 ? undefined : JSON.parse(body.toString()) });
      if (request.method === "DELETE") {
        response.writeHead(204);
        response.end();
      } else if (request.url === "/v2/service-identities/scopes") {
        json(response, 200, { scopes: ["agents:read", "agents:write"] });
      } else if (request.url === "/v2/service-identities" && request.method === "GET") {
        json(response, 200, { service_identities: [] });
      } else if (request.url?.endsWith("/credentials") && request.method === "GET") {
        json(response, 200, { credentials: [] });
      } else if (request.url?.endsWith("/credentials") && request.method === "POST") {
        json(response, 201, { credential: { id: "cred/1", workspace_id: "wksp_1", service_identity_id: "svc/1", name: "deployment", token_prefix: "tma_svc_abcd", status: "active", created_by: "admin", created_at: "2026-07-31T00:00:00Z" }, token: "tma_svc_locator.secret" });
      } else {
        json(response, request.method === "POST" ? 201 : 200, { id: "svc/1", workspace_id: "wksp_1", kind: "application", name: "knowledge", role: "member", scopes: ["agents:read"], status: "active", created_by: "admin", created_at: "2026-07-31T00:00:00Z", updated_at: "2026-07-31T00:00:00Z" });
      }
    });
    const service = new TMAClient(server.baseURL).serviceIdentities;
    expect(await service.scopes()).toEqual(["agents:read", "agents:write"]);
    expect(await service.list()).toEqual([]);
    await service.create({ name: "knowledge", scopes: ["agents:read"] });
    await service.get("svc/1");
    await service.update("svc/1", { status: "disabled" });
    expect(await service.credentials("svc/1")).toEqual([]);
    const created = await service.createCredential("svc/1", { name: "deployment" });
    expect(created.token).toBe("tma_svc_locator.secret");
    await service.revokeCredential("svc/1", "cred/1");

    expect(requests).toContainEqual({ method: "GET", url: "/v2/service-identities/svc%2F1", body: undefined });
    expect(requests).toContainEqual({ method: "DELETE", url: "/v2/service-identities/svc%2F1/credentials/cred%2F1", body: undefined });
  });
});
