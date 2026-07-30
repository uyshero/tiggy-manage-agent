package biographyvoice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

const defaultRecordingMaxBytes int64 = 128 * 1024 * 1024

type recordingUploadMetadata struct {
	ProjectID    string `json:"projectID"`
	ChapterID    string `json:"chapterID"`
	ChapterTitle string `json:"chapterTitle"`
	Transcript   string `json:"transcript"`
	DurationMS   int64  `json:"durationMs"`
	Title        string `json:"title"`
	CreatedAt    int64  `json:"createdAt"`
	SizeBytes    int64  `json:"sizeBytes"`
}

type recordingTitleUpdate struct {
	Title string `json:"title"`
}

func (server *Server) recordings(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user == nil || server.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "录音备份需要启用 OIDC 登录"})
		return
	}
	recordings, err := server.store.listRecordings(user.ID, strings.TrimSpace(r.URL.Query().Get("project_id")))
	if err != nil {
		server.recordingError(w, "读取录音记录失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recordings": recordings})
}

func (server *Server) recordingAudio(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user == nil || server.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "录音备份需要启用 OIDC 登录"})
		return
	}
	recordingID := strings.TrimSpace(r.PathValue("recordingID"))
	if !validRecordingID(recordingID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音标识不正确"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		server.downloadRecordingAudio(w, r, user.ID, recordingID)
	case http.MethodPut, http.MethodPost:
		server.uploadRecordingAudio(w, r, user.ID, recordingID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (server *Server) recording(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user == nil || server.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "录音备份需要启用 OIDC 登录"})
		return
	}
	recordingID := strings.TrimSpace(r.PathValue("recordingID"))
	if !validRecordingID(recordingID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音标识不正确"})
		return
	}
	switch r.Method {
	case http.MethodPut:
		server.upsertRecordingMetadata(w, r, user.ID, recordingID)
	case http.MethodPatch:
		server.renameRecording(w, r, user.ID, recordingID)
	case http.MethodDelete:
		server.deleteRecording(w, user.ID, recordingID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (server *Server) upsertRecordingMetadata(w http.ResponseWriter, r *http.Request, userID string, recordingID string) {
	defer r.Body.Close()
	var metadata recordingUploadMetadata
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&metadata); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "采访场次信息不正确"})
		return
	}
	if err := validateRecordingMetadata(metadata); err != nil || metadata.SizeBytes < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "采访场次信息不正确"})
		return
	}
	now := time.Now().UTC()
	createdAt := now
	if metadata.CreatedAt > 0 {
		createdAt = time.UnixMilli(metadata.CreatedAt).UTC()
	}
	recording := storedRecording{
		ID: recordingID, ProjectID: strings.TrimSpace(metadata.ProjectID), ChapterID: strings.TrimSpace(metadata.ChapterID),
		ChapterTitle: strings.TrimSpace(metadata.ChapterTitle), Transcript: strings.TrimSpace(metadata.Transcript),
		DurationMS: metadata.DurationMS, Title: strings.TrimSpace(metadata.Title), CreatedAt: createdAt,
		UpdatedAt: now, SizeBytes: metadata.SizeBytes, ContentType: "audio/wav",
	}
	if existing, found, err := server.store.recordingForUser(userID, recordingID); err != nil {
		server.recordingError(w, "保存采访场次失败", err)
		return
	} else if found && recording.Title == "" {
		recording.Title = existing.Title
	}
	if err := server.store.saveRecording(userID, recording); err != nil {
		server.recordingError(w, "保存采访场次失败", err)
		return
	}
	writeJSON(w, http.StatusOK, recording)
}

