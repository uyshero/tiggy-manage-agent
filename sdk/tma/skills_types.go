package tma

import (
	"bytes"
	"encoding/json"
	"time"
)

type Skill struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspace_id"`
	Identifier        string     `json:"identifier"`
	Title             string     `json:"title"`
	Description       string     `json:"description,omitempty"`
	OwnerType         string     `json:"owner_type"`
	OwnerID           string     `json:"owner_id"`
	Visibility        string     `json:"visibility"`
	ForkedFromSkillID string     `json:"forked_from_skill_id,omitempty"`
	ForkedFromVersion int        `json:"forked_from_version,omitempty"`
	SourcePluginID    string     `json:"source_plugin_id,omitempty"`
	SourceType        string     `json:"source_type"`
	SourceLocator     string     `json:"source_locator,omitempty"`
	SourcePath        string     `json:"source_path,omitempty"`
	Status            string     `json:"status"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	ArchivedAt        *time.Time `json:"archived_at,omitempty"`
}

type CreateSkillRequest struct {
	WorkspaceID    string `json:"workspace_id,omitempty"`
	Identifier     string `json:"identifier"`
	Title          string `json:"title"`
	Description    string `json:"description,omitempty"`
	OwnerType      string `json:"owner_type,omitempty"`
	OwnerID        string `json:"owner_id,omitempty"`
	Visibility     string `json:"visibility,omitempty"`
	SourcePluginID string `json:"source_plugin_id,omitempty"`
	SourceType     string `json:"source_type,omitempty"`
	SourceLocator  string `json:"source_locator,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
}

type SkillListQuery struct {
	WorkspaceID     string
	IncludeArchived bool
}

type SkillVersion struct {
	ID                 string                `json:"id"`
	SkillID            string                `json:"skill_id"`
	Version            int32                 `json:"version"`
	ContentFormat      string                `json:"content_format"`
	Manifest           SkillManifest         `json:"manifest"`
	ContentText        string                `json:"content_text"`
	Assets             *SkillAssetBundle     `json:"assets,omitempty"`
	ChecksumSHA256     string                `json:"checksum_sha256"`
	SourceRef          string                `json:"source_ref,omitempty"`
	SourceRevision     string                `json:"source_revision,omitempty"`
	SourceURL          string                `json:"source_url,omitempty"`
	PackageFormat      string                `json:"package_format"`
	PackageRoot        string                `json:"package_root,omitempty"`
	PackageChecksum    string                `json:"package_checksum_sha256,omitempty"`
	PackageObjectRefID string                `json:"package_object_ref_id,omitempty"`
	SkillMDObjectRefID string                `json:"skill_md_object_ref_id,omitempty"`
	PackageManifest    *SkillPackageManifest `json:"package_manifest,omitempty"`
	CreatedBy          string                `json:"created_by"`
	CreatedAt          time.Time             `json:"created_at"`
}

type CreateSkillVersionRequest struct {
	ContentFormat  string            `json:"content_format,omitempty"`
	Manifest       SkillManifest     `json:"manifest"`
	ContentText    string            `json:"content_text"`
	Assets         *SkillAssetBundle `json:"assets,omitempty"`
	SourceRef      string            `json:"source_ref,omitempty"`
	SourceRevision string            `json:"source_revision,omitempty"`
	SourceURL      string            `json:"source_url,omitempty"`
}

type SkillManifest struct {
	SystemRole   string               `json:"system_role,omitempty"`
	Blocks       []SkillManifestBlock `json:"blocks,omitempty"`
	InputsSchema json.RawMessage      `json:"inputs_schema,omitempty"`
}

type SkillManifestBlock struct {
	Type    string   `json:"type"`
	Title   string   `json:"title,omitempty"`
	Content string   `json:"content,omitempty"`
	Items   []string `json:"items,omitempty"`
}

type SkillAssetBundle struct {
	Files      []SkillAssetFile `json:"files"`
	TotalBytes int32            `json:"total_bytes"`
	Warnings   []string         `json:"warnings,omitempty"`
	SBOM       *SkillAssetSBOM  `json:"sbom,omitempty"`
}

func (b *SkillAssetBundle) UnmarshalJSON(raw []byte) error {
	type bundleAlias SkillAssetBundle
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var bundle bundleAlias
		if err := json.Unmarshal(trimmed, &bundle); err != nil {
			return err
		}
		if bundle.Files == nil {
			bundle.Files = []SkillAssetFile{}
		}
		*b = SkillAssetBundle(bundle)
		return nil
	}
	var files []SkillAssetFile
	if err := json.Unmarshal(trimmed, &files); err != nil {
		return err
	}
	if files == nil {
		files = []SkillAssetFile{}
	}
	*b = SkillAssetBundle{Files: files}
	return nil
}

