package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/runner"
)

func TestTypeScriptCoreSDKRealServerE2E(t *testing.T) {
	if os.Getenv("TMA_RUN_TYPESCRIPT_SDK_E2E") != "1" {
		t.Skip("set TMA_RUN_TYPESCRIPT_SDK_E2E=1 and build sdk/typescript first")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envvars.MasterKeyEnvironmentVariable, base64.StdEncoding.EncodeToString(key))
	store := newTestStore()
	server := httptest.NewServer(NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, time.Millisecond, nil), nil))
	defer server.Close()

	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(repositoryRoot, "sdk", "typescript", "test", "e2e.mjs"))
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "TMA_TYPESCRIPT_SDK_E2E_BASE_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("TypeScript SDK E2E failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"session_id"`) || !strings.Contains(string(output), `"trace_id"`) ||
		!strings.Contains(string(output), `"mcp_server_id"`) || !strings.Contains(string(output), `"skill_id"`) {
		t.Fatalf("unexpected TypeScript SDK E2E output: %s", output)
	}
}
