package managedagents

import "context"

type AgentDeliberationContextStore interface {
	CreateAgentDeliberationContext(ctx context.Context, input CreateAgentDeliberationInput) (AgentDeliberation, error)
	GetAgentDeliberationContext(ctx context.Context, id string) (AgentDeliberation, error)
	GetAgentDeliberationByIdempotencyContext(ctx context.Context, parentSessionID string, idempotencyKey string) (AgentDeliberation, error)
	ListAgentDeliberationsByParentSessionContext(ctx context.Context, parentSessionID string) ([]AgentDeliberation, error)
	UpdateAgentDeliberationContext(ctx context.Context, id string, input UpdateAgentDeliberationInput) (AgentDeliberation, error)
	ListAgentDeliberationParticipantsContext(ctx context.Context, deliberationID string) ([]AgentDeliberationParticipant, error)
	CreateAgentDeliberationRoundContext(ctx context.Context, round AgentDeliberationRound) (AgentDeliberationRound, error)
	GetAgentDeliberationRoundContext(ctx context.Context, deliberationID string, roundNumber int) (AgentDeliberationRound, error)
	ListAgentDeliberationRoundsContext(ctx context.Context, deliberationID string) ([]AgentDeliberationRound, error)
	UpdateAgentDeliberationRoundContext(ctx context.Context, deliberationID string, roundNumber int, input UpdateAgentDeliberationRoundInput) (AgentDeliberationRound, error)
	UpsertAgentDeliberationContributionContext(ctx context.Context, contribution AgentDeliberationContribution) (AgentDeliberationContribution, error)
	ListAgentDeliberationContributionsContext(ctx context.Context, deliberationID string, roundNumber int) ([]AgentDeliberationContribution, error)
}

func deliberationContextStore(store AgentDeliberationStore) (AgentDeliberationContextStore, bool) {
	scoped, ok := store.(AgentDeliberationContextStore)
	return scoped, ok
}

func CreateAgentDeliberationWithContext(ctx context.Context, store AgentDeliberationStore, input CreateAgentDeliberationInput) (AgentDeliberation, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.CreateAgentDeliberationContext(ctx, input)
	}
	return store.CreateAgentDeliberation(input)
}

func GetAgentDeliberationWithContext(ctx context.Context, store AgentDeliberationStore, id string) (AgentDeliberation, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.GetAgentDeliberationContext(ctx, id)
	}
	return store.GetAgentDeliberation(id)
}

func GetAgentDeliberationByIdempotencyWithContext(ctx context.Context, store AgentDeliberationStore, parentSessionID string, idempotencyKey string) (AgentDeliberation, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.GetAgentDeliberationByIdempotencyContext(ctx, parentSessionID, idempotencyKey)
	}
	return store.GetAgentDeliberationByIdempotency(parentSessionID, idempotencyKey)
}

func ListAgentDeliberationsByParentSessionWithContext(ctx context.Context, store AgentDeliberationStore, parentSessionID string) ([]AgentDeliberation, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.ListAgentDeliberationsByParentSessionContext(ctx, parentSessionID)
	}
	return store.ListAgentDeliberationsByParentSession(parentSessionID)
}

func UpdateAgentDeliberationWithContext(ctx context.Context, store AgentDeliberationStore, id string, input UpdateAgentDeliberationInput) (AgentDeliberation, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.UpdateAgentDeliberationContext(ctx, id, input)
	}
	return store.UpdateAgentDeliberation(id, input)
}

func ListAgentDeliberationParticipantsWithContext(ctx context.Context, store AgentDeliberationStore, deliberationID string) ([]AgentDeliberationParticipant, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.ListAgentDeliberationParticipantsContext(ctx, deliberationID)
	}
	return store.ListAgentDeliberationParticipants(deliberationID)
}

func CreateAgentDeliberationRoundWithContext(ctx context.Context, store AgentDeliberationStore, round AgentDeliberationRound) (AgentDeliberationRound, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.CreateAgentDeliberationRoundContext(ctx, round)
	}
	return store.CreateAgentDeliberationRound(round)
}

func GetAgentDeliberationRoundWithContext(ctx context.Context, store AgentDeliberationStore, deliberationID string, roundNumber int) (AgentDeliberationRound, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.GetAgentDeliberationRoundContext(ctx, deliberationID, roundNumber)
	}
	return store.GetAgentDeliberationRound(deliberationID, roundNumber)
}

func ListAgentDeliberationRoundsWithContext(ctx context.Context, store AgentDeliberationStore, deliberationID string) ([]AgentDeliberationRound, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.ListAgentDeliberationRoundsContext(ctx, deliberationID)
	}
	return store.ListAgentDeliberationRounds(deliberationID)
}

func UpdateAgentDeliberationRoundWithContext(ctx context.Context, store AgentDeliberationStore, deliberationID string, roundNumber int, input UpdateAgentDeliberationRoundInput) (AgentDeliberationRound, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.UpdateAgentDeliberationRoundContext(ctx, deliberationID, roundNumber, input)
	}
	return store.UpdateAgentDeliberationRound(deliberationID, roundNumber, input)
}

func UpsertAgentDeliberationContributionWithContext(ctx context.Context, store AgentDeliberationStore, contribution AgentDeliberationContribution) (AgentDeliberationContribution, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.UpsertAgentDeliberationContributionContext(ctx, contribution)
	}
	return store.UpsertAgentDeliberationContribution(contribution)
}

func ListAgentDeliberationContributionsWithContext(ctx context.Context, store AgentDeliberationStore, deliberationID string, roundNumber int) ([]AgentDeliberationContribution, error) {
	if scoped, ok := deliberationContextStore(store); ok {
		return scoped.ListAgentDeliberationContributionsContext(ctx, deliberationID, roundNumber)
	}
	return store.ListAgentDeliberationContributions(deliberationID, roundNumber)
}
