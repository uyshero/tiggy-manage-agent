import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, startServer, type TestServer } from "./helpers.js";

describe("capability discovery", () => {
  let server: TestServer | undefined;
  afterEach(async () => {
    await server?.close();
    server = undefined;
  });

  it("lists typed workspace capabilities", async () => {
    server = await startServer((request, response) => {
      expect(request.method).toBe("GET");
      expect(request.url).toBe("/v2/capabilities");
      json(response, 200, {
        workspace_id: "wksp_default",
        generated_at: "2026-08-03T00:00:00Z",
        capabilities: [{
          id: "model.multimodal_realtime",
          version: "v1",
          status: "available",
          health: "healthy",
          providers: ["native"],
          models: [{
            provider_id: "native",
            model: "realtime",
            capability_type: "multimodal_realtime",
            protocol: "tma_multimodal_websocket_v1",
            realtime: {
              input_formats: [{ kind: "audio", content_type: "audio/pcm", codec: "pcm_s16le" }],
              output_modalities: ["text"],
              max_input_tracks: 8,
              max_frame_bytes: 4194304,
            },
          }],
          updated_at: "2026-08-03T00:00:00Z",
        }],
      });
    });
    const client = new TMAClient(server.baseURL);
    const response = await client.capabilities.list();
    expect(response.workspace_id).toBe("wksp_default");
    expect(response.capabilities[0]?.models?.[0]?.realtime?.max_frame_bytes).toBe(4194304);
  });
});
