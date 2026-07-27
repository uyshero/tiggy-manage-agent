#!/bin/sh
set -eu

IMAGE="${TMA_GITLAB_MCP_IMAGE:-mcp/gitlab@sha256:a1b8571a210a3c8b17b288498d287cd1c3512c10519330ea71ca48e559e78917}"
GOCACHE_DIR="${GOCACHE:-$(pwd)/.gocache}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for GitLab MCP compatibility verification" >&2
  exit 1
fi

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "Pulling pinned GitLab MCP image: $IMAGE"
  docker pull "$IMAGE"
fi

echo "Verifying GitLab MCP stdio protocol and read-only tool filter"
TMA_RUN_GITLAB_DOCKER_MCP=1 \
TMA_GITLAB_MCP_IMAGE="$IMAGE" \
GOCACHE="$GOCACHE_DIR" \
go test ./internal/tools -run '^TestGitLabDockerMCPCompatibility$' -count=1 -v
