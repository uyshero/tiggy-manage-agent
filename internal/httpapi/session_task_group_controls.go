package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type inspectorCancelTaskGroupRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (s *Server) cancelSessionTaskGroup(w http.ResponseWriter, r *http.Request) {
	sessionID, groupID, ok := taskGroupControlPath(w, r)
	if !ok {
		return
	}
	request := inspectorCancelTaskGroupRequest{}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy)
	response, err := service.CancelTaskGroup(r.Context(), tools.AgentTaskGroupCancelRequest{
		ParentSessionID: sessionID,
		GroupID:         groupID,
		Reason:          strings.TrimSpace(request.Reason),
	})
	s.recordOperatorAction(r, sessionID, "agent.task_group.cancel", "task_group", groupID, err, map[string]any{
		"reason": strings.TrimSpace(request.Reason),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) retrySessionTaskGroup(w http.ResponseWriter, r *http.Request) {
	sessionID, groupID, ok := taskGroupControlPath(w, r)
	if !ok {
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy)
	response, err := service.RetryTaskGroup(r.Context(), tools.AgentTaskGroupRetryRequest{
		ParentSessionID: sessionID,
		GroupID:         groupID,
	})
	s.recordOperatorAction(r, sessionID, "agent.task_group.retry", "task_group", groupID, err, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) retrySessionTaskGroupItem(w http.ResponseWriter, r *http.Request) {
	sessionID, groupID, ok := taskGroupControlPath(w, r)
	if !ok {
		return
	}
	itemIndex, err := strconv.Atoi(strings.TrimSpace(r.PathValue("item_index")))
	if err != nil || itemIndex < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "item_index must be a non-negative integer"})
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy)
	response, err := service.RetryTaskGroupItem(r.Context(), tools.AgentTaskGroupRetryItemRequest{
		ParentSessionID: sessionID,
		GroupID:         groupID,
		ItemIndex:       itemIndex,
	})
	s.recordOperatorAction(r, sessionID, "agent.task_group.item.retry", "task_group_item", groupID+":"+strconv.Itoa(itemIndex), err, map[string]any{
		"group_id":   groupID,
		"item_index": itemIndex,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) reapOrphanSubagents(w http.ResponseWriter, r *http.Request) {
	input := managedagents.ReapOrphanSubagentsInput{}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	store, ok := s.store.(managedagents.OrphanSubagentStore)
	if !ok {
		err := errors.New("orphan subagent reaping is not supported by this store")
		s.recordOperatorAction(r, "", "agent.orphans.reap", "subagent_orphans", "", err, map[string]any{"limit": input.Limit})
		writeError(w, err)
		return
	}
	reaped, err := store.ReapOrphanSubagents(input)
	details := map[string]any{"limit": input.Limit, "count": len(reaped)}
	if len(reaped) > 0 {
		sessionIDs := make([]string, 0, len(reaped))
		for _, item := range reaped {
			sessionIDs = append(sessionIDs, item.Session.ID)
		}
		details["session_ids"] = sessionIDs
	}
	s.recordOperatorAction(r, "", "agent.orphans.reap", "subagent_orphans", "", err, details)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reaped": reaped,
		"count":  len(reaped),
	})
}

func (s *Server) listOperatorAudit(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"audit_records": []managedagents.OperatorAuditRecord{}})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 200"})
			return
		}
		limit = parsed
	}
	records, err := managedagents.ListOperatorAuditWithContext(r.Context(), store, managedagents.ListOperatorAuditInput{
		WorkspaceID: requestWorkspaceID(r, r.URL.Query().Get("workspace_id")),
		SessionID:   strings.TrimSpace(r.URL.Query().Get("session_id")),
		PrincipalID: strings.TrimSpace(r.URL.Query().Get("principal_id")),
		Action:      strings.TrimSpace(r.URL.Query().Get("action")),
		Limit:       limit,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_records": nonNilSlice(records)})
}

func (s *Server) listSessionOperatorAudit(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"audit_records": []managedagents.OperatorAuditRecord{}})
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	root, err := s.getSessionForRequest(r, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	sessionIDs := []string{}
	if err := s.collectSessionLineageIDs(r, root, map[string]bool{}, &sessionIDs); err != nil {
		writeError(w, err)
		return
	}
	records := []managedagents.OperatorAuditRecord{}
	for _, lineageSessionID := range sessionIDs {
		entries, err := managedagents.ListOperatorAuditWithContext(r.Context(), store, managedagents.ListOperatorAuditInput{SessionID: lineageSessionID, Limit: 200})
		if err != nil {
			writeError(w, err)
			return
		}
		records = append(records, entries...)
	}
	sort.Slice(records, func(i int, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if len(records) > 50 {
		records = records[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_records": nonNilSlice(records)})
}

func (s *Server) collectSessionLineageIDs(r *http.Request, session managedagents.Session, visited map[string]bool, sessionIDs *[]string) error {
	if visited[session.ID] {
		return nil
	}
	visited[session.ID] = true
	*sessionIDs = append(*sessionIDs, session.ID)
	children, err := s.listSessionsForRequest(r, managedagents.ListSessionsInput{
		ParentSessionID: session.ID,
		IncludeArchived: true,
		Limit:           100,
	})
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := s.collectSessionLineageIDs(r, child, visited, sessionIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) recordOperatorAction(r *http.Request, sessionID string, action string, resourceType string, resourceID string, actionErr error, details map[string]any) {
	workspaceID := ""
	if strings.TrimSpace(sessionID) != "" {
		if session, err := s.getSessionForRequest(r, sessionID); err == nil {
			workspaceID = session.WorkspaceID
		}
	}
	s.recordScopedOperatorAction(r, workspaceID, sessionID, action, resourceType, resourceID, actionErr, details)
}

func (s *Server) recordWorkspaceOperatorAction(r *http.Request, workspaceID string, action string, resourceType string, resourceID string, actionErr error, details map[string]any) {
	s.recordScopedOperatorAction(r, workspaceID, "", action, resourceType, resourceID, actionErr, details)
}

func (s *Server) recordScopedOperatorAction(r *http.Request, workspaceID string, sessionID string, action string, resourceType string, resourceID string, actionErr error, details map[string]any) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	detailsJSON := json.RawMessage(`{}`)
	if len(details) > 0 {
		if encoded, err := json.Marshal(details); err == nil {
			detailsJSON = encoded
		}
	}
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID:   auditWorkspaceID(r, workspaceID),
		SessionID:     strings.TrimSpace(sessionID),
		PrincipalID:   principal.ID,
		OperatorLabel: principal.OperatorLabel,
		Role:          principal.Role,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    resourceID,
		Outcome:       outcome,
		ErrorMessage:  errorMessage,
		Details:       detailsJSON,
	}); err != nil {
		s.logger.Warn("operator audit write failed", "action", action, "resource_type", resourceType, "resource_id", resourceID, "error", err)
	}
}

func taskGroupControlPath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	groupID := strings.TrimSpace(r.PathValue("group_id"))
	if sessionID == "" || groupID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id and group_id are required"})
		return "", "", false
	}
	return sessionID, groupID, true
}
