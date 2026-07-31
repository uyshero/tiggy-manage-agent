package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

const (
	modelRuntimeMaxOutputTokens = 32768
	modelRuntimeMaxRequestBytes = 2 << 20
)

type modelRuntimeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type modelRuntimeGenerateRequest struct {
	ProviderID      string                `json:"provider_id,omitempty"`
	Model           string                `json:"model,omitempty"`
	Messages        []modelRuntimeMessage `json:"messages"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
}

type modelRuntimeGenerateResponse struct {
	Text         string    `json:"text"`
	ProviderID   string    `json:"provider_id"`
	Model        string    `json:"model"`
	Usage        llm.Usage `json:"usage"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

type modelRuntimeEmbeddingRequest struct {
	ProviderID string   `json:"provider_id,omitempty"`
	Model      string   `json:"model,omitempty"`
	Inputs     []string `json:"inputs"`
}

type modelRuntimeEmbedding struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type modelRuntimeEmbeddingResponse struct {
	Embeddings []modelRuntimeEmbedding `json:"embeddings"`
	ProviderID string                  `json:"provider_id"`
	Model      string                  `json:"model"`
	Dimensions int                     `json:"dimensions"`
	Usage      llm.Usage               `json:"usage"`
}

type modelRuntimeRerankRequest struct {
	ProviderID string   `json:"provider_id,omitempty"`
	Model      string   `json:"model,omitempty"`
	Query      string   `json:"query"`
	Documents  []string `json:"documents"`
	TopN       int      `json:"top_n,omitempty"`
}

type modelRuntimeRerankResponse struct {
	Results    []llm.RerankResult `json:"results"`
	ProviderID string             `json:"provider_id"`
	Model      string             `json:"model"`
}

func (s *Server) listModelRuntimeInvocations(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(managedagents.ModelInvocationStore)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "model_invocation_store_unavailable", "model invocation reporting is unavailable", false, nil)
		return
	}
	query := r.URL.Query()
	from, err := parseOptionalTime(query.Get("from"))
	if err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: invalid from: %v", managedagents.ErrInvalid, err))
		return
	}
	to, err := parseOptionalTime(query.Get("to"))
	if err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: invalid to: %v", managedagents.ErrInvalid, err))
		return
	}
	limit := 100
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			writeV2ManagedError(w, r, fmt.Errorf("%w: limit must be an integer", managedagents.ErrInvalid))
			return
		}
	}
	report, err := store.ListModelInvocationsContext(r.Context(), managedagents.ListModelInvocationsInput{
		WorkspaceID: requestWorkspaceID(r, managedagents.DefaultWorkspaceID), PrincipalID: strings.TrimSpace(query.Get("principal_id")),
		ServiceIdentityID: strings.TrimSpace(query.Get("service_identity_id")),
		Capability:        strings.TrimSpace(query.Get("capability")), ProviderID: strings.TrimSpace(query.Get("provider_id")),
		Model: strings.TrimSpace(query.Get("model")), Status: strings.TrimSpace(query.Get("status")),
		From: from, To: to, Limit: limit,
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) generateModelRuntimeText(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, modelRuntimeMaxRequestBytes)
	var request modelRuntimeGenerateRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	providerID, modelName, messages, err := s.validateModelRuntimeRequest(request)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}

	provider, err := s.store.GetLLMProvider(providerID)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if !provider.Enabled {
		writeV2Error(w, requestIDFromRequest(r), http.StatusConflict, "model_provider_disabled", "the selected model provider is disabled", false, nil)
		return
	}
	model, exists, err := s.findLLMModel(providerID, modelName)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if !exists {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotFound, "model_not_found", "the selected model is not registered", false, nil)
		return
	}
	if !managedagents.LLMModelSupportsAgentRuntime(model.CapabilityType) {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "unsupported_model_capability", "the selected model does not support text generation", false, map[string]any{"capability_type": model.CapabilityType})
		return
	}

	startedAt := time.Now().UTC()
	invocation := s.newModelInvocationInput(r, provider, model, managedagents.ModelInvocationCapabilityGenerate, startedAt)
	invocation.InputItems = int64(len(messages))
	invocation.InputCharacters = modelMessagesCharacterCount(messages)
	release, admitted := s.admitModelInvocationHTTP(w, r, &invocation)
	if !admitted {
		return
	}
	defer release()
	response, err := s.modelExecutor().Generate(r.Context(), modelruntime.GenerateRequest{
		Route: s.modelRuntimeRoute(r, provider, model), MaxOutputTokens: request.MaxOutputTokens, Messages: messages,
	})
	if err != nil {
		completeModelInvocation(&invocation, managedagents.ModelInvocationStatusFailed, failedModelInvocationCode(err))
		s.recordModelInvocation(r, invocation)
		s.writeModelRuntimeError(w, r, err)
		return
	}
	invocation.InputTokens = response.Usage.InputTokens
	invocation.OutputTokens = response.Usage.OutputTokens
	invocation.TotalTokens = response.Usage.TotalTokens
	invocation.CachedInputTokens = response.Usage.CachedInputTokens
	invocation.ReasoningTokens = response.Usage.ReasoningTokens
	invocation.OutputItems = 1
	invocation.OutputCharacters = int64(utf8.RuneCountInString(modelRuntimeMessageText(response.Message)))
	completeModelInvocation(&invocation, managedagents.ModelInvocationStatusCompleted, "")
	s.recordModelInvocation(r, invocation)
	writeJSON(w, http.StatusOK, modelRuntimeGenerateResponse{
		Text: modelRuntimeMessageText(response.Message), ProviderID: provider.ID, Model: model.Model,
		Usage: response.Usage, FinishReason: response.FinishReason,
	})
}

