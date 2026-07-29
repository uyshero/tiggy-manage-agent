package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
)

func TestKnowledgeHTTPUploadAskAndPublicShare(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	base := postJSON[managedagents.KnowledgeBase](t, server, "/v2/knowledge/bases", `{
		"name": "售后知识库",
		"description": "Harness 售后政策"
	}`)

	body, contentType := multipartArtifactUpload(t, map[string]string{
		"bucket":     "tma-knowledge",
		"object_key": "wksp_default/knowledge/support.txt",
	}, "file", "support.txt", "Harness售后服务时间为工作日9:00到18:00。紧急故障2小时内响应。")
	uploadRequest := httptest.NewRequest(http.MethodPost, "/v2/knowledge/bases/"+base.ID+"/documents", body)
	uploadRequest.Header.Set("Content-Type", contentType)
	uploadResponse := httptest.NewRecorder()
	server.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload document expected status %d, got %d: %s", http.StatusCreated, uploadResponse.Code, uploadResponse.Body.String())
	}
	var upload struct {
		Document  managedagents.KnowledgeDocument `json:"document"`
		ObjectRef managedagents.ObjectRef         `json:"object_ref"`
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&upload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if upload.Document.ChunkCount == 0 || upload.ObjectRef.Bucket != "tma-knowledge" {
		t.Fatalf("unexpected upload response: %+v", upload)
	}
	if len(objectStore.puts) != 1 || objectStore.puts[0].Content != "Harness售后服务时间为工作日9:00到18:00。紧急故障2小时内响应。" {
		t.Fatalf("unexpected object store puts: %#v", objectStore.puts)
	}

	service := postJSON[managedagents.KnowledgeService](t, server, "/v2/knowledge/services", `{
		"name": "售后政策助手",
		"scenario": "回答 Harness 售后服务时间和紧急故障响应政策",
		"knowledge_base_ids": ["`+base.ID+`"],
		"sensitive_terms": ["内部密钥"]
	}`)

	updated := postJSONWithStatus[managedagents.KnowledgeService](t, server, http.MethodPatch, "/v2/knowledge/services/"+service.ID, `{
		"name": "售后政策助手 V2",
		"scenario": "回答 Harness 售后服务时间、紧急故障响应政策和服务边界",
		"system_prompt": "只回答售后相关问题。",
		"knowledge_base_ids": ["`+base.ID+`"],
		"allow_web_search": true,
		"sensitive_terms": ["内部密钥", "报价底价"]
	}`, http.StatusOK)
	if updated.Name != "售后政策助手 V2" || !updated.AllowWebSearch || updated.SystemPrompt == "" || len(updated.SensitiveTerms) != 2 {
		t.Fatalf("unexpected updated service: %+v", updated)
	}
	service = updated

	answer := postJSONWithStatus[knowledgeAnswerResponse](t, server, http.MethodPost, "/v2/knowledge/services/"+service.ID+"/ask", `{
		"question": "紧急故障多久响应？"
	}`, http.StatusOK)
	if answer.Refused || !strings.Contains(answer.Answer, "2小时") || len(answer.Sources) != 0 {
		t.Fatalf("unexpected knowledge answer: %+v", answer)
	}

	sensitive := postJSONWithStatus[knowledgeAnswerResponse](t, server, http.MethodPost, "/v2/knowledge/services/"+service.ID+"/ask", `{
		"question": "内部密钥是什么？"
	}`, http.StatusOK)
	if !sensitive.Refused || sensitive.RefusalReason != "sensitive" {
		t.Fatalf("expected sensitive refusal, got %+v", sensitive)
	}

	share := postJSON[knowledgeShareResponse](t, server, "/v2/knowledge/services/"+service.ID+"/shares", `{"expires_in":"1d"}`)
	if share.Token == "" || !strings.Contains(share.ShareURL, "/share/"+share.Token) {
		t.Fatalf("unexpected share response: %+v", share)
	}

	publicAnswer := postJSONWithStatus[knowledgeAnswerResponse](t, server, http.MethodPost, "/v2/public/knowledge-shares/"+share.Token+"/ask", `{
		"question": "售后服务时间是什么？"
	}`, http.StatusOK)
	if publicAnswer.Refused || !strings.Contains(publicAnswer.Answer, "工作日9:00到18:00") {
		t.Fatalf("unexpected public share answer: %+v", publicAnswer)
	}
	if publicAnswer.Service.WorkspaceID != "" || len(publicAnswer.Service.KnowledgeBaseIDs) != 0 || len(publicAnswer.Service.SensitiveTerms) != 0 {
		t.Fatalf("public answer leaked private service fields: %+v", publicAnswer.Service)
	}

	store.mu.Lock()
	questions := append([]testKnowledgeQuestion(nil), store.knowledgeQuestions...)
	store.mu.Unlock()
	if len(questions) != 3 || questions[1].RefusalReason != "sensitive" || questions[2].ShareID != share.Share.ID {
		t.Fatalf("unexpected recorded questions: %+v", questions)
	}
}

