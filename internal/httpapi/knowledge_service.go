package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

const (
	maxKnowledgeUploadBytes = 32 << 20
	knowledgeChunkMaxRunes  = 1200
	knowledgeChunkOverlap   = 160
	knowledgeVectorDims     = 384
)

type createKnowledgeBaseRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type createKnowledgeServiceRequest struct {
	Name                 string   `json:"name"`
	Scenario             string   `json:"scenario"`
	SystemPrompt         string   `json:"system_prompt,omitempty"`
	KnowledgeBaseIDs     []string `json:"knowledge_base_ids,omitempty"`
	KnowledgeDocumentIDs []string `json:"knowledge_document_ids,omitempty"`
	AllowWebSearch       bool     `json:"allow_web_search,omitempty"`
	SensitiveTerms       []string `json:"sensitive_terms,omitempty"`
}

type updateKnowledgeServiceRequest struct {
	Name                 string   `json:"name"`
	Scenario             string   `json:"scenario"`
	SystemPrompt         string   `json:"system_prompt,omitempty"`
	KnowledgeBaseIDs     []string `json:"knowledge_base_ids,omitempty"`
	KnowledgeDocumentIDs []string `json:"knowledge_document_ids,omitempty"`
	AllowWebSearch       bool     `json:"allow_web_search,omitempty"`
	SensitiveTerms       []string `json:"sensitive_terms,omitempty"`
}

type createKnowledgeShareRequest struct {
	ExpiresIn string `json:"expires_in"`
}

type askKnowledgeRequest struct {
	Question string `json:"question"`
}

type knowledgeShareResponse struct {
	Share    managedagents.KnowledgeServiceShare `json:"share"`
	Token    string                              `json:"token,omitempty"`
	ShareURL string                              `json:"share_url,omitempty"`
}

