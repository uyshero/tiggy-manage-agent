package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

const (
	defaultArtifactExchangeTTL = 15 * time.Minute
	minArtifactExchangeTTL     = time.Minute
	maxArtifactExchangeTTL     = 24 * time.Hour
)

type createArtifactImportExchangeRequest struct {
	SessionID              string          `json:"session_id"`
	Filename               string          `json:"filename"`
	Description            string          `json:"description,omitempty"`
	ArtifactType           string          `json:"artifact_type,omitempty"`
	EnvironmentID          string          `json:"environment_id,omitempty"`
	TurnID                 string          `json:"turn_id,omitempty"`
	ToolCallID             string          `json:"tool_call_id,omitempty"`
	Visibility             string          `json:"visibility,omitempty"`
	ContentType            string          `json:"content_type,omitempty"`
	ExpectedSizeBytes      *int64          `json:"expected_size_bytes,omitempty"`
	MaxSizeBytes           *int64          `json:"max_size_bytes,omitempty"`
	ExpectedChecksumSHA256 string          `json:"expected_checksum_sha256,omitempty"`
	TTLSeconds             int64           `json:"ttl_seconds,omitempty"`
	Metadata               json.RawMessage `json:"metadata,omitempty"`
}

type createArtifactExportExchangeRequest struct {
	SessionID   string `json:"session_id"`
	ArtifactID  string `json:"artifact_id,omitempty"`
	ObjectRefID string `json:"object_ref_id,omitempty"`
	Filename    string `json:"filename,omitempty"`
	TTLSeconds  int64  `json:"ttl_seconds,omitempty"`
}

type artifactExchangeGrant struct {
	Exchange   managedagents.ArtifactExchange `json:"exchange"`
	ContentURL string                         `json:"content_url"`
}

type artifactExchangeImportResult struct {
	Exchange  managedagents.ArtifactExchange `json:"exchange"`
	ObjectRef managedagents.ObjectRef        `json:"object_ref"`
	Artifact  managedagents.SessionArtifact  `json:"artifact"`
}

func (s *Server) registerArtifactExchangeRoutes() {
	s.mux.HandleFunc("POST /v2/artifact-exchanges/imports", s.withV2Request(s.createArtifactImportExchange))
	s.mux.HandleFunc("POST /v2/artifact-exchanges/exports", s.withV2Request(s.createArtifactExportExchange))
	s.mux.HandleFunc("GET /v2/artifact-exchanges/{exchange_id}", s.withV2Request(s.getArtifactExchange))
	s.mux.HandleFunc("GET /v2/artifact-exchanges/{exchange_id}/content", s.withV2Request(s.downloadArtifactExchangeContent))
	s.mux.HandleFunc("PUT /v2/artifact-exchanges/{exchange_id}/content", s.withV2Request(s.uploadArtifactExchangeContent))
}

