package tma

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

type SpeechSessionStart struct {
	ProviderID   string `json:"provider_id"`
	Model        string `json:"model"`
	SessionID    string `json:"session_id,omitempty"`
	Voice        string `json:"voice,omitempty"`
	Style        string `json:"style,omitempty"`
	AudioFormat  string `json:"audio_format,omitempty"`
	SampleRateHz int    `json:"sample_rate_hz,omitempty"`
}

type SpeechEvent struct {
	Type              string `json:"type"`
	SessionID         string `json:"session_id,omitempty"`
	Mode              string `json:"mode,omitempty"`
	Text              string `json:"text,omitempty"`
	Audio             []byte `json:"-"`
	AudioFormat       string `json:"audio_format,omitempty"`
	SampleRateHz      int    `json:"sample_rate_hz,omitempty"`
	Code              string `json:"code,omitempty"`
	Message           string `json:"message,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	LimitScope        string `json:"limit_scope,omitempty"`
}

type SpeechService struct{ client *Client }

type SpeechRealtimeClient struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
}

func (s *SpeechService) DialRealtime(ctx context.Context) (*SpeechRealtimeClient, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("tma: speech service is not initialized")
	}
	request, err := s.client.newRequest(ctx, http.MethodGet, "/v2/speech/realtime", nil)
	if err != nil {
		return nil, err
	}
	target := *request.URL
	if target.Scheme == "https" {
		target.Scheme = "wss"
	} else {
		target.Scheme = "ws"
	}
	connection, response, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{HTTPHeader: request.Header})
	if err != nil {
		if response != nil {
			defer response.Body.Close()
		}
		return nil, err
	}
	connection.SetReadLimit(1024 * 1024)
	return &SpeechRealtimeClient{connection: connection}, nil
}

func (c *SpeechRealtimeClient) Start(ctx context.Context, request SpeechSessionStart) error {
	return c.writeJSON(ctx, map[string]any{
		"type": "session.start", "provider_id": request.ProviderID, "model": request.Model,
		"session_id": request.SessionID, "voice": request.Voice, "style": request.Style,
		"audio_format": request.AudioFormat, "sample_rate_hz": request.SampleRateHz,
	})
}

func (c *SpeechRealtimeClient) SendAudio(ctx context.Context, audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	return c.write(ctx, websocket.MessageBinary, audio)
}

func (c *SpeechRealtimeClient) CommitAudio(ctx context.Context) error {
	return c.writeJSON(ctx, map[string]any{"type": "audio.commit"})
}

func (c *SpeechRealtimeClient) AppendText(ctx context.Context, text string) error {
	return c.writeJSON(ctx, map[string]any{"type": "text.append", "text": text})
}

func (c *SpeechRealtimeClient) CommitText(ctx context.Context) error {
	return c.writeJSON(ctx, map[string]any{"type": "text.commit"})
}

func (c *SpeechRealtimeClient) Cancel(ctx context.Context) error {
	return c.writeJSON(ctx, map[string]any{"type": "session.cancel"})
}

func (c *SpeechRealtimeClient) Read(ctx context.Context) (SpeechEvent, error) {
	messageType, payload, err := c.connection.Read(ctx)
	if err != nil {
		return SpeechEvent{}, err
	}
	if messageType == websocket.MessageBinary {
		return SpeechEvent{Type: "audio.delta", Audio: payload}, nil
	}
	var event SpeechEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return SpeechEvent{}, err
	}
	return event, nil
}

func (c *SpeechRealtimeClient) Close(status websocket.StatusCode, reason string) error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.Close(status, reason)
}

func (c *SpeechRealtimeClient) CloseNow() error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.CloseNow()
}

func (c *SpeechRealtimeClient) writeJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.write(ctx, websocket.MessageText, payload)
}

func (c *SpeechRealtimeClient) write(ctx context.Context, messageType websocket.MessageType, payload []byte) error {
	if c == nil || c.connection == nil {
		return errors.New("tma: speech realtime client is not connected")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.connection.Write(ctx, messageType, payload)
}
