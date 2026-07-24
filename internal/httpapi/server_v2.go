package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

const requestIDHeader = "X-Request-ID"

type v2RequestContextKey struct{}

type v2Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type v2ErrorEnvelope struct {
	Error v2Error `json:"error"`
}

func (s *Server) registerV2Routes() {
	s.mux.HandleFunc("POST /v2/sessions/{session_id}/runs", s.withV2Request(s.startSessionRunV2))
	s.mux.HandleFunc("GET /v2/sessions/{session_id}/runs", s.withV2Request(s.listSessionRunsV2))
	s.mux.HandleFunc("GET /v2/sessions/{session_id}/runs/{run_id}", s.withV2Request(s.getSessionRunV2))
	s.mux.HandleFunc("POST /v2/sessions/{session_id}/runs/{run_id}/cancel", s.withV2Request(s.cancelSessionRunV2))
	s.mux.HandleFunc("GET /v2/sessions/{session_id}/runs/{run_id}/events", s.withV2Request(s.listSessionRunEventsV2))
	s.mux.HandleFunc("GET /v2/sessions/{session_id}/runs/{run_id}/events/stream", s.withV2Request(s.streamSessionRunEventsV2))
	s.mux.HandleFunc("GET /v2/sessions/{session_id}/live/stream", s.withV2Request(s.streamSessionLiveEventsV2))
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		s.mux.HandleFunc(method+" /v2/{path...}", s.serveV2Alias)
	}
}

func (s *Server) streamSessionLiveEventsV2(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusInternalServerError, "streaming_unsupported", "streaming unsupported", false, nil)
		return
	}
	if _, err := s.getSessionForRequest(r, r.PathValue("session_id")); err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	source, ok := s.runner.(runner.LiveEventSource)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "live_stream_unavailable", "live stream is unavailable", false, nil)
		return
	}
	events, cancel, err := source.SubscribeLiveEvents(r.PathValue("session_id"))
	if err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "live_stream_unavailable", err.Error(), false, nil)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": live stream ready\nretry: 1000\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			if err := writeLiveSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeLiveSSE(w http.ResponseWriter, event runner.LiveEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.StreamSeq, event.Type, encoded)
	return err
}

func (s *Server) serveV2Alias(w http.ResponseWriter, r *http.Request) {
	requestID := ensureRequestID(w, r)
	if !v2AliasAllowed(r.Method, r.URL.Path) {
		writeV2Error(w, requestID, http.StatusNotFound, "not_found", "resource not found", false, nil)
		return
	}
	clone := r.Clone(r.Context())
	clone = clone.WithContext(context.WithValue(clone.Context(), v2RequestContextKey{}, true))
	clone.URL.Path = "/v1/" + strings.TrimPrefix(r.PathValue("path"), "/")
	clone.URL.RawPath = ""
	s.mux.ServeHTTP(w, clone)
}

func isV2Request(r *http.Request) bool {
	v2, _ := r.Context().Value(v2RequestContextKey{}).(bool)
	return v2 || strings.HasPrefix(r.URL.Path, "/v2/")
}

func v2AliasAllowed(method string, path string) bool {
	relative := strings.TrimPrefix(path, "/v2")
	if method == http.MethodGet && relative == "/task-templates" {
		return false
	}
	if method == http.MethodPost && relative == "/workers" {
		return false
	}
	if method == http.MethodPost && strings.HasSuffix(relative, "/heartbeat") && strings.HasPrefix(relative, "/workers/") {
		return false
	}
	if strings.Contains(relative, "/work/poll") || strings.Contains(relative, "/work/") && (strings.HasSuffix(relative, "/ack") || strings.HasSuffix(relative, "/heartbeat") || strings.HasSuffix(relative, "/result")) {
		return false
	}
	return true
}

func (s *Server) withV2Request(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ensureRequestID(w, r)
		next(w, r)
	}
}

func (s *Server) v2EnvelopeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v2/") {
			next.ServeHTTP(w, r)
			return
		}
		requestID := ensureRequestID(w, r)
		response := newV2ResponseWriter(w, requestID)
		next.ServeHTTP(response, r)
		response.finish()
	})
}

