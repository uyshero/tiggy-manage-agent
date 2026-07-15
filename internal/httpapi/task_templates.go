package httpapi

import "net/http"

type workbenchTaskTemplate struct {
	ID            string                      `json:"id"`
	Title         string                      `json:"title"`
	Category      string                      `json:"category"`
	Description   string                      `json:"description"`
	Prompt        string                      `json:"prompt"`
	Tools         []string                    `json:"tools,omitempty"`
	Skills        []string                    `json:"skills,omitempty"`
	WorkflowSteps []workbenchTaskTemplateStep `json:"workflow_steps,omitempty"`
}

type workbenchTaskTemplateStep struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Instruction string `json:"instruction"`
}

var builtinWorkbenchTaskTemplates = []workbenchTaskTemplate{
	{
		ID:          "ai_news_digest",
		Title:       "AI 新闻汇总",
		Category:    "调研",
		Description: "检索可信来源，提炼关键动态并生成可交付的新闻简报。",
		Prompt:      "汇总最近 24 小时最值得关注的 AI 新闻，覆盖模型、产品、开源、融资和监管动态。每条包含来源链接、发布时间、事实摘要和影响判断，最终生成 Markdown 简报。",
		Tools:       []string{"web", "default"},
		Skills:      []string{"ai-news", "research"},
		WorkflowSteps: []workbenchTaskTemplateStep{
			{ID: "search", Title: "检索", Instruction: "检索最近 24 小时的权威来源，去重并保留发布时间、标题和原始链接。"},
			{ID: "analyze", Title: "分析", Instruction: "核对来源并判断重要性，提炼事实、影响和不确定信息，避免把传闻当作事实。"},
			{ID: "generate", Title: "生成简报", Instruction: "按重要性组织内容，生成结构清晰的 Markdown 新闻简报并保存为结果文件。"},
			{ID: "review", Title: "审核", Instruction: "复核链接、日期、重复项和事实表述，修正问题并给出最终摘要。"},
		},
	},
	{
		ID:          "code_review",
		Title:       "代码审查",
		Category:    "代码",
		Description: "读取变更、验证行为并输出按严重程度排序的审查报告。",
		Prompt:      "审查当前代码变更，优先发现真实缺陷、行为回归、安全风险和缺失测试。运行相关检查，并输出带文件位置和修复建议的 Markdown 审查报告。",
		Tools:       []string{"default"},
		Skills:      []string{"code-review"},
		WorkflowSteps: []workbenchTaskTemplateStep{
			{ID: "inspect", Title: "读取变更", Instruction: "读取仓库约束和当前 diff，识别受影响模块、接口和关键行为。"},
			{ID: "analyze", Title: "风险分析", Instruction: "逐项检查缺陷、回归、安全、并发和数据一致性风险，记录可复现证据。"},
			{ID: "verify", Title: "运行验证", Instruction: "运行与变更范围匹配的测试、静态检查或构建，记录失败与覆盖缺口。"},
			{ID: "report", Title: "生成报告", Instruction: "按严重程度排序发现，给出文件位置、影响、修复建议和剩余风险。"},
		},
	},
	{
		ID:          "document_generation",
		Title:       "文档生成",
		Category:    "文档",
		Description: "收集材料、设计结构、生成文件并完成一致性审核。",
		Prompt:      "根据工作区现有材料生成一份结构完整、面向业务读者的正式文档。先梳理事实和受众，再生成文件，最后检查术语、引用、结构和格式一致性。",
		Tools:       []string{"default", "web"},
		Skills:      []string{"documents"},
		WorkflowSteps: []workbenchTaskTemplateStep{
			{ID: "collect", Title: "收集材料", Instruction: "读取工作区相关材料，提取可验证事实、约束、受众和交付格式。"},
			{ID: "outline", Title: "设计结构", Instruction: "形成章节结构、关键信息层级和待补充信息清单。"},
			{ID: "generate", Title: "生成文件", Instruction: "按既定结构生成正式文档文件，确保内容完整且可直接交付。"},
			{ID: "review", Title: "审核", Instruction: "检查事实、术语、引用、格式和前后表述一致性，修正后输出最终文件。"},
		},
	},
	{
		ID:          "data_cleanup",
		Title:       "数据整理",
		Category:    "数据",
		Description: "检查结构和质量，清洗数据并输出可追溯结果。",
		Prompt:      "整理工作区中的数据文件：识别字段和质量问题，统一类型与格式，处理重复值和缺失值，输出清洗后的文件以及变更说明。不要覆盖原始数据。",
		Tools:       []string{"default"},
		Skills:      []string{"spreadsheets"},
		WorkflowSteps: []workbenchTaskTemplateStep{
			{ID: "profile", Title: "数据剖析", Instruction: "读取数据文件，识别字段、类型、缺失、重复、异常值和格式不一致。"},
			{ID: "plan", Title: "制定规则", Instruction: "根据数据用途提出明确清洗规则，说明不可逆变更和需要保留的原始信息。"},
			{ID: "transform", Title: "执行整理", Instruction: "在保留原始文件的前提下执行清洗，生成新的结构化结果文件。"},
			{ID: "validate", Title: "质量审核", Instruction: "复核行数、字段、类型、重复和缺失情况，生成变更说明与质量摘要。"},
		},
	},
}

func (s *Server) listWorkbenchTaskTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"templates": builtinWorkbenchTaskTemplates})
}
