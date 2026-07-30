package biographyvoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const maxVoiceFrameBytes = 1024 * 1024

var (
	errInterviewInterrupted          = errors.New("interview turn interrupted")
	errInterviewFirstResponseTimeout = errors.New("interview first response timeout")
	errInterviewTotalTimeout         = errors.New("interview total timeout")
	errProjectUpdateQueueFull        = errors.New("biography project update queue is full")
)

type Server struct {
	config           Config
	logger           *slog.Logger
	mux              *http.ServeMux
	doubaoDialer     doubaoDialer
	interview        interviewEngine
	resumeTokens     *resumeTokenCodec
	auth             *authService
	store            biographyStore
	interviewLeaseMu sync.Mutex
	interviewLeases  map[string]string
}

func NewServer(config Config, logger *slog.Logger) (*Server, error) {
	return newServer(config, logger, defaultDoubaoDialer)
}

func newServer(config Config, logger *slog.Logger, dialer doubaoDialer) (*Server, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	if dialer == nil {
		dialer = defaultDoubaoDialer
	}
	interview, err := newInterviewEngine(config)
	if err != nil {
		return nil, err
	}
	var resumeTokens *resumeTokenCodec
	if valueOrDefault(config.InterviewProvider, ProviderMock) == ProviderTMA {
		resumeTokens, err = newResumeTokenCodec(config.ResumeSigningKey, config.ResumeTTL)
		if err != nil {
			return nil, err
		}
	}
	var store biographyStore
	var auth *authService
	if strings.TrimSpace(config.AuthMode) == biographyAuthModeOIDC {
		if strings.TrimSpace(config.DatabaseURL) != "" {
			store, err = newPostgresBiographyStore(config)
		} else {
			store, err = newBiographyDataStore(config.DataDir)
		}
		if err != nil {
			return nil, err
		}
		auth, err = newAuthService(config, store)
		if err != nil {
			return nil, err
		}
	}
	server := &Server{
		config: config, logger: logger, mux: http.NewServeMux(), doubaoDialer: dialer,
		interview: interview, resumeTokens: resumeTokens, auth: auth, store: store,
		interviewLeases: make(map[string]string),
	}
	server.mux.HandleFunc("GET /healthz", server.health)
	server.mux.HandleFunc("GET /v1/auth/config", server.authConfig)
	server.mux.HandleFunc("GET /v1/auth/me", server.authMe)
	server.mux.HandleFunc("GET /v1/progress", server.userProgress)
	server.mux.HandleFunc("GET /v1/recordings", server.recordings)
	server.mux.HandleFunc("GET /v1/recordings/{recordingID}/audio", server.recordingAudio)
	server.mux.HandleFunc("PUT /v1/recordings/{recordingID}/audio", server.recordingAudio)
	server.mux.HandleFunc("POST /v1/recordings/{recordingID}/audio", server.recordingAudio)
	server.mux.HandleFunc("PATCH /v1/recordings/{recordingID}", server.recording)
	server.mux.HandleFunc("DELETE /v1/recordings/{recordingID}", server.recording)
	server.mux.HandleFunc("GET /v1/recordings/{recordingID}/segments/{segmentID}/audio", server.recordingSegment)
	server.mux.HandleFunc("PUT /v1/recordings/{recordingID}/segments/{segmentID}/audio", server.recordingSegment)
	server.mux.HandleFunc("DELETE /v1/recordings/{recordingID}/segments/{segmentID}/audio", server.recordingSegment)
	server.mux.HandleFunc("GET /v1/voice/session", server.voiceSession)
	return server, nil
}

func (server *Server) Handler() http.Handler {
	return server.cors(server.mux)
}

// cors keeps the browser-facing REST endpoints aligned with the WebSocket
// origin allowlist. The mobile H5 build normally runs on a different local
// port from the voice gateway, so it needs the same explicit permission.
func (server *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && server.isAllowedBrowserOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			if origin == "" || !server.isAllowedBrowserOrigin(origin) {
				http.Error(w, "origin is not allowed", http.StatusForbidden)
				return
			}
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (server *Server) isAllowedBrowserOrigin(rawOrigin string) bool {
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	host := strings.ToLower(origin.Host)
	for _, allowed := range server.config.AllowedOrigins {
		matched, err := path.Match(strings.ToLower(strings.TrimSpace(allowed)), host)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func (server *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "provider": server.config.Provider})
}

func (server *Server) authConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, server.auth.publicConfig())
}

func (server *Server) authMe(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user == nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": publicUser{ID: user.ID, Subject: user.Subject, DisplayName: user.DisplayName}})
}

func (server *Server) userProgress(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user == nil || server.store == nil {
		project := newBiographyProject()
		writeJSON(w, http.StatusOK, buildBiographyProgress(project, nil, nil, activeProgressSession{}, nil, time.Now()))
		return
	}
	progress, exists, err := server.store.progressForUser(user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "读取进度失败"})
		return
	}
	if !exists {
		project := newBiographyProject()
		progress = buildBiographyProgress(project, nil, nil, activeProgressSession{}, nil, time.Now())
	} else if project, changed := removeLegacyEmptyChapterTemplate(progress.Project); changed {
		progress.Project = project
		progress.ActiveChapterTitles = activeChapterTitles(project)
		progress.PendingConfirmation = ""
		progress.UpdatedAt = time.Now()
		if err := server.store.saveProgress(user.ID, progress); err != nil {
			server.logger.Warn("biography progress migration failed", "error", server.safeProviderError(err))
		}
	}
	writeJSON(w, http.StatusOK, progress)
}

