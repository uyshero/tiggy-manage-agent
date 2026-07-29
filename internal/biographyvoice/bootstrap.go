package biographyvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"tiggy-manage-agent/sdk/tma"
	biographyskills "tiggy-manage-agent/skills"
)

type AgentAdmin interface {
	List(context.Context) ([]tma.Agent, error)
	Create(context.Context, tma.CreateAgentRequest) (tma.Agent, error)
	Update(context.Context, string, tma.UpdateAgentRequest) (tma.Agent, error)
}

type EnvironmentAdmin interface {
	List(context.Context) ([]tma.Environment, error)
}

type SkillAdmin interface {
	List(context.Context, tma.SkillListQuery) ([]tma.Skill, error)
	Create(context.Context, tma.CreateSkillRequest) (tma.Skill, error)
	ListVersions(context.Context, string) ([]tma.SkillVersion, error)
	CreateVersion(context.Context, string, tma.CreateSkillVersionRequest) (tma.SkillVersion, error)
}

type BiographySkillStatus struct {
	ID               string `json:"id"`
	Identifier       string `json:"identifier"`
	Version          int32  `json:"version"`
	Created          bool   `json:"created"`
	PublishedVersion bool   `json:"published_version"`
}

type BiographyAgentBootstrapConfig struct {
	Name          string
	System        string
	WorkspaceID   string
	EnvironmentID string
	LLMProvider   string
	LLMModel      string
	Tools         json.RawMessage
	Skills        tma.SkillConfig
}