func (s *Server) createModelRuntimeEmbeddings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, modelRuntimeMaxRequestBytes)
	var request modelRuntimeEmbeddingRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	provider, model, err := s.resolveModelRuntimeCapability(request.ProviderID, request.Model, managedagents.LLMModelCapabilityEmbedding)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if !modelRuntimeEmbeddingProtocolSupported(model.Capabilities.Protocol) {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "unsupported_model_protocol", "the selected embedding protocol is not supported", false, map[string]any{"protocol": model.Capabilities.Protocol})
		return
	}
	if len(request.Inputs) == 0 || len(request.Inputs) > model.Capabilities.MaxBatchSize {
		writeV2ManagedError(w, r, fmt.Errorf("%w: inputs must contain between 1 and %d items", managedagents.ErrInvalid, model.Capabilities.MaxBatchSize))
		return
	}
	for index, input := range request.Inputs {
		if strings.TrimSpace(input) == "" {
			writeV2ManagedError(w, r, fmt.Errorf("%w: inputs[%d] is required", managedagents.ErrInvalid, index))
			return
		}
	}

	startedAt := time.Now().UTC()
	invocation := s.newModelInvocationInput(r, provider, model, managedagents.ModelInvocationCapabilityEmbedding, startedAt)
	invocation.InputItems = int64(len(request.Inputs))
	invocation.InputCharacters = stringsCharacterCount(request.Inputs)
	release, admitted := s.admitModelInvocationHTTP(w, r, &invocation)
	if !admitted {
		return
	}
	defer release()
	response, err := s.modelExecutor().Embed(r.Context(), modelruntime.EmbeddingRequest{
		Route: s.modelRuntimeRoute(r, provider, model), Inputs: request.Inputs,
	})
	if err != nil {
		completeModelInvocation(&invocation, managedagents.ModelInvocationStatusFailed, failedModelInvocationCode(err))
		s.recordModelInvocation(r, invocation)
		s.writeModelRuntimeError(w, r, err)
		return
	}
	embeddings := make([]modelRuntimeEmbedding, 0, len(response.Embeddings))
	for index, vector := range response.Embeddings {
		if len(vector) != model.Capabilities.Dimensions {
			invocation.InputTokens = response.Usage.InputTokens
			invocation.TotalTokens = response.Usage.TotalTokens
			invocation.OutputItems = int64(len(response.Embeddings))
			completeModelInvocation(&invocation, managedagents.ModelInvocationStatusFailed, "invalid_provider_response")
			s.recordModelInvocation(r, invocation)
			writeV2Error(w, requestIDFromRequest(r), http.StatusBadGateway, "model_provider_error", "embedding dimensions do not match the registered model", false, map[string]any{"expected_dimensions": model.Capabilities.Dimensions, "actual_dimensions": len(vector)})
			return
		}
		embeddings = append(embeddings, modelRuntimeEmbedding{Index: index, Embedding: vector})
	}
	invocation.InputTokens = response.Usage.InputTokens
	invocation.OutputTokens = response.Usage.OutputTokens
	invocation.TotalTokens = response.Usage.TotalTokens
	invocation.CachedInputTokens = response.Usage.CachedInputTokens
	invocation.ReasoningTokens = response.Usage.ReasoningTokens
	invocation.OutputItems = int64(len(embeddings))
	completeModelInvocation(&invocation, managedagents.ModelInvocationStatusCompleted, "")
	s.recordModelInvocation(r, invocation)
	writeJSON(w, http.StatusOK, modelRuntimeEmbeddingResponse{
		Embeddings: embeddings, ProviderID: provider.ID, Model: model.Model,
		Dimensions: model.Capabilities.Dimensions, Usage: response.Usage,
	})
}

