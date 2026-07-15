package tma

import (
	"encoding/json"
	"time"
)

type MarketplaceSource struct {
	Provider       string `json:"provider"`
	Repository     string `json:"repository,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Path           string `json:"path,omitempty"`
	ArtifactID     string `json:"artifact_id,omitempty"`
	CatalogEntryID string `json:"catalog_entry_id,omitempty"`
	CatalogSkillID string `json:"catalog_skill_id,omitempty"`
}

type MarketplaceDiscoverQuery struct {
	SessionID  string
	Query      string
	Repository string
	Limit      int32
}

type MarketplaceCandidate struct {
	Provider            string   `json:"provider"`
	Repository          string   `json:"repository"`
	Path                string   `json:"path,omitempty"`
	Ref                 string   `json:"ref,omitempty"`
	HTMLURL             string   `json:"html_url"`
	CatalogEntryID      string   `json:"catalog_entry_id,omitempty"`
	CatalogSkillID      string   `json:"catalog_skill_id,omitempty"`
	SkillVersion        int32    `json:"skill_version,omitempty"`
	Title               string   `json:"title,omitempty"`
	Category            string   `json:"category,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	VersionChecksum     string   `json:"version_checksum_sha256,omitempty"`
	Description         string   `json:"description,omitempty"`
	Stars               int32    `json:"stars,omitempty"`
	SuggestedIdentifier string   `json:"suggested_identifier,omitempty"`
	Verified            bool     `json:"verified"`
}

type MarketplaceDiscoverResult struct {
	Provider   string                 `json:"provider"`
	SearchMode string                 `json:"search_mode"`
	Items      []MarketplaceCandidate `json:"items"`
	Count      int32                  `json:"count"`
}

type MarketplacePreviewRequest struct {
	SessionID  string            `json:"session_id"`
	Identifier string            `json:"identifier,omitempty"`
	Source     MarketplaceSource `json:"source"`
}

type MarketplaceAssetIndex struct {
	Files      []MarketplaceAssetIndexFile `json:"files"`
	TotalBytes int64                       `json:"total_bytes"`
	Warnings   []string                    `json:"warnings,omitempty"`
	SBOM       SkillAssetSBOM              `json:"sbom,omitempty"`
}

type MarketplaceAssetIndexFile struct {
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	Executable     bool   `json:"executable,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
	ScanStatus     string `json:"scan_status,omitempty"`
	ScanProvider   string `json:"scan_provider,omitempty"`
	ScanVersion    string `json:"scan_version,omitempty"`
}

type MarketplaceExistingSkill struct {
	SkillID        string `json:"skill_id"`
	Version        int32  `json:"version,omitempty"`
	Status         string `json:"status"`
	SourceType     string `json:"source_type"`
	SourceLocator  string `json:"source_locator,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
	SourceRef      string `json:"source_ref,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
}

type MarketplacePreviewChanges struct {
	ContentChanged bool     `json:"content_changed"`
	AddedFiles     []string `json:"added_files"`
	RemovedFiles   []string `json:"removed_files"`
	ChangedFiles   []string `json:"changed_files"`
}

type MarketplacePolicyDecision struct {
	Allowed        bool                     `json:"allowed"`
	PolicySource   string                   `json:"policy_source,omitempty"`
	PolicyID       string                   `json:"policy_id,omitempty"`
	PolicyVersion  int32                    `json:"policy_version,omitempty"`
	PolicyRevision string                   `json:"policy_revision,omitempty"`
	Checks         []MarketplacePolicyCheck `json:"checks"`
	Violations     []string                 `json:"violations,omitempty"`
}

type MarketplacePolicyCheck struct {
	Name     string `json:"name"`
	Enforced bool   `json:"enforced"`
	Passed   bool   `json:"passed"`
	Message  string `json:"message"`
}

type MarketplaceSecurityReport struct {
	DigestSHA256    string                        `json:"digest_sha256"`
	Attestation     MarketplaceAttestationResult  `json:"attestation"`
	Findings        []MarketplaceSecurityFinding  `json:"findings"`
	HighestSeverity string                        `json:"highest_severity,omitempty"`
	ScannedFiles    int32                         `json:"scanned_files"`
	FindingsLimited bool                          `json:"findings_limited,omitempty"`
	BinaryFiles     []MarketplaceBinaryScanResult `json:"binary_files"`
	SBOM            MarketplacePackageSBOM        `json:"sbom"`
}

type MarketplaceAttestationResult struct {
	Status       string `json:"status"`
	Path         string `json:"path,omitempty"`
	KeyID        string `json:"key_id,omitempty"`
	Algorithm    string `json:"algorithm,omitempty"`
	DigestSHA256 string `json:"digest_sha256"`
	Message      string `json:"message"`
}

type MarketplaceSecurityFinding struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int32  `json:"line"`
	Message  string `json:"message"`
}

type MarketplaceBinaryScanResult struct {
	Path           string                               `json:"path"`
	Status         string                               `json:"status"`
	Scanner        string                               `json:"scanner"`
	ExternalScan   *MarketplaceExternalBinaryScanResult `json:"external_scan,omitempty"`
	ContentType    string                               `json:"content_type"`
	Size           int64                                `json:"size"`
	ChecksumSHA256 string                               `json:"checksum_sha256"`
	Findings       []MarketplaceSecurityFinding         `json:"findings"`
}

type MarketplaceExternalBinaryScanResult struct {
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	Scanner    string `json:"scanner,omitempty"`
	ScanID     string `json:"scan_id,omitempty"`
	Signature  string `json:"signature,omitempty"`
	Message    string `json:"message,omitempty"`
	Attempts   int32  `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
}

