import assert from "node:assert/strict";
import test from "node:test";

import { sessionArtifactCLI } from "./utils.js";

test("sessionArtifactCLI accepts v2 artifact download paths", () => {
  const expected = "bin/tma session artifact download --session session_1 --artifact artifact_1";
  assert.equal(sessionArtifactCLI("/v2/sessions/session_1/artifacts/artifact_1/download?inline=true"), expected);
});

test("sessionArtifactCLI rejects legacy and unrelated paths", () => {
  assert.equal(sessionArtifactCLI("/v1/sessions/session_1/artifacts/artifact_1/download"), "");
  assert.equal(sessionArtifactCLI("/v2/sessions/session_1/events"), "");
  assert.equal(sessionArtifactCLI("https://example.test/v2/sessions/session_1/artifacts/artifact_1/download"), "");
});
