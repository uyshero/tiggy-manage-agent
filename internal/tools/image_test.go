package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
)

type imageModelClientStub struct {
	chatRequest llm.Request
}

func (s *imageModelClientStub) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	s.chatRequest = request
	return llm.Response{
		Message:      llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "visible invoice total: 42"}}},
		Usage:        llm.Usage{InputTokens: 5, OutputTokens: 4, TotalTokens: 9},
		FinishReason: "stop",
	}, nil
}

type streamingImageModelClientStub struct {
	chatRequest   llm.Request
	generateCalls int
	streamCalls   int
}

func (s *streamingImageModelClientStub) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	s.chatRequest = request
	s.generateCalls++
	return llm.Response{}, nil
}

func (s *streamingImageModelClientStub) GenerateStream(_ context.Context, request llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	s.chatRequest = request
	s.streamCalls++
	for _, delta := range []llm.Delta{
		{Kind: llm.DeltaKindReasoning, Text: "checking layout"},
		{Kind: llm.DeltaKindText, Text: "visible invoice total: 42"},
	} {
		if err := onDelta(delta); err != nil {
			return llm.Response{}, err
		}
	}
	return llm.Response{
		Message:      llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "visible invoice total: 42"}}},
		Usage:        llm.Usage{InputTokens: 5, OutputTokens: 4, TotalTokens: 9},
		FinishReason: "stop",
	}, nil
}

type continuingImageModelClientStub struct {
	requests  []llm.Request
	responses []llm.Response
}

func (s *continuingImageModelClientStub) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	s.requests = append(s.requests, request)
	index := len(s.requests) - 1
	if index >= len(s.responses) {
		return s.responses[len(s.responses)-1], nil
	}
	return s.responses[index], nil
}

func TestImageRuntimeManifestExposesDefaultGenerationAndVisionTools(t *testing.T) {
	manifest := (ImageRuntime{}).Manifest()
	if manifest.Identifier != ImageIdentifier || len(manifest.API) != 2 || manifest.Executors[0] != ExecutorServer {
		t.Fatalf("unexpected image manifest: %#v", manifest)
	}
	if manifest.API[0].Name != "generate" || manifest.API[0].ApprovalPolicy != ApprovalPolicyConditional || manifest.API[1].Name != "analyze" || manifest.API[1].ApprovalPolicy != ApprovalPolicyNever {
		t.Fatalf("unexpected image APIs: %#v", manifest.API)
	}
}

func TestImageRuntimeGenerateUsesShuYouAsyncAPIAndExportsImage(t *testing.T) {
	generatedPNG := []byte("\x89PNG\r\n\x1a\ngenerated")
	var createRequest shuyouCreateRequest
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer sandbox-secret" && request.URL.Path != "/generated.png" {
			t.Fatalf("unexpected authorization header: %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/v1/predictions":
			if request.Method != http.MethodPost {
				t.Fatalf("unexpected create method: %s", request.Method)
			}
			if err := json.NewDecoder(request.Body).Decode(&createRequest); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{"task_id": "task-123", "task_status": "processing"}})
		case "/v1/predictions/task-123":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{
				"task_id": "task-123", "task_status": "success",
				"output": []map[string]any{{"type": "image", "image": server.URL + "/generated.png"}},
			}})
		case "/generated.png":
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(generatedPNG)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	runtime := ImageRuntime{httpClient: server.Client(), predictionEndpoint: server.URL + "/v1/predictions", pollInterval: time.Millisecond}
	result, err := runtime.Execute(context.Background(), Call{
		ID: "call_generate", Identifier: ImageIdentifier, APIName: "generate",
		Arguments: json.RawMessage(`{"prompt":"create a product photo","use_case":"product-mockup","quality":"high","resolution":"2K","aspect_ratio":"16:9","count":1}`),
	}, ExecutionContext{Environment: map[string]string{shuyouAPIKeyEnvironment: "sandbox-secret"}})
	if err != nil || result.Error != nil {
		t.Fatalf("generate result=%+v err=%v", result, err)
	}
	if createRequest.Model != shuyouImageModel || createRequest.Function != "image" || createRequest.Input.Quality != "high" || createRequest.Input.Resolution != "2K" || createRequest.Input.AspectRatio != "16:9" || createRequest.Input.Prompt != "Use case: product-mockup\nPrimary request: create a product photo" {
		t.Fatalf("unexpected ShuYou request: %#v", createRequest)
	}
	if len(result.ExportedFiles) != 1 || result.ExportedFiles[0].Name != "generated-image-01.png" || string(result.ExportedFiles[0].Content) != string(generatedPNG) {
		t.Fatalf("unexpected image export: %#v", result.ExportedFiles)
	}
	if !strings.Contains(string(result.State), `"task_id":"task-123"`) || !strings.Contains(string(result.State), `"model":"gpt-image-2"`) {
		t.Fatalf("unexpected generation state: %s", result.State)
	}
}

