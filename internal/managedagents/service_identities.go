package managedagents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ServiceIdentityKindApplication = "application"
	ServiceIdentityKindService     = "service"

	ServiceIdentityStatusActive   = "active"
	ServiceIdentityStatusDisabled = "disabled"

	ServiceCredentialStatusActive  = "active"
	ServiceCredentialStatusRevoked = "revoked"

	ServiceScopeAgentsRead        = "agents:read"
	ServiceScopeAgentsWrite       = "agents:write"
	ServiceScopeArtifactsRead     = "artifacts:read"
	ServiceScopeArtifactsWrite    = "artifacts:write"
	ServiceScopeEnvironmentsRead  = "environments:read"
	ServiceScopeEnvironmentsWrite = "environments:write"
	ServiceScopeEvaluationsRead   = "evaluations:read"
	ServiceScopeEvaluationsWrite  = "evaluations:write"
	ServiceScopeMCPRead           = "mcp:read"
	ServiceScopeMCPWrite          = "mcp:write"
	ServiceScopeModelEmbedding    = "model:embedding"
	ServiceScopeModelGenerate     = "model:generate"
	ServiceScopeModelRerank       = "model:rerank"
	ServiceScopeRetrievalRead     = "retrieval:read"
	ServiceScopeRetrievalWrite    = "retrieval:write"
	ServiceScopeSessionsRead      = "sessions:read"
	ServiceScopeSessionsWrite     = "sessions:write"
	ServiceScopeSkillsRead        = "skills:read"
	ServiceScopeSkillsWrite       = "skills:write"
	ServiceScopeSpeechRealtime    = "speech:realtime"
)

var supportedServiceIdentityScopes = map[string]struct{}{
	ServiceScopeAgentsRead: {}, ServiceScopeAgentsWrite: {},
	ServiceScopeArtifactsRead: {}, ServiceScopeArtifactsWrite: {},
	ServiceScopeEnvironmentsRead: {}, ServiceScopeEnvironmentsWrite: {},
	ServiceScopeEvaluationsRead: {}, ServiceScopeEvaluationsWrite: {},
	ServiceScopeMCPRead: {}, ServiceScopeMCPWrite: {},
	ServiceScopeModelEmbedding: {}, ServiceScopeModelGenerate: {}, ServiceScopeModelRerank: {},
	ServiceScopeRetrievalRead: {}, ServiceScopeRetrievalWrite: {},
	ServiceScopeSessionsRead: {}, ServiceScopeSessionsWrite: {},
	ServiceScopeSkillsRead: {}, ServiceScopeSkillsWrite: {},
	ServiceScopeSpeechRealtime: {},
}

type ServiceIdentity struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Role        string    `json:"role"`
	Scopes      []string  `json:"scopes"`
	Status      string    `json:"status"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateServiceIdentityInput struct {
	WorkspaceID string
	Kind        string
	Name        string
	Description string
	Role        string
	Scopes      []string
	CreatedBy   string
}

type UpdateServiceIdentityInput struct {
	WorkspaceID string
	ID          string
	Name        string
	Description string
	Role        string
	Scopes      []string
	Status      string
}

type ServiceCredential struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspace_id"`
	ServiceIdentityID string     `json:"service_identity_id"`
	Name              string     `json:"name"`
	TokenPrefix       string     `json:"token_prefix"`
	Status            string     `json:"status"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

type CreateServiceCredentialInput struct {
	WorkspaceID       string
	ServiceIdentityID string
	Name              string
	Locator           string
	TokenPrefix       string
	SecretHash        []byte
	ExpiresAt         *time.Time
	CreatedBy         string
}

type AuthenticatedServiceIdentity struct {
	Identity     ServiceIdentity
	CredentialID string
}

type ServiceIdentityStore interface {
	ListServiceIdentities(context.Context, string) ([]ServiceIdentity, error)
	GetServiceIdentity(context.Context, string, string) (ServiceIdentity, error)
	CreateServiceIdentity(context.Context, CreateServiceIdentityInput) (ServiceIdentity, error)
	UpdateServiceIdentity(context.Context, UpdateServiceIdentityInput) (ServiceIdentity, error)
	ListServiceCredentials(context.Context, string, string) ([]ServiceCredential, error)
	CreateServiceCredential(context.Context, CreateServiceCredentialInput) (ServiceCredential, error)
	RevokeServiceCredential(context.Context, string, string, string) error
	AuthenticateServiceCredential(context.Context, string, []byte) (AuthenticatedServiceIdentity, error)
}

func NormalizeServiceIdentityInput(kind, name, description, role string, scopes []string) (string, string, string, string, []string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = ServiceIdentityKindApplication
	}
	if kind != ServiceIdentityKindApplication && kind != ServiceIdentityKindService {
		return "", "", "", "", nil, fmt.Errorf("%w: unsupported service identity kind", ErrInvalid)
	}
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || len(name) > 120 || len(description) > 1000 {
		return "", "", "", "", nil, fmt.Errorf("%w: service identity name is required and field lengths must be valid", ErrInvalid)
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = WorkspaceRoleMember
	}
	if role != WorkspaceRoleViewer && role != WorkspaceRoleMember && role != WorkspaceRoleOperator {
		return "", "", "", "", nil, fmt.Errorf("%w: service identity role must be viewer, member, or operator", ErrInvalid)
	}
	normalizedScopes, err := NormalizeServiceIdentityScopes(scopes)
	if err != nil {
		return "", "", "", "", nil, err
	}
	return kind, name, description, role, normalizedScopes, nil
}

func NormalizeServiceIdentityScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: at least one service identity scope is required", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(scopes))
	normalized := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if _, ok := supportedServiceIdentityScopes[scope]; !ok {
			return nil, fmt.Errorf("%w: unsupported service identity scope %q", ErrInvalid, scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func SupportedServiceIdentityScopes() []string {
	scopes := make([]string, 0, len(supportedServiceIdentityScopes))
	for scope := range supportedServiceIdentityScopes {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes
}
