import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("artifact exchanges", () => {
  let server: TestServer | undefined;

  afterEach(async () => {
    await server?.close();
    server = undefined;
  });

  it("creates and consumes one-time import and export URLs", async () => {
    const requests: string[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      if (request.method === "POST") {
        expect(JSON.parse((await readBody(request)).toString())).toMatchObject({ session_id: "sesn/1", filename: "report.txt" });
        json(response, 201, {
          exchange: {
            id: "aex_1", workspace_id: "wksp_1", owner_id: "app", direction: "import", status: "pending",
            filename: "report.txt", artifact_type: "file", visibility: "session", max_size_bytes: 11,
            expires_at: "2026-08-01T00:00:00Z", created_by: "app",
            created_at: "2026-07-31T00:00:00Z", updated_at: "2026-07-31T00:00:00Z",
          },
          content_url: "/v2/artifact-exchanges/aex_1/content?workspace_id=wksp_1&token=secret",
        });
        return;
      }
      if (request.method === "PUT") {
        expect(request.headers["content-type"]).toBe("text/plain");
        expect((await readBody(request)).toString()).toBe("report body");
        json(response, 201, { exchange: { id: "aex_1", status: "completed" }, object_ref: { id: "obj_1" }, artifact: { id: "art_1" } });
        return;
      }
      response.writeHead(200, { "content-type": "text/plain" });
      response.end("download body");
    });

    const client = new TMAClient(server.baseURL);
    const grant = await client.artifactExchanges.createImport({ session_id: "sesn/1", filename: "report.txt", content_type: "text/plain" });
    const imported = await client.artifactExchanges.upload(grant, "report body", "text/plain");
    expect(imported.artifact.id).toBe("art_1");
    const download = await client.artifactExchanges.download({ ...grant, content_url: "/v2/artifact-exchanges/aex_2/content?workspace_id=wksp_1&token=download" });
    expect(await download.text()).toBe("download body");
    expect(requests).toEqual([
      "POST /v2/artifact-exchanges/imports",
      "PUT /v2/artifact-exchanges/aex_1/content?workspace_id=wksp_1&token=secret",
      "GET /v2/artifact-exchanges/aex_2/content?workspace_id=wksp_1&token=download",
    ]);
  });
});
