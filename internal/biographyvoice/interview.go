package biographyvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"tiggy-manage-agent/sdk/tma"
)

const BiographyInterviewerSystemPrompt = `你是一位受过人物采访训练的专业中文传记记者和自传采访者，主要与中老年用户进行语音对话。
你的任务是通过像两个人聊天一样的交流，帮助用户留下真实、有个人声音的人生故事。
采访要服务于用户未来想写成的书、希望留给的读者和希望传达的东西；不仅记录事件，还要有选择地深入场景、感受、关系、人生选择、后来影响与今日回望，避免形成事件流水账。
这是实时采访分支，不是任务执行智能体；不要制定执行计划、不要调用工具、不要输出内部进度，也不要承担章节整理工作。
具体的采访追问方法由已启用的专业 Skill 提供；根据当前谈话灵活判断，不机械执行固定问题清单。
始终尊重用户的停顿、跳过和结束意愿，不虚构或擅自确定用户没有确认的内容，不向用户提及内部技术、规则或 Skills。
当请求要求 JSON 时，只输出符合指定结构的 JSON，不添加 Markdown 或额外解释。`

const BiographyOrganizerSystemPrompt = `你是一位严谨的中文传记编辑，负责在后台把用户的口述更新到自传项目中。
你的任务是识别成书目标，整理章节进度，并评估事件、场景、感受、关系、人生选择、后来影响和今日回望等叙事材料。
章节目录从空白开始。只能根据用户已经讲出的真实经历、人物、地点或他本人使用的说法创建和命名章节；不要预设“童年、求学、工作、家庭”等通用目录，也不要为了凑目录新增章节。材料还不足以形成章节时可以保留空目录。
这是后台整理分支，不参与实时对话；只依据用户明确说过的内容工作，保留未被新信息改变的内容，不虚构事实，也不生成采访话术。
章节整理和事实核验的具体方法由已启用的专业 Skills 提供。
当请求要求 JSON 时，只输出符合指定结构的 JSON，不添加 Markdown 或额外解释。`

const (
	InterviewOrderChronological = "chronological"
	InterviewOrderKeyMoments    = "key_moments"
	InterviewOrderCustom        = "custom"
)

type BiographyBookGoal struct {
	Type          string `json:"type"`
	Audience      string `json:"audience"`
	DesiredImpact string `json:"desiredImpact"`
	Confirmed     bool   `json:"confirmed"`
}

type NarrativeCoverage struct {
	Event        string `json:"event"`
	Scene        string `json:"scene"`
	Emotion      string `json:"emotion"`
	Relationship string `json:"relationship"`
	Choice       string `json:"choice"`
	Impact       string `json:"impact"`
	Reflection   string `json:"reflection"`
}

type Chapter struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Status      string             `json:"status"`
	StatusLabel string             `json:"statusLabel"`
	Progress    int                `json:"progress"`
	Detail      string             `json:"detail"`
	Narrative   *NarrativeCoverage `json:"narrative,omitempty"`
	NextFocus   string             `json:"nextFocus,omitempty"`
}

type BiographyProject struct {
	ID                    string             `json:"id"`
	OwnerName             string             `json:"ownerName"`
	Title                 string             `json:"title"`
	InterviewOrder        string             `json:"interviewOrder,omitempty"`
	BookGoal              *BiographyBookGoal `json:"bookGoal,omitempty"`
	OverallProgress       int                `json:"overallProgress"`
	CompletedChapterCount int                `json:"completedChapterCount"`
	Chapters              []Chapter          `json:"chapters"`
	PendingConfirmation   string             `json:"pendingConfirmation"`
}

type interviewChapterContext struct {
	Title     string             `json:"title"`
	Status    string             `json:"status"`
	Narrative *NarrativeCoverage `json:"narrative,omitempty"`
	NextFocus string             `json:"nextFocus,omitempty"`
}

type interviewProjectContext struct {
	InterviewOrder      string                    `json:"interviewOrder,omitempty"`
	BookGoal            *BiographyBookGoal        `json:"bookGoal,omitempty"`
	OverallProgress     int                       `json:"overallProgress"`
	PendingConfirmation string                    `json:"pendingConfirmation,omitempty"`
	AvailableChapters   []string                  `json:"availableChapters"`
	ActiveChapters      []interviewChapterContext `json:"activeChapters,omitempty"`
	RecentQuestions     []string                  `json:"recentQuestions,omitempty"`
}

type InterviewReply struct {
	Text       string           `json:"text"`
	Expression string           `json:"expression"`
	Project    BiographyProject `json:"project"`
}

