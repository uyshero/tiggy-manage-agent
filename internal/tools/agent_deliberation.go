package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const (
	AgentDeliberationStrategyBrainstormCritique = "brainstorm_then_critique"
	AgentDeliberationStrategyDebate             = "structured_debate"
	AgentDeliberationStrategyRedTeam            = "red_team_review"
	AgentDeliberationStrategyExpertPanel        = "expert_panel"
)

type AgentDeliberationService interface {
	CreateDeliberation(context.Context, AgentDeliberationCreateRequest) (AgentDeliberationResponse, error)
	GetDeliberation(context.Context, AgentDeliberationRequest) (AgentDeliberationResponse, error)
	WaitDeliberation(context.Context, AgentDeliberationWaitRequest) (AgentDeliberationWaitResponse, error)
	CancelDeliberation(context.Context, AgentDeliberationCancelRequest) (AgentDeliberationResponse, error)
	RetryDeliberationParticipant(context.Context, AgentDeliberationRetryParticipantRequest) (AgentDeliberationResponse, error)
	CollectDeliberation(context.Context, AgentDeliberationRequest) (AgentDeliberationResponse, error)
}

type AgentDeliberationStrategy struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type AgentDeliberationStrategyListResponse struct {
	Strategies     []AgentDeliberationStrategy `json:"strategies"`
	TeamPlanSchema json.RawMessage             `json:"team_plan_schema"`
}

type AgentDeliberationBudget struct {
	MaxTokens  int64 `json:"max_tokens,omitempty"`
	MaxSeconds int   `json:"max_seconds,omitempty"`
}

