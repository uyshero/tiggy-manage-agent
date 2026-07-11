package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	browsercap "tiggy-manage-agent/internal/browser"
	"tiggy-manage-agent/internal/capability"
)

const BrowserIdentifier = NamespaceBrowser

type BrowserRuntime struct {
	Provider browsercap.Provider
}

func (BrowserRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: BrowserIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Browser Tools",
			Description: "Open pages, inspect interactive elements, click, type, and capture screenshots through a browser provider.",
		},
		SystemRole: "Use browser.* tools when a task requires JavaScript rendering or page interaction. Prefer browser.read after navigation and use selectors from returned elements for click/type. Use browser.takeover only when local manual browser control is needed, such as login, MFA, CAPTCHA, or user-guided inspection. Use browser.close when a local persistent browser session is no longer needed.",
		Executors:  []string{ExecutorServer},
		API: []API{
			{
				Name:           "open",
				Namespace:      NamespaceBrowser,
				APIName:        "open",
				Description:    "Open a URL in a browser session and return page text plus interactable elements.",
				Parameters:     json.RawMessage(browserBaseParameters(`{"required":["url"]}`)),
				Capabilities:   []string{CapabilityBrowserOpen, CapabilityBrowserRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:           "read",
				Namespace:      NamespaceBrowser,
				APIName:        "read",
				Description:    "Read the current or supplied browser page and return page text plus interactable elements.",
				Parameters:     json.RawMessage(browserBaseParameters(`{}`)),
				Capabilities:   []string{CapabilityBrowserRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:              "click",
				Namespace:         NamespaceBrowser,
				APIName:           "click",
				Description:       "Click an element using a CSS selector returned by browser.read/open.",
				Parameters:        json.RawMessage(browserBaseParameters(`{"properties":{"selector":{"type":"string"},"ref":{"type":"string"}},"anyOf":[{"required":["selector"]},{"required":["ref"]}]}`)),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityBrowserRead, CapabilityBrowserInteract},
				Risk:              ToolRiskWrite,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:    ToolImplementationWorkerCapability,
			},
			{
				Name:              "type",
				Namespace:         NamespaceBrowser,
				APIName:           "type",
				Description:       "Fill text into an element using a CSS selector returned by browser.read/open.",
				Parameters:        json.RawMessage(browserBaseParameters(`{"properties":{"selector":{"type":"string"},"ref":{"type":"string"},"text":{"type":"string"},"clear":{"type":"boolean"}},"required":["text"],"anyOf":[{"required":["selector"]},{"required":["ref"]}]}`)),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityBrowserRead, CapabilityBrowserInteract},
				Risk:              ToolRiskWrite,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:    ToolImplementationWorkerCapability,
			},
			{
				Name:           "screenshot",
				Namespace:      NamespaceBrowser,
				APIName:        "screenshot",
				Description:    "Capture a screenshot of the current or supplied browser page.",
				Parameters:     json.RawMessage(browserBaseParameters(`{"properties":{"full_page":{"type":"boolean"}}}`)),
				Capabilities:   []string{CapabilityBrowserRead, CapabilityBrowserCapture},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:              "takeover",
				Namespace:         NamespaceBrowser,
				APIName:           "takeover",
				Description:       "Open a headed local browser for manual takeover, wait for the user to finish, then return the final page state.",
				Parameters:        json.RawMessage(browserBaseParameters(`{"properties":{"wait_seconds":{"type":"integer","minimum":1,"maximum":3600}}}`)),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityBrowserRead, CapabilityBrowserInteract, CapabilityBrowserTakeover},
				Risk:              ToolRiskWrite,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeLocalSystem}, Preferred: ToolRuntimeLocalSystem},
				Implementation:    ToolImplementationWorkerCapability,
			},
			{
				Name:              "close",
				Namespace:         NamespaceBrowser,
				APIName:           "close",
				Description:       "Close a local persistent browser session.",
				Parameters:        json.RawMessage(browserBaseParameters(`{}`)),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityBrowserClose},
				Risk:              ToolRiskWrite,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeLocalSystem}, Preferred: ToolRuntimeLocalSystem},
				Implementation:    ToolImplementationWorkerCapability,
			},
		},
	}
}

