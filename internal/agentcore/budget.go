package agentcore

import (
	"fmt"
	"time"

	"tiggy-manage-agent/internal/model"
)

type Budget struct {
	MaxRounds          int       `json:"max_rounds"`
	MaxModelCalls      int       `json:"max_model_calls"`
	MaxToolCalls       int       `json:"max_tool_calls"`
	MaxInputTokens     int64     `json:"max_input_tokens"`
	MaxOutputTokens    int64     `json:"max_output_tokens"`
	MaxReasoningTokens int64     `json:"max_reasoning_tokens"`
	MaxCostMicros      int64     `json:"max_cost_micros"`
	Deadline           time.Time `json:"deadline"`
}

type BudgetState struct {
	Limit      Budget      `json:"limit"`
	ModelCalls int         `json:"model_calls"`
	ToolCalls  int         `json:"tool_calls"`
	Usage      model.Usage `json:"usage"`
}

type BudgetDimension string

const (
	BudgetRounds          BudgetDimension = "rounds"
	BudgetModelCalls      BudgetDimension = "model_calls"
	BudgetToolCalls       BudgetDimension = "tool_calls"
	BudgetInputTokens     BudgetDimension = "input_tokens"
	BudgetOutputTokens    BudgetDimension = "output_tokens"
	BudgetReasoningTokens BudgetDimension = "reasoning_tokens"
	BudgetCost            BudgetDimension = "cost_micros"
	BudgetDeadline        BudgetDimension = "deadline"
)

type BudgetExceededError struct {
	Dimension BudgetDimension
	Limit     int64
	Consumed  int64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("agent budget exhausted for %s: consumed=%d limit=%d", e.Dimension, e.Consumed, e.Limit)
}

func NewBudgetState(limit Budget) BudgetState {
	return BudgetState{Limit: limit}
}

func (s BudgetState) Validate() error {
	if s.Limit.MaxRounds <= 0 || s.Limit.MaxModelCalls <= 0 || s.Limit.MaxToolCalls <= 0 {
		return fmt.Errorf("round, model call, and tool call budgets must be positive")
	}
	if s.Limit.MaxInputTokens <= 0 || s.Limit.MaxOutputTokens <= 0 || s.Limit.MaxReasoningTokens <= 0 || s.Limit.MaxCostMicros <= 0 {
		return fmt.Errorf("token and cost budgets must be positive")
	}
	if s.Limit.Deadline.IsZero() {
		return fmt.Errorf("budget deadline is required")
	}
	if s.ModelCalls < 0 || s.ToolCalls < 0 {
		return fmt.Errorf("budget counters cannot be negative")
	}
	if err := s.Usage.Validate(); err != nil {
		return err
	}
	return nil
}

func (s BudgetState) CheckBeforeModel(now time.Time, round int) error {
	if !now.Before(s.Limit.Deadline) {
		return &BudgetExceededError{Dimension: BudgetDeadline, Limit: s.Limit.Deadline.UnixMilli(), Consumed: now.UnixMilli()}
	}
	if round >= s.Limit.MaxRounds {
		return &BudgetExceededError{Dimension: BudgetRounds, Limit: int64(s.Limit.MaxRounds), Consumed: int64(round)}
	}
	if s.ModelCalls >= s.Limit.MaxModelCalls {
		return &BudgetExceededError{Dimension: BudgetModelCalls, Limit: int64(s.Limit.MaxModelCalls), Consumed: int64(s.ModelCalls)}
	}
	return s.checkUsage()
}

func (s BudgetState) CheckBeforeTools(count int) error {
	if count < 0 || s.ToolCalls+count > s.Limit.MaxToolCalls {
		return &BudgetExceededError{Dimension: BudgetToolCalls, Limit: int64(s.Limit.MaxToolCalls), Consumed: int64(s.ToolCalls + count)}
	}
	return s.checkUsage()
}

// CheckAfterUsage rejects a completed provider call that crossed a hard
// metered limit. Equality is allowed because the work has already completed;
// the before-call checks prevent any later metered work.
func (s BudgetState) CheckAfterUsage() error {
	return s.checkUsageLimit(false)
}

func (s BudgetState) checkUsage() error {
	return s.checkUsageLimit(true)
}

func (s BudgetState) checkUsageLimit(atLimit bool) error {
	checks := []struct {
		dimension BudgetDimension
		limit     int64
		consumed  int64
	}{
		{BudgetInputTokens, s.Limit.MaxInputTokens, s.Usage.InputTokens},
		{BudgetOutputTokens, s.Limit.MaxOutputTokens, s.Usage.OutputTokens},
		{BudgetReasoningTokens, s.Limit.MaxReasoningTokens, s.Usage.ReasoningTokens},
		{BudgetCost, s.Limit.MaxCostMicros, s.Usage.CostMicros},
	}
	for _, check := range checks {
		if check.consumed > check.limit || (atLimit && check.consumed == check.limit) {
			return &BudgetExceededError{Dimension: check.dimension, Limit: check.limit, Consumed: check.consumed}
		}
	}
	return nil
}

func (s *BudgetState) ReserveModelCall() {
	s.ModelCalls++
}

func (s *BudgetState) ReserveToolCalls(count int) {
	s.ToolCalls += count
}

func (s *BudgetState) AddUsage(usage model.Usage) {
	s.Usage = s.Usage.Add(usage)
}
