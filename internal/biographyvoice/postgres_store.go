package biographyvoice

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"tiggy-manage-agent/internal/objectstore"
)

// postgresBiographyStore is the production store. PostgreSQL owns metadata,
// ownership checks, audit history and project write serialization; recording
// bytes remain in object storage and never enter database rows.
type postgresBiographyStore struct {
	db      *sql.DB
	objects objectstore.Client
	bucket  string
}

func newPostgresBiographyStore(config Config) (*postgresBiographyStore, error) {
	db, err := sql.Open("pgx", strings.TrimSpace(config.DatabaseURL))
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect biography Postgres: %w", err)
	}
	objects, err := objectstore.NewClient(config.ObjectStore)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create biography object store: %w", err)
	}
	if _, err := objectstore.ResolveBucket(config.ObjectStore.Bucket, ""); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure biography object store: %w", err)
	}
	return &postgresBiographyStore{db: db, objects: objects, bucket: config.ObjectStore.Bucket}, nil
}

func (store *postgresBiographyStore) upsertOIDCUser(issuer string, subject string, displayName string, now time.Time) (storedUser, error) {
	user := storedUser{ID: stableUserID(issuer, subject), OIDCIssuer: strings.TrimSpace(issuer), OIDCSubject: strings.TrimSpace(subject), DisplayName: strings.TrimSpace(displayName), CreatedAt: now, LastLoginAt: now}
	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO biography_users (id, oidc_issuer, oidc_subject, display_name, created_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (oidc_issuer, oidc_subject) DO UPDATE
		SET display_name = EXCLUDED.display_name, last_login_at = EXCLUDED.last_login_at
	`, user.ID, user.OIDCIssuer, user.OIDCSubject, user.DisplayName, now)
	if err != nil {
		return storedUser{}, err
	}
	if err := store.audit(user.ID, "auth.login", "", "", nil); err != nil {
		return storedUser{}, err
	}
	return user, nil
}

func (store *postgresBiographyStore) userByID(userID string) (storedUser, bool, error) {
	var user storedUser
	err := store.db.QueryRowContext(context.Background(), `
		SELECT id, oidc_issuer, oidc_subject, display_name, created_at, last_login_at
		FROM biography_users WHERE id = $1
	`, userID).Scan(&user.ID, &user.OIDCIssuer, &user.OIDCSubject, &user.DisplayName, &user.CreatedAt, &user.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storedUser{}, false, nil
	}
	return user, err == nil, err
}

func (store *postgresBiographyStore) progressForUser(userID string) (BiographyProgress, bool, error) {
	var payload []byte
	err := store.db.QueryRowContext(context.Background(), `
		SELECT progress FROM biography_projects WHERE owner_id = $1 ORDER BY updated_at DESC LIMIT 1
	`, userID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return BiographyProgress{}, false, nil
	}
	if err != nil {
		return BiographyProgress{}, false, err
	}
	var progress BiographyProgress
	if err := json.Unmarshal(payload, &progress); err != nil {
		return BiographyProgress{}, false, fmt.Errorf("decode biography progress: %w", err)
	}
	return progress, true, nil
}

func (store *postgresBiographyStore) saveProgress(userID string, progress BiographyProgress) error {
	projectID := strings.TrimSpace(progress.Project.ID)
	if projectID == "" {
		return errors.New("biography project ID is required")
	}
	payload, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	// The advisory lock is shared by all gateway replicas and serializes
	// project writes without exposing a user-controlled lock key.
	lockKey := "biography:" + userID + ":" + projectID
	if _, err := transaction.ExecContext(context.Background(), `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, lockKey); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(context.Background(), `
		INSERT INTO biography_projects (owner_id, project_id, progress, updated_at)
		VALUES ($1, $2, $3::jsonb, $4)
		ON CONFLICT (owner_id, project_id) DO UPDATE SET progress = EXCLUDED.progress, updated_at = EXCLUDED.updated_at
	`, userID, projectID, payload, progress.UpdatedAt); err != nil {
		return err
	}
	if err := store.auditTx(transaction, userID, "project.progress_saved", projectID, "", map[string]any{"chapter_count": len(progress.Project.Chapters)}); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *postgresBiographyStore) listRecordings(userID string, projectID string) ([]storedRecording, error) {
	query := `SELECT recording_id, project_id, chapter_id, chapter_title, transcript, duration_ms, title, created_at, updated_at, size_bytes, content_type
		FROM biography_recording_sessions WHERE owner_id = $1`
	args := []any{userID}
	if projectID = strings.TrimSpace(projectID); projectID != "" {
		query += " AND project_id = $2"
		args = append(args, projectID)
	}
	query += " ORDER BY created_at DESC"
	rows, err := store.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recordings []storedRecording
	for rows.Next() {
		recording, err := scanStoredRecording(rows)
		if err != nil {
			return nil, err
		}
		recordings = append(recordings, recording)
	}
	return recordings, rows.Err()
}