func (s *Server) createArtifactImportExchange(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.ArtifactExchangeContextStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "artifact_exchange_unavailable", "artifact exchange API is unavailable", false, nil)
		return
	}
	var request createArtifactImportExchangeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	session, err := s.getSessionForRequest(r, strings.TrimSpace(request.SessionID))
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if principal, principalOK := PrincipalFromRequest(r); principalOK {
		if err := authorizeSessionPrincipal(principal, session); err != nil {
			writeV2ManagedError(w, r, err)
			return
		}
	}
	filename := safeArtifactFileName(request.Filename)
	if strings.TrimSpace(request.Filename) == "" || len(filename) > 512 {
		writeV2ManagedError(w, r, fmt.Errorf("%w: filename is required and must not exceed 512 bytes", managedagents.ErrInvalid))
		return
	}
	metadata, err := normalizeArtifactExchangeMetadata(request.Metadata)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	maxSize := int64(maxArtifactUploadBytes)
	if request.MaxSizeBytes != nil {
		maxSize = *request.MaxSizeBytes
	}
	if maxSize < 0 || maxSize > maxArtifactUploadBytes || request.ExpectedSizeBytes != nil && (*request.ExpectedSizeBytes < 0 || *request.ExpectedSizeBytes > maxSize) {
		writeV2ManagedError(w, r, fmt.Errorf("%w: upload size bounds must be between 0 and %d bytes", managedagents.ErrInvalid, maxArtifactUploadBytes))
		return
	}
	checksum := strings.ToLower(strings.TrimSpace(request.ExpectedChecksumSHA256))
	if checksum != "" && !validSHA256Hex(checksum) {
		writeV2ManagedError(w, r, fmt.Errorf("%w: expected_checksum_sha256 must be a 64-character hex digest", managedagents.ErrInvalid))
		return
	}
	ttl, err := artifactExchangeTTL(request.TTLSeconds)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	token, tokenHash, err := newArtifactExchangeToken()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	principal, _ := PrincipalFromRequest(r)
	ownerID := requestOwnerID(r, requestActorID(r, "system"))
	exchange, err := store.CreateArtifactExchangeContext(r.Context(), managedagents.CreateArtifactExchangeInput{
		WorkspaceID: session.WorkspaceID, AppID: principal.ServiceIdentityID, OwnerID: ownerID,
		Direction: managedagents.ArtifactExchangeDirectionImport, SessionID: session.ID,
		Filename: filename, Description: request.Description,
		ArtifactType:  fallbackString(request.ArtifactType, managedagents.ArtifactTypeFile),
		EnvironmentID: request.EnvironmentID, TurnID: request.TurnID, ToolCallID: request.ToolCallID,
		Visibility:  fallbackString(request.Visibility, managedagents.ObjectVisibilitySession),
		ContentType: strings.TrimSpace(request.ContentType), ExpectedSizeBytes: request.ExpectedSizeBytes,
		MaxSizeBytes: maxSize, ExpectedChecksumSHA256: checksum, TokenHash: tokenHash,
		ExpiresAt: time.Now().UTC().Add(ttl), Metadata: metadata, CreatedBy: requestActorID(r, ownerID),
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, artifactExchangeGrant{Exchange: exchange, ContentURL: artifactExchangeContentURL(exchange, token)})
}

func (s *Server) createArtifactExportExchange(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.ArtifactExchangeContextStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "artifact_exchange_unavailable", "artifact exchange API is unavailable", false, nil)
		return
	}
	var request createArtifactExportExchangeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.ArtifactID = strings.TrimSpace(request.ArtifactID)
	request.ObjectRefID = strings.TrimSpace(request.ObjectRefID)
	if (request.ArtifactID == "") == (request.ObjectRefID == "") {
		writeV2ManagedError(w, r, fmt.Errorf("%w: exactly one of artifact_id or object_ref_id is required", managedagents.ErrInvalid))
		return
	}
	var artifact managedagents.SessionArtifact
	var objectRef managedagents.ObjectRef
	var err error
	if request.ArtifactID != "" {
		if request.SessionID == "" {
			writeV2ManagedError(w, r, fmt.Errorf("%w: session_id is required for artifact export", managedagents.ErrInvalid))
			return
		}
		if _, err = s.getSessionForRequest(r, request.SessionID); err == nil {
			artifact, err = managedagents.GetSessionArtifactWithContext(r.Context(), s.store, request.SessionID, request.ArtifactID)
		}
		if err == nil {
			objectRef, err = s.getObjectRefForRequest(r, artifact.ObjectRefID)
		}
	} else {
		if request.SessionID == "" {
			writeV2ManagedError(w, r, fmt.Errorf("%w: session_id is required to authorize object_ref export", managedagents.ErrInvalid))
			return
		}
		objectRef, err = s.getObjectRefForRequest(r, request.ObjectRefID)
		if err == nil && !s.canDownloadObjectRef(r, objectRef) {
			err = fmt.Errorf("%w: object export not allowed", managedagents.ErrForbidden)
		}
	}
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	ttl, err := artifactExchangeTTL(request.TTLSeconds)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	token, tokenHash, err := newArtifactExchangeToken()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	filename := safeArtifactFileName(request.Filename)
	if strings.TrimSpace(request.Filename) == "" {
		filename = safeArtifactFileName(fallbackString(artifact.Name, objectRef.ObjectKey))
	}
	principal, _ := PrincipalFromRequest(r)
	ownerID := requestOwnerID(r, requestActorID(r, "system"))
	expectedSize := objectRef.SizeBytes
	exchange, err := store.CreateArtifactExchangeContext(r.Context(), managedagents.CreateArtifactExchangeInput{
		WorkspaceID: objectRef.WorkspaceID, AppID: principal.ServiceIdentityID, OwnerID: ownerID,
		Direction: managedagents.ArtifactExchangeDirectionExport, SessionID: request.SessionID,
		ObjectRefID: objectRef.ID, ArtifactID: artifact.ID, Filename: filename,
		ArtifactType: fallbackString(artifact.ArtifactType, managedagents.ArtifactTypeFile),
		Visibility:   objectRef.Visibility, ContentType: objectRef.ContentType,
		ExpectedSizeBytes: &expectedSize, MaxSizeBytes: objectRef.SizeBytes,
		ExpectedChecksumSHA256: strings.ToLower(objectRef.ChecksumSHA256), TokenHash: tokenHash,
		ExpiresAt: time.Now().UTC().Add(ttl), CreatedBy: requestActorID(r, ownerID),
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, artifactExchangeGrant{Exchange: exchange, ContentURL: artifactExchangeContentURL(exchange, token)})
}

