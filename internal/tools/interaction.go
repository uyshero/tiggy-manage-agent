package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const InteractionIdentifier = NamespaceInteraction

const (
	InteractionAPIAskUser             = "ask_user"
	InteractionAPIRequestPlanApproval = "request_plan_approval"
	InteractionAPIRequestUpload       = "request_upload"
	InteractionModeSelect             = "select"
	InteractionModeMulti              = "multiselect"
	InteractionModeForm               = "form"
	InteractionModeFree               = "freeform"
)

type AskUserChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type AskUserField struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Type        string          `json:"type"`
	Required    bool            `json:"required,omitempty"`
	Placeholder string          `json:"placeholder,omitempty"`
	Choices     []AskUserChoice `json:"choices,omitempty"`
}

type AskUserRequest struct {
	Question      string          `json:"question"`
	Mode          string          `json:"mode"`
	Choices       []AskUserChoice `json:"choices,omitempty"`
	Fields        []AskUserField  `json:"fields,omitempty"`
	AllowFreeform bool            `json:"allow_freeform,omitempty"`
}

type PlanApprovalRequest struct {
	PlanID  string `json:"plan_id,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type UploadRequest struct {
	Prompt     string   `json:"prompt"`
	Accept     []string `json:"accept,omitempty"`
	MaxFiles   int      `json:"max_files,omitempty"`
	MaxBytes   int64    `json:"max_bytes,omitempty"`
	Required   bool     `json:"required,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	UploadHint string   `json:"upload_hint,omitempty"`
}

type PlanApprovalSnapshot struct {
	Plan    managedagents.SessionTaskPlan `json:"plan"`
	Summary string                        `json:"summary,omitempty"`
}

type InteractionRuntime struct{}

func (InteractionRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: InteractionIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "User Interaction",
			Description: "Request missing information or user preferences and resume the same turn after a response.",
		},
		SystemRole: `Most tasks do not need a clarification. Use interaction.ask_user only when missing information materially changes the result, scope, cost, or an irreversible next action and the answer cannot be obtained from available context or read-only tools. Do not ask when a safe reversible default is available: state the assumption and continue. Do not use ask_user for progress updates, tool approval, plan approval, or file upload. Ask one focused question at a time and prefer concrete choices when the options are known. Use form mode when the user needs to provide several explicit fields such as environment, owner, deadline, rollout window, URLs, ticket IDs, or configuration values. Use interaction.request_upload only when the task genuinely requires binary files or large attachments that cannot be provided as text fields or existing session artifacts. Use interaction.request_plan_approval only when the user must review the current active task plan before execution. Plan approval approves direction only: it never approves a later tool call, command, or side effect.`,
		Executors:  []string{ExecutorServer},
		API: []API{{
			Name:           InteractionAPIAskUser,
			Namespace:      NamespaceInteraction,
			APIName:        InteractionAPIAskUser,
			Description:    "Pause the current turn to ask one focused question, collect a concrete choice, or request several explicit form fields when required information or a material user preference is missing. Do not use for tool or plan approval.",
			Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"question":{"type":"string","minLength":1,"maxLength":2000},"mode":{"type":"string","enum":["select","multiselect","form","freeform"]},"choices":{"type":"array","maxItems":8,"items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"}},"required":["id","label"]}},"fields":{"type":"array","maxItems":8,"items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"label":{"type":"string"},"type":{"type":"string","enum":["text","select","multiselect"]},"required":{"type":"boolean"},"placeholder":{"type":"string"},"choices":{"type":"array","maxItems":8,"items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"}},"required":["id","label"]}}},"required":["id","label","type"]}},"allow_freeform":{"type":"boolean"}},"required":["question","mode"]}`),
			Risk:           ToolRiskRead,
			Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
			Implementation: ToolImplementationServerBuiltin,
		}, {
			Name:           InteractionAPIRequestUpload,
			Namespace:      NamespaceInteraction,
			APIName:        InteractionAPIRequestUpload,
			Description:    "Pause the current turn to request one or more files or large attachments from the user. Use only when text fields or existing attachments are insufficient.",
			Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"prompt":{"type":"string","minLength":1,"maxLength":2000},"accept":{"type":"array","maxItems":12,"items":{"type":"string","minLength":1,"maxLength":120}},"max_files":{"type":"integer","minimum":1,"maximum":10},"max_bytes":{"type":"integer","minimum":1,"maximum":1073741824},"required":{"type":"boolean"},"reason":{"type":"string","maxLength":1000},"upload_hint":{"type":"string","maxLength":1000}},"required":["prompt"]}`),
			Risk:           ToolRiskRead,
			Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
			Implementation: ToolImplementationServerBuiltin,
		}, {
			Name:           InteractionAPIRequestPlanApproval,
			Namespace:      NamespaceInteraction,
			APIName:        InteractionAPIRequestPlanApproval,
			Description:    "Pause the current turn so the user can review and approve or reject the current active task plan. This approves plan direction only and never approves later tool execution.",
			Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string"},"summary":{"type":"string","maxLength":1000}}}`),
			Risk:           ToolRiskRead,
			Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
			Implementation: ToolImplementationServerBuiltin,
		}},
	}
}

