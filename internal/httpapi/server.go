package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillretention"
	"tiggy-manage-agent/internal/tools"
)

const serviceName = "tiggy-manage-agent"

type controlPrincipal struct {
	ID            string
	OperatorLabel string
	Role          string
}

type controlPrincipalContextKey struct{}

type Server struct {
	mux                *http.ServeMux
	store              managedagents.Store
	runner             runner.Runner
	logger             *slog.Logger
	defaultLLMProvider string
	defaultLLMModel    string
	objectStore        objectstore.Client
	executionResolver  execution.ProviderResolver
	workerAuthToken    string
	controlAuthToken   string
	authenticator      *identityAuthenticator
	webLogin           *oidcWebLogin
	authorizationAudit *authorizationAudit
	subagentPolicy     SubagentPolicy
	skillsToolService  tools.SkillsToolService
	skillRetention     *skillretention.Service
}

func NewServerWithStoreAndRunner(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreAndExecutionResolver(store, turnRunner, logger, "fake", "fake-demo", objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store))
}

func NewServerWithStoreRunnerAndLLMDefaults(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreAndExecutionResolver(store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store))
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreAndExecutionResolver(store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectStore, defaultExecutionResolver(store))
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStoreAndExecutionResolver(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client, executionResolver execution.ProviderResolver) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndWorkerAuth(store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectStore, executionResolver, "")
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndWorkerAuth(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client, executionResolver execution.ProviderResolver, workerAuthToken string) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectStore, executionResolver, workerAuthToken, "")
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAndAuth(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client, executionResolver execution.ProviderResolver, workerAuthToken string, controlAuthToken string) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAuthAndSubagentPolicy(store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectStore, executionResolver, workerAuthToken, controlAuthToken, defaultSubagentPolicy())
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAuthAndSubagentPolicy(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client, executionResolver execution.ProviderResolver, workerAuthToken string, controlAuthToken string, subagentPolicy SubagentPolicy) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAuthSubagentPolicyAndBinaryScanner(
		store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectStore, executionResolver,
		workerAuthToken, controlAuthToken, subagentPolicy, nil,
	)
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverAuthSubagentPolicyAndBinaryScanner(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client, executionResolver execution.ProviderResolver, workerAuthToken string, controlAuthToken string, subagentPolicy SubagentPolicy, binaryScanner skillmarketplace.BinaryScanner) http.Handler {
	return NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store, turnRunner, logger, defaultLLMProvider, defaultLLMModel, objectStore, executionResolver,
		workerAuthToken, controlAuthToken, AuthConfig{Mode: AuthModeDisabled}, subagentPolicy, binaryScanner,
	)
}

func NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string, objectStore objectstore.Client, executionResolver execution.ProviderResolver, workerAuthToken string, controlAuthToken string, authConfig AuthConfig, subagentPolicy SubagentPolicy, binaryScanner skillmarketplace.BinaryScanner) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if turnRunner == nil {
		panic("httpapi runner is required")
	}
	if objectStore == nil {
		objectStore = objectstore.NewNoopClient(objectstore.Config{})
	}
	if subagentPolicy == (SubagentPolicy{}) {
		subagentPolicy = defaultSubagentPolicy()
	}
	authConfig.LegacyControlToken = controlAuthToken
	authConfig.WorkerToken = workerAuthToken
	authenticator, err := newIdentityAuthenticator(authConfig)
	if err != nil {
		panic(fmt.Sprintf("invalid httpapi auth config: %v", err))
	}
	webLogin, err := newOIDCWebLogin(authConfig, authenticator)
	if err != nil {
		panic(fmt.Sprintf("invalid browser OIDC login config: %v", err))
	}
	server := &Server{
		mux:                http.NewServeMux(),
		store:              store,
		runner:             turnRunner,
		logger:             logger,
		defaultLLMProvider: defaultLLMProvider,
		defaultLLMModel:    defaultLLMModel,
		objectStore:        objectStore,
		executionResolver:  executionResolver,
		workerAuthToken:    strings.TrimSpace(workerAuthToken),
		controlAuthToken:   strings.TrimSpace(controlAuthToken),
		authenticator:      authenticator,
		webLogin:           webLogin,
		authorizationAudit: newAuthorizationAudit(authConfig.AuthorizationSink),
		subagentPolicy:     subagentPolicy,
	}
	tools.SetDefaultAgentToolService(newAgentToolService(store, turnRunner, logger, subagentPolicy))
	server.skillsToolService = newSkillsToolServiceWithDependenciesAndBinaryScanner(
		store,
		skillmarketplace.NewGitHubClient(os.Getenv("TMA_SKILLS_GITHUB_TOKEN")),
		skillMarketplacePolicyFromEnv(),
		objectStore,
		server.defaultObjectStoreBucket(),
		binaryScanner,
	)
	if retentionStore, ok := store.(skillretention.Store); ok {
		retentionService, retentionErr := skillretention.NewService(retentionStore, objectStore, skillAssetRetentionPolicyFromEnv())
		if retentionErr != nil {
			panic(fmt.Sprintf("invalid skill asset retention config: %v", retentionErr))
		}
		server.skillRetention = retentionService
	}
	tools.SetDefaultSkillsToolService(server.skillsToolService)
	server.routes()
	return server.v2EnvelopeMiddleware(server.identityMiddleware(server.mux))
}

