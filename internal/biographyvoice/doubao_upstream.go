package biographyvoice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const doubaoHandshakeTimeout = 15 * time.Second

var errDoubaoASRNoTranscript = errors.New("doubao ASR ended before returning a transcript")

type doubaoConnection interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
	Write(context.Context, websocket.MessageType, []byte) error
	Close(websocket.StatusCode, string) error
}

type doubaoDialer func(context.Context, string, http.Header) (doubaoConnection, error)

func defaultDoubaoDialer(ctx context.Context, target string, headers http.Header) (doubaoConnection, error) {
	connection, response, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return nil, doubaoHandshakeError(err, response)
	}
	connection.SetReadLimit(maxVoiceFrameBytes)
	return connection, nil
}

func doubaoHandshakeError(dialErr error, response *http.Response) error {
	if response == nil {
		return dialErr
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	detail := strings.TrimSpace(string(body))
	parts := []string{fmt.Sprintf("HTTP %d", response.StatusCode)}
	if logID := strings.TrimSpace(response.Header.Get("X-Tt-Logid")); logID != "" {
		parts = append(parts, "logid="+logID)
	}
	if detail != "" {
		parts = append(parts, "response="+detail)
	}
	return fmt.Errorf("%w (%s)", dialErr, strings.Join(parts, ", "))
}

type doubaoUpstreamEvent struct {
	StreamID string
	Type     string
	Text     string
	Audio    []byte
	Err      error
}

type doubaoASRStream struct {
	connection     doubaoConnection
	id             string
	events         chan<- doubaoUpstreamEvent
	writeMu        sync.Mutex
	stateMu        sync.Mutex
	committed      bool
	deferInterview bool
	lastText       string
	finalSent      bool
	closeOnce      sync.Once
}

func openDoubaoASR(ctx context.Context, config Config, sessionID string, dial doubaoDialer, events chan<- doubaoUpstreamEvent) (*doubaoASRStream, error) {
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, doubaoHandshakeTimeout)
	defer cancelHandshake()
	connectID := newDoubaoID("asr")
	headers := doubaoHeaders(config.DoubaoAPIKey, config.DoubaoASRResourceID, connectID)
	connection, err := dial(handshakeCtx, config.DoubaoASRURL, headers)
	if err != nil {
		return nil, fmt.Errorf("connect doubao ASR: %w", err)
	}
	stream := &doubaoASRStream{connection: connection, id: connectID, events: events}
	startPayload, err := json.Marshal(map[string]any{
		"user":  map[string]any{"uid": sessionID},
		"audio": map[string]any{"format": "pcm", "rate": 16000, "bits": 16, "channel": 1},
		"request": map[string]any{
			"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
			"show_utterances": true, "result_type": "single",
		},
	})
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	frame, err := buildDoubaoASRStart(startPayload)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.write(handshakeCtx, frame); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("start doubao ASR: %w", err)
	}
	go stream.readLoop(ctx)
	return stream, nil
}

func (stream *doubaoASRStream) SendAudio(ctx context.Context, audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	frame, err := buildDoubaoASRAudio(audio, false)
	if err != nil {
		return err
	}
	return stream.write(ctx, frame)
}

func (stream *doubaoASRStream) Commit(ctx context.Context) error {
	frame, err := buildDoubaoASRAudio(nil, true)
	if err != nil {
		return err
	}
	stream.stateMu.Lock()
	stream.committed = true
	stream.stateMu.Unlock()
	if err := stream.write(ctx, frame); err != nil {
		stream.stateMu.Lock()
		stream.committed = false
		stream.stateMu.Unlock()
		return err
	}
	return nil
}

func (stream *doubaoASRStream) Close() error {
	var closeErr error
	stream.closeOnce.Do(func() {
		closeErr = stream.connection.Close(websocket.StatusNormalClosure, "ASR turn complete")
	})
	return closeErr
}

func (stream *doubaoASRStream) write(ctx context.Context, frame []byte) error {
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	return stream.connection.Write(ctx, websocket.MessageBinary, frame)
}

func (stream *doubaoASRStream) readLoop(ctx context.Context) {
	for {
		messageType, payload, err := stream.connection.Read(ctx)
		if err != nil {
			if ctx.Err() == nil && stream.finishCommittedResult() {
				return
			}
			if ctx.Err() == nil && websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				stream.push(doubaoUpstreamEvent{Err: fmt.Errorf("read doubao ASR: %w", err)})
			}
			return
		}
		if messageType != websocket.MessageBinary {
			stream.push(doubaoUpstreamEvent{Err: fmt.Errorf("doubao ASR returned a non-binary frame")})
			return
		}
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			stream.push(doubaoUpstreamEvent{Err: err})
			return
		}
		if frame.MessageType == doubaoMessageError {
			stream.push(doubaoUpstreamEvent{Err: doubaoFrameError("ASR", frame)})
			return
		}
		if frame.MessageType != doubaoMessageFullServer {
			continue
		}
		result, final, err := decodeDoubaoASRResult(frame)
		if err != nil {
			stream.push(doubaoUpstreamEvent{Err: err})
			return
		}
		if result == "" {
			continue
		}
		stream.stateMu.Lock()
		stream.lastText = result
		if final {
			stream.finalSent = true
		}
		stream.stateMu.Unlock()
		eventType := ServerASRPartial
		if final {
			eventType = ServerASRFinal
		}
		stream.push(doubaoUpstreamEvent{Type: eventType, Text: result})
	}
}

