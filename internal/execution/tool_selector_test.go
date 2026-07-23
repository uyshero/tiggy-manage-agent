package execution

import (
	"context"
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestSelectTurnToolsKeepsOnlyCommonBuiltinsForOrdinaryTurn(t *testing.T) {
	selected := SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"帮我整理项目中的配置文件"}]}`),
	})
	names := selectedToolNames(selected)
	if len(names) != 14 {
		t.Fatalf("expected 14 common model tools, got %d: %#v", len(names), names)
	}

	assertSelected(t, names, "default_read_file", true)
	assertSelected(t, names, "interaction_ask_user", true)
	assertSelected(t, names, "task_create_plan", true)
	assertSelected(t, names, "web_search", false)
	assertSelected(t, names, "skills_search", false)
	assertSelected(t, names, "agent_spawn", false)
}

func TestSelectTurnToolsAddsRelevantCapabilityDomains(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		included []string
		excluded []string
	}{
		{name: "web", message: "搜索一下今天的最新新闻", included: []string{"web_search", "web_crawl"}, excluded: []string{"agent_spawn"}},
		{name: "skills", message: "从技能市场安装并启用一个 PDF skill", included: []string{"skills_search", "skills_install", "skills_enable"}, excluded: []string{"agent_spawn"}},
		{name: "subagents", message: "把工作并行委派给几个子智能体", included: []string{"agent_spawn", "agent_send_message", "agent_collect_result"}, excluded: []string{"agent_run_group", "agent_start_discussion"}},
		{name: "group", message: "用任务组批量委派这些工作", included: []string{"agent_spawn", "agent_run_group", "agent_collect_group"}, excluded: []string{"agent_start_discussion"}},
		{name: "discussion", message: "让多个智能体进行圆桌讨论", included: []string{"agent_spawn", "agent_run_group", "agent_start_discussion", "agent_collect_discussion"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": test.message}}})
			if err != nil {
				t.Fatal(err)
			}
			names := selectedToolNames(SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{UserPayload: payload}))
			for _, name := range test.included {
				assertSelected(t, names, name, true)
			}
			for _, name := range test.excluded {
				assertSelected(t, names, name, false)
			}
		})
	}
}

func TestSelectTurnToolsUsesRecentUserContextForContinuation(t *testing.T) {
	selected := SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"好的，继续"}]}`),
		History: []managedagents.ConversationMessage{
			{Role: "user", Payload: json.RawMessage(`{"content":[{"type":"text","text":"帮我搜索官网的最新发布说明"}]}`)},
			{Role: "assistant", Payload: json.RawMessage(`{"content":[{"type":"text","text":"我会继续处理"}]}`)},
		},
	})

	assertSelected(t, selectedToolNames(selected), "web_search", true)
}

func TestSelectTurnToolsUsesSummaryWhenContinuationHistoryWasCompacted(t *testing.T) {
	selected := SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"继续"}]}`),
		SummaryText: "The active task is to search the web for current release notes.",
	})

	assertSelected(t, selectedToolNames(selected), "web_search", true)
}

func TestSelectTurnToolsKeepsActiveSkillInspectionAndSkillRequiredWeb(t *testing.T) {
	selected := SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload:     json.RawMessage(`{"content":[{"type":"text","text":"生成报告"}]}`),
		HasActiveSkills: true,
		SkillContext:    json.RawMessage(`{"instructions":"Use web_search for source research."}`),
	})
	names := selectedToolNames(selected)

	assertSelected(t, names, "skills_inspect", true)
	assertSelected(t, names, "skills_read_asset", true)
	assertSelected(t, names, "skills_install", false)
	assertSelected(t, names, "web_search", true)
}

func TestSelectTurnToolsPreservesExplicitConfiguration(t *testing.T) {
	registry, policy := tools.DefaultRegistry().Configured(json.RawMessage(`{"tools":["web_search"]}`))
	selected := SelectTurnTools(registry, policy, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"只整理本地文件"}]}`),
	})
	names := selectedToolNames(selected)

	if len(names) != 1 || !names["web_search"] {
		t.Fatalf("expected explicit tool configuration to bypass selection, got %#v", names)
	}
}

func TestSelectTurnToolsPreservesExtensionNamespaces(t *testing.T) {
	registry := tools.DefaultRegistry()
	registry.Register(selectorExtensionRuntime{})
	selected := SelectTurnTools(registry, tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"整理文件"}]}`),
	})

	assertSelected(t, selectedToolNames(selected), "company_search_records", true)
}

func selectedToolNames(registry tools.Registry) map[string]bool {
	names := make(map[string]bool)
	for _, tool := range registry.ModelTools() {
		names[tool.Function.Name] = true
	}
	return names
}

func assertSelected(t *testing.T, names map[string]bool, name string, expected bool) {
	t.Helper()
	if names[name] != expected {
		t.Fatalf("expected %s selected=%v, got tools %#v", name, expected, names)
	}
}

type selectorExtensionRuntime struct{}

func (selectorExtensionRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier:     "company_search",
		Type:           "extension",
		Executors:      []string{tools.ExecutorServer},
		ApprovalPolicy: tools.ApprovalPolicyNever,
		API: []tools.API{{
			Name: "records", Namespace: "company_search", APIName: "records",
			Description: "Search company records.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			Risk:        tools.ToolRiskRead,
		}},
	}
}

func (selectorExtensionRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{}, nil
}