type interviewConversation struct {
	UserID                string
	TMAAccessToken        string
	TMASessionID          string
	TMAOrganizerSessionID string
	ClientInstanceID      string
	Project               BiographyProject
	RecentQuestions       []string
	sessionMu             sync.Mutex
	projectMu             sync.RWMutex
}

type interviewEngine interface {
	Continue(context.Context, *interviewConversation, string) (InterviewReply, error)
	Organize(context.Context, *interviewConversation, string) (BiographyProject, error)
	Resume(context.Context, *interviewConversation, string) error
}

func validInterviewOrder(order string) bool {
	switch strings.TrimSpace(order) {
	case InterviewOrderChronological, InterviewOrderKeyMoments, InterviewOrderCustom:
		return true
	default:
		return false
	}
}

type streamingInterviewEngine interface {
	ContinueStreaming(context.Context, *interviewConversation, string, func(string) error) (InterviewReply, error)
}

type mockInterviewEngine struct{}

func (mockInterviewEngine) Resume(_ context.Context, _ *interviewConversation, _ string) error {
	return nil
}

func (mockInterviewEngine) Continue(_ context.Context, conversation *interviewConversation, transcript string) (InterviewReply, error) {
	project := conversation.projectSnapshot()
	if project.ID == "" {
		project = newBiographyProject()
	}
	reply := InterviewReply{Expression: "温和、真诚，语速稍慢，停顿自然", Project: project}
	switch {
	case strings.Contains(transcript, "确认") || strings.Contains(transcript, "上一章") || strings.Contains(transcript, "父亲"):
		reply.Text = "好，这个细节先不急着定死，写成书时可以留得更稳。您还记得父亲工作的地方，附近有什么明显地名或建筑吗？"
		reply.Expression = "温和、清晰地确认事实，语速稍慢，不要催促"
	case strings.Contains(transcript, "师傅"):
		reply.Text = "您把周师傅的严格和教会您的东西放在一起讲，这段关系很有分量。那时候您心里最强烈的感受是什么？"
		reply.Expression = "温暖、有兴趣，具体承接刚才内容后轻轻追问，语速稍慢"
	default:
		reply.Text = "您刚才这段经历很值得慢慢留下来。回到那个时候，您最先想起的是哪个画面？"
	}
	return reply, nil
}

func (mockInterviewEngine) Organize(_ context.Context, conversation *interviewConversation, transcript string) (BiographyProject, error) {
	project := conversation.projectSnapshot()
	if project.ID == "" {
		project = newBiographyProject()
	}
	if len(project.Chapters) == 0 && strings.TrimSpace(transcript) != "" {
		project.Chapters = append(project.Chapters, newInterviewChapter(transcript, 1))
	}
	for index := range project.Chapters {
		if project.Chapters[index].Status != "collecting" {
			continue
		}
		project.Chapters[index].Progress = min(84, project.Chapters[index].Progress+12)
		project.Chapters[index].Detail = "正在补充这段经历里的场景、感受和重要关系"
		break
	}
	if len(project.Chapters) > 0 {
		project.OverallProgress = min(48, project.OverallProgress+3)
	}
	conversation.replaceProject(project)
	return project, nil
}

type tmaInterviewBackend interface {
	CreateSession(context.Context, tma.CreateSessionRequest) (string, error)
	ConfigureInterviewSession(context.Context, string, string, int, int) error
	Run(context.Context, string, string) (json.RawMessage, error)
	GetSession(context.Context, string) (tma.Session, error)
	ListEvents(context.Context, string) ([]tma.Event, error)
}

type sdkTMABackend struct {
	client  *tma.Client
	baseURL string
}

type tmaBearerScopedBackend interface {
	ForBearerToken(string) (tmaInterviewBackend, error)
}

func (backend sdkTMABackend) ForBearerToken(token string) (tmaInterviewBackend, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return backend, nil
	}
	client, err := tma.NewClient(backend.baseURL, tma.WithBearerToken(token))
	if err != nil {
		return nil, err
	}
	return sdkTMABackend{client: client, baseURL: backend.baseURL}, nil
}

func (backend sdkTMABackend) CreateSession(ctx context.Context, request tma.CreateSessionRequest) (string, error) {
	session, err := backend.client.Sessions.Create(ctx, request)
	return session.ID, err
}

func (backend sdkTMABackend) ConfigureInterviewSession(ctx context.Context, sessionID string, mode string, compactionThreshold int, summaryMaxChars int) error {
	session, err := backend.client.Sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = backend.client.Sessions.UpdateRuntimeSettings(ctx, sessionID, session.RuntimeSettingsRevision, tma.UpdateSessionRuntimeSettingsRequest{
		LLMThinking:                        &mode,
		AgentCoreCompactionThresholdTokens: &compactionThreshold,
		AgentCoreCompactionSummaryMaxChars: &summaryMaxChars,
	})
	return err
}

