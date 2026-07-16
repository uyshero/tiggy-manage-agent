package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

const AgentIdentifier = NamespaceAgent

type AgentToolError struct {
	Type    string
	Message string
	State   any
}

func (e AgentToolError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if strings.TrimSpace(e.Type) != "" {
		return e.Type
	}
	return "agent tool error"
}

type AgentToolService interface {
	Spawn(context.Context, AgentSpawnRequest) (AgentSpawnResponse, error)
	SendMessage(context.Context, AgentSendMessageRequest) (AgentSendMessageResponse, error)
	GetSession(context.Context, AgentSessionRequest) (AgentSessionResponse, error)
	Wait(context.Context, AgentWaitRequest) (AgentWaitResponse, error)
	CollectResult(context.Context, AgentCollectResultRequest) (AgentCollectResultResponse, error)
	ListEvents(context.Context, AgentListEventsRequest) (AgentListEventsResponse, error)
	StreamEvents(context.Context, AgentStreamEventsRequest) (AgentStreamEventsResponse, error)
	ApproveTool(context.Context, AgentInterventionDecisionRequest) (AgentInterventionDecisionResponse, error)
	RejectTool(context.Context, AgentInterventionDecisionRequest) (AgentInterventionDecisionResponse, error)
	ArchiveSession(context.Context, AgentArchiveSessionRequest) (AgentArchiveSessionResponse, error)
	CancelStart(context.Context, AgentCancelStartRequest) (AgentCancelStartResponse, error)
	CreateTaskGroup(context.Context, AgentTaskGroupCreateRequest) (AgentTaskGroupCreateResponse, error)
	GetTaskGroup(context.Context, AgentTaskGroupRequest) (AgentTaskGroupResponse, error)
	WaitTaskGroup(context.Context, AgentTaskGroupWaitRequest) (AgentTaskGroupWaitResponse, error)
	CollectTaskGroup(context.Context, AgentTaskGroupCollectRequest) (AgentTaskGroupCollectResponse, error)
	CancelTaskGroup(context.Context, AgentTaskGroupCancelRequest) (AgentTaskGroupCancelResponse, error)
	RetryTaskGroupItem(context.Context, AgentTaskGroupRetryItemRequest) (AgentTaskGroupRetryResponse, error)
	RetryTaskGroup(context.Context, AgentTaskGroupRetryRequest) (AgentTaskGroupRetryResponse, error)
}

type AgentSpawnRequest struct {
	ParentSessionID string `json:"-"`
	ParentTurnID    string `json:"-"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Agent           string `json:"agent,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	Title           string `json:"title,omitempty"`
	Message         string `json:"message,omitempty"`
}

type AgentSpawnResponse struct {
	Session       managedagents.Session               `json:"session"`
	Started       bool                                `json:"started"`
	CreatedBy     string                              `json:"created_by,omitempty"`
	InitialEvents []managedagents.Event               `json:"initial_events,omitempty"`
	Queued        bool                                `json:"queued,omitempty"`
	QueueRequest  *managedagents.SubagentStartRequest `json:"queue_request,omitempty"`
}

type AgentSendMessageRequest struct {
	ParentSessionID string `json:"-"`
	ParentTurnID    string `json:"-"`
	SessionID       string `json:"session_id"`
	Message         string `json:"message"`
}

type AgentSendMessageResponse struct {
	SessionID    string                              `json:"session_id"`
	Started      bool                                `json:"started"`
	Events       []managedagents.Event               `json:"events,omitempty"`
	Queued       bool                                `json:"queued,omitempty"`
	QueueRequest *managedagents.SubagentStartRequest `json:"queue_request,omitempty"`
}

type AgentSessionRequest struct {
	ParentSessionID string `json:"-"`
	SessionID       string `json:"session_id"`
}

type AgentSessionResponse struct {
	Session              managedagents.Session               `json:"session"`
	Status               string                              `json:"status"`
	PendingInterventions []managedagents.SessionIntervention `json:"pending_interventions,omitempty"`
	QueueRequest         *managedagents.SubagentStartRequest `json:"queue_request,omitempty"`
}

