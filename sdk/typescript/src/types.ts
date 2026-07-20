import type { components } from "./internal/generated/schema.js";

export type Schema = components["schemas"];
export type AuthClientConfiguration = Schema["AuthClientConfiguration"];
export type AuthState = Schema["AuthState"];
export type Agent = Schema["Agent"];
export type AgentConfigVersion = Schema["AgentConfigVersion"];
export type CreateAgentRequest = Schema["CreateAgentRequest"];
export type UpdateAgentRequest = Schema["UpdateAgentRequest"];
export type AgentImportRequest = Schema["AgentImportRequest"];
export type AgentExportDocument = Schema["AgentExportDocument"];
export type AgentConfigRollbackResponse = Schema["AgentConfigRollbackResponse"];
export type ToolingHealthRequest = Schema["ToolingHealthRequest"];
export type ToolingHealthResponse = Schema["ToolingHealthResponse"];
export type Environment = Schema["Environment"];
export type CreateEnvironmentRequest = Schema["CreateEnvironmentRequest"];
export type Session = Schema["Session"];
export type CreateSessionRequest = Schema["CreateSessionRequest"];
export type UpdateSessionMetadataRequest = Schema["UpdateSessionMetadataRequest"];
export type UpdateSessionRuntimeSettingsRequest = Schema["UpdateSessionRuntimeSettingsRequest"];
export type CompletionGateRuntimeSettings = Schema["CompletionGateRuntimeSettings"];
export type RerunSessionRequest = Schema["RerunSessionRequest"];
export type RerunSessionResponse = Schema["RerunSessionResponse"];
export type UpgradeSessionConfigRequest = Schema["UpgradeSessionConfigRequest"];
export type UpgradeSessionConfigResult = Schema["UpgradeSessionConfigResult"];
export type SessionComparison = Schema["SessionComparison"];
export type AgentRuntimeConfig = Schema["AgentRuntimeConfig"];
export type SessionRuntimeCapabilities = Schema["SessionRuntimeCapabilities"];
export type SessionSummary = Schema["SessionSummary"];
export type SessionTaskItem = Schema["SessionTaskItem"];
export type SessionTaskPlan = Schema["SessionTaskPlan"];
export type SessionUsage = Schema["SessionUsage"];
export type GeneratedRun = Schema["Run"];
export type Run = Omit<GeneratedRun, "status"> & { status: string };
export type StartRunRequest = Schema["StartRunRequest"];
export type Event = Schema["Event"];
export type LiveEvent = Schema["LiveEvent"];
export type AppendEvent = Schema["AppendEvent"];
export type AppendEventsRequest = Schema["AppendEventsRequest"];
export type AppendEventsResult = Schema["AppendEventsResult"];
export type Intervention = Schema["Intervention"];
export type InterventionDecision = Schema["InterventionDecision"];
export type Artifact = Schema["Artifact"];
export type ArtifactUpload = Schema["ArtifactUpload"];
export type CreateArtifactRequest = Schema["CreateArtifactRequest"];
export type Trace = Schema["TurnTrace"];
export type TraceCatalogEntry = Schema["TraceCatalogEntry"];
export type TraceSpanCatalogEntry = Schema["TraceSpanCatalogEntry"];
export type TraceSpanDetail = Schema["TraceSpanDetail"];
export type AgentTaskGroupTemplateList = Schema["AgentTaskGroupTemplateList"];
export type AgentDiscussionStrategyList = Schema["AgentDiscussionStrategyList"];
export type AgentDeliberationResponse = Schema["AgentDeliberationResponse"];
export type CancelAgentDeliberationRequest = Schema["CancelAgentDeliberationRequest"];
export type RetryAgentDeliberationParticipantRequest = Schema["RetryAgentDeliberationParticipantRequest"];
export type InspectorTaskGroupState = Schema["InspectorTaskGroupState"];
export type SessionTaskGroupTree = Schema["SessionTaskGroupTree"];
export type CancelTaskGroupRequest = Schema["CancelTaskGroupRequest"];
export type AgentTaskGroupResponse = Schema["AgentTaskGroupResponse"];
export type ReapOrphanSubagentsRequest = Schema["ReapOrphanSubagentsRequest"];
export type ReapOrphanSubagentsResult = Schema["ReapOrphanSubagentsResult"];
export type LLMProvider = Schema["LLMProvider"];
export type CreateLLMProviderRequest = Schema["CreateLLMProviderRequest"];
export type UpdateLLMProviderRequest = Schema["UpdateLLMProviderRequest"];
export type LLMModel = Schema["LLMModel"];
export type PutLLMModelRequest = Schema["PutLLMModelRequest"];
export type LLMDiagnosticResult = Schema["LLMDiagnosticResult"];
export type LLMUsageAggregateReport = Schema["LLMUsageAggregateReport"];
export type ObjectRef = Schema["ObjectRef"];
export type CreateObjectRefRequest = Schema["CreateObjectRefRequest"];
export type Worker = Schema["Worker"];
export type ReapExpiredWorkersRequest = Schema["ReapExpiredWorkersRequest"];
export type ReapExpiredWorkersResult = Schema["ReapExpiredWorkersResult"];
export type WorkerDiagnoseRequest = Schema["WorkerDiagnoseRequest"];
export type WorkerDiagnoseResponse = Schema["WorkerDiagnoseResponse"];
export type WorkerWork = Schema["WorkerWork"];
export type EnqueueWorkerWorkRequest = Schema["EnqueueWorkerWorkRequest"];
export type CancelWorkerWorkRequest = Schema["CancelWorkerWorkRequest"];
export type RequeueWorkerWorkRequest = Schema["RequeueWorkerWorkRequest"];
export type ReapExpiredWorkerWorkRequest = Schema["ReapExpiredWorkerWorkRequest"];
export type ReapExpiredWorkerWorkResult = Schema["ReapExpiredWorkerWorkResult"];
export type WorkerWorkDiagnosis = Schema["WorkerWorkDiagnosis"];
export type MCPServer = Schema["MCPServer"];
export type MCPServerConfig = Schema["MCPServerConfig"];
export type CreateMCPServerRequest = Schema["CreateMCPServerRequest"];
export type UpdateMCPServerRequest = Schema["UpdateMCPServerRequest"];
export type MCPServerVersion = Schema["MCPServerVersion"];
export type MCPRestoreResult = Schema["MCPRestoreResult"];
export type MCPRuntimeStatus = Schema["MCPRuntimeStatus"];
export type MCPServerTestResult = Schema["MCPServerTestResult"];
export type ObservabilityStatus = Schema["ObservabilityStatus"];
export type ObservabilityRetryResult = Schema["ObservabilityRetryResult"];
export type SecurityAuditIntegrityKeyStatus = Schema["SecurityAuditIntegrityKeyStatus"];
export type SecurityAuditReplayResult = Schema["SecurityAuditReplayResult"];
export type OperatorAuditRecord = Schema["OperatorAuditRecord"];
export type EnvironmentVariable = Schema["EnvironmentVariable"];
export type PutEnvironmentVariableRequest = Schema["PutEnvironmentVariableRequest"];
export type Skill = Schema["Skill"];
export type CreateSkillRequest = Schema["CreateSkillRequest"];
export type SkillVersion = Schema["SkillVersion"];
export type CreateSkillVersionRequest = Schema["CreateSkillVersionRequest"];
export type ResolveSkillsPreviewRequest = Schema["ResolveSkillsPreviewRequest"];
export type ResolveSkillsResult = Schema["ResolveSkillsResult"];
export type SkillUsage = Schema["SkillUsage"];
export type SkillPackageBackfillRequest = Schema["SkillPackageBackfillRequest"];
export type SkillPackageBackfillResult = Schema["SkillPackageBackfillResult"];
export type EffectiveSkillRetentionPolicy = Schema["EffectiveSkillRetentionPolicy"];
export type CreateSkillRetentionPolicyRequest = Schema["CreateSkillRetentionPolicyRequest"];
export type PublishSkillRetentionPolicyRequest = Schema["PublishSkillRetentionPolicyRequest"];
export type SkillRetentionPolicy = Schema["SkillRetentionPolicy"];
export type SkillRetentionPolicyVersion = Schema["SkillRetentionPolicyVersion"];
export type SkillRetentionPolicyResult = Schema["SkillRetentionPolicyResult"];
export type SkillAssetGCRequest = Schema["SkillAssetGCRequest"];
export type SkillAssetGCPreview = Schema["SkillAssetGCPreview"];
export type SkillAssetGCRun = Schema["SkillAssetGCRun"];
export type SkillAssetGCRunResult = Schema["SkillAssetGCRunResult"];
export type SkillAssetGCTombstone = Schema["SkillAssetGCTombstone"];
export type MarketplaceSource = Schema["MarketplaceSource"];
export type MarketplaceDiscoverResult = Schema["MarketplaceDiscoverResult"];
export type MarketplacePreviewRequest = Schema["MarketplacePreviewRequest"];
export type MarketplacePreviewResult = Schema["MarketplacePreviewResult"];
export type MarketplaceInstallRequest = Schema["MarketplaceInstallRequest"];
export type MarketplaceInstallResult = Schema["MarketplaceInstallResult"];
export type MarketplaceInternalResult = Schema["MarketplaceInternalResult"];
export type MarketplaceEnableRequest = Schema["MarketplaceEnableRequest"];
export type MarketplaceEnableResult = Schema["MarketplaceEnableResult"];
export type MarketplaceDisableRequest = Schema["MarketplaceDisableRequest"];
export type MarketplaceDisableResult = Schema["MarketplaceDisableResult"];
export type MarketplaceEntry = Schema["MarketplaceEntry"];
export type CreateMarketplaceEntryRequest = Schema["CreateMarketplaceEntryRequest"];
export type UpdateMarketplaceEntryRequest = Schema["UpdateMarketplaceEntryRequest"];
export type MarketplaceTransitionRequest = Schema["MarketplaceTransitionRequest"];
export type MarketplacePolicy = Schema["MarketplacePolicy"];
export type MarketplacePolicyResult = Schema["MarketplacePolicyResult"];
export type MarketplacePolicyVersion = Schema["MarketplacePolicyVersion"];
export type CreateMarketplacePolicyRequest = Schema["CreateMarketplacePolicyRequest"];
export type PublishMarketplacePolicyRequest = Schema["PublishMarketplacePolicyRequest"];