func (server *Server) voiceSession(w http.ResponseWriter, r *http.Request) {
	user, ok := server.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: server.config.AllowedOrigins})
	if err != nil {
		server.logger.Warn("biography voice websocket accept failed", "error", err)
		return
	}
	connection.SetReadLimit(maxVoiceFrameBytes)
	defer connection.CloseNow()

	var serveErr error
	if server.config.Provider == ProviderDoubao {
		serveErr = server.serveDoubaoSession(r.Context(), connection, user)
	} else {
		serveErr = server.serveMockSession(r.Context(), connection, user)
	}
	if serveErr != nil && !isExpectedClose(serveErr) {
		server.logger.Warn("biography voice session failed", "provider", server.config.Provider, "error", server.safeProviderError(serveErr))
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (server *Server) authenticate(r *http.Request) (*authenticatedUser, bool) {
	if server.auth != nil {
		user, err := server.auth.authenticateRequest(r)
		return user, err == nil
	}
	expected := server.config.ClientToken
	if expected == "" {
		return nil, true
	}
	provided := bearerToken(r)
	return nil, subtleStringCompare(provided, expected)
}

type inboundFrame struct {
	messageType websocket.MessageType
	payload     []byte
	err         error
}

type projectUpdateResult struct {
	project    BiographyProject
	transcript string
	err        error
}

type chapterConfirmationAction string

const (
	chapterConfirmationAccept chapterConfirmationAction = "accept"
	chapterConfirmationRevise chapterConfirmationAction = "revise"
)

func parseChapterConfirmationAction(transcript string) chapterConfirmationAction {
	compact := strings.NewReplacer("，", "", "。", "", "！", "", "？", "", "、", "", " ", "", "\n", "").Replace(strings.TrimSpace(transcript))
	switch compact {
	case "对", "对的", "是", "是的", "好", "好的", "没错", "可以", "就这样":
		return chapterConfirmationAccept
	case "补充", "我想补充", "再补充", "改一下", "修改", "我想改一下", "更正", "不对":
		return chapterConfirmationRevise
	default:
		return ""
	}
}

func (server *Server) acquireInterviewLease(user *authenticatedUser, sessionID string) bool {
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return true
	}
	userID := strings.TrimSpace(user.ID)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	server.interviewLeaseMu.Lock()
	defer server.interviewLeaseMu.Unlock()
	if server.interviewLeases == nil {
		server.interviewLeases = make(map[string]string)
	}
	existing, busy := server.interviewLeases[userID]
	if busy && existing != sessionID {
		return false
	}
	server.interviewLeases[userID] = sessionID
	return true
}