type AgentWaitRequest struct {
	ParentSessionID         string `json:"-"`
	SessionID               string `json:"session_id"`
	TimeoutSeconds          int    `json:"timeout_seconds,omitempty"`
	ReturnOnWaitingApproval *bool  `json:"return_on_waiting_approval,omitempty"`
}

type AgentWaitResponse struct {
	Session              managedagents.Session               `json:"session"`
	Status               string                              `json:"status"`
	LastTurnStatus       string                              `json:"last_turn_status,omitempty"`
	Reason               string                              `json:"reason,omitempty"`
	TimedOut             bool                                `json:"timed_out,omitempty"`
	PendingInterventions []managedagents.SessionIntervention `json:"pending_interventions,omitempty"`
	QueueRequest         *managedagents.SubagentStartRequest `json:"queue_request,omitempty"`
}

type AgentCollectResultRequest struct {
	ParentSessionID  string `json:"-"`
	SessionID        string `json:"session_id"`
	IncludeArtifacts *bool  `json:"include_artifacts,omitempty"`
}

type AgentCollectResultResponse struct {
	Session              managedagents.Session               `json:"session"`
	Status               string                              `json:"status"`
	LastTurnStatus       string                              `json:"last_turn_status,omitempty"`
	Reason               string                              `json:"reason,omitempty"`
	AgentMessage         *managedagents.Event                `json:"agent_message,omitempty"`
	AgentText            string                              `json:"agent_text,omitempty"`
	Artifacts            []managedagents.SessionArtifact     `json:"artifacts,omitempty"`
	PendingInterventions []managedagents.SessionIntervention `json:"pending_interventions,omitempty"`
	EventCount           int                                 `json:"event_count"`
	QueueRequest         *managedagents.SubagentStartRequest `json:"queue_request,omitempty"`
}

type AgentListEventsRequest struct {
	ParentSessionID string `json:"-"`
	SessionID       string `json:"session_id"`
	AfterSeq        int64  `json:"after_seq,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type AgentListEventsResponse struct {
	SessionID string                `json:"session_id"`
	Events    []managedagents.Event `json:"events"`
}

type AgentStreamEventsRequest struct {
	ParentSessionID string `json:"-"`
	SessionID       string `json:"session_id"`
	AfterSeq        int64  `json:"after_seq,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	WaitSeconds     int    `json:"wait_seconds,omitempty"`
}

type AgentStreamEventsResponse struct {
	SessionID string                `json:"session_id"`
	Events    []managedagents.Event `json:"events"`
	TimedOut  bool                  `json:"timed_out,omitempty"`
}

type AgentInterventionDecisionRequest struct {
	ParentSessionID string `json:"-"`
	SessionID       string `json:"session_id"`
	TurnID          string `json:"turn_id"`
	CallID          string `json:"call_id"`
	Reason          string `json:"reason,omitempty"`
}

type AgentInterventionDecisionResponse struct {
	Intervention managedagents.SessionIntervention `json:"intervention"`
	Events       []managedagents.Event             `json:"events,omitempty"`
	Resumed      bool                              `json:"resumed,omitempty"`
}

type AgentArchiveSessionRequest struct {
	ParentSessionID string `json:"-"`
	SessionID       string `json:"session_id"`
}

type AgentArchiveSessionResponse struct {
	Session managedagents.Session `json:"session"`
}

type AgentCancelStartRequest struct {
	ParentSessionID string `json:"-"`
	SessionID       string `json:"session_id"`
	Reason          string `json:"reason,omitempty"`
}

type AgentCancelStartResponse struct {
	QueueRequest managedagents.SubagentStartRequest `json:"queue_request"`
}

