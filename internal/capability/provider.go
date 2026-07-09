package capability

import (
	"context"
	"fmt"
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

type CapabilityDescriptor interface {
	ToolRuntime() string
	ToolCapabilities() []string
}

type ArtifactExportProvider interface {
	ExportArtifactFile(ctx context.Context, request ExportArtifactFileRequest) (ExportArtifactFileResult, error)
}

type ExportArtifactFileRequest struct {
	Path    string `json:"path"`
	WorkDir string `json:"work_dir,omitempty"`
}

type ExportArtifactFileResult struct {
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Content     []byte `json:"content,omitempty"`
}

type ArtifactRef struct {
	ArtifactID   string `json:"artifact_id"`
	ObjectRefID  string `json:"object_ref_id"`
	Name         string `json:"name"`
	ArtifactType string `json:"artifact_type"`
	DownloadPath string `json:"download_path"`
}

type UnavailableProvider struct {
	Runtime string
	Reason  string
}

func (p UnavailableProvider) ToolRuntime() string {
	return p.Runtime
}

func (UnavailableProvider) ToolCapabilities() []string {
	return nil
}

func (p UnavailableProvider) RunCommand(context.Context, RunCommandRequest) (CommandResult, error) {
	return CommandResult{}, p.err()
}

func (p UnavailableProvider) ExecuteCode(context.Context, ExecuteCodeRequest) (CommandResult, error) {
	return CommandResult{}, p.err()
}

func (p UnavailableProvider) ReadFile(context.Context, ReadFileRequest) (FileResult, error) {
	return FileResult{}, p.err()
}

func (p UnavailableProvider) WriteFile(context.Context, WriteFileRequest) (FileResult, error) {
	return FileResult{}, p.err()
}

func (p UnavailableProvider) EditFile(context.Context, EditFileRequest) (EditFileResult, error) {
	return EditFileResult{}, p.err()
}

func (p UnavailableProvider) err() error {
	if p.Reason != "" {
		return fmt.Errorf("%s runtime unavailable: %s", p.Runtime, p.Reason)
	}
	return fmt.Errorf("%s runtime unavailable", p.Runtime)
}

type RunCommandRequest struct {
	Meta        RequestMeta       `json:"meta"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	WorkDir     string            `json:"work_dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Stdin       []byte            `json:"stdin,omitempty"`
	OutputPaths []string          `json:"output_paths,omitempty"`
}

type ExecuteCodeRequest struct {
	Meta        RequestMeta       `json:"meta"`
	Language    string            `json:"language"`
	Code        string            `json:"code"`
	WorkDir     string            `json:"work_dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	OutputPaths []string          `json:"output_paths,omitempty"`
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
	ExitCode          int                        `json:"exit_code"`
	Stdout            string                     `json:"stdout,omitempty"`
	Stderr            string                     `json:"stderr,omitempty"`
	ExportedArtifacts []ExportArtifactFileResult `json:"-"`
	Artifacts         []ArtifactRef              `json:"-"`
	ArtifactError     string                     `json:"-"`
}

type FileResult struct {
	Path    string `json:"path"`
	Content []byte `json:"content,omitempty"`
}