func (server *Server) releaseInterviewLease(user *authenticatedUser, sessionID string) {
	if user == nil || strings.TrimSpace(user.ID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	server.interviewLeaseMu.Lock()
	defer server.interviewLeaseMu.Unlock()
	userID := strings.TrimSpace(user.ID)
	if server.interviewLeases[userID] == strings.TrimSpace(sessionID) {
		delete(server.interviewLeases, userID)
	}
}

func (server *Server) applyChapterConfirmation(conversation *interviewConversation, transcript string) (*ServerMessage, bool) {
	action := parseChapterConfirmationAction(transcript)
	if action == "" {
		return nil, false
	}
	conversation.projectMu.Lock()
	defer conversation.projectMu.Unlock()
	project := &conversation.Project
	chapterID := strings.TrimSpace(project.PendingConfirmationChapterID)
	if strings.TrimSpace(project.PendingConfirmation) == "" || chapterID == "" {
		return nil, false
	}
	chapterIndex := -1
	for index := range project.Chapters {
		if project.Chapters[index].ID == chapterID {
			chapterIndex = index
			break
		}
	}
	if chapterIndex < 0 {
		return nil, false
	}
	chapter := &project.Chapters[chapterIndex]
	project.PendingConfirmation = ""
	project.PendingConfirmationChapterID = ""
	var text string
	switch action {
	case chapterConfirmationAccept:
		chapter.Status = "completed"
		chapter.StatusLabel = "已确认"
		chapter.Progress = 100
		project.CompletedChapterCount = completedChapterCount(*project)
		text = "好，这一段已经按您的确认保存。以后想起新的细节，随时还能补进来。"
	case chapterConfirmationRevise:
		chapter.Status = "collecting"
		chapter.StatusLabel = "继续补充"
		chapter.Progress = min(90, max(35, chapter.Progress))
		conversation.FocusedChapterID = chapter.ID
		text = "好，这一段先不定稿。您想补上或改哪一点，慢慢讲给我听。"
	}
	projectCopy := cloneBiographyProject(*project)
	return &ServerMessage{
		Type: ServerInterviewReply, Text: text, Expression: "温和、清晰，语速稍慢，让对方放心",
		Project: &projectCopy, ResumeToken: server.interviewResumeToken(conversation, projectCopy),
	}, true
}

type interviewTurnEvent struct {
	id         uint64
	message    *ServerMessage
	transcript string
	done       bool
	accepted   bool
	failed     bool
}

type interviewTurnController struct {
	server       *Server
	ctx          context.Context
	conversation *interviewConversation
	events       chan interviewTurnEvent
	nextID       uint64
	activeID     uint64
	cancel       context.CancelCauseFunc
	failures     int
	openUntil    time.Time
}

func newInterviewTurnController(ctx context.Context, server *Server, conversation *interviewConversation) *interviewTurnController {
	return &interviewTurnController{
		server: server, ctx: ctx, conversation: conversation, events: make(chan interviewTurnEvent, 64),
	}
}

func (controller *interviewTurnController) start(transcript string) {
	controller.cancelActive(false)
	controller.nextID++
	controller.activeID = controller.nextID
	turnID := controller.activeID
	controller.events <- interviewTurnEvent{
		id: turnID,
		message: &ServerMessage{
			Type: ServerInterviewDelta,
			Text: "我听到了，正在想接下来问什么。",
		},
	}

	if time.Now().Before(controller.openUntil) {
		message := controller.server.interviewFallbackMessage(controller.conversation, interviewFallbackQuestion(transcript))
		controller.events <- interviewTurnEvent{id: turnID, message: &message, transcript: transcript, done: true, accepted: true, failed: true}
		return
	}

	turnCtx, cancel := context.WithCancelCause(controller.ctx)
	controller.cancel = cancel
	go controller.server.runInterviewTurn(turnCtx, cancel, turnID, controller.conversation, transcript, controller.events)
}

func (controller *interviewTurnController) cancelActive(notify bool) *ServerMessage {
	if controller.cancel == nil && controller.activeID == 0 {
		return nil
	}
	if controller.cancel != nil {
		controller.cancel(errInterviewInterrupted)
	}
	controller.cancel = nil
	controller.activeID = 0
	if !notify {
		return nil
	}
	message := ServerMessage{Type: ServerInterviewCanceled}
	return &message
}

func (controller *interviewTurnController) handle(event interviewTurnEvent) (string, *ServerMessage) {
	if event.id == 0 || event.id != controller.activeID {
		return "", nil
	}
	if !event.done {
		return "", event.message
	}
	controller.cancel = nil
	controller.activeID = 0
	if event.failed {
		controller.failures++
		if controller.failures >= 3 {
			controller.openUntil = time.Now().Add(30 * time.Second)
			controller.failures = 0
		}
	} else {
		controller.failures = 0
		controller.openUntil = time.Time{}
	}
	if event.accepted {
		return event.transcript, event.message
	}
	return "", event.message
}

func (server *Server) serveMockSession(ctx context.Context, connection *websocket.Conn, user *authenticatedUser) error {
	inbound := make(chan inboundFrame, 1)
	go readFrames(ctx, connection, inbound)

	var sessionID string
	var leasedSessionID string
	defer func() { server.releaseInterviewLease(user, leasedSessionID) }()
	var debugTranscript string
	var audioBytes int64
	var ttsTimer *time.Timer
	var ttsFinished <-chan time.Time
	conversation := server.newInterviewConversation()
	progressSession := activeProgressSession{}
	saveProgress := func(endedAt *time.Time) {
		server.saveConversationProgress(conversation, progressSession, endedAt)
	}
	defer func() {
		if progressSession.ID != "" {
			now := time.Now()
			saveProgress(&now)
		}
	}()
	projectTasks, projectUpdates := server.startProjectUpdateWorker(ctx, conversation)
	turns := newInterviewTurnController(ctx, server, conversation)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-turns.events:
			transcript, message := turns.handle(event)
			if transcript != "" {
				conversation.addPendingTranscript(transcript)
				progressSession.TranscriptCount++
				progressSession.TodayRecordingSaved = true
				saveProgress(nil)
				if err := enqueueProjectUpdate(ctx, projectTasks, transcript); err != nil {
					server.logger.Warn("biography project update deferred", "error", err)
				}
			}
			if message != nil {
				message.SessionID = sessionID
				if err := writeServerMessage(ctx, connection, *message); err != nil {
					return err
				}
			}
		case update := <-projectUpdates:
			if update.err == nil {
				conversation.markTranscriptOrganized(update.transcript)
			}
			if err := server.writeProjectUpdate(ctx, connection, sessionID, conversation, update); err != nil {
				return err
			}
			saveProgress(nil)
		case <-ttsFinished:
			ttsFinished = nil
			if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerTTSFinished, SessionID: sessionID}); err != nil {
				return err
			}
		case frame := <-inbound:
			if frame.err != nil {
				return frame.err
			}
			if frame.messageType == websocket.MessageBinary {
				if message := turns.cancelActive(true); message != nil {
					message.SessionID = sessionID
					if err := writeServerMessage(ctx, connection, *message); err != nil {
						return err
					}
				}
				audioBytes += int64(len(frame.payload))
				continue
			}
			var message ClientMessage
			if err := json.Unmarshal(frame.payload, &message); err != nil {
				if err := writeProtocolError(ctx, connection, sessionID, "invalid_json", "message must be valid JSON"); err != nil {
					return err
				}
				continue
			}
			switch message.Type {
			case ClientSessionStart:
				if strings.TrimSpace(message.SessionID) == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_id_required", "session_id is required"); err != nil {
						return err
					}
					continue
				}
				if err := server.prepareInterviewConversation(ctx, conversation, user, message); err != nil {
					server.logger.Warn("biography interview resume rejected", "error", server.safeProviderError(err))
					if writeErr := writeProtocolError(ctx, connection, sessionID, "resume_invalid", "上次采访暂时无法恢复，请重新开始"); writeErr != nil {
						return writeErr
					}
					continue
				}
				requestedSessionID := strings.TrimSpace(message.SessionID)
				if leasedSessionID != "" && leasedSessionID != requestedSessionID {
					if err := writeProtocolError(ctx, connection, sessionID, "session_already_started", "当前连接已有进行中的采访"); err != nil {
						return err
					}
					continue
				}
				if !server.acquireInterviewLease(user, requestedSessionID) {
					if err := writeProtocolError(ctx, connection, sessionID, "interview_busy", "这本书正在另一台设备上采访，请先在那边结束今天的采访"); err != nil {
						return err
					}
					continue
				}
				sessionID = requestedSessionID
				leasedSessionID = requestedSessionID
				progressSession = activeProgressSession{ID: sessionID, StartedAt: time.Now()}
				saveProgress(nil)
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerSessionReady, SessionID: sessionID}); err != nil {
					return err
				}
				project := conversation.projectSnapshot()
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerInterviewProject, SessionID: sessionID, Project: &project}); err != nil {
					return err
				}
				if err := server.writePendingChapterConfirmation(ctx, connection, sessionID, project); err != nil {
					return err
				}
				server.enqueuePendingProjectUpdates(ctx, projectTasks, conversation)
			case ClientASRDebugText:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				debugTranscript = strings.TrimSpace(message.Text)
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerASRPartial, SessionID: sessionID, Text: debugTranscript}); err != nil {
					return err
				}
			case ClientInputCommit:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				transcript := debugTranscript
				if transcript == "" {
					transcript = "已收到一段语音"
				}
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerASRFinal, SessionID: sessionID, Text: transcript, AudioBytes: audioBytes}); err != nil {
					return err
				}
				if !message.DeferInterview {
					turns.start(transcript)
				}
				debugTranscript = ""
				audioBytes = 0
			case ClientInputCancel:
				debugTranscript = ""
				audioBytes = 0
			case ClientInterviewFollowup:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				transcript := strings.TrimSpace(message.Text)
				if transcript == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "empty_transcript", "transcript is required"); err != nil {
						return err
					}
					continue
				}
				if confirmation, handled := server.applyChapterConfirmation(conversation, transcript); handled {
					saveProgress(nil)
					confirmation.SessionID = sessionID
					if err := writeServerMessage(ctx, connection, *confirmation); err != nil {
						return err
					}
					continue
				}
				turns.start(transcript)
			case ClientInterviewOrderSet:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				order := strings.TrimSpace(message.InterviewOrder)
				if !validInterviewOrder(order) {
					if err := writeProtocolError(ctx, connection, sessionID, "invalid_interview_order", "interview order must be chronological, key_moments, or custom"); err != nil {
						return err
					}
					continue
				}
				conversation.setInterviewOrder(order)
				saveProgress(nil)
				project := conversation.projectSnapshot()
				if err := writeServerMessage(ctx, connection, ServerMessage{
					Type: ServerProjectUpdated, SessionID: sessionID, Project: &project,
					ResumeToken: server.interviewResumeToken(conversation, project),
				}); err != nil {
					return err
				}
			case ClientInterviewChapterFocus:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				if err := conversation.setFocusedChapter(message.ChapterID); err != nil {
					if err := writeProtocolError(ctx, connection, sessionID, "invalid_chapter", "chapter must belong to this biography"); err != nil {
						return err
					}
				}
			case ClientTTSStart:
				if sessionID == "" || strings.TrimSpace(message.Text) == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "invalid_tts_request", "active session and text are required"); err != nil {
						return err
					}
					continue
				}
				if ttsTimer != nil {
					ttsTimer.Stop()
				}
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerTTSStarted, SessionID: sessionID}); err != nil {
					return err
				}
				ttsTimer = time.NewTimer(350 * time.Millisecond)
				ttsFinished = ttsTimer.C
			case ClientTTSCancel:
				if message := turns.cancelActive(true); message != nil {
					message.SessionID = sessionID
					if err := writeServerMessage(ctx, connection, *message); err != nil {
						return err
					}
				}
				if ttsTimer != nil {
					ttsTimer.Stop()
				}
				ttsFinished = nil
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerTTSCanceled, SessionID: sessionID}); err != nil {
					return err
				}
			case ClientSessionPing:
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerSessionPong, SessionID: sessionID}); err != nil {
					return err
				}
			case ClientSessionFinish:
				turns.cancelActive(false)
				if progressSession.ID != "" {
					now := time.Now()
					saveProgress(&now)
					progressSession = activeProgressSession{}
				}
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerSessionFinished, SessionID: sessionID}); err != nil {
					return err
				}
				return connection.Close(websocket.StatusNormalClosure, "session finished")
			default:
				if err := writeProtocolError(ctx, connection, sessionID, "unknown_message_type", "unknown message type"); err != nil {
					return err
				}
			}
		}
	}
}

