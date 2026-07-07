package managedagents

import "encoding/json"

type Store interface {
	EnsureLLMProvider(input EnsureLLMProviderInput) (LLMProvider, error)
	UpsertLLMProvider(input UpsertLLMProviderInput) (LLMProvider, error)
	GetLLMProvider(id string) (LLMProvider, error)
	ListLLMProviders() ([]LLMProvider, error)
	SetLLMProviderEnabled(id string, enabled bool) (LLMProvider, error)
	CreateAgent(input CreateAgentInput) (Agent, error)
	GetAgent(id string) (Agent, error)
	ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error)
	CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error)
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
	CreateSession(input CreateSessionInput) (Session, error)
	GetSession(id string) (Session, error)
	ResolveAgentRuntimeConfig(sessionID string) (AgentRuntimeConfig, error)
	ArchiveSession(id string) (Session, error)
	DeleteSession(id string) error
	AppendEvents(sessionID string, inputs []AppendEventInput) ([]Event, error)
	AppendRuntimeEvent(sessionID string, turnID string, input AppendEventInput) ([]Event, error)
	CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error)
	FailSessionTurn(sessionID string, turnID string, reason string) ([]Event, error)
	RecordLLMUsage(input RecordLLMUsageInput) (LLMUsageRecord, error)
	ListEvents(sessionID string, afterSeq int64) ([]Event, error)
	ListConversationMessages(sessionID string, beforeSeq int64) ([]ConversationMessage, error)
	SubscribeEvents(sessionID string) (<-chan Event, func(), error)
}
