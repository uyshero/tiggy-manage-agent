package managedagents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tiggy-manage-agent/internal/objectcleanup"
	"tiggy-manage-agent/internal/objectstore"
)

func TestPostgresStagesOnlyManagedUnlinkedObjectRefs(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "object-orphan-sweep")
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM object_refs WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Errorf("cleanup orphan sweep object refs: %v", err)
		}
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-48 * time.Hour)
	managedMetadata := json.RawMessage(`{"object_lifecycle":{"class":"managed"}}`)
	externalMetadata := json.RawMessage(`{"object_lifecycle":{"class":"external"}}`)

	createRef := func(key string, metadata json.RawMessage) ObjectRef {
		t.Helper()
		ref, err := store.CreateObjectRefContext(ctx, CreateObjectRefInput{
			WorkspaceID: workspaceID, StorageProvider: objectstore.ProviderLocalFS,
			Bucket: "artifacts", ObjectKey: workspaceID + "/" + key,
			Visibility: ObjectVisibilityWorkspace, Metadata: metadata,
		})
		if err != nil {
			t.Fatalf("create object ref %s: %v", key, err)
		}
		return ref
	}
	orphan := createRef("managed-orphan", managedMetadata)
	linked := createRef("managed-linked", managedMetadata)
	unmarked := createRef("unmarked", nil)
	external := createRef("external", externalMetadata)
	fresh := createRef("managed-fresh", managedMetadata)
	for _, ref := range []ObjectRef{orphan, linked, unmarked, external} {
		if _, err := store.db.ExecContext(context.Background(), `UPDATE object_refs SET created_at = $2 WHERE id = $1`, ref.ID, old); err != nil {
			t.Fatalf("age object ref %s: %v", ref.ID, err)
		}
	}
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role, created_at)
		VALUES ($1, $2, 'session_artifact', 'artifact_still_present', 'file', $3)
	`, linked.ID, workspaceID, old); err != nil {
		t.Fatalf("link managed object ref: %v", err)
	}
	blocked, err := store.EnqueueObjectCleanup(ctx, objectcleanup.EnqueueInput{
		WorkspaceID: workspaceID, ObjectRefID: external.ID, StorageProvider: external.StorageProvider,
		Bucket: external.Bucket, ObjectKey: external.ObjectKey, ObjectVersion: external.ObjectVersion,
		Reason: objectcleanup.ReasonUnsafeCustomKey, SafeToDelete: false,
		LastError: "custom key ownership unknown", CreatedAt: old,
	})
	if err != nil {
		t.Fatalf("enqueue blocked custom key: %v", err)
	}

	staged, err := store.StageOrphanObjectCleanup(ctx, objectcleanup.StageInput{
		WorkspaceID: workspaceID, Cutoff: now.Add(-24 * time.Hour), Limit: 10, Now: now,
	})
	if err != nil {
		t.Fatalf("stage orphan cleanup: %v", err)
	}
	if len(staged) != 1 || staged[0].ObjectRefID != orphan.ID || staged[0].Reason != objectcleanup.ReasonManagedObjectOrphaned || staged[0].Status != objectcleanup.StatusPending || !staged[0].SafeToDelete {
		t.Fatalf("unexpected staged cleanup: %+v", staged)
	}
	if _, err := store.GetObjectRefContext(ctx, orphan.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("staged orphan ref should be removed, got %v", err)
	}
	for _, ref := range []ObjectRef{linked, unmarked, external, fresh} {
		if _, err := store.GetObjectRefContext(ctx, ref.ID); err != nil {
			t.Fatalf("object ref %s should remain: %v", ref.ID, err)
		}
	}
	if stagedAgain, err := store.StageOrphanObjectCleanup(ctx, objectcleanup.StageInput{
		WorkspaceID: workspaceID, Cutoff: now.Add(-24 * time.Hour), Limit: 10, Now: now.Add(time.Second),
	}); err != nil || len(stagedAgain) != 0 {
		t.Fatalf("orphan staging must be idempotent: staged=%+v err=%v", stagedAgain, err)
	}
	var blockedStatus string
	if err := store.db.QueryRowContext(context.Background(), `SELECT status FROM object_cleanup_journal WHERE id = $1`, blocked.ID).Scan(&blockedStatus); err != nil {
		t.Fatalf("read blocked custom cleanup: %v", err)
	}
	if blockedStatus != objectcleanup.StatusBlocked {
		t.Fatalf("custom key cleanup should remain blocked, got %q", blockedStatus)
	}
}

func TestPostgresObjectCleanupJournalLifecycle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := createPostgresIntegrationWorkspace(t, store, "object-cleanup")
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)

	job, err := store.EnqueueObjectCleanup(ctx, objectcleanup.EnqueueInput{
		WorkspaceID: workspaceID, StorageProvider: objectstore.ProviderLocalFS,
		Bucket: "artifacts", ObjectKey: workspaceID + "/orphan.txt",
		Reason: objectcleanup.ReasonObjectRefCreateFailed, SafeToDelete: true,
		LastError: "initial delete failed", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue cleanup: %v", err)
	}
	if job.Status != objectcleanup.StatusPending || !job.SafeToDelete {
		t.Fatalf("unexpected pending job: %+v", job)
	}

	duplicate, err := store.EnqueueObjectCleanup(ctx, objectcleanup.EnqueueInput{
		WorkspaceID: workspaceID, StorageProvider: objectstore.ProviderLocalFS,
		Bucket: "artifacts", ObjectKey: workspaceID + "/orphan.txt",
		Reason: objectcleanup.ReasonArtifactCreateFailed, SafeToDelete: true,
		LastError: "second delete failed", CreatedAt: now.Add(time.Second),
	})
	if err != nil || duplicate.ID != job.ID || duplicate.LastError != "second delete failed" {
		t.Fatalf("active cleanup should be deduplicated: first=%+v duplicate=%+v err=%v", job, duplicate, err)
	}

	claimed, err := store.ClaimObjectCleanup(ctx, objectcleanup.ClaimInput{
		WorkspaceID: workspaceID, WorkerID: "worker-a", Limit: 10,
		Now: now.Add(2 * time.Second), LeaseExpiresAt: now.Add(time.Minute),
	})
	if err != nil || len(claimed) != 1 || claimed[0].Status != objectcleanup.StatusProcessing || claimed[0].AttemptCount != 1 {
		t.Fatalf("claim cleanup: jobs=%+v err=%v", claimed, err)
	}
	nextAttempt := now.Add(10 * time.Second)
	if err := store.FailObjectCleanup(ctx, objectcleanup.FailInput{
		WorkspaceID: workspaceID, JobID: job.ID, WorkerID: "worker-a",
		ErrorMessage: "still unavailable", NextAttemptAt: nextAttempt, FailedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("fail cleanup: %v", err)
	}
	claimed, err = store.ClaimObjectCleanup(ctx, objectcleanup.ClaimInput{
		WorkspaceID: workspaceID, WorkerID: "worker-b", Limit: 10,
		Now: nextAttempt.Add(-time.Second), LeaseExpiresAt: nextAttempt.Add(time.Minute),
	})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("cleanup should respect retry time: jobs=%+v err=%v", claimed, err)
	}
	claimed, err = store.ClaimObjectCleanup(ctx, objectcleanup.ClaimInput{
		WorkspaceID: workspaceID, WorkerID: "worker-b", Limit: 10,
		Now: nextAttempt, LeaseExpiresAt: nextAttempt.Add(time.Minute),
	})
	if err != nil || len(claimed) != 1 || claimed[0].AttemptCount != 2 {
		t.Fatalf("reclaim cleanup: jobs=%+v err=%v", claimed, err)
	}
	if err := store.CompleteObjectCleanup(ctx, objectcleanup.CompleteInput{
		WorkspaceID: workspaceID, JobID: job.ID, WorkerID: "worker-b",
		ObjectWasMissing: true, CompletedAt: nextAttempt.Add(time.Second),
	}); err != nil {
		t.Fatalf("complete cleanup: %v", err)
	}

	blocked, err := store.EnqueueObjectCleanup(ctx, objectcleanup.EnqueueInput{
		WorkspaceID: workspaceID, StorageProvider: objectstore.ProviderLocalFS,
		Bucket: "artifacts", ObjectKey: workspaceID + "/custom-key.txt",
		Reason: objectcleanup.ReasonUnsafeCustomKey, SafeToDelete: false,
		LastError: "custom key ownership unknown", CreatedAt: now,
	})
	if err != nil || blocked.Status != objectcleanup.StatusBlocked {
		t.Fatalf("enqueue blocked cleanup: job=%+v err=%v", blocked, err)
	}
	claimed, err = store.ClaimObjectCleanup(ctx, objectcleanup.ClaimInput{
		WorkspaceID: workspaceID, WorkerID: "worker-c", Limit: 10,
		Now: now.Add(time.Hour), LeaseExpiresAt: now.Add(2 * time.Hour),
	})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("blocked cleanup must not be claimed: jobs=%+v err=%v", claimed, err)
	}
}
