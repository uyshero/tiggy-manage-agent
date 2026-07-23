package managedagents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	EvaluationConclusionLeft         = "left"
	EvaluationConclusionRight        = "right"
	EvaluationConclusionTie          = "tie"
	EvaluationConclusionInconclusive = "inconclusive"
	EvaluationTypeManual             = "manual"
	EvaluationTypeAuto               = "auto"
)

type EvaluationCriterion struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type EvaluationRubric struct {
	ID          string                `json:"id"`
	WorkspaceID string                `json:"workspace_id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Criteria    []EvaluationCriterion `json:"criteria"`
	Revision    int64                 `json:"revision"`
	CreatedBy   string                `json:"created_by,omitempty"`
	UpdatedBy   string                `json:"updated_by,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

type CreateEvaluationRubricInput struct {
	WorkspaceID string                `json:"workspace_id,omitempty"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Criteria    []EvaluationCriterion `json:"criteria"`
	CreatedBy   string                `json:"created_by,omitempty"`
}

type EvaluationCriterionScore struct {
	CriterionID string `json:"criterion_id"`
	LeftScore   int    `json:"left_score"`
	RightScore  int    `json:"right_score"`
}

type EvaluationRubricSnapshot struct {
	RubricID    string                `json:"rubric_id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Revision    int64                 `json:"revision"`
	Criteria    []EvaluationCriterion `json:"criteria"`
}

type RunEvaluation struct {
	ID             string                     `json:"id"`
	WorkspaceID    string                     `json:"workspace_id"`
	LeftSessionID  string                     `json:"left_session_id"`
	LeftTurnID     string                     `json:"left_turn_id"`
	RightSessionID string                     `json:"right_session_id"`
	RightTurnID    string                     `json:"right_turn_id"`
	RubricID       string                     `json:"rubric_id,omitempty"`
	RubricSnapshot EvaluationRubricSnapshot   `json:"rubric_snapshot"`
	Scores         []EvaluationCriterionScore `json:"scores"`
	Conclusion     string                     `json:"conclusion"`
	Notes          string                     `json:"notes,omitempty"`
	EvaluationType string                     `json:"evaluation_type"`
	JudgeProvider  string                     `json:"judge_provider,omitempty"`
	JudgeModel     string                     `json:"judge_model,omitempty"`
	JudgeReasoning string                     `json:"judge_reasoning,omitempty"`
	CreatedBy      string                     `json:"created_by,omitempty"`
	CreatedAt      time.Time                  `json:"created_at"`
}

type CreateRunEvaluationInput struct {
	LeftSessionID  string                     `json:"left_session_id"`
	LeftTurnID     string                     `json:"left_turn_id"`
	RightSessionID string                     `json:"right_session_id"`
	RightTurnID    string                     `json:"right_turn_id"`
	RubricID       string                     `json:"rubric_id"`
	Scores         []EvaluationCriterionScore `json:"scores"`
	Conclusion     string                     `json:"conclusion"`
	Notes          string                     `json:"notes,omitempty"`
	CreatedBy      string                     `json:"created_by,omitempty"`
	EvaluationType string                     `json:"-"`
	JudgeProvider  string                     `json:"-"`
	JudgeModel     string                     `json:"-"`
	JudgeReasoning string                     `json:"-"`
}

type ListRunEvaluationsInput struct {
	LeftSessionID  string `json:"left_session_id"`
	LeftTurnID     string `json:"left_turn_id"`
	RightSessionID string `json:"right_session_id"`
	RightTurnID    string `json:"right_turn_id"`
	Limit          int    `json:"limit,omitempty"`
}

type EvaluationStore interface {
	CreateEvaluationRubricContext(ctx context.Context, input CreateEvaluationRubricInput) (EvaluationRubric, error)
	GetEvaluationRubricContext(ctx context.Context, id string) (EvaluationRubric, error)
	ListEvaluationRubricsContext(ctx context.Context, workspaceID string) ([]EvaluationRubric, error)
	CreateRunEvaluationContext(ctx context.Context, input CreateRunEvaluationInput) (RunEvaluation, error)
	ListRunEvaluationsContext(ctx context.Context, input ListRunEvaluationsInput) ([]RunEvaluation, error)
}

func ValidateEvaluationCriteria(criteria []EvaluationCriterion) ([]EvaluationCriterion, error) {
	if len(criteria) < 2 || len(criteria) > 6 {
		return nil, fmt.Errorf("%w: rubric must contain between 2 and 6 criteria", ErrInvalid)
	}
	normalized := make([]EvaluationCriterion, 0, len(criteria))
	seen := map[string]bool{}
	for _, criterion := range criteria {
		criterion.ID = strings.TrimSpace(criterion.ID)
		criterion.Name = strings.TrimSpace(criterion.Name)
		criterion.Description = strings.TrimSpace(criterion.Description)
		if criterion.ID == "" || len(criterion.ID) > 100 {
			return nil, fmt.Errorf("%w: criterion id is required and must not exceed 100 characters", ErrInvalid)
		}
		if seen[criterion.ID] {
			return nil, fmt.Errorf("%w: criterion ids must be unique", ErrInvalid)
		}
		if criterion.Name == "" || len(criterion.Name) > 160 {
			return nil, fmt.Errorf("%w: criterion name is required and must not exceed 160 characters", ErrInvalid)
		}
		if len(criterion.Description) > 1000 {
			return nil, fmt.Errorf("%w: criterion description must not exceed 1000 characters", ErrInvalid)
		}
		seen[criterion.ID] = true
		normalized = append(normalized, criterion)
	}
	return normalized, nil
}

func ValidateEvaluationScores(criteria []EvaluationCriterion, scores []EvaluationCriterionScore) ([]EvaluationCriterionScore, error) {
	if len(scores) != len(criteria) {
		return nil, fmt.Errorf("%w: scores must cover every rubric criterion", ErrInvalid)
	}
	expected := make(map[string]bool, len(criteria))
	for _, criterion := range criteria {
		expected[criterion.ID] = true
	}
	normalized := make([]EvaluationCriterionScore, 0, len(scores))
	seen := map[string]bool{}
	for _, score := range scores {
		score.CriterionID = strings.TrimSpace(score.CriterionID)
		if !expected[score.CriterionID] || seen[score.CriterionID] {
			return nil, fmt.Errorf("%w: scores must reference each criterion exactly once", ErrInvalid)
		}
		if score.LeftScore < 1 || score.LeftScore > 5 || score.RightScore < 1 || score.RightScore > 5 {
			return nil, fmt.Errorf("%w: evaluation scores must be between 1 and 5", ErrInvalid)
		}
		seen[score.CriterionID] = true
		normalized = append(normalized, score)
	}
	return normalized, nil
}

func ValidateEvaluationConclusion(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case EvaluationConclusionLeft, EvaluationConclusionRight, EvaluationConclusionTie, EvaluationConclusionInconclusive:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unsupported evaluation conclusion", ErrInvalid)
	}
}

func decodeEvaluationRubric(criteriaJSON []byte, rubric *EvaluationRubric) error {
	if err := json.Unmarshal(criteriaJSON, &rubric.Criteria); err != nil {
		return fmt.Errorf("decode evaluation rubric criteria: %w", err)
	}
	return nil
}