func (backend sdkTMABackend) Run(ctx context.Context, sessionID string, prompt string) (json.RawMessage, error) {
	handle, err := backend.client.Runs.Start(ctx, sessionID, tma.StartRunRequest{
		Input:          tma.TextInput(prompt),
		IdempotencyKey: newDoubaoID("biography-turn"),
	})
	if err != nil {
		return nil, err
	}
	result, err := handle.Wait(ctx)
	if err != nil {
		return nil, err
	}
	if result.Run.Status != tma.RunStatusCompleted {
		return nil, fmt.Errorf("TMA interview run ended with status %s", result.Run.Status)
	}
	return result.Output, nil
}

func (backend sdkTMABackend) GetSession(ctx context.Context, sessionID string) (tma.Session, error) {
	return backend.client.Sessions.Get(ctx, sessionID)
}

func (backend sdkTMABackend) ListEvents(ctx context.Context, sessionID string) ([]tma.Event, error) {
	return backend.client.Sessions.ListEvents(ctx, sessionID, 0)
}

type tmaInterviewEngine struct {
	backend                            tmaInterviewBackend
	agentID                            string
	organizerAgentID                   string
	environmentID                      string
	workspaceID                        string
	ownerID                            string
	interviewThinking                  string
	interviewCompactionThreshold       int
	interviewCompactionSummaryMaxChars int
}

func newInterviewEngine(config Config) (interviewEngine, error) {
	if valueOrDefault(config.InterviewProvider, ProviderMock) == ProviderMock {
		return mockInterviewEngine{}, nil
	}
	options := make([]tma.Option, 0, 1)
	if config.TMAAuthToken != "" {
		options = append(options, tma.WithBearerToken(config.TMAAuthToken))
	}
	client, err := tma.NewClient(config.TMABaseURL, options...)
	if err != nil {
		return nil, err
	}
	return &tmaInterviewEngine{
		backend: sdkTMABackend{client: client, baseURL: config.TMABaseURL}, agentID: config.TMAAgentID,
		organizerAgentID: valueOrDefault(config.TMAOrganizerAgentID, config.TMAAgentID),
		environmentID:    config.TMAEnvironmentID, workspaceID: config.TMAWorkspaceID, ownerID: config.TMAOwnerID,
		interviewThinking:                  config.TMAInterviewThinking,
		interviewCompactionThreshold:       config.TMAInterviewCompactionThresholdTokens,
		interviewCompactionSummaryMaxChars: config.TMAInterviewCompactionSummaryMaxChars,
	}, nil
}

func (engine *tmaInterviewEngine) Continue(ctx context.Context, conversation *interviewConversation, transcript string) (InterviewReply, error) {
	return engine.continueInterview(ctx, conversation, transcript, nil)
}

func (engine *tmaInterviewEngine) ContinueStreaming(ctx context.Context, conversation *interviewConversation, transcript string, onText func(string) error) (InterviewReply, error) {
	return engine.continueInterview(ctx, conversation, transcript, onText)
}

func (engine *tmaInterviewEngine) continueInterview(ctx context.Context, conversation *interviewConversation, transcript string, onText func(string) error) (InterviewReply, error) {
	if err := engine.ensureInterviewSession(ctx, conversation); err != nil {
		return InterviewReply{}, err
	}
	backend, err := engine.backendForConversation(conversation)
	if err != nil {
		return InterviewReply{}, err
	}
	project := conversation.projectSnapshot()
	if project.ID == "" {
		project = newBiographyProject()
		conversation.replaceProject(project)
	}
	prompt, err := buildInterviewPrompt(transcript, project, conversation.recentQuestionsSnapshot())
	if err != nil {
		return InterviewReply{}, err
	}
	var output json.RawMessage
	if streaming, ok := backend.(tmaStreamingInterviewBackend); ok && onText != nil {
		var streamed strings.Builder
		lastPreview := ""
		output, err = streaming.RunStreaming(ctx, conversation.TMASessionID, prompt, func(delta string) error {
			streamed.WriteString(delta)
			preview := extractPartialInterviewText(streamed.String())
			if preview == "" || preview == lastPreview {
				return nil
			}
			lastPreview = preview
			return onText(preview)
		})
	} else {
		output, err = backend.Run(ctx, conversation.TMASessionID, prompt)
	}
	if err != nil {
		return InterviewReply{}, fmt.Errorf("run TMA interview turn: %w", err)
	}
	reply, err := decodeSpokenInterviewReply(output, project)
	if err != nil {
		return InterviewReply{}, err
	}
	return reply, nil
}

