package managedagents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	AgentScheduleApprovalApproveForMe = "approve_for_me"
	AgentScheduleApprovalFullAccess   = "full_access"
	AgentScheduleSessionNew           = "new_session"
	AgentScheduleSessionExisting      = "existing_session"

	AgentScheduleRunPending        = "pending"
	AgentScheduleRunWaitingSession = "waiting_session"
	AgentScheduleRunDispatching    = "dispatching"
	AgentScheduleRunDispatched     = "dispatched"
	AgentScheduleRunFailed         = "failed"
)

var agentScheduleCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type AgentSchedule struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	OwnerID         string     `json:"owner_id"`
	AgentID         string     `json:"agent_id"`
	EnvironmentID   string     `json:"environment_id"`
	SessionMode     string     `json:"session_mode"`
	TargetSessionID string     `json:"target_session_id,omitempty"`
	ApprovalMode    string     `json:"approval_mode"`
	Name            string     `json:"name"`
	Prompt          string     `json:"prompt"`
	CronExpression  string     `json:"cron_expression"`
	Timezone        string     `json:"timezone"`
	Enabled         bool       `json:"enabled"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastSessionID   string     `json:"last_session_id,omitempty"`
	LastRunStatus   string     `json:"last_run_status,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CreateAgentScheduleInput struct {
	WorkspaceID     string `json:"workspace_id,omitempty"`
	OwnerID         string `json:"owner_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	SessionMode     string `json:"session_mode,omitempty"`
	TargetSessionID string `json:"target_session_id,omitempty"`
	ApprovalMode    string `json:"approval_mode,omitempty"`
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	CronExpression  string `json:"cron_expression"`
	Timezone        string `json:"timezone,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
	CreatedBy       string `json:"created_by,omitempty"`
}

type UpdateAgentScheduleInput struct {
	Name            *string `json:"name,omitempty"`
	Prompt          *string `json:"prompt,omitempty"`
	CronExpression  *string `json:"cron_expression,omitempty"`
	Timezone        *string `json:"timezone,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
	SessionMode     *string `json:"session_mode,omitempty"`
	TargetSessionID *string `json:"target_session_id,omitempty"`
	ApprovalMode    *string `json:"approval_mode,omitempty"`
}

type AgentScheduleInvocation struct {
	RunID        string        `json:"run_id"`
	ScheduledFor time.Time     `json:"scheduled_for"`
	Schedule     AgentSchedule `json:"schedule"`
}

type CompleteAgentScheduleRunInput struct {
	RunID      string
	ScheduleID string
	SessionID  string
	Status     string
	Error      string
}

type AgentScheduleStore interface {
	EnsureAgentScheduleEnvironment(ctx context.Context, workspaceID string) (Environment, error)
	CreateAgentSchedule(ctx context.Context, input CreateAgentScheduleInput) (AgentSchedule, error)
	GetAgentSchedule(ctx context.Context, id string) (AgentSchedule, error)
	ListAgentSchedules(ctx context.Context, agentID string) ([]AgentSchedule, error)
	UpdateAgentSchedule(ctx context.Context, id string, input UpdateAgentScheduleInput) (AgentSchedule, error)
	DeleteAgentSchedule(ctx context.Context, id string) error
	ClaimDueAgentSchedules(ctx context.Context, now time.Time, limit int) ([]AgentScheduleInvocation, error)
	ClaimRunnableAgentScheduleRuns(ctx context.Context, now time.Time, limit int, leaseDuration time.Duration) ([]AgentScheduleInvocation, error)
	ClaimAgentScheduleRun(ctx context.Context, runID string, now time.Time, leaseDuration time.Duration) (AgentScheduleInvocation, bool, error)
	StartAgentScheduleNow(ctx context.Context, id string, now time.Time) (AgentScheduleInvocation, error)
	DeferAgentScheduleRun(ctx context.Context, runID string, scheduleID string) error
	ReconcileInvalidAgentScheduleRuns(ctx context.Context, now time.Time) (int, error)
	CompleteAgentScheduleRun(ctx context.Context, input CompleteAgentScheduleRunInput) error
}

func NormalizeAgentScheduleModes(sessionMode, targetSessionID, approvalMode string) (string, string, string, error) {
	sessionMode = strings.ToLower(strings.TrimSpace(sessionMode))
	if sessionMode == "" {
		sessionMode = AgentScheduleSessionNew
	}
	targetSessionID = strings.TrimSpace(targetSessionID)
	switch sessionMode {
	case AgentScheduleSessionNew:
		targetSessionID = ""
	case AgentScheduleSessionExisting:
		if targetSessionID == "" {
			return "", "", "", fmt.Errorf("%w: target_session_id is required for existing_session mode", ErrInvalid)
		}
	default:
		return "", "", "", fmt.Errorf("%w: unsupported session_mode %q", ErrInvalid, sessionMode)
	}
	approvalMode = strings.ToLower(strings.TrimSpace(approvalMode))
	if approvalMode == "" {
		approvalMode = AgentScheduleApprovalApproveForMe
	}
	if approvalMode != AgentScheduleApprovalApproveForMe && approvalMode != AgentScheduleApprovalFullAccess {
		return "", "", "", fmt.Errorf("%w: unsupported approval_mode %q", ErrInvalid, approvalMode)
	}
	return sessionMode, targetSessionID, approvalMode, nil
}

func NormalizeAgentSchedule(cronExpression, timezone string, from time.Time) (string, string, time.Time, error) {
	cronExpression = strings.TrimSpace(cronExpression)
	if cronExpression == "" {
		return "", "", time.Time{}, fmt.Errorf("%w: cron_expression is required", ErrInvalid)
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("%w: invalid timezone %q", ErrInvalid, timezone)
	}
	schedule, err := agentScheduleCronParser.Parse(cronExpression)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("%w: invalid cron_expression: %v", ErrInvalid, err)
	}
	next := schedule.Next(from.In(location)).UTC()
	if next.IsZero() {
		return "", "", time.Time{}, fmt.Errorf("%w: cron_expression has no next occurrence", ErrInvalid)
	}
	return cronExpression, timezone, next, nil
}