func (InteractionRuntime) Execute(_ context.Context, call Call, _ ExecutionContext) (ExecutionResult, error) {
	return failedResult(call, "interaction_requires_runtime_parking", "interaction tools must be handled by AgentRuntime's human-interaction parking path"), nil
}

func IsPlanApprovalCall(call Call) bool {
	call = NormalizeCall(call)
	return call.Identifier == InteractionIdentifier && call.APIName == InteractionAPIRequestPlanApproval
}

func IsParkingInteractionCall(call Call) bool {
	return IsAskUserCall(call) || IsUploadRequestCall(call) || IsPlanApprovalCall(call)
}

func ParsePlanApprovalRequest(raw json.RawMessage) (PlanApprovalRequest, error) {
	var request PlanApprovalRequest
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) {
		return request, fmt.Errorf("request_plan_approval arguments must be a JSON object")
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return request, fmt.Errorf("decode request_plan_approval arguments: %w", err)
	}
	request.PlanID = strings.TrimSpace(request.PlanID)
	request.Summary = strings.TrimSpace(request.Summary)
	if len([]rune(request.Summary)) > 1000 {
		return request, fmt.Errorf("request_plan_approval summary exceeds 1000 characters")
	}
	return request, nil
}

func IsAskUserCall(call Call) bool {
	call = NormalizeCall(call)
	return call.Identifier == InteractionIdentifier && call.APIName == InteractionAPIAskUser
}

func IsUploadRequestCall(call Call) bool {
	call = NormalizeCall(call)
	return call.Identifier == InteractionIdentifier && call.APIName == InteractionAPIRequestUpload
}

