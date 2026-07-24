package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"tiggy-manage-agent/internal/skillmarketplace"
	skillspkg "tiggy-manage-agent/internal/skills"
)

const SkillsIdentifier = NamespaceSkills

type SkillsToolService interface {
	Search(context.Context, SkillsSearchRequest) (SkillsSearchResponse, error)
	Inspect(context.Context, SkillsInspectRequest) (SkillsInspectResponse, error)
	Discover(context.Context, SkillsDiscoverRequest) (SkillsDiscoverResponse, error)
	Preview(context.Context, SkillsPreviewRequest) (SkillsPreviewResponse, error)
	ReadAsset(context.Context, SkillsReadAssetRequest) (SkillsReadAssetResponse, error)
	Install(context.Context, SkillsInstallRequest) (SkillsInstallResponse, error)
	Enable(context.Context, SkillsEnableRequest) (SkillsEnableResponse, error)
	Disable(context.Context, SkillsDisableRequest) (SkillsDisableResponse, error)
}

type SkillsSearchRequest struct {
	WorkspaceID     string `json:"-"`
	SessionID       string `json:"-"`
	Query           string `json:"query,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type SkillsSearchItem struct {
	Skill         skillspkg.Skill    `json:"skill"`
	LatestVersion *skillspkg.Version `json:"latest_version,omitempty"`
}

type SkillsSearchResponse struct {
	Query   string             `json:"query,omitempty"`
	Items   []SkillsSearchItem `json:"items"`
	Count   int                `json:"count"`
	HasMore bool               `json:"has_more,omitempty"`
}

type SkillsInspectRequest struct {
	WorkspaceID     string `json:"-"`
	SessionID       string `json:"-"`
	Identifier      string `json:"identifier"`
	Version         int    `json:"version,omitempty"`
	ContentOffset   int    `json:"content_offset,omitempty"`
	ContentMaxChars int    `json:"content_max_chars,omitempty"`
}

type SkillsInspectResponse struct {
	Skill             skillspkg.Skill   `json:"skill"`
	Version           skillspkg.Version `json:"version"`
	ContentOffset     int               `json:"content_offset"`
	ContentChars      int               `json:"content_chars"`
	TotalContentChars int               `json:"total_content_chars"`
	NextOffset        int               `json:"next_offset,omitempty"`
	HasMore           bool              `json:"has_more"`
}

type SkillsDiscoverRequest struct {
	WorkspaceID string   `json:"-"`
	SessionID   string   `json:"-"`
	Provider    string   `json:"provider,omitempty"`
	Query       string   `json:"query,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

type SkillsDiscoverResponse struct {
	Provider   string                       `json:"provider"`
	SearchMode string                       `json:"search_mode"`
	Items      []skillmarketplace.Candidate `json:"items"`
	Count      int                          `json:"count"`
}

type SkillsPreviewRequest struct {
	WorkspaceID string              `json:"-"`
	SessionID   string              `json:"-"`
	Identifier  string              `json:"identifier,omitempty"`
	Source      SkillsInstallSource `json:"source"`
}

type SkillsAssetIndex struct {
	Files      []SkillsAssetIndexFile `json:"files"`
	TotalBytes int                    `json:"total_bytes"`
	Warnings   []string               `json:"warnings,omitempty"`
	SBOM       skillspkg.AssetSBOM    `json:"sbom,omitempty"`
}

type SkillsAssetIndexFile struct {
	Path           string `json:"path"`
	Size           int    `json:"size"`
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

type SkillsPreviewExisting struct {
	SkillID        string `json:"skill_id"`
	Version        int    `json:"version,omitempty"`
	Status         string `json:"status"`
	SourceType     string `json:"source_type"`
	SourceLocator  string `json:"source_locator,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
	SourceRef      string `json:"source_ref,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
}

type SkillsPreviewChanges struct {
	ContentChanged bool     `json:"content_changed"`
	AddedFiles     []string `json:"added_files"`
	RemovedFiles   []string `json:"removed_files"`
	ChangedFiles   []string `json:"changed_files"`
}

type SkillsPreviewResponse struct {
	Identifier   string                                 `json:"identifier"`
	Title        string                                 `json:"title,omitempty"`
	Description  string                                 `json:"description,omitempty"`
	License      string                                 `json:"license,omitempty"`
	Source       skillmarketplace.Source                `json:"source"`
	Revision     string                                 `json:"revision,omitempty"`
	SourceURL    string                                 `json:"source_url,omitempty"`
	ContentBytes int                                    `json:"content_bytes,omitempty"`
	Assets       SkillsAssetIndex                       `json:"assets"`
	Policy       skillmarketplace.PolicyDecision        `json:"policy"`
	Security     skillmarketplace.PackageSecurityReport `json:"security"`
	InstallState string                                 `json:"install_state"`
	BlockReason  string                                 `json:"block_reason,omitempty"`
	Existing     *SkillsPreviewExisting                 `json:"existing,omitempty"`
	Changes      SkillsPreviewChanges                   `json:"changes"`
}

type SkillsReadAssetRequest struct {
	WorkspaceID string `json:"-"`
	SessionID   string `json:"-"`
	Identifier  string `json:"identifier"`
	Version     int    `json:"version,omitempty"`
	Path        string `json:"path"`
}

type SkillsReadAssetResponse struct {
	SkillIdentifier string              `json:"skill_identifier"`
	SkillVersion    int                 `json:"skill_version"`
	Found           bool                `json:"found"`
	RequestedPath   string              `json:"requested_path,omitempty"`
	AvailablePaths  []string            `json:"available_paths,omitempty"`
	File            skillspkg.AssetFile `json:"file"`
}

type SkillsInstallSource = skillmarketplace.Source

type SkillsInstallRequest struct {
	WorkspaceID     string               `json:"-"`
	SessionID       string               `json:"-"`
	TurnID          string               `json:"-"`
	Identifier      string               `json:"identifier"`
	Title           string               `json:"title"`
	Description     string               `json:"description,omitempty"`
	ContentFormat   string               `json:"content_format,omitempty"`
	Manifest        json.RawMessage      `json:"manifest,omitempty"`
	ContentText     string               `json:"content_text"`
	Assets          json.RawMessage      `json:"assets,omitempty"`
	Source          *SkillsInstallSource `json:"source,omitempty"`
	PolicyID        string               `json:"policy_id,omitempty"`
	PolicyVersion   int                  `json:"policy_version,omitempty"`
	PolicyRevision  string               `json:"policy_revision,omitempty"`
	UpgradeExisting bool                 `json:"upgrade_existing,omitempty"`
}

type SkillsInstallResponse struct {
	Skill    skillspkg.Skill                         `json:"skill"`
	Version  skillspkg.Version                       `json:"version"`
	Upgraded bool                                    `json:"upgraded,omitempty"`
	Policy   *skillmarketplace.PolicyDecision        `json:"policy,omitempty"`
	Security *skillmarketplace.PackageSecurityReport `json:"security,omitempty"`
}

type SkillsEnableRequest struct {
	WorkspaceID string          `json:"-"`
	SessionID   string          `json:"-"`
	TurnID      string          `json:"-"`
	Identifier  string          `json:"identifier"`
	Version     int             `json:"version,omitempty"`
	Mode        string          `json:"mode,omitempty"`
	Priority    int             `json:"priority,omitempty"`
	Inputs      json.RawMessage `json:"inputs,omitempty"`
}

type SkillsEnableResponse struct {
	AgentID                string                 `json:"agent_id"`
	PreviousConfigVersion  int                    `json:"previous_config_version"`
	NewConfigVersion       int                    `json:"new_config_version"`
	CurrentSessionVersion  int                    `json:"current_session_version"`
	Binding                skillspkg.EnabledSkill `json:"binding"`
	Changed                bool                   `json:"changed"`
	RequiresSessionUpgrade bool                   `json:"requires_session_upgrade"`
}

type SkillsDisableRequest struct {
	WorkspaceID string `json:"-"`
	SessionID   string `json:"-"`
	TurnID      string `json:"-"`
	Identifier  string `json:"identifier"`
}

type SkillsDisableResponse struct {
	AgentID                string                 `json:"agent_id"`
	PreviousConfigVersion  int                    `json:"previous_config_version"`
	NewConfigVersion       int                    `json:"new_config_version"`
	CurrentSessionVersion  int                    `json:"current_session_version"`
	Binding                skillspkg.EnabledSkill `json:"binding"`
	Removed                bool                   `json:"removed"`
	RequiresSessionUpgrade bool                   `json:"requires_session_upgrade"`
}

var (
	defaultSkillsToolServiceMu sync.RWMutex
	defaultSkillsToolService   SkillsToolService
)

func SetDefaultSkillsToolService(service SkillsToolService) {
	defaultSkillsToolServiceMu.Lock()
	defer defaultSkillsToolServiceMu.Unlock()
	defaultSkillsToolService = service
}

func DefaultSkillsToolService() SkillsToolService {
	defaultSkillsToolServiceMu.RLock()
	defer defaultSkillsToolServiceMu.RUnlock()
	return defaultSkillsToolService
}

type SkillsRuntime struct {
	Service SkillsToolService
}

func (SkillsRuntime) Manifest() Manifest {
	runtimePolicy := &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto}
	return Manifest{
		Identifier: SkillsIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Skills Registry",
			Description: "Search, inspect, discover, preview, install, read package assets, enable, and disable versioned skills in the current workspace.",
		},
		SystemRole:     "Use skills_search for installed skills and skills_discover for installable skills. An enabled Skill that matches the user's request is a binding execution contract, not optional advice. Before taking any action governed by a summary-mode Skill, call skills_inspect with the exact identifier and frozen version and read every page until has_more is false. Follow all required tools, steps, validations, prohibitions, and delivery rules. Never substitute a mandatory workflow unless the Skill permits it or the user explicitly approves the deviation after hearing the blocker; otherwise stop and report the blocker without claiming success. skills_discover defaults to the organization-local catalog and does not access the network; use provider github only when the user explicitly requests GitHub and external network access is allowed. Use the returned catalog_entry_id as source.catalog_entry_id with provider catalog for skills_preview and skills_install. Preview before installing or upgrading, inspect before enabling, and use skills_read_asset when an enabled skill references a package file. Never guess asset paths: read only SKILL.md or an exact path listed in the version assets; an empty assets list means there are no additional package assets. For a user-uploaded offline Skill ZIP, use source provider artifact with the exact current Session artifact_id supplied in attachment context; never use workspace_path, a host filesystem path, bucket/key, or arbitrary URL. Preview is read-only and returns source, license, warnings, asset index, version differences, package attestation, static security findings, and policy checks. Do not install when policy.allowed is false or install_state is blocked/unchanged. For install_state=upgrade set upgrade_existing=true; otherwise leave it false. Pass preview policy_id, policy_version, and policy_revision unchanged to skills_install. Installing publishes content to the workspace registry and requires write approval. After successful installation, clearly offer to enable that exact version, but do not call skills_enable until the user requests it. Enabling and disabling require separate write approval and create a new Agent config version when the binding changes. The current running turn keeps its frozen config. When requires_session_upgrade is false and the versions differ, the same Session automatically follows the new config on the next user turn; say that the Skill is available from the next message, never that the Session cannot use it. Only requires_session_upgrade=true means the Session is explicitly pinned and needs a manual config upgrade. Disabling removes only the selected binding and does not archive or uninstall the Skill. Enabled frozen Skill packages are materialized under the runtime package directory in cloud_sandbox. Executable assets still require a separate approved default_* execution call. When a package expects a desktop browser unavailable in the sandbox, use registered browser_* tools instead of searching host or server directories.",
		Executors:      []string{ExecutorServer},
		ApprovalPolicy: ApprovalPolicyNever,
		API: []API{
			{
				Name: "search", Namespace: NamespaceSkills, APIName: "search",
				Description: "Search installed skills in the current workspace registry.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"include_archived":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":50}}}`),
				Risk:        ToolRiskRead, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "inspect", Namespace: NamespaceSkills, APIName: "inspect",
				Description: "Load a skill and its full SKILL.md instructions for one exact or latest version from the current workspace registry. Use this for enabled skills rendered in summary mode when full details are needed.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string"},"version":{"type":"integer","minimum":1},"content_offset":{"type":"integer","minimum":0,"description":"Character offset for paged SKILL.md content"},"content_max_chars":{"type":"integer","minimum":1,"maximum":8000,"default":8000}},"required":["identifier"]}`),
				Risk:        ToolRiskRead, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "discover", Namespace: NamespaceSkills, APIName: "discover",
				Description: "Discover installable skills from the organization-local catalog by default, or explicitly from GitHub.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"provider":{"type":"string","enum":["catalog","github"],"default":"catalog"},"query":{"type":"string"},"repository":{"type":"string","description":"Exact GitHub owner/repo coordinate to verify; implies provider github"},"category":{"type":"string","description":"Internal catalog category filter"},"tags":{"type":"array","items":{"type":"string"},"maxItems":12,"description":"Internal catalog tag filters"},"limit":{"type":"integer","minimum":1,"maximum":50}}}`),
				Risk:        ToolRiskRead, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "preview", Namespace: NamespaceSkills, APIName: "preview",
				Description: "Preview an internal catalog, GitHub, or current-Session artifact ZIP skill package without installing it, including provenance, license, asset index, security policy, and version differences.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string","description":"Optional install identifier override"},"source":{"type":"object","properties":{"provider":{"type":"string","enum":["catalog","github","artifact"]},"repository":{"type":"string","description":"GitHub owner/repo coordinate"},"ref":{"type":"string"},"path":{"type":"string","x-tma-file-ref":false},"artifact_id":{"type":"string","description":"ZIP artifact attached to the current Session"},"catalog_entry_id":{"type":"string","description":"Published entry returned by skills_discover"}},"required":["provider"],"allOf":[{"if":{"properties":{"provider":{"const":"catalog"}}},"then":{"required":["catalog_entry_id"]}},{"if":{"properties":{"provider":{"const":"github"}}},"then":{"required":["repository"]}},{"if":{"properties":{"provider":{"const":"artifact"}}},"then":{"required":["artifact_id"]}}]}},"required":["source"]}`),
				Risk:        ToolRiskRead, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "read_asset", Namespace: NamespaceSkills, APIName: "read_asset",
				Description: "Read SKILL.md or one exact text asset path listed by the installed skill version; never guess paths. A missing path returns found=false with available_paths so the task can recover. Script assets are returned as text and are not executed.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string"},"version":{"type":"integer","minimum":1},"path":{"type":"string","x-tma-file-ref":false}},"required":["identifier","path"]}`),
				Risk:        ToolRiskRead, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "install", Namespace: NamespaceSkills, APIName: "install",
				Description:    "Install an inline skill, internal catalog package, GitHub SKILL.md source, or current-Session artifact ZIP into the workspace, or publish a new version when upgrade_existing is true.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},"content_format":{"type":"string","enum":["markdown","json","hybrid"]},"manifest":{"type":"object"},"content_text":{"type":"string"},"assets":{},"source":{"type":"object","properties":{"provider":{"type":"string","enum":["catalog","github","artifact"]},"repository":{"type":"string","description":"GitHub owner/repo coordinate"},"ref":{"type":"string"},"path":{"type":"string","x-tma-file-ref":false},"artifact_id":{"type":"string","description":"ZIP artifact attached to the current Session"},"catalog_entry_id":{"type":"string","description":"Published entry returned by skills_discover"}},"required":["provider"],"allOf":[{"if":{"properties":{"provider":{"const":"catalog"}}},"then":{"required":["catalog_entry_id"]}},{"if":{"properties":{"provider":{"const":"github"}}},"then":{"required":["repository"]}},{"if":{"properties":{"provider":{"const":"artifact"}}},"then":{"required":["artifact_id"]}}]},"policy_id":{"type":"string"},"policy_version":{"type":"integer","minimum":1},"policy_revision":{"type":"string"},"upgrade_existing":{"type":"boolean"}},"anyOf":[{"required":["identifier","title","content_text"]},{"required":["source"]}]}`),
				ApprovalPolicy: ApprovalPolicyAlways,
				ApprovalReason: InterventionReasonSkillRegistry,
				Risk:           ToolRiskWrite, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "enable", Namespace: NamespaceSkills, APIName: "enable",
				Description:    "Enable an installed skill version for the current Agent by publishing a new Agent config version.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string"},"version":{"type":"integer","minimum":1},"mode":{"type":"string","enum":["full","summary","examples_only"],"default":"summary"},"priority":{"type":"integer","minimum":-1000,"maximum":1000},"inputs":{"type":"object"}},"required":["identifier"]}`),
				ApprovalPolicy: ApprovalPolicyAlways,
				ApprovalReason: InterventionReasonSkillRegistry,
				Risk:           ToolRiskWrite, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name: "disable", Namespace: NamespaceSkills, APIName: "disable",
				Description:    "Disable one installed skill for the current Agent by publishing a new Agent config version that removes only that binding.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string"}},"required":["identifier"]}`),
				ApprovalPolicy: ApprovalPolicyAlways,
				ApprovalReason: InterventionReasonSkillRegistry,
				Risk:           ToolRiskWrite, Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
		},
	}
}

