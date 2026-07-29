package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/internal/biographyvoice"
	"tiggy-manage-agent/internal/serverconfig"
	"tiggy-manage-agent/sdk/tma"
)

func main() {
	if err := serverconfig.LoadDotEnv(".env"); err != nil {
		fail(err)
	}
	tokenEnv := envOrDefault("TMA_BIOGRAPHY_TMA_TOKEN_ENV", "TMA_AUTH_TOKEN")
	options := make([]tma.Option, 0, 1)
	if token := strings.TrimSpace(os.Getenv(tokenEnv)); token != "" {
		options = append(options, tma.WithBearerToken(token))
	}
	client, err := tma.NewClient(envOrDefault("TMA_BIOGRAPHY_TMA_BASE_URL", "http://127.0.0.1:8080"), options...)
	if err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workspaceID := strings.TrimSpace(os.Getenv("TMA_BIOGRAPHY_TMA_WORKSPACE_ID"))
	environmentID, err := biographyvoice.ResolveBiographyEnvironment(
		ctx, client.Environments, os.Getenv("TMA_BIOGRAPHY_TMA_ENVIRONMENT_ID"), workspaceID,
	)
	if err != nil {
		fail(err)
	}
	skillConfig, skillStatuses, err := biographyvoice.EnsureBiographySkills(ctx, client.Skills, workspaceID)
	if err != nil {
		fail(err)
	}
	interviewerSkills := biographyvoice.SelectBiographySkills(skillConfig, "conduct-biography-interview", "verify-biography-facts")
	organizerSkills := biographyvoice.SelectBiographySkills(skillConfig, "structure-biography-chapters", "verify-biography-facts")
	defaultModel := strings.TrimSpace(os.Getenv("TMA_LLM_MODEL"))
	noTools := json.RawMessage(`{"disable_platform_defaults":true}`)
	interviewer, interviewerCreated, err := biographyvoice.EnsureBiographyAgent(ctx, client.Agents, biographyvoice.BiographyAgentBootstrapConfig{
		Name:          envOrDefault("TMA_BIOGRAPHY_TMA_AGENT_NAME", "自传采访者"),
		WorkspaceID:   workspaceID,
		EnvironmentID: environmentID,
		LLMProvider:   strings.TrimSpace(os.Getenv("TMA_LLM_PROVIDER")),
		LLMModel:      envOrDefault("TMA_BIOGRAPHY_TMA_INTERVIEW_MODEL", defaultModel),
		Tools:         noTools,
		Skills:        interviewerSkills,
	})
	if err != nil {
		fail(err)
	}
	organizer, organizerCreated, err := biographyvoice.EnsureBiographyAgent(ctx, client.Agents, biographyvoice.BiographyAgentBootstrapConfig{
		Name:          envOrDefault("TMA_BIOGRAPHY_TMA_ORGANIZER_AGENT_NAME", "自传章节整理者"),
		System:        biographyvoice.BiographyOrganizerSystemPrompt,
		WorkspaceID:   workspaceID,
		EnvironmentID: environmentID,
		LLMProvider:   strings.TrimSpace(os.Getenv("TMA_LLM_PROVIDER")),
		LLMModel:      envOrDefault("TMA_BIOGRAPHY_TMA_ORGANIZER_MODEL", defaultModel),
		Tools:         noTools,
		Skills:        organizerSkills,
	})
	if err != nil {
		fail(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"interviewer_agent_id": interviewer.ID, "interviewer_agent_name": interviewer.Name, "interviewer_created": interviewerCreated,
		"organizer_agent_id": organizer.ID, "organizer_agent_name": organizer.Name, "organizer_created": organizerCreated,
		"environment_id": environmentID, "skills": skillStatuses,
		"next_config": "TMA_BIOGRAPHY_TMA_AGENT_ID=" + interviewer.ID + "\nTMA_BIOGRAPHY_TMA_ORGANIZER_AGENT_ID=" + organizer.ID + "\nTMA_BIOGRAPHY_TMA_ENVIRONMENT_ID=" + environmentID,
	})
}

func envOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tma-biography-agent-bootstrap:", err)
	os.Exit(1)
}
