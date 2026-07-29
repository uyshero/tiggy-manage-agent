package execution

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestSelectTurnToolsKeepsOnlyCommonBuiltinsForOrdinaryTurn(t *testing.T) {
	selected, report := SelectTurnToolsWithReport(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"帮我整理项目中的配置文件"}]}`),
	})
	names := selectedToolNames(selected)
	selectedSchemas, err := json.Marshal(selected.ModelTools())
	if err != nil {
		t.Fatal(err)
	}
	fullSchemas, err := json.Marshal(tools.DefaultRegistry().ModelTools())
	if err != nil {
		t.Fatal(err)
	}
	if len(selectedSchemas)*2 >= len(fullSchemas) {
		t.Fatalf("ordinary turn schemas should use less than half of the full registry: selected=%d full=%d tools=%#v", len(selectedSchemas), len(fullSchemas), names)
	}
	if report.Mode != "progressive" || report.CandidateToolCount <= report.SelectedToolCount || report.CandidateSchemaBytes != len(fullSchemas) || report.SelectedSchemaBytes != len(selectedSchemas) || report.CandidateSchemaTokens <= report.SelectedSchemaTokens {
		t.Fatalf("unexpected progressive selection report: %#v", report)
	}
	if len(report.Triggers) != 0 {
		t.Fatalf("ordinary request should not record specialized triggers: %#v", report)
	}

	assertSelected(t, names, "default_read_file", true)
	assertSelected(t, names, "interaction_ask_user", true)
	assertSelected(t, names, "interaction_request_upload", false)
	assertSelected(t, names, "interaction_request_plan_approval", false)
	assertSelected(t, names, "task_create_plan", false)
	assertSelected(t, names, "image_generate", false)
	assertSelected(t, names, "image_analyze", false)
	assertSelected(t, names, "tool_catalog_inspect", true)
	assertSelected(t, names, "artifact_read", true)
	assertSelected(t, names, "web_search", false)
	assertSelected(t, names, "skills_search", false)
	assertSelected(t, names, "agent_spawn", false)
}

func TestSelectTurnToolsReportUsesBoundedTriggerNames(t *testing.T) {
	_, report := SelectTurnToolsWithReport(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload:   json.RawMessage(`{"content":[{"type":"text","text":"制定多步骤计划并分析图片"}]}`),
		HasActivePlan: true,
		HasImages:     true,
		SkillContext:  json.RawMessage(`{"instructions":"Use web_search and interaction_request_upload."}`),
	})
	joined := strings.Join(report.Triggers, ",")
	for _, expected := range []string{"active_plan", "image_attachment", "image_request", "task_request", "upload_skill", "web_skill"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("selection report lost trigger %q: %#v", expected, report)
		}
	}
	for _, sensitive := range []string{"制定多步骤计划", "web_search", "interaction_request_upload"} {
		if strings.Contains(joined, sensitive) {
			t.Fatalf("selection report leaked source text %q: %#v", sensitive, report)
		}
	}
}

func TestSelectTurnToolsAddsRelevantCapabilityDomains(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		included []string
		excluded []string
	}{
		{name: "web", message: "搜索一下今天的最新新闻", included: []string{"web_search", "web_crawl"}, excluded: []string{"agent_spawn"}},
		{name: "images", message: "分析这张图片并生成一张新海报", included: []string{"image_generate", "image_analyze"}, excluded: []string{"task_create_plan"}},
		{name: "upload", message: "请求用户上传一份 PDF 附件", included: []string{"interaction_request_upload"}, excluded: []string{"interaction_request_plan_approval"}},
		{name: "task plan", message: "制定一个多步骤执行计划", included: []string{"task_create_plan", "task_update_items", "interaction_request_plan_approval"}, excluded: []string{"image_generate"}},
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

func TestSelectTurnToolsKeepsStatefulDomainsWhenContextRequiresThem(t *testing.T) {
	selected := SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload:   json.RawMessage(`{"content":[{"type":"text","text":"继续"}]}`),
		HasActivePlan: true,
		HasImages:     true,
	})
	names := selectedToolNames(selected)

	assertSelected(t, names, "task_get_plan", true)
	assertSelected(t, names, "task_complete_plan", true)
	assertSelected(t, names, "interaction_request_plan_approval", true)
	assertSelected(t, names, "image_analyze", true)
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

func TestSelectTurnToolsLoadsDomainsRequiredBySkillInstructions(t *testing.T) {
	selected := SelectTurnTools(tools.DefaultRegistry(), tools.ConfigPolicy{}, TurnToolSelection{
		UserPayload:     json.RawMessage(`{"content":[{"type":"text","text":"执行当前技能"}]}`),
		HasActiveSkills: true,
		SkillContext:    json.RawMessage(`{"instructions":"Call image_generate, then maintain progress with task_create_plan."}`),
	})
	names := selectedToolNames(selected)

	assertSelected(t, names, "image_generate", true)
	assertSelected(t, names, "task_create_plan", true)
	assertSelected(t, names, "interaction_request_plan_approval", true)
}

func TestSelectTurnToolsKeepsPlatformDefaultsForExplicitConfiguration(t *testing.T) {
	registry, policy := tools.DefaultRegistry().Configured(json.RawMessage(`{"tools":["web_search"]}`))
	selected, report := SelectTurnToolsWithReport(registry, policy, TurnToolSelection{
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"只整理本地文件"}]}`),
	})
	names := selectedToolNames(selected)

	for _, name := range []string{"default_read_file", "web_search", "skills_search", "agent_spawn"} {
		if !names[name] {
			t.Fatalf("expected platform default %q to remain enabled, got %#v", name, names)
		}
	}
	if report.Mode != "explicit" || report.CandidateToolCount != report.SelectedToolCount || !reflect.DeepEqual(report.Triggers, []string{"explicit_config"}) {
		t.Fatalf("unexpected explicit selection report: %#v", report)
	}
}

func TestSelectTurnToolsAllowsExplicitlyToollessAgent(t *testing.T) {
	registry, policy := tools.DefaultRegistry().Configured(json.RawMessage(`{"disable_platform_defaults":true}`))
	selected, report := SelectTurnToolsWithReport(registry, policy, TurnToolSelection{
		UserPayload:     json.RawMessage(`{"content":[{"type":"text","text":"进行一次语音采访"}]}`),
		HasActiveSkills: true,
	})
	if names := selectedToolNames(selected); len(names) != 0 {
		t.Fatalf("expected explicitly tool-less agent, got %#v", names)
	}
	if report.Mode != "explicit" || report.CandidateToolCount != 0 || report.SelectedSchemaBytes != 0 || report.SelectedSchemaTokens != 0 {
		t.Fatalf("unexpected tool-less selection report: %#v", report)
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
