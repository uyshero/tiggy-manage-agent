package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

type appendEventsRequest struct {
	Events []managedagents.AppendEventInput `json:"events"`
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateAgentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	agent, err := s.store.CreateAgent(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateEnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	environment, err := s.store.CreateEnvironment(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, environment)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateSessionInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	session, err := s.store.CreateSession(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) archiveSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.ArchiveSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.PathValue("session_id")); err != nil {
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) appendSessionEvents(w http.ResponseWriter, r *http.Request) {
	var request appendEventsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.store.AppendEvents(r.PathValue("session_id"), request.Events)
	if err != nil {
		writeError(w, err)
		return
	}

	// Store 先把事件和状态写入数据库；后台执行只基于已经落库的事件启动。
	sessionID := r.PathValue("session_id")
	s.logEvents("session events appended", events)
	s.dispatchRunnerEvents(r, sessionID, events)
	writeJSON(w, http.StatusCreated, map[string]any{"events": events})
}

func (s *Server) dispatchRunnerEvents(r *http.Request, sessionID string, events []managedagents.Event) {
	for _, event := range events {
		switch event.Type {
		case managedagents.EventUserMessage:
			// turn_id 由 Store 生成并写入 payload，避免客户端伪造执行编号。
			turnID := payloadString(event.Payload, "turn_id")
			s.logger.Info("session turn starting",
				"session_id", sessionID,
				"turn_id", turnID,
				"event_id", event.ID,
				"event_seq", event.Seq,
			)
			if err := s.runner.StartTurn(r.Context(), runner.TurnRequest{
				SessionID:   sessionID,
				TurnID:      turnID,
				UserPayload: event.Payload,
			}); err != nil {
				reason := err.Error()
				s.logger.Error("runner start turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
				failedEvents, failErr := s.store.FailSessionTurn(sessionID, turnID, reason)
				if failErr != nil {
					s.logger.Error("session turn fail transition failed",
						"session_id", sessionID,
						"turn_id", turnID,
						"error", failErr,
					)
					continue
				}
				s.logEvents("session turn failed", failedEvents)
			}
		case managedagents.EventUserInterrupt:
			turnID := payloadString(event.Payload, "turn_id")
			if err := s.runner.InterruptTurn(r.Context(), runner.InterruptRequest{
				SessionID: sessionID,
				TurnID:    turnID,
			}); err != nil {
				s.logger.Error("runner interrupt turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
			}
		}
	}
}

func (s *Server) logEvents(message string, events []managedagents.Event) {
	for _, event := range events {
		s.logger.Info(message,
			"event_id", event.ID,
			"session_id", event.SessionID,
			"turn_id", payloadString(event.Payload, "turn_id"),
			"event_seq", event.Seq,
			"event_type", event.Type,
		)
	}
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}

	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}

func (s *Server) listSessionEvents(w http.ResponseWriter, r *http.Request) {
	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.store.ListEvents(r.PathValue("session_id"), afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	sessionID := r.PathValue("session_id")
	// SSE 先用 after_seq 补历史，再订阅未来事件，支持断线续传。
	history, err := s.store.ListEvents(sessionID, afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	events, cancel, err := s.store.SubscribeEvents(sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cancel()
	s.logger.Info("sse stream opened",
		"session_id", sessionID,
		"after_seq", afterSeq,
		"history_events", len(history),
	)
	defer s.logger.Info("sse stream closed",
		"session_id", sessionID,
		"after_seq", afterSeq,
	)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, event := range history {
		if err := writeSSE(w, event); err != nil {
			return
		}
		flusher.Flush()
	}

	fmt.Fprint(w, ": stream ready\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Seq <= afterSeq {
				continue
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func parseAfterSeq(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("after_seq")
	if value == "" {
		return 0, nil
	}

	return strconv.ParseInt(value, 10, 64)
}

func writeSSE(w http.ResponseWriter, event managedagents.Event) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, encoded)
	return err
}