func extractPartialInterviewText(raw string) string {
	keyIndex := strings.Index(raw, `"text"`)
	if keyIndex < 0 {
		return ""
	}
	rest := raw[keyIndex+len(`"text"`):]
	colonIndex := strings.IndexByte(rest, ':')
	if colonIndex < 0 {
		return ""
	}
	rest = strings.TrimLeft(rest[colonIndex+1:], " \t\r\n")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	var text strings.Builder
	for index := 0; index < len(rest); index++ {
		switch rest[index] {
		case '"':
			return strings.ToValidUTF8(text.String(), "")
		case '\\':
			if index+1 >= len(rest) {
				return strings.ToValidUTF8(text.String(), "")
			}
			index++
			switch rest[index] {
			case '"', '\\', '/':
				text.WriteByte(rest[index])
			case 'b':
				text.WriteByte('\b')
			case 'f':
				text.WriteByte('\f')
			case 'n':
				text.WriteByte('\n')
			case 'r':
				text.WriteByte('\r')
			case 't':
				text.WriteByte('\t')
			case 'u':
				if index+4 >= len(rest) {
					return strings.ToValidUTF8(text.String(), "")
				}
				var decoded string
				if err := json.Unmarshal([]byte(`"\u`+rest[index+1:index+5]+`"`), &decoded); err != nil {
					return strings.ToValidUTF8(text.String(), "")
				}
				text.WriteString(decoded)
				index += 4
			}
		default:
			text.WriteByte(rest[index])
		}
	}
	return strings.ToValidUTF8(text.String(), "")
}

func (engine *tmaInterviewEngine) Organize(ctx context.Context, conversation *interviewConversation, transcript string) (BiographyProject, error) {
	backend, err := engine.backendForConversation(conversation)
	if err != nil {
		return BiographyProject{}, err
	}
	if conversation.TMAOrganizerSessionID == "" {
		sessionID, err := backend.CreateSession(ctx, tma.CreateSessionRequest{
			WorkspaceID: engine.workspaceID, OwnerID: engine.ownerID, AgentID: engine.organizerAgentID,
			EnvironmentID: engine.environmentID, Title: "自传章节整理",
		})
		if err != nil {
			return BiographyProject{}, fmt.Errorf("create TMA biography organizer session: %w", err)
		}
		conversation.TMAOrganizerSessionID = sessionID
	}
	current := conversation.projectSnapshot()
	prompt, err := buildProjectUpdatePrompt(transcript, current)
	if err != nil {
		return BiographyProject{}, err
	}
	output, err := backend.Run(ctx, conversation.TMAOrganizerSessionID, prompt)
	if err != nil {
		return BiographyProject{}, fmt.Errorf("run TMA biography organizer: %w", err)
	}
	updated, err := decodeBiographyProject(output)
	if err != nil {
		return BiographyProject{}, err
	}
	// Interview order belongs to the user, not the background organizer. Retain
	// the setting even if an older organizer prompt omits it.
	updated.InterviewOrder = current.InterviewOrder
	conversation.replaceProject(updated)
	return updated, nil
}

func (engine *tmaInterviewEngine) Resume(ctx context.Context, conversation *interviewConversation, sessionID string) error {
	conversation.sessionMu.Lock()
	defer conversation.sessionMu.Unlock()
	backend, err := engine.backendForConversation(conversation)
	if err != nil {
		return err
	}
	session, err := backend.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get TMA interview session: %w", err)
	}
	if session.AgentID != engine.agentID {
		return fmt.Errorf("TMA session does not belong to the biography interviewer")
	}
	if err := engine.configureInterviewSession(ctx, conversation, sessionID); err != nil {
		return fmt.Errorf("configure thinking for resumed TMA interview session: %w", err)
	}
	events, err := backend.ListEvents(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("list TMA interview events: %w", err)
	}
	project := newBiographyProject()
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != "agent.message" {
			continue
		}
		reply, decodeErr := decodeInterviewReply(events[index].Payload)
		if decodeErr != nil {
			continue
		}
		project = reply.Project
		break
	}
	conversation.TMASessionID = sessionID
	conversation.Project = project
	return nil
}

