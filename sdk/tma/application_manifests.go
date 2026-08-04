package tma

import (
	"context"
	"encoding/json"
	"net/http"
)

const ApplicationManifestSchemaV1 = "tma.application-manifest.v1"

type ApplicationManifest struct {
	SchemaVersion string                           `json:"schema_version"`
	Revision      string                           `json:"revision"`
	Environments  []ApplicationManifestEnvironment `json:"environments,omitempty"`
	Skills        []ApplicationManifestSkill       `json:"skills,omitempty"`
	MCPServers    []ApplicationManifestMCPServer   `json:"mcp_servers,omitempty"`
	Agents        []ApplicationManifestAgent       `json:"agents,omitempty"`
}

type ApplicationManifestEnvironment struct {
	ExternalRef string            `json:"external_ref"`
	Labels      map[string]string `json:"labels,omitempty"`
	Name        string            `json:"name"`
	Config      json.RawMessage   `json:"config,omitempty"`
}

type ApplicationManifestSkill struct {
	ExternalRef   string                          `json:"external_ref"`
	Labels        map[string]string               `json:"labels,omitempty"`
	Identifier    string                          `json:"identifier"`
	Title         string                          `json:"title"`
	Description   string                          `json:"description,omitempty"`
	SourceType    string                          `json:"source_type,omitempty"`
	SourceLocator string                          `json:"source_locator,omitempty"`
	SourcePath    string                          `json:"source_path,omitempty"`
	Version       ApplicationManifestSkillVersion `json:"version"`
}

type ApplicationManifestSkillVersion struct {
	ContentFormat  string          `json:"content_format,omitempty"`
	Manifest       json.RawMessage `json:"manifest,omitempty"`
	ContentText    string          `json:"content_text,omitempty"`
	Assets         json.RawMessage `json:"assets,omitempty"`
	SourceRef      string          `json:"source_ref,omitempty"`
	SourceRevision string          `json:"source_revision,omitempty"`
	SourceURL      string          `json:"source_url,omitempty"`
}

type ApplicationManifestMCPServer struct {
	ExternalRef string            `json:"external_ref"`
	Labels      map[string]string `json:"labels,omitempty"`
	Identifier  string            `json:"identifier"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Config      MCPServerConfig   `json:"config"`
}

type ApplicationManifestAgent struct {
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

type PublishApplicationManifestRequest struct {
	AppID    string              `json:"app_id,omitempty"`
	Manifest ApplicationManifest `json:"manifest"`
}

type ApplicationManifestResourceResult struct {
	Type        string `json:"type"`
	ExternalRef string `json:"external_ref"`
	ResourceID  string `json:"resource_id"`
	Status      string `json:"status"`
	Version     int    `json:"version,omitempty"`
}

type ApplicationManifestPublishResult struct {
	SchemaVersion string                              `json:"schema_version"`
	Revision      string                              `json:"revision"`
	Checksum      string                              `json:"checksum_sha256"`
	Resources     []ApplicationManifestResourceResult `json:"resources"`
}

type ApplicationManifestsService struct{ client *Client }

func (s *ApplicationManifestsService) Publish(ctx context.Context, request PublishApplicationManifestRequest) (ApplicationManifestPublishResult, error) {
	var result ApplicationManifestPublishResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/application-manifests/publish", request, &result)
	return result, err
}