type MarketplacePackageSBOM struct {
	Format              string                     `json:"format"`
	PackageDigestSHA256 string                     `json:"package_digest_sha256"`
	Components          []MarketplaceSBOMComponent `json:"components"`
}

type MarketplaceSBOMComponent struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	ContentType    string `json:"content_type,omitempty"`
	Size           int64  `json:"size"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	Revision       string `json:"revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
}

type MarketplacePreviewResult struct {
	Identifier   string                    `json:"identifier"`
	Title        string                    `json:"title,omitempty"`
	Description  string                    `json:"description,omitempty"`
	License      string                    `json:"license,omitempty"`
	Source       MarketplaceSource         `json:"source"`
	Revision     string                    `json:"revision,omitempty"`
	SourceURL    string                    `json:"source_url,omitempty"`
	ContentBytes int64                     `json:"content_bytes,omitempty"`
	Assets       MarketplaceAssetIndex     `json:"assets"`
	Policy       MarketplacePolicyDecision `json:"policy"`
	Security     MarketplaceSecurityReport `json:"security"`
	InstallState string                    `json:"install_state"`
	BlockReason  string                    `json:"block_reason,omitempty"`
	Existing     *MarketplaceExistingSkill `json:"existing,omitempty"`
	Changes      MarketplacePreviewChanges `json:"changes"`
}

type MarketplaceInstallRequest struct {
	SessionID       string            `json:"session_id"`
	Identifier      string            `json:"identifier,omitempty"`
	Source          MarketplaceSource `json:"source"`
	PolicyID        string            `json:"policy_id,omitempty"`
	PolicyVersion   int32             `json:"policy_version,omitempty"`
	PolicyRevision  string            `json:"policy_revision,omitempty"`
	UpgradeExisting bool              `json:"upgrade_existing,omitempty"`
}

type MarketplaceInstallResult struct {
	Skill    Skill                      `json:"skill"`
	Version  SkillVersion               `json:"version"`
	Upgraded bool                       `json:"upgraded,omitempty"`
	Policy   *MarketplacePolicyDecision `json:"policy,omitempty"`
	Security *MarketplaceSecurityReport `json:"security,omitempty"`
}

type MarketplaceInternalQuery struct {
	SessionID string
	Query     string
	Category  string
	Tags      []string
	Limit     int32
}

type MarketplaceInternalCandidate struct {
	MarketplaceEntry
	Provider            string                    `json:"provider"`
	SuggestedIdentifier string                    `json:"suggested_identifier"`
	InstallState        string                    `json:"install_state"`
	Existing            *MarketplaceExistingSkill `json:"existing,omitempty"`
}

type MarketplaceInternalResult struct {
	Provider string                         `json:"provider"`
	Items    []MarketplaceInternalCandidate `json:"items"`
	Count    int32                          `json:"count"`
}

type MarketplaceEnableRequest struct {
	SessionID string          `json:"session_id"`
	Version   int32           `json:"version,omitempty"`
	Mode      string          `json:"mode,omitempty"`
	Priority  int32           `json:"priority,omitempty"`
	Inputs    json.RawMessage `json:"inputs,omitempty"`
}

type MarketplaceDisableRequest struct {
	SessionID string `json:"session_id"`
}

