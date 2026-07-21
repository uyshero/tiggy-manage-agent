package managedagents

import (
	"context"
	"encoding/json"
	"time"

	"tiggy-manage-agent/internal/agentcore"
)

type DatabaseTenantIsolationValidator interface {
	ValidateDatabaseTenantIsolation(ctx context.Context) error
}

type AgentContextStore interface {
	EnsureAgentContext(ctx context.Context, input EnsureAgentInput) (Agent, error)
	CreateAgentContext(ctx context.Context, input CreateAgentInput) (Agent, error)
	GetAgentContext(ctx context.Context, id string) (Agent, error)
	ListAgentsContext(ctx context.Context) ([]Agent, error)
	UpdateAgentContext(ctx context.Context, input UpdateAgentInput) (Agent, error)
	ListAgentConfigVersionsContext(ctx context.Context, agentID string) ([]AgentConfigVersion, error)
	CreateAgentConfigVersionContext(ctx context.Context, input CreateAgentConfigVersionInput) (Agent, error)
}

type EnvironmentContextStore interface {
	CreateEnvironmentContext(ctx context.Context, input CreateEnvironmentInput) (Environment, error)
}

type SessionContextStore interface {
	CreateSessionContext(ctx context.Context, input CreateSessionInput) (Session, error)
	CreateSubagentSessionContext(ctx context.Context, input CreateSubagentSessionInput) (Session, error)
	GetSessionContext(ctx context.Context, id string) (Session, error)
	ListSessionsContext(ctx context.Context, input ListSessionsInput) ([]Session, error)
	UpdateSessionRuntimeSettingsContext(ctx context.Context, id string, input UpdateSessionRuntimeSettingsInput) (Session, error)
	UpdateSessionMetadataContext(ctx context.Context, id string, input UpdateSessionMetadataInput) (Session, error)
	ArchiveSessionContext(ctx context.Context, id string) (Session, error)
	RestoreSessionContext(ctx context.Context, id string) (Session, error)
	DeleteSessionContext(ctx context.Context, id string) error
	AppendEventsContext(ctx context.Context, sessionID string, inputs []AppendEventInput) ([]Event, error)
	ListEventsContext(ctx context.Context, sessionID string, afterSeq int64) ([]Event, error)
	StartSubagentTurnContext(ctx context.Context, input StartSubagentTurnInput) ([]Event, error)
	UpgradeSessionAgentConfigContext(ctx context.Context, id string, input UpgradeSessionAgentConfigInput) (UpgradeSessionAgentConfigResult, error)
	GetSessionSummaryContext(ctx context.Context, sessionID string) (SessionSummary, error)
	SaveSessionSummaryContext(ctx context.Context, sessionID string, input UpsertSessionSummaryInput) (SessionSummary, error)
	UpsertSessionSummaryContext(ctx context.Context, sessionID string, input UpsertSessionSummaryInput) (UpsertSessionSummaryResult, error)
	SaveSessionInterventionContext(ctx context.Context, sessionID string, input SaveSessionInterventionInput) (SessionIntervention, error)
	ListSessionInterventionsContext(ctx context.Context, sessionID string, status string) ([]SessionIntervention, error)
	DecideSessionInterventionContext(ctx context.Context, sessionID string, input DecideSessionInterventionInput) (DecideSessionInterventionResult, error)
	MarkSessionTurnWaitingApprovalContext(ctx context.Context, sessionID string, turnID string) error
	ResolveAgentRuntimeConfigContext(ctx context.Context, sessionID string) (AgentRuntimeConfig, error)
	ListConversationMessagesContext(ctx context.Context, sessionID string, beforeSeq int64) ([]ConversationMessage, error)
	AppendRuntimeEventContext(ctx context.Context, sessionID string, turnID string, input AppendEventInput) ([]Event, error)
	CompleteSessionTurnContext(ctx context.Context, sessionID string, turnID string, agentPayload json.RawMessage) ([]Event, error)
	FailSessionTurnContext(ctx context.Context, sessionID string, turnID string, reason string) ([]Event, error)
	SubscribeEventsContext(ctx context.Context, sessionID string, afterSeq int64) (<-chan Event, func(), error)
	GetSessionLLMUsageContext(ctx context.Context, sessionID string) (LLMUsageReport, error)
}