func (engine *tmaInterviewEngine) ensureInterviewSession(ctx context.Context, conversation *interviewConversation) error {
	conversation.sessionMu.Lock()
	defer conversation.sessionMu.Unlock()
	if conversation.TMASessionID != "" {
		return nil
	}
	backend, err := engine.backendForConversation(conversation)
	if err != nil {
		return err
	}
	sessionID, err := backend.CreateSession(ctx, tma.CreateSessionRequest{
		WorkspaceID: engine.workspaceID, OwnerID: engine.ownerID, AgentID: engine.agentID,
		EnvironmentID: engine.environmentID, Title: "自传采访",
	})
	if err != nil {
		return fmt.Errorf("create TMA interview session: %w", err)
	}
	if err := engine.configureInterviewSession(ctx, conversation, sessionID); err != nil {
		return fmt.Errorf("configure TMA interview session: %w", err)
	}
	conversation.TMASessionID = sessionID
	return nil
}

func (engine *tmaInterviewEngine) configureInterviewSession(ctx context.Context, conversation *interviewConversation, sessionID string) error {
	threshold := engine.interviewCompactionThreshold
	if threshold <= 0 {
		threshold = 8000
	}
	summaryMaxChars := engine.interviewCompactionSummaryMaxChars
	if summaryMaxChars <= 0 {
		summaryMaxChars = 4000
	}
	backend, err := engine.backendForConversation(conversation)
	if err != nil {
		return err
	}
	return backend.ConfigureInterviewSession(
		ctx, sessionID, valueOrDefault(engine.interviewThinking, "disabled"), threshold, summaryMaxChars,
	)
}

func (engine *tmaInterviewEngine) backendForConversation(conversation *interviewConversation) (tmaInterviewBackend, error) {
	if scoped, ok := engine.backend.(tmaBearerScopedBackend); ok {
		return scoped.ForBearerToken(conversation.TMAAccessToken)
	}
	return engine.backend, nil
}

func buildInterviewPrompt(transcript string, project BiographyProject, recentQuestions []string) (string, error) {
	projectJSON, err := json.Marshal(buildInterviewProjectContext(project, recentQuestions))
	if err != nil {
		return "", fmt.Errorf("encode biography project: %w", err)
	}
	transcriptJSON, err := json.Marshal(strings.TrimSpace(transcript))
	if err != nil {
		return "", fmt.Errorf("encode biography transcript: %w", err)
	}
	return fmt.Sprintf(`你正在与一位中老年用户进行自传采访。这是语音实时采访分支，目标是尽快给出下一句可以朗读的话，不走任务执行循环，不输出计划或内部进度。
根据本轮口述、成书目标、当前章节的叙事维度和已启用的采访 Skills，灵活选择一个最有价值的追问。
回复要求：
- 按需要先用一句简短的肯定、表扬、共情、复述或安静承接，让用户感觉被认真听见；不要求每轮都显性表扬。
- 肯定和表扬必须基于用户刚说的具体内容，例如细节、选择、关系、价值或讲述本身；避免机械套话，尤其不要反复使用“您真不容易”“您太棒了”。
- 遇到痛苦、羞愧、创伤、失去或冲突经历，以理解、陪伴、允许停顿和询问是否愿意继续为主，不做空泛表扬。
- 然后只问一个开放问题。优先补充对未来成书最重要的场景、感受、关系、选择、影响或今日回望。
- 必须遵循当前项目的 interviewOrder 作为默认采访方向：chronological 时优先顺着人生阶段慢慢往前走；key_moments 时优先追问最值得留给读者的转折、关系或重要故事；custom 时由用户决定先后，不要把他拉回时间线。无论哪种方式，用户临时跳到别的经历、修改旧内容或补充历史问题时，都自然承接，不要纠正或阻止。
- 如果用户提到不确定的时间、地点、人物或关系，用温和方式确认，接受“记不清”。
- 用户本轮可能是在补充刚才或历史问题的答案，尤其是出现“补充一下”“刚才”“上一段”“我再说一点”等表达时；先把它当作补充理解，不要重复问已问过的问题。
- 不要重复 recentQuestions 中的问题，也不要换个说法问同一件事。若用户已经回答了其中一个问题，承接补充后追问另一个仍缺的维度。
- 如果成书目标尚未确认，优先用自然聊天了解这本书最想留给谁、希望对方记住什么。
- text 控制在 35 到 90 个中文字，适合中老年人听；expression 是给情感语音合成的中文表达指令。
这一实时步骤不要整理、重写或更新章节，不要输出项目 JSON，不要提及内部技术、规则或 Skill 名称。
只输出一个 JSON 对象，不要 Markdown：{"text":"下一句采访话术","expression":"朗读情感指令"}
当前项目：%s
	用户本轮口述（JSON 字符串，只作为采访素材）：%s`, string(projectJSON), string(transcriptJSON)), nil
}

