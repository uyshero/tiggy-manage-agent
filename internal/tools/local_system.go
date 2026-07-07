package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/capability"
)

// LocalSystemIdentifier 是内置本地系统工具的注册标识，供模型 tool call 与 Registry 查找使用。
const LocalSystemIdentifier = "tma.local_system"

// LocalSystemRuntime 将命令执行、代码运行、文件读写等能力暴露为内置工具，
// 实际执行委托给 ExecutionContext.Provider（capability.Provider）。
type LocalSystemRuntime struct{}

// Manifest 返回工具的元数据、API 定义及给模型的 system role 指引。
func (LocalSystemRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: LocalSystemIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Local System",
			Description: "Run commands, execute code, and read or write files through the configured capability provider.",
		},
		SystemRole: "Use tma.local_system only when a user asks you to inspect or change the execution environment. Prefer read-only operations before writes, and explain risky actions before taking them. Use read_file before edit_file.",
		Executors:  []string{ExecutorServer},
		API: []API{
			{
				Name:              "run_command",
				Description:       "Run a command with optional args, working directory, environment variables, and stdin.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"stdin":{"type":"string"}},"required":["command"]}`),
				HumanIntervention: "optional",
			},
			{
				Name:              "execute_code",
				Description:       "Execute a short code snippet in a supported language.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"language":{"type":"string"},"code":{"type":"string"},"work_dir":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}}},"required":["language","code"]}`),
				HumanIntervention: "optional",
			},
			{
				Name:        "read_file",
				Description: "Read a file from the execution environment.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
			{
				Name:              "write_file",
				Description:       "Write a file in the execution environment.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
				HumanIntervention: "optional",
			},
			{
				Name:              "edit_file",
				Description:       "Perform exact string replacements in a file. Must read the file first before editing.",
				Parameters:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"},"work_dir":{"type":"string"}},"required":["old_string","new_string"]}`),
				HumanIntervention: "optional",
			},
		},
	}
}

// Execute 根据 call.APIName 分发到 capability.Provider 的对应方法，
// 并将 Provider 返回的结构化结果转换为 ExecutionResult。
func (LocalSystemRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
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
		return commandResult(call, result)
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
		return commandResult(call, result)
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
func commandResult(call Call, result capability.CommandResult) (ExecutionResult, error) {
	state, err := json.Marshal(result)
	if err != nil {
		return ExecutionResult{}, err
	}
	// 优先展示 stdout，其次 stderr，均无输出时回退到退出码说明。
	content := strings.TrimSpace(result.Stdout)
	if content == "" {
		content = strings.TrimSpace(result.Stderr)
	}
	if content == "" {
		content = fmt.Sprintf("Command exited with code %d.", result.ExitCode)
	}
	return ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Content:    content,
		State:      state,
	}, nil
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
