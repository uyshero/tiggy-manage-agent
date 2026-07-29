package biographyvoice

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderMock   = "mock"
	ProviderDoubao = "doubao"
	ProviderTMA    = "tma"

	defaultDoubaoASRURL     = "wss://openspeech.bytedance.com/api/v3/plan/sauc/bigmodel_async"
	defaultDoubaoTTSSpeaker = "zh_female_kefunvsheng_uranus_bigtts"
)

type Config struct {
	HTTPAddr                              string
	Provider                              string
	ClientToken                           string
	AllowedOrigins                        []string
	AuthEnabled                           bool
	AuthSigningKey                        string
	AuthTokenTTL                          time.Duration
	AuthCodeTTL                           time.Duration
	AuthDevCode                           string
	AuthExposeDevCode                     bool
	DataDir                               string
	DoubaoAPIKey                          string
	DoubaoASRURL                          string
	DoubaoASRResourceID                   string
	DoubaoTTSURL                          string
	DoubaoTTSResourceID                   string
	DoubaoTTSModel                        string
	DoubaoTTSSpeaker                      string
	InterviewProvider                     string
	TMABaseURL                            string
	TMAAuthToken                          string
	TMAAgentID                            string
	TMAOrganizerAgentID                   string
	TMAEnvironmentID                      string
	TMAWorkspaceID                        string
	TMAOwnerID                            string
	TMAInterviewThinking                  string
	TMAInterviewCompactionThresholdTokens int
	TMAInterviewCompactionSummaryMaxChars int
	InterviewFirstResponseTimeout         time.Duration
	InterviewTimeout                      time.Duration
	ResumeSigningKey                      string
	ResumeTTL                             time.Duration
}

