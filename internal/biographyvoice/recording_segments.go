package biographyvoice

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

type recordingSegmentUploadMetadata struct {
	Transcript          string `json:"transcript"`
	DurationMS          int64  `json:"durationMs"`
	CreatedAt           int64  `json:"createdAt"`
	TranscriptionStatus string `json:"transcriptionStatus"`
}

func (server *Server) recordingSegment(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	segments, ok := server.store.(recordingSegmentStore)
	if user == nil || !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "分段录音需要启用 OIDC 登录和存储服务"})
		return
	}
	recordingID := strings.TrimSpace(r.PathValue("recordingID"))
	segmentID := strings.TrimSpace(r.PathValue("segmentID"))
	if !validRecordingID(recordingID) || !validRecordingID(segmentID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "录音或分段标识不正确"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		server.downloadRecordingSegment(w, r, user.ID, recordingID, segmentID, segments)
	case http.MethodPut:
		server.uploadRecordingSegment(w, r, user.ID, recordingID, segmentID, segments)
	case http.MethodDelete:
		server.deleteRecordingSegment(w, user.ID, recordingID, segmentID, segments)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (server *Server) uploadRecordingSegment(w http.ResponseWriter, r *http.Request, userID string, recordingID string, segmentID string, store recordingSegmentStore) {
	if _, found, err := server.store.recordingForUser(userID, recordingID); err != nil {
		server.recordingError(w, "读取采访场次失败", err)
		return
	} else if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这次采访"})
		return
	}
	maxBytes := server.recordingMaxBytes()
	if r.ContentLength > maxBytes+2*1024*1024 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"message": "这一段录音太大，请缩短后再试"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+2*1024*1024)
	if err := r.ParseMultipartForm(2 * 1024 * 1024); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "分段录音上传格式不正确"})
		return
	}
	var metadata recordingSegmentUploadMetadata
	if err := json.Unmarshal([]byte(r.FormValue("metadata")), &metadata); err != nil || !validRecordingSegmentMetadata(metadata) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "分段录音信息不正确"})
		return
	}
	audio, header, err := r.FormFile("audio")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "没有收到这一段录音"})
		return
	}
	defer audio.Close()
	size, err := store.writeRecordingSegmentAudio(userID, recordingID, segmentID, audio, maxBytes)
	if errors.Is(err, errRecordingTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"message": "这一段录音太大，请缩短后再试"})
		return
	}
	if err != nil {
		server.recordingError(w, "保存分段录音失败", err)
		return
	}
	createdAt := time.Now().UTC()
	if metadata.CreatedAt > 0 {
		createdAt = time.UnixMilli(metadata.CreatedAt).UTC()
	}
	segment := storedRecordingSegment{
		ID: segmentID, Transcript: strings.TrimSpace(metadata.Transcript), DurationMS: metadata.DurationMS, CreatedAt: createdAt,
		SizeBytes: size, ContentType: recordingContentType(header.Header.Get("Content-Type"), header.Filename),
		TranscriptionStatus: valueOrDefault(metadata.TranscriptionStatus, "ready"),
	}
	if err := store.saveRecordingSegment(userID, recordingID, segment); err != nil {
		_ = store.removeRecordingSegmentAudio(userID, recordingID, segmentID)
		server.recordingError(w, "保存分段录音记录失败", err)
		return
	}
	writeJSON(w, http.StatusCreated, segment)
}

func (server *Server) downloadRecordingSegment(w http.ResponseWriter, r *http.Request, userID string, recordingID string, segmentID string, store recordingSegmentStore) {
	segment, found, err := store.recordingSegmentForUser(userID, recordingID, segmentID)
	if err != nil {
		server.recordingError(w, "读取分段录音记录失败", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段讲述"})
		return
	}
	file, err := store.openRecordingSegmentAudio(userID, recordingID, segmentID)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, objectstore.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段讲述"})
		return
	}
	if err != nil {
		server.recordingError(w, "读取分段录音文件失败", err)
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(file)
	if err != nil {
		server.recordingError(w, "读取分段录音文件失败", err)
		return
	}
	w.Header().Set("Content-Type", valueOrDefault(segment.ContentType, "audio/wav"))
	http.ServeContent(w, r, segmentID+".wav", segment.CreatedAt, bytes.NewReader(payload))
}

func (server *Server) deleteRecordingSegment(w http.ResponseWriter, userID string, recordingID string, segmentID string, store recordingSegmentStore) {
	_, found, err := store.recordingSegmentForUser(userID, recordingID, segmentID)
	if err != nil {
		server.recordingError(w, "读取分段录音记录失败", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "没有找到这段讲述"})
		return
	}
	if err := store.deleteRecordingSegment(userID, recordingID, segmentID); err != nil {
		server.recordingError(w, "删除分段录音失败", err)
		return
	}
	if err := store.removeRecordingSegmentAudio(userID, recordingID, segmentID); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, objectstore.ErrNotFound) {
		server.logger.Error("biography recording segment file removal failed", "recording_id", recordingID, "segment_id", segmentID, "error", server.safeProviderError(err))
	}
	w.WriteHeader(http.StatusNoContent)
}

func validRecordingSegmentMetadata(metadata recordingSegmentUploadMetadata) bool {
	if metadata.DurationMS < 0 || len([]rune(metadata.Transcript)) > 100_000 {
		return false
	}
	status := valueOrDefault(metadata.TranscriptionStatus, "ready")
	return status == "ready" || status == "needs_retry"
}
