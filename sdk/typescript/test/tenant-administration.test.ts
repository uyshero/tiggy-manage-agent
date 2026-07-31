import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("TenantAdministrationService", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("owns tenant paths, response envelopes, and identifier escaping", async () => {
    const requests: Array<{ method: string; url: string; body: unknown }> = [];
    server = await startServer(async (request, response) => {
      const rawBody = await readBody(request);
      requests.push({
        method: request.method ?? "",
        url: request.url ?? "",
        body: rawBody.length === 0 ? undefined : JSON.parse(rawBody.toString()),
      });
      if (request.method === "DELETE") {
        response.writeHead(204);
        response.end();
      } else if (request.url === "/v2/administration/context") {
        json(response, 200, { authenticated: true, principal: { subject: "admin", workspace_id: "wksp/1", owner_id: "admin", roles: ["admin"], auth_type: "jwt" }, workspace_admin: true, platform_admin: true });
      } else if (request.url === "/v2/workspace/members") {
        json(response, 200, { members: [] });
      } else if (request.url === "/v2/platform/workspaces" && request.method === "GET") {
        json(response, 200, { workspaces: [] });
      } else if (request.url === "/v2/platform/workspaces" && request.method === "POST") {
        json(response, 201, { id: "wksp/1", name: "Workspace", created_at: "2026-07-30T00:00:00Z", member_count: 0 });
      } else if (request.url?.endsWith("/members") && request.method === "GET") {
        json(response, 200, { members: [] });
      } else if (request.url === "/v2/platform/admins" && request.method === "GET") {
        json(response, 200, { admins: [] });
      } else if (request.url?.startsWith("/v2/platform/admins/")) {
        json(response, 200, { subject: "platform/admin", role: "platform_admin", created_at: "2026-07-30T00:00:00Z", updated_at: "2026-07-30T00:00:00Z" });
      } else {
        json(response, 200, { workspace_id: "wksp/1", subject: "user/1", role: "admin", status: "active", created_at: "2026-07-30T00:00:00Z", updated_at: "2026-07-30T00:00:00Z" });
      }
    });
    const service = new TMAClient(server.baseURL).tenantAdministration;
    await service.context();
    expect(await service.listCurrentWorkspaceMembers()).toEqual([]);
    await service.upsertCurrentWorkspaceMember("user/1", { role: "admin", status: "active" });
    await service.deleteCurrentWorkspaceMember("user/1");
    expect(await service.listTenantWorkspaces()).toEqual([]);
    await service.createTenantWorkspace({ name: "Workspace" });
    expect(await service.listTenantWorkspaceMembers("wksp/1")).toEqual([]);
    await service.upsertTenantWorkspaceMember("wksp/1", "user/1", { role: "operator", status: "active" });
    await service.deleteTenantWorkspaceMember("wksp/1", "user/1");
    expect(await service.listPlatformAdmins()).toEqual([]);
    await service.upsertPlatformAdmin("platform/admin", { display_name: "Platform Admin" });
    await service.deletePlatformAdmin("platform/admin");

    expect(requests).toContainEqual({ method: "PUT", url: "/v2/workspace/members/user%2F1", body: { role: "admin", status: "active" } });
    expect(requests).toContainEqual({ method: "PUT", url: "/v2/platform/workspaces/wksp%2F1/members/user%2F1", body: { role: "operator", status: "active" } });
    expect(requests).toContainEqual({ method: "DELETE", url: "/v2/platform/admins/platform%2Fadmin", body: undefined });
  });
});
