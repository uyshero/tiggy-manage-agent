package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/toolresult"
)

const ArtifactIdentifier = NamespaceArtifact

const (
	ArtifactAPIInspect = "inspect"
	ArtifactAPIRead    = "read"
)

const (
	DefaultArtifactReadMaxBytes = 16 << 10
	MaximumArtifactReadMaxBytes = 64 << 10
)

type ArtifactToolService interface {
	Inspect(context.Context, string, string) (ArtifactDescriptor, error)
	Read(context.Context, string, ArtifactReadRequest) (ArtifactReadPage, error)
}

type ArtifactDescriptor struct {
	ArtifactID     string          `json:"artifact_id"`
	ObjectRefID    string          `json:"object_ref_id"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	ArtifactType   string          `json:"artifact_type"`
	ContentType    string          `json:"content_type,omitempty"`
	SizeBytes      int64           `json:"size_bytes"`
	ChecksumSHA256 string          `json:"checksum_sha256,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

type ArtifactReadRequest struct {
	ArtifactID  string `json:"artifact_id"`
	OffsetBytes int64  `json:"offset_bytes,omitempty"`
	MaxBytes    int    `json:"max_bytes,omitempty"`
}

type ArtifactReadPage struct {
	Artifact        ArtifactDescriptor `json:"artifact"`
	Content         string             `json:"content"`
	OffsetBytes     int64              `json:"offset_bytes"`
	ReturnedBytes   int                `json:"returned_bytes"`
	NextOffsetBytes int64              `json:"next_offset_bytes,omitempty"`
	EOF             bool               `json:"eof"`
}

type artifactReadState struct {
	Artifact        ArtifactDescriptor `json:"artifact"`
	OffsetBytes     int64              `json:"offset_bytes"`
	ReturnedBytes   int                `json:"returned_bytes"`
	NextOffsetBytes int64              `json:"next_offset_bytes,omitempty"`
	EOF             bool               `json:"eof"`
}

type ArtifactRuntime struct{}

func (ArtifactRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: ArtifactIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Session Artifacts",
			Description: "Inspect metadata or read one bounded page from an Artifact referenced in the current Session.",
		},
		SystemRole:     "Artifact IDs in tool results and attachment context are durable, Session-scoped references. Use artifact_inspect for metadata and lineage. Use artifact_read only when omitted text or JSON is needed to continue; follow next_offset_bytes and do not reread unchanged pages. Binary content stays out of model context and must use a format-specific tool or preview pipeline.",
		Executors:      []string{ExecutorServer},
		ApprovalPolicy: ApprovalPolicyNever,
		API: []API{
			{
				Name: ArtifactAPIInspect, Namespace: NamespaceArtifact, APIName: ArtifactAPIInspect,
				Description:  "Inspect metadata, lineage, validation fields, content type, and size for one current-Session Artifact without reading its content.",
				Parameters:   json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"artifact_id":{"type":"string","minLength":1,"maxLength":160}},"required":["artifact_id"]}`),
				Capabilities: []string{"artifact.metadata.read"}, Risk: ToolRiskRead,
				Runtime: artifactRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: ArtifactAPIRead, Namespace: NamespaceArtifact, APIName: ArtifactAPIRead,
				Description:  "Read one bounded UTF-8 text or JSON page from a current-Session Artifact. Continue with next_offset_bytes until eof; binary content is rejected.",
				Parameters:   json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"artifact_id":{"type":"string","minLength":1,"maxLength":160},"offset_bytes":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":1024,"maximum":65536,"default":16384}},"required":["artifact_id"]}`),
				Capabilities: []string{"artifact.content.read"}, Risk: ToolRiskRead,
				Runtime: artifactRuntimePolicy(), Implementation: ToolImplementationServerBuiltin,
			},
		},
	}
}

func (ArtifactRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	service := executionContext.ArtifactService
	if service == nil {
		return failedResult(call, "artifact_service_unavailable", "Session Artifact access is unavailable in this runtime"), nil
	}
	sessionID := strings.TrimSpace(executionContext.SessionID)
	if sessionID == "" {
		return failedResult(call, "invalid_artifact_context", "Artifact access requires a Session"), nil
	}

	switch strings.ToLower(strings.TrimSpace(call.APIName)) {
	case ArtifactAPIInspect:
		var request struct {
			ArtifactID string `json:"artifact_id"`
		}
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return failedResult(call, toolresult.CodeInvalidToolArguments, fmt.Sprintf("decode artifact_inspect arguments: %v", err)), nil
		}
		descriptor, err := service.Inspect(ctx, sessionID, strings.TrimSpace(request.ArtifactID))
		if err != nil {
			return artifactFailure(call, err), nil
		}
		return artifactResult(call, fmt.Sprintf("Loaded metadata for Artifact %s (%s, %d bytes).", descriptor.ArtifactID, descriptor.Name, descriptor.SizeBytes), descriptor)
	case ArtifactAPIRead:
		var request ArtifactReadRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return failedResult(call, toolresult.CodeInvalidToolArguments, fmt.Sprintf("decode artifact_read arguments: %v", err)), nil
		}
		request.ArtifactID = strings.TrimSpace(request.ArtifactID)
		if request.MaxBytes == 0 {
			request.MaxBytes = DefaultArtifactReadMaxBytes
		}
		page, err := service.Read(ctx, sessionID, request)
		if err != nil {
			return artifactFailure(call, err), nil
		}
		pageDescriptor := page.Artifact
		pageDescriptor.Metadata = nil
		state := artifactReadState{
			Artifact: pageDescriptor, OffsetBytes: page.OffsetBytes, ReturnedBytes: page.ReturnedBytes,
			NextOffsetBytes: page.NextOffsetBytes, EOF: page.EOF,
		}
		return artifactResult(call, page.Content, state)
	default:
		return failedResult(call, "unknown_artifact_api", fmt.Sprintf("unsupported artifact api %q", call.APIName)), nil
	}
}

func artifactRuntimePolicy() *RuntimePolicy {
	return &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto}
}

func artifactResult(call Call, content string, state any) (ExecutionResult, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: content, State: encoded}, nil
}

func artifactFailure(call Call, err error) ExecutionResult {
	errorType := "artifact_read_failed"
	switch {
	case errors.Is(err, managedagents.ErrNotFound):
		errorType = "artifact_not_found"
	case errors.Is(err, managedagents.ErrForbidden):
		errorType = "artifact_forbidden"
	case errors.Is(err, managedagents.ErrInvalid):
		errorType = "invalid_artifact_read"
	}
	return failedResult(call, errorType, err.Error())
}
