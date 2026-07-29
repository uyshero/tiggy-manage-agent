package biographyvoice

import (
	"strings"
	"testing"
	"time"
)

func TestConfigDefaultsToMockWithoutCredentials(t *testing.T) {
	config, err := ConfigFromEnvironment(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if config.Provider != ProviderMock || config.HTTPAddr != ":8091" {
		t.Fatalf("unexpected defaults: %+v", config)
	}
	if config.DoubaoTTSResourceID != "seed-tts-2.0" || config.DoubaoASRResourceID != "volc.seedasr.sauc.duration" ||
		config.DoubaoASRURL != defaultDoubaoASRURL || config.DoubaoTTSSpeaker != defaultDoubaoTTSSpeaker {
		t.Fatalf("unexpected Doubao resource defaults: %+v", config)
	}
	if config.DoubaoTTSModel != "" {
		t.Fatalf("unexpected Doubao TTS model default: %q", config.DoubaoTTSModel)
	}
}

func TestDoubaoConfigFailsClosedWithoutSecret(t *testing.T) {
	values := map[string]string{"TMA_BIOGRAPHY_VOICE_PROVIDER": ProviderDoubao}
	_, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing API key error, got %v", err)
	}

	values["TMA_LLM_API_KEY"] = "secret"
	config, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("expected official Doubao defaults to validate, got %v", err)
	}
	if config.DoubaoASRURL != defaultDoubaoASRURL || config.DoubaoTTSSpeaker != defaultDoubaoTTSSpeaker {
		t.Fatalf("unexpected official defaults: %+v", config)
	}
}

func TestDoubaoConfigReusesTMAProviderSecretReference(t *testing.T) {
	values := map[string]string{
		"TMA_BIOGRAPHY_VOICE_PROVIDER":           ProviderDoubao,
		"TMA_LLM_API_KEY_ENV":                    "TMA_LLM_API_KEY_DOUBAO",
		"TMA_LLM_API_KEY_DOUBAO":                 "shared-secret",
		"TMA_BIOGRAPHY_VOICE_DOUBAO_ASR_URL":     "wss://speech.example/asr",
		"TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_SPEAKER": "zh_female_example",
	}
	config, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.DoubaoAPIKey != "shared-secret" {
		t.Fatal("TMA provider API key environment reference was not reused")
	}
}

func TestDoubaoConfigReadsSecretByEnvironmentReference(t *testing.T) {
	values := map[string]string{
		"TMA_BIOGRAPHY_VOICE_PROVIDER":           ProviderDoubao,
		"TMA_BIOGRAPHY_VOICE_DOUBAO_API_KEY_ENV": "CUSTOM_DOUBAO_KEY",
		"CUSTOM_DOUBAO_KEY":                      "secret-value",
		"TMA_BIOGRAPHY_VOICE_DOUBAO_ASR_URL":     "wss://speech.example/asr",
		"TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_SPEAKER": "zh_female_example",
	}
	config, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.DoubaoAPIKey != "secret-value" {
		t.Fatal("configured API key environment reference was not resolved")
	}
}

func TestTMAInterviewConfigRequiresAgentAndReadsAuthTokenReference(t *testing.T) {
	values := map[string]string{
		"TMA_BIOGRAPHY_INTERVIEW_PROVIDER": ProviderTMA,
		"TMA_BIOGRAPHY_TMA_TOKEN_ENV":      "CUSTOM_TMA_TOKEN",
		"CUSTOM_TMA_TOKEN":                 "tma-secret",
	}
	_, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "agent ID") {
		t.Fatalf("expected missing agent ID error, got %v", err)
	}
	values["TMA_BIOGRAPHY_TMA_AGENT_ID"] = "agent-biography"
	_, err = ConfigFromEnvironment(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "resume signing key") {
		t.Fatalf("expected missing resume signing key error, got %v", err)
	}
	values["TMA_BIOGRAPHY_RESUME_SIGNING_KEY"] = "0123456789abcdef0123456789abcdef"
	config, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.TMAAuthToken != "tma-secret" || config.TMABaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected TMA interview config: %+v", config)
	}
	if config.TMAOrganizerAgentID != "agent-biography" {
		t.Fatalf("organizer agent should fall back to interviewer for old configurations: %+v", config)
	}
	if config.TMAInterviewThinking != "disabled" {
		t.Fatalf("real-time interview thinking should default to disabled: %+v", config)
	}
	if config.TMAInterviewCompactionThresholdTokens != 8000 || config.TMAInterviewCompactionSummaryMaxChars != 4000 {
		t.Fatalf("unexpected real-time interview compaction defaults: %+v", config)
	}
	if config.InterviewFirstResponseTimeout != 6*time.Second || config.InterviewTimeout != 45*time.Second {
		t.Fatalf("unexpected real-time interview timeouts: %+v", config)
	}

	values["TMA_BIOGRAPHY_TMA_ORGANIZER_AGENT_ID"] = "agent-organizer"
	config, err = ConfigFromEnvironment(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.TMAOrganizerAgentID != "agent-organizer" {
		t.Fatalf("dedicated organizer agent was not read: %+v", config)
	}
}

func TestTMAInterviewConfigRejectsUnsupportedThinkingMode(t *testing.T) {
	values := map[string]string{
		"TMA_BIOGRAPHY_INTERVIEW_PROVIDER":     ProviderTMA,
		"TMA_BIOGRAPHY_TMA_AGENT_ID":           "agent-biography",
		"TMA_BIOGRAPHY_RESUME_SIGNING_KEY":     "0123456789abcdef0123456789abcdef",
		"TMA_BIOGRAPHY_TMA_INTERVIEW_THINKING": "deep",
	}
	_, err := ConfigFromEnvironment(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "thinking") {
		t.Fatalf("expected invalid interview thinking mode error, got %v", err)
	}
}