func TestImageRuntimeGenerateReturnsShuYouFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/predictions":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{"task_id": "task-failed"}})
		case "/v1/predictions/task-failed":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{
				"task_status": "failed", "error": "content rejected",
			}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	runtime := ImageRuntime{httpClient: server.Client(), predictionEndpoint: server.URL + "/v1/predictions", pollInterval: time.Millisecond}
	result, err := runtime.Execute(context.Background(), Call{
		Identifier: ImageIdentifier, APIName: "generate",
		Arguments: json.RawMessage(`{"prompt":"create an illustration","use_case":"illustration-story"}`),
	}, ExecutionContext{Environment: map[string]string{shuyouAPIKeyEnvironment: "sandbox-secret"}})
	if err != nil || result.Error == nil || result.Error.Type != "image_generation_failed" || !strings.Contains(result.Error.Message, "content rejected") {
		t.Fatalf("unexpected failed generation result=%+v err=%v", result, err)
	}
}

func TestExtractShuYouImageURLsSupportsDocumentedOutputShapes(t *testing.T) {
	urls, err := extractShuYouImageURLs(json.RawMessage(`[
		{"type":"image","image":"https://cdn.example/one.png"},
		{"url":"https://cdn.example/two.webp"},
		"https://cdn.example/one.png"
	]`))
	if err != nil || len(urls) != 2 || urls[0] != "https://cdn.example/one.png" || urls[1] != "https://cdn.example/two.webp" {
		t.Fatalf("unexpected extracted URLs=%#v err=%v", urls, err)
	}
}

func TestBuildImageGenerationPromptUsesLatestImagegenStructure(t *testing.T) {
	prompt, err := buildImageGenerationPrompt(imageGenerateRequest{
		UseCase: "compositing", AssetType: "campaign image", Prompt: "place the subject into the base scene",
		InputImages: []imagePromptInput{
			{Path: "base.png", Role: "base scene", Description: "keep the framing"},
			{Path: "subject.png", Role: "subject to insert"},
		},
		Composition: "match the base framing", TextVerbatim: "Yours to Create.",
		Constraints: "change only the inserted subject; keep the base scene unchanged", Avoid: "extra people",
	})
	if err != nil {
		t.Fatalf("buildImageGenerationPrompt() error = %v", err)
	}
	want := strings.Join([]string{
		"Use case: compositing",
		"Asset type: campaign image",
		"Primary request: place the subject into the base scene",
		"Input images: Image 1: base scene (keep the framing); Image 2: subject to insert",
		"Composition/framing: match the base framing",
		`Text (verbatim): "Yours to Create."`,
		"Constraints: change only the inserted subject; keep the base scene unchanged; render the quoted text verbatim with no extra characters",
		"Avoid: extra people",
	}, "\n")
	if prompt != want {
		t.Fatalf("unexpected structured prompt:\n%s\nwant:\n%s", prompt, want)
	}
}

