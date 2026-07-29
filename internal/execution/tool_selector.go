package execution

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

const (
	toolSelectionHistoryMessages = 4
	toolSelectionMaxTextChars    = 12000
)

// TurnToolSelection describes the stable inputs used to narrow the default
// registry for one durable turn. Explicit Agent tool configuration bypasses
// automatic selection.
type TurnToolSelection struct {
	UserPayload     json.RawMessage
	History         []managedagents.ConversationMessage
	SummaryText     string
	HasActiveSkills bool
	HasActivePlan   bool
	HasImages       bool
	SkillContext    json.RawMessage
}

// SelectTurnTools keeps the common tool surface small while preserving every
// explicitly configured or extension-provided tool. Selection is frozen for
// the whole durable turn by Agent Core's tool snapshot.
func SelectTurnTools(registry tools.Registry, policy tools.ConfigPolicy, request TurnToolSelection) tools.Registry {
	if policy.Explicit {
		return registry
	}

	text := toolSelectionText(request.UserPayload, request.History, request.SummaryText)
	skillText := strings.ToLower(string(request.SkillContext))
	wantsWeb := containsAny(text, webIntentSignals) || containsAny(skillText, webToolSignals)
	wantsSkillManagement := containsAny(text, skillIntentSignals)
	wantsImages := request.HasImages || containsAny(text, imageIntentSignals) || containsAny(skillText, imageToolSignals)
	wantsUpload := containsAny(text, uploadIntentSignals) || containsAny(skillText, uploadToolSignals)
	wantsTaskPlan := request.HasActivePlan || containsAny(text, taskPlanIntentSignals) || containsAny(skillText, taskToolSignals)
	wantsDiscussion := containsAny(text, discussionIntentSignals)
	wantsGroup := wantsDiscussion || containsAny(text, groupIntentSignals)
	wantsAgent := wantsGroup || containsAny(text, agentIntentSignals)

	return registry.FilterAPIs(func(manifest tools.Manifest, api tools.API) bool {
		switch manifest.Identifier {
		case tools.DefaultIdentifier, tools.ArtifactIdentifier:
			return true
		case tools.ImageIdentifier:
			return wantsImages
		case tools.InteractionIdentifier:
			switch api.Name {
			case tools.InteractionAPIAskUser:
				return true
			case tools.InteractionAPIRequestUpload:
				return wantsUpload
			case tools.InteractionAPIRequestPlanApproval:
				return wantsTaskPlan
			default:
				return false
			}
		case tools.TaskIdentifier:
			return wantsTaskPlan
		case tools.WebIdentifier:
			return wantsWeb
		case tools.SkillsIdentifier:
			if wantsSkillManagement {
				return true
			}
			return request.HasActiveSkills && (api.Name == "inspect" || api.Name == "read_asset")
		case tools.AgentIdentifier:
			switch {
			case isDiscussionAPI(api.Name):
				return wantsDiscussion
			case isGroupAPI(api.Name):
				return wantsGroup
			default:
				return wantsAgent
			}
		default:
			// MCP servers and plugins own their semantics, so keep their complete
			// namespace available unless the Agent explicitly configured tools.
			return true
		}
	})
}

func toolSelectionText(payload json.RawMessage, history []managedagents.ConversationMessage, summary string) string {
	current := payloadText(payload)
	if !isContinuationMessage(current) {
		return strings.ToLower(truncateSelectionText(current))
	}

	parts := []string{current}
	remaining := toolSelectionHistoryMessages
	userMessages := 0
	for index := len(history) - 1; index >= 0 && remaining > 0; index-- {
		message := history[index]
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		text := payloadText(message.Payload)
		if strings.TrimSpace(text) == "" || text == current {
			continue
		}
		parts = append(parts, text)
		userMessages++
		remaining--
	}
	if userMessages == 0 && strings.TrimSpace(summary) != "" {
		parts = append(parts, summary)
	}
	return strings.ToLower(truncateSelectionText(strings.Join(parts, "\n")))
}