func (s *Server) rerankModelRuntimeDocuments(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, modelRuntimeMaxRequestBytes)
	var request modelRuntimeRerankRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	provider, model, err := s.resolveModelRuntimeCapability(request.ProviderID, request.Model, managedagents.LLMModelCapabilityReranker)
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if !modelRuntimeRerankProtocolSupported(model.Capabilities.Protocol) {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "unsupported_model_protocol", "the selected reranker protocol is not supported", false, map[string]any{"protocol": model.Capabilities.Protocol})
		return
	}
	if strings.TrimSpace(request.Query) == "" {
		writeV2ManagedError(w, r, fmt.Errorf("%w: query is required", managedagents.ErrInvalid))
		return
	}
	if len(request.Documents) == 0 || len(request.Documents) > model.Capabilities.MaxCandidates {
		writeV2ManagedError(w, r, fmt.Errorf("%w: documents must contain between 1 and %d items", managedagents.ErrInvalid, model.Capabilities.MaxCandidates))
		return
	}
	for index, document := range request.Documents {
		if strings.TrimSpace(document) == "" {
			writeV2ManagedError(w, r, fmt.Errorf("%w: documents[%d] is required", managedagents.ErrInvalid, index))
			return
		}
	}
	topN := request.TopN
	if topN == 0 {
		topN = len(request.Documents)
	}
	if topN < 1 || topN > len(request.Documents) {
		writeV2ManagedError(w, r, fmt.Errorf("%w: top_n must be between 1 and the number of documents", managedagents.ErrInvalid))
		return
	}

	startedAt := time.Now().UTC()
	invocation := s.newModelInvocationInput(r, provider, model, managedagents.ModelInvocationCapabilityRerank, startedAt)
	invocation.InputItems = int64(len(request.Documents))
	invocation.InputCharacters = int64(utf8.RuneCountInString(request.Query)) + stringsCharacterCount(request.Documents)
	release, admitted := s.admitModelInvocationHTTP(w, r, &invocation)
	if !admitted {
		return
	}
	defer release()
	results, err := s.modelExecutor().Rerank(r.Context(), modelruntime.RerankRequest{
		Route: s.modelRuntimeRoute(r, provider, model), Query: request.Query, Documents: request.Documents, TopN: topN,
	})
	if err != nil {
		completeModelInvocation(&invocation, managedagents.ModelInvocationStatusFailed, failedModelInvocationCode(err))
		s.recordModelInvocation(r, invocation)
		s.writeModelRuntimeError(w, r, err)
		return
	}
	invocation.OutputItems = int64(len(results))
	completeModelInvocation(&invocation, managedagents.ModelInvocationStatusCompleted, "")
	s.recordModelInvocation(r, invocation)
	writeJSON(w, http.StatusOK, modelRuntimeRerankResponse{Results: results, ProviderID: provider.ID, Model: model.Model})
}

func (s *Server) resolveModelRuntimeCapability(providerID string, modelName string, capabilityType string) (managedagents.LLMProvider, managedagents.LLMModel, error) {
	providerID = strings.TrimSpace(providerID)
	modelName = strings.TrimSpace(modelName)
	if (providerID == "") != (modelName == "") {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, fmt.Errorf("%w: provider_id and model must be specified together", managedagents.ErrInvalid)
	}

	var model managedagents.LLMModel
	if providerID == "" {
		models, err := s.store.ListLLMModels("")
		if err != nil {
			return managedagents.LLMProvider{}, managedagents.LLMModel{}, err
		}
		for _, candidate := range models {
			isDefault := capabilityType == managedagents.LLMModelCapabilityEmbedding && candidate.IsDefaultEmbedding ||
				capabilityType == managedagents.LLMModelCapabilityReranker && candidate.IsDefaultReranker
			if candidate.CapabilityType == capabilityType && isDefault {
				model = candidate
				break
			}
		}
		if model.Model == "" {
			return managedagents.LLMProvider{}, managedagents.LLMModel{}, fmt.Errorf("%w: provider_id and model are required because no default %s model is configured", managedagents.ErrInvalid, capabilityType)
		}
		providerID = model.ProviderID
	} else {
		var exists bool
		var err error
		model, exists, err = s.findLLMModel(providerID, modelName)
		if err != nil {
			return managedagents.LLMProvider{}, managedagents.LLMModel{}, err
		}
		if !exists {
			return managedagents.LLMProvider{}, managedagents.LLMModel{}, managedagents.ErrNotFound
		}
	}
	provider, err := s.store.GetLLMProvider(providerID)
	if err != nil {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, err
	}
	if !provider.Enabled {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, fmt.Errorf("%w: the selected model provider is disabled", managedagents.ErrConflict)
	}
	if model.CapabilityType != capabilityType {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, fmt.Errorf("%w: the selected model does not support %s", managedagents.ErrInvalid, capabilityType)
	}
	return provider, model, nil
}

