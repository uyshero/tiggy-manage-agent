package tma

import (
	"encoding/json"
	"fmt"
	"time"
)

type Principal struct {
	Subject        string   `json:"subject"`
	OrganizationID string   `json:"organization_id,omitempty"`
	WorkspaceID    string   `json:"workspace_id"`
	OwnerID        string   `json:"owner_id"`
	Roles          []string `json:"roles"`
	AuthType       string   `json:"auth_type"`
}

type AuthState struct {
	Authenticated bool       `json:"authenticated"`
	Principal     *Principal `json:"principal,omitempty"`
}

type AuthClientConfiguration struct {
	Mode string                       `json:"mode"`
	OIDC *AuthOIDCClientConfiguration `json:"oidc,omitempty"`
}

type AuthOIDCClientConfiguration struct {
	Issuer              string   `json:"issuer"`
	Audience            string   `json:"audience"`
	ClientID            string   `json:"client_id"`
	Scopes              []string `json:"scopes"`
	DeviceAuthorization bool     `json:"device_authorization"`
}

type MCPServer struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	Identifier     string          `json:"identifier"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	Status         string          `json:"status"`
	CurrentVersion int32           `json:"current_version"`
	Config         MCPServerConfig `json:"config"`
	UsageCount     int32           `json:"usage_count"`
	CreatedBy      string          `json:"created_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type MCPServerVersion struct {
	ID             string          `json:"id"`
	ServerID       string          `json:"server_id"`
	Version        int32           `json:"version"`
	Config         MCPServerConfig `json:"config"`
	ChecksumSHA256 string          `json:"checksum_sha256"`
	CreatedBy      string          `json:"created_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type CreateMCPServerRequest struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Identifier  string          `json:"identifier"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Config      MCPServerConfig `json:"config"`
}

type UpdateMCPServerRequest struct {
	Name        *string          `json:"name,omitempty"`
	Description *string          `json:"description,omitempty"`
	Config      *MCPServerConfig `json:"config,omitempty"`
}

type MCPServerConfig struct {
	Identifier   string                    `json:"identifier"`
	Command      string                    `json:"command,omitempty"`
	Args         []string                  `json:"args,omitempty"`
	Env          map[string]MCPConfigValue `json:"env,omitempty"`
	Cwd          string                    `json:"cwd,omitempty"`
	URL          string                    `json:"url,omitempty"`
	Headers      map[string]MCPConfigValue `json:"headers,omitempty"`
	OAuth        *MCPOAuthConfig           `json:"oauth,omitempty"`
	Listen       bool                      `json:"listen,omitempty"`
	Roots        []MCPRoot                 `json:"roots,omitempty"`
	Sampling     *MCPSamplingConfig        `json:"sampling,omitempty"`
	Elicitation  *MCPElicitationConfig     `json:"elicitation,omitempty"`
	Logging      *MCPLoggingConfig         `json:"logging,omitempty"`
	Runtime      *MCPRuntimePolicy         `json:"runtime,omitempty"`
	Expose       MCPExposeConfig           `json:"expose,omitempty"`
	Title        string                    `json:"title,omitempty"`
	Description  string                    `json:"description,omitempty"`
	IncludeTools []string                  `json:"include_tools,omitempty"`
	ExcludeTools []string                  `json:"exclude_tools,omitempty"`
	Transport    string                    `json:"transport,omitempty"`
	StdioFraming string                    `json:"stdio_framing,omitempty"`
	Disabled     bool                      `json:"disabled,omitempty"`
	Registry     *MCPRegistrySource        `json:"_registry,omitempty"`
}

type MCPRoot struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type MCPSamplingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

type MCPElicitationConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

type MCPLoggingConfig struct {
	Level string `json:"level,omitempty"`
}

type MCPRuntimePolicy struct {
	TimeoutSeconds   int32 `json:"timeout_seconds,omitempty"`
	MaxConcurrency   int32 `json:"max_concurrency,omitempty"`
	FailureThreshold int32 `json:"failure_threshold,omitempty"`
	CooldownSeconds  int32 `json:"cooldown_seconds,omitempty"`
}

