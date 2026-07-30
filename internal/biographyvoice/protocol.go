package biographyvoice

const (
	ClientSessionStart          = "session.start"
	ClientASRDebugText          = "asr.debug_text"
	ClientInputCommit           = "input.commit"
	ClientInputCancel           = "input.cancel"
	ClientInterviewFollowup     = "interview.followup"
	ClientInterviewOrderSet     = "interview.order.set"
	ClientInterviewChapterFocus = "interview.chapter.focus"
	ClientTTSStart              = "tts.start"
	ClientTTSCancel             = "tts.cancel"
	ClientSessionPing           = "session.ping"
	ClientSessionFinish         = "session.finish"

	ServerSessionReady        = "session.ready"
	ServerASRPartial          = "asr.partial"
	ServerASRFinal            = "asr.final"
	ServerInterviewProject    = "interview.project"
	ServerProjectUpdated      = "interview.project.updated"
	ServerChapterConfirmation = "chapter.confirmation"
	ServerInterviewDelta      = "interview.reply.delta"
	ServerInterviewReply      = "interview.reply"
	ServerTTSStarted          = "tts.started"
	ServerTTSFinished         = "tts.finished"
	ServerTTSCanceled         = "tts.canceled"
	ServerSessionPong         = "session.pong"
	ServerInterviewCanceled   = "interview.reply.canceled"
	ServerSessionFinished     = "session.finished"
	ServerError               = "error"
)

type ClientMessage struct {
	Type             string `json:"type"`
	SessionID        string `json:"session_id,omitempty"`
	ClientInstanceID string `json:"client_instance_id,omitempty"`
	ResumeToken      string `json:"resume_token,omitempty"`
	Text             string `json:"text,omitempty"`
	Expression       string `json:"expression,omitempty"`
	InterviewOrder   string `json:"interview_order,omitempty"`
	ChapterID        string `json:"chapter_id,omitempty"`
	DeferInterview   bool   `json:"defer_interview,omitempty"`
}

type ServerMessage struct {
	Type          string            `json:"type"`
	SessionID     string            `json:"session_id,omitempty"`
	Text          string            `json:"text,omitempty"`
	Code          string            `json:"code,omitempty"`
	Message       string            `json:"message,omitempty"`
	Retryable     bool              `json:"retryable,omitempty"`
	AudioBytes    int64             `json:"audio_bytes,omitempty"`
	Expression    string            `json:"expression,omitempty"`
	NeedsRetry    bool              `json:"needs_retry,omitempty"`
	SpeechReady   bool              `json:"speech_ready,omitempty"`
	SpeechStarted bool              `json:"speech_started,omitempty"`
	ChapterID     string            `json:"chapter_id,omitempty"`
	Project       *BiographyProject `json:"project,omitempty"`
	ResumeToken   string            `json:"resume_token,omitempty"`
}