func payloadText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw)
	}
	parts := make([]string, 0, len(payload.Content)+2)
	if strings.TrimSpace(payload.Message) != "" {
		parts = append(parts, payload.Message)
	}
	if strings.TrimSpace(payload.Text) != "" {
		parts = append(parts, payload.Text)
	}
	for _, part := range payload.Content {
		if part.Type == "" || strings.EqualFold(part.Type, "text") || strings.EqualFold(part.Type, "input_text") {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func truncateSelectionText(value string) string {
	if utf8.RuneCountInString(value) <= toolSelectionMaxTextChars {
		return value
	}
	runes := []rune(value)
	return string(runes[len(runes)-toolSelectionMaxTextChars:])
}

func isContinuationMessage(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.Trim(normalized, " .,!?:;，。！？：；")
	if utf8.RuneCountInString(normalized) > 32 {
		return false
	}
	return containsAny(normalized, continuationSignals)
}

func containsAny(value string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(value, signal) {
			return true
		}
	}
	return false
}

func isGroupAPI(name string) bool {
	return strings.Contains(name, "group")
}

func isDiscussionAPI(name string) bool {
	return strings.Contains(name, "discussion")
}

var continuationSignals = []string{
	"继续", "接着", "然后呢", "下一步", "照做", "按这个", "好的", "好", "可以", "没问题",
	"continue", "go on", "proceed", "next", "do it", "sounds good", "ok", "okay", "yes",
}

var webIntentSignals = []string{
	"http://", "https://", "www.", "上网", "联网", "网络搜索", "搜索网页", "网页搜索", "搜索引擎",
	"查一下", "搜一下", "浏览网页", "打开网页", "抓取网页", "官网", "新闻", "最新消息", "实时信息",
	"web search", "search the web", "internet", "browse", "crawl", "website", "webpage", "latest news", "online search",
}

var webToolSignals = []string{
	"web_search", "web_crawl", "联网", "网页搜索", "search the web",
}

var imageIntentSignals = []string{
	"图片", "图像", "照片", "插画", "海报", "图标", "看图", "识图", "画一个", "生成图", "编辑图",
	"image", "photo", "illustration", "poster", "icon", "analyze the image", "generate an image", "edit the image",
}

var imageToolSignals = []string{
	"image_generate", "image_analyze", "生成图片", "分析图片",
}

var uploadIntentSignals = []string{
	"上传", "上传文件", "提供文件", "提供附件", "需要附件", "向用户要文件",
	"upload", "attach a file", "attachment", "request a file", "request_upload",
}

var uploadToolSignals = []string{
	"interaction_request_upload", "request_upload", "请求上传",
}

var taskPlanIntentSignals = []string{
	"任务计划", "执行计划", "制定计划", "分步骤", "多步骤", "待办", "todo", "task plan", "execution plan", "multi-step",
	"端到端", "系统性", "全面调研", "完整实现", "重构", "迁移", "部署", "上线",
	"end-to-end", "systematic", "refactor", "migration", "migrate", "rollout", "deploy",
}

var taskToolSignals = []string{
	"task_create_plan", "task_update_items", "task_get_plan", "task_complete_plan", "task_cancel_plan",
}

var skillIntentSignals = []string{
	"技能", "skill", "插件", "plugin", "扩展", "extension", "市场", "marketplace",
	"安装技能", "启用技能", "禁用技能", "删除技能", "更新技能", "创建技能",
}

var agentIntentSignals = []string{
	"子智能体", "多智能体", "智能体协作", "代理协作", "委派", "分工", "并行处理", "并行执行",
	"subagent", "sub-agent", "multi-agent", "delegate", "delegation", "parallel agents", "agent collaboration",
}

var groupIntentSignals = []string{
	"任务组", "智能体组", "代理组", "分组执行", "批量委派", "小组协作",
	"task group", "agent group", "group execution", "group run",
}

var discussionIntentSignals = []string{
	"多方讨论", "智能体讨论", "圆桌讨论", "辩论", "头脑风暴", "讨论组",
	"agent discussion", "panel discussion", "debate", "brainstorm", "roundtable",
}
