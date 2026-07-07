package capability

import (
	"context"
	"time"
)

const ProtocolVersion = "tma.capability.v1"

// RequestMeta 是一次能力调用的公共上下文。
// 它不表达 LLM tool call，只用于把 session / turn / deadline 传给底层 Provider。
type RequestMeta struct {
	ProtocolVersion string     `json:"protocol_version"`
	SessionID       string     `json:"session_id"`
	TurnID          string     `json:"turn_id"`
	Deadline        *time.Time `json:"deadline,omitempty"`
}

func NewRequestMeta(sessionID string, turnID string, deadline *time.Time) RequestMeta {
	return RequestMeta{
		ProtocolVersion: ProtocolVersion,
		SessionID:       sessionID,
		TurnID:          turnID,
		Deadline:        deadline,
	}
}

// Provider 是底层执行环境的能力面。
// 未来 LLM tool calling 可以把 runCommand / executeCode / readFile / writeFile 包装成工具，
// 但 Provider 本身不负责 Tool Manifest、模型循环或 UI Inspector。
type Provider interface {
	RunCommand(ctx context.Context, request RunCommandRequest) (CommandResult, error)
	ExecuteCode(ctx context.Context, request ExecuteCodeRequest) (CommandResult, error)
	ReadFile(ctx context.Context, request ReadFileRequest) (FileResult, error)
	WriteFile(ctx context.Context, request WriteFileRequest) (FileResult, error)
	EditFile(ctx context.Context, request EditFileRequest) (EditFileResult, error)
}

type RunCommandRequest struct {
	Meta    RequestMeta       `json:"meta"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Stdin   []byte            `json:"stdin,omitempty"`
}

type ExecuteCodeRequest struct {
	Meta     RequestMeta       `json:"meta"`
	Language string            `json:"language"`
	Code     string            `json:"code"`
	WorkDir  string            `json:"work_dir,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type ReadFileRequest struct {
	Meta RequestMeta `json:"meta"`
	Path string      `json:"path"`
}

type WriteFileRequest struct {
	Meta    RequestMeta `json:"meta"`
	Path    string      `json:"path"`
	Content []byte      `json:"content"`
}

type CommandResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

type FileResult struct {
	Path    string `json:"path"`
	Content []byte `json:"content,omitempty"`
}