func (server *Server) uploadRecordingAudio(w http.ResponseWriter, r *http.Request, userID string, recordingID string) {
	maxBytes := server.recordingMaxBytes()
	if r.ContentLength > maxBytes+2*1024*1024 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"message": "录音文件太大，请在设置中联系管理员调整上限"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+2*1024*1024)
	if err := r.ParseMultipartForm(2 * 1024 * 1024); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音上传格式不正确"})
		return
	}
	var metadata recordingUploadMetadata
	if err := json.Unmarshal([]byte(r.FormValue("metadata")), &metadata); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音信息不正确"})
		return
	}
	if err := validateRecordingMetadata(metadata); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	audio, header, err := r.FormFile("audio")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "没有收到录音文件"})
		return
	}
	defer audio.Close()

	contentType := recordingContentType(header.Header.Get("Content-Type"), header.Filename)
	size, err := server.store.writeRecordingAudio(userID, recordingID, audio, maxBytes)
	if err != nil {
		if errors.Is(err, errRecordingTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"message": "录音文件太大，请在设置中联系管理员调整上限"})
			return
		}
		server.recordingError(w, "保存录音文件失败", err)
		return
	}
	now := time.Now().UTC()
	createdAt := now
	if metadata.CreatedAt > 0 {
		createdAt = time.UnixMilli(metadata.CreatedAt).UTC()
	}
	recording := storedRecording{
		ID: recordingID, ProjectID: strings.TrimSpace(metadata.ProjectID), ChapterID: strings.TrimSpace(metadata.ChapterID),
		ChapterTitle: strings.TrimSpace(metadata.ChapterTitle), Transcript: strings.TrimSpace(metadata.Transcript),
		DurationMS: metadata.DurationMS, Title: strings.TrimSpace(metadata.Title), CreatedAt: createdAt,
		UpdatedAt: now, SizeBytes: size, ContentType: contentType,
	}
	if existing, found, err := server.store.recordingForUser(userID, recordingID); err != nil {
		server.recordingError(w, "保存录音记录失败", err)
		return
	} else if found && recording.Title == "" {
		recording.Title = existing.Title
	}
	if err := server.store.saveRecording(userID, recording); err != nil {
		_ = server.store.removeRecordingAudio(userID, recordingID)
		server.recordingError(w, "保存录音记录失败", err)
		return
	}
	writeJSON(w, http.StatusCreated, recording)
}

func (server *Server) downloadRecordingAudio(w http.ResponseWriter, r *http.Request, userID string, recordingID string) {
	recording, found, err := server.store.recordingForUser(userID, recordingID)
	if err != nil {
		server.recordingError(w, "读取录音记录失败", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段录音"})
		return
	}
	file, err := server.store.openRecordingAudio(userID, recordingID)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, objectstore.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段录音"})
		return
	}
	if err != nil {
		server.recordingError(w, "读取录音文件失败", err)
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(file)
	if err != nil {
		server.recordingError(w, "读取录音文件失败", err)
		return
	}
	if contentType := strings.TrimSpace(recording.ContentType); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", recordingID+".wav"))
	http.ServeContent(w, r, recordingID+".wav", recording.UpdatedAt, bytes.NewReader(payload))
}

func (server *Server) renameRecording(w http.ResponseWriter, r *http.Request, userID string, recordingID string) {
	defer r.Body.Close()
	var update recordingTitleUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&update); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音名称不正确"})
		return
	}
	update.Title = strings.TrimSpace(update.Title)
	if update.Title == "" || len([]rune(update.Title)) > 120 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音名称需要在 1 到 120 个字之间"})
		return
	}
	recording, found, err := server.store.recordingForUser(userID, recordingID)
	if err != nil {
		server.recordingError(w, "读取录音记录失败", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段录音"})
		return
	}
	recording.Title = update.Title
	recording.UpdatedAt = time.Now().UTC()
	if err := server.store.saveRecording(userID, recording); err != nil {
		server.recordingError(w, "修改录音名称失败", err)
		return
	}
	writeJSON(w, http.StatusOK, recording)
}

