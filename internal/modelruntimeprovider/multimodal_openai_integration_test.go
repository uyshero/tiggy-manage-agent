package modelruntimeprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestOpenAIRealtimeLiveSmoke(t *testing.T) {
	if os.Getenv("TMA_RUN_OPENAI_REALTIME_TESTS") != "1" {
		t.Skip("set TMA_RUN_OPENAI_REALTIME_TESTS=1 to run the live OpenAI Realtime smoke test")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Fatal("OPENAI_API_KEY is required when TMA_RUN_OPENAI_REALTIME_TESTS=1")
	}
	model := strings.TrimSpace(os.Getenv("TMA_OPENAI_REALTIME_MODEL"))
	if model == "" {
		model = "gpt-realtime"
	}
	baseURL := strings.TrimSpace(os.Getenv("TMA_OPENAI_REALTIME_URL"))
	if baseURL == "" {
		baseURL = "wss://api.openai.com/v1/realtime"
	}

	request := testOpenAIRealtimeRequest()
	request.Route.BaseURL = baseURL
	request.Route.APIKey = apiKey
	request.Route.UpstreamModel = model
	request.Route.Constraints.InputFormats = request.Route.Constraints.InputFormats[:1]
	request.Route.Constraints.OutputModalities = []string{"text"}
	request.Route.Constraints.OutputFormats = nil
	request.Route.Constraints.MaxInputTracks = 1
	request.Start.InputTracks = request.Start.InputTracks[:1]
	request.Start.OutputModalities = []string{"text"}
	request.Start.OutputFlowLimits = nil
	request.Start.InitialOutputCredit = nil
	request.Start.BackpressureTimeoutMS = 5000

	testContext, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result := make(chan struct {
		metrics MultimodalMetrics
		err     error
	}, 1)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{MultimodalRealtimeSubprotocol}})
		if err != nil {
			result <- struct {
				metrics MultimodalMetrics
				err     error
			}{err: err}
			return
		}
		defer connection.CloseNow()
		metrics, proxyErr := ProxyMultimodalWithDialer(testContext, r.Context(), connection, request, nil)
		result <- struct {
			metrics MultimodalMetrics
			err     error
		}{metrics: metrics, err: proxyErr}
	}))
	defer platform.Close()
	client, _, err := websocket.Dial(testContext, "ws"+strings.TrimPrefix(platform.URL, "http"), &websocket.DialOptions{
		Subprotocols: []string{MultimodalRealtimeSubprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	var started MultimodalSessionStarted
	if err := readMultimodalTestJSON(testContext, client, &started); err != nil {
		t.Fatal(err)
	}
	if started.Type != "session.started" {
		t.Fatalf("unexpected live session start: %+v", started)
	}
	if err := writeMultimodalTestJSON(testContext, client, MultimodalEvent{Type: "input.text.append", Text: "Reply with exactly TMA_OK."}); err != nil {
		t.Fatal(err)
	}
	if err := writeMultimodalTestJSON(testContext, client, MultimodalEvent{Type: "input.commit"}); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for {
		var event MultimodalEvent
		if err := readMultimodalTestJSON(testContext, client, &event); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "output.text.delta", "output.text.final":
			output.WriteString(event.Text)
		case "session.completed":
			if !strings.Contains(output.String(), "TMA_OK") {
				t.Fatalf("unexpected live OpenAI response %q", output.String())
			}
			select {
			case proxyResult := <-result:
				if proxyResult.err != nil || !proxyResult.metrics.Completed {
					t.Fatalf("live OpenAI adapter result metrics=%+v err=%v", proxyResult.metrics, proxyResult.err)
				}
			case <-testContext.Done():
				t.Fatal(testContext.Err())
			}
			return
		case "error":
			t.Fatalf("live OpenAI adapter error code=%s message=%s", event.Code, event.Message)
		}
	}
}
