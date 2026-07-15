package managedagents

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	BuiltinGeneralAgentID     = "agt_general"
	BuiltinGeneralAgentName   = "通用智能体"
	BuiltinGeneralAgentSystem = "You are TMA's built-in general-purpose agent. Help users analyze information, plan work, create and edit files, run tools, and verify results. Be concise and practical. Use available tools when they improve accuracy, respect approval requirements, and clearly report blockers and completed work."
)

func BuiltinGeneralAgentInput(llmProvider string, llmModel string) EnsureAgentInput {
	return BuiltinGeneralAgentInputForWorkspace(DefaultWorkspaceID, llmProvider, llmModel)
}

func BuiltinGeneralAgentInputForWorkspace(workspaceID string, llmProvider string, llmModel string) EnsureAgentInput {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = DefaultWorkspaceID
	}
	return EnsureAgentInput{
		ID:          BuiltinGeneralAgentIDForWorkspace(workspaceID),
		WorkspaceID: workspaceID,
		Name:        BuiltinGeneralAgentName,
		LLMProvider: llmProvider,
		LLMModel:    llmModel,
		System:      BuiltinGeneralAgentSystem,
	}
}

func BuiltinGeneralAgentIDForWorkspace(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || workspaceID == DefaultWorkspaceID {
		return BuiltinGeneralAgentID
	}
	digest := sha256.Sum256([]byte(workspaceID))
	return BuiltinGeneralAgentID + "_" + hex.EncodeToString(digest[:6])
}