func TestBuildImageGenerationPromptRequiresEditInvariantsAndLabeledInputs(t *testing.T) {
	_, err := buildImageGenerationPrompt(imageGenerateRequest{
		UseCase: "precise-object-edit", Prompt: "replace the chairs",
		InputImages: []imagePromptInput{{Path: "room.png", Role: "edit target"}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires constraints") {
		t.Fatalf("expected edit invariant error, got %v", err)
	}
	_, err = buildImageGenerationPrompt(imageGenerateRequest{
		UseCase: "style-transfer", Prompt: "apply this style", Constraints: "keep the subject unchanged",
		InputImages: []imagePromptInput{{Path: "style.png"}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires role and exactly one of path or url") {
		t.Fatalf("expected input role error, got %v", err)
	}
}

func TestImageRuntimeAnalyzeUsesConfiguredVisionModel(t *testing.T) {
	client := &imageModelClientStub{}
	runtime := ImageRuntime{
		Client: client,
		Vision: ImageModelRoute{
			Provider: "vision-provider", ProviderType: llm.ProviderOpenAICompatible, Model: "vision-configured",
			BaseURL: "https://vision.example/v1", APIKey: "vision-secret",
		},
	}
	result, err := runtime.Execute(context.Background(), Call{
		ID: "call_analyze", Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"prompt":"read the total","image_urls":["data:image/png;base64,cG5n"],"detail":"high"}`),
	}, ExecutionContext{})
	if err != nil || result.Error != nil || result.Content != "visible invoice total: 42" {
		t.Fatalf("analyze result=%+v err=%v", result, err)
	}
	if client.chatRequest.Provider != "vision-provider" || client.chatRequest.Model != "vision-configured" || client.chatRequest.APIKey != "vision-secret" {
		t.Fatalf("unexpected configured vision request: %#v", client.chatRequest)
	}
	parts := client.chatRequest.Messages[0].Content
	if len(parts) != 2 || parts[1].ImageURL == nil || parts[1].ImageURL.Detail != "high" {
		t.Fatalf("unexpected vision content: %#v", parts)
	}
}

func TestImageRuntimeAnalyzeStreamsVisionProgress(t *testing.T) {
	client := &streamingImageModelClientStub{}
	progress := make([]ToolProgress, 0, 3)
	runtime := ImageRuntime{
		Client: client,
		Vision: ImageModelRoute{Provider: "vision-provider", ProviderType: llm.ProviderOpenAICompatible, Model: "vision-configured"},
	}
	result, err := runtime.Execute(t.Context(), Call{
		ID: "call_stream", Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"image_urls":["data:image/png;base64,cG5n"]}`),
	}, ExecutionContext{Progress: func(_ context.Context, update ToolProgress) {
		progress = append(progress, update)
	}})
	if err != nil || result.Error != nil || result.Content != "visible invoice total: 42" {
		t.Fatalf("streaming analyze result=%+v err=%v", result, err)
	}
	if client.streamCalls != 1 || client.generateCalls != 0 {
		t.Fatalf("expected streaming client path, stream=%d generate=%d", client.streamCalls, client.generateCalls)
	}
	if len(progress) < 2 || progress[0].Stage != "analyzing" || progress[1].Stage != "responding" ||
		!strings.Contains(progress[1].Message, "已接收") {
		t.Fatalf("unexpected streaming progress: %#v", progress)
	}
}

func TestImageRuntimeAnalyzeRequiresSingleImageForPreciseLayout(t *testing.T) {
	runtime := ImageRuntime{Client: &imageModelClientStub{}, Vision: ImageModelRoute{Provider: "vision", Model: "vision-model"}}
	result, err := runtime.Execute(t.Context(), Call{
		Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"prompt":"给出所有元素的精确坐标和边框","image_urls":["data:image/png;base64,b25l","data:image/png;base64,dHdv"]}`),
	}, ExecutionContext{})
	if err != nil || result.Error == nil || result.Error.Type != "image_analysis_split_required" ||
		!strings.Contains(result.Error.Message, "precise_layout") {
		t.Fatalf("unexpected precise-layout split result=%+v err=%v", result, err)
	}
}

func TestImageRuntimeAnalyzeContinuesTruncatedResponse(t *testing.T) {
	client := &continuingImageModelClientStub{responses: []llm.Response{
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "first section"}}},
			Usage:   llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}, FinishReason: "length",
		},
		{
			Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "remaining section " + imageAnalysisCompleteTag}}},
			Usage:   llm.Usage{InputTokens: 30, OutputTokens: 40, TotalTokens: 70}, FinishReason: "stop",
		},
	}}
	runtime := ImageRuntime{Client: client, Vision: ImageModelRoute{Provider: "vision", Model: "vision-model"}}
	result, err := runtime.Execute(t.Context(), Call{
		Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"analysis_mode":"precise_layout","max_output_tokens":30000,"prompt":"map the layout","image_urls":["data:image/png;base64,cG5n"]}`),
	}, ExecutionContext{})
	if err != nil || result.Error != nil || result.Content != "first section\nremaining section" {
		t.Fatalf("continued analyze result=%+v err=%v", result, err)
	}
	if len(client.requests) != 2 || client.requests[0].MaxOutputTokens != 30000 || client.requests[1].MaxOutputTokens != 30000 || len(client.requests[1].Messages) != 3 {
		t.Fatalf("unexpected continuation requests: %#v", client.requests)
	}
	state := string(result.State)
	if !strings.Contains(state, `"complete":true`) || !strings.Contains(state, `"segments":2`) ||
		!strings.Contains(state, `"input_tokens":40`) || !strings.Contains(state, `"output_tokens":60`) {
		t.Fatalf("unexpected continuation state: %s", state)
	}
}

