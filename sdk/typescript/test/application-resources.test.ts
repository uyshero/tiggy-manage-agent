import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, startServer, type TestServer } from "./helpers.js";

describe("application resource queries", () => {
  let server: TestServer | undefined;

  afterEach(async () => {
    await server?.close();
    server = undefined;
  });

  it("encodes application identity and external reference for every owned resource", async () => {
    const expected = [
      "/v2/agents?app_id=svc%2Fapp&external_ref=agent%2Fmain",
      "/v2/environments?app_id=svc%2Fapp&external_ref=environment%2Fdefault",
      "/v2/sessions?app_id=svc%2Fapp&external_ref=session%2Fcase",
      "/v2/skills?workspace_id=wksp%2F1&app_id=svc%2Fapp&external_ref=skill%2Finterview",
      "/v2/mcp-servers?workspace_id=wksp%2F1&app_id=svc%2Fapp&external_ref=mcp%2Frepository",
    ];
    server = await startServer((request, response) => {
      expect(request.url).toBe(expected.shift());
      const key = request.url?.split("?", 1)[0];
      const responseKey = key === "/v2/mcp-servers" ? "servers" : key?.slice(4);
      json(response, 200, { [responseKey ?? "items"]: [] });
    });

    const client = new TMAClient(server.baseURL);
    await client.agents.listByApplication({ appId: "svc/app", externalRef: "agent/main" });
    await client.environments.listByApplication({ appId: "svc/app", externalRef: "environment/default" });
    await client.sessions.list({ appId: "svc/app", externalRef: "session/case" });
    await client.skills.list({ workspaceId: "wksp/1", appId: "svc/app", externalRef: "skill/interview" });
    await client.mcp.list({ workspaceId: "wksp/1", appId: "svc/app", externalRef: "mcp/repository" });
    expect(expected).toEqual([]);
  });
});
