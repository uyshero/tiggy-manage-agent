package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestAgentDeliberationTeamPlanSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(AgentDeliberationTeamPlanSchema, &schema); err != nil {
		t.Fatalf("decode team plan schema: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing schema properties: %#v", schema)
	}
	participants, ok := properties["participants"].(map[string]any)
	if !ok || participants["minItems"] != float64(2) || participants["maxItems"] != float64(8) {
		t.Fatalf("unexpected participant schema: %#v", participants)
	}
	maxRounds, ok := properties["max_rounds"].(map[string]any)
	if !ok || len(maxRounds["enum"].([]any)) != 1 || maxRounds["enum"].([]any)[0] != float64(2) {
		t.Fatalf("unexpected max rounds schema: %#v", maxRounds)
	}
}

func TestNormalizeAgentDeliberationRequestDefaults(t *testing.T) {
	request, err := NormalizeAgentDeliberationRequest(validAgentDeliberationRequest(), managedagents.Session{
		AgentID:       "agnt_parent",
		EnvironmentID: "envr_parent",
	})
	if err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	if request.Strategy != AgentDeliberationStrategyBrainstormCritique || request.MaxRounds != 2 {
		t.Fatalf("unexpected strategy or rounds: %#v", request)
	}
	if request.Budget.MaxTokens != 100000 || request.Budget.MaxSeconds != 1800 {
		t.Fatalf("unexpected default budget: %#v", request.Budget)
	}
	for _, participant := range request.Participants {
		if participant.AgentID != "agnt_parent" || participant.EnvironmentID != "envr_parent" {
			t.Fatalf("participant did not inherit parent runtime: %#v", participant)
		}
	}
}

func TestNormalizeAgentDeliberationRequestParticipantBounds(t *testing.T) {
	for _, count := range []int{1, 9} {
		request := validAgentDeliberationRequest()
		request.Participants = make([]AgentDeliberationParticipantRequest, count)
		for index := range request.Participants {
			request.Participants[index] = AgentDeliberationParticipantRequest{
				RoleID:    "role_" + strings.Repeat("x", index+1),
				RoleTitle: "Role",
				Goal:      "Contribute",
			}
		}
		if _, err := NormalizeAgentDeliberationRequest(request, managedagents.Session{}); err == nil {
			t.Fatalf("expected participant count %d to fail", count)
		}
	}
}

func TestNormalizeAgentDeliberationRequestRejectsRoleIDs(t *testing.T) {
	tests := []struct {
		name   string
		roleID string
		second string
	}{
		{name: "invalid", roleID: "Risk Reviewer!", second: "builder"},
		{name: "duplicate", roleID: "reviewer", second: "REVIEWER"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validAgentDeliberationRequest()
			request.Participants[0].RoleID = test.roleID
			request.Participants[1].RoleID = test.second
			if _, err := NormalizeAgentDeliberationRequest(request, managedagents.Session{}); err == nil {
				t.Fatal("expected invalid role IDs to fail")
			}
		})
	}
}

func TestNormalizeAgentDeliberationRequestBudgetBounds(t *testing.T) {
	tests := []AgentDeliberationBudget{
		{MaxTokens: 999, MaxSeconds: 60},
		{MaxTokens: 1000001, MaxSeconds: 60},
		{MaxTokens: 1000, MaxSeconds: 59},
		{MaxTokens: 1000, MaxSeconds: 7201},
	}
	for _, budget := range tests {
		request := validAgentDeliberationRequest()
		request.Budget = budget
		if _, err := NormalizeAgentDeliberationRequest(request, managedagents.Session{}); err == nil {
			t.Fatalf("expected budget to fail: %#v", budget)
		}
	}
}

func TestListAgentDeliberationStrategies(t *testing.T) {
	response := ListAgentDeliberationStrategies()
	if len(response.Strategies) != 4 || len(response.TeamPlanSchema) == 0 {
		t.Fatalf("unexpected strategy response: %#v", response)
	}
	seen := map[string]bool{}
	for _, strategy := range response.Strategies {
		seen[strategy.ID] = true
	}
	for _, strategy := range []string{
		AgentDeliberationStrategyBrainstormCritique,
		AgentDeliberationStrategyDebate,
		AgentDeliberationStrategyRedTeam,
		AgentDeliberationStrategyExpertPanel,
	} {
		if !seen[strategy] {
			t.Fatalf("missing strategy %q", strategy)
		}
	}
}

func validAgentDeliberationRequest() AgentDeliberationCreateRequest {
	return AgentDeliberationCreateRequest{
		Objective: "Choose a reliable architecture",
		Participants: []AgentDeliberationParticipantRequest{
			{RoleID: "architect", RoleTitle: "Architect", Goal: "Propose the design"},
			{RoleID: "reviewer", RoleTitle: "Reviewer", Goal: "Challenge the design"},
		},
	}
}
