package agentcore_test

import (
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/model"
)

func TestToolBatchPlanValidatesLifecycleCombinations(t *testing.T) {
	t.Parallel()

	validCall := func() agentcore.PlannedToolCall {
		return agentcore.PlannedToolCall{
			Call:          model.ToolCall{ID: "call_1", Name: "default_write_file", Arguments: json.RawMessage(`{}`)},
			ExecutionMode: "sequential", SideEffect: "write", Idempotency: "keyed", IdempotencyKey: "key_1",
			Disposition: agentcore.ToolDispositionExecute, ValidationState: agentcore.ToolValidationValid,
			ApprovalState: agentcore.ToolApprovalNotRequired,
		}
	}
	tests := []struct {
		name        string
		mutate      func(*agentcore.PlannedToolCall)
		interaction *agentcore.RequiredInteraction
		wantError   string
	}{
		{name: "valid default"},
		{name: "unknown disposition", mutate: func(call *agentcore.PlannedToolCall) { call.Disposition = "unknown" }, wantError: "unsupported disposition"},
		{name: "invalid arguments marked executable", mutate: func(call *agentcore.PlannedToolCall) {
			call.ValidationState = agentcore.ToolValidationInvalidArguments
		}, wantError: "requires return_error disposition"},
		{name: "auto approval without policy source", mutate: func(call *agentcore.PlannedToolCall) {
			call.ApprovalState = agentcore.ToolApprovalAuto
		}, wantError: "requires policy source"},
		{name: "pending approval without interaction", mutate: func(call *agentcore.PlannedToolCall) {
			call.ApprovalState = agentcore.ToolApprovalPending
			call.ApprovalSource = agentcore.ToolApprovalSourceHuman
		}, wantError: "requires a tool_approval interaction"},
		{name: "valid pending approval", mutate: func(call *agentcore.PlannedToolCall) {
			call.ApprovalState = agentcore.ToolApprovalPending
			call.ApprovalSource = agentcore.ToolApprovalSourceHuman
		}, interaction: &agentcore.RequiredInteraction{
			ID: "tool_approval:call_1", Kind: "tool_approval", CallID: "call_1", Request: json.RawMessage(`{}`),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := validCall()
			if test.mutate != nil {
				test.mutate(&call)
			}
			plan := agentcore.ToolBatchPlan{Calls: []agentcore.PlannedToolCall{call}}
			if test.interaction != nil {
				plan.Interactions = []agentcore.RequiredInteraction{*test.interaction}
			}
			err := plan.Validate()
			if test.wantError == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("Validate() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestToolBatchPlanRejectsLegacyApprovalStatus(t *testing.T) {
	t.Parallel()

	var plan agentcore.ToolBatchPlan
	err := json.Unmarshal([]byte(`{"calls":[{"call":{"id":"call_1","name":"default_write_file","arguments":{}},"execution_mode":"sequential","side_effect":"write","idempotency":"keyed","idempotency_key":"key_1","approval_status":"approved"}]}`), &plan)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported disposition") {
		t.Fatalf("Validate() error = %v, want missing lifecycle rejection", err)
	}
}
