package skillretention

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	ScopeOrganization = "organization"
	ScopeWorkspace    = "workspace"
	SourceServer      = "server"

	PolicyStatusActive   = "active"
	PolicyStatusArchived = "archived"

	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusPartial   = "partial"
	RunStatusFailed    = "failed"

	ItemStatusCandidate = "candidate"
	ItemStatusDeleting  = "deleting"
	ItemStatusDeleted   = "deleted"
	ItemStatusSkipped   = "skipped"
	ItemStatusFailed    = "failed"

	ReasonArchivedRetention = "archived_retention_expired"
	ReasonOrphanedAsset     = "orphaned_skill_asset"
)

var (
	ErrDisabled = errors.New("skill asset retention is disabled")
	ErrConflict = errors.New("skill asset GC is already running")
	ErrInvalid  = errors.New("invalid skill asset retention input")
)

type Policy struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
	DeleteLimit   int  `json:"delete_limit"`
}

type PolicyRecord struct {
	ID             string     `json:"id"`
	ScopeType      string     `json:"scope_type"`
	OrganizationID string     `json:"organization_id,omitempty"`
	WorkspaceID    string     `json:"workspace_id,omitempty"`
	Status         string     `json:"status"`
	CurrentVersion int        `json:"current_version"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
}

type PolicyVersion struct {
	ID        string    `json:"id"`
	PolicyID  string    `json:"policy_id"`
	Version   int       `json:"version"`
	Config    Policy    `json:"config"`
	Checksum  string    `json:"checksum_sha256"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type EffectivePolicy struct {
	Source   string        `json:"source"`
	Policy   PolicyRecord  `json:"policy,omitempty"`
	Version  PolicyVersion `json:"version,omitempty"`
	Config   Policy        `json:"config"`
	Revision string        `json:"revision"`
}

type CreatePolicyInput struct {
	ScopeType      string
	OrganizationID string
	WorkspaceID    string
	Config         Policy
	CreatedBy      string
}

type ListPoliciesInput struct {
	OrganizationID  string
	WorkspaceID     string
	IncludeArchived bool
}

type Candidate struct {
	WorkspaceID     string          `json:"workspace_id"`
	SkillID         string          `json:"skill_id,omitempty"`
	SkillIdentifier string          `json:"skill_identifier,omitempty"`
	SkillVersionID  string          `json:"skill_version_id,omitempty"`
	SkillVersion    int             `json:"skill_version,omitempty"`
	AssetPath       string          `json:"asset_path,omitempty"`
	ObjectRefID     string          `json:"object_ref_id"`
	StorageProvider string          `json:"storage_provider"`
	Bucket          string          `json:"bucket"`
	ObjectKey       string          `json:"object_key"`
	ObjectVersion   string          `json:"object_version,omitempty"`
	ContentType     string          `json:"content_type,omitempty"`
	SizeBytes       int64           `json:"size_bytes"`
	ChecksumSHA256  string          `json:"checksum_sha256,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	ScanProvider    string          `json:"scan_provider,omitempty"`
	ScanVersion     string          `json:"scan_version,omitempty"`
	Reason          string          `json:"reason"`
	EligibleAt      time.Time       `json:"eligible_at"`
	ObjectCreatedAt time.Time       `json:"object_created_at"`
}

type ListCandidatesInput struct {
	WorkspaceID string
	Cutoff      time.Time
	Limit       int
	ObjectRefID string
}

type Preview struct {
	WorkspaceID    string          `json:"workspace_id"`
	Effective      EffectivePolicy `json:"effective_policy"`
	Cutoff         time.Time       `json:"cutoff"`
	CandidateCount int             `json:"candidate_count"`
	CandidateBytes int64           `json:"candidate_bytes"`
	Candidates     []Candidate     `json:"candidates"`
}

type Run struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	PolicySource   string     `json:"policy_source"`
	PolicyID       string     `json:"policy_id,omitempty"`
	PolicyVersion  int        `json:"policy_version,omitempty"`
	PolicyRevision string     `json:"policy_revision"`
	RetentionDays  int        `json:"retention_days"`
	DeleteLimit    int        `json:"delete_limit"`
	Status         string     `json:"status"`
	CandidateCount int        `json:"candidate_count"`
	DeletedCount   int        `json:"deleted_count"`
	SkippedCount   int        `json:"skipped_count"`
	FailedCount    int        `json:"failed_count"`
	BytesDeleted   int64      `json:"bytes_deleted"`
	RequestedBy    string     `json:"requested_by"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type Item struct {
	ID               string     `json:"id"`
	RunID            string     `json:"run_id"`
	Candidate        Candidate  `json:"candidate"`
	Status           string     `json:"status"`
	Reason           string     `json:"reason"`
	Attempts         int        `json:"attempts"`
	ObjectWasMissing bool       `json:"object_was_missing"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

type Tombstone struct {
	ID               string          `json:"id"`
	RunID            string          `json:"run_id"`
	WorkspaceID      string          `json:"workspace_id"`
	SkillID          string          `json:"skill_id,omitempty"`
	SkillVersionID   string          `json:"skill_version_id,omitempty"`
	AssetPath        string          `json:"asset_path,omitempty"`
	ObjectRefID      string          `json:"object_ref_id"`
	StorageProvider  string          `json:"storage_provider"`
	Bucket           string          `json:"bucket"`
	ObjectKey        string          `json:"object_key"`
	ObjectVersion    string          `json:"object_version,omitempty"`
	ContentType      string          `json:"content_type,omitempty"`
	SizeBytes        int64           `json:"size_bytes"`
	ChecksumSHA256   string          `json:"checksum_sha256,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	ScanProvider     string          `json:"scan_provider,omitempty"`
	ScanVersion      string          `json:"scan_version,omitempty"`
	Reason           string          `json:"reason"`
	ObjectWasMissing bool            `json:"object_was_missing"`
	DeletedBy        string          `json:"deleted_by"`
	DeletedAt        time.Time       `json:"deleted_at"`
}

type StartRunInput struct {
	WorkspaceID string
	Effective   EffectivePolicy
	RequestedBy string
	StartedAt   time.Time
	Candidates  []Candidate
}

type ListRunsInput struct {
	WorkspaceID string
	Limit       int
}

type ListTombstonesInput struct {
	WorkspaceID string
	Limit       int
}

type RunRequest struct {
	WorkspaceID string
	Limit       int
	RequestedBy string
}

type RunResult struct {
	Run   Run    `json:"run"`
	Items []Item `json:"items"`
}

type Store interface {
	CreateSkillAssetRetentionPolicy(context.Context, CreatePolicyInput) (PolicyRecord, PolicyVersion, error)
	GetSkillAssetRetentionPolicy(context.Context, string) (PolicyRecord, error)
	ListSkillAssetRetentionPolicies(context.Context, ListPoliciesInput) ([]PolicyRecord, error)
	PublishSkillAssetRetentionPolicyVersion(context.Context, string, Policy, string) (PolicyVersion, error)
	GetSkillAssetRetentionPolicyVersion(context.Context, string, int) (PolicyVersion, error)
	ArchiveSkillAssetRetentionPolicy(context.Context, string) (PolicyRecord, error)
	ResolveSkillAssetRetentionPolicy(context.Context, string) (EffectivePolicy, bool, error)

	ListSkillAssetGCCandidates(context.Context, ListCandidatesInput) ([]Candidate, error)
	AcquireSkillAssetGCLock(context.Context, string) (func() error, error)
	StartSkillAssetGCRun(context.Context, StartRunInput) (Run, []Item, error)
	ClaimSkillAssetGCItem(context.Context, string, time.Time) (Candidate, bool, string, error)
	SkipSkillAssetGCItem(context.Context, string, string) error
	FailSkillAssetGCItem(context.Context, string, string) error
	FinalizeSkillAssetGCItem(context.Context, string, string, bool) (Tombstone, error)
	FinishSkillAssetGCRun(context.Context, string) (Run, error)
	ListSkillAssetGCRuns(context.Context, ListRunsInput) ([]Run, error)
	GetSkillAssetGCRun(context.Context, string) (Run, []Item, error)
	ListSkillAssetGCTombstones(context.Context, ListTombstonesInput) ([]Tombstone, error)
	ListSkillAssetGCWorkspaceIDs(context.Context) ([]string, error)
}

type WorkspaceContextProvider interface {
	SkillAssetGCWorkspaceContext(context.Context, string) (context.Context, error)
}