type AgentTaskGroupItemRequest struct {
	AgentID              string          `json:"agent_id,omitempty"`
	Agent                string          `json:"agent,omitempty"`
	EnvironmentID        string          `json:"environment_id,omitempty"`
	Title                string          `json:"title,omitempty"`
	Message              string          `json:"message"`
	Priority             int             `json:"priority,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
}

type AgentTaskGroupCreateRequest struct {
	ParentSessionID string                      `json:"-"`
	ParentTurnID    string                      `json:"-"`
	TemplateID      string                      `json:"template_id,omitempty"`
	Strategy        string                      `json:"strategy,omitempty"`
	ResultReducer   string                      `json:"result_reducer,omitempty"`
	Quorum          int                         `json:"quorum,omitempty"`
	FailFast        bool                        `json:"fail_fast,omitempty"`
	Items           []AgentTaskGroupItemRequest `json:"items"`
}

type AgentTaskGroupRequest struct {
	ParentSessionID string `json:"-"`
	GroupID         string `json:"group_id"`
}

type AgentTaskGroupWaitRequest struct {
	ParentSessionID string `json:"-"`
	GroupID         string `json:"group_id"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

type AgentTaskGroupCollectRequest struct {
	ParentSessionID string `json:"-"`
	GroupID         string `json:"group_id"`
}

type AgentTaskGroupCancelRequest struct {
	ParentSessionID string `json:"-"`
	GroupID         string `json:"group_id"`
	Reason          string `json:"reason,omitempty"`
}

type AgentTaskGroupRetryItemRequest struct {
	ParentSessionID string `json:"-"`
	GroupID         string `json:"group_id"`
	ItemIndex       int    `json:"item_index"`
}

type AgentTaskGroupRetryRequest struct {
	ParentSessionID string `json:"-"`
	GroupID         string `json:"group_id"`
}

type AgentTaskGroupItemState struct {
	Item                  managedagents.SubagentTaskGroupItem `json:"item"`
	Session               *managedagents.Session              `json:"session,omitempty"`
	Status                string                              `json:"status"`
	LastTurnStatus        string                              `json:"last_turn_status,omitempty"`
	Reason                string                              `json:"reason,omitempty"`
	QueueRequest          *managedagents.SubagentStartRequest `json:"queue_request,omitempty"`
	PendingApprovals      []managedagents.SessionIntervention `json:"pending_approvals,omitempty"`
	AgentText             string                              `json:"agent_text,omitempty"`
	ResultJSON            json.RawMessage                     `json:"result_json,omitempty"`
	ResultSchema          json.RawMessage                     `json:"result_schema,omitempty"`
	ResultValid           bool                                `json:"result_valid"`
	ResultValidationError string                              `json:"result_validation_error,omitempty"`
	EventCount            int                                 `json:"event_count,omitempty"`
	NestedGroups          []AgentTaskGroupNestedState         `json:"nested_groups,omitempty"`
}

type AgentTaskGroupSummary struct {
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Failed     int    `json:"failed"`
	Canceled   int    `json:"canceled"`
	Terminated int    `json:"terminated"`
	Rejected   int    `json:"rejected"`
	Queued     int    `json:"queued"`
	Running    int    `json:"running"`
	Waiting    int    `json:"waiting"`
	Terminal   int    `json:"terminal"`
	Status     string `json:"status"`
}

type AgentTaskGroupAggregate struct {
	Reducer              string          `json:"reducer"`
	Text                 string          `json:"text,omitempty"`
	JSON                 json.RawMessage `json:"json,omitempty"`
	Schema               json.RawMessage `json:"schema,omitempty"`
	CompletedItemIndexes []int           `json:"completed_item_indexes,omitempty"`
	FailedItemIndexes    []int           `json:"failed_item_indexes,omitempty"`
	CanceledItemIndexes  []int           `json:"canceled_item_indexes,omitempty"`
}

type AgentTaskGroupNestedState struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

type AgentTaskGroupCreateResponse struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

type AgentTaskGroupResponse struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

type AgentTaskGroupWaitResponse struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	TimedOut  bool                            `json:"timed_out,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

type AgentTaskGroupCollectResponse struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

type AgentTaskGroupCancelResponse struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

type AgentTaskGroupRetryResponse struct {
	Group     managedagents.SubagentTaskGroup `json:"group"`
	Status    string                          `json:"status"`
	Completed bool                            `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary           `json:"summary"`
	Aggregate AgentTaskGroupAggregate         `json:"aggregate"`
	Items     []AgentTaskGroupItemState       `json:"items"`
}

var (
	defaultAgentToolServiceMu sync.RWMutex
	defaultAgentToolService   AgentToolService
)