func (stream *doubaoASRStream) finishCommittedResult() bool {
	stream.stateMu.Lock()
	defer stream.stateMu.Unlock()
	if !stream.committed || stream.finalSent {
		return false
	}
	stream.finalSent = true
	if stream.lastText == "" {
		stream.push(doubaoUpstreamEvent{Err: errDoubaoASRNoTranscript})
		return true
	}
	stream.push(doubaoUpstreamEvent{Type: ServerASRFinal, Text: stream.lastText})
	return true
}

func (stream *doubaoASRStream) push(event doubaoUpstreamEvent) {
	event.StreamID = stream.id
	select {
	case stream.events <- event:
	default:
		select {
		case stream.events <- doubaoUpstreamEvent{StreamID: stream.id, Err: fmt.Errorf("doubao ASR event buffer is full")}:
		default:
		}
	}
}

type doubaoTTSStream struct {
	connection   doubaoConnection
	id           string
	sessionID    string
	appSessionID string
	events       chan<- doubaoUpstreamEvent
	writeMu      sync.Mutex
	stateMu      sync.Mutex
	finished     bool
	closeOnce    sync.Once
}

func openDoubaoTTS(ctx context.Context, config Config, appSessionID string, text string, expression string, dial doubaoDialer, events chan<- doubaoUpstreamEvent) (*doubaoTTSStream, error) {
	stream, err := openDoubaoTTSSession(ctx, config, appSessionID, expression, dial, events)
	if err != nil {
		return nil, err
	}
	if err := stream.SendText(ctx, text); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.Finish(ctx); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

func openDoubaoTTSSession(ctx context.Context, config Config, appSessionID string, expression string, dial doubaoDialer, events chan<- doubaoUpstreamEvent) (*doubaoTTSStream, error) {
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, doubaoHandshakeTimeout)
	defer cancelHandshake()
	connectID := newDoubaoID("tts")
	headers := doubaoHeaders(config.DoubaoAPIKey, config.DoubaoTTSResourceID, connectID)
	connection, err := dial(handshakeCtx, config.DoubaoTTSURL, headers)
	if err != nil {
		return nil, fmt.Errorf("connect doubao TTS: %w", err)
	}
	stream := &doubaoTTSStream{
		connection: connection, id: connectID, sessionID: newDoubaoID("tts-session"),
		appSessionID: appSessionID, events: events,
	}
	if err := stream.sendConnectEvent(handshakeCtx, doubaoEventStartConnection, map[string]any{"namespace": "BidirectionalTTS"}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.waitForEvent(handshakeCtx, doubaoEventConnectionStarted); err != nil {
		_ = stream.Close()
		return nil, err
	}
	reqParams := map[string]any{
		"speaker":      config.DoubaoTTSSpeaker,
		"audio_params": map[string]any{"format": "pcm", "sample_rate": 24000},
		"section_id":   appSessionID,
	}
	if strings.TrimSpace(config.DoubaoTTSModel) != "" {
		reqParams["model"] = strings.TrimSpace(config.DoubaoTTSModel)
	}
	request := map[string]any{
		"user":       map[string]any{"uid": appSessionID},
		"event":      doubaoEventStartSession,
		"req_params": reqParams,
	}
	if strings.TrimSpace(expression) != "" {
		request["req_params"].(map[string]any)["context_texts"] = []string{strings.TrimSpace(expression)}
	}
	if err := stream.sendSessionEvent(handshakeCtx, doubaoEventStartSession, request); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.waitForEvent(handshakeCtx, doubaoEventSessionStarted); err != nil {
		_ = stream.Close()
		return nil, err
	}
	stream.push(doubaoUpstreamEvent{Type: ServerTTSStarted})
	go stream.readLoop(ctx)
	return stream, nil
}

func (stream *doubaoTTSStream) SendText(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	stream.stateMu.Lock()
	finished := stream.finished
	stream.stateMu.Unlock()
	if finished {
		return errors.New("doubao TTS session already finished")
	}
	return stream.sendSessionEvent(ctx, doubaoEventTaskRequest, map[string]any{
		"user": map[string]any{"uid": stream.appSessionID}, "event": doubaoEventTaskRequest,
		"req_params": map[string]any{"text": text},
	})
}

func (stream *doubaoTTSStream) Finish(ctx context.Context) error {
	stream.stateMu.Lock()
	if stream.finished {
		stream.stateMu.Unlock()
		return nil
	}
	stream.finished = true
	stream.stateMu.Unlock()
	return stream.sendSessionEvent(ctx, doubaoEventFinishSession, map[string]any{})
}

func (stream *doubaoTTSStream) Cancel(ctx context.Context) error {
	stream.stateMu.Lock()
	stream.finished = true
	stream.stateMu.Unlock()
	return stream.sendSessionEvent(ctx, doubaoEventCancelSession, map[string]any{})
}

func (stream *doubaoTTSStream) Close() error {
	var closeErr error
	stream.closeOnce.Do(func() {
		finish, err := buildDoubaoTTSConnectEvent(doubaoEventFinishConnection, []byte(`{}`))
		if err == nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = stream.write(closeCtx, finish)
			cancel()
		}
		closeErr = stream.connection.Close(websocket.StatusNormalClosure, "TTS complete")
	})
	return closeErr
}

