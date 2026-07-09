package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
)

const serviceName = "tiggy-manage-agent"

type Server struct {
	mux                *http.ServeMux
	store              managedagents.Store
	runner             runner.Runner
	logger             *slog.Logger
	defaultLLMProvider string
	defaultLLMModel    string
	objectStore        objectstore.Client
	continuationClient llm.Client
	executionResolver  execution.ProviderResolver
	workerAuthToken    string
	controlAuthToken   string
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
	if logger == nil {
		logger = slog.Default()
	}
	if turnRunner == nil {
		panic("httpapi runner is required")
	}
	if objectStore == nil {
		objectStore = objectstore.NewNoopClient(objectstore.Config{})
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
	}
	server.routes()
	return server.mux
}

func defaultExecutionResolver(store managedagents.Store) execution.ProviderResolver {
	return execution.SessionProviderResolver{Store: store}
}

func (s *Server) executionProvider(sessionID string) capability.Provider {
	request := execution.ProviderRequest{SessionID: sessionID}
	if s != nil && s.store != nil && sessionID != "" {
		if session, err := s.store.GetSession(sessionID); err == nil {
			request.WorkspaceID = session.WorkspaceID
			request.EnvironmentID = session.EnvironmentID
		}
	}
	return s.executionProviderForRequest(request)
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
	s.mux.HandleFunc("GET /health", healthHandler)
	s.mux.HandleFunc("GET /metrics", s.getMetrics)
	s.mux.HandleFunc("GET /inspector", s.getInspector)
	s.mux.HandleFunc("GET /v1/observability/status", s.getObservabilityStatus)

	s.mux.HandleFunc("GET /v1/llm-providers", s.listLLMProviders)
	s.mux.HandleFunc("POST /v1/llm-providers", s.createLLMProvider)
	s.mux.HandleFunc("GET /v1/llm-providers/{provider_id}", s.getLLMProvider)
	s.mux.HandleFunc("PATCH /v1/llm-providers/{provider_id}", s.updateLLMProvider)
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/enable", s.enableLLMProvider)
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/disable", s.disableLLMProvider)
	s.mux.HandleFunc("GET /v1/llm-models", s.listLLMModels)
	s.mux.HandleFunc("POST /v1/llm-models", s.upsertLLMModel)
	s.mux.HandleFunc("GET /v1/llm-usage", s.listLLMUsage)
	s.mux.HandleFunc("POST /v1/workers", s.requireWorkerAuth(s.registerWorker))
	s.mux.HandleFunc("GET /v1/workers", s.listWorkers)
	s.mux.HandleFunc("POST /v1/workers/diagnose", s.diagnoseWorkers)
	s.mux.HandleFunc("GET /v1/workers/{worker_id}", s.getWorker)
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/heartbeat", s.requireWorkerAuth(s.heartbeatWorker))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/archive", s.archiveWorker)
	s.mux.HandleFunc("POST /v1/worker-work", s.requireControlAuth(s.enqueueWorkerWork))
	s.mux.HandleFunc("POST /v1/worker-work/reap-expired", s.requireControlAuth(s.reapExpiredWorkerWork))
	s.mux.HandleFunc("GET /v1/worker-work/{work_id}", s.requireControlAuth(s.getWorkerWork))
	s.mux.HandleFunc("GET /v1/worker-work/{work_id}/diagnose", s.requireControlAuth(s.diagnoseWorkerWork))
	s.mux.HandleFunc("GET /v1/workers/{worker_id}/work/poll", s.requireWorkerAuth(s.pollWorkerWork))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/work/{work_id}/ack", s.requireWorkerAuth(s.ackWorkerWork))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/work/{work_id}/heartbeat", s.requireWorkerAuth(s.heartbeatWorkerWork))
	s.mux.HandleFunc("POST /v1/workers/{worker_id}/work/{work_id}/result", s.requireWorkerAuth(s.completeWorkerWork))
	s.mux.HandleFunc("POST /v1/object-refs", s.createObjectRef)
	s.mux.HandleFunc("GET /v1/object-refs/{object_ref_id}", s.getObjectRef)
	s.mux.HandleFunc("GET /v1/object-refs/{object_ref_id}/download", s.downloadObjectRef)
	s.mux.HandleFunc("DELETE /v1/object-refs/{object_ref_id}", s.deleteObjectRef)

	s.mux.HandleFunc("POST /v1/agents", s.createAgent)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}", s.getAgent)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}/config-versions", s.listAgentConfigVersions)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/config-versions", s.createAgentConfigVersion)
	s.mux.HandleFunc("POST /v1/environments", s.createEnvironment)
	s.mux.HandleFunc("POST /v1/sessions", s.createSession)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}", s.getSession)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/config/upgrade", s.upgradeSessionAgentConfig)
	s.mux.HandleFunc("PATCH /v1/sessions/{session_id}/runtime-settings", s.updateSessionRuntimeSettings)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/interventions", s.listSessionInterventions)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/approve", s.approveSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/interventions/{turn_id}/{call_id}/reject", s.rejectSessionIntervention)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/archive", s.archiveSession)
	s.mux.HandleFunc("DELETE /v1/sessions/{session_id}", s.deleteSession)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/summary", s.getSessionSummary)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/trace", s.getSessionTrace)
	s.mux.HandleFunc("PUT /v1/sessions/{session_id}/summary", s.upsertSessionSummary)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/usage", s.getSessionLLMUsage)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/artifacts", s.createSessionArtifact)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/artifacts", s.listSessionArtifacts)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/artifacts/{artifact_id}/download", s.downloadSessionArtifact)
	s.mux.HandleFunc("DELETE /v1/sessions/{session_id}/artifacts/{artifact_id}", s.deleteSessionArtifact)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/artifacts/upload", s.uploadSessionArtifact)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/events", s.appendSessionEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events", s.listSessionEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events/stream", s.streamSessionEvents)
}

func (s *Server) requireWorkerAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.requireBearerAuth(next, s.workerAuthToken, "tma-worker", "worker authorization required")
}

func (s *Server) requireControlAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.requireBearerAuth(next, s.controlAuthToken, "tma-control", "control authorization required")
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
	}

	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
