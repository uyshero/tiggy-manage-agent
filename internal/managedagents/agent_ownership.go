package managedagents

import (
	"fmt"
	"strings"
)

const (
	AgentOwnerUser      = "user"
	AgentOwnerWorkspace = "workspace"

	AgentVisibilityPrivate   = "private"
	AgentVisibilityWorkspace = "workspace"

	AgentKindGeneral = "general"
	AgentKindCustom  = "custom"
)

type AgentOwnership struct {
	OwnerType  string
	OwnerID    string
	Visibility string
	AgentKind  string
}

func NormalizeAgentOwnership(workspaceID string, ownership AgentOwnership) (AgentOwnership, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	ownership.OwnerType = strings.TrimSpace(strings.ToLower(ownership.OwnerType))
	ownership.OwnerID = strings.TrimSpace(ownership.OwnerID)
	ownership.Visibility = strings.TrimSpace(strings.ToLower(ownership.Visibility))
	ownership.AgentKind = strings.TrimSpace(strings.ToLower(ownership.AgentKind))
	if ownership.OwnerType == "" {
		ownership.OwnerType = AgentOwnerWorkspace
	}
	if ownership.AgentKind == "" {
		ownership.AgentKind = AgentKindCustom
	}
	switch ownership.AgentKind {
	case AgentKindGeneral, AgentKindCustom:
	default:
		return AgentOwnership{}, fmt.Errorf("%w: unsupported Agent agent_kind %q", ErrInvalid, ownership.AgentKind)
	}
	switch ownership.OwnerType {
	case AgentOwnerUser:
		if ownership.OwnerID == "" {
			return AgentOwnership{}, fmt.Errorf("%w: user-owned Agent requires owner_id", ErrInvalid)
		}
		if ownership.Visibility == "" {
			ownership.Visibility = AgentVisibilityPrivate
		}
		if ownership.Visibility != AgentVisibilityPrivate {
			return AgentOwnership{}, fmt.Errorf("%w: user-owned Agent visibility must be private", ErrInvalid)
		}
	case AgentOwnerWorkspace:
		if ownership.OwnerID == "" {
			ownership.OwnerID = workspaceID
		}
		if ownership.OwnerID == "" || ownership.OwnerID != workspaceID {
			return AgentOwnership{}, fmt.Errorf("%w: workspace-owned Agent owner_id must match workspace_id", ErrInvalid)
		}
		if ownership.Visibility == "" {
			ownership.Visibility = AgentVisibilityWorkspace
		}
		if ownership.Visibility != AgentVisibilityWorkspace {
			return AgentOwnership{}, fmt.Errorf("%w: workspace-owned Agent visibility must be workspace", ErrInvalid)
		}
	default:
		return AgentOwnership{}, fmt.Errorf("%w: unsupported Agent owner_type %q", ErrInvalid, ownership.OwnerType)
	}
	return ownership, nil
}