func defaultExecutionResolver(store managedagents.Store) execution.ProviderResolver {
	return execution.SessionProviderResolver{Store: store}
}

func (s *Server) executionProviderForRequest(request execution.ProviderRequest) capability.Provider {
	if s != nil && s.executionResolver != nil {
		if provider := s.executionResolver.ResolveProvider(request); provider != nil {
			return provider
		}
	}
	if s != nil && s.store != nil {
		return execution.SessionProviderResolver{Store: s.store}.ResolveProvider(request)
	}
	return execution.SessionProviderResolver{}.ResolveProvider(request)
}

func (s *Server) routes() {
	s.registerV2Routes()
	s.mux.HandleFunc("GET /{$}", redirectUserApp)
	s.mux.HandleFunc("GET /health", healthHandler)
	s.mux.HandleFunc("GET /metrics", s.requireControlAuth(s.getMetrics))
	s.mux.HandleFunc("GET /app", s.getUserApp)
	s.mux.HandleFunc("GET /app/{$}", redirectUserApp)
	s.mux.Handle("GET /app/assets/", appAssetHandler())
	s.mux.HandleFunc("GET /inspector", s.getInspector)
	s.mux.Handle("GET /inspector/assets/", inspectorAssetHandler())
	s.mux.HandleFunc("GET /space", s.getSpace)
	s.mux.Handle("GET /space/assets/", spaceAssetHandler())
	s.mux.HandleFunc("GET /auth/login", s.startOIDCLogin)
	s.mux.HandleFunc("GET /auth/callback", s.finishOIDCLogin)
	s.mux.HandleFunc("POST /auth/refresh", s.refreshOIDCLogin)
	s.mux.HandleFunc("POST /auth/logout", s.logoutOIDCLogin)
	s.mux.HandleFunc("GET /v1/auth/me", s.getCurrentPrincipal)
	s.mux.HandleFunc("GET /v1/auth/config", s.getAuthClientConfiguration)
	s.mux.HandleFunc("GET /v1/agent/task-group-templates", s.listTaskGroupTemplates)
	s.mux.HandleFunc("GET /v1/task-templates", s.listWorkbenchTaskTemplates)
	s.mux.HandleFunc("GET /v1/agent/discussion-strategies", s.listAgentDiscussionStrategies)
	s.mux.HandleFunc("GET /v1/traces", s.listTraces)
	s.mux.HandleFunc("GET /v1/traces/{trace_id}", s.getTrace)
	s.mux.HandleFunc("GET /v1/traces/{trace_id}/spans/{span_id}", s.getTraceSpan)
	s.mux.HandleFunc("GET /v1/spans", s.listSpans)
	s.mux.HandleFunc("GET /v1/observability/status", s.requireControlAuth(s.getObservabilityStatus))
	s.mux.HandleFunc("POST /v1/observability/retry", s.requireControlAuth(s.retryObservabilityExporters))
	s.mux.HandleFunc("GET /v1/observability/security-audit/integrity-keys", s.requireControlAuth(s.getSecurityAuditIntegrityKeyStatus))
	s.mux.HandleFunc("POST /v1/observability/security-audit/replay", s.requireControlAuth(s.replaySecurityAuditDeadLetters))

	s.mux.HandleFunc("GET /v1/llm-providers", s.listLLMProviders)
	s.mux.HandleFunc("POST /v1/llm-providers", s.requireControlAuth(s.createLLMProvider))
	s.mux.HandleFunc("GET /v1/llm-providers/{provider_id}", s.getLLMProvider)
	s.mux.HandleFunc("PATCH /v1/llm-providers/{provider_id}", s.requireControlAuth(s.updateLLMProvider))
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/enable", s.requireControlAuth(s.enableLLMProvider))
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/disable", s.requireControlAuth(s.disableLLMProvider))
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/test", s.requireControlAuth(s.testLLMProvider))
	s.mux.HandleFunc("DELETE /v1/llm-providers/{provider_id}", s.requireAdminAuth(s.deleteLLMProvider))
	s.mux.HandleFunc("GET /v1/llm-models", s.listLLMModels)
	s.mux.HandleFunc("POST /v1/llm-models", s.requireControlAuth(s.upsertLLMModel))
	s.mux.HandleFunc("POST /v1/llm-models/{provider_id}/{model}/test", s.requireControlAuth(s.testLLMModel))
	s.mux.HandleFunc("DELETE /v1/llm-models/{provider_id}/{model...}", s.requireAdminAuth(s.deleteLLMModel))
	s.mux.HandleFunc("GET /v1/llm-usage", s.requireControlAuth(s.listLLMUsage))
	s.mux.HandleFunc("POST /v1/workers", s.requireWorkerAuth(s.registerWorker))
	s.mux.HandleFunc("GET /v1/workers", s.requireControlAuth(s.listWorkers))
	s.mux.HandleFunc("POST /v1/workers/diagnose", s.requireWorkerOrControlAuth(s.diagnoseWorkers))
	s.mux.HandleFunc("POST /v1/workers/reap-expired", s.requireControlAuth(s.reapExpiredWorkers))
	s.mux.HandleFunc("GET /v1/workers/{worker_id}", s.requireControlAuth(s.getWorker))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/heartbeat", s.requireWorkerAuth(s.heartbeatWorker))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/archive", s.requireWorkerOrControlAuth(s.archiveWorker))
	s.mux.HandleFunc("POST /v1/worker-work", s.requireControlAuth(s.enqueueWorkerWork))
	s.mux.HandleFunc("POST /v1/worker-work/reap-expired", s.requireControlAuth(s.reapExpiredWorkerWork))
	s.mux.HandleFunc("GET /v1/worker-work/{work_id}", s.requireControlAuth(s.getWorkerWork))
	s.mux.HandleFunc("GET /v1/worker-work/{work_id}/diagnose", s.requireControlAuth(s.diagnoseWorkerWork))
	s.mux.HandleFunc("POST /v1/worker-work/{work_id}/cancel", s.requireControlAuth(s.cancelWorkerWork))
	s.mux.HandleFunc("POST /v1/worker-work/{work_id}/requeue", s.requireControlAuth(s.requeueWorkerWork))
	s.mux.HandleFunc("GET /v1/workers/{worker_id}/work/poll", s.requireWorkerAuth(s.pollWorkerWork))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/work/{work_id}/ack", s.requireWorkerAuth(s.ackWorkerWork))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/work/{work_id}/heartbeat", s.requireWorkerAuth(s.heartbeatWorkerWork))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/work/{work_id}/result", s.requireWorkerAuth(s.completeWorkerWork))
	s.mux.HandleFunc("POST /v1/object-refs", s.createObjectRef)
	s.mux.HandleFunc("GET /v1/object-refs/{object_ref_id}", s.getObjectRef)
	s.mux.HandleFunc("GET /v1/object-refs/{object_ref_id}/download", s.downloadObjectRef)
	s.mux.HandleFunc("DELETE /v1/object-refs/{object_ref_id}", s.deleteObjectRef)
	s.mux.HandleFunc("POST /v1/skills/resolve-preview", s.requireControlAuth(s.resolveSkillsPreview))
	s.mux.HandleFunc("GET /v1/skills/marketplace/discover", s.requireControlAuth(s.discoverSkillsMarketplace))
	s.mux.HandleFunc("POST /v1/skills/marketplace/preview", s.requireControlAuth(s.previewSkillsMarketplace))
	s.mux.HandleFunc("POST /v1/skills/marketplace/install", s.requireControlAuth(s.installSkillsMarketplace))
	s.mux.HandleFunc("GET /v1/skills/marketplace/internal", s.browseInternalMarketplace)
	s.mux.HandleFunc("POST /v1/skills/marketplace/internal/preview", s.previewInternalMarketplace)
	s.mux.HandleFunc("POST /v1/skills/marketplace/internal/install", s.installInternalMarketplace)
	s.mux.HandleFunc("POST /v1/skills", s.createSkill)
	s.mux.HandleFunc("GET /v1/skills", s.listSkills)
	s.mux.HandleFunc("GET /v1/skills/{skill_id}", s.getSkill)
	s.mux.HandleFunc("POST /v1/skills/{skill_id}/versions", s.createSkillVersion)
	s.mux.HandleFunc("GET /v1/skills/{skill_id}/draft", s.getSkillDraft)
	s.mux.HandleFunc("PUT /v1/skills/{skill_id}/draft", s.putSkillDraft)
	s.mux.HandleFunc("POST /v1/skills/{skill_id}/draft/publish", s.publishSkillDraft)
	s.mux.HandleFunc("POST /v1/skills/{skill_id}/fork", s.forkSkill)
	s.mux.HandleFunc("GET /v1/skills/{skill_id}/versions", s.listSkillVersions)
	s.mux.HandleFunc("GET /v1/skills/{skill_id}/versions/{version}", s.getSkillVersion)
	s.mux.HandleFunc("GET /v1/skills/{skill_id}/versions/{version}/package", s.downloadSkillPackage)
	s.mux.HandleFunc("POST /v1/skills/{skill_id}/enable", s.requireControlAuth(s.enableInstalledSkill))
	s.mux.HandleFunc("POST /v1/skills/{skill_id}/disable", s.requireControlAuth(s.disableInstalledSkill))
	s.mux.HandleFunc("POST /v1/skills/{skill_id}/archive", s.archiveSkill)
	s.mux.HandleFunc("POST /v1/skill-packages/backfill", s.requireControlAuth(s.backfillSkillPackages))
	s.mux.HandleFunc("POST /v1/skill-marketplace-entries", s.requireControlAuth(s.createMarketplaceEntry))
	s.mux.HandleFunc("GET /v1/skill-marketplace-entries", s.requireControlAuth(s.listMarketplaceEntries))
	s.mux.HandleFunc("GET /v1/skill-marketplace-entries/{entry_id}", s.requireControlAuth(s.getMarketplaceEntry))
	s.mux.HandleFunc("PATCH /v1/skill-marketplace-entries/{entry_id}", s.requireControlAuth(s.updateMarketplaceEntry))
	s.mux.HandleFunc("POST /v1/skill-marketplace-entries/{entry_id}/submit", s.requireControlAuth(s.submitMarketplaceEntry))
	s.mux.HandleFunc("POST /v1/skill-marketplace-entries/{entry_id}/publish", s.requireAdminAuth(s.publishMarketplaceEntry))
	s.mux.HandleFunc("POST /v1/skill-marketplace-entries/{entry_id}/withdraw", s.requireAdminAuth(s.withdrawMarketplaceEntry))
	s.mux.HandleFunc("POST /v1/skill-marketplace-policies", s.requireControlAuth(s.createMarketplacePolicy))
	s.mux.HandleFunc("GET /v1/skill-marketplace-policies", s.requireControlAuth(s.listMarketplacePolicies))
	s.mux.HandleFunc("GET /v1/skill-marketplace-policies/{policy_id}", s.requireControlAuth(s.getMarketplacePolicy))
	s.mux.HandleFunc("POST /v1/skill-marketplace-policies/{policy_id}/versions", s.requireControlAuth(s.publishMarketplacePolicyVersion))
	s.mux.HandleFunc("GET /v1/skill-marketplace-policies/{policy_id}/versions/{version}", s.requireControlAuth(s.getMarketplacePolicyVersion))
	s.mux.HandleFunc("POST /v1/skill-marketplace-policies/{policy_id}/archive", s.requireControlAuth(s.archiveMarketplacePolicy))
	s.mux.HandleFunc("GET /v1/skill-asset-retention/effective", s.requireControlAuth(s.getEffectiveSkillAssetRetentionPolicy))
	s.mux.HandleFunc("POST /v1/skill-asset-retention/policies", s.requireControlAuth(s.createSkillAssetRetentionPolicy))
	s.mux.HandleFunc("GET /v1/skill-asset-retention/policies", s.requireControlAuth(s.listSkillAssetRetentionPolicies))
	s.mux.HandleFunc("GET /v1/skill-asset-retention/policies/{policy_id}", s.requireControlAuth(s.getSkillAssetRetentionPolicy))
	s.mux.HandleFunc("POST /v1/skill-asset-retention/policies/{policy_id}/versions", s.requireControlAuth(s.publishSkillAssetRetentionPolicyVersion))
	s.mux.HandleFunc("GET /v1/skill-asset-retention/policies/{policy_id}/versions/{version}", s.requireControlAuth(s.getSkillAssetRetentionPolicyVersion))
	s.mux.HandleFunc("POST /v1/skill-asset-retention/policies/{policy_id}/archive", s.requireControlAuth(s.archiveSkillAssetRetentionPolicy))
	s.mux.HandleFunc("POST /v1/skill-asset-gc/preview", s.requireControlAuth(s.previewSkillAssetGC))
	s.mux.HandleFunc("POST /v1/skill-asset-gc/run", s.requireControlAuth(s.runSkillAssetGC))
	s.mux.HandleFunc("GET /v1/skill-asset-gc/runs", s.requireControlAuth(s.listSkillAssetGCRuns))
	s.mux.HandleFunc("GET /v1/skill-asset-gc/runs/{run_id}", s.requireControlAuth(s.getSkillAssetGCRun))
	s.mux.HandleFunc("GET /v1/skill-asset-gc/tombstones", s.requireControlAuth(s.listSkillAssetGCTombstones))

	s.mux.HandleFunc("POST /v1/agents", s.createAgent)
	s.mux.HandleFunc("POST /v1/agents/import", s.importAgent)
	s.mux.HandleFunc("GET /v1/agents/default", s.getDefaultAgent)
	s.mux.HandleFunc("GET /v1/agents", s.listAgents)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}", s.getAgent)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}/export", s.exportAgent)
	s.mux.HandleFunc("PATCH /v1/agents/{agent_id}", s.updateAgent)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}/config-versions", s.listAgentConfigVersions)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/config-versions", s.createAgentConfigVersion)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/config-versions/{version}/rollback", s.rollbackAgentConfigVersion)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/tooling-health", s.requireControlAuth(s.checkAgentToolingHealth))
	s.mux.HandleFunc("GET /v1/workspaces/{workspace_id}/tool-permissions", s.getWorkspaceToolPermissions)
	s.mux.HandleFunc("PUT /v1/workspaces/{workspace_id}/tool-permissions", s.requireControlAuth(s.updateWorkspaceToolPermissions))
	s.mux.HandleFunc("POST /v1/workspaces/{workspace_id}/tool-permissions/evaluate", s.evaluateWorkspaceToolPermission)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/schedules", s.createAgentSchedule)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}/schedules", s.listAgentSchedules)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}/schedules/{schedule_id}", s.getAgentSchedule)
	s.mux.HandleFunc("PATCH /v1/agents/{agent_id}/schedules/{schedule_id}", s.updateAgentSchedule)
	s.mux.HandleFunc("DELETE /v1/agents/{agent_id}/schedules/{schedule_id}", s.deleteAgentSchedule)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/schedules/{schedule_id}/run", s.runAgentScheduleNow)
	s.mux.HandleFunc("GET /v1/mcp-servers", s.listMCPRegistryServers)
	s.mux.HandleFunc("GET /v1/mcp-servers/runtime-status", s.listMCPRegistryRuntimeStatus)
	s.mux.HandleFunc("POST /v1/mcp-servers", s.requireControlAuth(s.createMCPRegistryServer))
	s.mux.HandleFunc("GET /v1/mcp-servers/{server_id}", s.getMCPRegistryServer)
	s.mux.HandleFunc("PATCH /v1/mcp-servers/{server_id}", s.requireControlAuth(s.updateMCPRegistryServer))
	s.mux.HandleFunc("DELETE /v1/mcp-servers/{server_id}", s.requireControlAuth(s.deleteMCPRegistryServer))
	s.mux.HandleFunc("POST /v1/mcp-servers/{server_id}/enable", s.requireControlAuth(s.enableMCPRegistryServer))
	s.mux.HandleFunc("POST /v1/mcp-servers/{server_id}/disable", s.requireControlAuth(s.disableMCPRegistryServer))
	s.mux.HandleFunc("POST /v1/mcp-servers/{server_id}/test", s.requireControlAuth(s.testMCPRegistryServer))
	s.mux.HandleFunc("GET /v1/mcp-servers/{server_id}/versions", s.listMCPRegistryVersions)
	s.mux.HandleFunc("POST /v1/mcp-servers/{server_id}/versions/{version}/restore", s.requireControlAuth(s.restoreMCPRegistryVersion))
	s.mux.HandleFunc("POST /v1/environments", s.createEnvironment)
	s.mux.HandleFunc("GET /v1/environments", s.listEnvironments)
	s.mux.HandleFunc("GET /v1/environments/{environment_id}", s.getEnvironment)
	s.mux.HandleFunc("GET /v1/environment-variables", s.listEnvironmentVariables)
	s.mux.HandleFunc("PUT /v1/environment-variables/{name}", s.putEnvironmentVariable)
	s.mux.HandleFunc("DELETE /v1/environment-variables/{name}", s.deleteEnvironmentVariable)
	s.mux.HandleFunc("POST /v1/sessions", s.createSession)
	s.mux.HandleFunc("GET /v1/sessions", s.listSessions)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}", s.getSession)
	s.mux.HandleFunc("PATCH /v1/sessions/{session_id}", s.updateSessionMetadata)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/runtime-config", s.getSessionRuntimeConfig)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/runtime-capabilities", s.getSessionRuntimeCapabilities)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/config/upgrade", s.upgradeSessionAgentConfig)
	s.mux.HandleFunc("PATCH /v1/sessions/{session_id}/runtime-settings", s.updateSessionRuntimeSettings)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/interventions", s.listSessionInterventions)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/approve", s.approveSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/reject", s.rejectSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/respond", s.respondSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/skip", s.skipSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/cancel", s.cancelSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/archive", s.archiveSession)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/restore", s.restoreSession)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/rerun", s.rerunSession)
	s.mux.HandleFunc("DELETE /v1/sessions/{session_id}", s.deleteSession)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/summary", s.getSessionSummary)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/task-plan", s.getSessionTaskPlan)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/task-plans", s.listSessionTaskPlans)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/trace", s.getSessionTrace)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/task-groups", s.listSessionTaskGroups)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/task-group-tree", s.getSessionTaskGroupTree)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/task-groups/{group_id}", s.getSessionTaskGroup)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/deliberations", s.listSessionDeliberations)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/deliberations/{deliberation_id}", s.getSessionDeliberation)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/deliberations/{deliberation_id}/cancel", s.requireControlAuth(s.cancelSessionDeliberation))
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/deliberations/{deliberation_id}/participants/{participant_index}/retry", s.requireControlAuth(s.retrySessionDeliberationParticipant))
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/task-groups/{group_id}/cancel", s.requireControlAuth(s.cancelSessionTaskGroup))
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/task-groups/{group_id}/retry", s.requireControlAuth(s.retrySessionTaskGroup))
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/task-groups/{group_id}/items/{item_index}/retry", s.requireControlAuth(s.retrySessionTaskGroupItem))
	s.mux.HandleFunc("POST /v1/subagents/reap-orphans", s.requireControlAuth(s.reapOrphanSubagents))
	s.mux.HandleFunc("GET /v1/operator-audit", s.requireControlAuth(s.listOperatorAudit))
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/operator-audit", s.requireControlAuth(s.listSessionOperatorAudit))
	s.mux.HandleFunc("PUT /v1/sessions/{session_id}/summary", s.upsertSessionSummary)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/usage", s.getSessionLLMUsage)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/skill-usages", s.getSessionSkillUsages)
	s.mux.HandleFunc("GET /v1/session-comparisons", s.compareSessions)
	s.mux.HandleFunc("GET /v1/run-comparisons", s.compareRuns)
	s.mux.HandleFunc("POST /v1/evaluation-rubrics", s.createEvaluationRubric)
	s.mux.HandleFunc("GET /v1/evaluation-rubrics", s.listEvaluationRubrics)
	s.mux.HandleFunc("GET /v1/evaluation-rubrics/{rubric_id}", s.getEvaluationRubric)
	s.mux.HandleFunc("POST /v1/run-evaluations", s.createRunEvaluation)
	s.mux.HandleFunc("POST /v1/run-evaluations/auto", s.autoEvaluateRun)
	s.mux.HandleFunc("GET /v1/run-evaluations", s.listRunEvaluations)
	s.mux.HandleFunc("POST /v1/evaluation-datasets", s.createEvaluationDataset)
	s.mux.HandleFunc("GET /v1/evaluation-datasets", s.listEvaluationDatasets)
	s.mux.HandleFunc("GET /v1/evaluation-datasets/{dataset_id}", s.getEvaluationDataset)
	s.mux.HandleFunc("POST /v1/evaluation-experiments", s.createEvaluationExperiment)
	s.mux.HandleFunc("GET /v1/evaluation-experiments", s.listEvaluationExperiments)
	s.mux.HandleFunc("GET /v1/evaluation-experiments/{experiment_id}", s.getEvaluationExperiment)
	s.mux.HandleFunc("POST /v1/evaluation-experiments/{experiment_id}/reconcile", s.reconcileEvaluationExperiment)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/artifacts", s.createSessionArtifact)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/artifacts", s.listSessionArtifacts)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/artifacts/{artifact_id}/download", s.downloadSessionArtifact)
	s.mux.HandleFunc("DELETE /v1/sessions/{session_id}/artifacts/{artifact_id}", s.deleteSessionArtifact)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/artifacts/upload", s.uploadSessionArtifact)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/artifacts/{artifact_id}/achievement-library", s.createAchievementLibraryItem)
	s.mux.HandleFunc("GET /v1/achievement-library", s.listAchievementLibraryItems)
	s.mux.HandleFunc("PATCH /v1/achievement-library/{item_id}", s.updateAchievementLibraryItem)
	s.mux.HandleFunc("DELETE /v1/achievement-library/{item_id}", s.deleteAchievementLibraryItem)
	s.mux.HandleFunc("GET /v1/achievement-library/{item_id}/download", s.downloadAchievementLibraryItem)
	s.mux.HandleFunc("POST /v1/achievement-library/{item_id}/reference", s.referenceAchievementLibraryItem)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/events", s.appendSessionEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events", s.listSessionEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/tool-permission-audit", s.listSessionToolPermissionAudit)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events/stream", s.streamSessionEvents)
}

