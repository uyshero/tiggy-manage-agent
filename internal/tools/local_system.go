package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"tiggy-manage-agent/internal/capability"
)

// DefaultIdentifier 是第一版通用能力域的内置工具注册标识，供模型 tool call 与 Registry 查找使用。
const DefaultIdentifier = NamespaceDefault

// DefaultRuntime 将命令执行、代码运行、文件读写等默认能力暴露为内置工具，
// 实际执行委托给 ExecutionContext.Provider（capability.Provider）。
type DefaultRuntime struct{}

// Manifest 返回工具的元数据、API 定义及给模型的 system role 指引。
func (DefaultRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: DefaultIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Default Tools",
			Description: "Run commands, execute code, and read or write files through the configured capability provider.",
		},
		SystemRole: "Use default.* tools only when a user asks you to inspect or change the execution environment. Prefer read-only operations before writes, and explain risky actions before taking them. Use read_file before edit_file.",
		Executors:  []string{ExecutorServer},
		API: []API{
			{
				Name:              "run_command",
				Namespace:         NamespaceDefault,
				APIName:           "run_command",
				Description:       "Run a command with optional args, working directory, environment variables, and stdin.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"stdin":{"type":"string"},"output_paths":{"type":"array","items":{"type":"string"},"description":"Optional file paths to persist as session artifacts after the command succeeds. Use absolute paths such as /mnt/data/report.csv in cloud_sandbox, or paths relative to work_dir."}},"required":["command"]}`),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityExec},
				Risk:              ToolRiskExec,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:    ToolImplementationWorkerCapability,
			},
			{
				Name:              "execute_code",
				Namespace:         NamespaceDefault,
				APIName:           "execute_code",
				Description:       "Execute a short code snippet in a supported language.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"language":{"type":"string"},"code":{"type":"string"},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"output_paths":{"type":"array","items":{"type":"string"},"description":"Optional file paths to persist as session artifacts after the code finishes. Use absolute paths such as /mnt/data/report.csv in cloud_sandbox, or paths relative to work_dir."}},"required":["language","code"]}`),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityCodeExecute, CapabilityExec},
				Risk:              ToolRiskExec,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:    ToolImplementationWorkerCapability,
			},
			{
				Name:           "read_file",
				Namespace:      NamespaceDefault,
				APIName:        "read_file",
				Description:    "Read a file from the execution environment.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
				Capabilities:   []string{CapabilityFilesystemRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:              "write_file",
				Namespace:         NamespaceDefault,
				APIName:           "write_file",
				Description:       "Write a file in the execution environment.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityFilesystemWrite},
				Risk:              ToolRiskWrite,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:    ToolImplementationWorkerCapability,
			},
			{
				Name:              "edit_file",
				Namespace:         NamespaceDefault,
				APIName:           "edit_file",
				Description:       "Perform exact string replacements in a file. Must read the file first before editing.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"},"work_dir":{"type":"string"}},"required":["old_string","new_string"]}`),
				HumanIntervention: "optional",
				Capabilities:      []string{CapabilityFilesystemRead, CapabilityFilesystemWrite},
				Risk:              ToolRiskWrite,
				Runtime:           &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:    ToolImplementationWorkerCapability,
			},
		},
	}
}

// Execute 根据 call.APIName 分发到 capability.Provider 的对应方法，
// 并将 Provider 返回的结构化结果转换为 ExecutionResult。
func (DefaultRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	provider := executionContext.Provider
	if provider == nil {
		return ExecutionResult{}, fmt.Errorf("capability provider is required")
	}

	switch normalizeAPIName(call.APIName) {
	case "run_command":
		var request capability.RunCommandRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode run_command arguments: %w", err)
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.RunCommand(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return commandResult(call, result, exportedFilesFromPaths(request.OutputPaths, request.WorkDir))
	case "execute_code":
		var request capability.ExecuteCodeRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode execute_code arguments: %w", err)
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.ExecuteCode(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return commandResult(call, result, exportedFilesFromPaths(request.OutputPaths, request.WorkDir))
	case "read_file":
		var request capability.ReadFileRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode read_file arguments: %w", err)
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.ReadFile(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(result)
		if err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{
			ID:         call.ID,
			Identifier: call.Identifier,
			APIName:    call.APIName,
			Content:    string(result.Content),
			State:      state,
		}, nil
	case "write_file":
		var request capability.WriteFileRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode write_file arguments: %w", err)
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.WriteFile(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(result)
		if err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{
			ID:         call.ID,
			Identifier: call.Identifier,
			APIName:    call.APIName,
			Content:    "Wrote file: " + result.Path,
			State:      state,
		}, nil
	case "edit_file":
		var request capability.EditFileRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode edit_file arguments: %w", err)
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.EditFile(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return editFileResult(call, result)
	default:
		return ExecutionResult{}, fmt.Errorf("unsupported local system api %q", call.APIName)
	}
}

func editFileResult(call Call, result capability.EditFileResult) (ExecutionResult, error) {
	state, err := json.Marshal(result)
	if err != nil {
		return ExecutionResult{}, err
	}
	executionResult := ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Content:    capability.FormatEditResult(result),
		State:      state,
	}
	if !result.Success {
		executionResult.Error = &ExecutionError{
			Type:    "edit_failed",
			Message: result.Error,
		}
	}
	return executionResult, nil
}

// commandResult 将命令/代码执行的 stdout/stderr 压缩为面向模型的文本内容，
// 完整结果（含 exit code）保留在 State 字段供下游消费。
func commandResult(call Call, result capability.CommandResult, exportedFiles []ArtifactExport) (ExecutionResult, error) {
	state, err := json.Marshal(result)
	if err != nil {
		return ExecutionResult{}, err
	}
	exportedFiles = mergeExportedArtifacts(exportedFiles, result.ExportedArtifacts)
	// 优先展示 stdout，其次 stderr，均无输出时回退到退出码说明。
	content := strings.TrimSpace(result.Stdout)
	if content == "" {
		content = strings.TrimSpace(result.Stderr)
	}
	if content == "" {
		content = fmt.Sprintf("Command exited with code %d.", result.ExitCode)
	}
	return ExecutionResult{
		ID:            call.ID,
		Identifier:    call.Identifier,
		APIName:       call.APIName,
		Content:       content,
		State:         state,
		ExportedFiles: exportedFiles,
		Artifacts:     capabilityArtifactRefs(result.Artifacts),
		ArtifactError: strings.TrimSpace(result.ArtifactError),
	}, nil
}

func capabilityArtifactRefs(refs []capability.ArtifactRef) []ArtifactRef {
	if len(refs) == 0 {
		return nil
	}
	result := make([]ArtifactRef, 0, len(refs))
	for _, ref := range refs {
		if ref.ArtifactID == "" && ref.ObjectRefID == "" && ref.DownloadPath == "" {
			continue
		}
		result = append(result, ArtifactRef{
			ArtifactID:   ref.ArtifactID,
			ObjectRefID:  ref.ObjectRefID,
			Name:         ref.Name,
			ArtifactType: ref.ArtifactType,
			DownloadPath: ref.DownloadPath,
		})
	}
	return result
}

func exportedFilesFromPaths(paths []string, workDir string) []ArtifactExport {
	if len(paths) == 0 {
		return nil
	}
	exports := make([]ArtifactExport, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		exports = append(exports, ArtifactExport{
			Path:    path,
			WorkDir: strings.TrimSpace(workDir),
		})
	}
	if len(exports) == 0 {
		return nil
	}
	return exports
}

func mergeExportedArtifacts(exports []ArtifactExport, files []capability.ExportArtifactFileResult) []ArtifactExport {
	if len(files) == 0 {
		return exports
	}
	merged := append([]ArtifactExport(nil), exports...)
	used := make([]bool, len(files))
	for index := range merged {
		for fileIndex, file := range files {
			if used[fileIndex] {
				continue
			}
			if !exportMatchesFile(merged[index], file) {
				continue
			}
			merged[index].Name = firstNonEmptyString(merged[index].Name, file.Name)
			merged[index].ContentType = firstNonEmptyString(merged[index].ContentType, file.ContentType)
			merged[index].Content = append([]byte(nil), file.Content...)
			used[fileIndex] = true
			break
		}
	}
	for fileIndex, file := range files {
		if used[fileIndex] {
			continue
		}
		merged = append(merged, ArtifactExport{
			Path:        file.Path,
			Name:        file.Name,
			ContentType: file.ContentType,
			Content:     append([]byte(nil), file.Content...),
		})
	}
	return merged
}

func exportMatchesFile(export ArtifactExport, file capability.ExportArtifactFileResult) bool {
	exportPath := strings.TrimSpace(export.Path)
	filePath := strings.TrimSpace(file.Path)
	if exportPath != "" && exportPath == filePath {
		return true
	}
	exportName := strings.TrimSpace(export.Name)
	if exportName == "" && exportPath != "" {
		exportName = filepath.Base(exportPath)
	}
	fileName := strings.TrimSpace(file.Name)
	if exportName != "" && fileName != "" && exportName == fileName {
		return true
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// normalizeAPIName 兼容 camelCase 与 snake_case 两种 API 命名风格。
func normalizeAPIName(value string) string {
	switch value {
	case "runCommand":
		return "run_command"
	case "executeCode":
		return "execute_code"
	case "readFile":
		return "read_file"
	case "writeFile":
		return "write_file"
	case "editFile":
		return "edit_file"
	default:
		return value
	}
}