type MCPRegistrySource struct {
	ServerID string `json:"server_id"`
	Version  int32  `json:"version"`
}

type MCPExposeConfig struct {
	Resources bool `json:"resources,omitempty"`
	Prompts   bool `json:"prompts,omitempty"`
}

type MCPOAuthConfig struct {
	GrantType               string          `json:"grant_type,omitempty"`
	TokenURL                string          `json:"token_url,omitempty"`
	ClientID                *MCPConfigValue `json:"client_id,omitempty"`
	ClientSecret            *MCPConfigValue `json:"client_secret,omitempty"`
	RefreshToken            *MCPConfigValue `json:"refresh_token,omitempty"`
	Scopes                  []string        `json:"scopes,omitempty"`
	Audience                string          `json:"audience,omitempty"`
	Resource                string          `json:"resource,omitempty"`
	TokenEndpointAuthMethod string          `json:"token_endpoint_auth_method,omitempty"`
}

// MCPConfigValue represents the MCP string-or-reference value used by env,
// headers and OAuth credentials. Exactly one field must be set.
type MCPConfigValue struct {
	Literal   *string
	EnvRef    string
	SecretRef string
}

func MCPConfigLiteral(value string) MCPConfigValue {
	return MCPConfigValue{Literal: &value}
}

func MCPConfigEnvRef(name string) MCPConfigValue {
	return MCPConfigValue{EnvRef: name}
}

func MCPConfigSecretRef(reference string) MCPConfigValue {
	return MCPConfigValue{SecretRef: reference}
}

func (v MCPConfigValue) MarshalJSON() ([]byte, error) {
	set := 0
	if v.Literal != nil {
		set++
	}
	if v.EnvRef != "" {
		set++
	}
	if v.SecretRef != "" {
		set++
	}
	if set != 1 {
		return nil, fmt.Errorf("tma: MCP config value requires exactly one literal, env_ref, or secret_ref")
	}
	if v.Literal != nil {
		return json.Marshal(*v.Literal)
	}
	if v.EnvRef != "" {
		return json.Marshal(map[string]string{"env_ref": v.EnvRef})
	}
	return json.Marshal(map[string]string{"secret_ref": v.SecretRef})
}

func (v *MCPConfigValue) UnmarshalJSON(raw []byte) error {
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		*v = MCPConfigLiteral(literal)
		return nil
	}
	var reference struct {
		Value     *string `json:"value"`
		EnvRef    string  `json:"env_ref"`
		SecretRef string  `json:"secret_ref"`
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		return fmt.Errorf("tma: decode MCP config value: %w", err)
	}
	set := 0
	if reference.Value != nil {
		set++
	}
	if reference.EnvRef != "" {
		set++
	}
	if reference.SecretRef != "" {
		set++
	}
	if set != 1 {
		return fmt.Errorf("tma: MCP config value requires exactly one literal, env_ref, or secret_ref")
	}
	switch {
	case reference.Value != nil:
		*v = MCPConfigLiteral(*reference.Value)
	case reference.EnvRef != "":
		*v = MCPConfigEnvRef(reference.EnvRef)
	default:
		*v = MCPConfigSecretRef(reference.SecretRef)
	}
	return nil
}

type MCPServerQuery struct {
	WorkspaceID string
}

type MCPRuntimeStatus struct {
	CheckedAt time.Time         `json:"checked_at"`
	States    []MCPRuntimeState `json:"states"`
}

