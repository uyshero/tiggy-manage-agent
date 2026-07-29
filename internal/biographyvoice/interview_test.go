package biographyvoice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/sdk/tma"
)

type fakeInterviewBackend struct {
	createCalls               int
	runCalls                  int
	request                   tma.CreateSessionRequest
	requests                  []tma.CreateSessionRequest
	prompt                    string
	output                    json.RawMessage
	outputs                   []json.RawMessage
	err                       error
	session                   tma.Session
	events                    []tma.Event
	thinkingModes             []string
	compactionThresholds      []int
	compactionSummaryMaxChars []int
	thinkingErr               error
}

func (backend *fakeInterviewBackend) ConfigureInterviewSession(_ context.Context, _ string, mode string, threshold int, summaryMaxChars int) error {
	backend.thinkingModes = append(backend.thinkingModes, mode)
	backend.compactionThresholds = append(backend.compactionThresholds, threshold)
	backend.compactionSummaryMaxChars = append(backend.compactionSummaryMaxChars, summaryMaxChars)
	return backend.thinkingErr
}

func (backend *fakeInterviewBackend) CreateSession(_ context.Context, request tma.CreateSessionRequest) (string, error) {
	backend.createCalls++
	backend.request = request
	backend.requests = append(backend.requests, request)
	if backend.err != nil {
		return "", backend.err
	}
	if strings.Contains(request.Title, "整理") {
		return "session-organizer", nil
	}
	return "session-biography", nil
}

func (backend *fakeInterviewBackend) Run(_ context.Context, _ string, prompt string) (json.RawMessage, error) {
	backend.runCalls++
	backend.prompt = prompt
	if len(backend.outputs) >= backend.runCalls {
		return backend.outputs[backend.runCalls-1], backend.err
	}
	return backend.output, backend.err
}

func (backend *fakeInterviewBackend) GetSession(_ context.Context, sessionID string) (tma.Session, error) {
	if backend.err != nil {
		return tma.Session{}, backend.err
	}
	if backend.session.ID == "" {
		return tma.Session{ID: sessionID, AgentID: "agent-biography"}, nil
	}
	return backend.session, nil
}

func (backend *fakeInterviewBackend) ListEvents(_ context.Context, _ string) ([]tma.Event, error) {
	return backend.events, backend.err
}

func TestTMAInterviewEngineSeparatesLiveReplyAndProjectUpdateSessions(t *testing.T) {
	reply := InterviewReply{
		Text: "那天是谁送您出门的？", Expression: "温和、关切，语速稍慢",
		Project: initialBiographyProject(),
	}
	reply.Project.OverallProgress = 35
	spokenJSON, err := json.Marshal(map[string]string{"text": reply.Text, "expression": reply.Expression})
	if err != nil {
		t.Fatal(err)
	}
	spokenMessage, err := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": string(spokenJSON)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	projectJSON, _ := json.Marshal(reply.Project)
	projectMessage, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": string(projectJSON)}},
	})
	backend := &fakeInterviewBackend{outputs: []json.RawMessage{spokenMessage, projectMessage, spokenMessage}}
	engine := &tmaInterviewEngine{
		backend: backend, agentID: "agent-biography", organizerAgentID: "agent-organizer", environmentID: "environment-main",
		workspaceID: "workspace-1", ownerID: "user-1",
	}
	conversation := &interviewConversation{
		Project:         initialBiographyProject(),
		RecentQuestions: []string{"出门那天是谁送您去车站？"},
	}

	got, err := engine.Continue(t.Context(), conversation, "那年我十九岁，第一次去上海。")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != reply.Text || conversation.TMASessionID != "session-biography" || conversation.Project.OverallProgress != 32 {
		t.Fatalf("unexpected interview result: reply=%+v conversation=%+v", got, conversation)
	}
	if backend.request.AgentID != "agent-biography" || backend.request.EnvironmentID != "environment-main" || backend.request.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected session request: %+v", backend.request)
	}
	if !strings.Contains(backend.prompt, "第一次去上海") || !strings.Contains(backend.prompt, `"overallProgress":32`) ||
		!strings.Contains(backend.prompt, "不要默认沿时间线") || !strings.Contains(backend.prompt, `"bookGoal"`) {
		t.Fatalf("prompt did not include transcript and current project: %s", backend.prompt)
	}
	if !strings.Contains(backend.prompt, "实时采访分支") || !strings.Contains(backend.prompt, "不要整理、重写或更新章节") ||
		!strings.Contains(backend.prompt, "肯定、表扬、共情") || !strings.Contains(backend.prompt, "不要求每轮都显性表扬") ||
		!strings.Contains(backend.prompt, "您真不容易") {
		t.Fatalf("prompt did not keep live interviewing separate from organization: %s", backend.prompt)
	}
	if !strings.Contains(backend.prompt, "出门那天是谁送您去车站？") || !strings.Contains(backend.prompt, "不要重复 recentQuestions") ||
		!strings.Contains(backend.prompt, "当作补充理解") {
		t.Fatalf("prompt did not include anti-repeat supplement guidance: %s", backend.prompt)
	}
	updated, err := engine.Organize(t.Context(), conversation, "那年我十九岁，第一次去上海。")
	if err != nil {
		t.Fatal(err)
	}
	if updated.OverallProgress != 35 || conversation.Project.OverallProgress != 35 ||
		conversation.TMAOrganizerSessionID == "" || conversation.TMAOrganizerSessionID == conversation.TMASessionID {
		t.Fatalf("project was not organized independently: updated=%+v conversation=%+v", updated, conversation)
	}
	if len(backend.requests) != 2 || backend.requests[1].AgentID != "agent-organizer" {
		t.Fatalf("organizer session did not use the dedicated agent: %+v", backend.requests)
	}
	if len(backend.thinkingModes) != 1 || backend.thinkingModes[0] != "disabled" {
		t.Fatalf("only the live interview session should disable thinking: %#v", backend.thinkingModes)
	}
	if len(backend.compactionThresholds) != 1 || backend.compactionThresholds[0] != 8000 ||
		len(backend.compactionSummaryMaxChars) != 1 || backend.compactionSummaryMaxChars[0] != 4000 {
		t.Fatalf("live interview compaction was not configured: thresholds=%v summary=%v", backend.compactionThresholds, backend.compactionSummaryMaxChars)
	}
	if !strings.Contains(backend.prompt, `"narrative"`) || !strings.Contains(backend.prompt, "missing|partial|sufficient") {
		t.Fatalf("organizer prompt did not require narrative coverage: %s", backend.prompt)
	}

	if _, err := engine.Continue(t.Context(), conversation, "是我父亲送的。"); err != nil {
		t.Fatal(err)
	}
	if backend.createCalls != 2 || backend.runCalls != 3 {
		t.Fatalf("expected separate live and organizer sessions, create=%d run=%d", backend.createCalls, backend.runCalls)
	}
}

