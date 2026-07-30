package objectcleanup

import (
	"context"
	"errors"
	"time"
)

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusBlocked    = "blocked"
	StatusDeadLetter = "dead_letter"

	ReasonObjectRefCreateFailed   = "object_ref_create_failed"
	ReasonArtifactCreateFailed    = "artifact_create_failed"
	ReasonObjectRefRollbackFailed = "object_ref_rollback_failed"
	ReasonUnsafeCustomKey         = "unsafe_custom_key"
	ReasonManagedObjectOrphaned   = "managed_object_orphaned"
)

var ErrInvalid = errors.New("invalid object cleanup input")

type Job struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	ObjectRefID      string     `json:"object_ref_id,omitempty"`
	StorageProvider  string     `json:"storage_provider"`
	Bucket           string     `json:"bucket"`
	ObjectKey        string     `json:"object_key"`
	ObjectVersion    string     `json:"object_version,omitempty"`
	SizeBytes        int64      `json:"size_bytes"`
	Reason           string     `json:"reason"`
	SafeToDelete     bool       `json:"safe_to_delete"`
	Status           string     `json:"status"`
	AttemptCount     int        `json:"attempt_count"`
	NextAttemptAt    time.Time  `json:"next_attempt_at"`
	LeaseOwner       string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	ObjectWasMissing bool       `json:"object_was_missing"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type EnqueueInput struct {
	WorkspaceID     string
	ObjectRefID     string
	StorageProvider string
	Bucket          string
	ObjectKey       string
	ObjectVersion   string
	SizeBytes       int64
	Reason          string
	SafeToDelete    bool
	LastError       string
	CreatedAt       time.Time
}

type ListInput struct {
	WorkspaceID string
	Status      string
	Reason      string
	CreatedFrom time.Time
	CreatedTo   time.Time
	Limit       int
}

type RetryInput struct {
	WorkspaceID string
	JobID       string
	Now         time.Time
}

type ApproveInput struct {
	WorkspaceID string
	JobID       string
	Now         time.Time
}

type StatusStats struct {
	Status         string `json:"status"`
	Jobs           int64  `json:"jobs"`
	Bytes          int64  `json:"bytes"`
	Attempts       int64  `json:"attempts"`
	RetriedJobs    int64  `json:"retried_jobs"`
	MissingObjects int64  `json:"missing_objects"`
	DeletedBytes   int64  `json:"deleted_bytes"`
}

type Stats struct {
	WorkspaceID       string        `json:"workspace_id"`
	Statuses          []StatusStats `json:"statuses"`
	OldestPendingAt   *time.Time    `json:"oldest_pending_at,omitempty"`
	OldestPendingAge  int64         `json:"oldest_pending_age_seconds"`
	OrphansStaged     int64         `json:"orphans_staged"`
	TotalAttempts     int64         `json:"total_attempts"`
	TotalRetriedJobs  int64         `json:"total_retried_jobs"`
	TotalDeletedBytes int64         `json:"total_deleted_bytes"`
}

type ClaimInput struct {
	WorkspaceID    string
	WorkerID       string
	Limit          int
	Now            time.Time
	LeaseExpiresAt time.Time
}

type StageInput struct {
	WorkspaceID string
	Cutoff      time.Time
	Limit       int
	Now         time.Time
}

type CompleteInput struct {
	WorkspaceID      string
	JobID            string
	WorkerID         string
	ObjectWasMissing bool
	CompletedAt      time.Time
}

type FailInput struct {
	WorkspaceID   string
	JobID         string
	WorkerID      string
	ErrorMessage  string
	NextAttemptAt time.Time
	DeadLetter    bool
	FailedAt      time.Time
}

type Enqueuer interface {
	EnqueueObjectCleanup(context.Context, EnqueueInput) (Job, error)
}

type Store interface {
	Enqueuer
	StageOrphanObjectCleanup(context.Context, StageInput) ([]Job, error)
	ClaimObjectCleanup(context.Context, ClaimInput) ([]Job, error)
	CompleteObjectCleanup(context.Context, CompleteInput) error
	FailObjectCleanup(context.Context, FailInput) error
	ListObjectCleanupWorkspaceIDs(context.Context) ([]string, error)
}

type OperationsStore interface {
	ListObjectCleanup(context.Context, ListInput) ([]Job, error)
	GetObjectCleanupStats(context.Context, string, time.Time) (Stats, error)
	RetryObjectCleanup(context.Context, RetryInput) (Job, error)
	ApproveBlockedObjectCleanup(context.Context, ApproveInput) (Job, error)
}

type WorkspaceContextProvider interface {
	ObjectCleanupWorkspaceContext(context.Context, string) (context.Context, error)
}
