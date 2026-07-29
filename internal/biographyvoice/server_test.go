package biographyvoice

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestMockVoiceSessionProtocol(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-1"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我十九岁去了上海"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "我十九岁去了上海")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "我十九岁去了上海")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSStart, Text: "那是第一次离开家吗？"})
	assertTextEvents(t, ctx, connection, ServerProjectUpdated, ServerTTSStarted, ServerTTSFinished)
}

func TestMockVoiceSessionCanDeferInterviewUntilFollowupRequest(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-defer"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我想补充第一段"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "我想补充第一段")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit, DeferInterview: true})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "我想补充第一段")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInterviewFollowup, Text: "我想补充第一段\n这是补充内容"})
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")
}

func TestVoiceSessionRequiresConfiguredClientToken(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, ClientToken: "required-token", AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized handshake, response=%v err=%v", response, err)
	}
}

func TestMockVoiceSessionCanCancelTTS(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-cancel"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSStart, Text: "这段话不应该播放完"})
	assertServerMessage(t, ctx, connection, ServerTTSStarted, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSCancel})
	assertServerMessage(t, ctx, connection, ServerTTSCanceled, "")
}

type blockingInterviewEngine struct {
	started  chan struct{}
	canceled chan struct{}
}

func (engine *blockingInterviewEngine) Continue(ctx context.Context, _ *interviewConversation, _ string) (InterviewReply, error) {
	select {
	case <-engine.started:
	default:
		close(engine.started)
	}
	<-ctx.Done()
	select {
	case <-engine.canceled:
	default:
		close(engine.canceled)
	}
	return InterviewReply{}, ctx.Err()
}

func (engine *blockingInterviewEngine) Organize(_ context.Context, conversation *interviewConversation, _ string) (BiographyProject, error) {
	return conversation.projectSnapshot(), nil
}

func (*blockingInterviewEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestInterviewTurnDoesNotBlockHeartbeatAndCanBeInterrupted(t *testing.T) {
	engine := &blockingInterviewEngine{started: make(chan struct{}), canceled: make(chan struct{})}
	server := &Server{
		config: Config{Provider: ProviderMock, InterviewFirstResponseTimeout: time.Second, InterviewTimeout: 2 * time.Second},
		logger: slog.Default(), interview: engine,
	}
	httpServer := httptest.NewServer(http.HandlerFunc(server.voiceSession))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-interrupt"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "这是一段会被打断的话"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "这是一段会被打断的话")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "这是一段会被打断的话")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	select {
	case <-engine.started:
	case <-ctx.Done():
		t.Fatal("interview turn did not start")
	}

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionPing})
	assertServerMessage(t, ctx, connection, ServerSessionPong, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSCancel})
	assertTextEvents(t, ctx, connection, ServerInterviewCanceled, ServerTTSCanceled)
	select {
	case <-engine.canceled:
	case <-ctx.Done():
		t.Fatal("interview model context was not canceled")
	}
}

func TestInterviewFirstResponseTimeoutReturnsSpokenFallback(t *testing.T) {
	engine := &blockingInterviewEngine{started: make(chan struct{}), canceled: make(chan struct{})}
	server := &Server{
		config: Config{Provider: ProviderMock, InterviewFirstResponseTimeout: 100 * time.Millisecond, InterviewTimeout: time.Second},
		logger: slog.Default(), interview: engine,
	}
	httpServer := httptest.NewServer(http.HandlerFunc(server.voiceSession))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-timeout"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "模型故意不返回"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "模型故意不返回")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "模型故意不返回")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	select {
	case <-engine.started:
	case <-ctx.Done():
		t.Fatal("interview turn did not start")
	}
	assertServerMessageAllowing(t, ctx, connection, ServerInterviewReply, "我先接着问一个简单的。回到刚才那段经历里，您现在最清楚记得的一个画面是什么？", ServerProjectUpdated)
}

