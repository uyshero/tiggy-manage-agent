package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	browsercap "tiggy-manage-agent/internal/browser"
)

func TestBrowserRuntimeExecutesProviderOpen(t *testing.T) {
	provider := &fakeBrowserProvider{
		state: browsercap.PageState{
			BrowserSessionID: "sesn_000001",
			URL:              "https://example.com",
			Title:            "Example",
			Text:             "Example Domain",
			Elements: []browsercap.Element{{
				Ref:      "e1",
				Role:     "a",
				Text:     "More information",
				Selector: "a",
			}},
		},
	}
	result, err := (RegistryExecutor{Registry: NewRegistry(BrowserRuntime{Provider: provider})}).Execute(context.Background(), Call{
		ID:         "call_browser",
		Identifier: NamespaceBrowser,
		APIName:    "open",
		Arguments:  json.RawMessage(`{"url":"https://example.com","browser_session_id":"sesn_000001"}`),
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute browser tool: %v", err)
	}
	if provider.open.URL != "https://example.com" || provider.open.Meta.SessionID != "sesn_000001" {
		t.Fatalf("unexpected provider request: %#v", provider.open)
	}
	if result.Identifier != NamespaceBrowser || result.APIName != "open" || !strings.Contains(result.Content, "Title: Example") {
		t.Fatalf("unexpected browser result: %#v", result)
	}
	var state browsercap.PageState
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatalf("decode browser state: %v", err)
	}
	if state.URL != "https://example.com" || len(state.Elements) != 1 {
		t.Fatalf("unexpected browser state: %#v", state)
	}
}

func TestBrowserRuntimeExportsScreenshotPath(t *testing.T) {
	provider := &fakeBrowserProvider{
		state: browsercap.PageState{
			URL:            "https://example.com",
			ScreenshotPath: "/mnt/data/browser/sesn_000001/screenshot.png",
		},
	}
	result, err := (RegistryExecutor{Registry: NewRegistry(BrowserRuntime{Provider: provider})}).Execute(context.Background(), Call{
		ID:         "call_screenshot",
		Identifier: NamespaceBrowser,
		APIName:    "screenshot",
		Arguments:  json.RawMessage(`{"url":"https://example.com"}`),
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
	})
	if err != nil {
		t.Fatalf("execute screenshot: %v", err)
	}
	if len(result.ExportedFiles) != 1 || result.ExportedFiles[0].Path != "/mnt/data/browser/sesn_000001/screenshot.png" {
		t.Fatalf("expected screenshot export, got %#v", result.ExportedFiles)
	}
	if result.ExportedFiles[0].ArtifactType != "asset" || result.ExportedFiles[0].ContentType != "image/png" {
		t.Fatalf("unexpected screenshot artifact metadata: %#v", result.ExportedFiles[0])
	}
}

func TestBrowserRuntimeLocalSystemOnlyAPIs(t *testing.T) {
	manifest := BrowserRuntime{}.Manifest()
	localOnly := map[string]API{}
	for _, api := range manifest.API {
		if api.Name == "takeover" || api.Name == "close" {
			localOnly[api.Name] = api
		}
	}
	for _, name := range []string{"takeover", "close"} {
		api, ok := localOnly[name]
		if !ok {
			t.Fatalf("expected browser %s API", name)
		}
		if api.Runtime == nil || api.Runtime.Preferred != ToolRuntimeLocalSystem {
			t.Fatalf("expected local_system preferred %s runtime, got %#v", name, api.Runtime)
		}
		if len(api.Runtime.Allowed) != 1 || api.Runtime.Allowed[0] != ToolRuntimeLocalSystem {
			t.Fatalf("expected %s to allow only local_system, got %#v", name, api.Runtime.Allowed)
		}
	}
}

type fakeBrowserProvider struct {
	state browsercap.PageState
	open  browsercap.OpenRequest
}

func (p *fakeBrowserProvider) Open(_ context.Context, request browsercap.OpenRequest) (browsercap.PageState, error) {
	p.open = request
	return p.state, nil
}

func (p *fakeBrowserProvider) Read(context.Context, browsercap.ReadRequest) (browsercap.PageState, error) {
	return p.state, nil
}

func (p *fakeBrowserProvider) Click(context.Context, browsercap.ClickRequest) (browsercap.PageState, error) {
	return p.state, nil
}

func (p *fakeBrowserProvider) Type(context.Context, browsercap.TypeRequest) (browsercap.PageState, error) {
	return p.state, nil
}

func (p *fakeBrowserProvider) Screenshot(context.Context, browsercap.ScreenshotRequest) (browsercap.PageState, error) {
	return p.state, nil
}

func (p *fakeBrowserProvider) Takeover(context.Context, browsercap.TakeoverRequest) (browsercap.PageState, error) {
	return p.state, nil
}

func (p *fakeBrowserProvider) Close(context.Context, browsercap.CloseRequest) (browsercap.PageState, error) {
	return p.state, nil
}
