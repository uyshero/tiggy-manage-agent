package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

type AgentTaskGroupTemplate struct {
	ID                       string                      `json:"id"`
	Title                    string                      `json:"title"`
	Description              string                      `json:"description"`
	Strategy                 string                      `json:"strategy"`
	ResultReducer            string                      `json:"result_reducer"`
	Quorum                   int                         `json:"quorum,omitempty"`
	FailFast                 bool                        `json:"fail_fast,omitempty"`
	ItemsRequired            bool                        `json:"items_required,omitempty"`
	DefaultItems             []AgentTaskGroupItemRequest `json:"default_items,omitempty"`
	ItemExpectedResultSchema json.RawMessage             `json:"item_expected_result_schema,omitempty"`
}

type AgentTaskGroupTemplateListResponse struct {
	Templates []AgentTaskGroupTemplate `json:"templates"`
}

var builtinAgentTaskGroupTemplates = []AgentTaskGroupTemplate{
	{
		ID:            "module_risk_audit",
		Title:         "Module Risk Audit",
		Description:   "Parallelize module-by-module audits and aggregate structured JSON findings.",
		Strategy:      managedagents.SubagentTaskGroupStrategyAllCompleted,
		ResultReducer: managedagents.SubagentTaskGroupReducerJSONValues,
		ItemsRequired: true,
		ItemExpectedResultSchema: json.RawMessage(`{
			"type":"object",
			"required":["module","risk_level","summary","files"],
			"properties":{
				"module":{"type":"string"},
				"risk_level":{"type":"string","enum":["low","medium","high","critical"]},
				"summary":{"type":"string"},
				"files":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"},
				"evidence":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"},
				"recommendations":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"}
			}
		}`),
	},
	{
		ID:            "research_compare_summarize",
		Title:         "Research Compare Summarize",
		Description:   "Fan out multiple research threads and collect concise text summaries for comparison.",
		Strategy:      managedagents.SubagentTaskGroupStrategyAllCompleted,
		ResultReducer: managedagents.SubagentTaskGroupReducerConcatText,
		ItemsRequired: true,
	},
	{
		ID:            "multi_file_refactor_plan",
		Title:         "Multi-file Refactor Plan",
		Description:   "Collect structured refactor plans from multiple subagents and merge them into one object.",
		Strategy:      managedagents.SubagentTaskGroupStrategyAllCompleted,
		ResultReducer: managedagents.SubagentTaskGroupReducerMergeObjects,
		ItemsRequired: true,
		ItemExpectedResultSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"summary":{"type":"string","x-conflict-mode":"first_wins"},
				"files":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"},
				"changes":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"},
				"risks":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"},
				"tests":{"type":"array","items":{"type":"string"},"x-array-merge":"dedupe"}
			}
		}`),
	},
}

func ListAgentTaskGroupTemplates() []AgentTaskGroupTemplate {
	templates := make([]AgentTaskGroupTemplate, 0, len(builtinAgentTaskGroupTemplates))
	for _, template := range builtinAgentTaskGroupTemplates {
		templates = append(templates, cloneAgentTaskGroupTemplate(template))
	}
	return templates
}

func LookupAgentTaskGroupTemplate(id string) (AgentTaskGroupTemplate, bool) {
	normalized := strings.TrimSpace(id)
	for _, template := range builtinAgentTaskGroupTemplates {
		if template.ID == normalized {
			return cloneAgentTaskGroupTemplate(template), true
		}
	}
	return AgentTaskGroupTemplate{}, false
}

func ExpandAgentTaskGroupTemplate(request AgentTaskGroupCreateRequest) (AgentTaskGroupCreateRequest, *AgentTaskGroupTemplate, error) {
	templateID := strings.TrimSpace(request.TemplateID)
	if templateID == "" {
		return request, nil, nil
	}
	template, ok := LookupAgentTaskGroupTemplate(templateID)
	if !ok {
		return AgentTaskGroupCreateRequest{}, nil, fmt.Errorf("unknown task group template %q", templateID)
	}
	expanded := request
	expanded.TemplateID = template.ID
	if strings.TrimSpace(expanded.Strategy) == "" {
		expanded.Strategy = template.Strategy
	}
	if strings.TrimSpace(expanded.ResultReducer) == "" {
		expanded.ResultReducer = template.ResultReducer
	}
	if expanded.Quorum == 0 {
		expanded.Quorum = template.Quorum
	}
	if len(expanded.Items) == 0 && len(template.DefaultItems) > 0 {
		expanded.Items = cloneAgentTaskGroupItems(template.DefaultItems)
	}
	if len(template.ItemExpectedResultSchema) > 0 {
		for index := range expanded.Items {
			if len(expanded.Items[index].ExpectedResultSchema) == 0 {
				expanded.Items[index].ExpectedResultSchema = cloneAgentTaskGroupSchema(template.ItemExpectedResultSchema)
			}
		}
	}
	return expanded, &template, nil
}

func cloneAgentTaskGroupTemplate(template AgentTaskGroupTemplate) AgentTaskGroupTemplate {
	template.DefaultItems = cloneAgentTaskGroupItems(template.DefaultItems)
	template.ItemExpectedResultSchema = cloneAgentTaskGroupSchema(template.ItemExpectedResultSchema)
	return template
}

func cloneAgentTaskGroupItems(items []AgentTaskGroupItemRequest) []AgentTaskGroupItemRequest {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]AgentTaskGroupItemRequest, len(items))
	copy(cloned, items)
	for index := range cloned {
		cloned[index].ExpectedResultSchema = cloneAgentTaskGroupSchema(cloned[index].ExpectedResultSchema)
	}
	return cloned
}

func cloneAgentTaskGroupSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return json.RawMessage(cloned)
}