export interface TraceListQuery {
  workspaceId?: string;
  sessionId?: string;
  turnId?: string;
  sessionStatus?: string;
  includeArchived?: boolean;
  limit?: number;
  cursor?: string;
}

export interface TraceSpanListQuery {
  workspaceId?: string;
  traceId?: string;
  sessionId?: string;
  turnId?: string;
  kind?: string;
  status?: string;
  search?: string;
  critical?: boolean;
  minDurationMs?: number;
  maxDurationMs?: number;
  minSelfDurationMs?: number;
  includeArchived?: boolean;
  limit?: number;
  cursor?: string;
}

export interface Page<T> {
  items: T[];
  next_cursor: string;
  has_more: boolean;
}

export interface UploadFile {
  body: Blob;
  filename: string;
  contentType?: string;
}

export interface LLMUsageQuery {
  workspaceId?: string;
  providerId?: string;
  model?: string;
  status?: string;
  groupBy?: string;
  from?: Date | string;
  to?: Date | string;
}

export interface WorkerListQuery {
  workspaceId?: string;
  status?: string;
}

export interface MCPServerQuery { workspaceId?: string }

export interface OperatorAuditQuery {
  workspaceId?: string;
  sessionId?: string;
  principalId?: string;
  action?: string;
  limit?: number;
}

export interface EnvironmentVariableQuery { workspaceId?: string }

export interface SkillListQuery { workspaceId?: string; includeArchived?: boolean }
export interface SkillRetentionPolicyQuery { organizationId?: string; workspaceId?: string; includeArchived?: boolean }
export interface SkillAssetGCListQuery { workspaceId?: string; limit?: number }

export interface MarketplaceDiscoverQuery { sessionId: string; query?: string; repository?: string; limit?: number }
export interface MarketplaceInternalQuery { sessionId: string; query?: string; category?: string; tags?: string[]; limit?: number }
export interface MarketplaceEntryQuery { workspaceId?: string; status?: string; includeWithdrawn?: boolean }
export interface MarketplacePolicyQuery { organizationId?: string; workspaceId?: string; includeArchived?: boolean }

export interface RunResult {
  run: Run;
  lastEvent?: Event;
  output?: unknown;
}