func SetDefaultAgentToolService(service AgentToolService) {
	defaultAgentToolServiceMu.Lock()
	defer defaultAgentToolServiceMu.Unlock()
	defaultAgentToolService = service
}

func DefaultAgentToolService() AgentToolService {
	defaultAgentToolServiceMu.RLock()
	defer defaultAgentToolServiceMu.RUnlock()
	return defaultAgentToolService
}

type AgentRuntime struct {
	Service AgentToolService
}

func (AgentRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: AgentIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Agent Tools",
			Description: "Create and orchestrate subagent sessions inside the current workspace.",
		},
		SystemRole: "Use agent.* tools only when there are two or more independent subtasks that can run concurrently, specialized isolation is useful, or intermediate research would consume substantial parent context. Do not delegate single-step work, tightly coupled sequential work, or tasks that can be completed in one or two tool calls. Prefer agent.spawn for one independent unit and agent.run_group for parallel fan-out. Always wait for and collect delegated results before final completion.",
		Executors:  []string{ExecutorServer},
		API: []API{
			{
				Name:           "spawn",
				Namespace:      NamespaceAgent,
				APIName:        "spawn",
				Description:    "Create a subagent session in the current workspace, optionally send its initial message, and start it running.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string"},"agent":{"type":"string"},"environment_id":{"type":"string"},"title":{"type":"string"},"message":{"type":"string"}}}`),
				Capabilities:   []string{"agent.session.write", "agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "create_session",
				Namespace:      NamespaceAgent,
				APIName:        "create_session",
				Description:    "Create a subagent session in the current workspace without sending an initial message.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string"},"agent":{"type":"string"},"environment_id":{"type":"string"},"title":{"type":"string"}}}`),
				Capabilities:   []string{"agent.session.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "send_message",
				Namespace:      NamespaceAgent,
				APIName:        "send_message",
				Description:    "Send a user message to an existing subagent session and start a new turn.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"message":{"type":"string"}},"required":["session_id","message"]}`),
				Capabilities:   []string{"agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "get_session",
				Namespace:      NamespaceAgent,
				APIName:        "get_session",
				Description:    "Read a subagent session and its high-level orchestration status.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.session.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "wait",
				Namespace:      NamespaceAgent,
				APIName:        "wait",
				Description:    "Wait until a subagent session becomes idle, terminated, failed, or starts waiting for approval.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":3600},"return_on_waiting_approval":{"type":"boolean"}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "collect_result",
				Namespace:      NamespaceAgent,
				APIName:        "collect_result",
				Description:    "Collect the latest result from a subagent session, including its last agent message and artifacts.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"include_artifacts":{"type":"boolean"}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read", "artifact.metadata.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "list_events",
				Namespace:      NamespaceAgent,
				APIName:        "list_events",
				Description:    "List subagent session events after a sequence number.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"after_seq":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "stream_events",
				Namespace:      NamespaceAgent,
				APIName:        "stream_events",
				Description:    "Wait for new subagent session events after a sequence number and return them with long-poll semantics.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"after_seq":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":200},"wait_seconds":{"type":"integer","minimum":1,"maximum":300}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.event.read", "agent.event.stream"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "approve_tool",
				Namespace:      NamespaceAgent,
				APIName:        "approve_tool",
				Description:    "Approve a pending subagent tool intervention and resume the child turn when possible.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"turn_id":{"type":"string"},"call_id":{"type":"string"},"reason":{"type":"string"}},"required":["session_id","turn_id","call_id"]}`),
				Capabilities:   []string{"agent.approval.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "reject_tool",
				Namespace:      NamespaceAgent,
				APIName:        "reject_tool",
				Description:    "Reject a pending subagent tool intervention and resume the child turn when possible.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"turn_id":{"type":"string"},"call_id":{"type":"string"},"reason":{"type":"string"}},"required":["session_id","turn_id","call_id"]}`),
				Capabilities:   []string{"agent.approval.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "cancel_start",
				Namespace:      NamespaceAgent,
				APIName:        "cancel_start",
				Description:    "Cancel a pending queued start for a subagent session before it begins running.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"reason":{"type":"string"}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.session.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "run_group",
				Namespace:      NamespaceAgent,
				APIName:        "run_group",
				Description:    "Create a fan-out task group of subagents and start each item with its own initial message.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"template_id":{"type":"string"},"strategy":{"type":"string","enum":["all_completed","any_completed","quorum"]},"result_reducer":{"type":"string","enum":["none","concat_text","json_array","json_object_by_item","first_success","majority_text","json_values","merge_objects","first_success_value","majority_value"]},"quorum":{"type":"integer","minimum":1},"fail_fast":{"type":"boolean"},"items":{"type":"array","minItems":1,"items":{"type":"object","properties":{"agent_id":{"type":"string"},"agent":{"type":"string"},"environment_id":{"type":"string"},"title":{"type":"string"},"message":{"type":"string"},"priority":{"type":"integer"},"expected_result_schema":{"type":"object"}},"required":["message"]}}},"anyOf":[{"required":["items"]},{"required":["template_id"]}]}`),
				Capabilities:   []string{"agent.session.write", "agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "list_group_templates",
				Namespace:      NamespaceAgent,
				APIName:        "list_group_templates",
				Description:    "List builtin task-group workflow templates, including their default reducers and result schema hints.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{}}`),
				Capabilities:   []string{"agent.session.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "get_group",
				Namespace:      NamespaceAgent,
				APIName:        "get_group",
				Description:    "Read the current fan-out/fan-in task group state and per-item statuses.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"}},"required":["group_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "wait_group",
				Namespace:      NamespaceAgent,
				APIName:        "wait_group",
				Description:    "Wait until a fan-out/fan-in task group satisfies its completion strategy or times out.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":3600}},"required":["group_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "collect_group",
				Namespace:      NamespaceAgent,
				APIName:        "collect_group",
				Description:    "Collect the latest aggregated result view for a fan-out/fan-in task group.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"}},"required":["group_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read", "artifact.metadata.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "cancel_group",
				Namespace:      NamespaceAgent,
				APIName:        "cancel_group",
				Description:    "Cancel a task group and cascade termination to every started or queued subagent item.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"},"reason":{"type":"string"}},"required":["group_id"]}`),
				Capabilities:   []string{"agent.session.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "retry_group_item",
				Namespace:      NamespaceAgent,
				APIName:        "retry_group_item",
				Description:    "Retry one failed, rejected, or terminated task group item by launching a fresh subagent session for that item.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"},"item_index":{"type":"integer","minimum":0}},"required":["group_id","item_index"]}`),
				Capabilities:   []string{"agent.session.write", "agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "retry_group",
				Namespace:      NamespaceAgent,
				APIName:        "retry_group",
				Description:    "Retry every failed, rejected, or terminated task group item and reactivate the group if it had been canceled.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string"}},"required":["group_id"]}`),
				Capabilities:   []string{"agent.session.write", "agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "start_discussion",
				Namespace:      NamespaceAgent,
				APIName:        "start_discussion",
				Description:    "Create a persistent two-round multi-agent deliberation from a dynamic team plan.",
				Parameters:     AgentDeliberationTeamPlanSchema,
				Capabilities:   []string{"agent.session.write", "agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "list_discussion_strategies",
				Namespace:      NamespaceAgent,
				APIName:        "list_discussion_strategies",
				Description:    "List supported multi-agent discussion strategies and the dynamic team-plan schema.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{}}`),
				Capabilities:   []string{"agent.session.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "get_discussion",
				Namespace:      NamespaceAgent,
				APIName:        "get_discussion",
				Description:    "Advance and read a persistent multi-agent deliberation, including rounds and contributions.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"deliberation_id":{"type":"string"}},"required":["deliberation_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "wait_discussion",
				Namespace:      NamespaceAgent,
				APIName:        "wait_discussion",
				Description:    "Wait while advancing a two-round deliberation until it completes, fails, is canceled, or times out.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"deliberation_id":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":3600}},"required":["deliberation_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "collect_discussion",
				Namespace:      NamespaceAgent,
				APIName:        "collect_discussion",
				Description:    "Collect the current or final deliberation result with round summaries and participant contributions.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"deliberation_id":{"type":"string"}},"required":["deliberation_id"]}`),
				Capabilities:   []string{"agent.session.read", "agent.event.read"},
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "cancel_discussion",
				Namespace:      NamespaceAgent,
				APIName:        "cancel_discussion",
				Description:    "Cancel a deliberation and all active participant or moderator task groups.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"deliberation_id":{"type":"string"},"reason":{"type":"string"}},"required":["deliberation_id"]}`),
				Capabilities:   []string{"agent.session.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "retry_discussion_participant",
				Namespace:      NamespaceAgent,
				APIName:        "retry_discussion_participant",
				Description:    "Retry one failed participant contribution in round 1 or round 2.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"deliberation_id":{"type":"string"},"round_number":{"type":"integer","minimum":1,"maximum":2},"participant_index":{"type":"integer","minimum":0}},"required":["deliberation_id","round_number","participant_index"]}`),
				Capabilities:   []string{"agent.session.write", "agent.message.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "archive_session",
				Namespace:      NamespaceAgent,
				APIName:        "archive_session",
				Description:    "Archive a finished or no-longer-needed subagent session.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`),
				Capabilities:   []string{"agent.session.write"},
				Risk:           ToolRiskWrite,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
		},
	}
}