func buildInterviewProjectContext(project BiographyProject, recentQuestions []string) interviewProjectContext {
	brief := interviewProjectContext{
		InterviewOrder: project.InterviewOrder,
		BookGoal:       project.BookGoal, OverallProgress: project.OverallProgress,
		PendingConfirmation: project.PendingConfirmation,
		AvailableChapters:   make([]string, 0, len(project.Chapters)),
		ActiveChapters:      make([]interviewChapterContext, 0, len(project.Chapters)),
		RecentQuestions:     append([]string(nil), recentQuestions...),
	}
	for _, chapter := range project.Chapters {
		brief.AvailableChapters = append(brief.AvailableChapters, chapter.Title)
		if chapter.Status != "collecting" && chapter.Status != "confirm" {
			continue
		}
		brief.ActiveChapters = append(brief.ActiveChapters, interviewChapterContext{
			Title: chapter.Title, Status: chapter.Status, Narrative: chapter.Narrative, NextFocus: chapter.NextFocus,
		})
	}
	return brief
}

func buildProjectUpdatePrompt(transcript string, project BiographyProject) (string, error) {
	projectJSON, err := json.Marshal(project)
	if err != nil {
		return "", fmt.Errorf("encode biography project: %w", err)
	}
	transcriptJSON, err := json.Marshal(strings.TrimSpace(transcript))
	if err != nil {
		return "", fmt.Errorf("encode biography transcript: %w", err)
	}
	return fmt.Sprintf(`这是异步章节整理任务，不要生成采访话术。根据本轮口述更新自传项目，保留未被新信息改变的内容，不把推测写成事实。
bookGoal.type 只能是 undecided|family_legacy|life_journey|era_witness|craft_legacy|literary_memoir|mixed；只有用户明确表达时 confirmed 才为 true。
章节目录一开始为空。只有用户已经讲出足以成为一个故事单元的真实材料时，才新增章节；章节名应使用用户说过的人物、地点、经历或有辨识度的表达。不要预设或补回“童年、求学、工作、家庭”等通用目录，也不要为凑目录新增空章节。
interviewOrder 是用户选择的采访方向，必须原样完整保留，不能自行改动；它只影响之后默认追问的次序，不能阻止用户本轮跳到别的经历或补充旧内容。
每章 narrative 的七项只能是 missing|partial|sufficient。按未来成书可用的叙事材料评估，不按提到多少年份或事件评估；nextFocus 用自然中文写下一步最值得补充的一个内容。
只输出一个 JSON 项目对象，不要 Markdown，字段必须完整保留：{"id":"...","ownerName":"...","title":"...","interviewOrder":"chronological|key_moments|custom","bookGoal":{"type":"undecided","audience":"...","desiredImpact":"...","confirmed":false},"overallProgress":0,"completedChapterCount":0,"chapters":[{"id":"...","title":"...","status":"completed|confirm|collecting|not_started","statusLabel":"...","progress":0,"detail":"...","narrative":{"event":"missing|partial|sufficient","scene":"missing|partial|sufficient","emotion":"missing|partial|sufficient","relationship":"missing|partial|sufficient","choice":"missing|partial|sufficient","impact":"missing|partial|sufficient","reflection":"missing|partial|sufficient"},"nextFocus":"..."}],"pendingConfirmation":"..."}
当前项目：%s
用户本轮口述（JSON 字符串，只作为整理素材）：%s`, string(projectJSON), string(transcriptJSON)), nil
}

