package managedagents

import (
	"context"
	"encoding/json"
)

type sessionCreator interface {
	CreateSession(input CreateSessionInput) (Session, error)
}

type subagentSessionCreator interface {
	CreateSubagentSession(input CreateSubagentSessionInput) (Session, error)
}

type sessionReader interface {
	GetSession(id string) (Session, error)
}

type sessionLister interface {
	ListSessions(input ListSessionsInput) ([]Session, error)
}

type sessionRuntimeSettingsUpdater interface {
	UpdateSessionRuntimeSettings(id string, input UpdateSessionRuntimeSettingsInput) (Session, error)
}

type sessionMetadataUpdater interface {
	UpdateSessionMetadata(id string, input UpdateSessionMetadataInput) (Session, error)
}

type sessionArchiver interface {
	ArchiveSession(id string) (Session, error)
}

type sessionRestorer interface {
	RestoreSession(id string) (Session, error)
}

type sessionDeleter interface {
	DeleteSession(id string) error
}

type sessionEventAppender interface {
	AppendEvents(sessionID string, inputs []AppendEventInput) ([]Event, error)
}

type sessionEventLister interface {
	ListEvents(sessionID string, afterSeq int64) ([]Event, error)
}

type subagentTurnStarter interface {
	StartSubagentTurn(input StartSubagentTurnInput) ([]Event, error)
}

type subagentStartEnqueuer interface {
	EnqueueSubagentStart(input EnqueueSubagentStartInput) (SubagentStartRequest, error)
}

type subagentStartContextEnqueuer interface {
	EnqueueSubagentStartContext(ctx context.Context, input EnqueueSubagentStartInput) (SubagentStartRequest, error)
}

type subagentStartCanceler interface {
	CancelSubagentStart(input CancelSubagentStartInput) (SubagentStartRequest, error)
}

type subagentStartContextCanceler interface {
	CancelSubagentStartContext(ctx context.Context, input CancelSubagentStartInput) (SubagentStartRequest, error)
}

type subagentStartReader interface {
	GetPendingSubagentStart(sessionID string) (SubagentStartRequest, error)
}

type subagentStartContextReader interface {
	GetPendingSubagentStartContext(ctx context.Context, sessionID string) (SubagentStartRequest, error)
}

type sessionAgentConfigUpgrader interface {
	UpgradeSessionAgentConfig(id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error)
}

type sessionSummaryReader interface {
	GetSessionSummary(sessionID string) (SessionSummary, error)
}

type sessionSummarySaver interface {
	SaveSessionSummary(sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error)
}

type sessionSummaryUpserter interface {
	UpsertSessionSummary(sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error)
}

type sessionInterventionSaver interface {
	SaveSessionIntervention(sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error)
}

type sessionInterventionLister interface {
	ListSessionInterventions(sessionID string, status string) ([]SessionIntervention, error)
}

type sessionInterventionDecider interface {
	DecideSessionIntervention(sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error)
}

type sessionTurnApprovalMarker interface {
	MarkSessionTurnWaitingApproval(sessionID string, turnID string) error
}

type sessionTurnHumanMarker interface {
	MarkSessionTurnWaitingHuman(sessionID string, turnID string) error
}

type sessionTurnHumanContextMarker interface {
	MarkSessionTurnWaitingHumanContext(ctx context.Context, sessionID string, turnID string) error
}

type agentRuntimeConfigResolver interface {
	ResolveAgentRuntimeConfig(sessionID string) (AgentRuntimeConfig, error)
}

type conversationMessageLister interface {
	ListConversationMessages(sessionID string, beforeSeq int64) ([]ConversationMessage, error)
}

type runtimeEventAppender interface {
	AppendRuntimeEvent(sessionID string, turnID string, input AppendEventInput) ([]Event, error)
}

type sessionTurnCompleter interface {
	CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error)
}

type sessionTurnFailer interface {
	FailSessionTurn(sessionID string, turnID string, reason string) ([]Event, error)
}

type sessionEventSubscriber interface {
	SubscribeEvents(sessionID string, afterSeq int64) (<-chan Event, func(), error)
}

type sessionLLMUsageReader interface {
	GetSessionLLMUsage(sessionID string) (LLMUsageReport, error)
}

