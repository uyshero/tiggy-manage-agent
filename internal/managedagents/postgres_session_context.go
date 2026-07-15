package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *PostgresStore) CreateSessionContext(ctx context.Context, input CreateSessionInput) (Session, error) {
	return s.createSessionContext(ctx, input)
}

func (s *PostgresStore) CreateSubagentSessionContext(ctx context.Context, input CreateSubagentSessionInput) (Session, error) {
	return s.createSubagentSessionContext(ctx, input)
}

func (s *PostgresStore) GetSessionContext(ctx context.Context, id string) (Session, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		return s.getSessionScopedContext(ctx, id, scope)
	}
	return s.GetSession(id)
}

func (s *PostgresStore) ListSessionsContext(ctx context.Context, input ListSessionsInput) ([]Session, error) {
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		return s.listSessionsScopedContext(ctx, input, scope)
	}
	return s.ListSessions(input)
}

func (s *PostgresStore) UpdateSessionRuntimeSettingsContext(ctx context.Context, id string, input UpdateSessionRuntimeSettingsInput) (Session, error) {
	return s.updateSessionRuntimeSettingsContext(ctx, id, input)
}

func (s *PostgresStore) UpdateSessionMetadataContext(ctx context.Context, id string, input UpdateSessionMetadataInput) (Session, error) {
	return s.updateSessionMetadataContext(ctx, id, input)
}

func (s *PostgresStore) ArchiveSessionContext(ctx context.Context, id string) (Session, error) {
	return s.archiveSessionContext(ctx, id)
}

func (s *PostgresStore) RestoreSessionContext(ctx context.Context, id string) (Session, error) {
	return s.restoreSessionContext(ctx, id)
}

func (s *PostgresStore) DeleteSessionContext(ctx context.Context, id string) error {
	return s.deleteSessionContext(ctx, id)
}

func (s *PostgresStore) AppendEventsContext(ctx context.Context, sessionID string, inputs []AppendEventInput) ([]Event, error) {
	return s.appendEventsContext(ctx, sessionID, inputs)
}

func (s *PostgresStore) ListEventsContext(ctx context.Context, sessionID string, afterSeq int64) ([]Event, error) {
	return s.listEventsContext(ctx, sessionID, afterSeq)
}

func (s *PostgresStore) StartSubagentTurnContext(ctx context.Context, input StartSubagentTurnInput) ([]Event, error) {
	return s.startSubagentTurnContext(ctx, input)
}

func (s *PostgresStore) UpgradeSessionAgentConfigContext(ctx context.Context, id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error) {
	return s.upgradeSessionAgentConfigContext(ctx, id, input)
}

func (s *PostgresStore) GetSessionSummaryContext(ctx context.Context, sessionID string) (SessionSummary, error) {
	return s.getSessionSummaryContext(ctx, sessionID)
}

func (s *PostgresStore) SaveSessionSummaryContext(ctx context.Context, sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error) {
	return s.saveSessionSummaryContext(ctx, sessionID, input)
}

func (s *PostgresStore) UpsertSessionSummaryContext(ctx context.Context, sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error) {
	return s.upsertSessionSummaryContext(ctx, sessionID, input)
}

func (s *PostgresStore) SaveSessionInterventionContext(ctx context.Context, sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error) {
	return s.saveSessionInterventionContext(ctx, sessionID, input)
}

func (s *PostgresStore) ListSessionInterventionsContext(ctx context.Context, sessionID string, status string) ([]SessionIntervention, error) {
	return s.listSessionInterventionsContext(ctx, sessionID, status)
}

func (s *PostgresStore) DecideSessionInterventionContext(ctx context.Context, sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error) {
	return s.decideSessionInterventionContext(ctx, sessionID, input)
}

func (s *PostgresStore) MarkSessionTurnWaitingApprovalContext(ctx context.Context, sessionID string, turnID string) error {
	return s.markSessionTurnWaitingApprovalContext(ctx, sessionID, turnID)
}

func (s *PostgresStore) ResolveAgentRuntimeConfigContext(ctx context.Context, sessionID string) (AgentRuntimeConfig, error) {
	return s.resolveAgentRuntimeConfigContext(ctx, sessionID)
}

func (s *PostgresStore) ListConversationMessagesContext(ctx context.Context, sessionID string, beforeSeq int64) ([]ConversationMessage, error) {
	return s.listConversationMessagesContext(ctx, sessionID, beforeSeq)
}

func (s *PostgresStore) AppendRuntimeEventContext(ctx context.Context, sessionID string, turnID string, input AppendEventInput) ([]Event, error) {
	return s.appendRuntimeEventContext(ctx, sessionID, turnID, input)
}

func (s *PostgresStore) CompleteSessionTurnContext(ctx context.Context, sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error) {
	return s.completeSessionTurnContext(ctx, sessionID, turnID, agentPayload)
}

func (s *PostgresStore) FailSessionTurnContext(ctx context.Context, sessionID string, turnID string, reason string) ([]Event, error) {
	return s.failSessionTurnContext(ctx, sessionID, turnID, reason)
}

func (s *PostgresStore) SubscribeEventsContext(ctx context.Context, sessionID string, afterSeq int64) (<-chan Event, func(), error) {
	return s.subscribeEventsContext(ctx, sessionID, afterSeq)
}

func (s *PostgresStore) GetSessionLLMUsageContext(ctx context.Context, sessionID string) (LLMUsageReport, error) {
	return s.getSessionLLMUsageContext(ctx, sessionID)
}

func setContextDatabaseAccessScope(ctx context.Context, tx *sql.Tx) (AccessScope, bool, error) {
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok {
		return AccessScope{}, false, nil
	}
	scope, err := setDatabaseAccessScope(ctx, tx, scope.WorkspaceID)
	return scope, true, err
}

func authorizeSessionAccessScope(session Session, scope AccessScope, scoped bool) error {
	if !scoped {
		return nil
	}
	if session.WorkspaceID != scope.WorkspaceID || (scope.OwnerID != "" && session.OwnerID != scope.OwnerID) {
		return ErrForbidden
	}
	return nil
}
