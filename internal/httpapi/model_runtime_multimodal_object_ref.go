package httpapi

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
	"tiggy-manage-agent/internal/objectstore"
)

// resolveMultimodalObjectRef turns an authorized durable object into an
// in-memory media frame. Storage coordinates never cross the Server/Runtime
// boundary.
func (s *Server) resolveMultimodalObjectRef(
	ctx context.Context,
	scope managedagents.AccessScope,
	route modelruntime.MultimodalRoute,
	start modelruntime.MultimodalSessionStart,
	input modelruntime.MultimodalObjectRefInput,
) (modelruntime.MultimodalMediaFrame, error) {
	scope, err := managedagents.ValidateAccessScope(scope)
	if err != nil {
		return modelruntime.MultimodalMediaFrame{}, err
	}
	if err := start.Validate(); err != nil {
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: invalid multimodal session start: %v", managedagents.ErrInvalid, err)
	}
	if err := input.Validate(start); err != nil {
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: invalid multimodal object ref input: %v", managedagents.ErrInvalid, err)
	}
	maxFrameBytes := route.Constraints.MaxFrameBytes
	if maxFrameBytes < 1 || maxFrameBytes > modelruntime.MultimodalMaxFrameBytes {
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: invalid multimodal route frame limit", managedagents.ErrInvalid)
	}
	if input.SizeBytes > maxFrameBytes {
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: multimodal object ref exceeds model frame limit", managedagents.ErrInvalid)
	}
	if strings.TrimSpace(start.SessionID) == "" {
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: session_id is required for multimodal object refs", managedagents.ErrInvalid)
	}

	session, err := s.store.GetSessionScoped(start.SessionID, scope)
	if err != nil || session.WorkspaceID != scope.WorkspaceID {
		return modelruntime.MultimodalMediaFrame{}, multimodalObjectRefUnavailable()
	}
	objectRef, err := s.store.GetObjectRefScoped(input.ObjectRefID, scope)
	if err != nil || objectRef.WorkspaceID != session.WorkspaceID {
		return modelruntime.MultimodalMediaFrame{}, multimodalObjectRefUnavailable()
	}
	if err := s.authorizeMultimodalObjectRefSession(ctx, scope, session, objectRef); err != nil {
		return modelruntime.MultimodalMediaFrame{}, err
	}

	track, ok := multimodalInputTrack(start, input.TrackID)
	if !ok {
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: multimodal object ref track is unavailable", managedagents.ErrInvalid)
	}
	if err := validateMultimodalObjectRefMetadata(objectRef, track, input, maxFrameBytes); err != nil {
		return modelruntime.MultimodalMediaFrame{}, err
	}
	verified, err := managedagents.ReadVerifiedObject(ctx, s.objectStore, objectRef, maxFrameBytes)
	if err != nil {
		if errors.Is(err, objectstore.ErrNotConfigured) {
			return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("multimodal object storage is unavailable: %w", objectstore.ErrNotConfigured)
		}
		return modelruntime.MultimodalMediaFrame{}, fmt.Errorf("%w: multimodal object ref content failed integrity validation", managedagents.ErrInvalid)
	}
	if err := validateVerifiedMultimodalObject(verified, track, input); err != nil {
		return modelruntime.MultimodalMediaFrame{}, err
	}

	return modelruntime.MultimodalMediaFrame{
		Kind: track.Kind, Sequence: input.Sequence, TimestampMicros: input.TimestampMicros,
		TrackID: input.TrackID, Payload: verified.Content,
	}, nil
}

func (s *Server) authorizeMultimodalObjectRefSession(ctx context.Context, scope managedagents.AccessScope, session managedagents.Session, objectRef managedagents.ObjectRef) error {
	switch objectRef.Visibility {
	case managedagents.ObjectVisibilityWorkspace:
		return nil
	case managedagents.ObjectVisibilitySession:
		databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, scope)
		if err != nil {
			return err
		}
		artifacts, err := managedagents.ListSessionArtifactsWithContext(databaseCtx, s.store, session.ID)
		if err != nil {
			return multimodalObjectRefUnavailable()
		}
		for _, artifact := range artifacts {
			if artifact.WorkspaceID == session.WorkspaceID && artifact.SessionID == session.ID && artifact.ObjectRefID == objectRef.ID {
				return nil
			}
		}
		return multimodalObjectRefUnavailable()
	default:
		return multimodalObjectRefUnavailable()
	}
}

func validateMultimodalObjectRefMetadata(objectRef managedagents.ObjectRef, track modelruntime.MultimodalTrack, input modelruntime.MultimodalObjectRefInput, maxFrameBytes int64) error {
	if objectRef.SizeBytes < 1 || objectRef.SizeBytes > maxFrameBytes || objectRef.SizeBytes != input.SizeBytes {
		return fmt.Errorf("%w: multimodal object ref size metadata mismatch", managedagents.ErrInvalid)
	}
	if !sameMultimodalContentType(objectRef.ContentType, input.ContentType) || !sameMultimodalContentType(objectRef.ContentType, track.ContentType) {
		return fmt.Errorf("%w: multimodal object ref content type metadata mismatch", managedagents.ErrInvalid)
	}
	checksum := strings.TrimSpace(objectRef.ChecksumSHA256)
	if !validMultimodalSHA256Hex(checksum) {
		return fmt.Errorf("%w: multimodal object ref requires a valid persisted checksum", managedagents.ErrInvalid)
	}
	if input.ChecksumSHA256 != "" && !strings.EqualFold(checksum, input.ChecksumSHA256) {
		return fmt.Errorf("%w: multimodal object ref checksum metadata mismatch", managedagents.ErrInvalid)
	}
	return nil
}

func validateVerifiedMultimodalObject(verified managedagents.VerifiedObjectContent, track modelruntime.MultimodalTrack, input modelruntime.MultimodalObjectRefInput) error {
	if verified.SizeBytes != input.SizeBytes {
		return fmt.Errorf("%w: multimodal object ref payload size mismatch", managedagents.ErrInvalid)
	}
	if strings.TrimSpace(verified.StorageContentType) == "" ||
		!sameMultimodalContentType(verified.StorageContentType, verified.ObjectRef.ContentType) ||
		!sameMultimodalContentType(verified.StorageContentType, input.ContentType) ||
		!sameMultimodalContentType(verified.StorageContentType, track.ContentType) {
		return fmt.Errorf("%w: multimodal object ref storage content type mismatch", managedagents.ErrInvalid)
	}
	if input.ChecksumSHA256 != "" && !strings.EqualFold(verified.ChecksumSHA256, input.ChecksumSHA256) {
		return fmt.Errorf("%w: multimodal object ref payload checksum mismatch", managedagents.ErrInvalid)
	}
	return nil
}

func multimodalInputTrack(start modelruntime.MultimodalSessionStart, trackID string) (modelruntime.MultimodalTrack, bool) {
	for _, track := range start.InputTracks {
		if track.ID == trackID {
			return track, true
		}
	}
	return modelruntime.MultimodalTrack{}, false
}

func sameMultimodalContentType(left, right string) bool {
	return normalizeMultimodalContentType(left) != "" && normalizeMultimodalContentType(left) == normalizeMultimodalContentType(right)
}

func normalizeMultimodalContentType(value string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.ToLower(mediaType)
}

func validMultimodalSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F' {
			continue
		}
		return false
	}
	return true
}

func multimodalObjectRefUnavailable() error {
	return fmt.Errorf("%w: multimodal object ref is unavailable", managedagents.ErrForbidden)
}
