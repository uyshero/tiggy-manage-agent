package tma

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesServiceListsWorkspaceCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/capabilities" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"workspace_id":"wksp_default","generated_at":"2026-08-03T00:00:00Z","capabilities":[{"id":"model.multimodal_realtime","version":"v1","status":"available","health":"healthy","providers":["native"],"models":[{"provider_id":"native","model":"realtime","capability_type":"multimodal_realtime","protocol":"tma_multimodal_websocket_v1","realtime":{"input_formats":[{"kind":"audio","content_type":"audio/pcm","codec":"pcm_s16le"}],"output_modalities":["text"],"max_input_tracks":8,"max_frame_bytes":4194304}}],"updated_at":"2026-08-03T00:00:00Z"}]}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Capabilities.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	model := response.Capabilities[0].Models[0]
	if response.WorkspaceID != "wksp_default" || len(response.Capabilities) != 1 || model.Model != "realtime" || model.Realtime == nil || model.Realtime.MaxFrameBytes != 4<<20 {
		t.Fatalf("unexpected capability response: %+v", response)
	}
}
