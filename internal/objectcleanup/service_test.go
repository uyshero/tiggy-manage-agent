package objectcleanup

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

func TestServiceCompletesDeletedAndMissingObjects(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := &cleanupTestStore{claimed: []Job{
		{ID: "cleanup_1", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderLocalFS, Bucket: "artifacts", ObjectKey: "one", AttemptCount: 1},
		{ID: "cleanup_2", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderLocalFS, Bucket: "artifacts", ObjectKey: "missing", AttemptCount: 1},
	}}
	objects := &cleanupTestObjectStore{deleteErrors: map[string]error{"missing": objectstore.ErrNotFound}}
	service := newCleanupTestService(t, store, objects, now)

	result, err := service.RunWorkspace(context.Background(), "wksp")
	if err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if result.Claimed != 2 || result.Completed != 2 || len(store.completed) != 2 {
		t.Fatalf("unexpected cleanup result: %+v completed=%+v", result, store.completed)
	}
	if !store.completed[1].ObjectWasMissing {
		t.Fatalf("missing object should be recorded: %+v", store.completed[1])
	}
}

func TestServiceRetriesThenDeadLettersDeleteFailures(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	deleteErr := errors.New("storage unavailable")
	store := &cleanupTestStore{claimed: []Job{
		{ID: "cleanup_1", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderLocalFS, Bucket: "artifacts", ObjectKey: "retry", AttemptCount: 2},
		{ID: "cleanup_2", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderLocalFS, Bucket: "artifacts", ObjectKey: "dead", AttemptCount: 3},
	}}
	objects := &cleanupTestObjectStore{deleteErrors: map[string]error{"retry": deleteErr, "dead": deleteErr}}
	service := newCleanupTestService(t, store, objects, now)

	result, err := service.RunWorkspace(context.Background(), "wksp")
	if err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if result.Retried != 1 || result.DeadLetter != 1 || len(store.failed) != 2 {
		t.Fatalf("unexpected cleanup result: %+v failed=%+v", result, store.failed)
	}
	if store.failed[0].DeadLetter || !store.failed[1].DeadLetter {
		t.Fatalf("unexpected failure states: %+v", store.failed)
	}
	if got := store.failed[0].NextAttemptAt.Sub(now); got != 2*time.Second {
		t.Fatalf("expected exponential retry delay 2s, got %s", got)
	}
}

func TestServiceRejectsProviderMismatchWithoutDeleting(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := &cleanupTestStore{claimed: []Job{{
		ID: "cleanup_1", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderS3,
		Bucket: "artifacts", ObjectKey: "one", AttemptCount: 1,
	}}}
	objects := &cleanupTestObjectStore{}
	service := newCleanupTestService(t, store, objects, now)

	result, err := service.RunWorkspace(context.Background(), "wksp")
	if err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if result.Retried != 1 || len(objects.deleted) != 0 || len(store.failed) != 1 {
		t.Fatalf("provider mismatch should be retried without delete: result=%+v deleted=%+v failed=%+v", result, objects.deleted, store.failed)
	}
}

func TestServiceStagesOrphansBeforeClaimingCleanup(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := &cleanupTestStore{
		staged:  []Job{{ID: "cleanup_staged"}},
		claimed: []Job{{ID: "cleanup_staged", WorkspaceID: "wksp", StorageProvider: objectstore.ProviderLocalFS, Bucket: "artifacts", ObjectKey: "orphan", AttemptCount: 1}},
	}
	objects := &cleanupTestObjectStore{}
	service, err := NewService(store, objects, Config{
		WorkerID: "worker", BatchSize: 10, LeaseDuration: time.Minute,
		MaxAttempts: 3, RetryInitialDelay: time.Second, RetryMaxDelay: time.Minute,
		OrphanSweepEnabled: true, OrphanGracePeriod: 24 * time.Hour, OrphanSweepLimit: 25,
	})
	if err != nil {
		t.Fatalf("new cleanup service: %v", err)
	}
	service.now = func() time.Time { return now }

	result, err := service.RunWorkspace(context.Background(), "wksp")
	if err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if result.Staged != 1 || result.Claimed != 1 || result.Completed != 1 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	if len(store.stageInputs) != 1 || !store.stageInputs[0].Cutoff.Equal(now.Add(-24*time.Hour)) || store.stageInputs[0].Limit != 25 {
		t.Fatalf("unexpected orphan staging input: %+v", store.stageInputs)
	}
}

func newCleanupTestService(t *testing.T, store Store, objects objectstore.Client, now time.Time) *Service {
	t.Helper()
	service, err := NewService(store, objects, Config{
		WorkerID: "worker", BatchSize: 10, LeaseDuration: time.Minute,
		MaxAttempts: 3, RetryInitialDelay: time.Second, RetryMaxDelay: time.Minute,
	})
	if err != nil {
		t.Fatalf("new cleanup service: %v", err)
	}
	service.now = func() time.Time { return now }
	return service
}

type cleanupTestStore struct {
	staged      []Job
	stageInputs []StageInput
	claimed     []Job
	completed   []CompleteInput
	failed      []FailInput
}

func (s *cleanupTestStore) StageOrphanObjectCleanup(_ context.Context, input StageInput) ([]Job, error) {
	s.stageInputs = append(s.stageInputs, input)
	items := append([]Job(nil), s.staged...)
	s.staged = nil
	return items, nil
}

func (s *cleanupTestStore) EnqueueObjectCleanup(context.Context, EnqueueInput) (Job, error) {
	return Job{}, nil
}

func (s *cleanupTestStore) ClaimObjectCleanup(_ context.Context, input ClaimInput) ([]Job, error) {
	items := append([]Job(nil), s.claimed...)
	s.claimed = nil
	return items, nil
}

func (s *cleanupTestStore) CompleteObjectCleanup(_ context.Context, input CompleteInput) error {
	s.completed = append(s.completed, input)
	return nil
}

func (s *cleanupTestStore) FailObjectCleanup(_ context.Context, input FailInput) error {
	s.failed = append(s.failed, input)
	return nil
}

func (s *cleanupTestStore) ListObjectCleanupWorkspaceIDs(context.Context) ([]string, error) {
	return []string{"wksp"}, nil
}

type cleanupTestObjectStore struct {
	deleted      []objectstore.DeleteObjectInput
	deleteErrors map[string]error
}

func (s *cleanupTestObjectStore) Config() objectstore.Config {
	return objectstore.Config{Provider: objectstore.ProviderLocalFS}
}

func (s *cleanupTestObjectStore) PutObject(context.Context, objectstore.PutObjectInput) (objectstore.PutObjectResult, error) {
	return objectstore.PutObjectResult{}, errors.New("not implemented")
}

func (s *cleanupTestObjectStore) GetObject(context.Context, objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	return objectstore.GetObjectResult{Body: io.NopCloser(nil)}, errors.New("not implemented")
}

func (s *cleanupTestObjectStore) DeleteObject(_ context.Context, input objectstore.DeleteObjectInput) error {
	s.deleted = append(s.deleted, input)
	return s.deleteErrors[input.Key]
}

func (s *cleanupTestObjectStore) PresignGetObject(context.Context, objectstore.PresignGetObjectInput) (objectstore.PresignedURL, error) {
	return objectstore.PresignedURL{}, errors.New("not implemented")
}