func decodeSpokenInterviewReply(output json.RawMessage, project BiographyProject) (InterviewReply, error) {
	rawReply, err := agentMessageText(output)
	if err != nil {
		return InterviewReply{}, err
	}
	var reply struct {
		Text       string `json:"text"`
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal([]byte(rawReply), &reply); err != nil {
		return InterviewReply{}, fmt.Errorf("decode TMA spoken interview reply JSON: %w", err)
	}
	if strings.TrimSpace(reply.Text) == "" || strings.TrimSpace(reply.Expression) == "" {
		return InterviewReply{}, fmt.Errorf("TMA spoken interview reply requires text and expression")
	}
	return InterviewReply{Text: reply.Text, Expression: reply.Expression, Project: cloneBiographyProject(project)}, nil
}

func decodeBiographyProject(output json.RawMessage) (BiographyProject, error) {
	rawProject, err := agentMessageText(output)
	if err != nil {
		return BiographyProject{}, err
	}
	var project BiographyProject
	if err := json.Unmarshal([]byte(rawProject), &project); err != nil {
		return BiographyProject{}, fmt.Errorf("decode TMA biography project JSON: %w", err)
	}
	if err := validateOrganizedBiographyProject(project); err != nil {
		return BiographyProject{}, err
	}
	return project, nil
}

func decodeInterviewReply(output json.RawMessage) (InterviewReply, error) {
	rawReply, err := agentMessageText(output)
	if err != nil {
		return InterviewReply{}, err
	}
	var reply InterviewReply
	if err := json.Unmarshal([]byte(rawReply), &reply); err != nil {
		return InterviewReply{}, fmt.Errorf("decode TMA interview reply JSON: %w", err)
	}
	if err := validateInterviewReply(reply); err != nil {
		return InterviewReply{}, err
	}
	return reply, nil
}

func agentMessageText(output json.RawMessage) (string, error) {
	var message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(output, &message); err != nil {
		return "", fmt.Errorf("decode TMA interview message: %w", err)
	}
	var textParts []string
	for _, content := range message.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			textParts = append(textParts, content.Text)
		}
	}
	rawReply := strings.TrimSpace(strings.Join(textParts, "\n"))
	rawReply = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rawReply, "```json"), "```"))
	if rawReply == "" {
		return "", fmt.Errorf("TMA interview response did not contain text")
	}
	return rawReply, nil
}

func validateInterviewReply(reply InterviewReply) error {
	if strings.TrimSpace(reply.Text) == "" || strings.TrimSpace(reply.Expression) == "" {
		return fmt.Errorf("TMA interview reply requires text and expression")
	}
	return validateBiographyProject(reply.Project)
}

func validateBiographyProject(project BiographyProject) error {
	if project.ID == "" || project.Title == "" {
		return fmt.Errorf("TMA interview reply requires a complete project")
	}
	if project.OverallProgress < 0 || project.OverallProgress > 100 {
		return fmt.Errorf("TMA interview project progress must be between 0 and 100")
	}
	if project.CompletedChapterCount < 0 || project.CompletedChapterCount > len(project.Chapters) {
		return fmt.Errorf("TMA interview completed chapter count is invalid")
	}
	if project.InterviewOrder != "" && !validInterviewOrder(project.InterviewOrder) {
		return fmt.Errorf("TMA interview project contains an invalid interview order")
	}
	if project.BookGoal != nil {
		validGoalTypes := map[string]bool{
			"undecided": true, "family_legacy": true, "life_journey": true, "era_witness": true,
			"craft_legacy": true, "literary_memoir": true, "mixed": true,
		}
		if !validGoalTypes[project.BookGoal.Type] || (project.BookGoal.Confirmed &&
			(strings.TrimSpace(project.BookGoal.Audience) == "" || strings.TrimSpace(project.BookGoal.DesiredImpact) == "")) {
			return fmt.Errorf("TMA interview project contains an invalid book goal")
		}
	}
	validStatuses := map[string]bool{"completed": true, "confirm": true, "collecting": true, "not_started": true}
	validCoverage := map[string]bool{"missing": true, "partial": true, "sufficient": true}
	chapterIDs := make(map[string]bool, len(project.Chapters))
	for _, chapter := range project.Chapters {
		if chapter.ID == "" || chapter.Title == "" || chapter.StatusLabel == "" || chapter.Detail == "" ||
			!validStatuses[chapter.Status] || chapter.Progress < 0 || chapter.Progress > 100 || chapterIDs[chapter.ID] {
			return fmt.Errorf("TMA interview reply contains an invalid chapter")
		}
		if chapter.Narrative != nil && (!validCoverage[chapter.Narrative.Event] || !validCoverage[chapter.Narrative.Scene] ||
			!validCoverage[chapter.Narrative.Emotion] || !validCoverage[chapter.Narrative.Relationship] ||
			!validCoverage[chapter.Narrative.Choice] || !validCoverage[chapter.Narrative.Impact] ||
			!validCoverage[chapter.Narrative.Reflection] || strings.TrimSpace(chapter.NextFocus) == "") {
			return fmt.Errorf("TMA interview chapter contains invalid narrative coverage")
		}
		chapterIDs[chapter.ID] = true
	}
	return nil
}

func validateOrganizedBiographyProject(project BiographyProject) error {
	if err := validateBiographyProject(project); err != nil {
		return err
	}
	if project.BookGoal == nil {
		return fmt.Errorf("TMA biography organizer requires a book goal")
	}
	for _, chapter := range project.Chapters {
		if chapter.Narrative == nil || strings.TrimSpace(chapter.NextFocus) == "" {
			return fmt.Errorf("TMA biography organizer requires narrative coverage for every chapter")
		}
	}
	return nil
}