func (s *Server) modelRuntimeRoute(r *http.Request, provider managedagents.LLMProvider, model managedagents.LLMModel) modelruntime.Route {
	return modelruntime.Route{
		ProviderID: provider.ID, ProviderType: llm.ResolveProviderType(provider.ID, provider.ProviderType),
		BaseURL: provider.BaseURL, APIKey: s.resolveLLMAPIKey(r.Context(), requestWorkspaceID(r, ""), provider.APIKeyEnv),
		Model: model.Model, Protocol: model.Capabilities.Protocol,
	}
}

func (s *Server) modelExecutor() modelruntime.Executor {
	if s.modelRuntimeExecutor == nil {
		return modelruntime.LocalExecutor{}
	}
	return s.modelRuntimeExecutor
}

func modelRuntimeEmbeddingProtocolSupported(protocol string) bool {
	switch strings.TrimSpace(protocol) {
	case llm.EmbeddingProtocolOpenAI, llm.EmbeddingProtocolTEI, llm.EmbeddingProtocolOllama:
		return true
	default:
		return false
	}
}

func modelRuntimeRerankProtocolSupported(protocol string) bool {
	switch strings.TrimSpace(protocol) {
	case llm.RerankProtocolJina, llm.RerankProtocolCohere, llm.RerankProtocolVLLM:
		return true
	default:
		return false
	}
}

func (s *Server) validateModelRuntimeRequest(request modelRuntimeGenerateRequest) (string, string, []llm.Message, error) {
	providerID := strings.TrimSpace(request.ProviderID)
	modelName := strings.TrimSpace(request.Model)
	if providerID == "" {
		providerID = strings.TrimSpace(s.defaultLLMProvider)
	}
	if modelName == "" {
		modelName = strings.TrimSpace(s.defaultLLMModel)
	}
	if providerID == "" || modelName == "" {
		return "", "", nil, fmt.Errorf("%w: provider_id and model must be configured explicitly or as Platform defaults", managedagents.ErrInvalid)
	}
	if len(request.Messages) == 0 {
		return "", "", nil, fmt.Errorf("%w: messages must contain at least one message", managedagents.ErrInvalid)
	}
	if request.MaxOutputTokens < 0 || request.MaxOutputTokens > modelRuntimeMaxOutputTokens {
		return "", "", nil, fmt.Errorf("%w: max_output_tokens must be between 1 and %d when specified", managedagents.ErrInvalid, modelRuntimeMaxOutputTokens)
	}
	messages := make([]llm.Message, 0, len(request.Messages))
	for index, message := range request.Messages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "system", "user", "assistant":
		default:
			return "", "", nil, fmt.Errorf("%w: messages[%d].role must be system, user, or assistant", managedagents.ErrInvalid, index)
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			return "", "", nil, fmt.Errorf("%w: messages[%d].content is required", managedagents.ErrInvalid, index)
		}
		messages = append(messages, llm.Message{Role: role, Content: []llm.ContentPart{{Type: "text", Text: content}}})
	}
	return providerID, modelName, messages, nil
}

func modelRuntimeMessageText(message llm.Message) string {
	parts := make([]string, 0, len(message.Content))
	for _, part := range message.Content {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func (s *Server) writeModelRuntimeError(w http.ResponseWriter, r *http.Request, err error) {
	var providerError *llm.ProviderError
	if errors.As(err, &providerError) {
		status := providerError.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		if providerError.Class == llm.ErrorClassTimeout {
			status = http.StatusGatewayTimeout
		}
		writeV2Error(w, requestIDFromRequest(r), status, "model_provider_error", providerError.Error(), providerError.Retryable, map[string]any{"class": providerError.Class})
		return
	}
	s.logger.Error("model runtime generation failed", "error", err)
	writeV2Error(w, requestIDFromRequest(r), http.StatusBadGateway, "model_provider_error", "model provider request failed", true, nil)
}
