package managedagents

import "encoding/json"

type Store interface {
	CreateAgent(input CreateAgentInput) (Agent, error)
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
	CreateSession(input CreateSessionInput) (Session, error)
	GetSession(id string) (Session, error)
	ArchiveSession(id string) (Session, error)
	DeleteSession(id string) error
	AppendEvents(sessionID string, inputs []AppendEventInput) ([]Event, error)
	AppendRuntimeEvent(sessionID string, turnID string, input AppendEventInput) ([]Event, error)
	CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error)
	FailSessionTurn(sessionID string, turnID string, reason string) ([]Event, error)
	ListEvents(sessionID string, afterSeq int64) ([]Event, error)
	SubscribeEvents(sessionID string) (<-chan Event, func(), error)
}
