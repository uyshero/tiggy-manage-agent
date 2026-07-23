package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type workspaceToolPermissionRequest struct {
	PermissionRules []tools.PermissionRule `json:"permission_rules"`
}

type workspaceToolPermissionResponse struct {
	WorkspaceID     string                 `json:"workspace_id"`
	PermissionRules []tools.PermissionRule `json:"permission_rules"`
	Revision        int64                  `json:"revision"`
	UpdatedBy       string                 `json:"updated_by"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type evaluateWorkspaceToolPermissionRequest struct {
	AgentID          string `json:"agent_id,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	Tool             string `json:"tool"`
	Path             string `json:"path"`
	InterventionMode string `json:"intervention_mode,omitempty"`
}

type evaluateWorkspaceToolPermissionResponse struct {
	WorkspaceID      string `json:"workspace_id"`
	AgentID          string `json:"agent_id,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	Tool             string `json:"tool"`
	Path             string `json:"path"`
	Decision         string `json:"decision"`
	Allowed          bool   `json:"allowed"`
	Required         bool   `json:"required"`
	InterventionMode string `json:"intervention_mode"`
	ApprovalPolicy   string `json:"approval_policy,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Risk             string `json:"risk,omitempty"`
	MatchedRuleID    string `json:"matched_rule_id,omitempty"`
	RuleSource       string `json:"rule_source,omitempty"`
}