func TestServerAcceptsConfiguredDoubaoProvider(t *testing.T) {
	server, err := NewServer(Config{
		HTTPAddr: ":0", Provider: ProviderDoubao, AllowedOrigins: []string{"127.0.0.1"},
		DoubaoAPIKey: "secret", DoubaoASRURL: "wss://speech.example/asr", DoubaoASRResourceID: "asr",
		DoubaoTTSURL: "wss://speech.example/tts", DoubaoTTSResourceID: "tts",
		DoubaoTTSSpeaker: "zh_female_example",
	}, nil)
	if err != nil || server == nil {
		t.Fatalf("expected configured provider to start, server=%v err=%v", server, err)
	}
}

func TestDoubaoVoiceSessionForwardsASRAndTTSAudio(t *testing.T) {
	asrConnection := newFakeDoubaoConnection()
	finalASR := mustDoubaoFrame(t, doubaoFrame{
		MessageType: doubaoMessageFullServer, Flags: doubaoFlagLastWithSequence,
		Serialization: doubaoSerializationJSON, HasSequence: true, Sequence: 2,
		Payload: []byte(`{"code":20000000,"result":{"text":"那年我十九岁","utterances":[{"definite":true}]}}`),
	})
	asrConnection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err == nil && frame.MessageType == doubaoMessageAudioClient && frame.Flags == doubaoFlagLastNoSequence {
			asrConnection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: finalASR}
		}
	}

	ttsConnection := newFakeDoubaoConnection()
	ttsConnection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return
		}
		var responses []doubaoFrame
		switch frame.Event {
		case doubaoEventStartConnection:
			responses = append(responses, doubaoFrame{MessageType: doubaoMessageFullServer, HasEvent: true, Event: doubaoEventConnectionStarted, EventID: "connection-1", Payload: []byte(`{}`)})
		case doubaoEventStartSession:
			responses = append(responses, doubaoFrame{MessageType: doubaoMessageFullServer, HasEvent: true, Event: doubaoEventSessionStarted, EventID: frame.EventID, Payload: []byte(`{}`)})
		case doubaoEventFinishSession:
			responses = append(responses,
				doubaoFrame{MessageType: doubaoMessageAudioServer, HasEvent: true, Event: doubaoEventTTSResponse, EventID: frame.EventID, Payload: []byte{10, 20, 30}},
				doubaoFrame{MessageType: doubaoMessageFullServer, HasEvent: true, Event: doubaoEventSessionFinished, EventID: frame.EventID, Payload: []byte(`{}`)},
			)
		}
		for _, response := range responses {
			encoded, encodeErr := buildDoubaoFrame(response)
			if encodeErr == nil {
				ttsConnection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: encoded}
			}
		}
	}

	config := testDoubaoConfig()
	dialer := func(_ context.Context, target string, _ http.Header) (doubaoConnection, error) {
		if target == config.DoubaoASRURL {
			return asrConnection, nil
		}
		return ttsConnection, nil
	}
	server, err := newServer(config, nil, dialer)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-doubao"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	if err := connection.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "那年我十九岁")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSStart, Text: "那是第一次离开家吗？", Expression: "温和、关切"})
	assertDoubaoTurnEvents(t, ctx, connection, []byte{10, 20, 30})
}