func (s *Server) getArtifactExchange(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.ArtifactExchangeContextStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "artifact_exchange_unavailable", "artifact exchange API is unavailable", false, nil)
		return
	}
	exchange, err := store.GetArtifactExchangeContext(r.Context(), r.PathValue("exchange_id"))
	if err == nil {
		err = authorizeArtifactExchangeRequest(r, exchange)
	}
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, exchange)
}

func (s *Server) downloadArtifactExchangeContent(w http.ResponseWriter, r *http.Request) {
	store, ctx, exchange, ok := s.claimPublicArtifactExchange(w, r, managedagents.ArtifactExchangeDirectionExport)
	if !ok {
		return
	}
	objectRef, err := managedagents.GetObjectRefWithContext(ctx, s.store, exchange.ObjectRefID)
	if err != nil {
		s.failClaimedArtifactExchange(ctx, store, exchange.ID, err)
		writeV2ManagedError(w, r, err)
		return
	}
	object, err := s.objectStore.GetObject(ctx, objectstore.GetObjectInput{
		Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
	})
	if err != nil {
		s.failClaimedArtifactExchange(ctx, store, exchange.ID, err)
		writeArtifactExchangeError(w, r, err)
		return
	}
	defer object.Body.Close()
	if _, err := store.CompleteArtifactExportContext(ctx, exchange.ID, time.Now().UTC()); err != nil {
		s.failClaimedArtifactExchange(ctx, store, exchange.ID, err)
		writeV2ManagedError(w, r, err)
		return
	}
	contentType := fallbackString(object.ContentType, fallbackString(exchange.ContentType, "application/octet-stream"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", object.SizeBytes))
	w.Header().Set("Content-Disposition", contentDispositionAttachment(exchange.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.ETag != "" {
		w.Header().Set("ETag", object.ETag)
	}
	if object.ChecksumSHA256 != "" {
		w.Header().Set("Digest", "sha-256="+object.ChecksumSHA256)
	}
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("artifact exchange download copy failed", "exchange_id", exchange.ID, "error", err)
	}
}

func (s *Server) uploadArtifactExchangeContent(w http.ResponseWriter, r *http.Request) {
	store, ctx, exchange, ok := s.claimPublicArtifactExchange(w, r, managedagents.ArtifactExchangeDirectionImport)
	if !ok {
		return
	}
	fail := func(err error) {
		s.failClaimedArtifactExchange(ctx, store, exchange.ID, err)
		writeArtifactExchangeError(w, r, err)
	}
	if r.ContentLength > exchange.MaxSizeBytes {
		fail(fmt.Errorf("%w: upload exceeds signed maximum size", managedagents.ErrInvalid))
		return
	}
	if exchange.ExpectedSizeBytes != nil && r.ContentLength >= 0 && r.ContentLength != *exchange.ExpectedSizeBytes {
		fail(fmt.Errorf("%w: upload content length does not match signed size", managedagents.ErrInvalid))
		return
	}
	contentType := normalizedMediaType(r.Header.Get("Content-Type"))
	if exchange.ContentType != "" && contentType != normalizedMediaType(exchange.ContentType) {
		fail(fmt.Errorf("%w: upload content type does not match signed content type", managedagents.ErrInvalid))
		return
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, exchange.MaxSizeBytes+1))
	if err != nil {
		fail(err)
		return
	}
	if int64(len(content)) > exchange.MaxSizeBytes {
		fail(fmt.Errorf("%w: upload exceeds signed maximum size", managedagents.ErrInvalid))
		return
	}
	if exchange.ExpectedSizeBytes != nil && int64(len(content)) != *exchange.ExpectedSizeBytes {
		fail(fmt.Errorf("%w: upload size does not match signed size", managedagents.ErrInvalid))
		return
	}
	digest := sha256.Sum256(content)
	checksum := hex.EncodeToString(digest[:])
	if exchange.ExpectedChecksumSHA256 != "" && checksum != exchange.ExpectedChecksumSHA256 {
		fail(fmt.Errorf("%w: upload checksum does not match signed checksum", managedagents.ErrInvalid))
		return
	}
	bucket, err := objectstore.ResolveBucket("", s.defaultObjectStoreBucket())
	if err != nil {
		fail(err)
		return
	}
	objectKey := fmt.Sprintf("%s/%s/imports/%s-%s", exchange.WorkspaceID, exchange.SessionID, exchange.ID, safeArtifactFileName(exchange.Filename))
	putInput := objectstore.PutObjectInput{
		Bucket: bucket, Key: objectKey, Body: bytes.NewReader(content), ContentType: fallbackString(exchange.ContentType, contentType),
		SizeBytes: int64(len(content)), ChecksumSHA256: checksum,
		Metadata: map[string]string{"artifact-exchange-id": exchange.ID},
	}
	put, err := s.objectStore.PutObject(ctx, putInput)
	if err != nil {
		fail(err)
		return
	}
	objectBucket := fallbackString(put.Bucket, bucket)
	storedKey := fallbackString(put.Key, objectKey)
	completed, objectRef, artifact, err := store.CompleteArtifactImportContext(ctx, managedagents.CompleteArtifactImportInput{
		WorkspaceID: exchange.WorkspaceID, ID: exchange.ID, CompletedAt: time.Now().UTC(),
		ObjectRef: managedagents.CreateObjectRefInput{
			WorkspaceID: exchange.WorkspaceID, StorageProvider: objectstore.ProviderForClient(s.objectStore),
			Bucket: objectBucket, ObjectKey: storedKey, ObjectVersion: put.Version,
			ContentType: fallbackString(exchange.ContentType, contentType), SizeBytes: int64(len(content)),
			ChecksumSHA256: fallbackString(put.ChecksumSHA256, checksum), ETag: put.ETag,
			Visibility: exchange.Visibility, Metadata: exchange.Metadata, CreatedBy: exchange.CreatedBy,
		},
		Artifact: managedagents.CreateSessionArtifactInput{
			SessionID: exchange.SessionID, EnvironmentID: exchange.EnvironmentID,
			TurnID: exchange.TurnID, ToolCallID: exchange.ToolCallID, Name: exchange.Filename,
			Description: exchange.Description, ArtifactType: exchange.ArtifactType,
			Metadata: exchange.Metadata, CreatedBy: exchange.CreatedBy,
		},
	})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		cleanupErr := s.objectStore.DeleteObject(cleanupCtx, objectstore.DeleteObjectInput{Bucket: objectBucket, Key: storedKey, Version: put.Version})
		cancel()
		if cleanupErr != nil && !errors.Is(cleanupErr, objectstore.ErrNotFound) {
			err = errors.Join(err, fmt.Errorf("rollback imported object: %w", cleanupErr))
		}
		fail(err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, artifactExchangeImportResult{Exchange: completed, ObjectRef: objectRef, Artifact: artifact})
}

func (s *Server) claimPublicArtifactExchange(w http.ResponseWriter, r *http.Request, direction string) (managedagents.ArtifactExchangeContextStore, context.Context, managedagents.ArtifactExchange, bool) {
	store, ok := s.store.(managedagents.ArtifactExchangeContextStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "artifact_exchange_unavailable", "artifact exchange API is unavailable", false, nil)
		return nil, nil, managedagents.ArtifactExchange{}, false
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if workspaceID == "" || token == "" {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotFound, "not_found", "artifact exchange not found", false, nil)
		return nil, nil, managedagents.ArtifactExchange{}, false
	}
	ctx, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return nil, nil, managedagents.ArtifactExchange{}, false
	}
	tokenHash := sha256.Sum256([]byte(token))
	exchange, err := store.ClaimArtifactExchangeContext(ctx, managedagents.ClaimArtifactExchangeInput{
		WorkspaceID: workspaceID, ID: r.PathValue("exchange_id"), Direction: direction,
		TokenHash: tokenHash[:], ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotFound, "not_found", "artifact exchange not found", false, nil)
		return nil, nil, managedagents.ArtifactExchange{}, false
	}
	return store, ctx, exchange, true
}