type MarketplaceEnableResult struct {
	AgentID                string       `json:"agent_id"`
	PreviousConfigVersion  int32        `json:"previous_config_version"`
	NewConfigVersion       int32        `json:"new_config_version"`
	CurrentSessionVersion  int32        `json:"current_session_version"`
	Binding                EnabledSkill `json:"binding"`
	Changed                bool         `json:"changed"`
	RequiresSessionUpgrade bool         `json:"requires_session_upgrade"`
}

type MarketplaceDisableResult struct {
	AgentID                string       `json:"agent_id"`
	PreviousConfigVersion  int32        `json:"previous_config_version"`
	NewConfigVersion       int32        `json:"new_config_version"`
	CurrentSessionVersion  int32        `json:"current_session_version"`
	Binding                EnabledSkill `json:"binding"`
	Removed                bool         `json:"removed"`
	RequiresSessionUpgrade bool         `json:"requires_session_upgrade"`
}

type MarketplaceEntry struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	SkillID          string     `json:"skill_id"`
	SkillVersion     int32      `json:"skill_version"`
	SkillIdentifier  string     `json:"skill_identifier"`
	SkillTitle       string     `json:"skill_title"`
	SkillDescription string     `json:"skill_description,omitempty"`
	SkillStatus      string     `json:"skill_status"`
	VersionChecksum  string     `json:"version_checksum_sha256"`
	PackageFormat    string     `json:"package_format"`
	Summary          string     `json:"summary,omitempty"`
	Category         string     `json:"category,omitempty"`
	Tags             []string   `json:"tags"`
	Status           string     `json:"status"`
	SubmittedBy      string     `json:"submitted_by,omitempty"`
	SubmittedAt      *time.Time `json:"submitted_at,omitempty"`
	PublishedBy      string     `json:"published_by,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	WithdrawnBy      string     `json:"withdrawn_by,omitempty"`
	WithdrawnAt      *time.Time `json:"withdrawn_at,omitempty"`
	ReviewNote       string     `json:"review_note,omitempty"`
	WithdrawalReason string     `json:"withdrawal_reason,omitempty"`
	CreatedBy        string     `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedBy        string     `json:"updated_by"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CreateMarketplaceEntryRequest struct {
	WorkspaceID  string   `json:"workspace_id,omitempty"`
	SkillID      string   `json:"skill_id"`
	SkillVersion int32    `json:"skill_version"`
	Summary      string   `json:"summary,omitempty"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type UpdateMarketplaceEntryRequest struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type MarketplaceEntryQuery struct {
	WorkspaceID      string
	Status           string
	IncludeWithdrawn bool
}

type MarketplaceTransitionRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Note        string `json:"note,omitempty"`
}

type MarketplacePolicyConfig struct {
	AllowedOwners           []string          `json:"allowed_owners,omitempty"`
	AllowedRepositories     []string          `json:"allowed_repositories,omitempty"`
	RequireCommitSHA        bool              `json:"require_commit_sha,omitempty"`
	AllowedLicenses         []string          `json:"allowed_licenses,omitempty"`
	DeniedLicenses          []string          `json:"denied_licenses,omitempty"`
	RequireLicense          bool              `json:"require_license,omitempty"`
	RequireAttestation      bool              `json:"require_attestation,omitempty"`
	TrustedAttestationKeys  map[string]string `json:"trusted_attestation_keys,omitempty"`
	StaticScanBlockSeverity string            `json:"static_scan_block_severity,omitempty"`
}

type MarketplacePolicy struct {
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

type MarketplacePolicyVersion struct {
	ID             string                  `json:"id"`
	PolicyID       string                  `json:"policy_id"`
	Version        int32                   `json:"version"`
	Config         MarketplacePolicyConfig `json:"config"`
	ChecksumSHA256 string                  `json:"checksum_sha256"`
	CreatedBy      string                  `json:"created_by"`
	CreatedAt      time.Time               `json:"created_at"`
}

type MarketplacePolicyResult struct {
	Policy  MarketplacePolicy        `json:"policy"`
	Version MarketplacePolicyVersion `json:"version"`
}

type CreateMarketplacePolicyRequest struct {
	ScopeType      string                  `json:"scope_type"`
	OrganizationID string                  `json:"organization_id,omitempty"`
	WorkspaceID    string                  `json:"workspace_id,omitempty"`
	Config         MarketplacePolicyConfig `json:"config"`
}

type PublishMarketplacePolicyRequest struct {
	Config MarketplacePolicyConfig `json:"config"`
}

type MarketplacePolicyQuery struct {
	OrganizationID  string
	WorkspaceID     string
	IncludeArchived bool
}