func ensureRequestID(w http.ResponseWriter, r *http.Request) string {
	requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
	if requestID == "" || len(requestID) > 128 {
		buffer := make([]byte, 12)
		if _, err := rand.Read(buffer); err == nil {
			requestID = "req_" + hex.EncodeToString(buffer)
		} else {
			requestID = "req_unknown"
		}
		r.Header.Set(requestIDHeader, requestID)
	}
	w.Header().Set(requestIDHeader, requestID)
	return requestID
}

func requestIDFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get(requestIDHeader))
}

func (s *Server) startSessionRunV2(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.SessionRunStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "run_store_unavailable", "run API is unavailable", false, nil)
		return
	}
	var request struct {
		Input          json.RawMessage `json:"input"`
		IdempotencyKey string          `json:"idempotency_key,omitempty"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	canonical, err := canonicalJSON(request.Input)
	if err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_run_input", err.Error(), false, nil)
		return
	}
	hash := sha256.Sum256(canonical)
	result, err := store.StartSessionRunContext(r.Context(), r.PathValue("session_id"), managedagents.StartSessionRunInput{
		Payload: request.Input, IdempotencyKey: request.IdempotencyKey, RequestHash: hex.EncodeToString(hash[:]),
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if result.Created {
		s.logEvents("session run started", result.Events)
		s.dispatchRunnerEvents(r, r.PathValue("session_id"), result.Events)
		writeJSON(w, http.StatusCreated, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("input is required")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("input must be valid JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("input must be a JSON object")
	}
	if len(object) == 0 {
		return nil, errors.New("input must not be empty")
	}
	return json.Marshal(object)
}

func (s *Server) listSessionRunsV2(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.SessionRunStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "run_store_unavailable", "run API is unavailable", false, nil)
		return
	}
	runs, err := store.ListSessionRunsContext(r.Context(), r.PathValue("session_id"))
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": nonNilSlice(runs)})
}

func (s *Server) getSessionRunV2(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunV2(r)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) getRunV2(r *http.Request) (managedagents.SessionRun, error) {
	store, ok := s.store.(managedagents.SessionRunStore)
	if !ok {
		return managedagents.SessionRun{}, fmt.Errorf("%w: run store unavailable", managedagents.ErrInvalid)
	}
	return store.GetSessionRunContext(r.Context(), r.PathValue("session_id"), r.PathValue("run_id"))
}

func (s *Server) cancelSessionRunV2(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunV2(r)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if run.Status == managedagents.TurnStatusCompleted || run.Status == managedagents.TurnStatusFailed || run.Status == managedagents.TurnStatusInterrupted {
		writeJSON(w, http.StatusOK, run)
		return
	}
	events, err := managedagents.AppendEventsWithContext(r.Context(), s.store, run.SessionID, []managedagents.AppendEventInput{{Type: managedagents.EventUserInterrupt}})
	if err != nil {
		latest, latestErr := s.getRunV2(r)
		if latestErr == nil && (latest.Status == managedagents.TurnStatusCompleted || latest.Status == managedagents.TurnStatusFailed || latest.Status == managedagents.TurnStatusInterrupted) {
			writeJSON(w, http.StatusOK, latest)
			return
		}
		writeV2ManagedError(w, r, err)
		return
	}
	s.dispatchRunnerEvents(r, run.SessionID, events)
	latest, err := s.getRunV2(r)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, latest)
}

func (s *Server) listSessionRunEventsV2(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.SessionRunStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "run_store_unavailable", "run API is unavailable", false, nil)
		return
	}
	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_after_seq", err.Error(), false, nil)
		return
	}
	events, err := store.ListSessionRunEventsContext(r.Context(), r.PathValue("session_id"), r.PathValue("run_id"), afterSeq)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": nonNilSlice(events)})
}

func (s *Server) streamSessionRunEventsV2(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusInternalServerError, "streaming_unsupported", "streaming unsupported", false, nil)
		return
	}
	if _, err := s.getRunV2(r); err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_after_seq", err.Error(), false, nil)
		return
	}
	events, cancel, err := managedagents.SubscribeEventsWithContext(r.Context(), s.store, r.PathValue("session_id"), afterSeq)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": stream ready\nretry: 1000\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			turnID := event.TurnID
			if turnID == "" {
				turnID = payloadString(event.Payload, "turn_id")
			}
			if turnID != r.PathValue("run_id") || event.Seq <= afterSeq {
				continue
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			afterSeq = event.Seq
			flusher.Flush()
		}
	}
}

func writeV2ManagedError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := http.StatusText(status)
	switch {
	case strings.Contains(err.Error(), "idempotency_conflict"):
		status, code, message = http.StatusConflict, "idempotency_conflict", "idempotency key is already associated with a different request"
	case errors.Is(err, managedagents.ErrInvalid):
		status, code, message = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, managedagents.ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", err.Error()
	case errors.Is(err, managedagents.ErrRevisionConflict):
		status, code, message = http.StatusPreconditionFailed, "revision_conflict", err.Error()
	case errors.Is(err, managedagents.ErrSessionBusy):
		status, code, message = http.StatusConflict, "session_busy", err.Error()
	case errors.Is(err, managedagents.ErrConflict), errors.Is(err, managedagents.ErrTerminated):
		status, code, message = http.StatusConflict, "conflict", err.Error()
	case errors.Is(err, managedagents.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", err.Error()
	}
	_, retryable := v2ErrorDefaults(status)
	writeV2Error(w, requestIDFromRequest(r), status, code, message, retryable, nil)
}

func writeV2Error(w http.ResponseWriter, requestID string, status int, code string, message string, retryable bool, details map[string]any) {
	writeJSON(w, status, v2ErrorEnvelope{Error: v2Error{
		Code: code, Message: message, RequestID: requestID, Retryable: retryable, Details: details,
	}})
}

type v2ResponseWriter struct {
	target    http.ResponseWriter
	requestID string
	status    int
	buffer    bytes.Buffer
	buffering bool
}

func newV2ResponseWriter(target http.ResponseWriter, requestID string) *v2ResponseWriter {
	return &v2ResponseWriter{target: target, requestID: requestID}
}

func (w *v2ResponseWriter) Header() http.Header { return w.target.Header() }

func (w *v2ResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status >= 400 {
		w.buffering = true
		return
	}
	w.target.WriteHeader(status)
}

func (w *v2ResponseWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		return w.buffer.Write(payload)
	}
	return w.target.Write(payload)
}

func (w *v2ResponseWriter) Flush() {
	if w.buffering {
		return
	}
	if flusher, ok := w.target.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *v2ResponseWriter) finish() {
	if !w.buffering {
		return
	}
	message := strings.TrimSpace(w.buffer.String())
	code := ""
	defaultCode, retryable := v2ErrorDefaults(w.status)
	var details map[string]any
	var legacy map[string]any
	if json.Unmarshal(w.buffer.Bytes(), &legacy) == nil {
		switch value := legacy["error"].(type) {
		case string:
			message = value
		case map[string]any:
			if typed, ok := value["message"].(string); ok {
				message = typed
			}
			if typed, ok := value["code"].(string); ok {
				code = typed
			}
			if typed, ok := value["retryable"].(bool); ok {
				retryable = typed
			}
			if typed, ok := value["details"].(map[string]any); ok {
				details = typed
			}
		}
		for key, value := range legacy {
			if key == "error" {
				continue
			}
			if details == nil {
				details = map[string]any{}
			}
			if _, exists := details[key]; !exists {
				details[key] = value
			}
		}
	}
	if message == "" {
		message = http.StatusText(w.status)
	}
	if code == "" {
		code = defaultCode
	}
	w.target.Header().Del("Content-Length")
	writeV2Error(w.target, w.requestID, w.status, code, message, retryable, details)
}

func v2ErrorDefaults(status int) (string, bool) {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request", false
	case http.StatusUnauthorized:
		return "unauthorized", false
	case http.StatusForbidden:
		return "forbidden", false
	case http.StatusNotFound:
		return "not_found", false
	case http.StatusMethodNotAllowed:
		return "method_not_allowed", false
	case http.StatusConflict:
		return "conflict", false
	case http.StatusPreconditionFailed:
		return "revision_conflict", false
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large", false
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type", false
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity", false
	case http.StatusTooManyRequests:
		return "rate_limited", true
	case http.StatusBadGateway:
		return "upstream_error", true
	case http.StatusServiceUnavailable:
		return "service_unavailable", true
	case http.StatusGatewayTimeout:
		return "upstream_timeout", true
	default:
		return "internal_error", false
	}
}

var _ http.Flusher = (*v2ResponseWriter)(nil)