func (store *postgresBiographyStore) recordingForUser(userID string, recordingID string) (storedRecording, bool, error) {
	recording, err := scanStoredRecording(store.db.QueryRowContext(context.Background(), `
		SELECT recording_id, project_id, chapter_id, chapter_title, transcript, duration_ms, title, created_at, updated_at, size_bytes, content_type
		FROM biography_recording_sessions WHERE owner_id = $1 AND recording_id = $2
	`, userID, recordingID))
	if errors.Is(err, sql.ErrNoRows) {
		return storedRecording{}, false, nil
	}
	return recording, err == nil, err
}

func (store *postgresBiographyStore) saveRecording(userID string, recording storedRecording) error {
	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO biography_recording_sessions (owner_id, recording_id, project_id, chapter_id, chapter_title, transcript, duration_ms, title, created_at, updated_at, size_bytes, content_type, object_bucket, object_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (owner_id, recording_id) DO UPDATE SET
		project_id = EXCLUDED.project_id, chapter_id = EXCLUDED.chapter_id, chapter_title = EXCLUDED.chapter_title,
		transcript = EXCLUDED.transcript, duration_ms = EXCLUDED.duration_ms, title = EXCLUDED.title,
		updated_at = EXCLUDED.updated_at, size_bytes = EXCLUDED.size_bytes, content_type = EXCLUDED.content_type,
		object_bucket = EXCLUDED.object_bucket, object_key = EXCLUDED.object_key
	`, userID, recording.ID, recording.ProjectID, recording.ChapterID, recording.ChapterTitle, recording.Transcript,
		recording.DurationMS, recording.Title, recording.CreatedAt, recording.UpdatedAt, recording.SizeBytes, recording.ContentType,
		store.bucket, store.recordingObjectKey(userID, recording.ID))
	if err != nil {
		return err
	}
	return store.audit(userID, "recording.saved", recording.ProjectID, recording.ID, map[string]any{"size_bytes": recording.SizeBytes})
}

func (store *postgresBiographyStore) deleteRecording(userID string, recordingID string) error {
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var projectID string
	err = transaction.QueryRowContext(context.Background(), `DELETE FROM biography_recording_sessions WHERE owner_id = $1 AND recording_id = $2 RETURNING project_id`, userID, recordingID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := store.auditTx(transaction, userID, "recording.deleted", projectID, recordingID, nil); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	return nil
}

func (store *postgresBiographyStore) writeRecordingAudio(userID string, recordingID string, source io.Reader, maxBytes int64) (int64, error) {
	payload, err := io.ReadAll(io.LimitReader(source, maxBytes+1))
	if err != nil {
		return 0, err
	}
	if int64(len(payload)) > maxBytes {
		return 0, errRecordingTooLarge
	}
	result, err := store.objects.PutObject(context.Background(), objectstore.PutObjectInput{
		Bucket: store.bucket, Key: store.recordingObjectKey(userID, recordingID), Body: bytes.NewReader(payload),
		ContentType: "audio/wav", SizeBytes: int64(len(payload)), Metadata: map[string]string{"owner_id": userID, "recording_id": recordingID},
	})
	if err != nil {
		return 0, err
	}
	return result.SizeBytes, nil
}

func (store *postgresBiographyStore) openRecordingAudio(userID string, recordingID string) (io.ReadCloser, error) {
	result, err := store.objects.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: store.bucket, Key: store.recordingObjectKey(userID, recordingID)})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func (store *postgresBiographyStore) removeRecordingAudio(userID string, recordingID string) error {
	return store.objects.DeleteObject(context.Background(), objectstore.DeleteObjectInput{Bucket: store.bucket, Key: store.recordingObjectKey(userID, recordingID)})
}

func (store *postgresBiographyStore) listRecordingSegments(userID string, recordingID string) ([]storedRecordingSegment, error) {
	rows, err := store.db.QueryContext(context.Background(), `
		SELECT segment_id, transcript, duration_ms, created_at, size_bytes, content_type, transcription_status
		FROM biography_recording_segments WHERE owner_id = $1 AND recording_id = $2 ORDER BY created_at
	`, userID, recordingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var segments []storedRecordingSegment
	for rows.Next() {
		segment, err := scanStoredRecordingSegment(rows)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, rows.Err()
}

func (store *postgresBiographyStore) recordingSegmentForUser(userID string, recordingID string, segmentID string) (storedRecordingSegment, bool, error) {
	segment, err := scanStoredRecordingSegment(store.db.QueryRowContext(context.Background(), `
		SELECT segment_id, transcript, duration_ms, created_at, size_bytes, content_type, transcription_status
		FROM biography_recording_segments WHERE owner_id = $1 AND recording_id = $2 AND segment_id = $3
	`, userID, recordingID, segmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return storedRecordingSegment{}, false, nil
	}
	return segment, err == nil, err
}

func (store *postgresBiographyStore) saveRecordingSegment(userID string, recordingID string, segment storedRecordingSegment) error {
	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO biography_recording_segments (owner_id, recording_id, segment_id, transcript, duration_ms, created_at, size_bytes, content_type, transcription_status, object_bucket, object_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (owner_id, recording_id, segment_id) DO UPDATE SET
		transcript = EXCLUDED.transcript, duration_ms = EXCLUDED.duration_ms, created_at = EXCLUDED.created_at,
		size_bytes = EXCLUDED.size_bytes, content_type = EXCLUDED.content_type,
		transcription_status = EXCLUDED.transcription_status, object_bucket = EXCLUDED.object_bucket, object_key = EXCLUDED.object_key
	`, userID, recordingID, segment.ID, segment.Transcript, segment.DurationMS, segment.CreatedAt, segment.SizeBytes,
		segment.ContentType, segment.TranscriptionStatus, store.bucket, store.recordingSegmentObjectKey(userID, recordingID, segment.ID))
	if err != nil {
		return err
	}
	return store.audit(userID, "recording.segment_saved", "", recordingID, map[string]any{"segment_id": segment.ID, "size_bytes": segment.SizeBytes})
}