func (server *Server) serveDoubaoSession(ctx context.Context, connection *websocket.Conn, user *authenticatedUser) error {
	inbound := make(chan inboundFrame, 1)
	upstream := make(chan doubaoUpstreamEvent, 64)
	go readFrames(ctx, connection, inbound)

	var sessionID string
	var leasedSessionID string
	defer func() { server.releaseInterviewLease(user, leasedSessionID) }()
	var asr *doubaoASRStream
	var tts *doubaoTTSStream
	var lastASRFinalAt time.Time
	var ttsRequestedAt time.Time
	var ttsFirstAudioLogged bool
	var interviewTTS bool
	var interviewTTSPrefix string
	conversation := server.newInterviewConversation()
	progressSession := activeProgressSession{}
	saveProgress := func(endedAt *time.Time) {
		server.saveConversationProgress(conversation, progressSession, endedAt)
	}
	projectTasks, projectUpdates := server.startProjectUpdateWorker(ctx, conversation)
	turns := newInterviewTurnController(ctx, server, conversation)
	defer func() {
		turns.cancelActive(false)
		if progressSession.ID != "" {
			now := time.Now()
			saveProgress(&now)
		}
		if asr != nil {
			_ = asr.Close()
		}
		if tts != nil {
			_ = tts.Close()
		}
	}()

	providerError := func(source string, err error) error {
		server.logger.Warn("doubao voice upstream failed", "source", source, "error", server.safeProviderError(err))
		return writeServerMessage(ctx, connection, ServerMessage{
			Type: ServerError, SessionID: sessionID, Code: "voice_provider_error",
			Message: "语音服务暂时不可用，请稍后重试", Retryable: true,
		})
	}
	openInterviewTTS := func(expression string) error {
		if tts != nil {
			_ = tts.Cancel(ctx)
			_ = tts.Close()
			tts = nil
		}
		ttsRequestedAt = time.Now()
		ttsFirstAudioLogged = false
		opened, err := openDoubaoTTSSession(ctx, server.config, sessionID, withBiographySpeechPace(expression), server.doubaoDialer, upstream)
		if err != nil {
			ttsRequestedAt = time.Time{}
			return err
		}
		tts = opened
		interviewTTS = true
		server.logger.Info("biography voice latency", "stage", "tts_session_ready", "session_id", sessionID, "latency_ms", time.Since(ttsRequestedAt).Milliseconds())
		return nil
	}
	streamInterviewSpeech := func(message *ServerMessage, final bool) error {
		if message == nil || strings.TrimSpace(message.Text) == "" {
			return nil
		}
		if interviewTTSPrefix != "" && !strings.HasPrefix(message.Text, interviewTTSPrefix) {
			server.logger.Warn("biography streamed reply changed after speech started", "session_id", sessionID)
			if final && tts != nil && interviewTTS {
				return tts.Finish(ctx)
			}
			return nil
		}
		chunks, consumed := stableSpeechChunks(message.Text, len(interviewTTSPrefix), final)
		if len(chunks) == 0 {
			if final && tts != nil && interviewTTS {
				return tts.Finish(ctx)
			}
			return nil
		}
		if tts == nil || !interviewTTS {
			if err := openInterviewTTS(message.Expression); err != nil {
				return err
			}
		}
		for _, chunk := range chunks {
			if err := tts.SendText(ctx, chunk); err != nil {
				return err
			}
		}
		interviewTTSPrefix = message.Text[:consumed]
		if final {
			return tts.Finish(ctx)
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case turnEvent := <-turns.events:
			transcript, message := turns.handle(turnEvent)
			if transcript != "" {
				conversation.addPendingTranscript(transcript)
				progressSession.TranscriptCount++
				progressSession.TodayRecordingSaved = true
				saveProgress(nil)
				if err := enqueueProjectUpdate(ctx, projectTasks, transcript); err != nil {
					server.logger.Warn("biography project update deferred", "error", err)
				}
			}
			if message != nil {
				message.SessionID = sessionID
				streamSpeech := message.Type == ServerInterviewReply || (message.Type == ServerInterviewDelta && message.SpeechReady)
				if streamSpeech {
					final := message.Type == ServerInterviewReply
					if err := streamInterviewSpeech(message, final); err != nil {
						server.logger.Warn("stream biography reply to TTS", "error", server.safeProviderError(err))
						if tts != nil && interviewTTS {
							_ = tts.Close()
							tts = nil
						}
						interviewTTS = false
						interviewTTSPrefix = ""
					} else if final && interviewTTS {
						message.SpeechStarted = true
					}
				}
				if err := writeServerMessage(ctx, connection, *message); err != nil {
					return err
				}
			}
		case update := <-projectUpdates:
			if update.err == nil {
				conversation.markTranscriptOrganized(update.transcript)
			}
			if err := server.writeProjectUpdate(ctx, connection, sessionID, conversation, update); err != nil {
				return err
			}
			saveProgress(nil)
		case event := <-upstream:
			isCurrentASR := asr != nil && event.StreamID == asr.id
			isCurrentTTS := tts != nil && event.StreamID == tts.id
			if !isCurrentASR && !isCurrentTTS {
				continue
			}
			if event.Err != nil {
				if isCurrentASR && errors.Is(event.Err, errDoubaoASRNoTranscript) {
					_ = asr.Close()
					asr = nil
					if err := writeServerMessage(ctx, connection, ServerMessage{
						Type: ServerError, SessionID: sessionID, Code: "no_speech",
						Message: "我刚才没有听到内容，您想好了可以慢慢说一遍。", Retryable: true,
					}); err != nil {
						return err
					}
					continue
				}
				source := "TTS"
				if isCurrentASR {
					source = "ASR"
					_ = asr.Close()
					asr = nil
				} else {
					_ = tts.Close()
					tts = nil
				}
				if err := providerError(source, event.Err); err != nil {
					return err
				}
				continue
			}
			if len(event.Audio) > 0 && isCurrentTTS {
				if !ttsFirstAudioLogged {
					ttsFirstAudioLogged = true
					if !ttsRequestedAt.IsZero() {
						server.logger.Info("biography voice latency", "stage", "tts_first_audio", "session_id", sessionID, "latency_ms", time.Since(ttsRequestedAt).Milliseconds())
					}
					if !lastASRFinalAt.IsZero() {
						server.logger.Info("biography voice latency", "stage", "asr_final_to_first_audio", "session_id", sessionID, "latency_ms", time.Since(lastASRFinalAt).Milliseconds())
						lastASRFinalAt = time.Time{}
					}
				}
				if err := connection.Write(ctx, websocket.MessageBinary, event.Audio); err != nil {
					return err
				}
				continue
			}
			switch event.Type {
			case ServerASRPartial:
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: event.Type, SessionID: sessionID, Text: event.Text}); err != nil {
					return err
				}
			case ServerASRFinal:
				lastASRFinalAt = time.Now()
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: event.Type, SessionID: sessionID, Text: event.Text}); err != nil {
					return err
				}
				deferInterview := asr.deferInterview
				_ = asr.Close()
				asr = nil
				if !deferInterview {
					turns.start(event.Text)
				}
			case ServerTTSStarted:
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: event.Type, SessionID: sessionID}); err != nil {
					return err
				}
			case ServerTTSFinished, ServerTTSCanceled:
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: event.Type, SessionID: sessionID}); err != nil {
					return err
				}
				_ = tts.Close()
				tts = nil
				ttsRequestedAt = time.Time{}
				ttsFirstAudioLogged = false
				interviewTTS = false
				interviewTTSPrefix = ""
			}
		case frame := <-inbound:
			if frame.err != nil {
				return frame.err
			}
			if frame.messageType == websocket.MessageBinary {
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				if message := turns.cancelActive(true); message != nil {
					message.SessionID = sessionID
					if err := writeServerMessage(ctx, connection, *message); err != nil {
						return err
					}
				}
				if asr == nil {
					opened, err := openDoubaoASR(ctx, server.config, sessionID, server.doubaoDialer, upstream)
					if err != nil {
						if writeErr := providerError("ASR", err); writeErr != nil {
							return writeErr
						}
						continue
					}
					asr = opened
				}
				if err := asr.SendAudio(ctx, frame.payload); err != nil {
					_ = asr.Close()
					asr = nil
					if writeErr := providerError("ASR", err); writeErr != nil {
						return writeErr
					}
				}
				continue
			}

			var message ClientMessage
			if err := json.Unmarshal(frame.payload, &message); err != nil {
				if err := writeProtocolError(ctx, connection, sessionID, "invalid_json", "message must be valid JSON"); err != nil {
					return err
				}
				continue
			}
			switch message.Type {
			case ClientSessionStart:
				if strings.TrimSpace(message.SessionID) == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_id_required", "session_id is required"); err != nil {
						return err
					}
					continue
				}
				if err := server.prepareInterviewConversation(ctx, conversation, user, message); err != nil {
					server.logger.Warn("biography interview resume rejected", "error", server.safeProviderError(err))
					if writeErr := writeProtocolError(ctx, connection, sessionID, "resume_invalid", "上次采访暂时无法恢复，请重新开始"); writeErr != nil {
						return writeErr
					}
					continue
				}
				requestedSessionID := strings.TrimSpace(message.SessionID)
				if leasedSessionID != "" && leasedSessionID != requestedSessionID {
					if err := writeProtocolError(ctx, connection, sessionID, "session_already_started", "当前连接已有进行中的采访"); err != nil {
						return err
					}
					continue
				}
				if !server.acquireInterviewLease(user, requestedSessionID) {
					if err := writeProtocolError(ctx, connection, sessionID, "interview_busy", "这本书正在另一台设备上采访，请先在那边结束今天的采访"); err != nil {
						return err
					}
					continue
				}
				sessionID = requestedSessionID
				leasedSessionID = requestedSessionID
				progressSession = activeProgressSession{ID: sessionID, StartedAt: time.Now()}
				saveProgress(nil)
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerSessionReady, SessionID: sessionID}); err != nil {
					return err
				}
				project := conversation.projectSnapshot()
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerInterviewProject, SessionID: sessionID, Project: &project}); err != nil {
					return err
				}
				if err := server.writePendingChapterConfirmation(ctx, connection, sessionID, project); err != nil {
					return err
				}
				server.enqueuePendingProjectUpdates(ctx, projectTasks, conversation)
			case ClientInputCommit:
				if asr == nil {
					if err := writeProtocolError(ctx, connection, sessionID, "no_audio", "no active audio input"); err != nil {
						return err
					}
					continue
				}
				asr.deferInterview = message.DeferInterview
				if err := asr.Commit(ctx); err != nil {
					_ = asr.Close()
					asr = nil
					if writeErr := providerError("ASR", err); writeErr != nil {
						return writeErr
					}
				}
			case ClientInputCancel:
				if asr != nil {
					_ = asr.Close()
					asr = nil
				}
			case ClientInterviewFollowup:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				transcript := strings.TrimSpace(message.Text)
				if transcript == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "empty_transcript", "transcript is required"); err != nil {
						return err
					}
					continue
				}
				if confirmation, handled := server.applyChapterConfirmation(conversation, transcript); handled {
					saveProgress(nil)
					confirmation.SessionID = sessionID
					if err := writeServerMessage(ctx, connection, *confirmation); err != nil {
						return err
					}
					continue
				}
				turns.start(transcript)
			case ClientInterviewOrderSet:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				order := strings.TrimSpace(message.InterviewOrder)
				if !validInterviewOrder(order) {
					if err := writeProtocolError(ctx, connection, sessionID, "invalid_interview_order", "interview order must be chronological, key_moments, or custom"); err != nil {
						return err
					}
					continue
				}
				conversation.setInterviewOrder(order)
				saveProgress(nil)
				project := conversation.projectSnapshot()
				if err := writeServerMessage(ctx, connection, ServerMessage{
					Type: ServerProjectUpdated, SessionID: sessionID, Project: &project,
					ResumeToken: server.interviewResumeToken(conversation, project),
				}); err != nil {
					return err
				}
			case ClientInterviewChapterFocus:
				if sessionID == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "session_not_started", "start the session first"); err != nil {
						return err
					}
					continue
				}
				if err := conversation.setFocusedChapter(message.ChapterID); err != nil {
					if err := writeProtocolError(ctx, connection, sessionID, "invalid_chapter", "chapter must belong to this biography"); err != nil {
						return err
					}
				}
			case ClientTTSStart:
				if sessionID == "" || strings.TrimSpace(message.Text) == "" {
					if err := writeProtocolError(ctx, connection, sessionID, "invalid_tts_request", "active session and text are required"); err != nil {
						return err
					}
					continue
				}
				if tts != nil {
					_ = tts.Cancel(ctx)
					_ = tts.Close()
					tts = nil
				}
				ttsRequestedAt = time.Now()
				ttsFirstAudioLogged = false
				opened, err := openDoubaoTTS(ctx, server.config, sessionID, strings.TrimSpace(message.Text), message.Expression, server.doubaoDialer, upstream)
				if err != nil {
					if writeErr := providerError("TTS", err); writeErr != nil {
						return writeErr
					}
					continue
				}
				tts = opened
				interviewTTS = false
				interviewTTSPrefix = ""
				server.logger.Info("biography voice latency", "stage", "tts_session_ready", "session_id", sessionID, "latency_ms", time.Since(ttsRequestedAt).Milliseconds())
			case ClientTTSCancel:
				if message := turns.cancelActive(true); message != nil {
					message.SessionID = sessionID
					if err := writeServerMessage(ctx, connection, *message); err != nil {
						return err
					}
				}
				if tts == nil {
					interviewTTS = false
					interviewTTSPrefix = ""
					if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerTTSCanceled, SessionID: sessionID}); err != nil {
						return err
					}
					continue
				}
				if err := tts.Cancel(ctx); err != nil {
					_ = tts.Close()
					tts = nil
					if writeErr := providerError("TTS", err); writeErr != nil {
						return writeErr
					}
				}
				interviewTTS = false
				interviewTTSPrefix = ""
			case ClientSessionPing:
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerSessionPong, SessionID: sessionID}); err != nil {
					return err
				}
			case ClientSessionFinish:
				turns.cancelActive(false)
				if progressSession.ID != "" {
					now := time.Now()
					saveProgress(&now)
					progressSession = activeProgressSession{}
				}
				if asr != nil {
					_ = asr.Close()
					asr = nil
				}
				if tts != nil {
					_ = tts.Close()
					tts = nil
				}
				if err := writeServerMessage(ctx, connection, ServerMessage{Type: ServerSessionFinished, SessionID: sessionID}); err != nil {
					return err
				}
				return connection.Close(websocket.StatusNormalClosure, "session finished")
			default:
				if err := writeProtocolError(ctx, connection, sessionID, "unknown_message_type", "unknown message type"); err != nil {
					return err
				}
			}
		}
	}
}

