import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("event subscriptions", () => {
  let server: TestServer | undefined;
  afterEach(async () => {
    await server?.close();
    server = undefined;
  });

  it("creates an application webhook and returns its one-time secret", async () => {
    server = await startServer(async (request, response) => {
      expect(request.method).toBe("POST");
      expect(request.url).toBe("/v2/event-subscriptions");
      expect(JSON.parse((await readBody(request)).toString())).toEqual({
        app_id: "svc/app",
        name: "primary",
        endpoint_url: "https://app.example/events",
        event_types: ["run.completed", "artifact.created"],
      });
      json(response, 201, {
        subscription: {
          id: "esub_1", workspace_id: "wksp_1", app_id: "svc/app", name: "primary",
          endpoint_url: "https://app.example/events", event_types: ["artifact.created", "run.completed"],
          status: "active", secret_version: 1, created_by: "test",
          created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
        },
        secret: "whsec_once",
      });
    });
    const client = new TMAClient(server.baseURL);
    const result = await client.eventSubscriptions.create({
      app_id: "svc/app",
      name: "primary",
      endpoint_url: "https://app.example/events",
      event_types: ["run.completed", "artifact.created"],
    });
    expect(result.secret).toBe("whsec_once");
  });
});