func TestKnowledgeServiceCanLimitDocumentsPerBase(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	baseA := postJSON[managedagents.KnowledgeBase](t, server, "/v2/knowledge/bases", `{"name":"售后知识库"}`)
	baseB := postJSON[managedagents.KnowledgeBase](t, server, "/v2/knowledge/bases", `{"name":"交付知识库"}`)
	uploadDoc := func(base managedagents.KnowledgeBase, name, text string) managedagents.KnowledgeDocument {
		body, contentType := multipartArtifactUpload(t, map[string]string{"bucket": "tma-knowledge"}, "file", name, text)
		request := httptest.NewRequest(http.MethodPost, "/v2/knowledge/bases/"+base.ID+"/documents", body)
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("upload %s expected status %d, got %d: %s", name, http.StatusCreated, response.Code, response.Body.String())
		}
		var upload struct {
			Document managedagents.KnowledgeDocument `json:"document"`
		}
		if err := json.NewDecoder(response.Body).Decode(&upload); err != nil {
			t.Fatalf("decode upload %s: %v", name, err)
		}
		return upload.Document
	}
	selectedA := uploadDoc(baseA, "support-hours.txt", "售后服务时间为工作日9点到18点。")
	excludedA := uploadDoc(baseA, "refund.txt", "退款政策为合同签署后7天内可申请。")
	allB := uploadDoc(baseB, "delivery.txt", "标准部署周期为10个工作日。")

	service := postJSON[managedagents.KnowledgeService](t, server, "/v2/knowledge/services", `{
		"name": "限定文件助手",
		"scenario": "回答售后服务时间和交付部署周期",
		"knowledge_base_ids": ["`+baseA.ID+`", "`+baseB.ID+`"],
		"knowledge_document_ids": ["`+selectedA.ID+`"]
	}`)
	if len(service.KnowledgeDocumentIDs) != 1 || service.KnowledgeDocumentIDs[0] != selectedA.ID {
		t.Fatalf("service did not preserve document scope: %+v", service)
	}

	results, err := store.SearchKnowledge(context.Background(), service.WorkspaceID, service.KnowledgeBaseIDs, service.KnowledgeDocumentIDs, "标准部署周期多久", localHashEmbedding("标准部署周期多久", knowledgeVectorDims), 8)
	if err != nil {
		t.Fatalf("search scoped service: %v", err)
	}
	var sawB, sawExcludedA bool
	for _, result := range results {
		if result.DocumentID == allB.ID {
			sawB = true
		}
		if result.DocumentID == excludedA.ID {
			sawExcludedA = true
		}
	}
	if !sawB || sawExcludedA {
		t.Fatalf("expected selected file scope per base, sawB=%v sawExcludedA=%v results=%+v", sawB, sawExcludedA, results)
	}
}

