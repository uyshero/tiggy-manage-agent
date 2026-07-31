package managedagents

import (
	"context"
	"time"
)

const (
	WorkspaceRoleViewer   = "viewer"
	WorkspaceRoleMember   = "member"
	WorkspaceRoleOperator = "operator"
	WorkspaceRoleAdmin    = "admin"
	PlatformRoleAdmin     = "platform_admin"
)

type WorkspaceMembership struct {
	WorkspaceID string    `json:"workspace_id"`
	Subject     string    `json:"subject"`
	DisplayName string    `json:"display_name,omitempty"`
	Email       string    `json:"email,omitempty"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UpsertWorkspaceMembershipInput struct {
	WorkspaceID string
	Subject     string
	DisplayName string
	Email       string
	Role        string
	Status      string
}

type PlatformRoleAssignment struct {
	Subject     string    `json:"subject"`
	DisplayName string    `json:"display_name,omitempty"`
	Email       string    `json:"email,omitempty"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type TenantWorkspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	MemberCount int64     `json:"member_count"`
}

// TenantAdministrationStore is optional so existing runtime and test stores do
// not need tenant-management capabilities.
type TenantAdministrationStore interface {
	GetWorkspaceMembership(ctx context.Context, workspaceID string, subject string) (WorkspaceMembership, error)
	ListWorkspaceMemberships(ctx context.Context, workspaceID string) ([]WorkspaceMembership, error)
	UpsertWorkspaceMembership(ctx context.Context, input UpsertWorkspaceMembershipInput) (WorkspaceMembership, error)
	DeleteWorkspaceMembership(ctx context.Context, workspaceID string, subject string) error
	IsPlatformAdmin(ctx context.Context, subject string) (bool, error)
	ListPlatformAdmins(ctx context.Context, callerSubject string) ([]PlatformRoleAssignment, error)
	UpsertPlatformAdmin(ctx context.Context, callerSubject string, input PlatformRoleAssignment) (PlatformRoleAssignment, error)
	DeletePlatformAdmin(ctx context.Context, callerSubject string, subject string) error
	ListTenantWorkspaces(ctx context.Context, callerSubject string) ([]TenantWorkspace, error)
	CreateTenantWorkspace(ctx context.Context, callerSubject string, name string) (TenantWorkspace, error)
}
