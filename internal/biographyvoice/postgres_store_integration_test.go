package biographyvoice

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

func TestPostgresBiographyStorePersistsUserOwnedProjectAndRecording(t *testing.T) {
	if os.Getenv("TMA_RUN_POSTGRES_TESTS") != "1" {
		t.Skip("set TMA_RUN_POSTGRES_TESTS=1 to run Postgres biography persistence integration tests")
	}
	databaseURL := os.Getenv("TMA_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TMA_DATABASE_URL to run Postgres biography persistence integration tests")
	}
	store, err := newPostgresBiographyStore(Config{
		DatabaseURL: databaseURL,
		ObjectStore: objectstore.Config{Provider: objectstore.ProviderLocalFS, Bucket: "biography-integration", RootDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("create Postgres biography store: %v", err)
	}
	issuer := "https://identity.integration.example/" + t.Name()
	user, err := store.upsertOIDCUser(issuer, "speaker", "采访测试用户", time.Now().UTC())
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec(`DELETE FROM biography_users WHERE id = $1`, user.ID)
		_ = store.db.Close()
	})

	project := newBiographyProject()
	project.ID = "project-integration"
	now := time.Now().UTC()
	progress := buildBiographyProgress(project, []string{"第一个问题"}, []string{"一段讲述"}, activeProgressSession{}, nil, now)
	if err := store.saveProgress(user.ID, progress); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	loaded, found, err := store.progressForUser(user.ID)
	if err != nil || !found {
		t.Fatalf("load progress: found=%v err=%v", found, err)
	}
	if loaded.Project.ID != project.ID || len(loaded.RecentQuestions) != 1 {
		t.Fatalf("unexpected loaded progress: %+v", loaded)
	}
	if _, found, err := store.progressForUser("another-user"); err != nil || found {
		t.Fatalf("progress leaked to another user: found=%v err=%v", found, err)
	}

	recordingID := "recording-integration-001"
	audio := []byte("RIFF-integration-audio")
	written, err := store.writeRecordingAudio(user.ID, recordingID, bytes.NewReader(audio), 1024)
	if err != nil || written != int64(len(audio)) {
		t.Fatalf("write recording audio: written=%d err=%v", written, err)
	}
	recording := storedRecording{
		ID: recordingID, ProjectID: project.ID, ChapterID: "chapter-1", ChapterTitle: "第一段故事",
		Transcript: "这是一次测试讲述。", DurationMS: 1_200, Title: "测试采访", CreatedAt: now, UpdatedAt: now,
		SizeBytes: written, ContentType: "audio/wav",
	}
	if err := store.saveRecording(user.ID, recording); err != nil {
		t.Fatalf("save recording metadata: %v", err)
	}
	segmentID := "segment-integration-001"
	segmentAudio := []byte("RIFF-integration-segment")
	segmentSize, err := store.writeRecordingSegmentAudio(user.ID, recordingID, segmentID, bytes.NewReader(segmentAudio), 1024)
	if err != nil || segmentSize != int64(len(segmentAudio)) {
		t.Fatalf("write recording segment: size=%d err=%v", segmentSize, err)
	}
	segment := storedRecordingSegment{ID: segmentID, Transcript: "第一段讲述。", DurationMS: 800, CreatedAt: now, SizeBytes: segmentSize, ContentType: "audio/wav", TranscriptionStatus: "ready"}
	if err := store.saveRecordingSegment(user.ID, recordingID, segment); err != nil {
		t.Fatalf("save recording segment: %v", err)
	}
	segments, err := store.listRecordingSegments(user.ID, recordingID)
	if err != nil || len(segments) != 1 || segments[0].ID != segmentID {
		t.Fatalf("list recording segments: segments=%+v err=%v", segments, err)
	}
	segmentReader, err := store.openRecordingSegmentAudio(user.ID, recordingID, segmentID)
	if err != nil {
		t.Fatalf("open recording segment: %v", err)
	}
	gotSegmentAudio, segmentReadErr := io.ReadAll(segmentReader)
	segmentCloseErr := segmentReader.Close()
	if segmentReadErr != nil || segmentCloseErr != nil || !bytes.Equal(gotSegmentAudio, segmentAudio) {
		t.Fatalf("unexpected segment bytes: %q read=%v close=%v", gotSegmentAudio, segmentReadErr, segmentCloseErr)
	}
	if err := store.deleteRecordingSegment(user.ID, recordingID, segmentID); err != nil {
		t.Fatalf("delete recording segment metadata: %v", err)
	}
	if err := store.removeRecordingSegmentAudio(user.ID, recordingID, segmentID); err != nil {
		t.Fatalf("delete recording segment object: %v", err)
	}
	items, err := store.listRecordings(user.ID, project.ID)
	if err != nil || len(items) != 1 || items[0].ID != recordingID {
		t.Fatalf("list recordings: items=%+v err=%v", items, err)
	}
	reader, err := store.openRecordingAudio(user.ID, recordingID)
	if err != nil {
		t.Fatalf("open recording audio: %v", err)
	}
	gotAudio, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(gotAudio, audio) {
		t.Fatalf("unexpected recording bytes: %q read=%v close=%v", gotAudio, readErr, closeErr)
	}
	if err := store.deleteRecording(user.ID, recordingID); err != nil {
		t.Fatalf("delete recording metadata: %v", err)
	}
	if err := store.removeRecordingAudio(user.ID, recordingID); err != nil {
		t.Fatalf("delete recording object: %v", err)
	}
	if _, found, err := store.recordingForUser(user.ID, recordingID); err != nil || found {
		t.Fatalf("recording survived delete: found=%v err=%v", found, err)
	}
	var auditCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM biography_audit_events WHERE owner_id = $1`, user.ID).Scan(&auditCount); err != nil || auditCount < 5 {
		t.Fatalf("expected audit events, count=%d err=%v", auditCount, err)
	}
}