func TestKnowledgeShareExpiryOptions(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		value string
		want  time.Time
	}{
		{value: "", want: now.Add(7 * 24 * time.Hour)},
		{value: "1d", want: now.Add(24 * time.Hour)},
		{value: "7d", want: now.Add(7 * 24 * time.Hour)},
		{value: "1m", want: now.AddDate(0, 1, 0)},
		{value: "1y", want: now.AddDate(1, 0, 0)},
	}
	for _, tc := range cases {
		got, err := knowledgeShareExpiry(tc.value, now)
		if err != nil {
			t.Fatalf("knowledgeShareExpiry(%q) returned error: %v", tc.value, err)
		}
		if got == nil || !got.Equal(tc.want) {
			t.Fatalf("knowledgeShareExpiry(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
	permanent, err := knowledgeShareExpiry("permanent", now)
	if err != nil || permanent != nil {
		t.Fatalf("permanent expiry = %v, %v; want nil, nil", permanent, err)
	}
	if _, err := knowledgeShareExpiry("2d", now); err == nil {
		t.Fatal("expected invalid expiry to fail")
	}
}

func TestKnowledgeShareHistoryURLAndDeleteRevokedRecord(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", &fakeObjectStore{})
	service := postJSON[managedagents.KnowledgeService](t, server, "/v2/knowledge/services", `{
		"name": "分享历史助手",
		"scenario": "回答分享历史问题"
	}`)

	created := postJSON[knowledgeShareResponse](t, server, "/v2/knowledge/services/"+service.ID+"/shares", `{"expires_in":"7d"}`)
	if created.ShareURL == "" || created.Share.ShareURL != created.ShareURL {
		t.Fatalf("expected created share URL to be available, got %+v", created)
	}
	list := getJSON[struct {
		Shares []managedagents.KnowledgeServiceShare `json:"shares"`
	}](t, server, "/v2/knowledge/services/"+service.ID+"/shares")
	if len(list.Shares) != 1 || list.Shares[0].ShareURL != created.ShareURL {
		t.Fatalf("expected historical share URL to be visible, got %+v", list.Shares)
	}

	activeDelete := httptest.NewRecorder()
	server.ServeHTTP(activeDelete, httptest.NewRequest(http.MethodDelete, "/v2/knowledge/shares/"+created.Share.ID, nil))
	if activeDelete.Code != http.StatusConflict {
		t.Fatalf("expected active share record delete to return 409, got %d: %s", activeDelete.Code, activeDelete.Body.String())
	}

	revoke := httptest.NewRecorder()
	server.ServeHTTP(revoke, httptest.NewRequest(http.MethodPost, "/v2/knowledge/shares/"+created.Share.ID+"/revoke", nil))
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("expected share revoke to return 204, got %d: %s", revoke.Code, revoke.Body.String())
	}
	deleteRevoked := httptest.NewRecorder()
	server.ServeHTTP(deleteRevoked, httptest.NewRequest(http.MethodDelete, "/v2/knowledge/shares/"+created.Share.ID, nil))
	if deleteRevoked.Code != http.StatusNoContent {
		t.Fatalf("expected revoked share record delete to return 204, got %d: %s", deleteRevoked.Code, deleteRevoked.Body.String())
	}
	empty := getJSON[struct {
		Shares []managedagents.KnowledgeServiceShare `json:"shares"`
	}](t, server, "/v2/knowledge/services/"+service.ID+"/shares")
	if len(empty.Shares) != 0 {
		t.Fatalf("expected deleted share record to be absent, got %+v", empty.Shares)
	}
}

func TestKnowledgeHTTPDeleteBaseRemovesDocuments(t *testing.T) {
	store := newTestStore()
	objectStore := &fakeObjectStore{}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStore(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil, "fake", "fake-demo", objectStore)

	base := postJSON[managedagents.KnowledgeBase](t, server, "/v2/knowledge/bases", `{"name":"待删除知识库"}`)
	body, contentType := multipartArtifactUpload(t, map[string]string{"bucket": "tma-knowledge"}, "file", "delete-me.txt", "这是一份待删除文件。")
	uploadRequest := httptest.NewRequest(http.MethodPost, "/v2/knowledge/bases/"+base.ID+"/documents", body)
	uploadRequest.Header.Set("Content-Type", contentType)
	uploadResponse := httptest.NewRecorder()
	server.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload document expected status %d, got %d: %s", http.StatusCreated, uploadResponse.Code, uploadResponse.Body.String())
	}

	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/v2/knowledge/bases/"+base.ID, nil))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete knowledge base expected status %d, got %d: %s", http.StatusNoContent, deleteResponse.Code, deleteResponse.Body.String())
	}

	list := getJSON[struct {
		KnowledgeBases []managedagents.KnowledgeBase `json:"knowledge_bases"`
	}](t, server, "/v2/knowledge/bases")
	if len(list.KnowledgeBases) != 0 {
		t.Fatalf("expected deleted knowledge base to be absent, got %+v", list.KnowledgeBases)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.knowledgeDocuments) != 0 || len(store.knowledgeChunks) != 0 {
		t.Fatalf("expected documents and chunks to be removed, documents=%+v chunks=%+v", store.knowledgeDocuments, store.knowledgeChunks)
	}
}