func (s *Server) startOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.webLogin == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "browser OIDC login is not enabled"})
		return
	}
	s.webLogin.login(w, r)
}

func (s *Server) finishOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.webLogin == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "browser OIDC login is not enabled"})
		return
	}
	s.webLogin.callback(w, r)
}

func (s *Server) refreshOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.webLogin == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "browser OIDC login is not enabled"})
		return
	}
	s.webLogin.refresh(w, r)
}

func (s *Server) logoutOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.webLogin == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "browser OIDC login is not enabled"})
		return
	}
	s.webLogin.logout(w, r)
}

func redirectUserApp(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/app", http.StatusTemporaryRedirect)
}

func (s *Server) requireWorkerAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.requireBearerAuth(next, s.workerAuthToken, "tma-worker", "worker authorization required")
}

func (s *Server) requireControlAuth(next http.HandlerFunc) http.HandlerFunc {
	withPrincipal := func(w http.ResponseWriter, r *http.Request) {
		principal := s.controlPrincipal(r)
		ctx := context.WithValue(r.Context(), controlPrincipalContextKey{}, principal)
		next(w, r.WithContext(ctx))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if principal, ok := PrincipalFromRequest(r); ok {
			if !principal.HasRole(RoleOperator) {
				s.auditAuthorizationDecision(r, principal, "denied", "control_role_required", RoleOperator, nil)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator role required"})
				return
			}
			s.auditAuthorizationDecision(r, principal, "allowed", "control_role", RoleOperator, nil)
			withPrincipal(w, r)
			return
		}
		s.requireBearerAuth(withPrincipal, s.controlAuthToken, "tma-control", "control authorization required")(w, r)
	}
}

