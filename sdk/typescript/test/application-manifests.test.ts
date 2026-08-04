import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("application manifests", () => {
  let server: TestServer | undefined;
  afterEach(async () => {
    await server?.close();
    server = undefined;
  });

  it("publishes a typed declarative manifest", async () => {
    server = await startServer(async (request, response) => {
      expect(request.method).toBe("POST");
      expect(request.url).toBe("/v2/application-manifests/publish");
      expect(JSON.parse((await readBody(request)).toString())).toMatchObject({
        manifest: {
          schema_version: "tma.application-manifest.v1",
          revision: "1",
          environments: [{ external_ref: "environment/default" }],
          agents: [{ external_ref: "agent/main", environment_ref: "environment/default" }],
        },
      });
      json(response, 200, {
        schema_version: "tma.application-manifest.v1",
        revision: "1",
        checksum_sha256: "a".repeat(64),
        resources: [{ type: "agent", external_ref: "agent/main", resource_id: "agt_1", status: "created", version: 1 }],
      });
    });
    const client = new TMAClient(server.baseURL);
    const result = await client.applicationManifests.publish({
      manifest: {
        schema_version: "tma.application-manifest.v1",
        revision: "1",
        environments: [{ external_ref: "environment/default", name: "Default", config: {} }],
        agents: [{ external_ref: "agent/main", environment_ref: "environment/default", name: "Main", llm_model: "fake-demo", system: "Assist" }],
      },
    });
    expect(result.resources[0]?.status).toBe("created");
  });
});