func (runtime SkillsRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	service := runtime.Service
	if service == nil {
		service = DefaultSkillsToolService()
	}
	if service == nil {
		return ExecutionResult{}, errors.New("skills tool service is not configured")
	}
	switch call.APIName {
	case "search":
		var request SkillsSearchRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_search arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		response, err := service.Search(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return skillsToolResult(call, response, fmt.Sprintf("Found %d matching skills.", response.Count))
	case "inspect":
		var request SkillsInspectRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_inspect arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		response, err := service.Inspect(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		state := response
		state.Version.ContentText = ""
		content := fmt.Sprintf("Loaded skill %s version %d instructions (characters %d-%d of %d).", response.Skill.Identifier, response.Version.Version, response.ContentOffset, response.ContentOffset+response.ContentChars, response.TotalContentChars)
		if response.Version.ContentText != "" {
			content += "\n\n" + response.Version.ContentText
		}
		if response.HasMore {
			content += fmt.Sprintf("\n\n[More instructions available. Call skills_inspect again with content_offset=%d and the same identifier/version.]", response.NextOffset)
		}
		return skillsToolResult(call, state, content)
	case "discover":
		var request SkillsDiscoverRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_discover arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		response, err := service.Discover(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return skillsToolResult(call, response, fmt.Sprintf("Discovered %d installable skills from %s using %s search.", response.Count, response.Provider, response.SearchMode))
	case "preview":
		var request SkillsPreviewRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_preview arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		response, err := service.Preview(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return skillsToolResult(call, response, fmt.Sprintf("Previewed skill %s at revision %s: %s.", response.Identifier, response.Revision, response.InstallState))
	case "read_asset":
		var request SkillsReadAssetRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_read_asset arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		response, err := service.ReadAsset(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		if !response.Found {
			available := "none"
			if len(response.AvailablePaths) > 0 {
				available = strings.Join(response.AvailablePaths, ", ")
			}
			return skillsToolResult(call, response, fmt.Sprintf("Asset %s is not installed for skill %s version %d. Available paths: %s.", response.RequestedPath, response.SkillIdentifier, response.SkillVersion, available))
		}
		return skillsToolResult(call, response, fmt.Sprintf("Read asset %s from skill %s version %d.\n\n%s", response.File.Path, response.SkillIdentifier, response.SkillVersion, response.File.Content))
	case "install":
		var request SkillsInstallRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_install arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		request.TurnID = executionContext.TurnID
		response, err := service.Install(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		verb := "Installed"
		if response.Upgraded {
			verb = "Upgraded"
		}
		return skillsToolResult(call, response, fmt.Sprintf("%s skill %s version %d.", verb, response.Skill.Identifier, response.Version.Version))
	case "enable":
		var request SkillsEnableRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_enable arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		request.TurnID = executionContext.TurnID
		response, err := service.Enable(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		content := fmt.Sprintf("Enabled skill %s version %d in Agent config version %d.", response.Binding.Skill, response.Binding.Version, response.NewConfigVersion)
		if response.CurrentSessionVersion < response.NewConfigVersion {
			if response.RequiresSessionUpgrade {
				content += fmt.Sprintf(" The Session is explicitly pinned to version %d and requires a manual config upgrade.", response.CurrentSessionVersion)
			} else {
				content += fmt.Sprintf(" The current turn keeps version %d; the next user turn in this Session automatically uses version %d, so the skill is available from the next message.", response.CurrentSessionVersion, response.NewConfigVersion)
			}
		}
		return skillsToolResult(call, response, content)
	case "disable":
		var request SkillsDisableRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode skills_disable arguments: %w", err)
		}
		request.WorkspaceID = executionContext.WorkspaceID
		request.SessionID = executionContext.SessionID
		request.TurnID = executionContext.TurnID
		response, err := service.Disable(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		if !response.Removed {
			return skillsToolResult(call, response, fmt.Sprintf("Skill %s was already disabled in Agent config version %d.", request.Identifier, response.NewConfigVersion))
		}
		content := fmt.Sprintf("Disabled skill %s version %d in Agent config version %d.", response.Binding.Skill, response.Binding.Version, response.NewConfigVersion)
		if response.CurrentSessionVersion < response.NewConfigVersion {
			if response.RequiresSessionUpgrade {
				content += fmt.Sprintf(" The Session is explicitly pinned to version %d and requires a manual config upgrade.", response.CurrentSessionVersion)
			} else {
				content += fmt.Sprintf(" The current turn keeps version %d; the next user turn in this Session automatically uses version %d, so the skill is disabled from the next message.", response.CurrentSessionVersion, response.NewConfigVersion)
			}
		}
		return skillsToolResult(call, response, content)
	default:
		return ExecutionResult{}, fmt.Errorf("unsupported skills API %q", call.APIName)
	}
}

func skillsToolResult(call Call, state any, content string) (ExecutionResult, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{ID: call.ID, Identifier: SkillsIdentifier, APIName: call.APIName, Content: content, State: encoded}, nil
}
