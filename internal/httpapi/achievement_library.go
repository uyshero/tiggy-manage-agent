package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

func (s *Server) achievementLibraryStore(w http.ResponseWriter) (managedagents.AchievementLibraryStore, bool) {
	store, ok := s.store.(managedagents.AchievementLibraryStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "achievement library is unavailable"})
	}
	return store, ok
}

func (s *Server) createAchievementLibraryItem(w http.ResponseWriter, r *http.Request) {
	store, ok := s.achievementLibraryStore(w)
	if !ok {
		return
	}
	sessionID := r.PathValue("session_id")
	artifact, err := managedagents.GetSessionArtifactWithContext(r.Context(), s.store, sessionID, r.PathValue("artifact_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var request struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Directory   string   `json:"directory"`
		Tags        []string `json:"tags"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	item, err := store.CreateAchievementLibraryItemContext(r.Context(), managedagents.CreateAchievementLibraryItemInput{
		WorkspaceID: artifact.WorkspaceID, ObjectRefID: artifact.ObjectRefID,
		SourceSessionID: sessionID, SourceArtifactID: artifact.ID,
		Name: fallbackString(strings.TrimSpace(request.Name), artifact.Name), Description: request.Description,
		Directory: request.Directory, Tags: request.Tags, CreatedBy: requestActorID(r, artifact.CreatedBy),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listAchievementLibraryItems(w http.ResponseWriter, r *http.Request) {
	store, ok := s.achievementLibraryStore(w)
	if !ok {
		return
	}
	items, err := store.ListAchievementLibraryItemsContext(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNilSlice(items)})
}

func (s *Server) updateAchievementLibraryItem(w http.ResponseWriter, r *http.Request) {
	store, ok := s.achievementLibraryStore(w)
	if !ok {
		return
	}
	var input managedagents.UpdateAchievementLibraryItemInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	input.UpdatedBy = requestActorID(r, input.UpdatedBy)
	item, err := store.UpdateAchievementLibraryItemContext(r.Context(), r.PathValue("item_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteAchievementLibraryItem(w http.ResponseWriter, r *http.Request) {
	store, ok := s.achievementLibraryStore(w)
	if !ok {
		return
	}
	if err := store.DeleteAchievementLibraryItemContext(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), r.PathValue("item_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) referenceAchievementLibraryItem(w http.ResponseWriter, r *http.Request) {
	store, ok := s.achievementLibraryStore(w)
	if !ok {
		return
	}
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	session, err := s.getSessionForRequest(r, strings.TrimSpace(request.SessionID))
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.GetAchievementLibraryItemContext(r.Context(), session.WorkspaceID, r.PathValue("item_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if item.WorkspaceID != session.WorkspaceID {
		writeError(w, fmt.Errorf("%w: achievement belongs to another workspace", managedagents.ErrForbidden))
		return
	}
	metadata, _ := json.Marshal(map[string]any{"source": "achievement_library", "achievement_library_id": item.ID, "directory": item.Directory, "tags": item.Tags})
	artifact, err := managedagents.CreateSessionArtifactWithContext(r.Context(), s.store, managedagents.CreateSessionArtifactInput{
		WorkspaceID: session.WorkspaceID, SessionID: session.ID, EnvironmentID: session.EnvironmentID,
		ObjectRefID: item.ObjectRefID, Name: item.Name, Description: item.Description,
		ArtifactType: managedagents.ArtifactTypeFile, Metadata: metadata, CreatedBy: requestActorID(r, "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	objectRef, err := managedagents.GetObjectRefWithContext(r.Context(), s.store, item.ObjectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": item, "artifact": artifact, "object_ref": objectRef, "workspace_path": capability.SessionArtifactSandboxPath(artifact)})
}

func (s *Server) downloadAchievementLibraryItem(w http.ResponseWriter, r *http.Request) {
	store, ok := s.achievementLibraryStore(w)
	if !ok {
		return
	}
	item, err := store.GetAchievementLibraryItemContext(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), r.PathValue("item_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	objectRef, err := managedagents.GetObjectRefWithContext(r.Context(), s.store, item.ObjectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	object, err := s.objectStore.GetObject(r.Context(), objectstore.GetObjectInput{Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion})
	if err != nil {
		writeError(w, err)
		return
	}
	defer object.Body.Close()
	contentType := fallbackString(object.ContentType, objectRef.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Content-Disposition", contentDispositionAttachment(item.Name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("achievement download copy failed", "item_id", item.ID, "error", err)
	}
}
