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
	EnsureAgent(input EnsureAgentInput) (Agent, error)
	CreateAgent(input CreateAgentInput) (Agent, error)
	GetAgent(id string) (Agent, error)
	ListAgents() ([]Agent, error)
	ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error)
	CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error)
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
	CreateSession(input CreateSessionInput) (Session, error)
	GetSession(id string) (Session, error)
	ListSessions(input ListSessionsInput) ([]Session, error)
	UpdateSessionRuntimeSettings(id string, input UpdateSessionRuntimeSettingsInput) (Session, error)
	UpgradeSessionAgentConfig(id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error)
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
	RecordObservabilityExporterRun(input RecordObservabilityExporterRunInput) (ObservabilityExporterRun, error)
	ListObservabilityExporterRuns(input ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error)
	CreateObjectRef(input CreateObjectRefInput) (ObjectRef, error)
	GetObjectRef(id string) (ObjectRef, error)
	CountSessionArtifactsByObjectRef(objectRefID string) (int, error)
	DeleteObjectRef(id string) error
	CreateSessionArtifact(input CreateSessionArtifactInput) (SessionArtifact, error)
	GetSessionArtifact(sessionID string, artifactID string) (SessionArtifact, error)
	DeleteSessionArtifact(sessionID string, artifactID string) error
	ListSessionArtifacts(sessionID string) ([]SessionArtifact, error)
	RegisterWorker(input RegisterWorkerInput) (Worker, error)
	GetWorker(id string) (Worker, error)
	ListWorkers(input ListWorkersInput) ([]Worker, error)
	HeartbeatWorker(id string, input WorkerHeartbeatInput) (Worker, error)
	ArchiveWorker(id string) (Worker, error)
	ReapExpiredWorkers(input ReapExpiredWorkersInput) ([]Worker, error)
	EnqueueWorkerWork(input EnqueueWorkerWorkInput) (WorkerWork, error)
	GetWorkerWork(id string) (WorkerWork, error)
	PollWorkerWork(workerID string, input PollWorkerWorkInput) (*WorkerWork, error)
	AckWorkerWork(workerID string, workID string) (WorkerWork, error)
	HeartbeatWorkerWork(workerID string, workID string, input WorkerWorkHeartbeatInput) (WorkerWork, error)
	CancelWorkerWork(workID string, input CancelWorkerWorkInput) (WorkerWork, error)
	RequeueWorkerWork(workID string, input RequeueWorkerWorkInput) (WorkerWork, error)
	ReapExpiredWorkerWork(input ReapExpiredWorkerWorkInput) ([]WorkerWork, error)
	CompleteWorkerWork(workerID string, workID string, input CompleteWorkerWorkInput) (WorkerWork, error)
	ListEvents(sessionID string, afterSeq int64) ([]Event, error)
	ListConversationMessages(sessionID string, beforeSeq int64) ([]ConversationMessage, error)
	SubscribeEvents(sessionID string, afterSeq int64) (<-chan Event, func(), error)
}

type TraceIndexStore interface {
	UpsertTraceIndex(input UpsertTraceIndexInput) error
	GetTraceIndex(traceID string) (TraceIndexEntry, error)
	ListTraceIndexes(input ListTraceIndexInput) ([]TraceIndexEntry, error)
	ListTraceSpanIndexes(input ListTraceSpanIndexInput) ([]TraceSpanIndexEntry, error)
	PruneTraceIndexes(input PruneTraceIndexInput) (int, error)
}

type SessionTurnQueueStore interface {
	ClaimSessionTurns(input ClaimSessionTurnsInput) ([]SessionTurnWork, error)
	RenewSessionTurnLease(input RenewSessionTurnLeaseInput) (bool, error)
	ReleaseSessionTurnLease(input ReleaseSessionTurnLeaseInput) error
}
