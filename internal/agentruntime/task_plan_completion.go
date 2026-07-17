package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const taskPlanCompletionValidator = "builtin.task_plan"

// TaskPlanCompletionGate prevents a final response from bypassing an active
// execution plan. The plan store remains the source of truth; model text is
// never treated as completion evidence.
type TaskPlanCompletionGate struct {
	Reader managedagents.SessionTaskPlanReader
}

func (gate TaskPlanCompletionGate) Validate(ctx context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	if gate.Reader == nil {
		return CompletionVerdict{Validator: taskPlanCompletionValidator}, errors.New("task plan completion gate requires a task plan reader")
	}
	plan, err := gate.Reader.GetCurrentSessionTaskPlanContext(ctx, candidate.SessionID)
	if errors.Is(err, managedagents.ErrNotFound) {
		return CompletionVerdict{
			Outcome:   CompletionOutcomePass,
			Validator: taskPlanCompletionValidator,
			Evidence:  map[string]any{"active_plan": false},
		}, nil
	}
	if err != nil {
		return CompletionVerdict{Validator: taskPlanCompletionValidator}, fmt.Errorf("read current task plan: %w", err)
	}
	if plan.Status != managedagents.TaskPlanStatusActive {
		return CompletionVerdict{
			Outcome:   CompletionOutcomePass,
			Validator: taskPlanCompletionValidator,
			Evidence: map[string]any{
				"active_plan": false,
				"plan_id":     plan.ID,
				"plan_status": plan.Status,
			},
		}, nil
	}

	remaining := make([]managedagents.SessionTaskItem, 0, len(plan.Items))
	completedWithEvidence := 0
	for _, item := range plan.Items {
		if item.Status == managedagents.TaskItemStatusCompleted && strings.TrimSpace(item.Evidence) != "" && len(item.EvidenceRefs) > 0 {
			completedWithEvidence++
			continue
		}
		remaining = append(remaining, item)
	}

	evidence := map[string]any{
		"active_plan":             true,
		"plan_id":                 plan.ID,
		"plan_status":             plan.Status,
		"item_count":              len(plan.Items),
		"completed_with_evidence": completedWithEvidence,
		"remaining_count":         len(remaining),
		"ready_to_complete":       len(remaining) == 0,
	}
	if len(remaining) == 0 {
		return CompletionVerdict{
			Outcome:   CompletionOutcomeRetry,
			Validator: taskPlanCompletionValidator,
			Reason:    "all task items have evidence but the task plan is still active",
			Feedback: fmt.Sprintf(
				"Completion is blocked because task plan %s is still active. All items have completion evidence. Call task.complete_plan with plan_id %q, then provide the final response only after that tool succeeds.",
				plan.ID, plan.ID,
			),
			Evidence: evidence,
		}, nil
	}

	remainingState := make([]any, 0, len(remaining))
	var feedback strings.Builder
	fmt.Fprintf(&feedback, "Completion is blocked because task plan %s still has %d unfinished or unverified item(s). Continue the work and update the plan with task.update_items. Completed items must include concrete execution or verification evidence.\n", plan.ID, len(remaining))
	for _, item := range remaining {
		state := map[string]any{"item_id": item.ID, "status": item.Status}
		issue := "not completed"
		if item.Status == managedagents.TaskItemStatusCompleted {
			issue = "missing evidence text or verified tool result reference"
			state["issue"] = "missing_verified_evidence"
		} else {
			state["issue"] = "not_completed"
		}
		remainingState = append(remainingState, state)
		fmt.Fprintf(&feedback, "- item_id=%s status=%s: %s (%s)\n", item.ID, item.Status, item.Description, issue)
	}
	feedback.WriteString("Do not provide a final answer while the plan remains incomplete. If the goal was abandoned, explicitly call task.cancel_plan with a reason.")
	evidence["remaining_items"] = remainingState

	return CompletionVerdict{
		Outcome:   CompletionOutcomeRetry,
		Validator: taskPlanCompletionValidator,
		Reason:    fmt.Sprintf("task plan has %d unfinished or unverified item(s)", len(remaining)),
		Feedback:  feedback.String(),
		Evidence:  evidence,
	}, nil
}
