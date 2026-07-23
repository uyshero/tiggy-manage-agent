package managedagents

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	EvaluationExperimentStatusRunning   = "running"
	EvaluationExperimentStatusCompleted = "completed"
	EvaluationExperimentStatusFailed    = "failed"

	EvaluationExperimentItemStatusQueued    = "queued"
	EvaluationExperimentItemStatusRunning   = "running"
	EvaluationExperimentItemStatusCompleted = "completed"
	EvaluationExperimentItemStatusFailed    = "failed"
)

type EvaluationDatasetItem struct {
	ID             string    `json:"id"`
	DatasetID      string    `json:"dataset_id"`
	ItemIndex      int       `json:"item_index"`
	Prompt         string    `json:"prompt"`
	ExpectedOutput string    `json:"expected_output,omitempty"`
	Tags           []string  `json:"tags"`
	CreatedAt      time.Time `json:"created_at"`
}

type EvaluationDataset struct {
	ID          string                  `json:"id"`
	WorkspaceID string                  `json:"workspace_id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Items       []EvaluationDatasetItem `json:"items"`
	CreatedBy   string                  `json:"created_by,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

type CreateEvaluationDatasetItemInput struct {
	Prompt         string   `json:"prompt"`
	ExpectedOutput string   `json:"expected_output,omitempty"`
	Tags           []string `json:"tags,omitempty"`
}

type CreateEvaluationDatasetInput struct {
	WorkspaceID string                             `json:"workspace_id,omitempty"`
	Name        string                             `json:"name"`
	Description string                             `json:"description,omitempty"`
	Items       []CreateEvaluationDatasetItemInput `json:"items"`
	CreatedBy   string                             `json:"created_by,omitempty"`
}

type EvaluationExperimentItem struct {
	ID             string    `json:"id"`
	ExperimentID   string    `json:"experiment_id"`
	DatasetItemID  string    `json:"dataset_item_id,omitempty"`
	ItemIndex      int       `json:"item_index"`
	Prompt         string    `json:"prompt"`
	ExpectedOutput string    `json:"expected_output,omitempty"`
	Tags           []string  `json:"tags"`
	LeftSessionID  string    `json:"left_session_id,omitempty"`
	LeftTurnID     string    `json:"left_turn_id,omitempty"`
	RightSessionID string    `json:"right_session_id,omitempty"`
	RightTurnID    string    `json:"right_turn_id,omitempty"`
	EvaluationID   string    `json:"evaluation_id,omitempty"`
	Status         string    `json:"status"`
	Conclusion     string    `json:"conclusion,omitempty"`
	LeftAverage    float64   `json:"left_average"`
	RightAverage   float64   `json:"right_average"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type EvaluationExperimentSummary struct {
	Total        int     `json:"total"`
	Queued       int     `json:"queued"`
	Running      int     `json:"running"`
	Completed    int     `json:"completed"`
	Failed       int     `json:"failed"`
	LeftWins     int     `json:"left_wins"`
	RightWins    int     `json:"right_wins"`
	Ties         int     `json:"ties"`
	Inconclusive int     `json:"inconclusive"`
	LeftAverage  float64 `json:"left_average"`
	RightAverage float64 `json:"right_average"`
}

type EvaluationExperiment struct {
	ID                     string                      `json:"id"`
	WorkspaceID            string                      `json:"workspace_id"`
	Name                   string                      `json:"name"`
	DatasetID              string                      `json:"dataset_id,omitempty"`
	RubricID               string                      `json:"rubric_id,omitempty"`
	LeftTemplateSessionID  string                      `json:"left_template_session_id,omitempty"`
	RightTemplateSessionID string                      `json:"right_template_session_id,omitempty"`
	Status                 string                      `json:"status"`
	Summary                EvaluationExperimentSummary `json:"summary"`
	Items                  []EvaluationExperimentItem  `json:"items"`
	CreatedBy              string                      `json:"created_by,omitempty"`
	CreatedAt              time.Time                   `json:"created_at"`
	UpdatedAt              time.Time                   `json:"updated_at"`
	CompletedAt            *time.Time                  `json:"completed_at,omitempty"`
}

type CreateEvaluationExperimentInput struct {
	WorkspaceID            string `json:"workspace_id,omitempty"`
	Name                   string `json:"name"`
	DatasetID              string `json:"dataset_id"`
	RubricID               string `json:"rubric_id"`
	LeftTemplateSessionID  string `json:"left_template_session_id"`
	RightTemplateSessionID string `json:"right_template_session_id"`
	CreatedBy              string `json:"created_by,omitempty"`
}

type UpdateEvaluationExperimentItemInput struct {
	ExperimentID   string
	ItemID         string
	LeftSessionID  string
	LeftTurnID     string
	RightSessionID string
	RightTurnID    string
	EvaluationID   string
	Status         string
	Conclusion     string
	LeftAverage    float64
	RightAverage   float64
	ErrorMessage   string
}

type EvaluationExperimentStore interface {
	CreateEvaluationDatasetContext(ctx context.Context, input CreateEvaluationDatasetInput) (EvaluationDataset, error)
	GetEvaluationDatasetContext(ctx context.Context, id string) (EvaluationDataset, error)
	ListEvaluationDatasetsContext(ctx context.Context, workspaceID string) ([]EvaluationDataset, error)
	CreateEvaluationExperimentContext(ctx context.Context, input CreateEvaluationExperimentInput) (EvaluationExperiment, error)
	GetEvaluationExperimentContext(ctx context.Context, id string) (EvaluationExperiment, error)
	ListEvaluationExperimentsContext(ctx context.Context, workspaceID string, limit int) ([]EvaluationExperiment, error)
	UpdateEvaluationExperimentItemContext(ctx context.Context, input UpdateEvaluationExperimentItemInput) (EvaluationExperiment, error)
}

func ValidateEvaluationDatasetItems(items []CreateEvaluationDatasetItemInput) ([]CreateEvaluationDatasetItemInput, error) {
	if len(items) < 1 || len(items) > 20 {
		return nil, fmt.Errorf("%w: evaluation dataset must contain between 1 and 20 items", ErrInvalid)
	}
	normalized := make([]CreateEvaluationDatasetItemInput, 0, len(items))
	for _, item := range items {
		item.Prompt = strings.TrimSpace(item.Prompt)
		item.ExpectedOutput = strings.TrimSpace(item.ExpectedOutput)
		if item.Prompt == "" || len([]rune(item.Prompt)) > 20000 {
			return nil, fmt.Errorf("%w: dataset item prompt is required and must not exceed 20000 characters", ErrInvalid)
		}
		if len([]rune(item.ExpectedOutput)) > 20000 {
			return nil, fmt.Errorf("%w: dataset item expected output must not exceed 20000 characters", ErrInvalid)
		}
		if len(item.Tags) > 10 {
			return nil, fmt.Errorf("%w: dataset item must not contain more than 10 tags", ErrInvalid)
		}
		tags := make([]string, 0, len(item.Tags))
		seen := map[string]bool{}
		for _, tag := range item.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" || len([]rune(tag)) > 80 {
				return nil, fmt.Errorf("%w: dataset item tags must be non-empty and not exceed 80 characters", ErrInvalid)
			}
			if !seen[tag] {
				seen[tag] = true
				tags = append(tags, tag)
			}
		}
		item.Tags = tags
		normalized = append(normalized, item)
	}
	return normalized, nil
}

func SummarizeEvaluationExperiment(items []EvaluationExperimentItem) EvaluationExperimentSummary {
	summary := EvaluationExperimentSummary{Total: len(items)}
	var scored int
	for _, item := range items {
		switch item.Status {
		case EvaluationExperimentItemStatusQueued:
			summary.Queued++
		case EvaluationExperimentItemStatusRunning:
			summary.Running++
		case EvaluationExperimentItemStatusCompleted:
			summary.Completed++
			scored++
			summary.LeftAverage += item.LeftAverage
			summary.RightAverage += item.RightAverage
			switch item.Conclusion {
			case EvaluationConclusionLeft:
				summary.LeftWins++
			case EvaluationConclusionRight:
				summary.RightWins++
			case EvaluationConclusionTie:
				summary.Ties++
			default:
				summary.Inconclusive++
			}
		case EvaluationExperimentItemStatusFailed:
			summary.Failed++
		}
	}
	if scored > 0 {
		summary.LeftAverage /= float64(scored)
		summary.RightAverage /= float64(scored)
	}
	return summary
}
