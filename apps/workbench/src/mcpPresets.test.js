import assert from "node:assert/strict";
import test from "node:test";

import {
  GITLAB_DOCKER_MCP_IMAGE,
  GITLAB_DOCKER_READ_TOOLS,
  gitLabDockerMCPDraft
} from "./mcpPresets.js";

test("GitLab Docker preset pins the image and exposes only read tools", () => {
  const draft = gitLabDockerMCPDraft();
  const config = JSON.parse(draft.config);

  assert.equal(draft.identifier, "gitlab");
  assert.equal(config.command, "docker");
  assert.equal(config.transport, "stdio");
  assert.equal(config.stdio_framing, "json_lines");
  assert.equal(config.args.at(-1), GITLAB_DOCKER_MCP_IMAGE);
  assert.match(GITLAB_DOCKER_MCP_IMAGE, /^mcp\/gitlab@sha256:[a-f0-9]{64}$/);
  assert.deepEqual(config.include_tools, [...GITLAB_DOCKER_READ_TOOLS]);
  assert.deepEqual(config.env.GITLAB_PERSONAL_ACCESS_TOKEN, {
    secret_ref: "env:TMA_GITLAB_PERSONAL_ACCESS_TOKEN"
  });
  assert.equal(config.env.GITLAB_API_URL, "https://gitlab.com/api/v4");
});

test("GitLab Docker preset returns an isolated editable draft", () => {
  const first = gitLabDockerMCPDraft();
  const second = gitLabDockerMCPDraft();

  first.identifier = "changed";
  assert.equal(second.identifier, "gitlab");
});