func TestKnowledgeQuestionWithoutKnowledgeRefusal(t *testing.T) {
	store := newTestStore()
	server := &Server{store: store, logger: slog.Default(), defaultLLMProvider: "fake", defaultLLMModel: "fake-demo"}
	service := managedagents.KnowledgeService{
		ID:               "ksvc_scope",
		WorkspaceID:      managedagents.DefaultWorkspaceID,
		Name:             "售后政策助手",
		Scenario:         "只回答 Harness 售后政策",
		KnowledgeBaseIDs: []string{"kb_empty"},
		Status:           "active",
	}

	answer, err := server.answerKnowledgeQuestion(context.Background(), store, service, "", "请介绍一下量子计算投资建议")
	if err != nil {
		t.Fatalf("answer out-of-scope question: %v", err)
	}
	if !answer.Refused || answer.RefusalReason != "no_knowledge" {
		t.Fatalf("expected no-knowledge refusal, got %+v", answer)
	}
}

func TestKnowledgeQuestionScopeAllowsUsefulWeakEvidence(t *testing.T) {
	service := managedagents.KnowledgeService{
		Name:     "售后政策助手",
		Scenario: "回答 Harness 售后政策",
	}
	results := []managedagents.KnowledgeSearchResult{{
		DocumentName: "support.txt",
		Content:      "客户提交工单后，紧急故障2小时内响应。",
		KeywordScore: 0,
		VectorScore:  0.06,
		Score:        0.035,
	}}
	if !knowledgeQuestionInScope("紧急故障多久响应？", service, results) {
		t.Fatalf("expected useful weak evidence to be treated as in-scope")
	}
	if knowledgeQuestionInScope("请介绍一下量子计算投资建议", service, nil) {
		t.Fatalf("expected unrelated question without evidence to remain out-of-scope")
	}
}