type SkillAssetFile struct {
	Path           string `json:"path"`
	Content        string `json:"content,omitempty"`
	ContentBase64  string `json:"content_base64,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
	ScanStatus     string `json:"scan_status,omitempty"`
	ScanProvider   string `json:"scan_provider,omitempty"`
	ScanVersion    string `json:"scan_version,omitempty"`
	Size           int32  `json:"size"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	Executable     bool   `json:"executable,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
}

type SkillAssetSBOM struct {
	Format              string                    `json:"format"`
	PackageDigestSHA256 string                    `json:"package_digest_sha256"`
	Components          []SkillAssetSBOMComponent `json:"components"`
}

type SkillAssetSBOMComponent struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	ContentType    string `json:"content_type,omitempty"`
	Size           int32  `json:"size"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
}

type SkillPackageManifest struct {
	Format          string             `json:"format"`
	Root            string             `json:"root"`
	PackageChecksum string             `json:"package_checksum_sha256"`
	Files           []SkillPackageFile `json:"files"`
}

type SkillPackageFile struct {
	Path           string `json:"path"`
	Role           string `json:"role"`
	ContentType    string `json:"content_type"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
	ObjectKey      string `json:"object_key,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
	Executable     bool   `json:"executable,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	ScanStatus     string `json:"scan_status,omitempty"`
	ScanProvider   string `json:"scan_provider,omitempty"`
	ScanVersion    string `json:"scan_version,omitempty"`
}

type SkillConfig struct {
	Enabled []EnabledSkill `json:"enabled"`
}

type EnabledSkill struct {
	SkillID  string          `json:"skill_id,omitempty"`
	Skill    string          `json:"skill"`
	Version  int32           `json:"version,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Priority int32           `json:"priority,omitempty"`
	Inputs   json.RawMessage `json:"inputs,omitempty"`
}

type ResolveSkillsPreviewRequest struct {
	WorkspaceID string      `json:"workspace_id,omitempty"`
	Skills      SkillConfig `json:"skills"`
	MaxTokens   int32       `json:"max_tokens,omitempty"`
}

type ResolvedSkill struct {
	Skill           Skill           `json:"skill"`
	Version         SkillVersion    `json:"version"`
	RequestedMode   string          `json:"requested_mode"`
	RenderedMode    string          `json:"rendered_mode,omitempty"`
	Priority        int32           `json:"priority"`
	Inputs          json.RawMessage `json:"inputs,omitempty"`
	Rendered        string          `json:"rendered,omitempty"`
	EstimatedTokens int64           `json:"estimated_tokens"`
	Status          string          `json:"status"`
	FailureReason   string          `json:"failure_reason,omitempty"`
}

type ResolveSkillsResult struct {
	Config            SkillConfig            `json:"config"`
	Rendered          *RenderedSkillsContext `json:"rendered,omitempty"`
	Skills            []ResolvedSkill        `json:"skills,omitempty"`
	LegacyPassthrough bool                   `json:"legacy_passthrough,omitempty"`
	EstimatedTokens   int64                  `json:"estimated_tokens"`
	Truncated         bool                   `json:"truncated"`
}

type RenderedSkillsContext struct {
	Format  string `json:"format"`
	Content string `json:"content"`
}

type SkillUsage struct {
	WorkspaceID        string    `json:"workspace_id"`
	SessionID          string    `json:"session_id"`
	TurnID             string    `json:"turn_id"`
	AgentID            string    `json:"agent_id"`
	AgentConfigVersion int32     `json:"agent_config_version"`
	SkillID            string    `json:"skill_id"`
	SkillIdentifier    string    `json:"skill_identifier"`
	SkillVersion       int32     `json:"skill_version"`
	RequestedMode      string    `json:"requested_mode"`
	RenderedMode       string    `json:"rendered_mode,omitempty"`
	Priority           int32     `json:"priority"`
	EstimatedTokens    int64     `json:"estimated_tokens"`
	Status             string    `json:"status"`
	FailureReason      string    `json:"failure_reason,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
}

type SkillPackageBackfillRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int32  `json:"limit,omitempty"`
}

type SkillPackageBackfillResult struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Scanned     int32  `json:"scanned"`
	Migrated    int32  `json:"migrated"`
}

type SkillRetentionPolicyConfig struct {
	Enabled       bool  `json:"enabled"`
	RetentionDays int32 `json:"retention_days"`
	DeleteLimit   int32 `json:"delete_limit"`
}

type SkillRetentionPolicy struct {
	ID             string     `json:"id"`
	ScopeType      string     `json:"scope_type"`
	OrganizationID string     `json:"organization_id,omitempty"`
	WorkspaceID    string     `json:"workspace_id,omitempty"`
	Status         string     `json:"status"`
	CurrentVersion int32      `json:"current_version"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
}