func TestDoubaoVoiceSessionReturnsNoSpeechWithoutProviderFailure(t *testing.T) {
	asrConnection := newFakeDoubaoConnection()
	asrConnection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err == nil && frame.MessageType == doubaoMessageAudioClient && frame.Flags == doubaoFlagLastNoSequence {
			asrConnection.reads <- fakeDoubaoRead{err: errDoubaoASRNoTranscript}
		}
	}
	config := testDoubaoConfig()
	server, err := newServer(config, nil, func(context.Context, string, http.Header) (doubaoConnection, error) {
		return asrConnection, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-no-speech"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	if err := connection.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})

	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message ServerMessage
	if messageType != websocket.MessageText || json.Unmarshal(payload, &message) != nil {
		t.Fatalf("unexpected no-speech response: type=%v payload=%q", messageType, payload)
	}
	if message.Type != ServerError || message.Code != "no_speech" || !message.Retryable {
		t.Fatalf("unexpected no-speech message: %+v", message)
	}
}

type recordingInterviewEngine struct {
	resumedSessionID string
}

func (engine *recordingInterviewEngine) Continue(context.Context, *interviewConversation, string) (InterviewReply, error) {
	return InterviewReply{}, nil
}

func (engine *recordingInterviewEngine) Organize(_ context.Context, conversation *interviewConversation, _ string) (BiographyProject, error) {
	return conversation.projectSnapshot(), nil
}

func (engine *recordingInterviewEngine) Resume(_ context.Context, conversation *interviewConversation, sessionID string) error {
	engine.resumedSessionID = sessionID
	conversation.TMASessionID = sessionID
	conversation.Project = initialBiographyProject()
	return nil
}

func TestServerPreparesEncryptedInterviewResume(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingInterviewEngine{}
	server := &Server{
		config:    Config{InterviewProvider: ProviderTMA, InterviewTimeout: time.Second},
		interview: engine, resumeTokens: codec,
	}
	token, err := codec.Encode("session-resume", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &interviewConversation{Project: newBiographyProject()}
	err = server.prepareInterviewConversation(t.Context(), conversation, ClientMessage{
		ClientInstanceID: "device-1", ResumeToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.resumedSessionID != "session-resume" || conversation.ClientInstanceID != "device-1" || conversation.Project.OverallProgress != 32 {
		t.Fatalf("unexpected resumed conversation: engine=%+v conversation=%+v", engine, conversation)
	}
	if err := server.prepareInterviewConversation(t.Context(), &interviewConversation{}, ClientMessage{
		ClientInstanceID: "device-2", ResumeToken: token,
	}); err == nil {
		t.Fatal("expected cross-device resume rejection")
	}
}

func TestServerRestoresProjectFromAsyncUpdateToken(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	asyncProject := initialBiographyProject()
	asyncProject.OverallProgress = 77
	token, err := codec.EncodeState("session-resume", "device-1", &asyncProject)
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingInterviewEngine{}
	server := &Server{
		config:    Config{InterviewProvider: ProviderTMA, InterviewTimeout: time.Second},
		interview: engine, resumeTokens: codec,
	}
	conversation := &interviewConversation{Project: newBiographyProject()}
	if err := server.prepareInterviewConversation(t.Context(), conversation, ClientMessage{
		ClientInstanceID: "device-1", ResumeToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if conversation.projectSnapshot().OverallProgress != 77 {
		t.Fatalf("async project snapshot was not restored: %+v", conversation.projectSnapshot())
	}
}

type blockingOrganizerEngine struct {
	organizeStarted chan struct{}
	releaseOrganize chan struct{}
}

func (engine *blockingOrganizerEngine) Continue(_ context.Context, conversation *interviewConversation, _ string) (InterviewReply, error) {
	project := conversation.projectSnapshot()
	return InterviewReply{Text: "您第一次到上海时，最先看到的是什么？", Expression: "温和", Project: project}, nil
}

func (engine *blockingOrganizerEngine) Organize(ctx context.Context, conversation *interviewConversation, _ string) (BiographyProject, error) {
	close(engine.organizeStarted)
	select {
	case <-engine.releaseOrganize:
		project := conversation.projectSnapshot()
		project.OverallProgress++
		conversation.replaceProject(project)
		return project, nil
	case <-ctx.Done():
		return BiographyProject{}, ctx.Err()
	}
}

func (*blockingOrganizerEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestVoiceSessionSendsLiveReplyBeforeOrganizerFinishes(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := &blockingOrganizerEngine{organizeStarted: make(chan struct{}), releaseOrganize: make(chan struct{})}
	server.interview = engine
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-async"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我十九岁去了上海"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")

	select {
	case <-engine.organizeStarted:
	case <-ctx.Done():
		t.Fatal("organizer did not start")
	}
	close(engine.releaseOrganize)
	assertServerMessage(t, ctx, connection, ServerProjectUpdated, "")
}

type orderedOrganizerEngine struct {
	mu        sync.Mutex
	started   []string
	active    int
	maxActive int
	release   chan struct{}
}

func (*orderedOrganizerEngine) Continue(context.Context, *interviewConversation, string) (InterviewReply, error) {
	return InterviewReply{}, nil
}

func (engine *orderedOrganizerEngine) Organize(ctx context.Context, conversation *interviewConversation, transcript string) (BiographyProject, error) {
	engine.mu.Lock()
	engine.started = append(engine.started, transcript)
	engine.active++
	if engine.active > engine.maxActive {
		engine.maxActive = engine.active
	}
	call := len(engine.started)
	engine.mu.Unlock()
	if call == 1 {
		select {
		case <-engine.release:
		case <-ctx.Done():
			return BiographyProject{}, ctx.Err()
		}
	}
	engine.mu.Lock()
	engine.active--
	engine.mu.Unlock()
	project := conversation.projectSnapshot()
	project.OverallProgress = call
	conversation.replaceProject(project)
	return project, nil
}

func (*orderedOrganizerEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestProjectUpdateWorkerRunsTasksInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine := &orderedOrganizerEngine{release: make(chan struct{})}
	server := &Server{config: Config{InterviewTimeout: time.Second}, interview: engine}
	tasks, results := server.startProjectUpdateWorker(ctx, &interviewConversation{Project: initialBiographyProject()})
	if err := enqueueProjectUpdate(ctx, tasks, "第一段"); err != nil {
		t.Fatal(err)
	}
	if err := enqueueProjectUpdate(ctx, tasks, "第二段"); err != nil {
		t.Fatal(err)
	}
	close(engine.release)
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatal(result.err)
			}
		case <-ctx.Done():
			t.Fatal("project update worker did not finish")
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if strings.Join(engine.started, ",") != "第一段,第二段" || engine.maxActive != 1 {
		t.Fatalf("organizer was not sequential: started=%v max_active=%d", engine.started, engine.maxActive)
	}
}

func writeClientMessage(t *testing.T, ctx context.Context, connection *websocket.Conn, message ClientMessage) {
	t.Helper()
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func assertServerMessage(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedType string, expectedText string) {
	t.Helper()
	message := readServerTextMessage(t, ctx, connection)
	if message.Type != expectedType || (expectedText != "" && message.Text != expectedText) {
		t.Fatalf("unexpected server message: %+v", message)
	}
}

func assertServerMessageAllowing(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedType string, expectedText string, allowedTypes ...string) {
	t.Helper()
	allowed := make(map[string]bool, len(allowedTypes))
	for _, allowedType := range allowedTypes {
		allowed[allowedType] = true
	}
	for {
		message := readServerTextMessage(t, ctx, connection)
		if message.Type == expectedType && (expectedText == "" || message.Text == expectedText) {
			return
		}
		if !allowed[message.Type] {
			t.Fatalf("unexpected server message: %+v", message)
		}
	}
}

func readServerTextMessage(t *testing.T, ctx context.Context, connection *websocket.Conn) ServerMessage {
	t.Helper()
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("expected text message, got %v", messageType)
	}
	var message ServerMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func assertTextEvents(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedTypes ...string) {
	t.Helper()
	pending := make(map[string]bool, len(expectedTypes))
	for _, expectedType := range expectedTypes {
		pending[expectedType] = true
	}
	for len(pending) > 0 {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if messageType != websocket.MessageText {
			t.Fatalf("expected text message, got %v", messageType)
		}
		var message ServerMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if !pending[message.Type] {
			t.Fatalf("unexpected server message while waiting for %v: %+v", pending, message)
		}
		delete(pending, message.Type)
	}
}

func assertDoubaoTurnEvents(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedAudio []byte) {
	t.Helper()
	pending := map[string]bool{ServerProjectUpdated: true, ServerTTSStarted: true, ServerTTSFinished: true}
	audioReceived := false
	for len(pending) > 0 || !audioReceived {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if messageType == websocket.MessageBinary {
			if string(payload) != string(expectedAudio) || audioReceived {
				t.Fatalf("unexpected TTS audio frame: %v", payload)
			}
			audioReceived = true
			continue
		}
		var message ServerMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if !pending[message.Type] {
			t.Fatalf("unexpected server message while waiting for %v: %+v", pending, message)
		}
		delete(pending, message.Type)
	}
}