func TestKnowledgePublicShareBypassesAuth(t *testing.T) {
	store := newTestStore()
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store,
		runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil),
		nil,
		"fake",
		"fake-demo",
		objectstore.NewNoopClient(objectstore.Config{}),
		defaultExecutionResolver(store),
		"",
		"",
		AuthConfig{Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api"},
		defaultSubagentPolicy(),
		nil,
	)

	service, err := store.CreateKnowledgeService(context.Background(), managedagents.CreateKnowledgeServiceInput{
		WorkspaceID: managedagents.DefaultWorkspaceID,
		Name:        "公开售后助手",
		Scenario:    "回答公开售后问题",
		CreatedBy:   "tester",
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	token := "public-share-token"
	if _, err := store.CreateKnowledgeServiceShare(context.Background(), managedagents.DefaultWorkspaceID, service.ID, token, knowledgeShareTokenHash(token), "tester", nil); err != nil {
		t.Fatalf("create share: %v", err)
	}

	management := httptest.NewRecorder()
	server.ServeHTTP(management, httptest.NewRequest(http.MethodGet, "/v2/knowledge/bases", nil))
	if management.Code != http.StatusUnauthorized {
		t.Fatalf("expected management endpoint to require auth, got %d: %s", management.Code, management.Body.String())
	}

	public := httptest.NewRecorder()
	server.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/v2/public/knowledge-shares/"+token, nil))
	if public.Code != http.StatusOK {
		t.Fatalf("expected public share endpoint to bypass auth, got %d: %s", public.Code, public.Body.String())
	}
}

func TestKnowledgeExtractStructuredFormats(t *testing.T) {
	csvText, err := extractKnowledgeText("policy.csv", "text/csv", []byte("问题,答案\n紧急故障,2小时响应\n售后时间,工作日9:00-18:00\n"))
	if err != nil {
		t.Fatalf("extract csv: %v", err)
	}
	if !strings.Contains(csvText, "问题: 紧急故障") || !strings.Contains(csvText, "答案: 2小时响应") {
		t.Fatalf("csv extraction lost header/value context: %q", csvText)
	}

	jsonText, err := extractKnowledgeText("policy.json", "application/json", []byte(`{"support":{"hours":"工作日9:00-18:00","urgent":"2小时响应"},"tags":["售后","工单"]}`))
	if err != nil {
		t.Fatalf("extract json: %v", err)
	}
	if !strings.Contains(jsonText, "support.hours: 工作日9:00-18:00") || !strings.Contains(jsonText, "support.urgent: 2小时响应") {
		t.Fatalf("json extraction lost key paths: %q", jsonText)
	}

	xmlText, err := extractKnowledgeText("policy.xml", "application/xml", []byte(`<policy><support><hours>工作日9:00-18:00</hours><urgent>2小时响应</urgent></support></policy>`))
	if err != nil {
		t.Fatalf("extract xml: %v", err)
	}
	if !strings.Contains(xmlText, "policy.support.hours: 工作日9:00-18:00") || !strings.Contains(xmlText, "policy.support.urgent: 2小时响应") {
		t.Fatalf("xml extraction lost element paths: %q", xmlText)
	}
}

func TestKnowledgeNormalizeSplitAndTokensImproveChineseRetrieval(t *testing.T) {
	text := normalizeKnowledgeText("标题\n\n第一段：售后服务时间。\n第二段：紧急故障 2 小时响应。")
	if !strings.Contains(text, "\n\n") || !strings.Contains(text, "\n第二段") {
		t.Fatalf("normalize should preserve paragraph boundaries, got %q", text)
	}

	long := strings.Repeat("售后服务时间为工作日9点到18点。", 90) + "\n退款政策按合同执行。"
	chunks := splitKnowledgeText(long)
	if len(chunks) < 2 {
		t.Fatalf("expected long document to split into multiple chunks, got %d", len(chunks))
	}
	if utf8.RuneCountInString(chunks[0]) > knowledgeChunkMaxRunes {
		t.Fatalf("first chunk too large: %d", utf8.RuneCountInString(chunks[0]))
	}

	tokens := knowledgeTokens("紧急故障响应 support hours")
	for _, want := range []string{"紧急", "紧急故", "紧急故障", "故障响应", "support", "hours", "support hours"} {
		if !containsKnowledgeTestString(tokens, want) {
			t.Fatalf("knowledgeTokens missing %q in %v", want, tokens)
		}
	}
	queryVector := localHashEmbedding("紧急故障多久响应", knowledgeVectorDims)
	docVector := localHashEmbedding("客户提交工单后，紧急故障2小时内响应。", knowledgeVectorDims)
	if dotFloat64(queryVector, docVector) <= 0.05 {
		t.Fatalf("expected local hash embedding to overlap for related Chinese query")
	}
}

func TestKnowledgeExtractDOCXIncludesHeadersAndBody(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	writeZipFile(t, writer, "word/header1.xml", `<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>页眉售后政策</w:t></w:r></w:p></w:hdr>`)
	writeZipFile(t, writer, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>正文紧急故障2小时响应</w:t></w:r></w:p></w:body></w:document>`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close docx zip: %v", err)
	}
	text, err := extractKnowledgeText("policy.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", archive.Bytes())
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if !strings.Contains(text, "页眉售后政策") || !strings.Contains(text, "正文紧急故障2小时响应") {
		t.Fatalf("docx extraction missed header/body: %q", text)
	}
}

func writeZipFile(t *testing.T, writer *zip.Writer, name string, content string) {
	t.Helper()
	file, err := writer.Create(name)
	if err != nil {
		t.Fatalf("create zip entry %s: %v", name, err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatalf("write zip entry %s: %v", name, err)
	}
}

func containsKnowledgeTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func dotFloat64(a []float64, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	score := 0.0
	for i := range a {
		score += a[i] * b[i]
	}
	return score
}
