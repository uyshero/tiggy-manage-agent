package httpapi

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"tiggy-manage-agent/internal/eventsubscription"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

const (
	capabilityVersion           = "v1"
	capabilityStatusAvailable   = "available"
	capabilityStatusUnavailable = "unavailable"
	capabilityHealthHealthy     = "healthy"
	capabilityHealthDegraded    = "degraded"
	capabilityHealthUnavailable = "unavailable"
)

type capabilityModel struct {
	ProviderID     string                                 `json:"provider_id"`
	Model          string                                 `json:"model"`
	CapabilityType string                                 `json:"capability_type"`
	Protocol       string                                 `json:"protocol,omitempty"`
	Realtime       *managedagents.LLMRealtimeCapabilities `json:"realtime,omitempty"`
}

type capabilityDescriptor struct {
	ID        string            `json:"id"`
	Version   string            `json:"version"`
	Status    string            `json:"status"`
	Health    string            `json:"health"`
	Providers []string          `json:"providers"`
	Models    []capabilityModel `json:"models,omitempty"`
	Details   map[string]any    `json:"details,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type capabilityDiscoveryResponse struct {
	WorkspaceID  string                 `json:"workspace_id"`
	Capabilities []capabilityDescriptor `json:"capabilities"`
	GeneratedAt  time.Time              `json:"generated_at"`
}

func (s *Server) registerCapabilityRoutes() {
	s.mux.HandleFunc("GET /v2/capabilities", s.withV2Request(s.listCapabilities))
}

func (s *Server) listCapabilities(w http.ResponseWriter, r *http.Request) {
	workspaceID := requestWorkspaceID(r, managedagents.DefaultWorkspaceID)
	generatedAt := time.Now().UTC()

	providers, err := s.store.ListLLMProviders()
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	models, err := s.store.ListLLMModels("")
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}

	capabilities := make(map[string]*capabilityDescriptor, 8)
	for _, id := range []string{
		"model.generate", "model.embedding", "model.rerank", "model.multimodal_realtime", "speech.asr", "speech.tts",
		"retrieval.search", "artifact.exchange", "events.subscribe", "worker.execute",
	} {
		capabilities[id] = &capabilityDescriptor{
			ID: id, Version: capabilityVersion, Status: capabilityStatusUnavailable,
			Health: capabilityHealthUnavailable, Providers: []string{},
		}
	}

	enabledProviders := make(map[string]managedagents.LLMProvider, len(providers))
	for _, provider := range providers {
		if provider.Enabled {
			enabledProviders[provider.ID] = provider
		}
	}
	for _, model := range models {
		provider, ok := enabledProviders[model.ProviderID]
		if !ok {
			continue
		}
		id := modelCapabilityID(model.CapabilityType)
		if id == "" {
			continue
		}
		item := capabilities[id]
		item.Models = append(item.Models, capabilityModel{
			ProviderID: model.ProviderID, Model: model.Model, CapabilityType: model.CapabilityType,
			Protocol: model.Capabilities.Protocol, Realtime: model.Capabilities.Realtime,
		})
		item.Providers = appendUniqueString(item.Providers, provider.ID)
		item.Status = capabilityStatusAvailable
		item.Health = capabilityHealthHealthy
		item.UpdatedAt = latestTime(item.UpdatedAt, latestTime(provider.UpdatedAt, model.UpdatedAt))
	}
	for _, item := range capabilities {
		sort.Strings(item.Providers)
		sort.Slice(item.Models, func(i, j int) bool {
			if item.Models[i].ProviderID == item.Models[j].ProviderID {
				return item.Models[i].Model < item.Models[j].Model
			}
			return item.Models[i].ProviderID < item.Models[j].ProviderID
		})
	}

	if _, ok := s.store.(managedagents.RetrievalStore); ok {
		markBuiltInCapability(capabilities["retrieval.search"], generatedAt)
	}
	if _, ok := s.store.(managedagents.ArtifactExchangeContextStore); ok {
		markBuiltInCapability(capabilities["artifact.exchange"], generatedAt)
	}
	if _, ok := s.store.(eventsubscription.Store); ok {
		markBuiltInCapability(capabilities["events.subscribe"], generatedAt)
	}

	workers, err := s.listWorkersForRequest(r, managedagents.ListWorkersInput{WorkspaceID: workspaceID})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	addWorkerCapability(capabilities["worker.execute"], workers, generatedAt)

	result := make([]capabilityDescriptor, 0, len(capabilities))
	for _, item := range capabilities {
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = generatedAt
		}
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	writeJSON(w, http.StatusOK, capabilityDiscoveryResponse{
		WorkspaceID: workspaceID, Capabilities: result, GeneratedAt: generatedAt,
	})
}

func modelCapabilityID(capabilityType string) string {
	switch strings.TrimSpace(capabilityType) {
	case managedagents.LLMModelCapabilityText, managedagents.LLMModelCapabilityTextImage:
		return "model.generate"
	case managedagents.LLMModelCapabilityEmbedding:
		return "model.embedding"
	case managedagents.LLMModelCapabilityReranker:
		return "model.rerank"
	case managedagents.LLMModelCapabilitySpeechToText:
		return "speech.asr"
	case managedagents.LLMModelCapabilityTextToSpeech:
		return "speech.tts"
	case managedagents.LLMModelCapabilityMultimodalRealtime:
		return "model.multimodal_realtime"
	default:
		return ""
	}
}

func markBuiltInCapability(item *capabilityDescriptor, updatedAt time.Time) {
	item.Status = capabilityStatusAvailable
	item.Health = capabilityHealthHealthy
	item.Providers = []string{"tma"}
	item.UpdatedAt = updatedAt
}

func addWorkerCapability(item *capabilityDescriptor, workers []managedagents.Worker, generatedAt time.Time) {
	registered := 0
	online := 0
	providers := map[string]bool{}
	declared := map[string]bool{}
	var latest time.Time
	for _, worker := range workers {
		if worker.ArchivedAt != nil || worker.Status == managedagents.WorkerStatusArchived {
			continue
		}
		registered++
		if worker.LastSeenAt != nil {
			latest = latestTime(latest, *worker.LastSeenAt)
		}
		if worker.RegisteredAt.After(latest) {
			latest = worker.RegisteredAt
		}
		leaseActive := worker.LeaseExpiresAt == nil || worker.LeaseExpiresAt.After(generatedAt)
		if worker.Status != managedagents.WorkerStatusOnline || !leaseActive {
			continue
		}
		online++
		workerType := strings.TrimSpace(worker.WorkerType)
		if workerType != "" {
			providers[workerType] = true
		}
		if capabilities, err := tools.DecodeWorkerCapabilities(worker.Capabilities); err == nil {
			for _, capability := range capabilities.Capabilities {
				if value := strings.TrimSpace(capability); value != "" && len(declared) < 100 {
					declared[value] = true
				}
			}
		}
	}
	item.Providers = make([]string, 0, len(providers))
	for provider := range providers {
		item.Providers = append(item.Providers, provider)
	}
	sort.Strings(item.Providers)
	declaredCapabilities := make([]string, 0, len(declared))
	for capability := range declared {
		declaredCapabilities = append(declaredCapabilities, capability)
	}
	sort.Strings(declaredCapabilities)
	item.Details = map[string]any{
		"registered_workers":    registered,
		"online_workers":        online,
		"declared_capabilities": declaredCapabilities,
	}
	item.UpdatedAt = latest
	if online > 0 {
		item.Status = capabilityStatusAvailable
		item.Health = capabilityHealthHealthy
	} else if registered > 0 {
		item.Health = capabilityHealthDegraded
	}
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func latestTime(current, candidate time.Time) time.Time {
	if candidate.After(current) {
		return candidate
	}
	return current
}