type AgentDeliberationParticipantRequest struct {
	RoleID        string `json:"role_id"`
	RoleTitle     string `json:"role_title"`
	Goal          string `json:"goal"`
	AgentID       string `json:"agent_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

type AgentDeliberationCreateRequest struct {
	ParentSessionID        string                                `json:"-"`
	ParentTurnID           string                                `json:"-"`
	Objective              string                                `json:"objective"`
	Strategy               string                                `json:"strategy,omitempty"`
	Participants           []AgentDeliberationParticipantRequest `json:"participants"`
	ModeratorAgentID       string                                `json:"moderator_agent_id,omitempty"`
	ModeratorEnvironmentID string                                `json:"moderator_environment_id,omitempty"`
	Budget                 AgentDeliberationBudget               `json:"budget,omitempty"`
	MaxRounds              int                                   `json:"max_rounds,omitempty"`
	IdempotencyKey         string                                `json:"idempotency_key,omitempty"`
}

type AgentDeliberationRequest struct {
	ParentSessionID string `json:"-"`
	DeliberationID  string `json:"deliberation_id"`
}

type AgentDeliberationWaitRequest struct {
	ParentSessionID string `json:"-"`
	DeliberationID  string `json:"deliberation_id"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

type AgentDeliberationCancelRequest struct {
	ParentSessionID string `json:"-"`
	DeliberationID  string `json:"deliberation_id"`
	Reason          string `json:"reason,omitempty"`
}

type AgentDeliberationRetryParticipantRequest struct {
	ParentSessionID  string `json:"-"`
	DeliberationID   string `json:"deliberation_id"`
	RoundNumber      int    `json:"round_number"`
	ParticipantIndex int    `json:"participant_index"`
}

type AgentDeliberationRoundState struct {
	Round         managedagents.AgentDeliberationRound          `json:"round"`
	Contributions []managedagents.AgentDeliberationContribution `json:"contributions"`
}

type AgentDeliberationResponse struct {
	Deliberation managedagents.AgentDeliberation              `json:"deliberation"`
	Participants []managedagents.AgentDeliberationParticipant `json:"participants"`
	Rounds       []AgentDeliberationRoundState                `json:"rounds"`
	Completed    bool                                         `json:"completed,omitempty"`
}

type AgentDeliberationWaitResponse struct {
	AgentDeliberationResponse
	TimedOut bool `json:"timed_out,omitempty"`
}

var agentDeliberationRoleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var agentDeliberationStrategies = []AgentDeliberationStrategy{
	{ID: AgentDeliberationStrategyBrainstormCritique, Title: "Brainstorm then Critique", Description: "Independent proposals followed by cross-review and moderator synthesis."},
	{ID: AgentDeliberationStrategyDebate, Title: "Structured Debate", Description: "Participants defend distinct positions, challenge assumptions, and converge on supported conclusions."},
	{ID: AgentDeliberationStrategyRedTeam, Title: "Red Team Review", Description: "Primary proposals are stress-tested by adversarial and risk-focused roles."},
	{ID: AgentDeliberationStrategyExpertPanel, Title: "Expert Panel", Description: "Specialists contribute domain views, then reconcile tradeoffs into a panel recommendation."},
}

var AgentDeliberationTeamPlanSchema = json.RawMessage(`{
  "type":"object",
  "required":["objective","participants"],
  "properties":{
    "objective":{"type":"string","minLength":1,"maxLength":8000},
    "strategy":{"type":"string","enum":["brainstorm_then_critique","structured_debate","red_team_review","expert_panel"]},
    "participants":{"type":"array","minItems":2,"maxItems":8,"items":{"type":"object","required":["role_id","role_title","goal"],"properties":{"role_id":{"type":"string","pattern":"^[a-z0-9][a-z0-9_-]{0,63}$"},"role_title":{"type":"string","minLength":1,"maxLength":120},"goal":{"type":"string","minLength":1,"maxLength":2000},"agent_id":{"type":"string"},"environment_id":{"type":"string"}}}},
    "moderator_agent_id":{"type":"string"},
    "moderator_environment_id":{"type":"string"},
    "budget":{"type":"object","properties":{"max_tokens":{"type":"integer","minimum":1000,"maximum":1000000},"max_seconds":{"type":"integer","minimum":60,"maximum":7200}}},
    "max_rounds":{"type":"integer","enum":[2]},
    "idempotency_key":{"type":"string","maxLength":200}
  }
}`)

var AgentDeliberationContributionSchema = json.RawMessage(`{"type":"object","required":["position","key_points","risks","questions","confidence"],"properties":{"position":{"type":"string"},"key_points":{"type":"array","items":{"type":"string"}},"risks":{"type":"array","items":{"type":"string"}},"questions":{"type":"array","items":{"type":"string"}},"confidence":{"type":"number","minimum":0,"maximum":1}}}`)

var AgentDeliberationModerationSchema = json.RawMessage(`{"type":"object","required":["agreements","disagreements","missing_evidence","questions_by_role"],"properties":{"agreements":{"type":"array","items":{"type":"string"}},"disagreements":{"type":"array","items":{"type":"string"}},"missing_evidence":{"type":"array","items":{"type":"string"}},"questions_by_role":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}}}}`)

var AgentDeliberationFinalSchema = json.RawMessage(`{"type":"object","required":["recommendation","consensus","dissenting_opinions","risks","followups","confidence"],"properties":{"recommendation":{"type":"string"},"consensus":{"type":"array","items":{"type":"string"}},"dissenting_opinions":{"type":"array","items":{"type":"string"}},"risks":{"type":"array","items":{"type":"string"}},"followups":{"type":"array","items":{"type":"string"}},"confidence":{"type":"number","minimum":0,"maximum":1}}}`)

func ListAgentDeliberationStrategies() AgentDeliberationStrategyListResponse {
	strategies := append([]AgentDeliberationStrategy(nil), agentDeliberationStrategies...)
	return AgentDeliberationStrategyListResponse{Strategies: strategies, TeamPlanSchema: append(json.RawMessage(nil), AgentDeliberationTeamPlanSchema...)}
}

func NormalizeAgentDeliberationRequest(request AgentDeliberationCreateRequest, parent managedagents.Session) (AgentDeliberationCreateRequest, error) {
	request.Objective = strings.TrimSpace(request.Objective)
	if request.Objective == "" || len(request.Objective) > 8000 {
		return request, fmt.Errorf("objective is required and must not exceed 8000 characters")
	}
	request.Strategy = strings.TrimSpace(request.Strategy)
	if request.Strategy == "" {
		request.Strategy = AgentDeliberationStrategyBrainstormCritique
	}
	if !isAgentDeliberationStrategy(request.Strategy) {
		return request, fmt.Errorf("unsupported deliberation strategy %q", request.Strategy)
	}
	if len(request.Participants) < 2 || len(request.Participants) > 8 {
		return request, fmt.Errorf("participants must contain between 2 and 8 roles")
	}
	seen := map[string]bool{}
	for index := range request.Participants {
		participant := &request.Participants[index]
		participant.RoleID = strings.TrimSpace(strings.ToLower(participant.RoleID))
		participant.RoleTitle = strings.TrimSpace(participant.RoleTitle)
		participant.Goal = strings.TrimSpace(participant.Goal)
		if !agentDeliberationRoleIDPattern.MatchString(participant.RoleID) {
			return request, fmt.Errorf("participant %d role_id is invalid", index)
		}
		if seen[participant.RoleID] {
			return request, fmt.Errorf("participant role_id %q is duplicated", participant.RoleID)
		}
		seen[participant.RoleID] = true
		if participant.RoleTitle == "" || participant.Goal == "" || len(participant.RoleTitle) > 120 || len(participant.Goal) > 2000 {
			return request, fmt.Errorf("participant %d requires bounded role_title and goal", index)
		}
		if strings.TrimSpace(participant.AgentID) == "" {
			participant.AgentID = parent.AgentID
		}
		if strings.TrimSpace(participant.EnvironmentID) == "" {
			participant.EnvironmentID = parent.EnvironmentID
		}
	}
	if request.MaxRounds == 0 {
		request.MaxRounds = 2
	}
	if request.MaxRounds != 2 {
		return request, fmt.Errorf("deliberation currently requires exactly 2 rounds")
	}
	if request.Budget.MaxTokens == 0 {
		request.Budget.MaxTokens = 100000
	}
	if request.Budget.MaxTokens < 1000 || request.Budget.MaxTokens > 1000000 {
		return request, fmt.Errorf("budget.max_tokens must be between 1000 and 1000000")
	}
	if request.Budget.MaxSeconds == 0 {
		request.Budget.MaxSeconds = 1800
	}
	if request.Budget.MaxSeconds < 60 || request.Budget.MaxSeconds > 7200 {
		return request, fmt.Errorf("budget.max_seconds must be between 60 and 7200")
	}
	if strings.TrimSpace(request.ModeratorAgentID) == "" {
		request.ModeratorAgentID = parent.AgentID
	}
	if strings.TrimSpace(request.ModeratorEnvironmentID) == "" {
		request.ModeratorEnvironmentID = parent.EnvironmentID
	}
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if len(request.IdempotencyKey) > 200 {
		return request, fmt.Errorf("idempotency_key must not exceed 200 characters")
	}
	return request, nil
}

func isAgentDeliberationStrategy(strategy string) bool {
	for _, item := range agentDeliberationStrategies {
		if item.ID == strategy {
			return true
		}
	}
	return false
}
