package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type sessionDeliberationsResponse struct {
	Deliberations []tools.AgentDeliberationResponse `json:"deliberations"`
}

type cancelSessionDeliberationRequest struct {
	Reason string `json:"reason,omitempty"`
}

type retrySessionDeliberationParticipantRequest struct {
	RoundNumber int `json:"round_number"`
}

func (s *Server) listAgentDiscussionStrategies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, tools.ListAgentDeliberationStrategies())
}

func (s *Server) listSessionDeliberations(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	if _, err := s.getSessionForRequest(r, sessionID); err != nil {
		writeError(w, err)
		return
	}
	store, ok := s.store.(managedagents.AgentDeliberationStore)
	if !ok {
		writeJSON(w, http.StatusOK, sessionDeliberationsResponse{Deliberations: []tools.AgentDeliberationResponse{}})
		return
	}
	deliberations, err := managedagents.ListAgentDeliberationsByParentSessionWithContext(r.Context(), store, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy).(tools.AgentDeliberationService)
	response := sessionDeliberationsResponse{Deliberations: make([]tools.AgentDeliberationResponse, 0, len(deliberations))}
	for _, deliberation := range deliberations {
		state, err := service.GetDeliberation(r.Context(), tools.AgentDeliberationRequest{
			ParentSessionID: sessionID,
			DeliberationID:  deliberation.ID,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		response.Deliberations = append(response.Deliberations, state)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getSessionDeliberation(w http.ResponseWriter, r *http.Request) {
	sessionID, deliberationID, ok := sessionDeliberationPath(w, r)
	if !ok {
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy).(tools.AgentDeliberationService)
	response, err := service.GetDeliberation(r.Context(), tools.AgentDeliberationRequest{
		ParentSessionID: sessionID,
		DeliberationID:  deliberationID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) cancelSessionDeliberation(w http.ResponseWriter, r *http.Request) {
	sessionID, deliberationID, ok := sessionDeliberationPath(w, r)
	if !ok {
		return
	}
	request := cancelSessionDeliberationRequest{}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	reason := strings.TrimSpace(request.Reason)
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy).(tools.AgentDeliberationService)
	response, err := service.CancelDeliberation(r.Context(), tools.AgentDeliberationCancelRequest{
		ParentSessionID: sessionID,
		DeliberationID:  deliberationID,
		Reason:          reason,
	})
	s.recordOperatorAction(r, sessionID, "agent.deliberation.cancel", "agent_deliberation", deliberationID, err, map[string]any{"reason": reason})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) retrySessionDeliberationParticipant(w http.ResponseWriter, r *http.Request) {
	sessionID, deliberationID, ok := sessionDeliberationPath(w, r)
	if !ok {
		return
	}
	participantIndex, err := strconv.Atoi(strings.TrimSpace(r.PathValue("participant_index")))
	if err != nil || participantIndex < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "participant_index must be a non-negative integer"})
		return
	}
	request := retrySessionDeliberationParticipantRequest{}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy).(tools.AgentDeliberationService)
	response, err := service.RetryDeliberationParticipant(r.Context(), tools.AgentDeliberationRetryParticipantRequest{
		ParentSessionID:  sessionID,
		DeliberationID:   deliberationID,
		RoundNumber:      request.RoundNumber,
		ParticipantIndex: participantIndex,
	})
	s.recordOperatorAction(r, sessionID, "agent.deliberation.participant.retry", "agent_deliberation_participant", deliberationID+":"+strconv.Itoa(participantIndex), err, map[string]any{
		"deliberation_id":   deliberationID,
		"round_number":      request.RoundNumber,
		"participant_index": participantIndex,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func sessionDeliberationPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	deliberationID := strings.TrimSpace(r.PathValue("deliberation_id"))
	if sessionID == "" || deliberationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id and deliberation_id are required"})
		return "", "", false
	}
	return sessionID, deliberationID, true
}
