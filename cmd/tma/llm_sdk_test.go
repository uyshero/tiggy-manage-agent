package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCommandProviderUpdateUsesCurrentRevision(t *testing.T) {
	calls := 0
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v2/llm-providers/provider%2F1" {
				t.Fatalf("unexpected provider lookup %s %s", r.Method, r.URL.EscapedPath())
			}
			return jsonResponse(`{"id":"provider/1","provider_type":"openai","revision":4}`), nil
		case 2:
			if r.Method != http.MethodPatch || r.URL.EscapedPath() != "/v2/llm-providers/provider%2F1" {
				t.Fatalf("unexpected provider update %s %s", r.Method, r.URL.EscapedPath())
			}
			if r.Header.Get("If-Match") != `"4"` {
				t.Fatalf("unexpected If-Match %q", r.Header.Get("If-Match"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["base_url"] != "https://llm.example.test" {
				t.Fatalf("unexpected update body %#v", body)
			}
			return jsonResponse(`{"id":"provider/1","provider_type":"openai","base_url":"https://llm.example.test","revision":5}`), nil
		default:
			t.Fatalf("unexpected extra request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})

	captureStdout(t, func() {
		if err := commandProvider(client, []string{"update", "--id", "provider/1", "--base-url", "https://llm.example.test"}); err != nil {
			t.Fatalf("provider update: %v", err)
		}
	})
	if calls != 2 {
		t.Fatalf("expected lookup and conditional update, got %d requests", calls)
	}
}

func TestCommandModelUpsertUsesConditionalCreateOrUpdate(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		calls := 0
		client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				if r.Method != http.MethodGet || r.URL.Path != "/v2/llm-models" || r.URL.Query().Get("provider_id") != "provider/1" {
					t.Fatalf("unexpected model list %s %s", r.Method, r.URL.String())
				}
				return jsonResponse(`{"models":[]}`), nil
			}
			if r.Method != http.MethodPost || r.URL.Path != "/v2/llm-models" || r.Header.Get("If-None-Match") != "*" {
				t.Fatalf("unexpected model create %s %s headers=%v", r.Method, r.URL.Path, r.Header)
			}
			return jsonResponse(`{"provider_id":"provider/1","model":"gpt-5","revision":1}`), nil
		})
		captureStdout(t, func() {
			if err := commandModel(client, []string{"upsert", "--provider", "provider/1", "--model", "gpt-5"}); err != nil {
				t.Fatalf("model create: %v", err)
			}
		})
		if calls != 2 {
			t.Fatalf("expected list and create, got %d requests", calls)
		}
	})

	t.Run("update", func(t *testing.T) {
		calls := 0
		client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return jsonResponse(`{"models":[{"provider_id":"provider/1","model":"gpt-5","context_window_tokens":128000,"capability_type":"text","revision":9}]}`), nil
			}
			if r.Header.Get("If-Match") != `"9"` || r.Header.Get("If-None-Match") != "" {
				t.Fatalf("unexpected conditional headers %v", r.Header)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["context_window_tokens"] != float64(128000) || body["capability_type"] != "text" {
				t.Fatalf("existing model fields were not preserved: %#v", body)
			}
			return jsonResponse(`{"provider_id":"provider/1","model":"gpt-5","context_window_tokens":128000,"revision":10}`), nil
		})
		captureStdout(t, func() {
			if err := commandModel(client, []string{"upsert", "--provider", "provider/1", "--model", "gpt-5"}); err != nil {
				t.Fatalf("model update: %v", err)
			}
		})
		if calls != 2 {
			t.Fatalf("expected list and update, got %d requests", calls)
		}
	})
}

func TestCommandModelUpsertRegistersSpeechCapability(t *testing.T) {
	calls := 0
	client := newTestAPIClient(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonResponse(`{"models":[]}`), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		capabilities, _ := body["capabilities"].(map[string]any)
		if body["capability_type"] != "text_to_speech" || capabilities["protocol"] != "doubao_bidirectional_tts" || capabilities["resource_id"] != "seed-tts-2.0" || capabilities["default_voice"] != "warm" || capabilities["sample_rate_hz"] != float64(24000) {
			t.Fatalf("unexpected speech model body: %#v", body)
		}
		return jsonResponse(`{"provider_id":"doubao-tts","model":"seed-tts-2.0","capability_type":"text_to_speech","revision":1}`), nil
	})
	captureStdout(t, func() {
		err := commandModel(client, []string{
			"upsert", "--provider", "doubao-tts", "--model", "seed-tts-2.0",
			"--capability", "text_to_speech", "--protocol", "doubao_bidirectional_tts",
			"--resource-id", "seed-tts-2.0", "--default-voice", "warm", "--sample-rate", "24000",
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	if calls != 2 {
		t.Fatalf("expected list and create, got %d requests", calls)
	}
}
