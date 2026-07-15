package execution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/browser"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workerselect"
)

const (
	defaultWorkerProviderPollInterval = 200 * time.Millisecond
	defaultWorkerProviderWaitTimeout  = 30 * time.Second
)

type WorkerBackedStore interface {
	workerselect.Store
	EnqueueWorkerWork(input managedagents.EnqueueWorkerWorkInput) (managedagents.WorkerWork, error)
	GetWorkerWork(id string) (managedagents.WorkerWork, error)
}

// WorkerBackedProvider bridges capability.Provider calls to standard tma.work.v1 tool_execution work.
type WorkerBackedProvider struct {
	Store             WorkerBackedStore
	WorkspaceID       string
	SessionID         string
	EnvironmentID     string
	TurnID            string
	Environment       map[string]string
	EnvironmentCipher *envvars.Cipher
	PollInterval      time.Duration
	WaitTimeout       time.Duration
}

func (p WorkerBackedProvider) HandlesManagedEnvironment() {}

func (p WorkerBackedProvider) ToolRuntime() string {
	return tools.ToolRuntimeLocalSystem
}

func (p WorkerBackedProvider) ToolCapabilities() []string {
	capabilities := capability.LocalSystemProvider{}.ToolCapabilities()
	capabilities = append(capabilities, browser.BrowserCapabilities()...)
	return capabilities
}