func (stream *doubaoTTSStream) sendConnectEvent(ctx context.Context, event int32, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame, err := buildDoubaoTTSConnectEvent(event, body)
	if err != nil {
		return err
	}
	return stream.write(ctx, frame)
}

func (stream *doubaoTTSStream) sendSessionEvent(ctx context.Context, event int32, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame, err := buildDoubaoTTSSessionEvent(stream.sessionID, event, body)
	if err != nil {
		return err
	}
	return stream.write(ctx, frame)
}

func (stream *doubaoTTSStream) write(ctx context.Context, frame []byte) error {
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	return stream.connection.Write(ctx, websocket.MessageBinary, frame)
}

func (stream *doubaoTTSStream) waitForEvent(ctx context.Context, expected int32) error {
	for {
		messageType, payload, err := stream.connection.Read(ctx)
		if err != nil {
			return fmt.Errorf("wait for doubao TTS event %d: %w", expected, err)
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return err
		}
		if frame.MessageType == doubaoMessageError || frame.Event == doubaoEventConnectionFailed || frame.Event == doubaoEventSessionFailed {
			return doubaoFrameError("TTS", frame)
		}
		if frame.Event == expected {
			return nil
		}
	}
}

func (stream *doubaoTTSStream) readLoop(ctx context.Context) {
	defer stream.Close()
	for {
		messageType, payload, err := stream.connection.Read(ctx)
		if err != nil {
			if ctx.Err() == nil && websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				stream.push(doubaoUpstreamEvent{Err: fmt.Errorf("read doubao TTS: %w", err)})
			}
			return
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			stream.push(doubaoUpstreamEvent{Err: err})
			return
		}
		if frame.MessageType == doubaoMessageError || frame.Event == doubaoEventSessionFailed {
			stream.push(doubaoUpstreamEvent{Err: doubaoFrameError("TTS", frame)})
			return
		}
		if frame.MessageType == doubaoMessageAudioServer || frame.Event == doubaoEventTTSResponse {
			if len(frame.Payload) > 0 {
				stream.push(doubaoUpstreamEvent{Audio: frame.Payload})
			}
		}
		switch frame.Event {
		case doubaoEventSessionFinished:
			stream.push(doubaoUpstreamEvent{Type: ServerTTSFinished})
			return
		case doubaoEventSessionCanceled:
			stream.push(doubaoUpstreamEvent{Type: ServerTTSCanceled})
			return
		}
	}
}

func (stream *doubaoTTSStream) push(event doubaoUpstreamEvent) {
	event.StreamID = stream.id
	select {
	case stream.events <- event:
	default:
		select {
		case stream.events <- doubaoUpstreamEvent{StreamID: stream.id, Err: fmt.Errorf("doubao TTS event buffer is full")}:
		default:
		}
	}
}

func doubaoHeaders(apiKey string, resourceID string, connectID string) http.Header {
	headers := make(http.Header)
	headers.Set("X-Api-Key", apiKey)
	headers.Set("X-Api-Resource-Id", resourceID)
	headers.Set("X-Api-Connect-Id", connectID)
	return headers
}

func decodeDoubaoASRResult(frame doubaoFrame) (string, bool, error) {
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			Text       string `json:"text"`
			Utterances []struct {
				Definite bool `json:"definite"`
			} `json:"utterances"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frame.Payload, &response); err != nil {
		return "", false, fmt.Errorf("decode doubao ASR result: %w", err)
	}
	if response.Code != 0 && response.Code != 20000000 {
		return "", false, fmt.Errorf("doubao ASR error %d: %s", response.Code, response.Message)
	}
	final := frame.Flags&0x03 == doubaoFlagLastNoSequence || frame.Flags&0x03 == doubaoFlagLastWithSequence
	return strings.TrimSpace(response.Result.Text), final, nil
}

func doubaoFrameError(service string, frame doubaoFrame) error {
	message := strings.TrimSpace(string(frame.Payload))
	if message == "" {
		message = "unknown upstream error"
	}
	return fmt.Errorf("doubao %s error %d: %s", service, frame.ErrorCode, message)
}

func newDoubaoID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-fallback"
	}
	return prefix + "-" + hex.EncodeToString(value)
}
