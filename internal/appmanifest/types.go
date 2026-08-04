package appmanifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/appresource"
)

const (
	SchemaVersionV1 = "tma.application-manifest.v1"

	ResourceEnvironment = "environment"
	ResourceSkill       = "skill"
	ResourceMCPServer   = "mcp_server"
	ResourceAgent       = "agent"

	StatusCreated   = "created"
	StatusUpdated   = "updated"
	StatusUnchanged = "unchanged"
)

var ErrInvalid = errors.New("invalid application manifest")

type Manifest struct {
	SchemaVersion string            `json:"schema_version"`
	Revision      string            `json:"revision"`
	Environments  []EnvironmentSpec `json:"environments,omitempty"`
	Skills        []SkillSpec       `json:"skills,omitempty"`
	MCPServers    []MCPServerSpec   `json:"mcp_servers,omitempty"`
	Agents        []AgentSpec       `json:"agents,omitempty"`
}

type EnvironmentSpec struct {
	ExternalRef string            `json:"external_ref"`
	Labels      map[string]string `json:"labels,omitempty"`
	Name        string            `json:"name"`
	Config      json.RawMessage   `json:"config,omitempty"`
}

type SkillSpec struct {
	ExternalRef   string            `json:"external_ref"`
	Labels        map[string]string `json:"labels,omitempty"`
	Identifier    string            `json:"identifier"`
	Title         string            `json:"title"`
	Description   string            `json:"description,omitempty"`
	SourceType    string            `json:"source_type,omitempty"`
	SourceLocator string            `json:"source_locator,omitempty"`
	SourcePath    string            `json:"source_path,omitempty"`
	Version       SkillVersionSpec  `json:"version"`
}

type SkillVersionSpec struct {
	ContentFormat  string          `json:"content_format,omitempty"`
	Manifest       json.RawMessage `json:"manifest,omitempty"`
	ContentText    string          `json:"content_text,omitempty"`
	Assets         json.RawMessage `json:"assets,omitempty"`
	SourceRef      string          `json:"source_ref,omitempty"`
	SourceRevision string          `json:"source_revision,omitempty"`
	SourceURL      string          `json:"source_url,omitempty"`
}

type MCPServerSpec struct {
	ExternalRef string            `json:"external_ref"`
	Labels      map[string]string `json:"labels,omitempty"`
	Identifier  string            `json:"identifier"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Config      json.RawMessage   `json:"config"`
}

type AgentSpec struct {
	ExternalRef    string            `json:"external_ref"`
	Labels         map[string]string `json:"labels,omitempty"`
	EnvironmentRef string            `json:"environment_ref,omitempty"`
	Name           string            `json:"name"`
	LLMProvider    string            `json:"llm_provider,omitempty"`
	LLMModel       string            `json:"llm_model"`
	System         string            `json:"system"`
	Tools          json.RawMessage   `json:"tools,omitempty"`
	MCP            json.RawMessage   `json:"mcp,omitempty"`
	Skills         json.RawMessage   `json:"skills,omitempty"`
}

type PublishInput struct {
	WorkspaceID string
	AppID       string
	PublishedBy string
	Manifest    Manifest
}

type ResourceResult struct {
	Type        string `json:"type"`
	ExternalRef string `json:"external_ref"`
	ResourceID  string `json:"resource_id"`
	Status      string `json:"status"`
	Version     int    `json:"version,omitempty"`
}

type PublishResult struct {
	SchemaVersion string           `json:"schema_version"`
	Revision      string           `json:"revision"`
	Checksum      string           `json:"checksum_sha256"`
	Resources     []ResourceResult `json:"resources"`
}

type Publisher interface {
	PublishApplicationManifest(ctx context.Context, input PublishInput) (PublishResult, error)
}

func Validate(manifest Manifest) error {
	if strings.TrimSpace(manifest.SchemaVersion) != SchemaVersionV1 {
		return fmt.Errorf("%w: schema_version must be %q", ErrInvalid, SchemaVersionV1)
	}
	revision := strings.TrimSpace(manifest.Revision)
	if revision == "" || len(revision) > 128 {
		return fmt.Errorf("%w: revision is required and must not exceed 128 characters", ErrInvalid)
	}
	total := len(manifest.Environments) + len(manifest.Skills) + len(manifest.MCPServers) + len(manifest.Agents)
	if total == 0 || total > 100 {
		return fmt.Errorf("%w: manifest must contain between 1 and 100 resources", ErrInvalid)
	}
	seen := map[string]struct{}{}
	validateOwnership := func(resourceType, externalRef string, labels map[string]string) error {
		ownership, err := appresource.Normalize("manifest-app", externalRef, labels)
		if err != nil || ownership.ExternalRef == "" {
			return fmt.Errorf("%w: %s external_ref and labels are invalid: %v", ErrInvalid, resourceType, err)
		}
		key := resourceType + "\x00" + ownership.ExternalRef
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate %s external_ref %q", ErrInvalid, resourceType, ownership.ExternalRef)
		}
		seen[key] = struct{}{}
		return nil
	}
	for _, spec := range manifest.Environments {
		if err := validateOwnership(ResourceEnvironment, spec.ExternalRef, spec.Labels); err != nil {
			return err
		}
		if strings.TrimSpace(spec.Name) == "" || !validJSONObject(spec.Config, true) {
			return fmt.Errorf("%w: Environment %q requires name and object config", ErrInvalid, spec.ExternalRef)
		}
	}
	for _, spec := range manifest.Skills {
		if err := validateOwnership(ResourceSkill, spec.ExternalRef, spec.Labels); err != nil {
			return err
		}
		if strings.TrimSpace(spec.Identifier) == "" || strings.TrimSpace(spec.Title) == "" ||
			!validJSONObject(spec.Version.Manifest, true) || !validJSON(spec.Version.Assets, true) {
			return fmt.Errorf("%w: Skill %q requires identifier, title, and valid version content", ErrInvalid, spec.ExternalRef)
		}
	}
	for _, spec := range manifest.MCPServers {
		if err := validateOwnership(ResourceMCPServer, spec.ExternalRef, spec.Labels); err != nil {
			return err
		}
		if strings.TrimSpace(spec.Identifier) == "" || strings.TrimSpace(spec.Name) == "" || !validJSONObject(spec.Config, false) {
			return fmt.Errorf("%w: MCP Server %q requires identifier, name, and object config", ErrInvalid, spec.ExternalRef)
		}
	}
	for _, spec := range manifest.Agents {
		if err := validateOwnership(ResourceAgent, spec.ExternalRef, spec.Labels); err != nil {
			return err
		}
		if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.LLMModel) == "" || strings.TrimSpace(spec.System) == "" ||
			!validJSONObject(spec.Tools, true) || !validJSONObject(spec.MCP, true) || !validJSONObject(spec.Skills, true) {
			return fmt.Errorf("%w: Agent %q requires name, llm_model, system, and valid object configs", ErrInvalid, spec.ExternalRef)
		}
	}
	return nil
}

func Checksum(manifest Manifest) (string, error) {
	normalized, err := NormalizeJSON(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), nil
}

func NormalizeJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func validJSON(raw json.RawMessage, emptyAllowed bool) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return emptyAllowed
	}
	return json.Valid(raw)
}

func validJSONObject(raw json.RawMessage, emptyAllowed bool) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return emptyAllowed
	}
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}