func (s *Server) getWorkspaceToolPermissions(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.WorkspaceToolPermissionStore)
	if !ok {
		writeError(w, fmt.Errorf("%w: workspace tool permissions are not supported", managedagents.ErrInvalid))
		return
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspace_id"))
	policy, err := store.GetWorkspaceToolPermissionPolicyContext(r.Context(), workspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	rules, err := tools.ParsePermissionRulesForSource(policy.Policy, tools.PermissionRuleSourceWorkspace)
	if err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	setWorkspaceToolPermissionETag(w, policy.Revision)
	writeJSON(w, http.StatusOK, workspaceToolPermissionResponse{
		WorkspaceID: workspaceID, PermissionRules: nonNilSlice(rules),
		Revision: policy.Revision, UpdatedBy: policy.UpdatedBy, UpdatedAt: policy.UpdatedAt,
	})
}

func (s *Server) updateWorkspaceToolPermissions(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.WorkspaceToolPermissionStore)
	if !ok {
		writeError(w, fmt.Errorf("%w: workspace tool permissions are not supported", managedagents.ErrInvalid))
		return
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspace_id"))
	expectedRevision, err := parseWorkspaceToolPermissionIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.recordWorkspaceOperatorAction(r, workspaceID, "workspace.tool_permissions.update", "workspace", workspaceID, err, nil)
		writeError(w, err)
		return
	}
	var request workspaceToolPermissionRequest
	if err := decodeJSON(r, &request); err != nil {
		s.recordWorkspaceOperatorAction(r, workspaceID, "workspace.tool_permissions.update", "workspace", workspaceID, err, nil)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := tools.ValidateWorkspacePermissionRules(request.PermissionRules); err != nil {
		err = fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		s.recordWorkspaceOperatorAction(r, workspaceID, "workspace.tool_permissions.update", "workspace", workspaceID, err, nil)
		writeError(w, err)
		return
	}
	policyJSON, err := json.Marshal(workspaceToolPermissionRequest{PermissionRules: request.PermissionRules})
	if err != nil {
		writeError(w, err)
		return
	}
	policy, err := store.UpdateWorkspaceToolPermissionPolicyContext(r.Context(), managedagents.UpdateWorkspaceToolPermissionPolicyInput{
		WorkspaceID:      workspaceID,
		Policy:           policyJSON,
		ExpectedRevision: expectedRevision,
		UpdatedBy:        requestActorID(r, "system"),
	})
	s.recordWorkspaceOperatorAction(r, workspaceID, "workspace.tool_permissions.update", "workspace", workspaceID, err, map[string]any{"rule_count": len(request.PermissionRules), "expected_revision": expectedRevision})
	if err != nil {
		writeError(w, err)
		return
	}
	setWorkspaceToolPermissionETag(w, policy.Revision)
	writeJSON(w, http.StatusOK, workspaceToolPermissionResponse{
		WorkspaceID: workspaceID, PermissionRules: nonNilSlice(request.PermissionRules),
		Revision: policy.Revision, UpdatedBy: policy.UpdatedBy, UpdatedAt: policy.UpdatedAt,
	})
}

func (s *Server) evaluateWorkspaceToolPermission(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspace_id"))
	if workspaceID == "" {
		writeError(w, fmt.Errorf("%w: workspace_id is required", managedagents.ErrInvalid))
		return
	}
	var request evaluateWorkspaceToolPermissionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Tool = strings.ToLower(strings.TrimSpace(request.Tool))
	request.Path = strings.TrimSpace(request.Path)
	if request.Tool == "" || request.Path == "" {
		writeError(w, fmt.Errorf("%w: tool and path are required", managedagents.ErrInvalid))
		return
	}
	if len(request.Path) > 4096 {
		writeError(w, fmt.Errorf("%w: path must not exceed 4096 characters", managedagents.ErrInvalid))
		return
	}
	switch request.Tool {
	case tools.ModelToolName(tools.NamespaceDefault, "read_file"), tools.ModelToolName(tools.NamespaceDefault, "write_file"), tools.ModelToolName(tools.NamespaceDefault, "edit_file"):
	default:
		writeError(w, fmt.Errorf("%w: tool %q does not support path permission rules", managedagents.ErrInvalid, request.Tool))
		return
	}
	registry := tools.DefaultRegistry()
	call := registry.ResolveCall(tools.Call{Name: request.Tool})
	identifier, apiName := call.Identifier, call.APIName
	manifest, api, ok := registry.GetAPI(identifier, apiName)
	if !ok {
		writeError(w, fmt.Errorf("%w: unknown built-in tool %q", managedagents.ErrInvalid, request.Tool))
		return
	}

	var runtimeSettings, agentTools, workspacePolicy json.RawMessage
	resolvedAgentID := request.AgentID
	resolvedSessionID := request.SessionID
	if request.SessionID != "" {
		config, err := managedagents.ResolveAgentRuntimeConfigWithContext(r.Context(), s.store, request.SessionID)
		if err != nil {
			writeError(w, err)
			return
		}
		if config.WorkspaceID != workspaceID {
			writeError(w, fmt.Errorf("%w: session does not belong to workspace", managedagents.ErrForbidden))
			return
		}
		if request.AgentID != "" && request.AgentID != config.AgentID {
			writeError(w, fmt.Errorf("%w: agent_id does not match session", managedagents.ErrInvalid))
			return
		}
		resolvedAgentID = config.AgentID
		runtimeSettings = config.RuntimeSettings
		agentTools = config.Tools
		workspacePolicy = config.WorkspaceToolPolicy
	} else {
		store, ok := s.store.(managedagents.WorkspaceToolPermissionStore)
		if !ok {
			writeError(w, fmt.Errorf("%w: workspace tool permissions are not supported", managedagents.ErrInvalid))
			return
		}
		policy, err := store.GetWorkspaceToolPermissionPolicyContext(r.Context(), workspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		workspacePolicy = policy.Policy
		if request.AgentID != "" {
			agent, err := s.getAgentForRequest(r, request.AgentID)
			if err != nil {
				writeError(w, err)
				return
			}
			if agent.WorkspaceID != workspaceID {
				writeError(w, fmt.Errorf("%w: agent does not belong to workspace", managedagents.ErrForbidden))
				return
			}
			agentTools = agent.ConfigVersion.Tools
		}
	}

	mode := strings.TrimSpace(request.InterventionMode)
	if mode == "" && request.SessionID != "" {
		mode = tools.ParseInterventionMode(runtimeSettings)
	}
	if mode == "" {
		mode = tools.InterventionModeRequestApproval
	}
	var valid bool
	if mode, valid = tools.NormalizeInterventionMode(mode); !valid {
		writeError(w, fmt.Errorf("%w: invalid intervention_mode", managedagents.ErrInvalid))
		return
	}
	rules, err := tools.ResolvePermissionRules(runtimeSettings, agentTools, workspacePolicy)
	if err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	arguments, err := json.Marshal(map[string]string{"path": request.Path})
	if err != nil {
		writeError(w, err)
		return
	}
	decision := (tools.InterventionPolicy{Mode: mode, Rules: rules}).EvaluateCall(manifest, api, tools.Call{
		Identifier: identifier,
		APIName:    apiName,
		Arguments:  arguments,
	}, tools.ExecutionContext{WorkspaceID: workspaceID, SessionID: resolvedSessionID})
	decisionName := "deny"
	if decision.Allowed {
		decisionName = "allow"
	} else if decision.Required {
		decisionName = "ask"
	}
	writeJSON(w, http.StatusOK, evaluateWorkspaceToolPermissionResponse{
		WorkspaceID: workspaceID, AgentID: resolvedAgentID, SessionID: resolvedSessionID,
		Tool: request.Tool, Path: request.Path, Decision: decisionName,
		Allowed: decision.Allowed, Required: decision.Required, InterventionMode: decision.Mode,
		ApprovalPolicy: decision.ApprovalPolicy, Reason: decision.Reason, Risk: decision.Risk,
		MatchedRuleID: decision.MatchedRuleID, RuleSource: decision.RuleSource,
	})
}

func setWorkspaceToolPermissionETag(w http.ResponseWriter, revision int64) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(revision, 10)))
}

func parseWorkspaceToolPermissionIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%w: If-Match header is required", managedagents.ErrInvalid)
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return 0, fmt.Errorf("%w: If-Match must be a quoted workspace policy revision", managedagents.ErrInvalid)
	}
	revision, err := strconv.ParseInt(unquoted, 10, 64)
	if err != nil || revision <= 0 {
		return 0, fmt.Errorf("%w: If-Match must contain a positive workspace policy revision", managedagents.ErrInvalid)
	}
	return revision, nil
}

func validateAgentToolPermissionRules(raw json.RawMessage) error {
	_, err := tools.ParsePermissionRulesForSource(raw, tools.PermissionRuleSourceAgent)
	if err != nil {
		return fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	var config struct {
		Runtime *string `json:"runtime"`
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &config); err != nil {
			return fmt.Errorf("%w: tools must be valid JSON", managedagents.ErrInvalid)
		}
		if config.Runtime != nil {
			if _, ok := tools.NormalizeToolRuntime(*config.Runtime); !ok {
				return fmt.Errorf("%w: unsupported Agent tools.runtime %q", managedagents.ErrInvalid, *config.Runtime)
			}
		}
	}
	return nil
}
