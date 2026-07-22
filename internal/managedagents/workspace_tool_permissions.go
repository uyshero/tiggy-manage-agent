package managedagents

import (
	"context"
	"encoding/json"
	"time"
)

type WorkspaceToolPermissionPolicy struct {
	WorkspaceID string          `json:"workspace_id"`
	Policy      json.RawMessage `json:"policy"`
	Revision    int64           `json:"revision"`
	UpdatedBy   string          `json:"updated_by"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type UpdateWorkspaceToolPermissionPolicyInput struct {
	WorkspaceID      string
	Policy           json.RawMessage
	ExpectedRevision int64
	UpdatedBy        string
}

// WorkspaceToolPermissionStore is separate from Store so policy management
// can be adopted without widening unrelated store implementations.
type WorkspaceToolPermissionStore interface {
	GetWorkspaceToolPermissionPolicyContext(ctx context.Context, workspaceID string) (WorkspaceToolPermissionPolicy, error)
	UpdateWorkspaceToolPermissionPolicyContext(ctx context.Context, input UpdateWorkspaceToolPermissionPolicyInput) (WorkspaceToolPermissionPolicy, error)
}