func ConfigFromEnvironment(lookup func(string) string) (Config, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	sharedAPIKeyEnv := valueOrDefault(lookup("TMA_LLM_API_KEY_ENV"), "TMA_LLM_API_KEY")
	voiceAPIKeyEnv := valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_API_KEY_ENV"), sharedAPIKeyEnv)
	tmaTokenEnv := valueOrDefault(lookup("TMA_BIOGRAPHY_TMA_TOKEN_ENV"), "TMA_AUTH_TOKEN")
	interviewerAgentID := strings.TrimSpace(lookup("TMA_BIOGRAPHY_TMA_AGENT_ID"))
	firstResponseTimeout, err := durationFromEnvironment(lookup, "TMA_BIOGRAPHY_INTERVIEW_FIRST_RESPONSE_TIMEOUT", 6*time.Second)
	if err != nil {
		return Config{}, err
	}
	interviewTimeout, err := durationFromEnvironment(lookup, "TMA_BIOGRAPHY_INTERVIEW_TIMEOUT", 45*time.Second)
	if err != nil {
		return Config{}, err
	}
	authTokenTTL, err := durationFromEnvironment(lookup, "TMA_BIOGRAPHY_AUTH_TOKEN_TTL", 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	authCodeTTL, err := durationFromEnvironment(lookup, "TMA_BIOGRAPHY_AUTH_CODE_TTL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	compactionThreshold, err := intFromEnvironment(lookup, "TMA_BIOGRAPHY_TMA_INTERVIEW_COMPACTION_THRESHOLD_TOKENS", 8000)
	if err != nil {
		return Config{}, err
	}
	compactionSummaryMaxChars, err := intFromEnvironment(lookup, "TMA_BIOGRAPHY_TMA_INTERVIEW_COMPACTION_SUMMARY_MAX_CHARS", 4000)
	if err != nil {
		return Config{}, err
	}
	config := Config{
		HTTPAddr:                              valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_HTTP_ADDR"), ":8091"),
		Provider:                              strings.ToLower(valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_PROVIDER"), ProviderMock)),
		ClientToken:                           strings.TrimSpace(lookup("TMA_BIOGRAPHY_VOICE_CLIENT_TOKEN")),
		AllowedOrigins:                        splitList(valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_ALLOWED_ORIGINS"), "localhost:*,127.0.0.1:*")),
		AuthEnabled:                           boolFromEnvironment(lookup, "TMA_BIOGRAPHY_AUTH_ENABLED"),
		AuthSigningKey:                        strings.TrimSpace(lookup("TMA_BIOGRAPHY_AUTH_SIGNING_KEY")),
		AuthTokenTTL:                          authTokenTTL,
		AuthCodeTTL:                           authCodeTTL,
		AuthDevCode:                           strings.TrimSpace(lookup("TMA_BIOGRAPHY_AUTH_DEV_CODE")),
		AuthExposeDevCode:                     boolFromEnvironment(lookup, "TMA_BIOGRAPHY_AUTH_EXPOSE_DEV_CODE"),
		DataDir:                               valueOrDefault(lookup("TMA_BIOGRAPHY_DATA_DIR"), ".tma-biography"),
		DoubaoAPIKey:                          strings.TrimSpace(lookup(voiceAPIKeyEnv)),
		DoubaoASRURL:                          valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_ASR_URL"), defaultDoubaoASRURL),
		DoubaoASRResourceID:                   valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_ASR_RESOURCE_ID"), "volc.seedasr.sauc.duration"),
		DoubaoTTSURL:                          valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_URL"), "wss://openspeech.bytedance.com/api/v3/plan/tts/bidirection"),
		DoubaoTTSResourceID:                   valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_RESOURCE_ID"), "seed-tts-2.0"),
		DoubaoTTSModel:                        strings.TrimSpace(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_MODEL")),
		DoubaoTTSSpeaker:                      valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_DOUBAO_TTS_SPEAKER"), defaultDoubaoTTSSpeaker),
		InterviewProvider:                     strings.ToLower(valueOrDefault(lookup("TMA_BIOGRAPHY_INTERVIEW_PROVIDER"), ProviderMock)),
		TMABaseURL:                            valueOrDefault(lookup("TMA_BIOGRAPHY_TMA_BASE_URL"), "http://127.0.0.1:8080"),
		TMAAuthToken:                          strings.TrimSpace(lookup(tmaTokenEnv)),
		TMAAgentID:                            interviewerAgentID,
		TMAOrganizerAgentID:                   valueOrDefault(lookup("TMA_BIOGRAPHY_TMA_ORGANIZER_AGENT_ID"), interviewerAgentID),
		TMAEnvironmentID:                      strings.TrimSpace(lookup("TMA_BIOGRAPHY_TMA_ENVIRONMENT_ID")),
		TMAWorkspaceID:                        strings.TrimSpace(lookup("TMA_BIOGRAPHY_TMA_WORKSPACE_ID")),
		TMAOwnerID:                            strings.TrimSpace(lookup("TMA_BIOGRAPHY_TMA_OWNER_ID")),
		TMAInterviewThinking:                  strings.ToLower(valueOrDefault(lookup("TMA_BIOGRAPHY_TMA_INTERVIEW_THINKING"), "disabled")),
		TMAInterviewCompactionThresholdTokens: compactionThreshold,
		TMAInterviewCompactionSummaryMaxChars: compactionSummaryMaxChars,
		InterviewFirstResponseTimeout:         firstResponseTimeout,
		InterviewTimeout:                      interviewTimeout,
		ResumeSigningKey:                      lookup("TMA_BIOGRAPHY_RESUME_SIGNING_KEY"),
		ResumeTTL:                             30 * 24 * time.Hour,
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if strings.TrimSpace(config.HTTPAddr) == "" {
		return fmt.Errorf("biography voice HTTP address is required")
	}
	if len(config.AllowedOrigins) == 0 {
		return fmt.Errorf("biography voice allowed origins are required")
	}
	if config.AuthEnabled {
		if len(config.AuthSigningKey) < 32 {
			return fmt.Errorf("biography auth signing key must be at least 32 bytes")
		}
		if config.AuthTokenTTL <= 0 || config.AuthCodeTTL <= 0 {
			return fmt.Errorf("biography auth TTLs must be positive")
		}
		if strings.TrimSpace(config.DataDir) == "" {
			return fmt.Errorf("biography data directory is required when auth is enabled")
		}
	}
	switch config.Provider {
	case ProviderMock:
	case ProviderDoubao:
		if config.DoubaoAPIKey == "" {
			return fmt.Errorf("doubao speech API key is required")
		}
		if config.DoubaoASRURL == "" {
			return fmt.Errorf("doubao ASR URL is required for the selected Plan")
		}
		if config.DoubaoASRResourceID == "" || config.DoubaoTTSResourceID == "" {
			return fmt.Errorf("doubao ASR and TTS resource IDs are required")
		}
		if config.DoubaoTTSSpeaker == "" {
			return fmt.Errorf("doubao TTS speaker is required")
		}
		if !strings.HasPrefix(config.DoubaoASRURL, "wss://") || !strings.HasPrefix(config.DoubaoTTSURL, "wss://") {
			return fmt.Errorf("doubao ASR and TTS URLs must use wss")
		}
	default:
		return fmt.Errorf("unsupported biography voice provider %q", config.Provider)
	}
	switch valueOrDefault(config.InterviewProvider, ProviderMock) {
	case ProviderMock:
		return nil
	case ProviderTMA:
		if config.TMAInterviewThinking != "enabled" && config.TMAInterviewThinking != "disabled" {
			return fmt.Errorf("TMA biography interview thinking must be enabled or disabled")
		}
		parsed, err := url.Parse(strings.TrimSpace(config.TMABaseURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("biography TMA base URL must be an absolute HTTP URL")
		}
		if config.TMAAgentID == "" {
			return fmt.Errorf("biography TMA agent ID is required")
		}
		if config.InterviewTimeout <= 0 {
			return fmt.Errorf("biography interview timeout must be positive")
		}
		if config.InterviewFirstResponseTimeout <= 0 || config.InterviewFirstResponseTimeout >= config.InterviewTimeout {
			return fmt.Errorf("biography interview first response timeout must be positive and shorter than the total timeout")
		}
		if config.TMAInterviewCompactionThresholdTokens <= 0 || config.TMAInterviewCompactionSummaryMaxChars <= 0 {
			return fmt.Errorf("biography interview compaction settings must be positive")
		}
		if len(config.ResumeSigningKey) < 32 {
			return fmt.Errorf("biography resume signing key must be at least 32 bytes")
		}
		if config.ResumeTTL <= 0 {
			return fmt.Errorf("biography resume TTL must be positive")
		}
		return nil
	default:
		return fmt.Errorf("unsupported biography interview provider %q", config.InterviewProvider)
	}
}

func durationFromEnvironment(lookup func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(lookup(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return value, nil
}

func intFromEnvironment(lookup func(string) string, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(lookup(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func boolFromEnvironment(lookup func(string) string, key string) bool {
	raw := strings.TrimSpace(strings.ToLower(lookup(key)))
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func valueOrDefault(value string, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func splitList(value string) []string {
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
