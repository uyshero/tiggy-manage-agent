package managedagents

import (
	"context"
)

func (s *PostgresStore) EnsureAgentContext(ctx context.Context, input EnsureAgentInput) (Agent, error) {
	return s.ensureAgentContext(ctx, input)
}

func (s *PostgresStore) CreateAgentContext(ctx context.Context, input CreateAgentInput) (Agent, error) {
	return s.createAgentContext(ctx, input)
}

func (s *PostgresStore) GetAgentContext(ctx context.Context, id string) (Agent, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		return s.GetAgentScoped(id, scope)
	}
	return s.GetAgent(id)
}

func (s *PostgresStore) ListAgentsContext(ctx context.Context) ([]Agent, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		return s.ListAgentsScoped(scope)
	}
	return s.ListAgents()
}

func (s *PostgresStore) UpdateAgentContext(ctx context.Context, input UpdateAgentInput) (Agent, error) {
	return s.updateAgentContext(ctx, input)
}

func (s *PostgresStore) ListAgentConfigVersionsContext(ctx context.Context, agentID string) ([]AgentConfigVersion, error) {
	return s.listAgentConfigVersionsContext(ctx, agentID)
}

func (s *PostgresStore) CreateAgentConfigVersionContext(ctx context.Context, input CreateAgentConfigVersionInput) (Agent, error) {
	return s.createAgentConfigVersionContext(ctx, input)
}