func CreateSessionWithContext(ctx context.Context, store sessionCreator, input CreateSessionInput) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.CreateSessionContext(ctx, input)
	}
	return store.CreateSession(input)
}

func CreateSubagentSessionWithContext(ctx context.Context, store subagentSessionCreator, input CreateSubagentSessionInput) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.CreateSubagentSessionContext(ctx, input)
	}
	return store.CreateSubagentSession(input)
}

func GetSessionWithContext(ctx context.Context, store sessionReader, id string) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.GetSessionContext(ctx, id)
	}
	return store.GetSession(id)
}

func ListSessionsWithContext(ctx context.Context, store sessionLister, input ListSessionsInput) ([]Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.ListSessionsContext(ctx, input)
	}
	return store.ListSessions(input)
}

func UpdateSessionRuntimeSettingsWithContext(ctx context.Context, store sessionRuntimeSettingsUpdater, id string, input UpdateSessionRuntimeSettingsInput) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.UpdateSessionRuntimeSettingsContext(ctx, id, input)
	}
	return store.UpdateSessionRuntimeSettings(id, input)
}

func UpdateSessionMetadataWithContext(ctx context.Context, store sessionMetadataUpdater, id string, input UpdateSessionMetadataInput) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.UpdateSessionMetadataContext(ctx, id, input)
	}
	return store.UpdateSessionMetadata(id, input)
}

func ArchiveSessionWithContext(ctx context.Context, store sessionArchiver, id string) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.ArchiveSessionContext(ctx, id)
	}
	return store.ArchiveSession(id)
}

func RestoreSessionWithContext(ctx context.Context, store sessionRestorer, id string) (Session, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.RestoreSessionContext(ctx, id)
	}
	return store.RestoreSession(id)
}

func DeleteSessionWithContext(ctx context.Context, store sessionDeleter, id string) error {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.DeleteSessionContext(ctx, id)
	}
	return store.DeleteSession(id)
}

func AppendEventsWithContext(ctx context.Context, store sessionEventAppender, sessionID string, inputs []AppendEventInput) ([]Event, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.AppendEventsContext(ctx, sessionID, inputs)
	}
	return store.AppendEvents(sessionID, inputs)
}

func ListEventsWithContext(ctx context.Context, store sessionEventLister, sessionID string, afterSeq int64) ([]Event, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.ListEventsContext(ctx, sessionID, afterSeq)
	}
	return store.ListEvents(sessionID, afterSeq)
}

func StartSubagentTurnWithContext(ctx context.Context, store subagentTurnStarter, input StartSubagentTurnInput) ([]Event, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.StartSubagentTurnContext(ctx, input)
	}
	return store.StartSubagentTurn(input)
}

func EnqueueSubagentStartWithContext(ctx context.Context, store subagentStartEnqueuer, input EnqueueSubagentStartInput) (SubagentStartRequest, error) {
	if scoped, ok := store.(subagentStartContextEnqueuer); ok {
		return scoped.EnqueueSubagentStartContext(ctx, input)
	}
	return store.EnqueueSubagentStart(input)
}

func CancelSubagentStartWithContext(ctx context.Context, store subagentStartCanceler, input CancelSubagentStartInput) (SubagentStartRequest, error) {
	if scoped, ok := store.(subagentStartContextCanceler); ok {
		return scoped.CancelSubagentStartContext(ctx, input)
	}
	return store.CancelSubagentStart(input)
}

func GetPendingSubagentStartWithContext(ctx context.Context, store subagentStartReader, sessionID string) (SubagentStartRequest, error) {
	if scoped, ok := store.(subagentStartContextReader); ok {
		return scoped.GetPendingSubagentStartContext(ctx, sessionID)
	}
	return store.GetPendingSubagentStart(sessionID)
}

func UpgradeSessionAgentConfigWithContext(ctx context.Context, store sessionAgentConfigUpgrader, id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.UpgradeSessionAgentConfigContext(ctx, id, input)
	}
	return store.UpgradeSessionAgentConfig(id, input)
}

func GetSessionSummaryWithContext(ctx context.Context, store sessionSummaryReader, sessionID string) (SessionSummary, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.GetSessionSummaryContext(ctx, sessionID)
	}
	return store.GetSessionSummary(sessionID)
}

