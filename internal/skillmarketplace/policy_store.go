package skillmarketplace

import (
	"context"
	"time"
)

const (
	PolicyScopeOrganization = "organization"
	PolicyScopeWorkspace    = "workspace"
	PolicyStatusActive      = "active"
	PolicyStatusArchived    = "archived"
	PolicySourceServer      = "server"
)

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

type PolicyStore interface {
	CreateMarketplacePolicy(context.Context, CreatePolicyInput) (PolicyRecord, PolicyVersion, error)
	GetMarketplacePolicy(context.Context, string) (PolicyRecord, error)
	ListMarketplacePolicies(context.Context, ListPoliciesInput) ([]PolicyRecord, error)
	PublishMarketplacePolicyVersion(context.Context, string, Policy, string) (PolicyVersion, error)
	GetMarketplacePolicyVersion(context.Context, string, int) (PolicyVersion, error)
	ArchiveMarketplacePolicy(context.Context, string) (PolicyRecord, error)
	ResolveMarketplacePolicy(context.Context, string) (EffectivePolicy, error)
}