func (s *Server) requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	withPrincipal := func(w http.ResponseWriter, r *http.Request) {
		principal := s.controlPrincipal(r)
		ctx := context.WithValue(r.Context(), controlPrincipalContextKey{}, principal)
		next(w, r.WithContext(ctx))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if principal, ok := PrincipalFromRequest(r); ok {
			if !principal.HasRole(RoleAdmin) {
				s.auditAuthorizationDecision(r, principal, "denied", "admin_role_required", RoleAdmin, nil)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
				return
			}
			s.auditAuthorizationDecision(r, principal, "allowed", "admin_role", RoleAdmin, nil)
			withPrincipal(w, r)
			return
		}
		s.requireBearerAuth(withPrincipal, s.controlAuthToken, "tma-admin", "admin authorization required")(w, r)
	}
}

func (s *Server) requireWorkerOrControlAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if principal, ok := PrincipalFromRequest(r); ok {
			if !principal.HasRole(RoleOperator) {
				s.auditAuthorizationDecision(r, principal, "denied", "worker_or_control_role_required", RoleOperator, nil)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator role required"})
				return
			}
			s.auditAuthorizationDecision(r, principal, "allowed", "worker_or_control_role", RoleOperator, nil)
			next(w, r)
			return
		}
		if s == nil || (s.workerAuthToken == "" && s.controlAuthToken == "") {
			next(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		if bearerTokenMatches(header, s.workerAuthToken) || bearerTokenMatches(header, s.controlAuthToken) {
			next(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="tma-worker-or-control"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "worker or control authorization required"})
	}
}