func (runtime AgentRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	if normalizeAgentAPIName(call.APIName) == "list_group_templates" {
		response := AgentTaskGroupTemplateListResponse{
			Templates: ListAgentTaskGroupTemplates(),
		}
		return agentToolResult(call, "list_group_templates", response, fmt.Sprintf("Listed %d task group templates.", len(response.Templates)))
	}
	if normalizeAgentAPIName(call.APIName) == "list_discussion_strategies" {
		response := ListAgentDeliberationStrategies()
		return agentToolResult(call, "list_discussion_strategies", response, fmt.Sprintf("Listed %d discussion strategies.", len(response.Strategies)))
	}

	service := runtime.Service
	if service == nil {
		service = DefaultAgentToolService()
	}
	if service == nil {
		return ExecutionResult{}, errors.New("agent tool service is not configured")
	}

	switch normalizeAgentAPIName(call.APIName) {
	case "spawn":
		var request AgentSpawnRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode spawn arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		request.ParentTurnID = executionContext.TurnID
		response, err := service.Spawn(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		message := fmt.Sprintf("Spawned subagent session %s.", response.Session.ID)
		if response.Queued {
			message = fmt.Sprintf("Spawned subagent session %s and queued its initial turn.", response.Session.ID)
		}
		return agentToolResult(call, "spawn", response, message)
	case "create_session":
		var request AgentSpawnRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode create_session arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		request.ParentTurnID = executionContext.TurnID
		request.Message = ""
		response, err := service.Spawn(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "create_session", response, fmt.Sprintf("Created subagent session %s.", response.Session.ID))
	case "send_message":
		var request AgentSendMessageRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode send_message arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		request.ParentTurnID = executionContext.TurnID
		response, err := service.SendMessage(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		message := fmt.Sprintf("Sent message to subagent session %s.", response.SessionID)
		if response.Queued {
			message = fmt.Sprintf("Queued message for subagent session %s.", response.SessionID)
		}
		return agentToolResult(call, "send_message", response, message)
	case "get_session":
		var request AgentSessionRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode get_session arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.GetSession(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "get_session", response, fmt.Sprintf("Session %s is %s.", response.Session.ID, response.Status))
	case "wait":
		var request AgentWaitRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode wait arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.Wait(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		message := fmt.Sprintf("Session %s reached status %s.", response.Session.ID, response.Status)
		if response.TimedOut {
			message = fmt.Sprintf("Timed out while waiting for session %s; current status is %s.", response.Session.ID, response.Status)
		}
		return agentToolResult(call, "wait", response, message)
	case "collect_result":
		var request AgentCollectResultRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode collect_result arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.CollectResult(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		message := fmt.Sprintf("Collected result for session %s with status %s.", response.Session.ID, response.Status)
		if text := strings.TrimSpace(response.AgentText); text != "" {
			message = text
		}
		return agentToolResult(call, "collect_result", response, message)
	case "list_events":
		var request AgentListEventsRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode list_events arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.ListEvents(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "list_events", response, fmt.Sprintf("Loaded %d events from session %s.", len(response.Events), response.SessionID))
	case "stream_events":
		var request AgentStreamEventsRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode stream_events arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.StreamEvents(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		message := fmt.Sprintf("Loaded %d streamed events from session %s.", len(response.Events), response.SessionID)
		if response.TimedOut {
			message = fmt.Sprintf("Timed out while waiting for new events from session %s.", response.SessionID)
		}
		return agentToolResult(call, "stream_events", response, message)
	case "approve_tool":
		var request AgentInterventionDecisionRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode approve_tool arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.ApproveTool(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "approve_tool", response, fmt.Sprintf("Approved tool call %s for session %s.", response.Intervention.CallID, response.Intervention.SessionID))
	case "reject_tool":
		var request AgentInterventionDecisionRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode reject_tool arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.RejectTool(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "reject_tool", response, fmt.Sprintf("Rejected tool call %s for session %s.", response.Intervention.CallID, response.Intervention.SessionID))
	case "archive_session":
		var request AgentArchiveSessionRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode archive_session arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.ArchiveSession(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "archive_session", response, fmt.Sprintf("Archived session %s.", response.Session.ID))
	case "cancel_start":
		var request AgentCancelStartRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode cancel_start arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.CancelStart(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "cancel_start", response, fmt.Sprintf("Canceled queued start %s for session %s.", response.QueueRequest.ID, response.QueueRequest.SessionID))
	case "run_group":
		var request AgentTaskGroupCreateRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode run_group arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		request.ParentTurnID = executionContext.TurnID
		response, err := service.CreateTaskGroup(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "run_group", response, fmt.Sprintf("Created task group %s with %d items.", response.Group.ID, len(response.Items)))
	case "get_group":
		var request AgentTaskGroupRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode get_group arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.GetTaskGroup(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "get_group", response, fmt.Sprintf("Task group %s is %s.", response.Group.ID, response.Status))
	case "wait_group":
		var request AgentTaskGroupWaitRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode wait_group arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.WaitTaskGroup(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		message := fmt.Sprintf("Task group %s reached status %s.", response.Group.ID, response.Status)
		if response.TimedOut {
			message = fmt.Sprintf("Timed out while waiting for task group %s; current status is %s.", response.Group.ID, response.Status)
		}
		return agentToolResult(call, "wait_group", response, message)
	case "collect_group":
		var request AgentTaskGroupCollectRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode collect_group arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.CollectTaskGroup(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "collect_group", response, fmt.Sprintf("Collected task group %s with status %s.", response.Group.ID, response.Status))
	case "cancel_group":
		var request AgentTaskGroupCancelRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode cancel_group arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.CancelTaskGroup(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "cancel_group", response, fmt.Sprintf("Canceled task group %s.", response.Group.ID))
	case "retry_group_item":
		var request AgentTaskGroupRetryItemRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode retry_group_item arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.RetryTaskGroupItem(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "retry_group_item", response, fmt.Sprintf("Retried task group %s item %d.", response.Group.ID, request.ItemIndex))
	case "retry_group":
		var request AgentTaskGroupRetryRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode retry_group arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := service.RetryTaskGroup(ctx, request)
		if err != nil {
			if result, ok := agentToolFailureResult(call, err); ok {
				return result, nil
			}
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "retry_group", response, fmt.Sprintf("Retried eligible items in task group %s.", response.Group.ID))
	case "start_discussion":
		discussionService, ok := service.(AgentDeliberationService)
		if !ok {
			return ExecutionResult{}, errors.New("agent deliberation service is not configured")
		}
		var request AgentDeliberationCreateRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode start_discussion arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		request.ParentTurnID = executionContext.TurnID
		response, err := discussionService.CreateDeliberation(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "start_discussion", response, fmt.Sprintf("Started deliberation %s with %d participants.", response.Deliberation.ID, len(response.Participants)))
	case "get_discussion", "collect_discussion":
		discussionService, ok := service.(AgentDeliberationService)
		if !ok {
			return ExecutionResult{}, errors.New("agent deliberation service is not configured")
		}
		var request AgentDeliberationRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode %s arguments: %w", call.APIName, err)
		}
		request.ParentSessionID = executionContext.SessionID
		var response AgentDeliberationResponse
		var err error
		if normalizeAgentAPIName(call.APIName) == "collect_discussion" {
			response, err = discussionService.CollectDeliberation(ctx, request)
		} else {
			response, err = discussionService.GetDeliberation(ctx, request)
		}
		if err != nil {
			return ExecutionResult{}, err
		}
		return agentToolResult(call, normalizeAgentAPIName(call.APIName), response, fmt.Sprintf("Deliberation %s is %s.", response.Deliberation.ID, response.Deliberation.Status))
	case "wait_discussion":
		discussionService, ok := service.(AgentDeliberationService)
		if !ok {
			return ExecutionResult{}, errors.New("agent deliberation service is not configured")
		}
		var request AgentDeliberationWaitRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode wait_discussion arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := discussionService.WaitDeliberation(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "wait_discussion", response, fmt.Sprintf("Deliberation %s is %s.", response.Deliberation.ID, response.Deliberation.Status))
	case "cancel_discussion":
		discussionService, ok := service.(AgentDeliberationService)
		if !ok {
			return ExecutionResult{}, errors.New("agent deliberation service is not configured")
		}
		var request AgentDeliberationCancelRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode cancel_discussion arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := discussionService.CancelDeliberation(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "cancel_discussion", response, fmt.Sprintf("Canceled deliberation %s.", response.Deliberation.ID))
	case "retry_discussion_participant":
		discussionService, ok := service.(AgentDeliberationService)
		if !ok {
			return ExecutionResult{}, errors.New("agent deliberation service is not configured")
		}
		var request AgentDeliberationRetryParticipantRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode retry_discussion_participant arguments: %w", err)
		}
		request.ParentSessionID = executionContext.SessionID
		response, err := discussionService.RetryDeliberationParticipant(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		return agentToolResult(call, "retry_discussion_participant", response, fmt.Sprintf("Retried participant %d in deliberation %s round %d.", request.ParticipantIndex, response.Deliberation.ID, request.RoundNumber))
	default:
		return ExecutionResult{}, fmt.Errorf("unsupported agent api %q", call.APIName)
	}
}

func agentToolFailureResult(call Call, err error) (ExecutionResult, bool) {
	var toolErr AgentToolError
	if !errors.As(err, &toolErr) {
		return ExecutionResult{}, false
	}
	call = NormalizeCall(call)
	var state json.RawMessage
	if toolErr.State != nil {
		encoded, marshalErr := json.Marshal(toolErr.State)
		if marshalErr == nil {
			state = encoded
		}
	}
	return ExecutionResult{
		ID:         call.ID,
		Identifier: AgentIdentifier,
		APIName:    call.APIName,
		Content:    toolErr.Error(),
		State:      state,
		Error: &ExecutionError{
			Type:    fallbackString(strings.TrimSpace(toolErr.Type), "agent_tool_error"),
			Message: toolErr.Error(),
		},
	}, true
}

func normalizeAgentAPIName(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func agentToolResult(call Call, apiName string, state any, content string) (ExecutionResult, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		ID:         call.ID,
		Identifier: AgentIdentifier,
		APIName:    apiName,
		Content:    content,
		State:      encoded,
	}, nil
}

func ExtractAgentMessageText(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ""
	}
	lines := make([]string, 0, len(decoded.Content))
	for _, item := range decoded.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			lines = append(lines, item.Text)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ExtractAgentMessageResultJSON(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	var decoded struct {
		ResultJSON json.RawMessage `json:"result_json"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil
	}
	if len(decoded.ResultJSON) == 0 || string(decoded.ResultJSON) == "null" {
		return nil
	}
	clone := make([]byte, len(decoded.ResultJSON))
	copy(clone, decoded.ResultJSON)
	return clone
}

func WaitTimeout(timeoutSeconds int) time.Duration {
	if timeoutSeconds <= 0 {
		return 30 * time.Second
	}
	if timeoutSeconds > 3600 {
		timeoutSeconds = 3600
	}
	return time.Duration(timeoutSeconds) * time.Second
}