func ParseUploadRequest(raw json.RawMessage) (UploadRequest, error) {
	var request UploadRequest
	if len(raw) == 0 || !json.Valid(raw) {
		return request, fmt.Errorf("request_upload arguments must be a JSON object")
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return request, fmt.Errorf("decode request_upload arguments: %w", err)
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.Reason = strings.TrimSpace(request.Reason)
	request.UploadHint = strings.TrimSpace(request.UploadHint)
	if request.Prompt == "" {
		return request, fmt.Errorf("request_upload prompt is required")
	}
	if len([]rune(request.Prompt)) > 2000 {
		return request, fmt.Errorf("request_upload prompt exceeds 2000 characters")
	}
	if len([]rune(request.Reason)) > 1000 {
		return request, fmt.Errorf("request_upload reason exceeds 1000 characters")
	}
	if len([]rune(request.UploadHint)) > 1000 {
		return request, fmt.Errorf("request_upload upload_hint exceeds 1000 characters")
	}
	if request.MaxFiles == 0 {
		request.MaxFiles = 1
	}
	if request.MaxFiles < 1 || request.MaxFiles > 10 {
		return request, fmt.Errorf("request_upload max_files must be between 1 and 10")
	}
	if request.MaxBytes < 0 || request.MaxBytes > 1073741824 {
		return request, fmt.Errorf("request_upload max_bytes must be between 1 and 1073741824 when provided")
	}
	normalizedAccept := make([]string, 0, len(request.Accept))
	seen := map[string]bool{}
	for _, value := range request.Accept {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if len([]rune(item)) > 120 {
			return request, fmt.Errorf("request_upload accept entry exceeds 120 characters")
		}
		if !seen[item] {
			seen[item] = true
			normalizedAccept = append(normalizedAccept, item)
		}
	}
	if len(normalizedAccept) > 12 {
		return request, fmt.Errorf("request_upload accept supports at most 12 entries")
	}
	request.Accept = normalizedAccept
	return request, nil
}

func ParseAskUserRequest(raw json.RawMessage) (AskUserRequest, error) {
	var request AskUserRequest
	if len(raw) == 0 || !json.Valid(raw) {
		return request, fmt.Errorf("ask_user arguments must be a JSON object")
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return request, fmt.Errorf("decode ask_user arguments: %w", err)
	}
	request.Question = strings.TrimSpace(request.Question)
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Question == "" {
		return request, fmt.Errorf("ask_user question is required")
	}
	if len([]rune(request.Question)) > 2000 {
		return request, fmt.Errorf("ask_user question exceeds 2000 characters")
	}
	switch request.Mode {
	case InteractionModeSelect, InteractionModeMulti:
		if len(request.Choices) < 2 || len(request.Choices) > 8 {
			return request, fmt.Errorf("ask_user %s mode requires 2-8 choices", request.Mode)
		}
		if err := validateAskUserChoices(request.Choices); err != nil {
			return request, err
		}
	case InteractionModeForm:
		if len(request.Fields) == 0 || len(request.Fields) > 8 {
			return request, fmt.Errorf("ask_user form mode requires 1-8 fields")
		}
		seen := map[string]bool{}
		for index := range request.Fields {
			field := &request.Fields[index]
			field.ID = strings.TrimSpace(field.ID)
			field.Label = strings.TrimSpace(field.Label)
			field.Type = strings.ToLower(strings.TrimSpace(field.Type))
			if field.ID == "" || field.Label == "" || seen[field.ID] {
				return request, fmt.Errorf("ask_user form fields require unique non-empty id and label")
			}
			seen[field.ID] = true
			switch field.Type {
			case "text":
			case InteractionModeSelect, InteractionModeMulti:
				if len(field.Choices) < 2 || len(field.Choices) > 8 {
					return request, fmt.Errorf("ask_user field %q requires 2-8 choices", field.ID)
				}
				if err := validateAskUserChoices(field.Choices); err != nil {
					return request, fmt.Errorf("ask_user field %q: %w", field.ID, err)
				}
			default:
				return request, fmt.Errorf("ask_user field %q has unsupported type %q", field.ID, field.Type)
			}
		}
	case InteractionModeFree:
	default:
		return request, fmt.Errorf("ask_user mode must be select, multiselect, form, or freeform")
	}
	return request, nil
}

func validateAskUserChoices(choices []AskUserChoice) error {
	seen := map[string]bool{}
	for index := range choices {
		choices[index].ID = strings.TrimSpace(choices[index].ID)
		choices[index].Label = strings.TrimSpace(choices[index].Label)
		if choices[index].ID == "" || choices[index].Label == "" || seen[choices[index].ID] {
			return fmt.Errorf("ask_user choices require unique non-empty id and label")
		}
		seen[choices[index].ID] = true
	}
	return nil
}
