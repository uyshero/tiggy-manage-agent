package browser

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tiggy-manage-agent/internal/capability"
)

func TestPlaywrightRunnerScriptSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	scriptPath := filepath.Join(t.TempDir(), "runner.js")
	if err := os.WriteFile(scriptPath, []byte(playwrightRunnerScript), 0o600); err != nil {
		t.Fatalf("write runner script: %v", err)
	}
	output, err := exec.Command(node, "--check", scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("runner script syntax error: %v\n%s", err, string(output))
	}
}

func TestCommandProviderTakeoverRunsHeaded(t *testing.T) {
	runner := &fakeCommandRunner{
		result: capability.CommandResult{
			Stdout: `{"browser_session_id":"browser-test","url":"https://example.com","title":"Example"}`,
		},
	}
	provider := CommandProvider{
		Runner: runner,
		Env: map[string]string{
			"TMA_BROWSER_TAKEOVER_TEST": "1",
		},
	}
	state, err := provider.Takeover(context.Background(), TakeoverRequest{
		BaseRequest: BaseRequest{
			BrowserSessionID: "browser-test",
			URL:              "https://example.com",
		},
		WaitSeconds: 0,
	})
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if state.URL != "https://example.com" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if runner.request.Env["TMA_BROWSER_HEADLESS"] != "false" {
		t.Fatalf("expected takeover to run headed, env=%#v", runner.request.Env)
	}
	var payload commandRequest
	if err := json.Unmarshal(runner.request.Stdin, &payload); err != nil {
		t.Fatalf("decode stdin: %v", err)
	}
	if payload.Action != "takeover" || payload.WaitSeconds != DefaultTakeoverWaitSeconds {
		t.Fatalf("unexpected takeover payload: %#v", payload)
	}
}

func TestCommandProviderCanRequestPersistentSession(t *testing.T) {
	runner := &fakeCommandRunner{
		result: capability.CommandResult{
			Stdout: `{"browser_session_id":"browser-test","url":"https://example.com","title":"Example","persistent":true}`,
		},
	}
	provider := CommandProvider{
		Runner:     runner,
		Persistent: true,
	}
	_, err := provider.Open(context.Background(), OpenRequest{
		BaseRequest: BaseRequest{
			BrowserSessionID: "browser-test",
			URL:              "https://example.com",
		},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var payload commandRequest
	if err := json.Unmarshal(runner.request.Stdin, &payload); err != nil {
		t.Fatalf("decode stdin: %v", err)
	}
	if !payload.Persistent {
		t.Fatalf("expected persistent browser payload: %#v", payload)
	}
}

func TestCommandProviderCloseUsesBrowserAction(t *testing.T) {
	runner := &fakeCommandRunner{
		result: capability.CommandResult{
			Stdout: `{"browser_session_id":"browser-test","text":"Browser session closed.","persistent":true}`,
		},
	}
	provider := CommandProvider{
		Runner:     runner,
		Persistent: true,
	}
	_, err := provider.Close(context.Background(), CloseRequest{
		BaseRequest: BaseRequest{BrowserSessionID: "browser-test"},
	})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	var payload commandRequest
	if err := json.Unmarshal(runner.request.Stdin, &payload); err != nil {
		t.Fatalf("decode stdin: %v", err)
	}
	if payload.Action != "close" || !payload.Persistent {
		t.Fatalf("unexpected close payload: %#v", payload)
	}
}

type fakeCommandRunner struct {
	request capability.RunCommandRequest
	result  capability.CommandResult
}

func (r *fakeCommandRunner) RunCommand(_ context.Context, request capability.RunCommandRequest) (capability.CommandResult, error) {
	r.request = request
	return r.result, nil
}

func (r *fakeCommandRunner) ExecuteCode(context.Context, capability.ExecuteCodeRequest) (capability.CommandResult, error) {
	return capability.CommandResult{}, nil
}

func (r *fakeCommandRunner) ReadFile(context.Context, capability.ReadFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (r *fakeCommandRunner) WriteFile(context.Context, capability.WriteFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (r *fakeCommandRunner) EditFile(context.Context, capability.EditFileRequest) (capability.EditFileResult, error) {
	return capability.EditFileResult{}, nil
}
