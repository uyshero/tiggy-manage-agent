package agentcore

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"tiggy-manage-agent/internal/model"
)

const StateVersion = "tma.agent_loop_state.v1"

type Phase string

const (
	PhasePreparing            Phase = "preparing"
	PhaseAwaitingModel        Phase = "awaiting_model"
	PhasePreflightingTools    Phase = "preflighting_tools"
	PhasePaused               Phase = "paused"
	PhaseExecutingTools       Phase = "executing_tools"
	PhaseValidatingCompletion Phase = "validating_completion"
	PhaseCompleted            Phase = "completed"
	PhaseFailed               Phase = "failed"
	PhaseCanceled             Phase = "canceled"
)

type State struct {
	Version  string `json:"version"`
	Revision int64  `json:"revision"`
	Phase    Phase  `json:"phase"`

	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`

	Messages []model.Message `json:"messages"`

	PendingModel      *PendingModelAttempt `json:"pending_model,omitempty"`
	PendingCompaction *PendingCompaction   `json:"pending_compaction,omitempty"`
	PendingToolBatch  *ToolBatchPlan       `json:"pending_tool_batch,omitempty"`
	PendingCompletion *PendingCompletion   `json:"pending_completion,omitempty"`
	Pause             *PauseState          `json:"pause,omitempty"`
	Failure           *Failure             `json:"failure,omitempty"`

	Round              int `json:"round"`
	ModelAttempts      int `json:"model_attempts"`
	CompactionAttempts int `json:"compaction_attempts"`
	ToolCalls          int `json:"tool_calls"`
	CompletionAttempts int `json:"completion_attempts"`

	Budget        BudgetState            `json:"budget"`
	Usage         model.Usage            `json:"usage"`
	ControlCursor int64                  `json:"control_cursor"`
	ActiveTools   []string               `json:"active_tools,omitempty"`
	ToolJournal   []ToolCallJournalEntry `json:"tool_journal,omitempty"`

	Context      ContextState               `json:"context"`
	FeatureState map[string]json.RawMessage `json:"feature_state,omitempty"`
}

type PendingModelAttempt struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Status string `json:"status"`
}

type PendingCompaction struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
}

type PendingCompletion struct {
	MessageID string `json:"message_id"`
	Attempt   int    `json:"attempt"`
}

type ContextState struct {
	SummarySourceUntilSeq int64  `json:"summary_source_until_seq,omitempty"`
	SummaryRevision       string `json:"summary_revision,omitempty"`
	EstimatedInputTokens  int64  `json:"estimated_input_tokens,omitempty"`
	CompactionCount       int    `json:"compaction_count,omitempty"`
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

func NewState(sessionID, turnID string, budget Budget) State {
	return State{
		Version:   StateVersion,
		Phase:     PhasePreparing,
		SessionID: strings.TrimSpace(sessionID),
		TurnID:    strings.TrimSpace(turnID),
		Budget:    NewBudgetState(budget),
	}
}

func ValidatePhaseTransition(from, to Phase) error {
	if from == to && (from == PhaseAwaitingModel || from == PhaseExecutingTools || from == PhaseValidatingCompletion) {
		return nil
	}
	allowed := false
	switch from {
	case PhasePreparing:
		allowed = to == PhaseAwaitingModel
	case PhaseAwaitingModel:
		allowed = to == PhasePreflightingTools || to == PhaseValidatingCompletion
	case PhasePreflightingTools:
		allowed = to == PhasePaused || to == PhaseExecutingTools
	case PhasePaused:
		allowed = to == PhaseExecutingTools
	case PhaseExecutingTools:
		allowed = to == PhaseAwaitingModel
	case PhaseValidatingCompletion:
		allowed = to == PhaseAwaitingModel || to == PhaseCompleted
	}
	if to == PhaseFailed || to == PhaseCanceled {
		allowed = from != PhaseCompleted && from != PhaseFailed && from != PhaseCanceled
	}
	if !allowed {
		return fmt.Errorf("unsupported loop phase transition %q -> %q", from, to)
	}
	return nil
}

func (s State) Validate() error {
	if s.Version != StateVersion {
		return fmt.Errorf("unsupported loop state version %q", s.Version)
	}
	if s.Revision < 0 {
		return fmt.Errorf("state revision cannot be negative")
	}
	if strings.TrimSpace(s.SessionID) == "" || strings.TrimSpace(s.TurnID) == "" {
		return fmt.Errorf("session id and turn id are required")
	}
	if err := s.Budget.Validate(); err != nil {
		return err
	}
	if err := s.Usage.Validate(); err != nil {
		return err
	}
	if s.Round < 0 || s.ModelAttempts < 0 || s.CompactionAttempts < 0 || s.ToolCalls < 0 || s.CompletionAttempts < 0 || s.ControlCursor < 0 || s.Context.CompactionCount < 0 {
		return fmt.Errorf("state counters cannot be negative")
	}
	if s.ModelAttempts+s.CompactionAttempts != s.Budget.ModelCalls || s.ToolCalls != s.Budget.ToolCalls || s.Usage != s.Budget.Usage {
		return fmt.Errorf("state counters and usage must match budget accounting")
	}
	if err := validateMessages(s.Messages); err != nil {
		return err
	}
	if err := validateActiveTools(s.ActiveTools); err != nil {
		return err
	}
	if err := validateToolJournal(s.Messages, s.ToolJournal); err != nil {
		return err
	}
	for name, raw := range s.FeatureState {
		if strings.TrimSpace(name) == "" || len(raw) == 0 || !json.Valid(raw) {
			return fmt.Errorf("feature state %q must contain valid JSON", name)
		}
	}
	if s.Phase != PhaseFailed && s.Phase != PhaseCanceled && s.Failure != nil {
		return fmt.Errorf("%s state cannot contain failure details", s.Phase)
	}

	switch s.Phase {
	case PhasePreparing:
		return requireNoPending(s)
	case PhaseAwaitingModel:
		if s.PendingToolBatch != nil || s.PendingCompletion != nil || s.Pause != nil || s.Failure != nil {
			return fmt.Errorf("awaiting_model state contains incompatible pending data")
		}
		if s.PendingModel != nil {
			if strings.TrimSpace(s.PendingModel.ID) == "" || s.PendingModel.Number <= 0 || s.PendingModel.Status != "running" {
				return fmt.Errorf("pending model attempt is invalid")
			}
			if s.PendingModel.Number != s.ModelAttempts {
				return fmt.Errorf("pending model attempt number does not match model attempts")
			}
		}
		if s.PendingCompaction != nil {
			if strings.TrimSpace(s.PendingCompaction.ID) == "" || s.PendingCompaction.Number <= 0 || s.PendingCompaction.Number != s.CompactionAttempts {
				return fmt.Errorf("pending compaction attempt is invalid")
			}
			if s.PendingModel != nil {
				return fmt.Errorf("awaiting_model state cannot contain model and compaction attempts together")
			}
		}
	case PhasePreflightingTools, PhaseExecutingTools:
		if s.PendingToolBatch == nil {
			return fmt.Errorf("%s state requires a pending tool batch", s.Phase)
		}
		if err := s.PendingToolBatch.Validate(); err != nil {
			return err
		}
		if s.PendingModel != nil || s.PendingCompaction != nil || s.PendingCompletion != nil || s.Pause != nil || s.Failure != nil {
			return fmt.Errorf("%s state contains incompatible pending data", s.Phase)
		}
	case PhasePaused:
		if s.PendingToolBatch == nil || s.Pause == nil {
			return fmt.Errorf("paused state requires a tool batch and pause data")
		}
		if err := s.PendingToolBatch.Validate(); err != nil {
			return err
		}
		if err := s.Pause.Validate(); err != nil {
			return err
		}
		if err := validatePauseMatchesPlan(*s.Pause, *s.PendingToolBatch); err != nil {
			return err
		}
		if s.PendingModel != nil || s.PendingCompaction != nil || s.PendingCompletion != nil || s.Failure != nil {
			return fmt.Errorf("paused state contains incompatible pending data")
		}
	case PhaseValidatingCompletion:
		if s.PendingCompletion == nil || strings.TrimSpace(s.PendingCompletion.MessageID) == "" || s.PendingCompletion.Attempt < 0 {
			return fmt.Errorf("validating_completion state requires a valid candidate")
		}
		if _, ok := s.messageByID(s.PendingCompletion.MessageID); !ok {
			return fmt.Errorf("completion candidate message %q does not exist", s.PendingCompletion.MessageID)
		}
		if s.PendingModel != nil || s.PendingCompaction != nil || s.PendingToolBatch != nil || s.Pause != nil || s.Failure != nil {
			return fmt.Errorf("validating_completion state contains incompatible pending data")
		}
	case PhaseCompleted:
		if err := requireNoPending(s); err != nil {
			return err
		}
		if s.Failure != nil {
			return fmt.Errorf("completed state cannot contain failure")
		}
	case PhaseFailed, PhaseCanceled:
		if s.PendingModel != nil || s.PendingCompaction != nil || s.PendingToolBatch != nil || s.PendingCompletion != nil || s.Pause != nil {
			return fmt.Errorf("terminal state contains pending work")
		}
		if s.Failure == nil || strings.TrimSpace(s.Failure.Code) == "" || strings.TrimSpace(s.Failure.Message) == "" {
			return fmt.Errorf("%s state requires failure details", s.Phase)
		}
	default:
		return fmt.Errorf("unsupported loop phase %q", s.Phase)
	}
	return nil
}

func requireNoPending(s State) error {
	if s.PendingModel != nil || s.PendingCompaction != nil || s.PendingToolBatch != nil || s.PendingCompletion != nil || s.Pause != nil {
		return fmt.Errorf("%s state contains pending work", s.Phase)
	}
	return nil
}

func validateMessages(messages []model.Message) error {
	messageIDs := make(map[string]struct{}, len(messages))
	toolCalls := map[string]string{}
	toolResults := map[string]struct{}{}
	for index, message := range messages {
		if err := message.Validate(); err != nil {
			return fmt.Errorf("message %d: %w", index, err)
		}
		if _, exists := messageIDs[message.ID]; exists {
			return fmt.Errorf("duplicate message id %q", message.ID)
		}
		messageIDs[message.ID] = struct{}{}
		for _, content := range message.Content {
			if content.ToolCall != nil {
				if _, exists := toolCalls[content.ToolCall.ID]; exists {
					return fmt.Errorf("duplicate tool call id %q", content.ToolCall.ID)
				}
				toolCalls[content.ToolCall.ID] = content.ToolCall.Name
			}
			if content.ToolResult != nil {
				if _, exists := toolResults[content.ToolResult.CallID]; exists {
					return fmt.Errorf("duplicate tool result for call %q", content.ToolResult.CallID)
				}
				toolResults[content.ToolResult.CallID] = struct{}{}
			}
		}
	}
	for callID := range toolResults {
		if _, exists := toolCalls[callID]; !exists {
			return fmt.Errorf("tool result references unknown call %q", callID)
		}
	}
	return nil
}

func validateActiveTools(tools []string) error {
	for index, name := range tools {
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
			return fmt.Errorf("active tool %d is invalid", index)
		}
		if index > 0 && tools[index-1] >= name {
			return fmt.Errorf("active tools must be unique and sorted")
		}
	}
	return nil
}

func (s State) messageByID(id string) (model.Message, bool) {
	for _, message := range s.Messages {
		if message.ID == id {
			return message, true
		}
	}
	return model.Message{}, false
}

func (s State) Clone() State {
	cloned := s
	cloned.Messages = model.CloneMessages(s.Messages)
	cloned.ActiveTools = append([]string(nil), s.ActiveTools...)
	cloned.ToolJournal = cloneToolCallJournal(s.ToolJournal)
	cloned.PendingModel = clonePendingModel(s.PendingModel)
	cloned.PendingCompaction = clonePendingCompaction(s.PendingCompaction)
	cloned.PendingToolBatch = cloneToolBatchPlan(s.PendingToolBatch)
	cloned.PendingCompletion = clonePendingCompletion(s.PendingCompletion)
	cloned.Pause = clonePauseState(s.Pause)
	cloned.Failure = cloneFailure(s.Failure)
	if s.FeatureState != nil {
		cloned.FeatureState = make(map[string]json.RawMessage, len(s.FeatureState))
		for name, raw := range s.FeatureState {
			cloned.FeatureState[name] = append(json.RawMessage(nil), raw...)
		}
	}
	return cloned
}

func validateToolJournal(messages []model.Message, journal []ToolCallJournalEntry) error {
	calls := make(map[string]string)
	for _, message := range messages {
		for _, content := range message.Content {
			if content.ToolCall != nil {
				calls[content.ToolCall.ID] = content.ToolCall.Name
			}
		}
	}
	seen := make(map[string]struct{}, len(journal))
	for index, entry := range journal {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("tool journal entry %d: %w", index, err)
		}
		if name, ok := calls[entry.CallID]; ok && name != entry.Name {
			return fmt.Errorf("tool journal entry %q references an unknown call", entry.CallID)
		}
		if _, ok := calls[entry.CallID]; !ok && entry.Status == ToolCallStarted {
			return fmt.Errorf("started tool journal entry %q references an unknown call", entry.CallID)
		}
		if _, exists := seen[entry.CallID]; exists {
			return fmt.Errorf("duplicate tool journal entry for call %q", entry.CallID)
		}
		seen[entry.CallID] = struct{}{}
	}
	return nil
}

func (s *State) NormalizeActiveTools() {
	cleaned := make([]string, 0, len(s.ActiveTools))
	for _, name := range s.ActiveTools {
		if value := strings.TrimSpace(name); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	slices.Sort(cleaned)
	s.ActiveTools = slices.Compact(cleaned)
}

func clonePendingModel(value *PendingModelAttempt) *PendingModelAttempt {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePendingCompaction(value *PendingCompaction) *PendingCompaction {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePendingCompletion(value *PendingCompletion) *PendingCompletion {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFailure(value *Failure) *Failure {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