func (s *Server) requireBearerAuth(next http.HandlerFunc, token string, realm string, errorMessage string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil || token == "" {
			next(w, r)
			return
		}
		if !bearerTokenMatches(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s"`, realm))
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": errorMessage})
			return
		}
		next(w, r)
	}
}

func bearerTokenMatches(header string, expected string) bool {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	token = strings.TrimSpace(token)
	expected = strings.TrimSpace(expected)
	if token == "" || expected == "" || len(token) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (s *Server) controlPrincipal(r *http.Request) controlPrincipal {
	if principal, ok := PrincipalFromRequest(r); ok {
		operatorLabel := strings.TrimSpace(r.Header.Get("X-TMA-Operator"))
		if operatorLabel == "" {
			operatorLabel = principal.Subject
		}
		if len(operatorLabel) > 128 {
			operatorLabel = operatorLabel[:128]
		}
		return controlPrincipal{ID: principal.Subject, OperatorLabel: operatorLabel, Role: highestPrincipalRole(principal)}
	}
	principalID := "control:open"
	if token := strings.TrimSpace(s.controlAuthToken); token != "" {
		digest := sha256.Sum256([]byte(token))
		principalID = "control:" + hex.EncodeToString(digest[:6])
	}
	operatorLabel := strings.TrimSpace(r.Header.Get("X-TMA-Operator"))
	if len(operatorLabel) > 128 {
		operatorLabel = operatorLabel[:128]
	}
	return controlPrincipal{ID: principalID, OperatorLabel: operatorLabel, Role: "admin"}
}

func highestPrincipalRole(principal Principal) string {
	for _, role := range []string{RoleAdmin, RoleOperator, RoleMember, RoleViewer} {
		if principal.HasRole(role) {
			return role
		}
	}
	return RoleViewer
}

func controlPrincipalFromRequest(r *http.Request) controlPrincipal {
	if r != nil {
		if principal, ok := r.Context().Value(controlPrincipalContextKey{}).(controlPrincipal); ok {
			return principal
		}
	}
	return controlPrincipal{ID: "control:unknown", Role: "admin"}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": serviceName,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func nonNilSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := http.StatusText(status)

	switch {
	case errors.Is(err, managedagents.ErrInvalid):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, managedagents.ErrForbidden):
		status = http.StatusForbidden
		message = err.Error()
	case errors.Is(err, managedagents.ErrRevisionConflict):
		status = http.StatusPreconditionFailed
		message = err.Error()
	case errors.Is(err, managedagents.ErrSessionBusy):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, managedagents.ErrConflict):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, managedagents.ErrNotFound):
		status = http.StatusNotFound
		message = err.Error()
	case errors.Is(err, managedagents.ErrTerminated):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, objectstore.ErrNotConfigured):
		status = http.StatusServiceUnavailable
		message = err.Error()
	case errors.Is(err, skillretention.ErrInvalid):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, skillretention.ErrDisabled):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, skillretention.ErrConflict):
		status = http.StatusConflict
		message = err.Error()
	}

	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
