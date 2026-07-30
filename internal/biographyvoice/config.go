package biographyvoice

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
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
	AuthMode                              string
	AuthOIDCIssuer                        string
	AuthOIDCAudience                      string
	AuthOIDCJWKSURL                       string
	AuthOIDCClientID                      string
	AuthOIDCScopes                        []string
	AuthOIDCHTTPTimeout                   time.Duration
	DataDir                               string
	DatabaseURL                           string
	ObjectStore                           objectstore.Config
	RecordingMaxBytes                     int64
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
	authOIDCHTTPTimeout, err := durationFromEnvironment(lookup, "TMA_BIOGRAPHY_AUTH_OIDC_HTTP_TIMEOUT", 10*time.Second)
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
	recordingMaxBytes, err := int64FromEnvironment(lookup, "TMA_BIOGRAPHY_RECORDING_MAX_BYTES", 128*1024*1024)
	if err != nil {
		return Config{}, err
	}
	objectStoreAccessKeyEnv := valueOrDefault(
		lookup("TMA_BIOGRAPHY_OBJECT_STORE_ACCESS_KEY_ENV"),
		valueOrDefault(lookup("TMA_OBJECT_STORE_ACCESS_KEY_ENV"), "TMA_OBJECT_STORE_ACCESS_KEY"),
	)
	objectStoreSecretKeyEnv := valueOrDefault(
		lookup("TMA_BIOGRAPHY_OBJECT_STORE_SECRET_KEY_ENV"),
		valueOrDefault(lookup("TMA_OBJECT_STORE_SECRET_KEY_ENV"), "TMA_OBJECT_STORE_SECRET_KEY"),
	)
	config := Config{
		HTTPAddr:            valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_HTTP_ADDR"), ":8091"),
		Provider:            strings.ToLower(valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_PROVIDER"), ProviderMock)),
		ClientToken:         strings.TrimSpace(lookup("TMA_BIOGRAPHY_VOICE_CLIENT_TOKEN")),
		AllowedOrigins:      splitList(valueOrDefault(lookup("TMA_BIOGRAPHY_VOICE_ALLOWED_ORIGINS"), "localhost:*,127.0.0.1:*")),
		AuthMode:            strings.ToLower(valueOrDefault(lookup("TMA_BIOGRAPHY_AUTH_MODE"), biographyAuthModeDisabled)),
		AuthOIDCIssuer:      strings.TrimSpace(lookup("TMA_BIOGRAPHY_AUTH_OIDC_ISSUER")),
		AuthOIDCAudience:    strings.TrimSpace(lookup("TMA_BIOGRAPHY_AUTH_OIDC_AUDIENCE")),
		AuthOIDCJWKSURL:     strings.TrimSpace(lookup("TMA_BIOGRAPHY_AUTH_OIDC_JWKS_URL")),
		AuthOIDCClientID:    strings.TrimSpace(lookup("TMA_BIOGRAPHY_AUTH_OIDC_CLIENT_ID")),
		AuthOIDCScopes:      splitList(valueOrDefault(lookup("TMA_BIOGRAPHY_AUTH_OIDC_SCOPES"), "openid,profile,email")),
		AuthOIDCHTTPTimeout: authOIDCHTTPTimeout,
		DataDir:             valueOrDefault(lookup("TMA_BIOGRAPHY_DATA_DIR"), ".tma-biography"),
		DatabaseURL:         strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_DATABASE_URL"), lookup("TMA_DATABASE_URL"))),
		ObjectStore: objectstore.Config{
			Provider:     valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_PROVIDER"), lookup("TMA_OBJECT_STORE_PROVIDER")),
			Endpoint:     strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_ENDPOINT"), lookup("TMA_OBJECT_STORE_ENDPOINT"))),
			Region:       strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_REGION"), lookup("TMA_OBJECT_STORE_REGION"))),
			Bucket:       strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_BUCKET"), lookup("TMA_OBJECT_STORE_BUCKET"))),
			RootDir:      strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_ROOT_DIR"), lookup("TMA_OBJECT_STORE_ROOT_DIR"))),
			AccessKey:    strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_ACCESS_KEY"), lookup(objectStoreAccessKeyEnv))),
			SecretKey:    strings.TrimSpace(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_SECRET_KEY"), lookup(objectStoreSecretKeyEnv))),
			UsePathStyle: strings.EqualFold(valueOrDefault(lookup("TMA_BIOGRAPHY_OBJECT_STORE_USE_PATH_STYLE"), lookup("TMA_OBJECT_STORE_USE_PATH_STYLE")), "true"),
		},
		RecordingMaxBytes:                     recordingMaxBytes,
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
	switch strings.TrimSpace(config.AuthMode) {
	case "", biographyAuthModeDisabled:
	case biographyAuthModeOIDC:
		if strings.TrimSpace(config.AuthOIDCIssuer) == "" || strings.TrimSpace(config.AuthOIDCAudience) == "" {
			return fmt.Errorf("biography OIDC auth requires issuer and audience")
		}
		if config.AuthOIDCHTTPTimeout <= 0 {
			return fmt.Errorf("biography OIDC HTTP timeout must be positive")
		}
		if strings.TrimSpace(config.DataDir) == "" {
			return fmt.Errorf("biography data directory is required when auth is enabled")
		}
		if config.RecordingMaxBytes < 0 {
			return fmt.Errorf("biography recording max bytes cannot be negative")
		}
		if strings.TrimSpace(config.DatabaseURL) != "" {
			if strings.TrimSpace(config.ObjectStore.Provider) == "" || strings.TrimSpace(config.ObjectStore.Bucket) == "" {
				return fmt.Errorf("biography Postgres persistence requires object storage provider and bucket")
			}
		}
	default:
		return fmt.Errorf("unsupported biography auth mode %q", config.AuthMode)
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

func int64FromEnvironment(lookup func(string) string, key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(lookup(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
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
