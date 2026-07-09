package execution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
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
	Store         WorkerBackedStore
	WorkspaceID   string
	SessionID     string
	EnvironmentID string
	TurnID        string
	PollInterval  time.Duration
	WaitTimeout   time.Duration
}

func (p WorkerBackedProvider) ToolRuntime() string {
	return tools.ToolRuntimeLocalSystem
}

func (p WorkerBackedProvider) ToolCapabilities() []string {
	return capability.LocalSystemProvider{}.ToolCapabilities()
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

func (p WorkerBackedProvider) executeDefaultTool(ctx context.Context, api string, input any, state any) error {
	if p.Store == nil {
		return fmt.Errorf("worker-backed provider store is required")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode worker tool input: %w", err)
	}
	invocation, ok := tools.DefaultRegistry().WorkInvocation(tools.NamespaceDefault, api, tools.ToolRuntimeLocalSystem, inputJSON)
	if !ok {
		return fmt.Errorf("default tool api %q is not registered", api)
	}
	workerID, err := workerselect.Selector{Store: p.Store}.SelectWorkerID(workerselect.Request{
		WorkspaceID: p.workspaceID(),
		Invocation:  invocation,
	})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(invocation)
	if err != nil {
		return fmt.Errorf("encode worker invocation: %w", err)
	}
	work, err := p.Store.EnqueueWorkerWork(managedagents.EnqueueWorkerWorkInput{
		WorkspaceID:   p.workspaceID(),
		WorkerID:      workerID,
		EnvironmentID: p.EnvironmentID,
		SessionID:     p.sessionID(input),
		TurnID:        p.turnID(input),
		WorkType:      managedagents.WorkerWorkTypeToolExecution,
		Payload:       payload,
	})
	if err != nil {
		return err
	}
	result, err := p.waitForToolResult(ctx, work.ID)
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

func (p WorkerBackedProvider) waitForToolResult(ctx context.Context, workID string) (tools.ExecutionResult, error) {
	ctx, cancel := p.waitContext(ctx)
	defer cancel()

	ticker := time.NewTicker(p.pollInterval())
	defer ticker.Stop()

	for {
		work, err := p.Store.GetWorkerWork(workID)
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