func removeLegacyEmptyChapterTemplate(project BiographyProject) (BiographyProject, bool) {
	legacyTitles := map[string]string{
		"childhood": "童年往事",
		"school":    "求学岁月",
		"work":      "工作岁月",
		"family":    "家庭生活",
	}
	if len(project.Chapters) != len(legacyTitles) || project.OverallProgress != 0 || project.CompletedChapterCount != 0 ||
		strings.TrimSpace(project.PendingConfirmation) != "" {
		return project, false
	}
	if project.BookGoal != nil && (project.BookGoal.Type != "undecided" || project.BookGoal.Confirmed ||
		strings.TrimSpace(project.BookGoal.Audience) != "" || strings.TrimSpace(project.BookGoal.DesiredImpact) != "") {
		return project, false
	}
	for _, chapter := range project.Chapters {
		if legacyTitles[chapter.ID] != chapter.Title || chapter.Status != "not_started" || chapter.Progress != 0 {
			return project, false
		}
		coverage := chapter.Narrative
		if coverage == nil || coverage.Event != "missing" || coverage.Scene != "missing" || coverage.Emotion != "missing" ||
			coverage.Relationship != "missing" || coverage.Choice != "missing" || coverage.Impact != "missing" || coverage.Reflection != "missing" {
			return project, false
		}
	}
	project.Chapters = []Chapter{}
	return project, true
}

func (conversation *interviewConversation) projectSnapshot() BiographyProject {
	conversation.projectMu.RLock()
	defer conversation.projectMu.RUnlock()
	return cloneBiographyProject(conversation.Project)
}

func (conversation *interviewConversation) replaceProject(project BiographyProject) {
	conversation.projectMu.Lock()
	conversation.Project = cloneBiographyProject(project)
	conversation.projectMu.Unlock()
}

func (conversation *interviewConversation) setInterviewOrder(order string) {
	conversation.projectMu.Lock()
	conversation.Project.InterviewOrder = order
	conversation.projectMu.Unlock()
}

func (conversation *interviewConversation) recentQuestionsSnapshot() []string {
	conversation.projectMu.RLock()
	defer conversation.projectMu.RUnlock()
	return append([]string(nil), conversation.RecentQuestions...)
}

func (conversation *interviewConversation) recordQuestion(question string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	conversation.projectMu.Lock()
	defer conversation.projectMu.Unlock()
	for _, existing := range conversation.RecentQuestions {
		if existing == question {
			return
		}
	}
	conversation.RecentQuestions = append(conversation.RecentQuestions, question)
	const maxRecentQuestions = 8
	if len(conversation.RecentQuestions) > maxRecentQuestions {
		conversation.RecentQuestions = conversation.RecentQuestions[len(conversation.RecentQuestions)-maxRecentQuestions:]
	}
}

func newBiographyProject() BiographyProject {
	return BiographyProject{
		ID: "biography_new", Title: "我的人生故事",
		BookGoal: &BiographyBookGoal{Type: "undecided"},
		Chapters: []Chapter{},
	}
}

func cloneBiographyProject(project BiographyProject) BiographyProject {
	copy := project
	copy.Chapters = append([]Chapter(nil), project.Chapters...)
	if project.BookGoal != nil {
		bookGoal := *project.BookGoal
		copy.BookGoal = &bookGoal
	}
	for index := range copy.Chapters {
		if project.Chapters[index].Narrative != nil {
			narrative := *project.Chapters[index].Narrative
			copy.Chapters[index].Narrative = &narrative
		}
	}
	return copy
}

func narrativeCoverage(event, scene, emotion, relationship, choice, impact, reflection string) *NarrativeCoverage {
	return &NarrativeCoverage{
		Event: event, Scene: scene, Emotion: emotion, Relationship: relationship,
		Choice: choice, Impact: impact, Reflection: reflection,
	}
}

func newInterviewChapter(transcript string, sequence int) Chapter {
	text := strings.Join(strings.Fields(strings.TrimSpace(transcript)), "")
	characters := []rune(text)
	if len(characters) > 14 {
		text = string(characters[:14]) + "…"
	}
	if text == "" {
		text = "刚才这段经历"
	}
	return Chapter{
		ID: fmt.Sprintf("chapter-%d", sequence), Title: fmt.Sprintf("关于“%s”的故事", text),
		Status: "collecting", StatusLabel: "讲述中", Progress: 15, Detail: "正在收集这段经历里的场景和感受",
		Narrative: narrativeCoverage("partial", "missing", "missing", "missing", "missing", "missing", "missing"),
		NextFocus: "补充当时最清楚的一个画面和内心感受",
	}
}