func TestImageRuntimeAnalyzeRejectsStillIncompleteResponse(t *testing.T) {
	client := &continuingImageModelClientStub{responses: []llm.Response{{
		Message:      llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "partial"}}},
		FinishReason: "length",
	}}}
	runtime := ImageRuntime{Client: client, Vision: ImageModelRoute{Provider: "vision", Model: "vision-model"}}
	result, err := runtime.Execute(t.Context(), Call{
		Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"analysis_mode":"precise_layout","prompt":"map the layout","image_urls":["data:image/png;base64,cG5n"]}`),
	}, ExecutionContext{})
	if err != nil || result.Error == nil || result.Error.Type != "incomplete_vision_analysis" || len(client.requests) != maxAnalysisContinuations+1 {
		t.Fatalf("unexpected incomplete result=%+v requests=%d err=%v", result, len(client.requests), err)
	}
	if !strings.Contains(result.Error.Message, "21 output characters") {
		t.Fatalf("expected non-streaming output character count, got %q", result.Error.Message)
	}
}

func TestImageRuntimeAnalyzeDoesNotTreatContentFilterAsComplete(t *testing.T) {
	client := &continuingImageModelClientStub{responses: []llm.Response{{
		Message:      llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "partial " + imageAnalysisCompleteTag}}},
		FinishReason: "content_filter",
	}}}
	runtime := ImageRuntime{Client: client, Vision: ImageModelRoute{Provider: "vision", Model: "vision-model"}}
	result, err := runtime.Execute(t.Context(), Call{
		Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"prompt":"analyze","image_urls":["data:image/png;base64,cG5n"]}`),
	}, ExecutionContext{})
	if err != nil || result.Error == nil || result.Error.Type != "incomplete_vision_analysis" || len(client.requests) != 1 {
		t.Fatalf("unexpected filtered result=%+v requests=%d err=%v", result, len(client.requests), err)
	}
	if !strings.Contains(result.Error.Message, `finish_reason="content_filter"`) {
		t.Fatalf("expected content_filter reason in error, got %q", result.Error.Message)
	}
}

func TestImageRuntimeAnalyzeReadsWorkspacePathThroughProvider(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "source.png")
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\nsource"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &imageModelClientStub{}
	runtime := ImageRuntime{Client: client, Vision: ImageModelRoute{Provider: "vision", Model: "vision-model"}}
	result, err := runtime.Execute(context.Background(), Call{
		Identifier: ImageIdentifier, APIName: "analyze",
		Arguments: json.RawMessage(`{"paths":[` + strconv.Quote(imagePath) + `]}`),
	}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil || result.Error != nil {
		t.Fatalf("analyze workspace image result=%+v err=%v", result, err)
	}
	parts := client.chatRequest.Messages[0].Content
	if len(parts) != 2 || parts[1].ImageURL == nil || !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("workspace image was not converted to a vision input: %#v", parts)
	}
}

func TestImageRuntimeReturnsConfigurationAndURLValidationErrors(t *testing.T) {
	runtime := ImageRuntime{Client: &imageModelClientStub{}}
	missing, err := runtime.Execute(context.Background(), Call{
		Identifier: ImageIdentifier, APIName: "generate", Arguments: json.RawMessage(`{"prompt":"image","use_case":"illustration-story"}`),
	}, ExecutionContext{})
	if err != nil || missing.Error == nil || missing.Error.Type != "shuyou_api_key_not_configured" {
		t.Fatalf("unexpected missing-key result=%+v err=%v", missing, err)
	}

	runtime.Vision = ImageModelRoute{Provider: "vision", Model: "vision-model"}
	invalid, err := runtime.Execute(context.Background(), Call{
		Identifier: ImageIdentifier, APIName: "analyze", Arguments: json.RawMessage(`{"image_urls":["http://127.0.0.1/private.png"]}`),
	}, ExecutionContext{})
	if err != nil || invalid.Error == nil || invalid.Error.Type != "invalid_image_url" || !strings.Contains(invalid.Error.Message, "HTTPS") {
		t.Fatalf("unexpected URL validation result=%+v err=%v", invalid, err)
	}
}
