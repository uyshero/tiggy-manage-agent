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
		SystemRole: "Use default.* tools only when a user asks you to inspect or change the execution environment. Prefer read-only operations before writes, and explain risky actions before taking them. Use read_file before edit_file. Small files may be created with one write_file call. For a file likely to exceed 6000 output tokens, never put the full file in one tool call: first use write_file to create a small skeleton containing unique numbered placeholders such as __TMA_PLACEHOLDER_REPORT_001__, then replace one placeholder at a time with edit_file. During segmented generation, issue only one write_file or edit_file call per model response and wait for its result before generating the next segment. Keep each content/new_string at or below 6000 tokens when possible and always below 8000. Split only at complete semantic boundaries such as functions, classes, modules, chapters, or complete data structures. Placeholder edits must use exact old_string and replace_all=false; this makes retries idempotent because a consumed placeholder cannot be applied twice. After all segments, use read_file to confirm no __TMA_PLACEHOLDER_...__ markers remain and run the appropriate syntax check or test before reporting completion. Never retry an unchanged oversized or malformed payload. Any file intended as a user deliverable must be persisted as a session artifact: use write_file/edit_file, or include every deliverable path in output_paths when using run_command or execute_code. In cloud_sandbox, uploaded inputs are synchronized under /workspace/uploads and final user deliverables such as reports, HTML pages, images, spreadsheets, exports, or completed source files must also be stored under /workspace so the same session can reopen them later. Use /mnt/data only for caches, temporary files, and intermediate generation results. If a final result was built under /mnt/data, copy or move it into /workspace and publish only the /workspace path before completion. Preserve the existing path when editing a user-provided file unless the user asks for a separate final copy. Absolute file paths must stay under /workspace or /mnt/data; do not use /root, /tmp, or other absolute roots.",
		Executors:  []string{ExecutorServer},
		API: []API{
			{
				Name:              "run_command",
				Namespace:         NamespaceDefault,
				APIName:           "run_command",
				Description:       "Run a command with optional args, working directory, environment variables, and stdin.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"stdin":{"type":"string"},"output_paths":{"type":"array","items":{"type":"string"},"description":"Optional final file paths to persist as session artifacts after the command succeeds. In cloud_sandbox, final deliverables must be under /workspace, for example /workspace/report.csv. Do not publish temporary or intermediate /mnt/data files."}},"required":["command"]}`),
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
				Parameters:        json.RawMessage(`{"type":"object","properties":{"language":{"type":"string"},"code":{"type":"string"},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"output_paths":{"type":"array","items":{"type":"string"},"description":"Optional final file paths to persist as session artifacts after the code finishes. In cloud_sandbox, final deliverables must be under /workspace, for example /workspace/report.csv. Do not publish temporary or intermediate /mnt/data files."}},"required":["language","code"]}`),
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
				Description:    "Read a UTF-8 text file or extract text from a .docx file in the execution environment. Other binary formats return a safe explanatory result.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to read. In cloud_sandbox, absolute paths must begin with /workspace or /mnt/data; otherwise use a relative path."}},"required":["path"]}`),
				Capabilities:   []string{CapabilityFilesystemRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:              "write_file",
				Namespace:         NamespaceDefault,
				APIName:           "write_file",
				Description:       "Write a small complete file or a skeleton for segmented generation. If the complete file may exceed 6000 tokens, write only a skeleton with unique numbered __TMA_PLACEHOLDER_<scope>_<number>__ markers, then replace them with edit_file. content has a hard limit of 8000 estimated tokens.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to write. In cloud_sandbox, uploaded inputs are under /workspace/uploads and final user deliverables must be written under /workspace; use /mnt/data only for caches, temporary files, and intermediate results. Absolute paths must begin with /workspace or /mnt/data; otherwise use a relative path."},"content":{"type":"string","description":"Complete small-file content, or a compact large-file skeleton with unique numbered placeholders. Recommended <=6000 estimated tokens; hard limit 8000."}},"required":["path","content"]}`),
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
				Description:       "Perform an exact string replacement. For segmented generation, replace exactly one unique numbered skeleton placeholder with one complete semantic segment; consumed placeholders make retries idempotent. Must read the file first before ordinary edits, and must verify no placeholders remain before completion. new_string has a hard limit of 8000 estimated tokens.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to edit. In cloud_sandbox, absolute paths must begin with /workspace or /mnt/data; otherwise use a relative path."},"file_path":{"type":"string","description":"Legacy alias for path. In cloud_sandbox, absolute file_path values must begin with /workspace or /mnt/data; otherwise use a relative path."},"old_string":{"type":"string","description":"Exact existing text. For segmented generation use one unique __TMA_PLACEHOLDER_<scope>_<number>__ marker."},"new_string":{"type":"string","description":"One complete semantic segment. Recommended <=6000 estimated tokens; hard limit 8000."},"replace_all":{"type":"boolean","description":"Keep false for unique placeholder replacement."},"work_dir":{"type":"string","description":"Base directory for relative paths. In cloud_sandbox, absolute work_dir values must begin with /workspace or /mnt/data."}},"required":["old_string","new_string"]}`),
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
	if validationError := ValidateFileMutationCall(call); validationError != nil {
		result := failedResult(call, validationError.Type, validationError.Message)
		result.Content = validationError.Message
		return result, nil
	}

	switch normalizeAPIName(call.APIName) {
	case "run_command":
		var request capability.RunCommandRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode run_command arguments: %w", err)
		}
		if err := validateDeliverableOutputPaths(provider, request.OutputPaths, request.WorkDir); err != nil {
			result := failedResult(call, "invalid_output_path", err.Error())
			result.Content = err.Error()
			return result, nil
		}
		if _, delegated := provider.(interface{ HandlesManagedEnvironment() }); !delegated {
			request.Env = mergeManagedEnvironment(request.Env, executionContext.Environment)
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
		if err := validateDeliverableOutputPaths(provider, request.OutputPaths, request.WorkDir); err != nil {
			result := failedResult(call, "invalid_output_path", err.Error())
			result.Content = err.Error()
			return result, nil
		}
		if _, delegated := provider.(interface{ HandlesManagedEnvironment() }); !delegated {
			request.Env = mergeManagedEnvironment(request.Env, executionContext.Environment)
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
		content, readable, err := readableFileContent(result.Path, result.Content)
		if err != nil {
			return ExecutionResult{}, err
		}
		if !readable {
			content = fmt.Sprintf("File %q contains binary data that read_file cannot decode as text. Use execute_code with a format-specific parser.", result.Path)
			state, err = json.Marshal(map[string]any{
				"path":       result.Path,
				"binary":     true,
				"size_bytes": len(result.Content),
			})
			if err != nil {
				return ExecutionResult{}, err
			}
		}
		return ExecutionResult{
			ID:         call.ID,
			Identifier: call.Identifier,
			APIName:    call.APIName,
			Content:    contentWithPlaceholderWarning(content),
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
		capability.ResetSegmentEditState(result.Path)
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
			ExportedFiles: []ArtifactExport{{
				Path:         result.Path,
				Name:         filepath.Base(result.Path),
				Description:  "Generated by default.write_file",
				ArtifactType: "file",
			}},
		}, nil
	case "edit_file":
		var request capability.EditFileRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode edit_file arguments: %w", err)
		}
		request.Idempotent = IsSegmentedFilePlaceholder(request.OldString)
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

func validateDeliverableOutputPaths(provider capability.Provider, paths []string, workDir string) error {
	descriptor, ok := provider.(capability.CapabilityDescriptor)
	if !ok || descriptor.ToolRuntime() != ToolRuntimeCloudSandbox || len(paths) == 0 {
		return nil
	}
	workDir = filepath.ToSlash(filepath.Clean(strings.TrimSpace(workDir)))
	for _, rawPath := range paths {
		outputPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rawPath)))
		if outputPath == "." {
			continue
		}
		absoluteOutsideWorkspace := filepath.IsAbs(outputPath) && outputPath != "/workspace" && !strings.HasPrefix(outputPath, "/workspace/")
		relativeOutsideWorkspace := !filepath.IsAbs(outputPath) && filepath.IsAbs(workDir) && workDir != "/workspace" && !strings.HasPrefix(workDir, "/workspace/")
		if absoluteOutsideWorkspace || relativeOutsideWorkspace {
			return fmt.Errorf("output_paths entry %q is temporary in cloud_sandbox; copy or move the final deliverable under /workspace and publish that path", rawPath)
		}
	}
	return nil
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
	} else {
		executionResult.ExportedFiles = []ArtifactExport{{
			Path:         result.Path,
			Name:         filepath.Base(result.Path),
			Description:  "Generated by default.edit_file",
			ArtifactType: "file",
		}}
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
