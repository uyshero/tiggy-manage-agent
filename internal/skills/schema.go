package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func ValidateIdentifier(value string) error {
	if !identifierPattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("skill identifier must match %s", identifierPattern.String())
	}
	return nil
}

func ValidateManifest(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("invalid skill manifest: %w", err)
	}
	for index, block := range manifest.Blocks {
		switch block.Type {
		case "instruction", "constraint", "checklist", "example":
		default:
			return fmt.Errorf("invalid skill manifest: blocks[%d].type is unsupported", index)
		}
	}
	if err := ValidateInputsSchema(manifest.InputsSchema); err != nil {
		return fmt.Errorf("invalid skill manifest: %w", err)
	}
	return nil
}

func ValidateConfig(raw json.RawMessage) (Config, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Config{Enabled: []EnabledSkill{}}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("invalid skills config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("invalid skills config: multiple JSON values")
		}
		return Config{}, fmt.Errorf("invalid skills config: %w", err)
	}
	if config.Enabled == nil {
		config.Enabled = []EnabledSkill{}
	}
	seen := make(map[string]struct{}, len(config.Enabled))
	for index := range config.Enabled {
		enabled := &config.Enabled[index]
		enabled.SkillID = strings.TrimSpace(enabled.SkillID)
		enabled.Skill = strings.TrimSpace(enabled.Skill)
		if !identifierPattern.MatchString(enabled.Skill) {
			return Config{}, fmt.Errorf("invalid skills config: enabled[%d].skill must be a lowercase identifier", index)
		}
		if enabled.Version <= 0 {
			return Config{}, fmt.Errorf("invalid skills config: enabled[%d].version must be greater than zero", index)
		}
		identity := enabled.SkillID
		if identity == "" {
			identity = enabled.Skill
		}
		if _, ok := seen[identity]; ok {
			return Config{}, fmt.Errorf("invalid skills config: duplicate skill %q", enabled.Skill)
		}
		seen[identity] = struct{}{}
		if enabled.Mode == "" {
			enabled.Mode = DefaultMode
		}
		if !validMode(enabled.Mode) {
			return Config{}, fmt.Errorf("invalid skills config: enabled[%d].mode is unsupported", index)
		}
		if enabled.Priority == 0 {
			enabled.Priority = DefaultPriority
		}
		if enabled.Priority < -1000 || enabled.Priority > 1000 {
			return Config{}, fmt.Errorf("invalid skills config: enabled[%d].priority must be between -1000 and 1000", index)
		}
		if len(enabled.Inputs) > 0 && string(enabled.Inputs) != "null" {
			var inputs map[string]any
			if err := json.Unmarshal(enabled.Inputs, &inputs); err != nil {
				return Config{}, fmt.Errorf("invalid skills config: enabled[%d].inputs must be an object", index)
			}
		}
	}
	return config, nil
}

func NormalizeConfig(raw json.RawMessage) (Config, bool) {
	if config, err := ValidateConfig(raw); err == nil {
		return config, true
	}
	if len(raw) == 0 || string(raw) == "null" || !json.Valid(raw) {
		return Config{}, false
	}
	if skill, ok := normalizeSkillName(rawString(raw)); ok {
		return Config{Enabled: []EnabledSkill{legacyEnabledSkill(skill)}}, true
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(raw, &rawItems); err == nil {
		return Config{Enabled: normalizeEnabledList(rawItems)}, true
	}
	var object struct {
		Enabled []json.RawMessage `json:"enabled"`
		Skills  []json.RawMessage `json:"skills"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		if object.Enabled != nil {
			return Config{Enabled: normalizeEnabledList(object.Enabled)}, true
		}
		if object.Skills != nil {
			return Config{Enabled: normalizeEnabledList(object.Skills)}, true
		}
	}
	if single, ok := normalizeEnabledSkill(raw); ok {
		return Config{Enabled: []EnabledSkill{single}}, true
	}
	return Config{}, false
}

func normalizeEnabledList(items []json.RawMessage) []EnabledSkill {
	enabled := make([]EnabledSkill, 0, len(items))
	for _, item := range items {
		if skill, ok := normalizeEnabledSkill(item); ok {
			enabled = append(enabled, skill)
		}
	}
	return enabled
}

func normalizeEnabledSkill(raw json.RawMessage) (EnabledSkill, bool) {
	if skill, ok := normalizeSkillName(rawString(raw)); ok {
		return legacyEnabledSkill(skill), true
	}
	var object struct {
		Skill      string           `json:"skill"`
		Name       string           `json:"name"`
		ID         string           `json:"id"`
		Identifier string           `json:"identifier"`
		Version    int              `json:"version"`
		Mode       string           `json:"mode"`
		Priority   *int             `json:"priority"`
		Inputs     *json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return EnabledSkill{}, false
	}
	skill, ok := normalizeSkillName(firstNonEmpty(object.Skill, object.Name, object.ID, object.Identifier))
	if !ok {
		return EnabledSkill{}, false
	}
	enabled := legacyEnabledSkill(skill)
	if object.Version > 0 {
		enabled.Version = object.Version
	}
	if validMode(object.Mode) {
		enabled.Mode = object.Mode
	}
	if object.Priority != nil {
		enabled.Priority = *object.Priority
	}
	if object.Inputs != nil && len(*object.Inputs) > 0 && string(*object.Inputs) != "null" {
		enabled.Inputs = cloneRaw(*object.Inputs)
	}
	return enabled, true
}

func legacyEnabledSkill(skill string) EnabledSkill {
	return EnabledSkill{Skill: skill, Mode: DefaultMode, Priority: DefaultPriority}
}

func validMode(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case ModeFull, ModeSummary, ModeExamplesOnly:
		return true
	default:
		return false
	}
}

func normalizeSkillName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != ""
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