func TestDecodeInterviewReplyAcceptsJSONFenceAndRejectsInvalidProgress(t *testing.T) {
	reply := InterviewReply{Text: "请继续讲。", Expression: "温和", Project: initialBiographyProject()}
	replyJSON, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "```json\n" + string(replyJSON) + "\n```"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeInterviewReply(message); err != nil {
		t.Fatalf("expected fenced JSON to decode: %v", err)
	}

	reply.Project.OverallProgress = 101
	invalidReply, _ := json.Marshal(reply)
	invalidMessage, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": string(invalidReply)}},
	})
	if _, err := decodeInterviewReply(invalidMessage); err == nil || !strings.Contains(err.Error(), "between 0 and 100") {
		t.Fatalf("expected invalid progress error, got %v", err)
	}
}

func TestExtractPartialInterviewText(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: `{"te`, want: ""},
		{raw: `{"text":"您还记得`, want: "您还记得"},
		{raw: `{"text":"您说：\"很害怕\"","expression":"温和"}`, want: `您说："很害怕"`},
		{raw: "{\"text\":\"第一句\\n第二句", want: "第一句\n第二句"},
	}
	for _, test := range tests {
		if got := extractPartialInterviewText(test.raw); got != test.want {
			t.Errorf("extractPartialInterviewText(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestTMAInterviewEngineResumesLatestValidatedProject(t *testing.T) {
	reply := InterviewReply{Text: "请继续讲。", Expression: "温和", Project: initialBiographyProject()}
	reply.Project.OverallProgress = 61
	replyJSON, _ := json.Marshal(reply)
	messageJSON, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": string(replyJSON)}},
	})
	backend := &fakeInterviewBackend{
		session: tma.Session{ID: "session-resume", AgentID: "agent-biography"},
		events:  []tma.Event{{Type: "agent.message", Payload: messageJSON}},
	}
	engine := &tmaInterviewEngine{backend: backend, agentID: "agent-biography"}
	conversation := &interviewConversation{}
	if err := engine.Resume(t.Context(), conversation, "session-resume"); err != nil {
		t.Fatal(err)
	}
	if conversation.TMASessionID != "session-resume" || conversation.Project.OverallProgress != 61 {
		t.Fatalf("unexpected resumed conversation: %+v", conversation)
	}
	if len(backend.thinkingModes) != 1 || backend.thinkingModes[0] != "disabled" {
		t.Fatalf("resumed interview did not disable thinking: %#v", backend.thinkingModes)
	}

	backend.session.AgentID = "another-agent"
	if err := engine.Resume(t.Context(), &interviewConversation{}, "session-resume"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("expected cross-agent resume rejection, got %v", err)
	}
}

func TestOrganizedProjectRequiresBookGoalAndNarrativeCoverage(t *testing.T) {
	legacy := initialBiographyProject()
	legacy.BookGoal = nil
	for index := range legacy.Chapters {
		legacy.Chapters[index].Narrative = nil
		legacy.Chapters[index].NextFocus = ""
	}
	if err := validateBiographyProject(legacy); err != nil {
		t.Fatalf("legacy project should remain resumable: %v", err)
	}
	if err := validateOrganizedBiographyProject(legacy); err == nil || !strings.Contains(err.Error(), "book goal") {
		t.Fatalf("expected organizer to require a book goal, got %v", err)
	}

	project := initialBiographyProject()
	project.Chapters[0].Narrative.Emotion = "unknown"
	if err := validateOrganizedBiographyProject(project); err == nil || !strings.Contains(err.Error(), "narrative coverage") {
		t.Fatalf("expected invalid narrative coverage to be rejected, got %v", err)
	}
}

func TestCloneBiographyProjectCopiesNarrativeState(t *testing.T) {
	original := initialBiographyProject()
	cloned := cloneBiographyProject(original)
	cloned.BookGoal.Audience = "朋友"
	cloned.Chapters[0].Narrative.Emotion = "missing"
	if original.BookGoal.Audience == "朋友" || original.Chapters[0].Narrative.Emotion == "missing" {
		t.Fatalf("clone shared nested narrative state with original: original=%+v cloned=%+v", original, cloned)
	}
}
