package tools

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	mcppkg "tiggy-manage-agent/internal/mcp"
)

const defaultGitLabMCPImage = "mcp/gitlab@sha256:a1b8571a210a3c8b17b288498d287cd1c3512c10519330ea71ca48e559e78917"

func TestGitLabDockerMCPCompatibility(t *testing.T) {
	if os.Getenv("TMA_RUN_GITLAB_DOCKER_MCP") != "1" {
		t.Skip("set TMA_RUN_GITLAB_DOCKER_MCP=1 to run the pinned GitLab Docker MCP")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required for GitLab MCP compatibility testing")
	}

	image := os.Getenv("TMA_GITLAB_MCP_IMAGE")
	if image == "" {
		image = defaultGitLabMCPImage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	runtime, err := LoadMCPRuntime(ctx, mcppkg.ServerConfig{
		Identifier: "gitlab",
		Command:    "docker",
		Args: []string{
			"run", "--rm", "-i",
			"-e", "GITLAB_PERSONAL_ACCESS_TOKEN",
			"-e", "GITLAB_API_URL",
			image,
		},
		Env: map[string]mcppkg.EnvValue{
			"GITLAB_PERSONAL_ACCESS_TOKEN": mcppkg.LiteralEnv("compatibility-probe"),
			"GITLAB_API_URL":               mcppkg.LiteralEnv("https://gitlab.com/api/v4"),
		},
		IncludeTools: []string{"search_repositories", "get_file_contents"},
		Transport:    mcppkg.TransportStdio,
		StdioFraming: mcppkg.StdioFramingJSONLines,
	})
	if err != nil {
		t.Fatalf("load GitLab Docker MCP: %v", err)
	}

	manifest := runtime.Manifest()
	if len(manifest.API) != 2 {
		t.Fatalf("expected two read-only GitLab APIs after filtering, got %#v", manifest.API)
	}
	for _, name := range []string{"search_repositories", "get_file_contents"} {
		if !manifestHasAPI(manifest, name, ToolRiskWrite) {
			t.Fatalf("expected conservatively approved GitLab API %q, got %#v", name, manifest.API)
		}
	}
	for _, api := range manifest.API {
		if api.ApprovalPolicy != ApprovalPolicyAlways || api.ApprovalReason != InterventionReasonExternalWrite {
			t.Fatalf("GitLab MCP tool without read annotations must require approval: %#v", api)
		}
	}

	t.Logf("image=%s server=%s protocol=%v tools=%d", image, manifest.Meta.Title, manifest.Metadata["mcp_protocol_version"], len(manifest.API))
}