func (server *Server) deleteRecording(w http.ResponseWriter, userID string, recordingID string) {
	_, found, err := server.store.recordingForUser(userID, recordingID)
	if err != nil {
		server.recordingError(w, "读取录音记录失败", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段录音"})
		return
	}
	var segments []storedRecordingSegment
	segmentStore, hasSegments := server.store.(recordingSegmentStore)
	if hasSegments {
		segments, err = segmentStore.listRecordingSegments(userID, recordingID)
		if err != nil {
			server.recordingError(w, "读取分段录音记录失败", err)
			return
		}
	}
	if err := server.store.deleteRecording(userID, recordingID); err != nil {
		server.recordingError(w, "删除录音记录失败", err)
		return
	}
	if err := server.store.removeRecordingAudio(userID, recordingID); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, objectstore.ErrNotFound) {
		server.logger.Error("biography recording file removal failed", "recording_id", recordingID, "error", server.safeProviderError(err))
	}
	if hasSegments {
		for _, segment := range segments {
			if err := segmentStore.removeRecordingSegmentAudio(userID, recordingID, segment.ID); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, objectstore.ErrNotFound) {
				server.logger.Error("biography recording segment file removal failed", "recording_id", recordingID, "segment_id", segment.ID, "error", server.safeProviderError(err))
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) recordingMaxBytes() int64 {
	if server.config.RecordingMaxBytes > 0 {
		return server.config.RecordingMaxBytes
	}
	return defaultRecordingMaxBytes
}

func (server *Server) recordingError(w http.ResponseWriter, message string, err error) {
	server.logger.Error(message, "error", server.safeProviderError(err))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"message": message})
}

func validateRecordingMetadata(metadata recordingUploadMetadata) error {
	if strings.TrimSpace(metadata.ProjectID) == "" {
		return errors.New("录音缺少所属书籍")
	}
	if metadata.DurationMS < 0 {
		return errors.New("录音时长不正确")
	}
	if len([]rune(metadata.Transcript)) > 100_000 || len([]rune(metadata.Title)) > 120 || len([]rune(metadata.ChapterTitle)) > 120 {
		return errors.New("录音信息太长")
	}
	return nil
}

func validRecordingID(recordingID string) bool {
	if len(recordingID) < 8 || len(recordingID) > 128 {
		return false
	}
	for _, char := range recordingID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func recordingContentType(raw string, filename string) string {
	contentType := strings.TrimSpace(strings.Split(raw, ";")[0])
	if contentType != "" && contentType != "application/octet-stream" {
		return contentType
	}
	if guessed := mime.TypeByExtension(filepath.Ext(filename)); guessed != "" {
		return strings.TrimSpace(strings.Split(guessed, ";")[0])
	}
	return "audio/wav"
}

var errRecordingTooLarge = errors.New("biography recording is too large")

func (store *biographyDataStore) listRecordings(userID string, projectID string) ([]storedRecording, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	items := store.data.Recordings[userID]
	result := make([]storedRecording, 0, len(items))
	for _, recording := range items {
		if projectID == "" || recording.ProjectID == projectID {
			result = append(result, recording)
		}
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].CreatedAt.After(result[right].CreatedAt) })
	return result, nil
}

func (store *biographyDataStore) recordingForUser(userID string, recordingID string) (storedRecording, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	recording, found := store.data.Recordings[userID][recordingID]
	return recording, found, nil
}

func (store *biographyDataStore) saveRecording(userID string, recording storedRecording) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.data.Recordings == nil {
		store.data.Recordings = map[string]map[string]storedRecording{}
	}
	if store.data.Recordings[userID] == nil {
		store.data.Recordings[userID] = map[string]storedRecording{}
	}
	store.data.Recordings[userID][recording.ID] = recording
	return store.saveLocked()
}

func (store *biographyDataStore) deleteRecording(userID string, recordingID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if recordings := store.data.Recordings[userID]; recordings != nil {
		delete(recordings, recordingID)
		if len(recordings) == 0 {
			delete(store.data.Recordings, userID)
		}
	}
	if segments := store.data.RecordingSegments[userID]; segments != nil {
		delete(segments, recordingID)
		if len(segments) == 0 {
			delete(store.data.RecordingSegments, userID)
		}
	}
	return store.saveLocked()
}