type ObjectArtifactContextStore interface {
	CreateObjectRefContext(ctx context.Context, input CreateObjectRefInput) (ObjectRef, error)
	GetObjectRefContext(ctx context.Context, id string) (ObjectRef, error)
	CountSessionArtifactsByObjectRefContext(ctx context.Context, objectRefID string) (int, error)
	DeleteObjectRefContext(ctx context.Context, id string) error
	CreateSessionArtifactContext(ctx context.Context, input CreateSessionArtifactInput) (SessionArtifact, error)
	GetSessionArtifactContext(ctx context.Context, sessionID string, artifactID string) (SessionArtifact, error)
	DeleteSessionArtifactContext(ctx context.Context, sessionID string, artifactID string) error
	ListSessionArtifactsContext(ctx context.Context, sessionID string) ([]SessionArtifact, error)
}

type Store interface {
	EnsureLLMProvider(input EnsureLLMProviderInput) (LLMProvider, error)
	UpsertLLMProvider(input UpsertLLMProviderInput) (LLMProvider, error)
	CreateLLMProvider(input UpsertLLMProviderInput) (LLMProvider, error)
	UpdateLLMProvider(input UpdateLLMProviderInput) (LLMProvider, error)
	GetLLMProvider(id string) (LLMProvider, error)
	ListLLMProviders() ([]LLMProvider, error)
	SetLLMProviderEnabled(id string, enabled bool) (LLMProvider, error)
	SetLLMProviderEnabledIfRevision(id string, enabled bool, expectedRevision int64) (LLMProvider, error)
	DeleteLLMProvider(id string) error
	DeleteLLMProviderIfRevision(id string, expectedRevision int64) error
	UpsertLLMModel(input UpsertLLMModelInput) (LLMModel, error)
	CreateLLMModel(input UpsertLLMModelInput) (LLMModel, error)
	UpdateLLMModel(input UpdateLLMModelInput) (LLMModel, error)
	ListLLMModels(providerID string) ([]LLMModel, error)
	DeleteLLMModel(providerID string, model string) error
	DeleteLLMModelIfRevision(providerID string, model string, expectedRevision int64) error
	EnsureAgent(input EnsureAgentInput) (Agent, error)
	CreateAgent(input CreateAgentInput) (Agent, error)
	GetAgent(id string) (Agent, error)
	GetAgentScoped(id string, scope AccessScope) (Agent, error)
	ListAgents() ([]Agent, error)
	ListAgentsScoped(scope AccessScope) ([]Agent, error)
	UpdateAgent(input UpdateAgentInput) (Agent, error)
	ListAgentConfigVersions(agentID string) ([]AgentConfigVersion, error)
	CreateAgentConfigVersion(input CreateAgentConfigVersionInput) (Agent, error)
	CreateEnvironment(input CreateEnvironmentInput) (Environment, error)
	CreateSession(input CreateSessionInput) (Session, error)
	CreateSubagentSession(input CreateSubagentSessionInput) (Session, error)
	StartSubagentTurn(input StartSubagentTurnInput) ([]Event, error)
	EnqueueSubagentStart(input EnqueueSubagentStartInput) (SubagentStartRequest, error)
	GetPendingSubagentStart(sessionID string) (SubagentStartRequest, error)
	CancelSubagentStart(input CancelSubagentStartInput) (SubagentStartRequest, error)
	CreateSubagentTaskGroup(input CreateSubagentTaskGroupInput) (SubagentTaskGroup, error)
	AppendSubagentTaskGroupItem(groupID string, input AppendSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error)
	UpdateSubagentTaskGroupItem(groupID string, itemIndex int, input UpdateSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error)
	GetSubagentTaskGroup(id string) (SubagentTaskGroup, error)
	ListSubagentTaskGroupsByParentSession(parentSessionID string) ([]SubagentTaskGroup, error)
	GetSubagentTaskGroupItemBySession(sessionID string) (SubagentTaskGroupItem, error)
	ListSubagentTaskGroupItems(groupID string) ([]SubagentTaskGroupItem, error)
	ListChildSubagentTaskGroups(parentGroupID string, parentItemIndex int) ([]SubagentTaskGroup, error)
	CancelSubagentTaskGroup(input CancelSubagentTaskGroupInput) (SubagentTaskGroup, error)
	ReactivateSubagentTaskGroup(input ReactivateSubagentTaskGroupInput) (SubagentTaskGroup, error)
	GetSubagentTaskGroupMetrics(input GetSubagentTaskGroupMetricsInput) (SubagentTaskGroupMetrics, error)
	GetSubagentMetrics(input GetSubagentMetricsInput) (SubagentMetrics, error)
	GetSession(id string) (Session, error)
	GetSessionScoped(id string, scope AccessScope) (Session, error)
	ListSessions(input ListSessionsInput) ([]Session, error)
	ListSessionsScoped(input ListSessionsInput, scope AccessScope) ([]Session, error)
	UpdateSessionRuntimeSettings(id string, input UpdateSessionRuntimeSettingsInput) (Session, error)
	UpdateSessionMetadata(id string, input UpdateSessionMetadataInput) (Session, error)
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
	RestoreSession(id string) (Session, error)
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
	GetObjectRefScoped(id string, scope AccessScope) (ObjectRef, error)
	CountSessionArtifactsByObjectRef(objectRefID string) (int, error)
	DeleteObjectRef(id string) error
	CreateSessionArtifact(input CreateSessionArtifactInput) (SessionArtifact, error)
	GetSessionArtifact(sessionID string, artifactID string) (SessionArtifact, error)
	DeleteSessionArtifact(sessionID string, artifactID string) error
	ListSessionArtifacts(sessionID string) ([]SessionArtifact, error)
	RegisterWorker(input RegisterWorkerInput) (Worker, error)
	GetWorker(id string) (Worker, error)
	GetWorkerScoped(id string, scope AccessScope) (Worker, error)
	ListWorkers(input ListWorkersInput) ([]Worker, error)
	ListWorkersScoped(input ListWorkersInput, scope AccessScope) ([]Worker, error)
	HeartbeatWorker(id string, input WorkerHeartbeatInput) (Worker, error)
	ArchiveWorker(id string) (Worker, error)
	ReapExpiredWorkers(input ReapExpiredWorkersInput) ([]Worker, error)
	EnqueueWorkerWork(input EnqueueWorkerWorkInput) (WorkerWork, error)
	GetWorkerWork(id string) (WorkerWork, error)
	GetWorkerWorkScoped(id string, scope AccessScope) (WorkerWork, error)
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

type SessionRunStore interface {
	StartSessionRunContext(ctx context.Context, sessionID string, input StartSessionRunInput) (StartSessionRunResult, error)
	GetSessionRunContext(ctx context.Context, sessionID string, runID string) (SessionRun, error)
	ListSessionRunsContext(ctx context.Context, sessionID string) ([]SessionRun, error)
	ListSessionRunEventsContext(ctx context.Context, sessionID string, runID string, afterSeq int64) ([]Event, error)
}

type SessionControlReader interface {
	ListSessionTurnControlEventsContext(ctx context.Context, sessionID string, turnID string, afterSeq int64) ([]Event, error)
}

type AgentLoopRepositoryFactory interface {
	AgentLoopRepository(AgentLoopFence) agentcore.StateRepository
}

// SessionTaskPlanReader keeps read-only API consumers independent from the
// runtime's task-plan mutation surface.
type SessionTaskPlanReader interface {
	GetCurrentSessionTaskPlanContext(ctx context.Context, sessionID string) (SessionTaskPlan, error)
	ListSessionTaskPlansContext(ctx context.Context, sessionID string) ([]SessionTaskPlan, error)
}

// SessionTaskPlanStore is deliberately separate from Store so task tracking can
// be adopted by runtimes without widening every existing Store implementation.
type SessionTaskPlanStore interface {
	SessionTaskPlanReader
	CreateSessionTaskPlanContext(ctx context.Context, sessionID string, input CreateSessionTaskPlanInput) (SessionTaskPlanResult, error)
	UpdateSessionTaskItemsContext(ctx context.Context, sessionID string, input UpdateSessionTaskItemsInput) (SessionTaskPlanResult, error)
	CompleteSessionTaskPlanContext(ctx context.Context, sessionID string, input FinishSessionTaskPlanInput) (SessionTaskPlanResult, error)
	CancelSessionTaskPlanContext(ctx context.Context, sessionID string, input FinishSessionTaskPlanInput) (SessionTaskPlanResult, error)
}

type SubagentStartQueueStore interface {
	PromoteSubagentStarts(input PromoteSubagentStartsInput) ([]SubagentStartPromotion, error)
}

type OrphanSubagentStore interface {
	ReapOrphanSubagents(input ReapOrphanSubagentsInput) ([]ReapedSubagent, error)
}

type OperatorAuditStore interface {
	RecordOperatorAudit(input RecordOperatorAuditInput) (OperatorAuditRecord, error)
	ListOperatorAudit(input ListOperatorAuditInput) ([]OperatorAuditRecord, error)
}

type SecurityAuditOutboxStore interface {
	RecordSecurityAuditOutbox(input RecordSecurityAuditOutboxInput) (SecurityAuditOutboxEvent, error)
	ClaimSecurityAuditOutbox(input ClaimSecurityAuditOutboxInput) ([]SecurityAuditOutboxEvent, error)
	CompleteSecurityAuditOutbox(input CompleteSecurityAuditOutboxInput) (int, error)
	FailSecurityAuditOutbox(input FailSecurityAuditOutboxInput) (int, error)
	ReplaySecurityAuditDeadLetters(input ReplaySecurityAuditDeadLettersInput) (int, error)
	GetSecurityAuditOutboxStats(now time.Time) (SecurityAuditOutboxStats, error)
	ListSecurityAuditIntegrityKeyStats() ([]SecurityAuditIntegrityKeyStats, error)
	PruneDeliveredSecurityAuditOutbox(before time.Time, limit int) (int, error)
}

type AgentDeliberationStore interface {
	CreateAgentDeliberation(input CreateAgentDeliberationInput) (AgentDeliberation, error)
	GetAgentDeliberation(id string) (AgentDeliberation, error)
	GetAgentDeliberationByIdempotency(parentSessionID string, idempotencyKey string) (AgentDeliberation, error)
	ListAgentDeliberationsByParentSession(parentSessionID string) ([]AgentDeliberation, error)
	UpdateAgentDeliberation(id string, input UpdateAgentDeliberationInput) (AgentDeliberation, error)
	ListAgentDeliberationParticipants(deliberationID string) ([]AgentDeliberationParticipant, error)
	CreateAgentDeliberationRound(round AgentDeliberationRound) (AgentDeliberationRound, error)
	GetAgentDeliberationRound(deliberationID string, roundNumber int) (AgentDeliberationRound, error)
	ListAgentDeliberationRounds(deliberationID string) ([]AgentDeliberationRound, error)
	UpdateAgentDeliberationRound(deliberationID string, roundNumber int, input UpdateAgentDeliberationRoundInput) (AgentDeliberationRound, error)
	UpsertAgentDeliberationContribution(contribution AgentDeliberationContribution) (AgentDeliberationContribution, error)
	ListAgentDeliberationContributions(deliberationID string, roundNumber int) ([]AgentDeliberationContribution, error)
}
