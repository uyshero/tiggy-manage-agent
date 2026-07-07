package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"tiggy-manage-agent/internal/managedagents"
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
}

func NewServerWithStoreAndRunner(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger) http.Handler {
	return NewServerWithStoreRunnerAndLLMDefaults(store, turnRunner, logger, "fake", "fake-demo")
}

func NewServerWithStoreRunnerAndLLMDefaults(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, defaultLLMProvider string, defaultLLMModel string) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if turnRunner == nil {
		panic("httpapi runner is required")
	}
	server := &Server{
		mux:                http.NewServeMux(),
		store:              store,
		runner:             turnRunner,
		logger:             logger,
		defaultLLMProvider: defaultLLMProvider,
		defaultLLMModel:    defaultLLMModel,
	}
	server.routes()
	return server.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", healthHandler)

	s.mux.HandleFunc("GET /v1/llm-providers", s.listLLMProviders)
	s.mux.HandleFunc("POST /v1/llm-providers", s.createLLMProvider)
	s.mux.HandleFunc("GET /v1/llm-providers/{provider_id}", s.getLLMProvider)
	s.mux.HandleFunc("PATCH /v1/llm-providers/{provider_id}", s.updateLLMProvider)
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/enable", s.enableLLMProvider)
	s.mux.HandleFunc("POST /v1/llm-providers/{provider_id}/disable", s.disableLLMProvider)

	s.mux.HandleFunc("POST /v1/agents", s.createAgent)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}", s.getAgent)
	s.mux.HandleFunc("GET /v1/agents/{agent_id}/config-versions", s.listAgentConfigVersions)
	s.mux.HandleFunc("POST /v1/agents/{agent_id}/config-versions", s.createAgentConfigVersion)
	s.mux.HandleFunc("POST /v1/environments", s.createEnvironment)
	s.mux.HandleFunc("POST /v1/sessions", s.createSession)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}", s.getSession)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/archive", s.archiveSession)
	s.mux.HandleFunc("DELETE /v1/sessions/{session_id}", s.deleteSession)
	s.mux.HandleFunc("POST /v1/sessions/{session_id}/events", s.appendSessionEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events", s.listSessionEvents)
	s.mux.HandleFunc("GET /v1/sessions/{session_id}/events/stream", s.streamSessionEvents)
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
	case errors.Is(err, managedagents.ErrNotFound):
		status = http.StatusNotFound
		message = err.Error()
	case errors.Is(err, managedagents.ErrTerminated):
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