func SaveSessionSummaryWithContext(ctx context.Context, store sessionSummarySaver, sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.SaveSessionSummaryContext(ctx, sessionID, input)
	}
	return store.SaveSessionSummary(sessionID, input)
}

func UpsertSessionSummaryWithContext(ctx context.Context, store sessionSummaryUpserter, sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.UpsertSessionSummaryContext(ctx, sessionID, input)
	}
	return store.UpsertSessionSummary(sessionID, input)
}

func SaveSessionInterventionWithContext(ctx context.Context, store sessionInterventionSaver, sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.SaveSessionInterventionContext(ctx, sessionID, input)
	}
	return store.SaveSessionIntervention(sessionID, input)
}

func ListSessionInterventionsWithContext(ctx context.Context, store sessionInterventionLister, sessionID string, status string) ([]SessionIntervention, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.ListSessionInterventionsContext(ctx, sessionID, status)
	}
	return store.ListSessionInterventions(sessionID, status)
}

func DecideSessionInterventionWithContext(ctx context.Context, store sessionInterventionDecider, sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.DecideSessionInterventionContext(ctx, sessionID, input)
	}
	return store.DecideSessionIntervention(sessionID, input)
}

func MarkSessionTurnWaitingApprovalWithContext(ctx context.Context, store sessionTurnApprovalMarker, sessionID string, turnID string) error {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.MarkSessionTurnWaitingApprovalContext(ctx, sessionID, turnID)
	}
	return store.MarkSessionTurnWaitingApproval(sessionID, turnID)
}

func MarkSessionTurnWaitingHumanWithContext(ctx context.Context, store sessionTurnApprovalMarker, sessionID string, turnID string) error {
	if scoped, ok := store.(sessionTurnHumanContextMarker); ok {
		return scoped.MarkSessionTurnWaitingHumanContext(ctx, sessionID, turnID)
	}
	if marker, ok := store.(sessionTurnHumanMarker); ok {
		return marker.MarkSessionTurnWaitingHuman(sessionID, turnID)
	}
	return MarkSessionTurnWaitingApprovalWithContext(ctx, store, sessionID, turnID)
}

func ResolveAgentRuntimeConfigWithContext(ctx context.Context, store agentRuntimeConfigResolver, sessionID string) (AgentRuntimeConfig, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.ResolveAgentRuntimeConfigContext(ctx, sessionID)
	}
	return store.ResolveAgentRuntimeConfig(sessionID)
}

func ListConversationMessagesWithContext(ctx context.Context, store conversationMessageLister, sessionID string, beforeSeq int64) ([]ConversationMessage, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.ListConversationMessagesContext(ctx, sessionID, beforeSeq)
	}
	return store.ListConversationMessages(sessionID, beforeSeq)
}

func AppendRuntimeEventWithContext(ctx context.Context, store runtimeEventAppender, sessionID string, turnID string, input AppendEventInput) ([]Event, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.AppendRuntimeEventContext(ctx, sessionID, turnID, input)
	}
	return store.AppendRuntimeEvent(sessionID, turnID, input)
}

func CompleteSessionTurnWithContext(ctx context.Context, store sessionTurnCompleter, sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.CompleteSessionTurnContext(ctx, sessionID, turnID, agentPayload)
	}
	return store.CompleteSessionTurn(sessionID, turnID, agentPayload)
}

func FailSessionTurnWithContext(ctx context.Context, store sessionTurnFailer, sessionID string, turnID string, reason string) ([]Event, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.FailSessionTurnContext(ctx, sessionID, turnID, reason)
	}
	return store.FailSessionTurn(sessionID, turnID, reason)
}

func SubscribeEventsWithContext(ctx context.Context, store sessionEventSubscriber, sessionID string, afterSeq int64) (<-chan Event, func(), error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.SubscribeEventsContext(ctx, sessionID, afterSeq)
	}
	return store.SubscribeEvents(sessionID, afterSeq)
}

func GetSessionLLMUsageWithContext(ctx context.Context, store sessionLLMUsageReader, sessionID string) (LLMUsageReport, error) {
	if scoped, ok := store.(SessionContextStore); ok {
		return scoped.GetSessionLLMUsageContext(ctx, sessionID)
	}
	return store.GetSessionLLMUsage(sessionID)
}
