package tma

import (
	"context"
	"net/http"
	"time"
)

type CapabilityModel struct {
	ProviderID     string                   `json:"provider_id"`
	Model          string                   `json:"model"`
	CapabilityType string                   `json:"capability_type"`
	Protocol       string                   `json:"protocol,omitempty"`
	Realtime       *LLMRealtimeCapabilities `json:"realtime,omitempty"`
}

type CapabilityDescriptor struct {
	ID        string            `json:"id"`
	Version   string            `json:"version"`
	Status    string            `json:"status"`
	Health    string            `json:"health"`
	Providers []string          `json:"providers"`
	Models    []CapabilityModel `json:"models,omitempty"`
	Details   map[string]any    `json:"details,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type CapabilityDiscoveryResponse struct {
	WorkspaceID  string                 `json:"workspace_id"`
	Capabilities []CapabilityDescriptor `json:"capabilities"`
	GeneratedAt  time.Time              `json:"generated_at"`
}

type CapabilitiesService struct{ client *Client }

func (s *CapabilitiesService) List(ctx context.Context) (CapabilityDiscoveryResponse, error) {
	var response CapabilityDiscoveryResponse
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/capabilities", nil, &response)
	return response, err
}
