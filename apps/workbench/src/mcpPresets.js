export const GITLAB_DOCKER_MCP_IMAGE = "mcp/gitlab@sha256:a1b8571a210a3c8b17b288498d287cd1c3512c10519330ea71ca48e559e78917";

export const GITLAB_DOCKER_READ_TOOLS = Object.freeze([
  "search_repositories",
  "get_file_contents"
]);

export function gitLabDockerMCPDraft() {
  return {
    identifier: "gitlab",
    name: "GitLab (Docker)",
    description: "通过固定版本的 Docker MCP 只读访问 GitLab 仓库。",
    config: JSON.stringify({
      transport: "stdio",
      command: "docker",
      args: [
        "run",
        "--rm",
        "-i",
        "-e",
        "GITLAB_PERSONAL_ACCESS_TOKEN",
        "-e",
        "GITLAB_API_URL",
        GITLAB_DOCKER_MCP_IMAGE
      ],
      env: {
        GITLAB_PERSONAL_ACCESS_TOKEN: { secret_ref: "env:TMA_GITLAB_PERSONAL_ACCESS_TOKEN" },
        GITLAB_API_URL: "https://gitlab.com/api/v4"
      },
      stdio_framing: "json_lines",
      include_tools: GITLAB_DOCKER_READ_TOOLS,
      runtime: {
        timeout_seconds: 60,
        max_concurrency: 2,
        failure_threshold: 3,
        cooldown_seconds: 30
      },
      title: "GitLab",
      description: "Search repositories and read repository files through GitLab MCP."
    }, null, 2)
  };
}