func (runtime BrowserRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	provider := runtime.Provider
	if provider == nil {
		provider = browserProviderFromCapability(executionContext.Provider)
	}
	if provider == nil {
		return ExecutionResult{}, fmt.Errorf("browser provider is required")
	}

	var state browsercap.PageState
	var err error
	switch normalizeAPIName(call.APIName) {
	case "open":
		var request browsercap.OpenRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Open(ctx, request)
	case "read":
		var request browsercap.ReadRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Read(ctx, request)
	case "click":
		var request browsercap.ClickRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Click(ctx, request)
	case "type":
		var request browsercap.TypeRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Type(ctx, request)
	case "screenshot":
		var request browsercap.ScreenshotRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Screenshot(ctx, request)
	case "takeover":
		var request browsercap.TakeoverRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Takeover(ctx, request)
	case "close":
		var request browsercap.CloseRequest
		if err := decodeBrowserArgs(call.Arguments, &request); err != nil {
			return ExecutionResult{}, err
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		state, err = provider.Close(ctx, request)
	default:
		return ExecutionResult{}, fmt.Errorf("unsupported browser api %q", call.APIName)
	}
	if err != nil {
		return ExecutionResult{}, err
	}
	return browserExecutionResult(call, state)
}

func browserProviderFromCapability(provider capability.Provider) browsercap.Provider {
	if provider == nil {
		return nil
	}
	if browserProvider, ok := provider.(browsercap.Provider); ok {
		return browserProvider
	}
	commandProvider := browsercap.NewCommandProvider(provider)
	if onlyboxes, ok := provider.(capability.OnlyboxesProvider); ok {
		if strings.TrimSpace(browserSandboxImage()) != "" {
			onlyboxes.Image = browserSandboxImage()
		}
		onlyboxes.ContainerScope = "browser"
		commandProvider.Runner = onlyboxes
		commandProvider.StateRoot = "/mnt/data/browser"
	}
	return commandProvider
}

func browserSandboxImage() string {
	return strings.TrimSpace(os.Getenv("TMA_BROWSER_SANDBOX_IMAGE"))
}

func decodeBrowserArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode browser arguments: %w", err)
	}
	return nil
}

func browserExecutionResult(call Call, page browsercap.PageState) (ExecutionResult, error) {
	state, err := json.Marshal(page)
	if err != nil {
		return ExecutionResult{}, err
	}
	result := ExecutionResult{
		ID:         call.ID,
		Identifier: BrowserIdentifier,
		APIName:    call.APIName,
		Content:    formatBrowserContent(page),
		State:      state,
	}
	if strings.TrimSpace(page.ScreenshotPath) != "" {
		result.ExportedFiles = []ArtifactExport{{
			Path:         page.ScreenshotPath,
			Name:         "browser-screenshot.png",
			ArtifactType: "asset",
			ContentType:  "image/png",
		}}
	}
	return result, nil
}

func formatBrowserContent(page browsercap.PageState) string {
	var builder strings.Builder
	if page.Title != "" {
		builder.WriteString("Title: ")
		builder.WriteString(page.Title)
		builder.WriteString("\n")
	}
	if page.URL != "" {
		builder.WriteString("URL: ")
		builder.WriteString(page.URL)
		builder.WriteString("\n")
	}
	if page.Text != "" {
		builder.WriteString("\n")
		builder.WriteString(page.Text)
		builder.WriteString("\n")
	}
	if len(page.Elements) > 0 {
		builder.WriteString("\nElements:\n")
		for _, element := range page.Elements {
			builder.WriteString("- ")
			builder.WriteString(element.Ref)
			if element.Role != "" {
				builder.WriteString(" ")
				builder.WriteString(element.Role)
			}
			if element.Text != "" {
				builder.WriteString(": ")
				builder.WriteString(element.Text)
			}
			if element.Selector != "" {
				builder.WriteString(" [")
				builder.WriteString(element.Selector)
				builder.WriteString("]")
			}
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func browserBaseParameters(extra string) string {
	var extraObject map[string]any
	_ = json.Unmarshal([]byte(extra), &extraObject)
	properties := map[string]any{
		"browser_session_id": map[string]any{"type": "string"},
		"url":                map[string]any{"type": "string"},
		"timeout_ms":         map[string]any{"type": "integer", "minimum": 1000, "maximum": 120000},
		"user_agent":         map[string]any{"type": "string"},
		"viewport": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"width":  map[string]any{"type": "integer", "minimum": 320, "maximum": 3840},
				"height": map[string]any{"type": "integer", "minimum": 240, "maximum": 2160},
			},
		},
	}
	if extraProperties, ok := extraObject["properties"].(map[string]any); ok {
		for key, value := range extraProperties {
			properties[key] = value
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	for key, value := range extraObject {
		if key == "properties" {
			continue
		}
		schema[key] = value
	}
	encoded, _ := json.Marshal(schema)
	return string(encoded)
}