type MCPRuntimeState struct {
	ServerID                 string     `json:"server_id"`
	Version                  int32      `json:"version"`
	State                    string     `json:"state"`
	InFlight                 int32      `json:"in_flight"`
	MaxConcurrency           int32      `json:"max_concurrency"`
	ConsecutiveFailures      int32      `json:"consecutive_failures"`
	FailureThreshold         int32      `json:"failure_threshold"`
	LastFailureClass         string     `json:"last_failure_class,omitempty"`
	LastFailureAt            *time.Time `json:"last_failure_at,omitempty"`
	OpenUntil                *time.Time `json:"open_until,omitempty"`
	CooldownRemainingSeconds int64      `json:"cooldown_remaining_seconds,omitempty"`
}

type MCPServerTestResult struct {
	ServerID string        `json:"server_id"`
	Version  int32         `json:"version"`
	Result   MCPHealthItem `json:"result"`
}

type MCPHealthItem struct {
	Identifier            string   `json:"identifier"`
	Kind                  string   `json:"kind"`
	Status                string   `json:"status"`
	Detail                string   `json:"detail,omitempty"`
	LatencyMillis         int64    `json:"latency_ms,omitempty"`
	ToolCount             int32    `json:"tool_count,omitempty"`
	Version               int32    `json:"version,omitempty"`
	ServerName            string   `json:"server_name,omitempty"`
	Transport             string   `json:"transport,omitempty"`
	EstimatedTokens       int32    `json:"estimated_tokens,omitempty"`
	Capabilities          []string `json:"capabilities,omitempty"`
	ResourceCount         int32    `json:"resource_count,omitempty"`
	ResourceTemplateCount int32    `json:"resource_template_count,omitempty"`
	PromptCount           int32    `json:"prompt_count,omitempty"`
}

type MCPRestoreResult struct {
	Server          MCPServer `json:"server"`
	SourceVersion   int32     `json:"source_version"`
	PreviousVersion int32     `json:"previous_version"`
	NewVersion      int32     `json:"new_version"`
}

type OperatorAuditRecord struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	PrincipalID   string          `json:"principal_id"`
	OperatorLabel string          `json:"operator_label,omitempty"`
	Role          string          `json:"role"`
	Action        string          `json:"action"`
	ResourceType  string          `json:"resource_type"`
	ResourceID    string          `json:"resource_id,omitempty"`
	Outcome       string          `json:"outcome"`
	ErrorMessage  string          `json:"error_message,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type OperatorAuditQuery struct {
	WorkspaceID string
	SessionID   string
	PrincipalID string
	Action      string
	Limit       int32
}

type ToolPermissionAuditRecord struct {
	SessionID        string    `json:"session_id"`
	TurnID           string    `json:"turn_id"`
	CallID           string    `json:"call_id"`
	Tool             string    `json:"tool"`
	Path             string    `json:"path,omitempty"`
	Decision         string    `json:"decision"`
	Allowed          bool      `json:"allowed"`
	Required         bool      `json:"required"`
	InterventionMode string    `json:"intervention_mode"`
	ApprovalPolicy   string    `json:"approval_policy,omitempty"`
	ApprovalStatus   string    `json:"approval_status"`
	ExecutionStatus  string    `json:"execution_status"`
	Reason           string    `json:"reason,omitempty"`
	Risk             string    `json:"risk,omitempty"`
	MatchedRuleID    string    `json:"matched_rule_id,omitempty"`
	RuleSource       string    `json:"rule_source,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type ToolPermissionAuditQuery struct {
	Decision string
	Tool     string
	Limit    int32
	Cursor   string
}

type ToolPermissionAuditPage struct {
	Records    []ToolPermissionAuditRecord `json:"records"`
	NextCursor string                      `json:"next_cursor"`
	HasMore    bool                        `json:"has_more"`
}

type SecurityAuditReplayResult struct {
	Replayed int32 `json:"replayed"`
}

type EnvironmentVariable struct {
	Name       string    `json:"name"`
	Configured bool      `json:"configured"`
	Scope      string    `json:"scope"`
	Editable   bool      `json:"editable"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type EnvironmentVariableQuery struct {
	WorkspaceID string
}

type PutEnvironmentVariableRequest struct {
	Value string `json:"value"`
}