func (s *Server) failClaimedArtifactExchange(ctx context.Context, store managedagents.ArtifactExchangeContextStore, id string, cause error) {
	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := store.FailArtifactExchangeContext(failCtx, id, time.Now().UTC(), cause.Error()); err != nil && !errors.Is(err, managedagents.ErrConflict) {
		s.logger.Warn("mark artifact exchange failed", "exchange_id", id, "error", err)
	}
}

func writeArtifactExchangeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, objectstore.ErrInvalid):
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
	case errors.Is(err, objectstore.ErrNotFound):
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotFound, "not_found", "artifact content not found", false, nil)
	case errors.Is(err, objectstore.ErrNotConfigured):
		writeV2Error(w, requestIDFromRequest(r), http.StatusServiceUnavailable, "object_store_unavailable", err.Error(), true, nil)
	default:
		writeV2ManagedError(w, r, err)
	}
}

func authorizeArtifactExchangeRequest(r *http.Request, exchange managedagents.ArtifactExchange) error {
	principal, ok := PrincipalFromRequest(r)
	if !ok {
		return nil
	}
	if err := authorizeWorkspacePrincipal(principal, exchange.WorkspaceID); err != nil {
		return err
	}
	if principal.HasRole(RoleOperator) || exchange.OwnerID == principal.OwnerID || exchange.AppID != "" && exchange.AppID == principal.ServiceIdentityID {
		return nil
	}
	return fmt.Errorf("%w: artifact exchange belongs to another owner", managedagents.ErrForbidden)
}

func artifactExchangeTTL(seconds int64) (time.Duration, error) {
	if seconds == 0 {
		return defaultArtifactExchangeTTL, nil
	}
	ttl := time.Duration(seconds) * time.Second
	if ttl < minArtifactExchangeTTL || ttl > maxArtifactExchangeTTL {
		return 0, fmt.Errorf("%w: ttl_seconds must be between %d and %d", managedagents.ErrInvalid, int64(minArtifactExchangeTTL/time.Second), int64(maxArtifactExchangeTTL/time.Second))
	}
	return ttl, nil
}

func newArtifactExchangeToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate artifact exchange token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

func artifactExchangeContentURL(exchange managedagents.ArtifactExchange, token string) string {
	query := url.Values{"workspace_id": {exchange.WorkspaceID}, "token": {token}}
	return "/v2/artifact-exchanges/" + url.PathEscape(exchange.ID) + "/content?" + query.Encode()
}

func normalizeArtifactExchangeMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, fmt.Errorf("%w: metadata must be a JSON object", managedagents.ErrInvalid)
	}
	return json.Marshal(value)
}

func validSHA256Hex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func normalizedMediaType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(value)
	}
	return strings.ToLower(mediaType)
}
