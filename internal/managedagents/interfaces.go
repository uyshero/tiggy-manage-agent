package managedagents

import "encoding/json"

type Store interface {
	EnsureLLMProvider(input EnsureLLMProviderInput) (LLMProvider, error)
	UpsertLLMProvider(input UpsertLLMProviderInput) (LLMProvider, error)
	GetLLMProvider(id string) (LLMProvider, error)
	ListLLMProviders() ([]LLMProvider, error)
	SetLLMProviderEnabled(id string, enabled bool) (LLMProvider, error)
	UpsertLLMModel(input UpsertLLMModelInput) (LLMModel, error)
	ListLLMModels(providerID string) ([]LLMModel, error)
	CreateAgent(input CreateAgentInput) (Agent, error)
	GetAgent(id string) (Agent, error)
	ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error)
	CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error)
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
	CreateSession(input CreateSessionInput) (Session, error)
	GetSession(id string) (Session, error)
	UpdateSessionRuntimeSettings(id string, input UpdateSessionRuntimeSettingsInput) (Session, error)
	ResolveAgentRuntimeConfig(sessionID string) (AgentRuntimeConfig, error)
	SaveSessionIntervention(sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error)
	ListSessionInterventions(sessionID string, status string) ([]SessionIntervention, error)
	DecideSessionIntervention(sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error)
	MarkSessionTurnWaitingApproval(sessionID string, turnID string) error
	GetSessionSummary(sessionID string) (SessionSummary, error)
	SaveSessionSummary(sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error)
	UpsertSessionSummary(sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error)
	ArchiveSession(id string) (Session, error)
	DeleteSession(id string) error
	AppendEvents(sessionID string, inputs []AppendEventInput) ([]Event, error)
	AppendRuntimeEvent(sessionID string, turnID string, input AppendEventInput) ([]Event, error)
	CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error)
	FailSessionTurn(sessionID string, turnID string, reason string) ([]Event, error)
	RecordLLMUsage(input RecordLLMUsageInput) (LLMUsageRecord, error)
	GetSessionLLMUsage(sessionID string) (LLMUsageReport, error)
	ListLLMUsage(input ListLLMUsageInput) (LLMUsageAggregateReport, error)
	ListEvents(sessionID string, afterSeq int64) ([]Event, error)
	ListConversationMessages(sessionID string, beforeSeq int64) ([]ConversationMessage, error)
	SubscribeEvents(sessionID string) (<-chan Event, func(), error)
}
