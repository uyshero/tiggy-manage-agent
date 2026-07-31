package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

func TestRetrievalHTTPAndLegacyKnowledgeCompatibility(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	collection := postJSON[managedagents.RetrievalCollection](t, server, "/v2/retrieval/collections", `{
		"name": "交付资料",
		"description": "供多个应用复用的通用检索集合"
	}`)
	if collection.Name != "交付资料" || collection.WorkspaceID == "" {
		t.Fatalf("unexpected retrieval collection: %+v", collection)
	}

	body, contentType := multipartArtifactUpload(t, map[string]string{
		"bucket":     "tma-retrieval",
		"object_key": "wksp_default/retrieval/delivery.txt",
	}, "file", "delivery.txt", "Harness标准部署周期为10个工作日，紧急故障2小时内响应。")
	uploadRequest := httptest.NewRequest(http.MethodPost, "/v2/retrieval/collections/"+collection.ID+"/documents", body)
	uploadRequest.Header.Set("Content-Type", contentType)
	uploadResponse := httptest.NewRecorder()
	server.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload retrieval document expected status %d, got %d: %s", http.StatusCreated, uploadResponse.Code, uploadResponse.Body.String())
	}
	var upload struct {
		Document     managedagents.RetrievalDocument     `json:"document"`
		ObjectRef    managedagents.ObjectRef             `json:"object_ref"`
		IngestionJob managedagents.RetrievalIngestionJob `json:"ingestion_job"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&upload); err != nil {
		t.Fatalf("decode retrieval upload response: %v", err)
	}
	if upload.Document.CollectionID != collection.ID || upload.Document.ChunkCount == 0 || upload.IngestionJob.Status != "ready" || upload.IngestionJob.DocumentID != upload.Document.ID {
		t.Fatalf("unexpected retrieval upload response: %+v", upload)
	}
	if upload.ObjectRef.Bucket != "tma-retrieval" || len(objectStore.puts) != 1 {
		t.Fatalf("unexpected retrieval object storage result: object=%+v puts=%+v", upload.ObjectRef, objectStore.puts)
	}

	job := getJSON[managedagents.RetrievalIngestionJob](t, server, "/v2/retrieval/ingestion-jobs/"+upload.IngestionJob.ID)
	if job.Status != "ready" || job.CompletedAt == nil {
		t.Fatalf("unexpected retrieval ingestion job: %+v", job)
	}

	search := postJSONWithStatus[struct {
		Results   []managedagents.RetrievalSearchResult `json:"results"`
		Citations []retrievalCitation                   `json:"citations"`
	}](t, server, http.MethodPost, "/v2/retrieval/search", `{
		"collection_ids": ["`+collection.ID+`"],
		"query": "标准部署周期",
		"limit": 3
	}`, http.StatusOK)
	if len(search.Results) == 0 || len(search.Citations) != len(search.Results) {
		t.Fatalf("expected retrieval results and citations, got %+v", search)
	}
	if search.Citations[0].CollectionID != collection.ID || search.Citations[0].DocumentID != upload.Document.ID {
		t.Fatalf("unexpected retrieval citation: %+v", search.Citations[0])
	}

	legacy := getJSON[struct {
		KnowledgeBases []managedagents.KnowledgeBase `json:"knowledge_bases"`
	}](t, server, "/v2/knowledge/bases")
	if len(legacy.KnowledgeBases) != 1 || legacy.KnowledgeBases[0].ID != collection.ID || legacy.KnowledgeBases[0].DocumentCount != 1 {
		t.Fatalf("legacy Knowledge API does not expose shared compatibility data: %+v", legacy.KnowledgeBases)
	}
}
