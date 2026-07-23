package tools

import (
	"context"
	"encoding/json"
	"errors"
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
			Description: "Run commands and read or write files through the configured capability provider.",
		},
		SystemRole:     "Use default_* tools only when a user asks you to inspect or change the execution environment. Prefer read-only operations before writes, and explain risky actions before taking them. Use default_read_file before default_edit_file. Small files can be read with path only. A large file returns one bounded page plus pagination metadata: continue only with next_offset_bytes and the same file_revision, never repeat an unchanged page, and do not blindly traverse hundreds of pages. Use default_find_files to discover paths and default_search_files when a keyword or regex is known, then read a focused line or byte window around a match. For very large JSON, CSV, logs, generated data, or exceptionally long lines, prefer format-aware parsers or bounded analysis commands over sequential page walking. Binary files are classified but their bytes are never inserted into model context; follow suggested_capability and use vision, a document skill, default_run_command with a format-specific parser, or another dedicated capability. When reporting conclusions from a partial read, state the inspected byte or line ranges and never describe a sample as a complete review. Small files may be created with one default_write_file call. For a file likely to exceed 6000 output tokens, never put the full file in one tool call: first use default_write_file to create a small skeleton containing unique numbered placeholders such as __TMA_PLACEHOLDER_REPORT_001__, then replace one placeholder at a time with default_edit_file. During segmented generation, issue only one default_write_file or default_edit_file call per model response and wait for its result before generating the next segment. Keep each content/new_string at or below 6000 tokens when possible and always below 8000. Split only at complete semantic boundaries such as functions, classes, modules, chapters, or complete data structures. Placeholder edits must use exact old_string and replace_all=false; this makes retries idempotent because a consumed placeholder cannot be applied twice. After all segments, use default_search_files for __TMA_PLACEHOLDER_ to confirm no markers remain and run the appropriate syntax check or test before reporting completion. Never retry an unchanged oversized or malformed payload. Any file intended as a user deliverable must be persisted as a session artifact: use default_write_file/default_edit_file, or include every deliverable path in output_paths when using default_run_command. In cloud_sandbox, uploaded inputs are synchronized under /workspace/uploads and final user deliverables such as reports, HTML pages, images, spreadsheets, exports, or completed source files must also be stored under /workspace so the same session can reopen them later. Use /mnt/data only for caches, temporary files, and intermediate generation results. If a final result was built under /mnt/data, copy or move it into /workspace and publish only the /workspace path before completion. Preserve the existing path when editing a user-provided file unless the user asks for a separate final copy. Absolute file paths must stay under /workspace or /mnt/data; do not use /root, /tmp, or other absolute roots.",
		Executors:      []string{ExecutorServer},
		ApprovalPolicy: ApprovalPolicyNever,
		API: []API{
			{
				Name:           "run_command",
				Namespace:      NamespaceDefault,
				APIName:        "run_command",
				Description:    "Run a command with optional args, working directory, environment variables, and stdin.",
				Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"command":{"type":"string","minLength":1},"args":{"type":"array","items":{"type":"string"}},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"stdin":{"type":"string"},"timeout_ms":{"type":"integer","minimum":100,"maximum":600000,"default":120000,"description":"Total command deadline in milliseconds."},"max_output_bytes":{"type":"integer","minimum":1024,"maximum":1048576,"default":65536,"description":"Maximum captured bytes for each of stdout and stderr. Additional bytes are counted and discarded."},"output_paths":{"type":"array","items":{"type":"string"},"description":"Optional final file paths to persist as session artifacts after the command succeeds. In cloud_sandbox, final deliverables must be under /workspace, for example /workspace/report.csv. Do not publish temporary or intermediate /mnt/data files."}},"required":["command"]}`),
				ApprovalPolicy: ApprovalPolicyAlways,
				ApprovalReason: InterventionReasonProcessExec,
				Capabilities:   []string{CapabilityExec},
				Risk:           ToolRiskExec,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:            "execute_code",
				Namespace:       NamespaceDefault,
				APIName:         "execute_code",
				Description:     "Execute a short code snippet in a supported language.",
				Parameters:      json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"language":{"type":"string"},"code":{"type":"string"},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"timeout_ms":{"type":"integer","minimum":100,"maximum":600000,"default":120000},"max_output_bytes":{"type":"integer","minimum":1024,"maximum":1048576,"default":65536},"output_paths":{"type":"array","items":{"type":"string"},"description":"Optional final file paths to persist as session artifacts after the code finishes. In cloud_sandbox, final deliverables must be under /workspace, for example /workspace/report.csv. Do not publish temporary or intermediate /mnt/data files."}},"required":["language","code"]}`),
				ApprovalPolicy:  ApprovalPolicyAlways,
				ApprovalReason:  InterventionReasonProcessExec,
				Capabilities:    []string{CapabilityCodeExecute, CapabilityExec},
				Risk:            ToolRiskExec,
				Runtime:         &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation:  ToolImplementationWorkerCapability,
				HiddenFromModel: true,
			},
			{
				Name:           "read_file",
				Namespace:      NamespaceDefault,
				APIName:        "read_file",
				Description:    "Read one bounded UTF-8 page. Path-only reads small files completely and returns the first bounded page for large files. Byte mode uses raw file byte offsets; line mode is mutually exclusive. Continue with next_offset_bytes and file_revision. DOCX supports path-only text extraction; other binary formats return a safe explanation.",
				Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","description":"Path to read. In cloud_sandbox, absolute paths must begin with /workspace or /mnt/data; otherwise use a relative path."},"offset_bytes":{"type":"integer","minimum":0,"description":"Raw file byte offset for byte mode. Never use a rune or character index."},"max_bytes":{"type":"integer","minimum":4,"maximum":1048576,"description":"Maximum raw bytes for byte mode. The provider may enforce a lower deployment hard limit."},"start_line":{"type":"integer","minimum":1,"description":"1-based first line for line mode."},"max_lines":{"type":"integer","minimum":1,"maximum":5000,"description":"Maximum lines for line mode. The page remains byte-bounded and may stop inside an exceptionally long line."},"file_revision":{"type":"string","description":"Revision returned by a previous page. Required when continuing a multi-page read so file changes produce stale_file_revision instead of mixed content."}},"required":["path"]}`),
				Capabilities:   []string{CapabilityFilesystemRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:           "find_files",
				Namespace:      NamespaceDefault,
				APIName:        "find_files",
				Description:    "Find regular files by path pattern without invoking a shell. Results are metadata-only, lexically ordered, bounded, and do not follow symbolic links.",
				Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"root":{"type":"string","description":"Directory to search; defaults to the workspace root."},"pattern":{"type":"string","description":"Path pattern relative to root. Supports *, ?, and **."},"exclude":{"type":"array","items":{"type":"string"},"maxItems":32,"description":"Patterns to exclude, such as .git/** or vendor/**."},"include_hidden":{"type":"boolean","description":"Include dot-prefixed path components; defaults to false."},"max_results":{"type":"integer","minimum":1,"maximum":1000,"description":"Maximum results; defaults to 200."},"after_path":{"type":"string","description":"Continue after this lexical path from a previous next_path value."}},"required":["pattern"]}`),
				Capabilities:   []string{CapabilityFilesystemRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:           "search_files",
				Namespace:      NamespaceDefault,
				APIName:        "search_files",
				Description:    "Search bounded sets of UTF-8 text files selected by path patterns. Supports literal and Go RE2 regular-expression modes, returns stable line and byte locations, and safely skips binary files.",
				Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"root":{"type":"string","description":"Directory used as the path-pattern root; defaults to the workspace root."},"query":{"type":"string","minLength":1,"maxLength":1024,"description":"Single-line text or Go RE2 expression to search for."},"paths":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":32,"description":"Relative path patterns, for example internal/**/*.go or README.md."},"exclude":{"type":"array","items":{"type":"string"},"maxItems":32},"mode":{"type":"string","enum":["literal","regex"],"default":"literal"},"case_sensitive":{"type":"boolean","default":true},"include_hidden":{"type":"boolean","default":false},"max_files":{"type":"integer","minimum":1,"maximum":5000,"default":1000},"max_results":{"type":"integer","minimum":1,"maximum":500,"default":100}},"required":["query","paths"]}`),
				Capabilities:   []string{CapabilityFilesystemRead},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:            "search_file",
				Namespace:       NamespaceDefault,
				APIName:         "search_file",
				Description:     "Search one UTF-8 text file for a literal string using bounded memory. Returns matching line numbers and raw byte offsets so read_file can fetch a focused window. This is read-only and does not require command execution approval.",
				Parameters:      json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","description":"Text file to search. Cloud sandbox path rules are identical to read_file."},"query":{"type":"string","minLength":1,"maxLength":1024,"description":"Single-line literal UTF-8 string, not a regular expression."},"max_results":{"type":"integer","minimum":1,"maximum":100,"description":"Maximum matching lines to return; defaults to 50."},"file_revision":{"type":"string","description":"Optional revision from read_file. A mismatch returns stale_file_revision."}},"required":["path","query"]}`),
				Capabilities:    []string{CapabilityFilesystemRead},
				Risk:            ToolRiskRead,
				Runtime:         &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation:  ToolImplementationWorkerCapability,
				HiddenFromModel: true,
			},
			{
				Name:           "write_file",
				Namespace:      NamespaceDefault,
				APIName:        "write_file",
				Description:    "Write a small complete file or a skeleton for segmented generation. If the complete file may exceed 6000 tokens, write only a skeleton with unique numbered __TMA_PLACEHOLDER_<scope>_<number>__ markers, then replace them with edit_file. content has a hard limit of 8000 estimated tokens.",
				Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","description":"Path to write. In cloud_sandbox, uploaded inputs are under /workspace/uploads and final user deliverables must be written under /workspace; use /mnt/data only for caches, temporary files, and intermediate results. Absolute paths must begin with /workspace or /mnt/data; otherwise use a relative path."},"content":{"type":"string","maxLength":8000,"description":"Complete UTF-8 small-file content, or a compact large-file skeleton with unique numbered placeholders. Binary deliverables must use artifact or format-specific workflows."},"mode":{"type":"string","enum":["create","overwrite","create_or_overwrite"],"default":"create_or_overwrite"},"expected_absent":{"type":"boolean","description":"Fail if the target already exists."},"expected_revision":{"type":"string","description":"Only commit when the current file_revision matches."},"content_sha256":{"type":"string","pattern":"^[0-9a-fA-F]{64}$","description":"Optional SHA-256 integrity check for content."},"create_parents":{"type":"boolean","default":true}},"required":["path","content"]}`),
				ApprovalPolicy: ApprovalPolicyConditional,
				ApprovalReason: InterventionReasonFilesystemWrite,
				Capabilities:   []string{CapabilityFilesystemWrite},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation: ToolImplementationWorkerCapability,
			},
			{
				Name:           "edit_file",
				Namespace:      NamespaceDefault,
				APIName:        "edit_file",
				Description:    "Atomically edit one file using one exact replacement or multiple non-overlapping replacements matched against the same file revision. For segmented generation, replace exactly one unique numbered skeleton placeholder with one complete semantic segment; consumed placeholders make retries idempotent. Must read the file first before ordinary edits, and must verify no placeholders remain before completion. Total replacement content has a hard limit of 8000 estimated tokens.",
				Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","minLength":1,"description":"Required target path. In cloud_sandbox, absolute paths must begin with /workspace or /mnt/data; otherwise use a relative path."},"edits":{"type":"array","minItems":1,"maxItems":20,"description":"Atomic non-overlapping replacements. Every old_string is matched against the same original file revision.","items":{"type":"object","additionalProperties":false,"properties":{"old_string":{"type":"string","minLength":1,"description":"Exact unique UTF-8 text copied from the prior read."},"new_string":{"type":"string","maxLength":8000,"description":"Replacement UTF-8 text; use an empty string to delete old_string."},"replace_all":{"type":"boolean","default":false,"description":"Replace every non-overlapping match for this operation."}},"required":["old_string","new_string"]}},"old_string":{"type":"string","minLength":1,"description":"Legacy single-edit exact text. Do not combine with edits."},"new_string":{"type":"string","maxLength":8000,"description":"Legacy single-edit replacement text. Do not combine with edits."},"replace_all":{"type":"boolean","default":false,"description":"Legacy single-edit replace-all option."}},"required":["path"],"oneOf":[{"required":["edits"]},{"required":["old_string","new_string"]}]}`),
				ApprovalPolicy: ApprovalPolicyConditional,
				ApprovalReason: InterventionReasonFilesystemWrite,
				Capabilities:   []string{CapabilityFilesystemRead, CapabilityFilesystemWrite},
				Risk:           ToolRiskWrite,
				LockKey:        "file:{path}",
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeCloudSandbox},
				Implementation: ToolImplementationWorkerCapability,
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
			if failure, ok := structuredFileReadFailure(call, err); ok {
				return failure, nil
			}
			return ExecutionResult{}, err
		}
		if result.SizeBytes == 0 && len(result.Content) > 0 {
			result.SizeBytes = int64(len(result.Content))
			result.ReturnedBytes = len(result.Content)
			result.NextOffsetBytes = int64(len(result.Content))
			result.EOF = true
		}
		var state json.RawMessage
		if executionContext.CapabilityTransport {
			state, err = json.Marshal(result)
		} else {
			state, err = marshalFileResultMetadata(result)
		}
		if err != nil {
			return ExecutionResult{}, err
		}
		content, readable, err := readableFileContent(result.Path, result.Content)
		if err != nil {
			return ExecutionResult{}, err
		}
		if result.Binary || !readable {
			content = fmt.Sprintf("File %q is %s binary data (%s); bytes were not added to model context. Suggested capability: %s.", result.Path, result.Kind, result.ContentType, result.SuggestedCapability)
		}
		return ExecutionResult{
			ID:         call.ID,
			Identifier: call.Identifier,
			APIName:    call.APIName,
			Content:    contentWithPlaceholderWarning(content),
			State:      state,
		}, nil
	case "find_files":
		var request capability.FindFilesRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode find_files arguments: %w", err)
		}
		finder, ok := provider.(capability.FileDiscoveryProvider)
		if !ok {
			return failedResult(call, "find_files_unavailable", "the selected capability provider does not support safe file discovery"), nil
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := finder.FindFiles(ctx, request)
		if err != nil {
			if failure, ok := structuredFileReadFailure(call, err); ok {
				return failure, nil
			}
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(result)
		if err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: formatFindFilesResult(result), State: state}, nil
	case "search_files":
		var request capability.SearchFilesRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode search_files arguments: %w", err)
		}
		searcher, ok := provider.(capability.FileTreeSearchProvider)
		if !ok {
			return failedResult(call, "search_files_unavailable", "the selected capability provider does not support safe cross-file search"), nil
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := searcher.SearchFiles(ctx, request)
		if err != nil {
			if failure, ok := structuredFileReadFailure(call, err); ok {
				return failure, nil
			}
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(result)
		if err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: formatSearchFilesResult(result), State: state}, nil
	case "search_file":
		var request capability.SearchFileRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode search_file arguments: %w", err)
		}
		searcher, ok := provider.(capability.FileSearchProvider)
		if !ok {
			return failedResult(call, "search_file_unavailable", "the selected capability provider does not support safe file search"), nil
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := searcher.SearchFile(ctx, request)
		if err != nil {
			if failure, ok := structuredFileReadFailure(call, err); ok {
				return failure, nil
			}
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(result)
		if err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{
			ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
			Content: formatSearchFileResult(result), State: state,
		}, nil
	case "write_file":
		var request capability.WriteFileRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode write_file arguments: %w", err)
		}
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.WriteFile(ctx, request)
		if err != nil {
			if failure, ok := structuredFileReadFailure(call, err); ok {
				return failure, nil
			}
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
		request.ExpectedRevision = executionContext.ExpectedFileRevision
		request.ExpectedContentSHA256 = executionContext.ExpectedFileContentSHA256
		request.Meta = capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
		result, err := provider.EditFile(ctx, request)
		if err != nil {
			if failure, ok := structuredFileReadFailure(call, err); ok {
				return failure, nil
			}
			return failedResult(call, "edit_execution_failed", err.Error()), nil
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
			Type:    fallbackString(result.Code, "edit_failed"),
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

func marshalFileResultMetadata(result capability.FileResult) (json.RawMessage, error) {
	result.Content = nil
	return json.Marshal(result)
}

func structuredFileReadFailure(call Call, err error) (ExecutionResult, bool) {
	var readErr *capability.FileReadError
	if !errors.As(err, &readErr) {
		return ExecutionResult{}, false
	}
	state, marshalErr := json.Marshal(map[string]any{"error": readErr})
	if marshalErr != nil {
		state = json.RawMessage(`{"error":{"code":"file_read_failed","message":"failed to encode structured file error"}}`)
	}
	result := failedResult(call, readErr.Code, readErr.Message)
	result.Content = readErr.Message
	result.State = state
	return result, true
}

func formatSearchFileResult(result capability.SearchFileResult) string {
	if result.Binary {
		return fmt.Sprintf("File %q contains binary data that search_file cannot search as UTF-8 text.", result.Path)
	}
	var content strings.Builder
	fmt.Fprintf(&content, "Search results for %q in %s (revision %s):", result.Query, result.Path, result.FileRevision)
	if len(result.Matches) == 0 {
		content.WriteString(" no matches")
		return content.String()
	}
	for _, match := range result.Matches {
		fmt.Fprintf(&content, "\n%d [byte %d]: %s", match.LineNumber, match.OffsetBytes, match.Line)
		if match.LineTruncated {
			content.WriteString(" [line preview truncated]")
		}
	}
	if result.Truncated {
		content.WriteString("\n[Search results truncated; narrow the query before requesting more matches.]")
	}
	return content.String()
}

func formatFindFilesResult(result capability.FindFilesResult) string {
	var content strings.Builder
	fmt.Fprintf(&content, "Files matching %q under %s:", result.Pattern, result.Root)
	if len(result.Files) == 0 {
		content.WriteString(" no matches")
		return content.String()
	}
	for _, file := range result.Files {
		fmt.Fprintf(&content, "\n%s [%s, %d bytes, revision %s]", file.Path, file.Kind, file.SizeBytes, file.FileRevision)
	}
	if result.Truncated {
		fmt.Fprintf(&content, "\n[Results truncated; continue with after_path=%q.]", result.NextPath)
	}
	return content.String()
}

func formatSearchFilesResult(result capability.SearchFilesResult) string {
	var content strings.Builder
	fmt.Fprintf(&content, "Search results for %q (%s):", result.Query, result.Mode)
	if len(result.Matches) == 0 {
		content.WriteString(" no matches")
	} else {
		for _, match := range result.Matches {
			fmt.Fprintf(&content, "\n%s:%d [byte %d, revision %s]: %s", match.Path, match.LineNumber, match.OffsetBytes, match.FileRevision, match.Line)
			if match.LineTruncated {
				content.WriteString(" [line preview truncated]")
			}
		}
	}
	fmt.Fprintf(&content, "\n[Scanned %d files/%d bytes; skipped %d binary files.]", result.ScannedFiles, result.ScannedBytes, result.SkippedBinaryFiles)
	if result.Truncated {
		content.WriteString("\n[Search truncated; narrow paths or query before retrying.]")
	}
	return content.String()
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
	var notices []string
	if result.TimedOut {
		notices = append(notices, fmt.Sprintf("command timed out after %d ms", result.DurationMS))
	} else if result.Canceled {
		notices = append(notices, "command was canceled")
	}
	if result.StdoutTruncated {
		notices = append(notices, fmt.Sprintf("stdout truncated after capturing %d of %d bytes", result.StdoutCapturedBytes, result.StdoutBytes))
	}
	if result.StderrTruncated {
		notices = append(notices, fmt.Sprintf("stderr truncated after capturing %d of %d bytes", result.StderrCapturedBytes, result.StderrBytes))
	}
	if len(notices) > 0 {
		content += "\n[" + strings.Join(notices, "; ") + "]"
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