func (p WorkerBackedProvider) RunCommand(ctx context.Context, request capability.RunCommandRequest) (capability.CommandResult, error) {
	var result capability.CommandResult
	if err := p.executeDefaultTool(ctx, "run_command", request, &result); err != nil {
		return capability.CommandResult{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) ExecuteCode(ctx context.Context, request capability.ExecuteCodeRequest) (capability.CommandResult, error) {
	var result capability.CommandResult
	if err := p.executeDefaultTool(ctx, "execute_code", request, &result); err != nil {
		return capability.CommandResult{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) ReadFile(ctx context.Context, request capability.ReadFileRequest) (capability.FileResult, error) {
	var result capability.FileResult
	if err := p.executeDefaultTool(ctx, "read_file", request, &result); err != nil {
		return capability.FileResult{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) WriteFile(ctx context.Context, request capability.WriteFileRequest) (capability.FileResult, error) {
	var result capability.FileResult
	if err := p.executeDefaultTool(ctx, "write_file", request, &result); err != nil {
		return capability.FileResult{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) EditFile(ctx context.Context, request capability.EditFileRequest) (capability.EditFileResult, error) {
	var result capability.EditFileResult
	if err := p.executeDefaultTool(ctx, "edit_file", request, &result); err != nil {
		return capability.EditFileResult{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Open(ctx context.Context, request browser.OpenRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "open", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Read(ctx context.Context, request browser.ReadRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "read", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Click(ctx context.Context, request browser.ClickRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "click", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Type(ctx context.Context, request browser.TypeRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "type", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Screenshot(ctx context.Context, request browser.ScreenshotRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "screenshot", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Takeover(ctx context.Context, request browser.TakeoverRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "takeover", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) Close(ctx context.Context, request browser.CloseRequest) (browser.PageState, error) {
	var result browser.PageState
	if err := p.executeBrowserTool(ctx, "close", request, &result); err != nil {
		return browser.PageState{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) executeDefaultTool(ctx context.Context, api string, input any, state any) error {
	return p.executeTool(ctx, tools.NamespaceDefault, api, input, state)
}

func (p WorkerBackedProvider) executeBrowserTool(ctx context.Context, api string, input any, state any) error {
	return p.executeTool(ctx, tools.NamespaceBrowser, api, input, state)
}

func (p WorkerBackedProvider) executeTool(ctx context.Context, namespace string, api string, input any, state any) error {
	if p.Store == nil {
		return fmt.Errorf("worker-backed provider store is required")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode worker tool input: %w", err)
	}
	invocation, ok := tools.DefaultRegistry().WorkInvocation(namespace, api, tools.ToolRuntimeLocalSystem, inputJSON)
	if !ok {
		return fmt.Errorf("%s tool api %q is not registered", namespace, api)
	}
	result, err := p.executeInvocation(ctx, invocation, p.sessionID(input), p.turnID(input))
	if err != nil {
		return err
	}
	if len(result.State) == 0 {
		if result.Error != nil {
			return fmt.Errorf("worker tool execution failed: %s", result.Error.Message)
		}
		return fmt.Errorf("worker tool execution returned empty state")
	}
	if err := json.Unmarshal(result.State, state); err != nil {
		return fmt.Errorf("decode worker tool state: %w", err)
	}
	if commandResult, ok := state.(*capability.CommandResult); ok {
		commandResult.ExportedArtifacts = workerExportedArtifacts(result.ExportedFiles)
		commandResult.Artifacts = workerArtifactRefs(result.Artifacts)
		if strings.TrimSpace(result.ArtifactError) != "" {
			commandResult.ArtifactError = result.ArtifactError
		}
	}
	return nil
}

func (p WorkerBackedProvider) ExecuteWorkerTool(ctx context.Context, manifest tools.Manifest, api tools.API, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	invocation := tools.WorkInvocationFromAPI(manifest, api, tools.ToolRuntimeLocalSystem, call.Arguments)
	result, err := p.executeInvocation(ctx, invocation, p.SessionID, p.TurnID)
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	if result.ID == "" {
		result.ID = call.ID
	}
	if result.Identifier == "" {
		result.Identifier = call.Identifier
	}
	if result.APIName == "" {
		result.APIName = call.APIName
	}
	return result, nil
}

func (p WorkerBackedProvider) executeInvocation(ctx context.Context, invocation tools.WorkInvocation, sessionID string, turnID string) (tools.ExecutionResult, error) {
	if p.Store == nil {
		return tools.ExecutionResult{}, fmt.Errorf("worker-backed provider store is required")
	}
	if len(p.Environment) > 0 {
		if p.EnvironmentCipher == nil {
			return tools.ExecutionResult{}, envvars.ErrNotConfigured
		}
		envelope, err := p.EnvironmentCipher.SealMap(p.Environment, envvars.EnvelopeAssociatedData(p.workspaceID(), sessionID, turnID))
		if err != nil {
			return tools.ExecutionResult{}, fmt.Errorf("encrypt worker environment: %w", err)
		}
		invocation.EnvironmentEnvelope = envelope
	}
	workerID, err := workerselect.Selector{Store: p.Store}.SelectWorkerIDContext(ctx, workerselect.Request{
		WorkspaceID: p.workspaceID(),
		Invocation:  invocation,
	})
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	payload, err := json.Marshal(invocation)
	if err != nil {
		return tools.ExecutionResult{}, fmt.Errorf("encode worker invocation: %w", err)
	}
	work, err := managedagents.EnqueueWorkerWorkWithContext(ctx, p.Store, managedagents.EnqueueWorkerWorkInput{
		WorkspaceID:   p.workspaceID(),
		WorkerID:      workerID,
		EnvironmentID: p.EnvironmentID,
		SessionID:     sessionID,
		TurnID:        turnID,
		WorkType:      managedagents.WorkerWorkTypeToolExecution,
		Payload:       payload,
	})
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	result, err := p.waitForToolResult(ctx, work.ID)
	if err != nil {
		return tools.ExecutionResult{}, err
	}
	return result, nil
}

func (p WorkerBackedProvider) waitForToolResult(ctx context.Context, workID string) (tools.ExecutionResult, error) {
	ctx, cancel := p.waitContext(ctx)
	defer cancel()

	ticker := time.NewTicker(p.pollInterval())
	defer ticker.Stop()

	for {
		work, err := managedagents.GetWorkerWorkWithContext(ctx, p.Store, workID)
		if err != nil {
			return tools.ExecutionResult{}, err
		}
		switch work.Status {
		case managedagents.WorkerWorkStatusCompleted, managedagents.WorkerWorkStatusFailed:
			result, resultErr := decodeWorkerToolResult(work)
			if resultErr != nil {
				return tools.ExecutionResult{}, resultErr
			}
			return result, nil
		case managedagents.WorkerWorkStatusCanceled:
			return tools.ExecutionResult{}, fmt.Errorf("worker work %s was canceled", work.ID)
		}

		select {
		case <-ctx.Done():
			return tools.ExecutionResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeWorkerToolResult(work managedagents.WorkerWork) (tools.ExecutionResult, error) {
	var response struct {
		ToolResult tools.ExecutionResult `json:"tool_result"`
		Error      string                `json:"error"`
	}
	if err := json.Unmarshal(work.Result, &response); err != nil {
		return tools.ExecutionResult{}, fmt.Errorf("decode worker work result: %w", err)
	}
	if len(response.ToolResult.State) == 0 && work.ErrorMessage != "" {
		return tools.ExecutionResult{}, fmt.Errorf("worker work failed: %s", work.ErrorMessage)
	}
	if len(response.ToolResult.State) == 0 && response.Error != "" {
		return tools.ExecutionResult{}, fmt.Errorf("worker work failed: %s", response.Error)
	}
	for index := range response.ToolResult.ExportedFiles {
		if response.ToolResult.ExportedFiles[index].ContentBase64 == "" {
			continue
		}
		if base64.StdEncoding.DecodedLen(len(response.ToolResult.ExportedFiles[index].ContentBase64)) > tools.MaxTransportedArtifactBytes {
			return tools.ExecutionResult{}, fmt.Errorf("worker exported file %q exceeds transported artifact limit %d", response.ToolResult.ExportedFiles[index].Path, tools.MaxTransportedArtifactBytes)
		}
		content, err := base64.StdEncoding.DecodeString(response.ToolResult.ExportedFiles[index].ContentBase64)
		if err != nil {
			return tools.ExecutionResult{}, fmt.Errorf("decode worker exported file %q: %w", response.ToolResult.ExportedFiles[index].Path, err)
		}
		if len(content) > tools.MaxTransportedArtifactBytes {
			return tools.ExecutionResult{}, fmt.Errorf("worker exported file %q exceeds transported artifact limit %d", response.ToolResult.ExportedFiles[index].Path, tools.MaxTransportedArtifactBytes)
		}
		response.ToolResult.ExportedFiles[index].Content = content
	}
	return response.ToolResult, nil
}

func (p WorkerBackedProvider) waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	timeout := p.WaitTimeout
	if timeout <= 0 {
		timeout = defaultWorkerProviderWaitTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (p WorkerBackedProvider) pollInterval() time.Duration {
	if p.PollInterval <= 0 {
		return defaultWorkerProviderPollInterval
	}
	return p.PollInterval
}

func (p WorkerBackedProvider) workspaceID() string {
	if p.WorkspaceID == "" {
		return managedagents.DefaultWorkspaceID
	}
	return p.WorkspaceID
}

func (p WorkerBackedProvider) sessionID(input any) string {
	if p.SessionID != "" {
		return p.SessionID
	}
	if meta := requestMeta(input); meta.SessionID != "" {
		return meta.SessionID
	}
	return ""
}

func (p WorkerBackedProvider) turnID(input any) string {
	if p.TurnID != "" {
		return p.TurnID
	}
	if meta := requestMeta(input); meta.TurnID != "" {
		return meta.TurnID
	}
	return ""
}

func requestMeta(input any) capability.RequestMeta {
	switch request := input.(type) {
	case capability.RunCommandRequest:
		return request.Meta
	case capability.ExecuteCodeRequest:
		return request.Meta
	case capability.ReadFileRequest:
		return request.Meta
	case capability.WriteFileRequest:
		return request.Meta
	case capability.EditFileRequest:
		return request.Meta
	case browser.OpenRequest:
		return request.Meta
	case browser.ReadRequest:
		return request.Meta
	case browser.ClickRequest:
		return request.Meta
	case browser.TypeRequest:
		return request.Meta
	case browser.ScreenshotRequest:
		return request.Meta
	case browser.TakeoverRequest:
		return request.Meta
	case browser.CloseRequest:
		return request.Meta
	default:
		return capability.RequestMeta{}
	}
}

func workerExportedArtifacts(exports []tools.ArtifactExport) []capability.ExportArtifactFileResult {
	if len(exports) == 0 {
		return nil
	}
	files := make([]capability.ExportArtifactFileResult, 0, len(exports))
	for _, export := range exports {
		if len(export.Content) == 0 && strings.TrimSpace(export.ContentType) == "" {
			continue
		}
		files = append(files, capability.ExportArtifactFileResult{
			Path:        export.Path,
			Name:        export.Name,
			ContentType: export.ContentType,
			Content:     append([]byte(nil), export.Content...),
		})
	}
	if len(files) == 0 {
		return nil
	}
	return files
}

func workerArtifactRefs(refs []tools.ArtifactRef) []capability.ArtifactRef {
	if len(refs) == 0 {
		return nil
	}
	result := make([]capability.ArtifactRef, 0, len(refs))
	for _, ref := range refs {
		if ref.ArtifactID == "" && ref.ObjectRefID == "" && ref.DownloadPath == "" {
			continue
		}
		result = append(result, capability.ArtifactRef{
			ArtifactID:   ref.ArtifactID,
			ObjectRefID:  ref.ObjectRefID,
			Name:         ref.Name,
			ArtifactType: ref.ArtifactType,
			DownloadPath: ref.DownloadPath,
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