func (store *biographyDataStore) recordingAudioPath(userID string, recordingID string) string {
	return filepath.Join(filepath.Dir(store.path), "recordings", userID, recordingID+".wav")
}

func (store *biographyDataStore) writeRecordingAudio(userID string, recordingID string, source io.Reader, maxBytes int64) (int64, error) {
	path := store.recordingAudioPath(userID, recordingID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return 0, closeErr
	}
	if written > maxBytes {
		_ = os.Remove(temporary)
		return 0, errRecordingTooLarge
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	return written, nil
}

func (store *biographyDataStore) openRecordingAudio(userID string, recordingID string) (io.ReadCloser, error) {
	return os.Open(store.recordingAudioPath(userID, recordingID))
}

func (store *biographyDataStore) removeRecordingAudio(userID string, recordingID string) error {
	if err := os.Remove(store.recordingAudioPath(userID, recordingID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(filepath.Dir(store.recordingSegmentAudioPath(userID, recordingID, "placeholder")))
}

func (store *biographyDataStore) listRecordingSegments(userID string, recordingID string) ([]storedRecordingSegment, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	segments := store.data.RecordingSegments[userID][recordingID]
	result := make([]storedRecordingSegment, 0, len(segments))
	for _, segment := range segments {
		result = append(result, segment)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].CreatedAt.Before(result[right].CreatedAt) })
	return result, nil
}

func (store *biographyDataStore) recordingSegmentForUser(userID string, recordingID string, segmentID string) (storedRecordingSegment, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	segment, found := store.data.RecordingSegments[userID][recordingID][segmentID]
	return segment, found, nil
}

func (store *biographyDataStore) saveRecordingSegment(userID string, recordingID string, segment storedRecordingSegment) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.data.RecordingSegments == nil {
		store.data.RecordingSegments = map[string]map[string]map[string]storedRecordingSegment{}
	}
	if store.data.RecordingSegments[userID] == nil {
		store.data.RecordingSegments[userID] = map[string]map[string]storedRecordingSegment{}
	}
	if store.data.RecordingSegments[userID][recordingID] == nil {
		store.data.RecordingSegments[userID][recordingID] = map[string]storedRecordingSegment{}
	}
	store.data.RecordingSegments[userID][recordingID][segment.ID] = segment
	return store.saveLocked()
}

func (store *biographyDataStore) deleteRecordingSegment(userID string, recordingID string, segmentID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if segments := store.data.RecordingSegments[userID][recordingID]; segments != nil {
		delete(segments, segmentID)
		if len(segments) == 0 {
			delete(store.data.RecordingSegments[userID], recordingID)
		}
	}
	return store.saveLocked()
}

func (store *biographyDataStore) recordingSegmentAudioPath(userID string, recordingID string, segmentID string) string {
	return filepath.Join(filepath.Dir(store.path), "recordings", userID, recordingID, "segments", segmentID+".wav")
}

func (store *biographyDataStore) writeRecordingSegmentAudio(userID string, recordingID string, segmentID string, source io.Reader, maxBytes int64) (int64, error) {
	path := store.recordingSegmentAudioPath(userID, recordingID, segmentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return 0, copyErr
		}
		return 0, closeErr
	}
	if written > maxBytes {
		_ = os.Remove(temporary)
		return 0, errRecordingTooLarge
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	return written, nil
}

func (store *biographyDataStore) openRecordingSegmentAudio(userID string, recordingID string, segmentID string) (io.ReadCloser, error) {
	return os.Open(store.recordingSegmentAudioPath(userID, recordingID, segmentID))
}

func (store *biographyDataStore) removeRecordingSegmentAudio(userID string, recordingID string, segmentID string) error {
	return os.Remove(store.recordingSegmentAudioPath(userID, recordingID, segmentID))
}
