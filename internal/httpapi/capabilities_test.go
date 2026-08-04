package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/eventsubscription"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestCapabilityDiscoveryServiceIdentityScopeMapping(t *testing.T) {
	scopes, err := managedagents.NormalizeServiceIdentityScopes([]string{managedagents.ServiceScopeCapabilitiesRead})
	if err != nil || len(scopes) != 1 || scopes[0] != managedagents.ServiceScopeCapabilitiesRead {
		t.Fatalf("capability discovery scope normalization = %v, %v", scopes, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v2/capabilities", nil)
	scope, mapped := serviceIdentityScopeForRequest(request)
	if !mapped || scope != managedagents.ServiceScopeCapabilitiesRead {
		t.Fatalf("capability discovery scope = %q mapped=%t", scope, mapped)
	}
}

func TestCapabilityDiscoveryAggregatesWorkspaceRoutes(t *testing.T) {
	store := newTestStore()
	now := time.Now().UTC()
	for _, model := range []managedagents.LLMModel{
		{ProviderID: "fake", Model: "embed", CapabilityType: managedagents.LLMModelCapabilityEmbedding, UpdatedAt: now},
		{ProviderID: "fake", Model: "rerank", CapabilityType: managedagents.LLMModelCapabilityReranker, UpdatedAt: now},
		{ProviderID: "fake", Model: "asr", CapabilityType: managedagents.LLMModelCapabilitySpeechToText, UpdatedAt: now},
		{ProviderID: "fake", Model: "tts", CapabilityType: managedagents.LLMModelCapabilityTextToSpeech, UpdatedAt: now},
		{ProviderID: "fake", Model: "realtime", CapabilityType: managedagents.LLMModelCapabilityMultimodalRealtime, Capabilities: managedagents.LLMModelCapabilities{
			Protocol: managedagents.LLMMultimodalRealtimeProtocolTMAWebSocket,
			Realtime: &managedagents.LLMRealtimeCapabilities{InputFormats: []managedagents.LLMRealtimeMediaFormat{{Kind: "audio", ContentType: "audio/pcm", Codec: "pcm_s16le"}}, OutputModalities: []string{"text"}, MaxInputTracks: 8, MaxFrameBytes: 4 << 20},
		}, UpdatedAt: now},
	} {
		store.models[llmModelKey(model.ProviderID, model.Model)] = model
	}
	if _, err := store.RegisterWorker(managedagents.RegisterWorkerInput{
		Name: "local", WorkerType: managedagents.WorkerTypeLocal,
		Capabilities: json.RawMessage(`{"namespaces":["default"],"apis":["exec"],"runtimes":["local"],"capabilities":["exec","filesystem.read"]}`),
	}); err != nil {
		t.Fatal(err)
	}
	expired, err := store.RegisterWorker(managedagents.RegisterWorkerInput{
		Name: "expired", WorkerType: managedagents.WorkerTypeShared,
		Capabilities: json.RawMessage(`{"namespaces":["browser"],"apis":["open"],"runtimes":["browser"],"capabilities":["browser.read"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Minute)
	expired.LeaseExpiresAt = &past
	store.workers[expired.ID] = expired

	server := NewServerWithStoreRunnerAndLLMDefaults(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo")
	response := getJSON[capabilityDiscoveryResponse](t, server, "/v2/capabilities")
	if response.WorkspaceID != managedagents.DefaultWorkspaceID || len(response.Capabilities) != 10 {
		t.Fatalf("unexpected capability response: %+v", response)
	}
	byID := make(map[string]capabilityDescriptor, len(response.Capabilities))
	for _, item := range response.Capabilities {
		byID[item.ID] = item
		if item.Version != capabilityVersion || item.UpdatedAt.IsZero() {
			t.Fatalf("capability metadata missing: %+v", item)
		}
	}
	for _, id := range []string{"model.generate", "model.embedding", "model.rerank", "model.multimodal_realtime", "speech.asr", "speech.tts", "retrieval.search", "artifact.exchange", "worker.execute"} {
		if byID[id].Status != capabilityStatusAvailable || byID[id].Health != capabilityHealthHealthy {
			t.Fatalf("expected %s to be available and healthy: %+v", id, byID[id])
		}
	}
	if _, ok := any(store).(eventsubscription.Store); ok {
		if byID["events.subscribe"].Status != capabilityStatusAvailable || byID["events.subscribe"].Health != capabilityHealthHealthy {
			t.Fatalf("expected event subscription capability to be available: %+v", byID["events.subscribe"])
		}
	} else if byID["events.subscribe"].Status != capabilityStatusUnavailable {
		t.Fatalf("expected unavailable event subscription capability: %+v", byID["events.subscribe"])
	}
	if len(byID["model.embedding"].Models) != 1 || byID["model.embedding"].Models[0].Model != "embed" {
		t.Fatalf("embedding route was not exposed: %+v", byID["model.embedding"])
	}
	realtimeModels := byID["model.multimodal_realtime"].Models
	if len(realtimeModels) != 1 || realtimeModels[0].Protocol != managedagents.LLMMultimodalRealtimeProtocolTMAWebSocket || realtimeModels[0].Realtime == nil || realtimeModels[0].Realtime.MaxInputTracks != 8 {
		t.Fatalf("realtime route metadata was not exposed: %+v", realtimeModels)
	}
	onlineWorkers, ok := byID["worker.execute"].Details["online_workers"].(float64)
	if !ok || onlineWorkers != 1 {
		t.Fatalf("worker status was not aggregated: %+v", byID["worker.execute"])
	}
	registeredWorkers, ok := byID["worker.execute"].Details["registered_workers"].(float64)
	if !ok || registeredWorkers != 2 || len(byID["worker.execute"].Providers) != 1 || byID["worker.execute"].Providers[0] != managedagents.WorkerTypeLocal {
		t.Fatalf("expired worker must not provide an executable route: %+v", byID["worker.execute"])
	}
	declared, ok := byID["worker.execute"].Details["declared_capabilities"].([]any)
	if !ok || len(declared) != 2 || declared[0] != "exec" || declared[1] != "filesystem.read" {
		t.Fatalf("worker capabilities were not exposed: %#v", byID["worker.execute"].Details)
	}
}

func TestCapabilityDiscoveryMarksDisabledRoutesUnavailable(t *testing.T) {
	store := newTestStore()
	store.providers["fake"] = managedagents.LLMProvider{ID: "fake", ProviderType: "fake", Enabled: false}
	server := NewServerWithStoreRunnerAndLLMDefaults(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo")
	response := getJSON[capabilityDiscoveryResponse](t, server, "/v2/capabilities")
	for _, item := range response.Capabilities {
		if strings.HasPrefix(item.ID, "model.") || strings.HasPrefix(item.ID, "speech.") {
			if item.Status != capabilityStatusUnavailable || item.Health != capabilityHealthUnavailable {
				t.Fatalf("expected disabled model route to be unavailable: %+v", item)
			}
		}
	}
}