func (store *postgresBiographyStore) deleteRecordingSegment(userID string, recordingID string, segmentID string) error {
	result, err := store.db.ExecContext(context.Background(), `DELETE FROM biography_recording_segments WHERE owner_id = $1 AND recording_id = $2 AND segment_id = $3`, userID, recordingID, segmentID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		return store.audit(userID, "recording.segment_deleted", "", recordingID, map[string]any{"segment_id": segmentID})
	}
	return nil
}

func (store *postgresBiographyStore) writeRecordingSegmentAudio(userID string, recordingID string, segmentID string, source io.Reader, maxBytes int64) (int64, error) {
	payload, err := io.ReadAll(io.LimitReader(source, maxBytes+1))
	if err != nil {
		return 0, err
	}
	if int64(len(payload)) > maxBytes {
		return 0, errRecordingTooLarge
	}
	result, err := store.objects.PutObject(context.Background(), objectstore.PutObjectInput{
		Bucket: store.bucket, Key: store.recordingSegmentObjectKey(userID, recordingID, segmentID), Body: bytes.NewReader(payload),
		ContentType: "audio/wav", SizeBytes: int64(len(payload)), Metadata: map[string]string{"owner_id": userID, "recording_id": recordingID, "segment_id": segmentID},
	})
	if err != nil {
		return 0, err
	}
	return result.SizeBytes, nil
}

func (store *postgresBiographyStore) openRecordingSegmentAudio(userID string, recordingID string, segmentID string) (io.ReadCloser, error) {
	result, err := store.objects.GetObject(context.Background(), objectstore.GetObjectInput{Bucket: store.bucket, Key: store.recordingSegmentObjectKey(userID, recordingID, segmentID)})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func (store *postgresBiographyStore) removeRecordingSegmentAudio(userID string, recordingID string, segmentID string) error {
	return store.objects.DeleteObject(context.Background(), objectstore.DeleteObjectInput{Bucket: store.bucket, Key: store.recordingSegmentObjectKey(userID, recordingID, segmentID)})
}

func (store *postgresBiographyStore) recordingObjectKey(userID string, recordingID string) string {
	return path.Join("biography", userID, "recordings", recordingID+".wav")
}

func (store *postgresBiographyStore) recordingSegmentObjectKey(userID string, recordingID string, segmentID string) string {
	return path.Join("biography", userID, "recordings", recordingID, "segments", segmentID+".wav")
}

type recordingScanner interface{ Scan(...any) error }

func scanStoredRecording(scanner recordingScanner) (storedRecording, error) {
	var recording storedRecording
	err := scanner.Scan(&recording.ID, &recording.ProjectID, &recording.ChapterID, &recording.ChapterTitle, &recording.Transcript,
		&recording.DurationMS, &recording.Title, &recording.CreatedAt, &recording.UpdatedAt, &recording.SizeBytes, &recording.ContentType)
	return recording, err
}

func scanStoredRecordingSegment(scanner recordingScanner) (storedRecordingSegment, error) {
	var segment storedRecordingSegment
	err := scanner.Scan(&segment.ID, &segment.Transcript, &segment.DurationMS, &segment.CreatedAt, &segment.SizeBytes, &segment.ContentType, &segment.TranscriptionStatus)
	return segment, err
}

func (store *postgresBiographyStore) audit(userID string, action string, projectID string, recordingID string, details map[string]any) error {
	return store.auditTx(nil, userID, action, projectID, recordingID, details)
}

func (store *postgresBiographyStore) auditTx(transaction *sql.Tx, userID string, action string, projectID string, recordingID string, details map[string]any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	id, err := randomID("bioaudit")
	if err != nil {
		return err
	}
	if transaction != nil {
		_, err = transaction.ExecContext(context.Background(), `INSERT INTO biography_audit_events (id, owner_id, action, project_id, recording_id, details, created_at) VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6::jsonb, $7)`, id, userID, action, projectID, recordingID, payload, time.Now().UTC())
	} else {
		_, err = store.db.ExecContext(context.Background(), `INSERT INTO biography_audit_events (id, owner_id, action, project_id, recording_id, details, created_at) VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6::jsonb, $7)`, id, userID, action, projectID, recordingID, payload, time.Now().UTC())
	}
	return err
}
