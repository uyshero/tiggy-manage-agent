package httpapi

import (
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/serverconfig"
)

type SubagentPolicy struct {
	MaxDepth              int
	MaxChildrenPerTurn    int
	MaxChildrenPerSession int
	WorkspaceActiveLimit  int
	UserActiveLimit       int
	WorkspaceQueuedLimit  int
	UserQueuedLimit       int
	QueueTimeoutSeconds   int
}

func defaultSubagentPolicy() SubagentPolicy {
	return SubagentPolicy{
		MaxDepth:              serverconfig.DefaultSubagentMaxDepth,
		MaxChildrenPerTurn:    serverconfig.DefaultSubagentMaxChildrenPerTurn,
		MaxChildrenPerSession: serverconfig.DefaultSubagentMaxChildrenPerSession,
		WorkspaceActiveLimit:  serverconfig.DefaultSubagentWorkspaceActiveLimit,
		UserActiveLimit:       serverconfig.DefaultSubagentUserActiveLimit,
		WorkspaceQueuedLimit:  serverconfig.DefaultSubagentWorkspaceQueuedLimit,
		UserQueuedLimit:       serverconfig.DefaultSubagentUserQueuedLimit,
		QueueTimeoutSeconds:   serverconfig.DefaultSubagentQueueTimeoutSeconds,
	}
}

func (p SubagentPolicy) storeLimits() managedagents.SubagentLimits {
	return managedagents.SubagentLimits{
		MaxDepth:              p.MaxDepth,
		MaxChildrenPerTurn:    p.MaxChildrenPerTurn,
		MaxChildrenPerSession: p.MaxChildrenPerSession,
		WorkspaceActiveLimit:  p.WorkspaceActiveLimit,
		UserActiveLimit:       p.UserActiveLimit,
		WorkspaceQueuedLimit:  p.WorkspaceQueuedLimit,
		UserQueuedLimit:       p.UserQueuedLimit,
		QueueTimeoutSeconds:   p.QueueTimeoutSeconds,
	}
}
