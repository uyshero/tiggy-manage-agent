package capability

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
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

// RuntimeSkillMaterializer exposes immutable Skill packages inside an execution
// environment before the model can invoke package scripts or read package files.
type RuntimeSkillMaterializer interface {
	MaterializeRuntimeSkills(ctx context.Context, packages []RuntimeSkillPackage) ([]MaterializedRuntimeSkill, error)
}

type RuntimeSkillCache interface {
	LookupMaterializedRuntimeSkill(ctx context.Context, skillID string, identifier string, version int, checksum string) (MaterializedRuntimeSkill, bool, error)
}

type WorkspaceSnapshotProvider interface {
	CreateWorkspaceSnapshot(ctx context.Context) ([]byte, int, error)
	RestoreWorkspaceSnapshot(ctx context.Context, archive []byte) error
}

type RuntimeSkillPackage struct {
	SkillID    string
	Identifier string
	Version    int
	Checksum   string
	Files      []RuntimeSkillFile
}

type RuntimeSkillFile struct {
	Path       string
	Content    []byte
	Executable bool
}

type MaterializedRuntimeSkill struct {
	SkillID    string
	Identifier string
	Version    int
	Directory  string
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

func (r RunCommandRequest) MarshalJSON() ([]byte, error) {
	type payload struct {
		Meta        RequestMeta       `json:"meta"`
		Command     string            `json:"command"`
		Args        []string          `json:"args,omitempty"`
		WorkDir     string            `json:"work_dir,omitempty"`
		Env         map[string]string `json:"env,omitempty"`
		Stdin       string            `json:"stdin,omitempty"`
		StdinBase64 string            `json:"stdin_base64,omitempty"`
		OutputPaths []string          `json:"output_paths,omitempty"`
	}
	value := payload{
		Meta:        r.Meta,
		Command:     r.Command,
		Args:        r.Args,
		WorkDir:     r.WorkDir,
		Env:         r.Env,
		OutputPaths: r.OutputPaths,
	}
	assignBytePayload(&value.Stdin, &value.StdinBase64, r.Stdin)
	return json.Marshal(value)
}

func (r *RunCommandRequest) UnmarshalJSON(data []byte) error {
	type payload struct {
		Meta        RequestMeta       `json:"meta"`
		Command     string            `json:"command"`
		Args        []string          `json:"args,omitempty"`
		WorkDir     string            `json:"work_dir,omitempty"`
		Env         map[string]string `json:"env,omitempty"`
		Stdin       *string           `json:"stdin"`
		StdinBase64 string            `json:"stdin_base64,omitempty"`
		OutputPaths []string          `json:"output_paths,omitempty"`
	}
	var decoded payload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	stdin, err := decodeBytePayload(decoded.Stdin, decoded.StdinBase64, "stdin")
	if err != nil {
		return err
	}
	r.Meta = decoded.Meta
	r.Command = decoded.Command
	r.Args = decoded.Args
	r.WorkDir = decoded.WorkDir
	r.Env = decoded.Env
	r.Stdin = stdin
	r.OutputPaths = decoded.OutputPaths
	return nil
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
	Meta         RequestMeta `json:"meta"`
	Path         string      `json:"path"`
	OffsetBytes  *int64      `json:"offset_bytes,omitempty"`
	MaxBytes     *int        `json:"max_bytes,omitempty"`
	StartLine    *int        `json:"start_line,omitempty"`
	MaxLines     *int        `json:"max_lines,omitempty"`
	FileRevision string      `json:"file_revision,omitempty"`
}

type WriteFileRequest struct {
	Meta             RequestMeta `json:"meta"`
	Path             string      `json:"path"`
	Content          []byte      `json:"content"`
	Mode             string      `json:"mode,omitempty"`
	ExpectedAbsent   bool        `json:"expected_absent,omitempty"`
	ExpectedRevision string      `json:"expected_revision,omitempty"`
	ContentSHA256    string      `json:"content_sha256,omitempty"`
	CreateParents    *bool       `json:"create_parents,omitempty"`
}

func (r WriteFileRequest) MarshalJSON() ([]byte, error) {
	type payload struct {
		Meta             RequestMeta `json:"meta"`
		Path             string      `json:"path"`
		Content          *string     `json:"content,omitempty"`
		ContentBase64    string      `json:"content_base64,omitempty"`
		Mode             string      `json:"mode,omitempty"`
		ExpectedAbsent   bool        `json:"expected_absent,omitempty"`
		ExpectedRevision string      `json:"expected_revision,omitempty"`
		ContentSHA256    string      `json:"content_sha256,omitempty"`
		CreateParents    *bool       `json:"create_parents,omitempty"`
	}
	value := payload{
		Meta: r.Meta, Path: r.Path, Mode: r.Mode, ExpectedAbsent: r.ExpectedAbsent,
		ExpectedRevision: r.ExpectedRevision, ContentSHA256: r.ContentSHA256, CreateParents: r.CreateParents,
	}
	if utf8.Valid(r.Content) {
		content := string(r.Content)
		value.Content = &content
	} else {
		value.ContentBase64 = base64.StdEncoding.EncodeToString(r.Content)
	}
	return json.Marshal(value)
}

func (r *WriteFileRequest) UnmarshalJSON(data []byte) error {
	type payload struct {
		Meta             RequestMeta `json:"meta"`
		Path             string      `json:"path"`
		Content          *string     `json:"content"`
		ContentBase64    string      `json:"content_base64,omitempty"`
		Mode             string      `json:"mode,omitempty"`
		ExpectedAbsent   bool        `json:"expected_absent,omitempty"`
		ExpectedRevision string      `json:"expected_revision,omitempty"`
		ContentSHA256    string      `json:"content_sha256,omitempty"`
		CreateParents    *bool       `json:"create_parents,omitempty"`
	}
	var decoded payload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	content, err := decodeBytePayload(decoded.Content, decoded.ContentBase64, "content")
	if err != nil {
		return err
	}
	r.Meta = decoded.Meta
	r.Path = decoded.Path
	r.Content = content
	r.Mode = decoded.Mode
	r.ExpectedAbsent = decoded.ExpectedAbsent
	r.ExpectedRevision = decoded.ExpectedRevision
	r.ContentSHA256 = decoded.ContentSHA256
	r.CreateParents = decoded.CreateParents
	return nil
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
	Path                 string `json:"path"`
	Content              []byte `json:"content,omitempty"`
	SizeBytes            int64  `json:"size_bytes"`
	OffsetBytes          int64  `json:"offset_bytes"`
	RequestedOffsetBytes *int64 `json:"requested_offset_bytes,omitempty"`
	ReturnedBytes        int    `json:"returned_bytes"`
	StartLine            int    `json:"start_line"`
	EndLine              int    `json:"end_line"`
	NextOffsetBytes      int64  `json:"next_offset_bytes"`
	EOF                  bool   `json:"eof"`
	Truncated            bool   `json:"truncated"`
	FileRevision         string `json:"file_revision,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	Binary               bool   `json:"binary,omitempty"`
	LineTruncated        bool   `json:"line_truncated,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	ContentType          string `json:"content_type,omitempty"`
	Encoding             string `json:"encoding,omitempty"`
	SuggestedCapability  string `json:"suggested_capability,omitempty"`
	ContentSHA256        string `json:"content_sha256,omitempty"`
}

type SearchFileRequest struct {
	Meta         RequestMeta `json:"meta"`
	Path         string      `json:"path"`
	Query        string      `json:"query"`
	MaxResults   int         `json:"max_results,omitempty"`
	FileRevision string      `json:"file_revision,omitempty"`
}

type SearchFileMatch struct {
	LineNumber    int    `json:"line_number"`
	OffsetBytes   int64  `json:"offset_bytes"`
	Line          string `json:"line"`
	LineTruncated bool   `json:"line_truncated,omitempty"`
}

type SearchFileResult struct {
	Path         string            `json:"path"`
	SizeBytes    int64             `json:"size_bytes"`
	FileRevision string            `json:"file_revision"`
	Query        string            `json:"query"`
	Matches      []SearchFileMatch `json:"matches"`
	Truncated    bool              `json:"truncated"`
	Binary       bool              `json:"binary,omitempty"`
}

// FileSearchProvider is optional so third-party capability providers remain
// source-compatible. Built-in local, guarded, sandbox, and worker providers
// implement it with the same read-only semantics.
type FileSearchProvider interface {
	SearchFile(ctx context.Context, request SearchFileRequest) (SearchFileResult, error)
}

type FindFilesRequest struct {
	Meta          RequestMeta `json:"meta"`
	Root          string      `json:"root,omitempty"`
	Pattern       string      `json:"pattern"`
	Exclude       []string    `json:"exclude,omitempty"`
	IncludeHidden bool        `json:"include_hidden,omitempty"`
	MaxResults    int         `json:"max_results,omitempty"`
	AfterPath     string      `json:"after_path,omitempty"`
}

type FoundFile struct {
	Path         string `json:"path"`
	SizeBytes    int64  `json:"size_bytes"`
	FileRevision string `json:"file_revision"`
	Kind         string `json:"kind,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
}

type FindFilesResult struct {
	Root      string      `json:"root"`
	Pattern   string      `json:"pattern"`
	Files     []FoundFile `json:"files"`
	Scanned   int         `json:"scanned_files"`
	Truncated bool        `json:"truncated"`
	NextPath  string      `json:"next_path,omitempty"`
}

type FileDiscoveryProvider interface {
	FindFiles(ctx context.Context, request FindFilesRequest) (FindFilesResult, error)
}

type SearchFilesRequest struct {
	Meta          RequestMeta `json:"meta"`
	Root          string      `json:"root,omitempty"`
	Query         string      `json:"query"`
	Paths         []string    `json:"paths"`
	Exclude       []string    `json:"exclude,omitempty"`
	Mode          string      `json:"mode,omitempty"`
	CaseSensitive *bool       `json:"case_sensitive,omitempty"`
	IncludeHidden bool        `json:"include_hidden,omitempty"`
	MaxFiles      int         `json:"max_files,omitempty"`
	MaxResults    int         `json:"max_results,omitempty"`
}

type SearchFilesMatch struct {
	Path          string `json:"path"`
	LineNumber    int    `json:"line_number"`
	OffsetBytes   int64  `json:"offset_bytes"`
	Line          string `json:"line"`
	LineTruncated bool   `json:"line_truncated,omitempty"`
	FileRevision  string `json:"file_revision"`
}

type SearchFilesResult struct {
	Query              string             `json:"query"`
	Mode               string             `json:"mode"`
	Matches            []SearchFilesMatch `json:"matches"`
	ScannedFiles       int                `json:"scanned_files"`
	ScannedBytes       int64              `json:"scanned_bytes"`
	SkippedBinaryFiles int                `json:"skipped_binary_files"`
	Truncated          bool               `json:"truncated"`
}

type FileTreeSearchProvider interface {
	SearchFiles(ctx context.Context, request SearchFilesRequest) (SearchFilesResult, error)
}

func (r SearchFilesRequest) CaseSensitiveValue() bool {
	return r.CaseSensitive == nil || *r.CaseSensitive
}

func NormalizeSearchMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "literal"
	}
	return value
}

func assignBytePayload(text *string, encoded *string, value []byte) {
	if len(value) == 0 {
		return
	}
	if utf8.Valid(value) {
		*text = string(value)
		return
	}
	*encoded = base64.StdEncoding.EncodeToString(value)
}

func decodeBytePayload(text *string, encoded string, field string) ([]byte, error) {
	if encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode %s_base64: %w", field, err)
		}
		return decoded, nil
	}
	if text == nil {
		return nil, nil
	}
	return []byte(*text), nil
}