type knowledgeAnswerSource struct {
	Type       string  `json:"type"`
	Title      string  `json:"title,omitempty"`
	URL        string  `json:"url,omitempty"`
	DocumentID string  `json:"document_id,omitempty"`
	Content    string  `json:"content,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

type knowledgeAnswerResponse struct {
	Service       managedagents.KnowledgeService `json:"service,omitempty"`
	Answer        string                         `json:"answer"`
	Refused       bool                           `json:"refused"`
	RefusalReason string                         `json:"refusal_reason,omitempty"`
	Sources       []knowledgeAnswerSource        `json:"sources"`
}

type embeddingResult struct {
	Vector []float64
	Model  string
}

func (s *Server) knowledgeStore() (managedagents.KnowledgeServiceStore, error) {
	store, ok := s.store.(managedagents.KnowledgeServiceStore)
	if !ok {
		return nil, fmt.Errorf("%w: knowledge service store is unavailable", managedagents.ErrInvalid)
	}
	return store, nil
}

func (s *Server) getKnowledge(w http.ResponseWriter, r *http.Request) {
	serveKnowledgeIndex(w, "knowledge")
}

func (s *Server) getKnowledgeShare(w http.ResponseWriter, r *http.Request) {
	serveKnowledgeIndex(w, "share")
}

func serveKnowledgeIndex(w http.ResponseWriter, assetRoot string) {
	content, err := inspectorAssets.ReadFile("knowledge/index.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": assetRoot + " app unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) listKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListKnowledgeBases(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_bases": nonNilSlice(items)})
}

func (s *Server) createKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createKnowledgeBaseRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	item, err := store.CreateKnowledgeBase(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), request.Name, request.Description, requestActorID(r, "system"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeleteKnowledgeBase(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("base_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listKnowledgeDocuments(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListKnowledgeDocuments(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("base_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": nonNilSlice(items)})
}

func (s *Server) uploadKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "knowledge document upload requires multipart/form-data"})
		return
	}
	if r.ContentLength > maxKnowledgeUploadBytes+1024 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "knowledge document upload exceeds size limit"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxKnowledgeUploadBytes+1024)
	if err := r.ParseMultipartForm(maxKnowledgeUploadBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "knowledge document upload exceeds size limit"})
			return
		}
		writeError(w, fmt.Errorf("%w: parse multipart knowledge document upload: %v", managedagents.ErrInvalid, err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, fmt.Errorf("%w: knowledge document upload requires file field", managedagents.ErrInvalid))
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, err)
		return
	}
	contentType := fallbackString(r.FormValue("content_type"), header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	name := fallbackString(strings.TrimSpace(r.FormValue("name")), safeArtifactFileName(header.Filename))
	text, err := extractKnowledgeText(name, contentType, content)
	if err != nil {
		writeError(w, err)
		return
	}
	chunkTexts := splitKnowledgeText(text)
	if len(chunkTexts) == 0 {
		writeError(w, fmt.Errorf("%w: document contains no readable text", managedagents.ErrInvalid))
		return
	}

	embeddings := make([]managedagents.KnowledgeChunkInput, 0, len(chunkTexts))
	for _, chunk := range chunkTexts {
		embedding := s.embedKnowledgeText(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), chunk)
		embeddings = append(embeddings, managedagents.KnowledgeChunkInput{Content: chunk, Embedding: embedding.Vector, EmbeddingModel: embedding.Model})
	}

	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	bucket, err := objectstore.ResolveBucket(r.FormValue("bucket"), s.defaultObjectStoreBucket())
	if err != nil {
		writeError(w, err)
		return
	}
	objectKey := strings.TrimSpace(r.FormValue("object_key"))
	if objectKey == "" {
		objectKey = fmt.Sprintf("%s/knowledge/%s/%d-%s", requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("base_id"), time.Now().UTC().UnixNano(), safeArtifactFileName(name))
	}
	if err := objectstore.ValidateObjectKey(objectKey); err != nil {
		writeError(w, err)
		return
	}
	putResult, err := s.objectStore.PutObject(r.Context(), objectstore.PutObjectInput{
		Bucket: bucket, Key: objectKey, Body: bytes.NewReader(content), ContentType: contentType,
		SizeBytes: int64(len(content)), ChecksumSHA256: checksumHex,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	objectRef, err := managedagents.CreateObjectRefWithContext(r.Context(), s.store, managedagents.CreateObjectRefInput{
		WorkspaceID: requestWorkspaceID(r, managedagents.DefaultWorkspaceID), StorageProvider: managedagents.ObjectStorageProviderS3,
		Bucket: fallbackString(putResult.Bucket, bucket), ObjectKey: fallbackString(putResult.Key, objectKey), ObjectVersion: putResult.Version,
		ContentType: contentType, SizeBytes: int64(len(content)), ChecksumSHA256: fallbackString(putResult.ChecksumSHA256, checksumHex),
		ETag: putResult.ETag, Visibility: managedagents.ObjectVisibilityWorkspace, CreatedBy: requestActorID(r, "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	document, err := store.CreateKnowledgeDocument(r.Context(), managedagents.KnowledgeDocument{
		WorkspaceID: requestWorkspaceID(r, managedagents.DefaultWorkspaceID), KnowledgeBaseID: r.PathValue("base_id"), ObjectRefID: objectRef.ID,
		Name: name, ContentType: contentType, SizeBytes: int64(len(content)), CreatedBy: requestActorID(r, "system"),
	}, embeddings)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"document": document, "object_ref": objectRef})
}

func (s *Server) deleteKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	document, err := store.DeleteKnowledgeDocument(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("document_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if document.ObjectRefID != "" {
		if err := managedagents.DeleteObjectRefWithContext(r.Context(), s.store, document.ObjectRefID); err != nil && !errors.Is(err, managedagents.ErrConflict) && !errors.Is(err, managedagents.ErrNotFound) {
			s.logger.Warn("delete knowledge document object ref failed", "document_id", document.ID, "object_ref_id", document.ObjectRefID, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listKnowledgeServices(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListKnowledgeServices(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": nonNilSlice(items)})
}

func (s *Server) createKnowledgeService(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createKnowledgeServiceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	item, err := store.CreateKnowledgeService(r.Context(), managedagents.CreateKnowledgeServiceInput{
		WorkspaceID: requestWorkspaceID(r, managedagents.DefaultWorkspaceID), Name: request.Name, Scenario: request.Scenario,
		SystemPrompt: request.SystemPrompt, KnowledgeBaseIDs: request.KnowledgeBaseIDs, AllowWebSearch: false,
		KnowledgeDocumentIDs: request.KnowledgeDocumentIDs, SensitiveTerms: request.SensitiveTerms, CreatedBy: requestActorID(r, "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getKnowledgeService(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.GetKnowledgeService(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("service_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateKnowledgeService(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request updateKnowledgeServiceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	item, err := store.UpdateKnowledgeService(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("service_id"), managedagents.UpdateKnowledgeServiceInput{
		Name: request.Name, Scenario: request.Scenario, SystemPrompt: request.SystemPrompt,
		KnowledgeBaseIDs: request.KnowledgeBaseIDs, AllowWebSearch: false,
		KnowledgeDocumentIDs: request.KnowledgeDocumentIDs, SensitiveTerms: request.SensitiveTerms,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteKnowledgeService(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeleteKnowledgeService(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("service_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listKnowledgeShares(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListKnowledgeServiceShares(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("service_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	for index := range items {
		items[index] = knowledgeShareWithURL(r, items[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": nonNilSlice(items)})
}

func (s *Server) createKnowledgeShare(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createKnowledgeShareRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	expiresAt, err := knowledgeShareExpiry(request.ExpiresIn, time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := newKnowledgeShareToken()
	if err != nil {
		writeError(w, err)
		return
	}
	hash := knowledgeShareTokenHash(token)
	share, err := store.CreateKnowledgeServiceShare(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("service_id"), token, hash, requestActorID(r, "system"), expiresAt)
	if err != nil {
		writeError(w, err)
		return
	}
	share = knowledgeShareWithURL(r, share)
	writeJSON(w, http.StatusCreated, knowledgeShareResponse{Share: share, Token: token, ShareURL: share.ShareURL})
}

func (s *Server) revokeKnowledgeShare(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.RevokeKnowledgeServiceShare(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("share_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteRevokedKnowledgeShare(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeleteRevokedKnowledgeServiceShare(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("share_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPublicKnowledgeShare(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	share, service, err := store.ResolveKnowledgeServiceShare(r.Context(), knowledgeShareTokenHash(r.PathValue("token")))
	if err != nil {
		writeError(w, err)
		return
	}
	share.Token = ""
	share.ShareURL = ""
	writeJSON(w, http.StatusOK, map[string]any{"share": share, "service": publicKnowledgeService(service)})
}

func (s *Server) askKnowledgeService(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request askKnowledgeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	service, err := store.GetKnowledgeService(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("service_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := s.answerKnowledgeQuestion(r.Context(), store, service, "", request.Question)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Service = service
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) askPublicKnowledgeShare(w http.ResponseWriter, r *http.Request) {
	store, err := s.knowledgeStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request askKnowledgeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	share, service, err := store.ResolveKnowledgeServiceShare(r.Context(), knowledgeShareTokenHash(r.PathValue("token")))
	if err != nil {
		writeError(w, err)
		return
	}
	scoped, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{WorkspaceID: service.WorkspaceID})
	if err != nil {
		writeError(w, err)
		return
	}
	response, err := s.answerKnowledgeQuestion(scoped, store, service, share.ID, request.Question)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Service = publicKnowledgeService(service)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) answerKnowledgeQuestion(ctx context.Context, store managedagents.KnowledgeServiceStore, service managedagents.KnowledgeService, shareID string, rawQuestion string) (knowledgeAnswerResponse, error) {
	question := strings.TrimSpace(rawQuestion)
	if question == "" || utf8.RuneCountInString(question) > 2000 {
		return knowledgeAnswerResponse{}, fmt.Errorf("%w: question is required and must be at most 2000 characters", managedagents.ErrInvalid)
	}
	if matched := sensitiveKnowledgeTerm(question, service.SensitiveTerms); matched != "" {
		answer := "抱歉，这个问题属于敏感问题，当前对话服务不能回答。"
		_ = store.RecordKnowledgeQuestion(ctx, service.WorkspaceID, service.ID, shareID, question, answer, true, "sensitive", 0)
		return knowledgeAnswerResponse{Answer: answer, Refused: true, RefusalReason: "sensitive", Sources: []knowledgeAnswerSource{}}, nil
	}
	queryEmbedding := s.embedKnowledgeText(ctx, service.WorkspaceID, question)
	searchResults, err := store.SearchKnowledge(ctx, service.WorkspaceID, service.KnowledgeBaseIDs, service.KnowledgeDocumentIDs, question, queryEmbedding.Vector, 8)
	if err != nil {
		return knowledgeAnswerResponse{}, err
	}
	sources := knowledgeSourcesFromSearch(searchResults)
	if len(searchResults) == 0 {
		answer := "抱歉，知识库中没有找到相关内容，当前对话服务不能回答。"
		_ = store.RecordKnowledgeQuestion(ctx, service.WorkspaceID, service.ID, shareID, question, answer, true, "no_knowledge", 0)
		return knowledgeAnswerResponse{Answer: answer, Refused: true, RefusalReason: "no_knowledge", Sources: []knowledgeAnswerSource{}}, nil
	}
	if !knowledgeQuestionInScope(question, service, searchResults) {
		answer := "抱歉，这个问题超出了当前对话服务定义的主要场景范围。"
		_ = store.RecordKnowledgeQuestion(ctx, service.WorkspaceID, service.ID, shareID, question, answer, true, "out_of_scope", len(sources))
		return knowledgeAnswerResponse{Answer: answer, Refused: true, RefusalReason: "out_of_scope", Sources: visibleKnowledgeAnswerSources(sources)}, nil
	}
	answer := s.generateKnowledgeAnswer(ctx, service, question, sources)
	if strings.TrimSpace(answer) == "" {
		answer = extractiveKnowledgeAnswer(question, sources)
	}
	_ = store.RecordKnowledgeQuestion(ctx, service.WorkspaceID, service.ID, shareID, question, answer, false, "", len(sources))
	return knowledgeAnswerResponse{Answer: answer, Sources: visibleKnowledgeAnswerSources(sources)}, nil
}

func (s *Server) embedKnowledgeText(ctx context.Context, workspaceID string, text string) embeddingResult {
	if model, provider, ok := s.defaultEmbeddingModel(ctx, workspaceID); ok {
		if vector, err := s.openAIEmbedding(ctx, workspaceID, provider, model.Model, text); err == nil && len(vector) > 0 {
			return embeddingResult{Vector: normalizeVector(vector), Model: model.ProviderID + "/" + model.Model}
		} else if err != nil {
			s.logger.Debug("knowledge embedding provider failed; using local vector", "provider", model.ProviderID, "model", model.Model, "error", err)
		}
	}
	return embeddingResult{Vector: localHashEmbedding(text, knowledgeVectorDims), Model: "local-hash-v1"}
}

func (s *Server) defaultEmbeddingModel(ctx context.Context, workspaceID string) (managedagents.LLMModel, managedagents.LLMProvider, bool) {
	models, err := s.store.ListLLMModels("")
	if err != nil {
		return managedagents.LLMModel{}, managedagents.LLMProvider{}, false
	}
	for _, model := range models {
		if !model.IsDefaultEmbedding {
			continue
		}
		provider, err := s.store.GetLLMProvider(model.ProviderID)
		if err == nil && provider.Enabled {
			return model, provider, true
		}
	}
	_ = ctx
	_ = workspaceID
	return managedagents.LLMModel{}, managedagents.LLMProvider{}, false
}

func (s *Server) openAIEmbedding(ctx context.Context, workspaceID string, provider managedagents.LLMProvider, model string, text string) ([]float64, error) {
	providerType := llm.ResolveProviderType(provider.ID, provider.ProviderType)
	if providerType == llm.ProviderFake {
		return nil, errors.New("fake provider has no embedding endpoint")
	}
	apiKey := s.resolveLLMAPIKey(ctx, workspaceID, provider.APIKeyEnv)
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("embedding provider API key is not configured")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if baseURL == "" {
		baseURL = llm.DefaultOpenAIBaseURL
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "input": text})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("embedding provider returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding provider returned no vector")
	}
	return decoded.Data[0].Embedding, nil
}

func (s *Server) generateKnowledgeAnswer(ctx context.Context, service managedagents.KnowledgeService, question string, sources []knowledgeAnswerSource) string {
	provider, err := s.store.GetLLMProvider(s.defaultLLMProvider)
	if err != nil || !provider.Enabled || llm.ResolveProviderType(provider.ID, provider.ProviderType) == llm.ProviderFake {
		return extractiveKnowledgeAnswer(question, sources)
	}
	manager, err := llm.NewManagerWithConfig(llm.ManagerConfig{
		Provider: provider.ID, ProviderType: provider.ProviderType, Model: s.defaultLLMModel, BaseURL: provider.BaseURL,
		APIKey: s.resolveLLMAPIKey(ctx, service.WorkspaceID, provider.APIKeyEnv),
	})
	if err != nil {
		s.logger.Debug("knowledge answer model unavailable", "error", err)
		return extractiveKnowledgeAnswer(question, sources)
	}
	contextText := formatKnowledgeSourcesForPrompt(sources, 10000)
	system := strings.TrimSpace(service.SystemPrompt)
	if system == "" {
		system = "你是一个企业知识库问答助手。只回答服务场景内的问题，只依据给定知识库资料回答，不使用外部知识，不编造。"
	}
	prompt := fmt.Sprintf("服务场景：%s\n\n资料：\n%s\n\n用户问题：%s\n\n请用中文简洁回答。若资料不足，请明确说明资料不足。不要在答案中列出或引用知识库文件名、文档 ID 或来源编号。", service.Scenario, contextText, question)
	response, err := manager.Generate(ctx, llm.Request{MaxOutputTokens: 800, Messages: []llm.Message{
		{Role: "system", Content: []llm.ContentPart{{Type: "text", Text: system}}},
		{Role: "user", Content: []llm.ContentPart{{Type: "text", Text: prompt}}},
	}})
	if err != nil {
		s.logger.Debug("knowledge answer generation failed", "error", err)
		return extractiveKnowledgeAnswer(question, sources)
	}
	return strings.TrimSpace(llmMessageText(response.Message))
}

func extractKnowledgeText(filename string, contentType string, content []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	ct := strings.ToLower(contentType)
	switch {
	case ext == ".pdf" || strings.Contains(ct, "pdf"):
		reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return "", fmt.Errorf("%w: parse PDF: %v", managedagents.ErrInvalid, err)
		}
		textReader, err := reader.GetPlainText()
		if err != nil {
			return "", fmt.Errorf("%w: extract PDF text: %v", managedagents.ErrInvalid, err)
		}
		text, err := io.ReadAll(textReader)
		if err != nil {
			return "", err
		}
		return normalizeKnowledgeText(string(text)), nil
	case ext == ".docx" || strings.Contains(ct, "wordprocessingml"):
		text, err := extractDOCXText(content)
		if err != nil {
			return "", err
		}
		return normalizeKnowledgeText(text), nil
	case ext == ".html" || ext == ".htm" || strings.Contains(ct, "html"):
		return normalizeKnowledgeText(extractHTMLText(bytes.NewReader(content))), nil
	case ext == ".csv" || ext == ".tsv" || strings.Contains(ct, "csv") || strings.Contains(ct, "tab-separated-values"):
		return normalizeKnowledgeText(extractDelimitedText(ext, content)), nil
	case ext == ".json" || strings.Contains(ct, "json"):
		text, err := extractJSONText(content)
		if err != nil {
			return "", err
		}
		return normalizeKnowledgeText(text), nil
	case ext == ".xml" || strings.Contains(ct, "xml"):
		return normalizeKnowledgeText(extractXMLText(content)), nil
	default:
		if !utf8.Valid(content) {
			return "", fmt.Errorf("%w: unsupported binary document type", managedagents.ErrInvalid)
		}
		return normalizeKnowledgeText(string(content)), nil
	}
}

func extractDOCXText(content []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("%w: parse DOCX: %v", managedagents.ErrInvalid, err)
	}
	var documents []*zip.File
	for _, file := range reader.File {
		if file.Name == "word/document.xml" || strings.HasPrefix(file.Name, "word/header") || strings.HasPrefix(file.Name, "word/footer") ||
			strings.HasPrefix(file.Name, "word/footnotes") || strings.HasPrefix(file.Name, "word/endnotes") || strings.HasPrefix(file.Name, "word/comments") {
			documents = append(documents, file)
		}
	}
	if len(documents) == 0 {
		return "", fmt.Errorf("%w: DOCX document.xml not found", managedagents.ErrInvalid)
	}
	var builder strings.Builder
	for _, document := range documents {
		text, err := extractDOCXXMLText(document)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(text)
		}
	}
	return builder.String(), nil
}

func extractDOCXXMLText(document *zip.File) (string, error) {
	body, err := document.Open()
	if err != nil {
		return "", err
	}
	defer body.Close()
	var builder strings.Builder
	decoder := xml.NewDecoder(body)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: parse DOCX XML: %v", managedagents.ErrInvalid, err)
		}
		switch value := token.(type) {
		case xml.CharData:
			builder.Write([]byte(value))
		case xml.EndElement:
			switch value.Name.Local {
			case "p", "br", "tr":
				builder.WriteByte('\n')
			case "tab", "tc":
				builder.WriteByte(' ')
			}
		}
	}
	return builder.String(), nil
}

func extractHTMLText(reader io.Reader) string {
	root, err := html.Parse(reader)
	if err != nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
			builder.WriteByte(' ')
		}
		if node.Type == html.ElementNode && (node.Data == "p" || node.Data == "br" || node.Data == "li" || node.Data == "tr") {
			builder.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return builder.String()
}

func extractDelimitedText(ext string, content []byte) string {
	if !utf8.Valid(content) {
		return ""
	}
	reader := csv.NewReader(bytes.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	if ext == ".tsv" {
		reader.Comma = '\t'
	}
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return string(content)
	}
	var builder strings.Builder
	headers := records[0]
	for rowIndex, row := range records {
		if rowIndex == 0 {
			builder.WriteString(strings.Join(row, " | "))
			builder.WriteByte('\n')
			continue
		}
		for colIndex, value := range row {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if colIndex < len(headers) && strings.TrimSpace(headers[colIndex]) != "" {
				builder.WriteString(strings.TrimSpace(headers[colIndex]))
				builder.WriteString(": ")
			}
			builder.WriteString(value)
			builder.WriteString("；")
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func extractJSONText(content []byte) (string, error) {
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return "", fmt.Errorf("%w: parse JSON: %v", managedagents.ErrInvalid, err)
	}
	var builder strings.Builder
	flattenJSONText(&builder, "", value, 0)
	return builder.String(), nil
}

func flattenJSONText(builder *strings.Builder, path string, value any, depth int) {
	if depth > 20 {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			flattenJSONText(builder, nextPath, typed[key], depth+1)
		}
	case []any:
		for index, item := range typed {
			nextPath := path
			if nextPath == "" {
				nextPath = fmt.Sprintf("[%d]", index)
			}
			flattenJSONText(builder, nextPath, item, depth+1)
		}
	case string:
		writeKnowledgeField(builder, path, typed)
	case float64, bool, nil:
		writeKnowledgeField(builder, path, fmt.Sprint(typed))
	default:
		writeKnowledgeField(builder, path, fmt.Sprint(typed))
	}
}

func writeKnowledgeField(builder *strings.Builder, name string, value string) {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return
	}
	if name != "" {
		builder.WriteString(strings.ReplaceAll(name, "_", " "))
		builder.WriteString(": ")
	}
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func extractXMLText(content []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	var stack []string
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if utf8.Valid(content) {
				return string(content)
			}
			return ""
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			text := strings.TrimSpace(string(value))
			if text == "" {
				continue
			}
			if len(stack) > 0 {
				builder.WriteString(strings.Join(stack, "."))
				builder.WriteString(": ")
			}
			builder.WriteString(text)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

var knowledgeHorizontalWhitespace = regexp.MustCompile(`[ \t\f\v\r]+`)
var knowledgeBlankLines = regexp.MustCompile(`\n{3,}`)

func normalizeKnowledgeText(text string) string {
	text = strings.ReplaceAll(text, "\x00", " ")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(knowledgeHorizontalWhitespace.ReplaceAllString(line, " "))
		if line == "" {
			if len(cleaned) == 0 || cleaned[len(cleaned)-1] == "" {
				continue
			}
			cleaned = append(cleaned, "")
			continue
		}
		cleaned = append(cleaned, line)
	}
	text = strings.Join(cleaned, "\n")
	text = knowledgeBlankLines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

func splitKnowledgeText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{}
	}
	runes := []rune(text)
	chunks := []string{}
	for start := 0; start < len(runes); {
		end := min(start+knowledgeChunkMaxRunes, len(runes))
		if end < len(runes) {
			for candidate := end; candidate > start+knowledgeChunkMaxRunes/2; candidate-- {
				if strings.ContainsRune("。！？.!?\n", runes[candidate-1]) {
					end = candidate
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end >= len(runes) {
			break
		}
		start = max(end-knowledgeChunkOverlap, start+1)
	}
	return chunks
}

func localHashEmbedding(text string, dims int) []float64 {
	if dims <= 0 {
		dims = knowledgeVectorDims
	}
	vector := make([]float64, dims)
	for _, token := range knowledgeTokens(text) {
		sum := sha256.Sum256([]byte(token))
		index := int(sum[0])<<8 | int(sum[1])
		index %= dims
		sign := 1.0
		if sum[2]&1 == 1 {
			sign = -1
		}
		vector[index] += sign
	}
	return normalizeVector(vector)
}

func normalizeVector(vector []float64) []float64 {
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	out := make([]float64, len(vector))
	for i, value := range vector {
		out[i] = value / norm
	}
	return out
}

func knowledgeSourcesFromSearch(results []managedagents.KnowledgeSearchResult) []knowledgeAnswerSource {
	sources := make([]knowledgeAnswerSource, 0, len(results))
	for _, item := range results {
		sources = append(sources, knowledgeAnswerSource{Type: "knowledge", Title: item.DocumentName, DocumentID: item.DocumentID, Content: item.Content, Score: item.Score})
	}
	return sources
}

func visibleKnowledgeAnswerSources(sources []knowledgeAnswerSource) []knowledgeAnswerSource {
	visible := make([]knowledgeAnswerSource, 0, len(sources))
	for _, source := range sources {
		if source.Type == "knowledge" {
			continue
		}
		source.Content = ""
		visible = append(visible, source)
	}
	return visible
}

func sensitiveKnowledgeTerm(question string, custom []string) string {
	terms := append([]string{
		"炸弹", "爆炸物", "制毒", "毒品合成", "自杀", "自残", "攻击政府", "窃取密码", "绕过登录", "银行卡盗刷",
		"make a bomb", "malware", "steal password", "credit card fraud", "suicide method",
	}, custom...)
	lower := strings.ToLower(question)
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(lower, term) {
			return term
		}
	}
	return ""
}

func knowledgeQuestionInScope(question string, service managedagents.KnowledgeService, results []managedagents.KnowledgeSearchResult) bool {
	if len(results) > 0 {
		best := results[0]
		if best.Score >= 0.045 || best.KeywordScore > 0 || best.VectorScore >= 0.12 {
			return true
		}
		if len(results) >= 2 && results[0].Score+results[1].Score >= 0.08 {
			return true
		}
		if knowledgeEvidenceMatchesQuestion(question, results) {
			return true
		}
	}
	scopeText := strings.Join([]string{service.Name, service.Scenario, service.SystemPrompt}, " ")
	questionTokens := knowledgeTokenSet(question)
	scopeTokens := knowledgeTokenSet(scopeText)
	for token := range questionTokens {
		if scopeTokens[token] {
			return true
		}
	}
	lowerQuestion := strings.ToLower(question)
	for token := range scopeTokens {
		if utf8.RuneCountInString(token) >= 2 && strings.Contains(lowerQuestion, token) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceMatchesQuestion(question string, results []managedagents.KnowledgeSearchResult) bool {
	questionTokens := meaningfulKnowledgeTokenSet(question)
	if len(questionTokens) == 0 {
		return false
	}
	for index, result := range results {
		if index >= 3 {
			break
		}
		evidenceTokens := meaningfulKnowledgeTokenSet(result.DocumentName + " " + result.Content)
		matches := 0
		for token := range questionTokens {
			if !evidenceTokens[token] {
				continue
			}
			matches++
			if utf8.RuneCountInString(token) >= 4 || matches >= 2 {
				return true
			}
		}
	}
	return false
}

func meaningfulKnowledgeTokenSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, token := range knowledgeTokens(text) {
		token = strings.TrimSpace(token)
		runeCount := utf8.RuneCountInString(token)
		if runeCount < 2 || knowledgeCommonToken(token) {
			continue
		}
		set[token] = true
	}
	return set
}

func knowledgeCommonToken(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "这个", "那个", "什么", "一下", "可以", "如何", "怎么", "多少", "多久", "是否", "服务", "问题", "回答", "请问", "the", "and", "for", "with":
		return true
	default:
		return false
	}
}

func knowledgeTokens(text string) []string {
	text = strings.ToLower(text)
	tokens := []string{}
	var current []rune
	previousWord := ""
	joinPreviousWord := false
	flush := func() {
		if len(current) > 0 {
			word := string(current)
			tokens = append(tokens, word)
			if joinPreviousWord && previousWord != "" {
				tokens = append(tokens, previousWord+" "+word)
			}
			if utf8.RuneCountInString(word) >= 6 {
				runes := []rune(word)
				for size := 3; size <= 5; size++ {
					for i := 0; i+size <= len(runes); i++ {
						tokens = append(tokens, string(runes[i:i+size]))
					}
				}
			}
			previousWord = word
			joinPreviousWord = true
			current = nil
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
			continue
		}
		flush()
		if !unicode.IsSpace(r) {
			previousWord = ""
			joinPreviousWord = false
		}
	}
	flush()
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) {
			tokens = append(tokens, string(runes[i]))
			for size := 2; size <= 4; size++ {
				if i+size <= len(runes) && allHanRunes(runes[i:i+size]) {
					tokens = append(tokens, string(runes[i:i+size]))
				}
			}
		}
	}
	return tokens
}

func allHanRunes(runes []rune) bool {
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

func knowledgeTokenSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, token := range knowledgeTokens(text) {
		if utf8.RuneCountInString(token) >= 2 || unicode.Is(unicode.Han, []rune(token)[0]) {
			set[token] = true
		}
	}
	return set
}

func formatKnowledgeSourcesForPrompt(sources []knowledgeAnswerSource, limit int) string {
	var builder strings.Builder
	for index, source := range sources {
		label := fmt.Sprintf("资料片段 %d", index+1)
		if source.Type == "web" {
			label = fmt.Sprintf("联网摘要 %d %s", index+1, strings.TrimSpace(source.Title+" "+source.URL))
		}
		line := fmt.Sprintf("%s\n%s\n\n", strings.TrimSpace(label), source.Content)
		if builder.Len()+len(line) > limit {
			break
		}
		builder.WriteString(line)
	}
	if builder.Len() == 0 {
		return "无可用资料。"
	}
	return builder.String()
}

func extractiveKnowledgeAnswer(question string, sources []knowledgeAnswerSource) string {
	if len(sources) == 0 {
		return "当前资料不足，无法在这个服务场景内给出可靠回答。"
	}
	ranked := append([]knowledgeAnswerSource(nil), sources...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	var builder strings.Builder
	builder.WriteString("根据当前资料：")
	for i, source := range ranked {
		if i >= 3 {
			break
		}
		text := strings.TrimSpace(source.Content)
		if utf8.RuneCountInString(text) > 180 {
			runes := []rune(text)
			text = string(runes[:180]) + "..."
		}
		if text != "" {
			builder.WriteString("\n")
			builder.WriteString("- ")
			builder.WriteString(text)
		}
	}
	_ = question
	return builder.String()
}

func publicKnowledgeService(service managedagents.KnowledgeService) managedagents.KnowledgeService {
	service.WorkspaceID = ""
	service.SystemPrompt = ""
	service.SensitiveTerms = []string{}
	service.KnowledgeBaseIDs = []string{}
	service.KnowledgeDocumentIDs = []string{}
	return service
}

func knowledgeShareExpiry(value string, now time.Time) (*time.Time, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "7d":
		expires := now.Add(7 * 24 * time.Hour)
		return &expires, nil
	case "1d":
		expires := now.Add(24 * time.Hour)
		return &expires, nil
	case "1m":
		expires := now.AddDate(0, 1, 0)
		return &expires, nil
	case "1y":
		expires := now.AddDate(1, 0, 0)
		return &expires, nil
	case "permanent":
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: expires_in must be one of 1d, 7d, 1m, 1y, permanent", managedagents.ErrInvalid)
	}
}

func newKnowledgeShareToken() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func knowledgeShareTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func knowledgeShareWithURL(r *http.Request, share managedagents.KnowledgeServiceShare) managedagents.KnowledgeServiceShare {
	token := strings.TrimSpace(share.Token)
	share.Token = ""
	if token != "" {
		share.ShareURL = absoluteShareURL(r, token)
	}
	return share
}

func absoluteShareURL(r *http.Request, token string) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: "/share/" + token}).String()
}