func EnsureBiographySkills(ctx context.Context, admin SkillAdmin, workspaceID string) (tma.SkillConfig, []BiographySkillStatus, error) {
	if admin == nil {
		return tma.SkillConfig{}, nil, fmt.Errorf("TMA skill administration client is required")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	existing, err := admin.List(ctx, tma.SkillListQuery{WorkspaceID: workspaceID})
	if err != nil {
		return tma.SkillConfig{}, nil, fmt.Errorf("list TMA biography skills: %w", err)
	}

	definitions := biographyskills.BiographyDefinitions()
	config := tma.SkillConfig{Enabled: make([]tma.EnabledSkill, 0, len(definitions))}
	statuses := make([]BiographySkillStatus, 0, len(definitions))
	for _, definition := range definitions {
		skill, created, ensureErr := ensureBiographySkill(ctx, admin, existing, workspaceID, definition)
		if ensureErr != nil {
			return tma.SkillConfig{}, nil, ensureErr
		}
		version, published, versionErr := ensureBiographySkillVersion(ctx, admin, skill, definition)
		if versionErr != nil {
			return tma.SkillConfig{}, nil, versionErr
		}
		config.Enabled = append(config.Enabled, tma.EnabledSkill{
			SkillID: skill.ID, Skill: skill.Identifier, Version: version.Version, Mode: "full", Priority: definition.Priority,
		})
		statuses = append(statuses, BiographySkillStatus{
			ID: skill.ID, Identifier: skill.Identifier, Version: version.Version, Created: created, PublishedVersion: published,
		})
	}
	return config, statuses, nil
}

func SelectBiographySkills(config tma.SkillConfig, identifiers ...string) tma.SkillConfig {
	selected := make(map[string]struct{}, len(identifiers))
	for _, identifier := range identifiers {
		selected[identifier] = struct{}{}
	}
	result := tma.SkillConfig{Enabled: make([]tma.EnabledSkill, 0, len(identifiers))}
	for _, skill := range config.Enabled {
		if _, ok := selected[skill.Skill]; ok {
			result.Enabled = append(result.Enabled, skill)
		}
	}
	return result
}

func ensureBiographySkill(ctx context.Context, admin SkillAdmin, existing []tma.Skill, workspaceID string, definition biographyskills.Definition) (tma.Skill, bool, error) {
	for _, skill := range existing {
		if skill.ArchivedAt == nil && skill.Identifier == definition.Identifier {
			return skill, false, nil
		}
	}
	request := tma.CreateSkillRequest{
		WorkspaceID: workspaceID, Identifier: definition.Identifier, Title: definition.Title,
		Description: definition.Description, SourcePath: "skills/" + definition.Identifier,
	}
	if workspaceID != "" {
		request.OwnerType = "workspace"
		request.OwnerID = workspaceID
		request.Visibility = "workspace"
	}
	skill, err := admin.Create(ctx, request)
	if err != nil {
		return tma.Skill{}, false, fmt.Errorf("create TMA biography skill %s: %w", definition.Identifier, err)
	}
	return skill, true, nil
}

func ensureBiographySkillVersion(ctx context.Context, admin SkillAdmin, skill tma.Skill, definition biographyskills.Definition) (tma.SkillVersion, bool, error) {
	versions, err := admin.ListVersions(ctx, skill.ID)
	if err != nil {
		return tma.SkillVersion{}, false, fmt.Errorf("list versions for TMA biography skill %s: %w", definition.Identifier, err)
	}
	var latest tma.SkillVersion
	for _, version := range versions {
		if version.Version > latest.Version {
			latest = version
		}
	}
	if latest.Version > 0 && latest.ContentFormat == "markdown" && latest.ContentText == definition.Content {
		return latest, false, nil
	}
	created, err := admin.CreateVersion(ctx, skill.ID, tma.CreateSkillVersionRequest{
		ContentFormat: "markdown", Manifest: tma.SkillManifest{}, ContentText: definition.Content,
		SourceRef: "repository", SourceRevision: "embedded", SourceURL: "skills/" + definition.Identifier + "/SKILL.md",
	})
	if err != nil {
		return tma.SkillVersion{}, false, fmt.Errorf("publish TMA biography skill %s: %w", definition.Identifier, err)
	}
	return created, true, nil
}

func EnsureBiographyAgent(ctx context.Context, admin AgentAdmin, config BiographyAgentBootstrapConfig) (tma.Agent, bool, error) {
	if admin == nil {
		return tma.Agent{}, false, fmt.Errorf("TMA agent administration client is required")
	}
	config.Name = valueOrDefault(config.Name, "自传采访者")
	config.System = valueOrDefault(config.System, BiographyInterviewerSystemPrompt)
	config.WorkspaceID = strings.TrimSpace(config.WorkspaceID)
	config.EnvironmentID = strings.TrimSpace(config.EnvironmentID)
	config.LLMProvider = strings.TrimSpace(config.LLMProvider)
	config.LLMModel = strings.TrimSpace(config.LLMModel)
	if config.LLMProvider == "" || config.LLMModel == "" {
		return tma.Agent{}, false, fmt.Errorf("TMA LLM provider and model are required to bootstrap the biography agent")
	}

	agents, err := admin.List(ctx)
	if err != nil {
		return tma.Agent{}, false, fmt.Errorf("list TMA agents: %w", err)
	}
	for _, agent := range agents {
		if agent.ArchivedAt == nil && strings.TrimSpace(agent.Name) == config.Name &&
			(config.WorkspaceID == "" || agent.WorkspaceID == config.WorkspaceID) {
			update := tma.UpdateAgentRequest{}
			if config.EnvironmentID != "" && agent.EnvironmentID != config.EnvironmentID {
				update.EnvironmentID = &config.EnvironmentID
			}
			if agent.ConfigVersion.System != config.System {
				systemPrompt := config.System
				update.System = &systemPrompt
			}
			if agent.ConfigVersion.LLMProvider != config.LLMProvider {
				update.LLMProvider = &config.LLMProvider
			}
			if agent.ConfigVersion.LLMModel != config.LLMModel {
				update.LLMModel = &config.LLMModel
			}
			if len(config.Tools) > 0 && !biographyJSONEqual(agent.ConfigVersion.Tools, config.Tools) {
				tools := append(json.RawMessage(nil), config.Tools...)
				update.Tools = &tools
			}
			if len(config.Skills.Enabled) > 0 && !biographySkillConfigsEqual(agent.ConfigVersion.Skills, config.Skills) {
				encodedSkills, marshalErr := json.Marshal(config.Skills)
				if marshalErr != nil {
					return tma.Agent{}, false, fmt.Errorf("encode TMA biography skills: %w", marshalErr)
				}
				rawSkills := json.RawMessage(encodedSkills)
				update.Skills = &rawSkills
			}
			if update.EnvironmentID != nil || update.System != nil || update.LLMProvider != nil || update.LLMModel != nil || update.Tools != nil || update.Skills != nil {
				updated, updateErr := admin.Update(ctx, agent.ID, update)
				if updateErr != nil {
					return tma.Agent{}, false, fmt.Errorf("update TMA biography agent: %w", updateErr)
				}
				return updated, false, nil
			}
			return agent, false, nil
		}
	}

	request := tma.CreateAgentRequest{
		WorkspaceID: config.WorkspaceID, EnvironmentID: config.EnvironmentID,
		OwnerType: "workspace", OwnerID: config.WorkspaceID, Visibility: "workspace", AgentKind: "custom",
		Name: config.Name, LLMProvider: config.LLMProvider, LLMModel: config.LLMModel,
		System: config.System,
		Tools:  append(json.RawMessage(nil), config.Tools...),
	}
	if len(config.Skills.Enabled) > 0 {
		rawSkills, marshalErr := json.Marshal(config.Skills)
		if marshalErr != nil {
			return tma.Agent{}, false, fmt.Errorf("encode TMA biography skills: %w", marshalErr)
		}
		request.Skills = rawSkills
	}
	agent, err := admin.Create(ctx, request)
	if err != nil {
		return tma.Agent{}, false, fmt.Errorf("create TMA biography agent: %w", err)
	}
	return agent, true, nil
}

func biographyJSONEqual(current json.RawMessage, desired json.RawMessage) bool {
	var currentValue any
	var desiredValue any
	if json.Unmarshal(current, &currentValue) != nil || json.Unmarshal(desired, &desiredValue) != nil {
		return false
	}
	return reflect.DeepEqual(currentValue, desiredValue)
}

func biographySkillConfigsEqual(raw json.RawMessage, desired tma.SkillConfig) bool {
	if len(raw) == 0 {
		return len(desired.Enabled) == 0
	}
	var current tma.SkillConfig
	if err := json.Unmarshal(raw, &current); err != nil {
		return false
	}
	return reflect.DeepEqual(current, desired)
}

func ResolveBiographyEnvironment(ctx context.Context, admin EnvironmentAdmin, requestedID string, workspaceID string) (string, error) {
	if requestedID = strings.TrimSpace(requestedID); requestedID != "" {
		return requestedID, nil
	}
	if admin == nil {
		return "", fmt.Errorf("TMA environment administration client is required")
	}
	environments, err := admin.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list TMA environments: %w", err)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	candidates := make([]tma.Environment, 0, len(environments))
	for _, environment := range environments {
		if environment.ArchivedAt == nil && (workspaceID == "" || environment.WorkspaceID == workspaceID) {
			candidates = append(candidates, environment)
		}
	}
	for _, environment := range candidates {
		if strings.TrimSpace(environment.Name) == "通用 Sandbox" {
			return environment.ID, nil
		}
	}
	if len(candidates) == 1 {
		return candidates[0].ID, nil
	}
	return "", fmt.Errorf("TMA biography environment is required when the workspace does not have one unambiguous default")
}