type SkillRetentionPolicyVersion struct {
	ID             string                     `json:"id"`
	PolicyID       string                     `json:"policy_id"`
	Version        int32                      `json:"version"`
	Config         SkillRetentionPolicyConfig `json:"config"`
	ChecksumSHA256 string                     `json:"checksum_sha256"`
	CreatedBy      string                     `json:"created_by"`
	CreatedAt      time.Time                  `json:"created_at"`
}

type SkillRetentionPolicyResult struct {
	Policy  SkillRetentionPolicy        `json:"policy"`
	Version SkillRetentionPolicyVersion `json:"version"`
}

type CreateSkillRetentionPolicyRequest struct {
	ScopeType      string                     `json:"scope_type"`
	OrganizationID string                     `json:"organization_id,omitempty"`
	WorkspaceID    string                     `json:"workspace_id,omitempty"`
	Config         SkillRetentionPolicyConfig `json:"config"`
}

type PublishSkillRetentionPolicyRequest struct {
	Config SkillRetentionPolicyConfig `json:"config"`
}

type SkillRetentionPolicyQuery struct {
	OrganizationID  string
	WorkspaceID     string
	IncludeArchived bool
}

type EffectiveSkillRetentionPolicy struct {
	Source   string                       `json:"source"`
	Policy   *SkillRetentionPolicy        `json:"policy,omitempty"`
	Version  *SkillRetentionPolicyVersion `json:"version,omitempty"`
	Config   SkillRetentionPolicyConfig   `json:"config"`
	Revision string                       `json:"revision"`
}

type SkillAssetGCRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int32  `json:"limit,omitempty"`
	Confirm     string `json:"confirm,omitempty"`
}

type SkillAssetCandidate struct {
	WorkspaceID     string          `json:"workspace_id"`
	SkillID         string          `json:"skill_id,omitempty"`
	SkillIdentifier string          `json:"skill_identifier,omitempty"`
	SkillVersionID  string          `json:"skill_version_id,omitempty"`
	SkillVersion    int32           `json:"skill_version,omitempty"`
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

type SkillAssetGCPreview struct {
	WorkspaceID    string                        `json:"workspace_id"`
	Effective      EffectiveSkillRetentionPolicy `json:"effective_policy"`
	Cutoff         time.Time                     `json:"cutoff"`
	CandidateCount int32                         `json:"candidate_count"`
	CandidateBytes int64                         `json:"candidate_bytes"`
	Candidates     []SkillAssetCandidate         `json:"candidates"`
}

type SkillAssetGCRun struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	PolicySource   string     `json:"policy_source"`
	PolicyID       string     `json:"policy_id,omitempty"`
	PolicyVersion  int32      `json:"policy_version,omitempty"`
	PolicyRevision string     `json:"policy_revision"`
	RetentionDays  int32      `json:"retention_days"`
	DeleteLimit    int32      `json:"delete_limit"`
	Status         string     `json:"status"`
	CandidateCount int32      `json:"candidate_count"`
	DeletedCount   int32      `json:"deleted_count"`
	SkippedCount   int32      `json:"skipped_count"`
	FailedCount    int32      `json:"failed_count"`
	BytesDeleted   int64      `json:"bytes_deleted"`
	RequestedBy    string     `json:"requested_by"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type SkillAssetGCItem struct {
	ID               string              `json:"id"`
	RunID            string              `json:"run_id"`
	Candidate        SkillAssetCandidate `json:"candidate"`
	Status           string              `json:"status"`
	Reason           string              `json:"reason"`
	Attempts         int32               `json:"attempts"`
	ObjectWasMissing bool                `json:"object_was_missing"`
	ErrorMessage     string              `json:"error_message,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	DeletedAt        *time.Time          `json:"deleted_at,omitempty"`
}

type SkillAssetGCRunResult struct {
	Run   SkillAssetGCRun    `json:"run"`
	Items []SkillAssetGCItem `json:"items"`
}

type SkillAssetGCTombstone struct {
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

type SkillAssetGCListQuery struct {
	WorkspaceID string
	Limit       int32
}
