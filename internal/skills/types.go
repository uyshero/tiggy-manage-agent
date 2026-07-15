package skills

import (
	"context"
	"encoding/json"
	"time"
)

const (
	DefaultMode     = ModeSummary
	DefaultPriority = 100

	ModeFull         = "full"
	ModeSummary      = "summary"
	ModeExamplesOnly = "examples_only"

	OwnerTypeBuiltin   = "builtin"
	OwnerTypeWorkspace = "workspace"
	OwnerTypePlugin    = "plugin"

	SourceTypeInline   = "inline"
	SourceTypeGitHub   = "github"
	SourceTypeArtifact = "artifact"
	SourceTypeCatalog  = "catalog"
	SourceTypePlugin   = "plugin"
	SourceTypeBuiltin  = "builtin"

	StatusActive   = "active"
	StatusArchived = "archived"

	UsageResolved = "resolved"
	UsageDegraded = "degraded"
	UsageSkipped  = "skipped"
	UsageFailed   = "failed"
)

type Config struct {
	Enabled []EnabledSkill `json:"enabled"`
}

type EnabledSkill struct {
	Skill    string          `json:"skill"`
	Version  int             `json:"version,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Priority int             `json:"priority,omitempty"`
	Inputs   json.RawMessage `json:"inputs,omitempty"`
}

type Skill struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	Identifier     string     `json:"identifier"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	OwnerType      string     `json:"owner_type"`
	SourcePluginID string     `json:"source_plugin_id,omitempty"`
	SourceType     string     `json:"source_type"`
	SourceLocator  string     `json:"source_locator,omitempty"`
	SourcePath     string     `json:"source_path,omitempty"`
	Status         string     `json:"status"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
}

type Version struct {
	ID                 string          `json:"id"`
	SkillID            string          `json:"skill_id"`
	Version            int             `json:"version"`
	ContentFormat      string          `json:"content_format"`
	Manifest           json.RawMessage `json:"manifest"`
	ContentText        string          `json:"content_text"`
	Assets             json.RawMessage `json:"assets,omitempty"`
	Checksum           string          `json:"checksum_sha256"`
	SourceRef          string          `json:"source_ref,omitempty"`
	SourceRevision     string          `json:"source_revision,omitempty"`
	SourceURL          string          `json:"source_url,omitempty"`
	PackageFormat      string          `json:"package_format"`
	PackageRoot        string          `json:"package_root,omitempty"`
	PackageChecksum    string          `json:"package_checksum_sha256,omitempty"`
	PackageObjectRefID string          `json:"package_object_ref_id,omitempty"`
	SkillMDObjectRefID string          `json:"skill_md_object_ref_id,omitempty"`
	PackageManifest    json.RawMessage `json:"package_manifest,omitempty"`
	CreatedBy          string          `json:"created_by"`
	CreatedAt          time.Time       `json:"created_at"`
}

type Manifest struct {
	SystemRole   string          `json:"system_role,omitempty"`
	Blocks       []ManifestBlock `json:"blocks,omitempty"`
	InputsSchema json.RawMessage `json:"inputs_schema,omitempty"`
}

type ManifestBlock struct {
	Type    string   `json:"type"`
	Title   string   `json:"title,omitempty"`
	Content string   `json:"content,omitempty"`
	Items   []string `json:"items,omitempty"`
}

type CreateSkillInput struct {
	WorkspaceID    string
	Identifier     string
	Title          string
	Description    string
	OwnerType      string
	SourcePluginID string
	SourceType     string
	SourceLocator  string
	SourcePath     string
	CreatedBy      string
}

type ListSkillsInput struct {
	WorkspaceID     string
	IncludeArchived bool
}

type CreateVersionInput struct {
	SkillID        string
	ContentFormat  string
	Manifest       json.RawMessage
	ContentText    string
	Assets         json.RawMessage
	SourceRef      string
	SourceRevision string
	SourceURL      string
	CreatedBy      string
}

type Registry interface {
	CreateSkill(ctx context.Context, input CreateSkillInput) (Skill, error)
	GetSkill(ctx context.Context, id string) (Skill, error)
	GetSkillByIdentifier(ctx context.Context, workspaceID string, identifier string) (Skill, error)
	ListSkills(ctx context.Context, input ListSkillsInput) ([]Skill, error)
	ArchiveSkill(ctx context.Context, id string) (Skill, error)
	CreateSkillVersion(ctx context.Context, input CreateVersionInput) (Version, error)
	GetSkillVersion(ctx context.Context, skillID string, version int) (Version, error)
	ListSkillVersions(ctx context.Context, skillID string) ([]Version, error)
}

type ResolvedSkill struct {
	Skill           Skill           `json:"skill"`
	Version         Version         `json:"version"`
	RequestedMode   string          `json:"requested_mode"`
	RenderedMode    string          `json:"rendered_mode,omitempty"`
	Priority        int             `json:"priority"`
	Inputs          json.RawMessage `json:"inputs,omitempty"`
	Rendered        string          `json:"rendered,omitempty"`
	EstimatedTokens int             `json:"estimated_tokens"`
	Status          string          `json:"status"`
	FailureReason   string          `json:"failure_reason,omitempty"`
}

type ResolveResult struct {
	Config            Config          `json:"config"`
	Rendered          json.RawMessage `json:"rendered,omitempty"`
	Skills            []ResolvedSkill `json:"skills,omitempty"`
	LegacyPassthrough bool            `json:"legacy_passthrough,omitempty"`
	EstimatedTokens   int             `json:"estimated_tokens"`
	Truncated         bool            `json:"truncated"`
}

type Usage struct {
	WorkspaceID        string    `json:"workspace_id"`
	SessionID          string    `json:"session_id"`
	TurnID             string    `json:"turn_id"`
	AgentID            string    `json:"agent_id"`
	AgentConfigVersion int       `json:"agent_config_version"`
	SkillID            string    `json:"skill_id"`
	SkillIdentifier    string    `json:"skill_identifier"`
	SkillVersion       int       `json:"skill_version"`
	RequestedMode      string    `json:"requested_mode"`
	RenderedMode       string    `json:"rendered_mode,omitempty"`
	Priority           int       `json:"priority"`
	EstimatedTokens    int       `json:"estimated_tokens"`
	Status             string    `json:"status"`
	FailureReason      string    `json:"failure_reason,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
}

type UsageRecorder interface {
	RecordSkillUsages(ctx context.Context, usages []Usage) error
}

type UsageReader interface {
	ListSkillUsages(ctx context.Context, sessionID string, turnID string) ([]Usage, error)
}

type PackageBackfillInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type PackageBackfillResult struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Scanned     int    `json:"scanned"`
	Migrated    int    `json:"migrated"`
}

type PackageBackfiller interface {
	BackfillSkillPackages(ctx context.Context, input PackageBackfillInput, createdBy string) (PackageBackfillResult, error)
}