func (server *Server) safeProviderError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if secret := strings.TrimSpace(server.config.DoubaoAPIKey); secret != "" {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	if secret := strings.TrimSpace(server.config.TMAAuthToken); secret != "" {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return errors.New(message)
}

func (server *Server) newInterviewConversation() *interviewConversation {
	return &interviewConversation{Project: newBiographyProject()}
}

func (server *Server) prepareInterviewConversation(ctx context.Context, conversation *interviewConversation, user *authenticatedUser, message ClientMessage) error {
	if user != nil {
		conversation.UserID = user.ID
		conversation.TMAAccessToken = user.AccessToken
		server.loadStoredConversation(conversation)
	}
	if valueOrDefault(server.config.InterviewProvider, ProviderMock) != ProviderTMA {
		return nil
	}
	clientInstanceID := strings.TrimSpace(message.ClientInstanceID)
	if clientInstanceID == "" || len(clientInstanceID) > 128 {
		return errors.New("client instance ID is required for TMA interview sessions")
	}
	conversation.ClientInstanceID = clientInstanceID
	if strings.TrimSpace(message.ResumeToken) == "" {
		return nil
	}
	expectedUserID := ""
	if user != nil {
		expectedUserID = user.ID
	}
	claims, err := server.resumeTokens.Decode(message.ResumeToken, clientInstanceID, expectedUserID)
	if err != nil {
		return err
	}
	resumeCtx, cancel := context.WithTimeout(ctx, server.config.InterviewTimeout)
	defer cancel()
	if err := server.interview.Resume(resumeCtx, conversation, claims.TMASessionID); err != nil {
		return err
	}
	if claims.Project != nil {
		project, _ := removeLegacyEmptyChapterTemplate(*claims.Project)
		if err := validateBiographyProject(project); err != nil {
			return fmt.Errorf("resume token contains invalid biography project: %w", err)
		}
		conversation.replaceProject(project)
	}
	return nil
}

func (server *Server) loadStoredConversation(conversation *interviewConversation) {
	if server.store == nil || strings.TrimSpace(conversation.UserID) == "" {
		return
	}
	progress, ok, err := server.store.progressForUser(conversation.UserID)
	if err != nil {
		server.logger.Warn("biography progress load failed", "error", server.safeProviderError(err))
		return
	}
	if !ok {
		return
	}
	project, changed := removeLegacyEmptyChapterTemplate(progress.Project)
	if changed {
		progress.Project = project
		progress.ActiveChapterTitles = activeChapterTitles(project)
		progress.PendingConfirmation = ""
		progress.UpdatedAt = time.Now()
		if err := server.store.saveProgress(conversation.UserID, progress); err != nil {
			server.logger.Warn("biography progress migration failed", "error", server.safeProviderError(err))
		}
	}
	if err := validateBiographyProject(project); err == nil {
		conversation.replaceProject(project)
	}
	conversation.projectMu.Lock()
	conversation.RecentQuestions = append([]string(nil), progress.RecentQuestions...)
	conversation.PendingTranscripts = append([]string(nil), progress.PendingTranscripts...)
	conversation.projectMu.Unlock()
}

func (server *Server) saveConversationProgress(conversation *interviewConversation, session activeProgressSession, endedAt *time.Time) {
	if server.store == nil || strings.TrimSpace(conversation.UserID) == "" {
		return
	}
	project := conversation.projectSnapshot()
	recent := conversation.recentQuestionsSnapshot()
	pending := conversation.pendingTranscriptsSnapshot()
	progress := buildBiographyProgress(project, recent, pending, session, endedAt, time.Now())
	if err := server.store.saveProgress(conversation.UserID, progress); err != nil {
		server.logger.Warn("biography progress save failed", "error", server.safeProviderError(err))
	}
}

func (server *Server) runInterviewTurn(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	turnID uint64,
	conversation *interviewConversation,
	transcript string,
	events chan<- interviewTurnEvent,
) {
	turnStartedAt := time.Now()
	firstResponseTimeout := server.config.InterviewFirstResponseTimeout
	if firstResponseTimeout <= 0 {
		firstResponseTimeout = 6 * time.Second
	}
	totalTimeout := server.config.InterviewTimeout
	if totalTimeout <= 0 {
		totalTimeout = 45 * time.Second
	}
	var firstResponseOnce sync.Once
	var firstTextOnce sync.Once
	var firstResponse *time.Timer
	firstResponse = time.AfterFunc(firstResponseTimeout, func() {
		firstResponseOnce.Do(func() { cancel(errInterviewFirstResponseTimeout) })
	})
	total := time.AfterFunc(totalTimeout, func() { cancel(errInterviewTotalTimeout) })
	defer firstResponse.Stop()
	defer total.Stop()

	emit := func(event interviewTurnEvent) bool {
		select {
		case events <- event:
			return true
		default:
		}
		select {
		case events <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	markResponding := func() {
		firstResponseOnce.Do(func() { firstResponse.Stop() })
	}
	markFirstText := func() {
		firstTextOnce.Do(func() {
			server.logger.Info("biography voice latency", "stage", "llm_first_text", "turn_id", turnID, "latency_ms", time.Since(turnStartedAt).Milliseconds())
		})
	}

	var reply InterviewReply
	var err error
	if streaming, ok := server.interview.(streamingInterviewEngine); ok {
		reply, err = streaming.ContinueStreaming(ctx, conversation, transcript, func(preview InterviewReplyPreview) error {
			markResponding()
			markFirstText()
			if emit(interviewTurnEvent{id: turnID, message: &ServerMessage{
				Type: ServerInterviewDelta, Text: preview.Text, Expression: preview.Expression,
				NeedsRetry: preview.NeedsRetry, SpeechReady: preview.ControlsReady,
			}}) {
				return nil
			}
			return context.Cause(ctx)
		})
	} else {
		reply, err = server.interview.Continue(ctx, conversation, transcript)
	}
	markResponding()
	if err != nil {
		server.logger.Info("biography voice latency", "stage", "llm_failed", "turn_id", turnID, "latency_ms", time.Since(turnStartedAt).Milliseconds())
		cause := context.Cause(ctx)
		if errors.Is(cause, errInterviewInterrupted) {
			_ = emit(interviewTurnEvent{id: turnID, done: true})
			return
		}
		server.logger.Warn("biography interview turn failed", "provider", valueOrDefault(server.config.InterviewProvider, ProviderMock), "error", server.safeProviderError(err))
		text := "刚才连接有些慢。您刚才这段话已先保存，之后会继续整理。您可以接着讲。"
		if errors.Is(cause, errInterviewFirstResponseTimeout) || errors.Is(cause, errInterviewTotalTimeout) || errors.Is(err, context.DeadlineExceeded) {
			text = interviewFallbackQuestion(transcript)
		}
		message := server.interviewFallbackMessage(conversation, text)
		_ = emit(interviewTurnEvent{id: turnID, message: &message, transcript: transcript, done: true, accepted: true, failed: true})
		return
	}
	markFirstText()
	server.logger.Info("biography voice latency", "stage", "llm_complete", "turn_id", turnID, "latency_ms", time.Since(turnStartedAt).Milliseconds())
	message := server.interviewReplyMessage(conversation, reply)
	_ = emit(interviewTurnEvent{id: turnID, message: &message})
	completed := interviewTurnEvent{id: turnID, done: true}
	if !reply.NeedsRetry {
		completed.transcript = transcript
		completed.accepted = true
	}
	_ = emit(completed)
}

func (server *Server) interviewReplyMessage(conversation *interviewConversation, reply InterviewReply) ServerMessage {
	if !reply.NeedsRetry {
		conversation.recordQuestion(reply.Text)
	}
	resumeToken := server.interviewResumeToken(conversation, reply.Project)
	return ServerMessage{
		Type: ServerInterviewReply, Text: reply.Text, Expression: reply.Expression, NeedsRetry: reply.NeedsRetry,
		Project: &reply.Project, ResumeToken: resumeToken,
	}
}

func (server *Server) interviewFallbackMessage(conversation *interviewConversation, text string) ServerMessage {
	conversation.recordQuestion(text)
	project := conversation.projectSnapshot()
	return ServerMessage{
		Type: ServerInterviewReply, Text: text,
		Expression: "温和、简短、让对方放心，语速稍慢", Project: &project,
		ResumeToken: server.interviewResumeToken(conversation, project),
	}
}

func interviewFallbackQuestion(transcript string) string {
	switch {
	case strings.Contains(transcript, "师傅") || strings.Contains(transcript, "老师"):
		return "我先接着问一个简单的。您刚才提到这位师傅，最记得他教您的哪个动作或规矩？"
	case strings.Contains(transcript, "父亲") || strings.Contains(transcript, "爸爸"):
		return "我先接着问一个简单的。说到您父亲，您脑子里最先浮出来的是他的哪个样子？"
	case strings.Contains(transcript, "上海") || strings.Contains(transcript, "离开家") || strings.Contains(transcript, "出门"):
		return "我先接着问一个简单的。您到上海或离家出门那一刻，第一眼最记得的是什么？"
	default:
		return "我先接着问一个简单的。回到刚才那段经历里，您现在最清楚记得的一个画面是什么？"
	}
}

func (server *Server) interviewResumeToken(conversation *interviewConversation, project BiographyProject) string {
	if server.resumeTokens == nil || strings.TrimSpace(conversation.TMASessionID) == "" {
		return ""
	}
	resumeToken, err := server.resumeTokens.EncodeState(conversation.TMASessionID, conversation.ClientInstanceID, conversation.UserID, &project)
	if err != nil {
		server.logger.Warn("biography resume token creation failed", "error", server.safeProviderError(err))
		return ""
	}
	return resumeToken
}

func (server *Server) startProjectUpdateWorker(ctx context.Context, conversation *interviewConversation) (chan<- string, <-chan projectUpdateResult) {
	tasks := make(chan string, 16)
	results := make(chan projectUpdateResult, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case transcript := <-tasks:
				timeout := server.config.InterviewTimeout
				if timeout <= 0 {
					timeout = 90 * time.Second
				}
				updateCtx, cancel := context.WithTimeout(ctx, timeout)
				project, err := server.interview.Organize(updateCtx, conversation, transcript)
				cancel()
				select {
				case results <- projectUpdateResult{project: project, transcript: transcript, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return tasks, results
}

func (server *Server) enqueuePendingProjectUpdates(ctx context.Context, tasks chan<- string, conversation *interviewConversation) {
	for _, transcript := range conversation.pendingTranscriptsSnapshot() {
		if err := enqueueProjectUpdate(ctx, tasks, transcript); err != nil {
			server.logger.Warn("biography pending project update deferred", "error", err)
			return
		}
	}
}

func enqueueProjectUpdate(ctx context.Context, tasks chan<- string, transcript string) error {
	select {
	case tasks <- transcript:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errProjectUpdateQueueFull
	}
}

func (server *Server) writeProjectUpdate(ctx context.Context, connection *websocket.Conn, sessionID string, conversation *interviewConversation, update projectUpdateResult) error {
	if update.err != nil {
		server.logger.Warn("biography project update failed", "provider", valueOrDefault(server.config.InterviewProvider, ProviderMock), "error", server.safeProviderError(update.err))
		return nil
	}
	resumeToken := ""
	if server.resumeTokens != nil {
		var err error
		resumeToken, err = server.resumeTokens.EncodeState(conversation.TMASessionID, conversation.ClientInstanceID, conversation.UserID, &update.project)
		if err != nil {
			server.logger.Warn("biography project resume token creation failed", "error", server.safeProviderError(err))
			return nil
		}
	}
	if err := writeServerMessage(ctx, connection, ServerMessage{
		Type: ServerProjectUpdated, SessionID: sessionID, Project: &update.project, ResumeToken: resumeToken,
	}); err != nil {
		return err
	}
	return server.writePendingChapterConfirmation(ctx, connection, sessionID, update.project)
}

func (server *Server) writePendingChapterConfirmation(ctx context.Context, connection *websocket.Conn, sessionID string, project BiographyProject) error {
	if strings.TrimSpace(project.PendingConfirmation) == "" || strings.TrimSpace(project.PendingConfirmationChapterID) == "" {
		return nil
	}
	return writeServerMessage(ctx, connection, ServerMessage{
		Type: ServerChapterConfirmation, SessionID: sessionID, Text: project.PendingConfirmation,
		Expression: "温和、清晰，像传记编辑朗读一段整理后的文字，语速稍慢",
		ChapterID:  project.PendingConfirmationChapterID, Project: &project,
	})
}

func readFrames(ctx context.Context, connection *websocket.Conn, output chan<- inboundFrame) {
	for {
		messageType, payload, err := connection.Read(ctx)
		output <- inboundFrame{messageType: messageType, payload: payload, err: err}
		if err != nil {
			return
		}
	}
}

func writeProtocolError(ctx context.Context, connection *websocket.Conn, sessionID string, code string, message string) error {
	return writeServerMessage(ctx, connection, ServerMessage{Type: ServerError, SessionID: sessionID, Code: code, Message: message})
}

func writeServerMessage(ctx context.Context, connection *websocket.Conn, message ServerMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func isExpectedClose(err error) bool {
	status := websocket.CloseStatus(err)
	return errors.Is(err, context.Canceled) || status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}
