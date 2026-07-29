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
	Reason          string
	SafeToDelete    bool
	LastError       string
	CreatedAt       time.Time
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

type WorkspaceContextProvider interface {
	ObjectCleanupWorkspaceContext(context.Context, string) (context.Context, error)
}
