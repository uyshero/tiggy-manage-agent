package runner

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/tools"
)

type artifactToolStore interface {
	GetSessionArtifact(sessionID string, artifactID string) (managedagents.SessionArtifact, error)
	GetObjectRef(id string) (managedagents.ObjectRef, error)
}

type ArtifactToolService struct {
	Store       artifactToolStore
	ObjectStore objectstore.Client
}

func NewArtifactToolService(store artifactToolStore, objectStore objectstore.Client) tools.ArtifactToolService {
	if store == nil || objectStore == nil {
		return nil
	}
	return ArtifactToolService{Store: store, ObjectStore: objectStore}
}

func (s ArtifactToolService) Inspect(ctx context.Context, sessionID string, artifactID string) (tools.ArtifactDescriptor, error) {
	artifact, objectRef, err := s.resolve(ctx, sessionID, artifactID)
	if err != nil {
		return tools.ArtifactDescriptor{}, err
	}
	return artifactDescriptor(artifact, objectRef), nil
}

func (s ArtifactToolService) Read(ctx context.Context, sessionID string, request tools.ArtifactReadRequest) (tools.ArtifactReadPage, error) {
	artifact, objectRef, err := s.resolve(ctx, sessionID, request.ArtifactID)
	if err != nil {
		return tools.ArtifactReadPage{}, err
	}
	if knownBinaryArtifact(objectRef.ContentType, artifact.Name) {
		return tools.ArtifactReadPage{}, fmt.Errorf("%w: Artifact %q is binary (%s); use a format-specific parser or preview", managedagents.ErrInvalid, artifact.ID, objectRef.ContentType)
	}
	if objectRef.SizeBytes > tools.MaxTransportedArtifactBytes {
		return tools.ArtifactReadPage{}, fmt.Errorf("%w: Artifact %q exceeds the %d-byte text read limit", managedagents.ErrInvalid, artifact.ID, tools.MaxTransportedArtifactBytes)
	}
	verified, err := managedagents.ReadVerifiedObject(ctx, s.ObjectStore, objectRef, tools.MaxTransportedArtifactBytes)
	if err != nil {
		return tools.ArtifactReadPage{}, err
	}
	if !utf8.Valid(verified.Content) {
		return tools.ArtifactReadPage{}, fmt.Errorf("%w: Artifact %q is not UTF-8 text; use a format-specific parser or preview", managedagents.ErrInvalid, artifact.ID)
	}
	if request.OffsetBytes < 0 || request.OffsetBytes > int64(len(verified.Content)) {
		return tools.ArtifactReadPage{}, fmt.Errorf("%w: offset_bytes must be between 0 and %d", managedagents.ErrInvalid, len(verified.Content))
	}
	offset := int(request.OffsetBytes)
	if offset < len(verified.Content) && !utf8.RuneStart(verified.Content[offset]) {
		return tools.ArtifactReadPage{}, fmt.Errorf("%w: offset_bytes must point to a UTF-8 character boundary", managedagents.ErrInvalid)
	}
	maximum := request.MaxBytes
	if maximum == 0 {
		maximum = tools.DefaultArtifactReadMaxBytes
	}
	if maximum < 1 || maximum > tools.MaximumArtifactReadMaxBytes {
		return tools.ArtifactReadPage{}, fmt.Errorf("%w: max_bytes must be between 1 and %d", managedagents.ErrInvalid, tools.MaximumArtifactReadMaxBytes)
	}
	end := offset + maximum
	if end > len(verified.Content) {
		end = len(verified.Content)
	}
	for end > offset && !utf8.Valid(verified.Content[offset:end]) {
		end--
	}
	nextOffset := int64(0)
	eof := end == len(verified.Content)
	if !eof {
		nextOffset = int64(end)
	}
	return tools.ArtifactReadPage{
		Artifact: artifactDescriptor(artifact, objectRef), Content: string(verified.Content[offset:end]),
		OffsetBytes: int64(offset), ReturnedBytes: end - offset, NextOffsetBytes: nextOffset, EOF: eof,
	}, nil
}

func (s ArtifactToolService) resolve(ctx context.Context, sessionID string, artifactID string) (managedagents.SessionArtifact, managedagents.ObjectRef, error) {
	if s.Store == nil || s.ObjectStore == nil {
		return managedagents.SessionArtifact{}, managedagents.ObjectRef{}, objectstore.ErrNotConfigured
	}
	sessionID = strings.TrimSpace(sessionID)
	artifactID = strings.TrimSpace(artifactID)
	if sessionID == "" || artifactID == "" {
		return managedagents.SessionArtifact{}, managedagents.ObjectRef{}, fmt.Errorf("%w: session_id and artifact_id are required", managedagents.ErrInvalid)
	}
	artifact, err := managedagents.GetSessionArtifactWithContext(ctx, s.Store, sessionID, artifactID)
	if err != nil {
		return managedagents.SessionArtifact{}, managedagents.ObjectRef{}, err
	}
	objectRef, err := managedagents.GetObjectRefWithContext(ctx, s.Store, artifact.ObjectRefID)
	if err != nil {
		return managedagents.SessionArtifact{}, managedagents.ObjectRef{}, err
	}
	if artifact.WorkspaceID != "" && objectRef.WorkspaceID != "" && artifact.WorkspaceID != objectRef.WorkspaceID {
		return managedagents.SessionArtifact{}, managedagents.ObjectRef{}, fmt.Errorf("%w: Artifact object workspace mismatch", managedagents.ErrForbidden)
	}
	return artifact, objectRef, nil
}

func artifactDescriptor(artifact managedagents.SessionArtifact, objectRef managedagents.ObjectRef) tools.ArtifactDescriptor {
	return tools.ArtifactDescriptor{
		ArtifactID: artifact.ID, ObjectRefID: artifact.ObjectRefID, Name: artifact.Name,
		Description: artifact.Description, ArtifactType: artifact.ArtifactType,
		ContentType: objectRef.ContentType, SizeBytes: objectRef.SizeBytes, ChecksumSHA256: objectRef.ChecksumSHA256,
		TurnID: artifact.TurnID, ToolCallID: artifact.ToolCallID, Metadata: append([]byte(nil), artifact.Metadata...),
	}
}

func knownBinaryArtifact(contentType string, name string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") {
		return true
	}
	switch contentType {
	case "application/pdf", "application/zip", "application/gzip", "application/x-gzip", "application/x-tar",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, suffix := range []string{".pdf", ".zip", ".gz", ".tar", ".docx", ".xlsx", ".pptx", ".png", ".jpg", ".jpeg", ".gif", ".webp"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
