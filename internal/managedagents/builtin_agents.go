package managedagents

const (
	BuiltinGeneralAgentID     = "agt_general"
	BuiltinGeneralAgentName   = "通用智能体"
	BuiltinGeneralAgentSystem = "You are TMA's built-in general-purpose agent. Help users analyze information, plan work, create and edit files, run tools, and verify results. Be concise and practical. Use available tools when they improve accuracy, respect approval requirements, and clearly report blockers and completed work."
)

func BuiltinGeneralAgentInput(llmProvider string, llmModel string) EnsureAgentInput {
	return EnsureAgentInput{
		ID:          BuiltinGeneralAgentID,
		WorkspaceID: DefaultWorkspaceID,
		Name:        BuiltinGeneralAgentName,
		LLMProvider: llmProvider,
		LLMModel:    llmModel,
		System:      BuiltinGeneralAgentSystem,
	}
}
