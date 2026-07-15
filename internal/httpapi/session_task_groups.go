package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type inspectorTaskGroupState struct {
	TemplateID    string                       `json:"template_id,omitempty"`
	TemplateTitle string                       `json:"template_title,omitempty"`
	State         tools.AgentTaskGroupResponse `json:"state"`
}

type sessionTaskGroupsResponse struct {
	TaskGroups []inspectorTaskGroupState `json:"task_groups"`
}

type inspectorTaskGroupTreeSummary struct {
	Sessions       int   `json:"sessions"`
	Groups         int   `json:"groups"`
	Items          int   `json:"items"`
	Queued         int   `json:"queued"`
	Running        int   `json:"running"`
	Rejected       int   `json:"rejected"`
	Waiting        int   `json:"waiting"`
	MaxWaitSeconds int64 `json:"max_wait_seconds"`
}

type inspectorTaskGroupSessionNode struct {
	Session    managedagents.Session           `json:"session"`
	TaskGroups []inspectorTaskGroupState       `json:"task_groups"`
	Children   []inspectorTaskGroupSessionNode `json:"children"`
}

type sessionTaskGroupTreeResponse struct {
	Root    inspectorTaskGroupSessionNode `json:"root"`
	Summary inspectorTaskGroupTreeSummary `json:"summary"`
}

func (s *Server) listSessionTaskGroups(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	if _, err := s.getSessionForRequest(r, sessionID); err != nil {
		writeError(w, err)
		return
	}
	groups, err := managedagents.ListSubagentTaskGroupsByParentSessionWithContext(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy)
	hints, _ := s.sessionTaskGroupTemplateHints(r.Context(), sessionID)
	response := sessionTaskGroupsResponse{
		TaskGroups: make([]inspectorTaskGroupState, 0, len(groups)),
	}
	for _, group := range groups {
		state, err := service.GetTaskGroup(r.Context(), tools.AgentTaskGroupRequest{
			ParentSessionID: sessionID,
			GroupID:         group.ID,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		entry := inspectorTaskGroupState{State: state}
		if hint, ok := hints[group.ID]; ok {
			entry.TemplateID = hint.ID
			entry.TemplateTitle = hint.Title
		}
		response.TaskGroups = append(response.TaskGroups, entry)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getSessionTaskGroup(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	groupID := strings.TrimSpace(r.PathValue("group_id"))
	if sessionID == "" || groupID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id and group_id are required"})
		return
	}
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy)
	state, err := service.GetTaskGroup(r.Context(), tools.AgentTaskGroupRequest{
		ParentSessionID: sessionID,
		GroupID:         groupID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	entry := inspectorTaskGroupState{State: state}
	if hints, err := s.sessionTaskGroupTemplateHints(r.Context(), sessionID); err == nil {
		if hint, ok := hints[groupID]; ok {
			entry.TemplateID = hint.ID
			entry.TemplateTitle = hint.Title
		}
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) getSessionTaskGroupTree(w http.ResponseWriter, r *http.Request) {
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
	service := newAgentToolService(s.store, s.runner, s.logger, s.subagentPolicy)
	summary := inspectorTaskGroupTreeSummary{}
	node, err := s.buildSessionTaskGroupTree(r, service, root, map[string]bool{}, &summary)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionTaskGroupTreeResponse{Root: node, Summary: summary})
}

func (s *Server) buildSessionTaskGroupTree(r *http.Request, service tools.AgentToolService, session managedagents.Session, visited map[string]bool, summary *inspectorTaskGroupTreeSummary) (inspectorTaskGroupSessionNode, error) {
	if visited[session.ID] {
		return inspectorTaskGroupSessionNode{}, nil
	}
	visited[session.ID] = true
	summary.Sessions++

	groups, err := managedagents.ListSubagentTaskGroupsByParentSessionWithContext(r.Context(), s.store, session.ID)
	if err != nil {
		return inspectorTaskGroupSessionNode{}, err
	}
	hints, _ := s.sessionTaskGroupTemplateHints(r.Context(), session.ID)
	node := inspectorTaskGroupSessionNode{
		Session:    session,
		TaskGroups: make([]inspectorTaskGroupState, 0, len(groups)),
		Children:   []inspectorTaskGroupSessionNode{},
	}
	for _, group := range groups {
		state, err := service.GetTaskGroup(r.Context(), tools.AgentTaskGroupRequest{
			ParentSessionID: session.ID,
			GroupID:         group.ID,
		})
		if err != nil {
			return inspectorTaskGroupSessionNode{}, err
		}
		entry := inspectorTaskGroupState{State: state}
		if hint, ok := hints[group.ID]; ok {
			entry.TemplateID = hint.ID
			entry.TemplateTitle = hint.Title
		}
		node.TaskGroups = append(node.TaskGroups, entry)
		addTaskGroupTreeSummary(summary, state)
	}

	children, err := s.listSessionsForRequest(r, managedagents.ListSessionsInput{
		ParentSessionID: session.ID,
		IncludeArchived: true,
		Limit:           100,
	})
	if err != nil {
		return inspectorTaskGroupSessionNode{}, err
	}
	for _, child := range children {
		if visited[child.ID] {
			continue
		}
		childNode, err := s.buildSessionTaskGroupTree(r, service, child, visited, summary)
		if err != nil {
			return inspectorTaskGroupSessionNode{}, err
		}
		node.Children = append(node.Children, childNode)
	}
	return node, nil
}

func addTaskGroupTreeSummary(summary *inspectorTaskGroupTreeSummary, state tools.AgentTaskGroupResponse) {
	summary.Groups++
	summary.Items += len(state.Items)
	summary.Queued += state.Summary.Queued
	summary.Running += state.Summary.Running
	summary.Rejected += state.Summary.Rejected
	summary.Waiting += state.Summary.Waiting
	for _, item := range state.Items {
		if item.QueueRequest != nil && item.QueueRequest.WaitSeconds > summary.MaxWaitSeconds {
			summary.MaxWaitSeconds = item.QueueRequest.WaitSeconds
		}
	}
}

func (s *Server) sessionTaskGroupTemplateHints(ctx context.Context, sessionID string) (map[string]tools.AgentTaskGroupTemplate, error) {
	events, err := managedagents.ListEventsWithContext(ctx, s.store, sessionID, 0)
	if err != nil {
		return nil, err
	}
	hints := make(map[string]tools.AgentTaskGroupTemplate)
	for _, event := range events {
		if event.Type != managedagents.EventRuntimeSubagentGroupCreated {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		groupID, _ := payload["group_id"].(string)
		templateID, _ := payload["template_id"].(string)
		if strings.TrimSpace(groupID) == "" || strings.TrimSpace(templateID) == "" {
			continue
		}
		if template, ok := tools.LookupAgentTaskGroupTemplate(templateID); ok {
			hints[groupID] = template
		}
	}
	return hints, nil
}
