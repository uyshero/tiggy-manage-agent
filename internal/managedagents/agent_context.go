package managedagents

import "context"

type agentEnsurer interface {
	EnsureAgent(input EnsureAgentInput) (Agent, error)
}

type agentCreator interface {
	CreateAgent(input CreateAgentInput) (Agent, error)
}

type agentReader interface {
	GetAgent(id string) (Agent, error)
}

type agentLister interface {
	ListAgents() ([]Agent, error)
}

type agentUpdater interface {
	UpdateAgent(input UpdateAgentInput) (Agent, error)
}

type agentConfigVersionLister interface {
	ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error)
}

type agentConfigVersionCreator interface {
	CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error)
}

func EnsureAgentWithContext(ctx context.Context, store agentEnsurer, input EnsureAgentInput) (Agent, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.EnsureAgentContext(ctx, input)
	}
	return store.EnsureAgent(input)
}

func CreateAgentWithContext(ctx context.Context, store agentCreator, input CreateAgentInput) (Agent, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.CreateAgentContext(ctx, input)
	}
	return store.CreateAgent(input)
}

func GetAgentWithContext(ctx context.Context, store agentReader, id string) (Agent, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.GetAgentContext(ctx, id)
	}
	return store.GetAgent(id)
}

func ListAgentsWithContext(ctx context.Context, store agentLister) ([]Agent, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.ListAgentsContext(ctx)
	}
	return store.ListAgents()
}

func UpdateAgentWithContext(ctx context.Context, store agentUpdater, input UpdateAgentInput) (Agent, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.UpdateAgentContext(ctx, input)
	}
	return store.UpdateAgent(input)
}

func ListAgentConfigVersionsWithContext(ctx context.Context, store agentConfigVersionLister, agentID string) ([]AgentConfigVersion, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.ListAgentConfigVersionsContext(ctx, agentID)
	}
	return store.ListAgentConfigVersions(agentID)
}

func CreateAgentConfigVersionWithContext(ctx context.Context, store agentConfigVersionCreator, input CreateAgentConfigVersionInput) (Agent, error) {
	if scoped, ok := store.(AgentContextStore); ok {
		return scoped.CreateAgentConfigVersionContext(ctx, input)
	}
	return store.CreateAgentConfigVersion(input)
}
