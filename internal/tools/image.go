package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
)

const (
	ImageIdentifier          = "image"
	shuyouAPIKeyEnvironment  = "SHUYOU_API_KEY"
	shuyouImageModel         = "gpt-image-2"
	shuyouPredictionEndpoint = "https://coder.shuyou.ai/v1/predictions"
	maxImageInputBytes       = 20 << 20
	maxImageDownloadBytes    = 50 << 20
	maxImageInputs           = 16
	maxGeneratedImages       = 10
	maxShuYouResponseBytes   = 2 << 20
	defaultImagePollInterval = 5 * time.Second
	defaultImageTimeout      = 10 * time.Minute
)

type ImageModelRoute struct {
	Provider     string
	ProviderType string
	Model        string
	BaseURL      string
	APIKey       string
}

type ImageRuntime struct {
	Client             llm.Client
	Vision             ImageModelRoute
	httpClient         *http.Client
	predictionEndpoint string
	pollInterval       time.Duration
}

type imageGenerateRequest struct {
	Prompt            string             `json:"prompt"`
	UseCase           string             `json:"use_case"`
	AssetType         string             `json:"asset_type,omitempty"`
	InputImages       []imagePromptInput `json:"input_images,omitempty"`
	SceneBackdrop     string             `json:"scene_backdrop,omitempty"`
	Subject           string             `json:"subject,omitempty"`
	StyleMedium       string             `json:"style_medium,omitempty"`
	Composition       string             `json:"composition_framing,omitempty"`
	LightingMood      string             `json:"lighting_mood,omitempty"`
	ColorPalette      string             `json:"color_palette,omitempty"`
	MaterialsTextures string             `json:"materials_textures,omitempty"`
	TextVerbatim      string             `json:"text_verbatim,omitempty"`
	Constraints       string             `json:"constraints,omitempty"`
	Avoid             string             `json:"avoid,omitempty"`
	MaskPath          string             `json:"mask_path,omitempty"`
	MaskURL           string             `json:"mask_url,omitempty"`
	Size              string             `json:"size,omitempty"`
	Resolution        string             `json:"resolution,omitempty"`
	Quality           string             `json:"quality,omitempty"`
	AspectRatio       string             `json:"aspect_ratio,omitempty"`
	OutputFormat      string             `json:"output_format,omitempty"`
	OutputCompression *int               `json:"output_compression,omitempty"`
	Count             int                `json:"count,omitempty"`
	TimeoutSeconds    int                `json:"timeout_seconds,omitempty"`
}

type imagePromptInput struct {
	Path        string `json:"path"`
	URL         string `json:"url"`
	Role        string `json:"role"`
	Description string `json:"description,omitempty"`
}

type workspaceImageData struct {
	Name        string
	ContentType string
	Content     []byte
}

type shuyouImageInput struct {
	Prompt            string   `json:"prompt"`
	Size              string   `json:"size,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
	Quality           string   `json:"quality,omitempty"`
	NumImages         int      `json:"num_images"`
	AspectRatio       string   `json:"aspect_ratio,omitempty"`
	OutputFormat      string   `json:"output_format,omitempty"`
	OutputCompression *int     `json:"output_compression,omitempty"`
	ImageURLs         []string `json:"image_urls,omitempty"`
	MaskURL           string   `json:"mask_url,omitempty"`
}

type shuyouCreateRequest struct {
	Model    string           `json:"model"`
	Function string           `json:"function"`
	Input    shuyouImageInput `json:"input"`
}

type shuyouPredictionResponse struct {
	Data struct {
		TaskID     string          `json:"task_id"`
		TaskStatus string          `json:"task_status"`
		Status     string          `json:"status"`
		Output     json.RawMessage `json:"output"`
		Error      any             `json:"error"`
	} `json:"data"`
}

type imageAnalyzeRequest struct {
	Prompt    string   `json:"prompt,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	ImageURLs []string `json:"image_urls,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

var imageGenerateUseCases = map[string]bool{
	"photorealistic-natural": true,
	"product-mockup":         true,
	"ui-mockup":              true,
	"infographic-diagram":    true,
	"scientific-educational": true,
	"ads-marketing":          true,
	"productivity-visual":    true,
	"logo-brand":             true,
	"illustration-story":     true,
	"stylized-concept":       true,
	"historical-scene":       true,
}

var imageEditUseCases = map[string]bool{
	"text-localization":     true,
	"identity-preserve":     true,
	"precise-object-edit":   true,
	"lighting-weather":      true,
	"background-extraction": true,
	"style-transfer":        true,
	"compositing":           true,
	"sketch-to-render":      true,
}

func (ImageRuntime) Manifest() Manifest {
	runtimePolicy := &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto}
	return Manifest{
		Identifier: ImageIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Image",
			Description: "Generate and edit raster images with ShuYou gpt-image-2, or analyze them with the configured vision model.",
		},
		SystemRole: "When the user requests image generation or editing, you MUST call the exact function image_generate in the same turn. Never merely promise to create an image, claim it was created before the tool result, or defer the call to a later message. Use image_analyze when visual inspection is needed. For image_generate, classify the request with the imagegen taxonomy, preserve a detailed user prompt without adding creative requirements, label every input image by index and role, quote exact in-image text verbatim, and state edit invariants as 'change only X; keep Y unchanged'. Generation always uses ShuYou gpt-image-2 and SHUYOU_API_KEY from the sandbox environment. Generated files are returned as durable artifacts.",
		Executors:  []string{ExecutorServer},
		API: []API{
			{
				Name:        "generate",
				Description: "Generate or edit a raster image with the hard-coded ShuYou gpt-image-2 async API and the latest imagegen structured brief. SHUYOU_API_KEY is read from the sandbox environment, and downloaded outputs are saved as artifacts.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"prompt":{"type":"string","minLength":1,"description":"The user's primary request. Preserve specific prompts; only add tasteful detail through the optional structured fields when it materially improves a generic prompt."},
						"use_case":{"type":"string","enum":["photorealistic-natural","product-mockup","ui-mockup","infographic-diagram","scientific-educational","ads-marketing","productivity-visual","logo-brand","illustration-story","stylized-concept","historical-scene","text-localization","identity-preserve","precise-object-edit","lighting-weather","background-extraction","style-transfer","compositing","sketch-to-render"]},
						"asset_type":{"type":"string","description":"Where the asset will be used, such as landing page hero, product photo, mobile screen, or game texture."},
						"input_images":{"type":"array","maxItems":16,"description":"Labeled reference or edit images. Supply exactly one of path (workspace image, sent as a data URL) or url (public HTTPS image URL).","items":{"type":"object","properties":{"path":{"type":"string","minLength":1},"url":{"type":"string","minLength":1},"role":{"type":"string","minLength":1,"description":"For example: edit target, reference image, style reference, or compositing input."},"description":{"type":"string"}},"required":["role"],"oneOf":[{"required":["path"]},{"required":["url"]}],"additionalProperties":false}},
						"scene_backdrop":{"type":"string"},
						"subject":{"type":"string"},
						"style_medium":{"type":"string"},
						"composition_framing":{"type":"string"},
						"lighting_mood":{"type":"string"},
						"color_palette":{"type":"string"},
						"materials_textures":{"type":"string"},
						"text_verbatim":{"type":"string","description":"Exact in-image text. It will be quoted and marked for verbatim rendering with no extra characters."},
						"constraints":{"type":"string","description":"Must-keep and must-avoid invariants. Required for edit use cases; say change only X and keep Y unchanged."},
						"avoid":{"type":"string","description":"Negative constraints without inventing unrelated requirements."},
						"mask_path":{"type":"string","description":"Optional workspace PNG mask path, sent to ShuYou as a data URL."},
						"mask_url":{"type":"string","description":"Optional public HTTPS mask URL. Do not use together with mask_path."},
						"size":{"type":"string","description":"Output dimensions such as auto, 1024x1024, or 1920x1080."},
						"resolution":{"type":"string","enum":["1K","2K","4K"]},
						"quality":{"type":"string","enum":["auto","low","medium","high"]},
						"aspect_ratio":{"type":"string","enum":["1:1","2:3","3:2","9:16","16:9"]},
						"output_format":{"type":"string","enum":["png","jpeg","webp"]},
						"output_compression":{"type":"integer","minimum":0,"maximum":100},
						"count":{"type":"integer","minimum":1,"maximum":10},
						"timeout_seconds":{"type":"integer","minimum":30,"maximum":600,"description":"Total async task timeout; defaults to 600 seconds."}
					},
					"required":["prompt","use_case"],
					"additionalProperties":false
				}`),
				Risk: ToolRiskWrite, ApprovalPolicy: ApprovalPolicyConditional, ApprovalReason: InterventionReasonFilesystemWrite,
				Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:        "analyze",
				Description: "Analyze one or more workspace images or data/HTTPS image URLs with the configured default vision model.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"prompt":{"type":"string","description":"Question or analysis instructions for the images."},
						"paths":{"type":"array","items":{"type":"string","minLength":1},"maxItems":16,"description":"Workspace image paths to inspect."},
						"image_urls":{"type":"array","items":{"type":"string","minLength":1},"maxItems":16,"description":"Image data URLs or HTTPS URLs to inspect."},
						"detail":{"type":"string","enum":["auto","low","high"]}
					},
					"additionalProperties":false
				}`),
				Risk: ToolRiskRead, ApprovalPolicy: ApprovalPolicyNever,
				Runtime: runtimePolicy, Implementation: ToolImplementationServerBuiltin,
			},
		},
	}
}

func (r ImageRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	switch call.APIName {
	case "generate":
		return r.generate(ctx, call, executionContext)
	case "analyze":
		return r.analyze(ctx, call, executionContext)
	default:
		return failedResult(call, "unsupported_api", fmt.Sprintf("unsupported image api %q", call.APIName)), nil
	}
}

func (r ImageRuntime) generate(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	var input imageGenerateRequest
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return failedResult(call, "invalid_arguments", err.Error()), nil
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		return failedResult(call, "invalid_arguments", "prompt is required"), nil
	}
	finalPrompt, promptErr := buildImageGenerationPrompt(input)
	if promptErr != nil {
		return failedResult(call, "invalid_image_prompt", promptErr.Error()), nil
	}
	if len(input.InputImages) > maxImageInputs {
		return failedResult(call, "invalid_arguments", fmt.Sprintf("input_images must contain at most %d images", maxImageInputs)), nil
	}
	if input.Count == 0 {
		input.Count = 1
	}
	if input.Count < 1 || input.Count > maxGeneratedImages {
		return failedResult(call, "invalid_arguments", fmt.Sprintf("count must be between 1 and %d", maxGeneratedImages)), nil
	}
	apiKey := strings.TrimSpace(executionContext.Environment[shuyouAPIKeyEnvironment])
	if apiKey == "" {
		return failedResult(call, "shuyou_api_key_not_configured", shuyouAPIKeyEnvironment+" is not configured in the sandbox environment."), nil
	}
	apiKey = strings.Trim(strings.TrimSpace(apiKey), "\"'")
	if apiKey == "" {
		return failedResult(call, "shuyou_api_key_not_configured", shuyouAPIKeyEnvironment+" is empty in the sandbox environment."), nil
	}
	request := shuyouCreateRequest{
		Model: shuyouImageModel, Function: "image",
		Input: shuyouImageInput{
			Prompt: finalPrompt, Size: defaultImageOption(input.Size, "auto"), Resolution: input.Resolution,
			Quality: defaultImageOption(input.Quality, "medium"), NumImages: input.Count,
			AspectRatio: input.AspectRatio, OutputFormat: defaultImageOption(input.OutputFormat, "png"),
			OutputCompression: input.OutputCompression,
		},
	}
	for _, promptInput := range input.InputImages {
		path := strings.TrimSpace(promptInput.Path)
		imageURL := strings.TrimSpace(promptInput.URL)
		if path != "" && imageURL != "" {
			return failedResult(call, "invalid_arguments", "each input image must use exactly one of path or url"), nil
		}
		if path != "" {
			image, err := workspaceImage(ctx, executionContext, path)
			if err != nil {
				return failedResult(call, "image_input_failed", err.Error()), nil
			}
			imageURL = imageDataURL(image)
		} else if err := validateImageReferenceURL(imageURL); err != nil {
			return failedResult(call, "invalid_image_url", err.Error()), nil
		}
		request.Input.ImageURLs = append(request.Input.ImageURLs, imageURL)
	}
	maskPath := strings.TrimSpace(input.MaskPath)
	maskURL := strings.TrimSpace(input.MaskURL)
	if maskPath != "" && maskURL != "" {
		return failedResult(call, "invalid_arguments", "use only one of mask_path or mask_url"), nil
	}
	if maskPath != "" {
		mask, err := workspaceImage(ctx, executionContext, input.MaskPath)
		if err != nil {
			return failedResult(call, "image_mask_failed", err.Error()), nil
		}
		if mask.ContentType != "image/png" {
			return failedResult(call, "invalid_image_mask", "mask_path must reference a PNG image"), nil
		}
		request.Input.MaskURL = imageDataURL(mask)
	} else if maskURL != "" {
		if err := validateImageReferenceURL(maskURL); err != nil {
			return failedResult(call, "invalid_mask_url", err.Error()), nil
		}
		request.Input.MaskURL = maskURL
	}
	timeout := defaultImageTimeout
	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	generationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if executionContext.Progress != nil {
		executionContext.Progress(ctx, ToolProgress{CallID: call.ID, Tool: ModelToolName(ImageIdentifier, "generate"), Stage: "submitting", Message: "Submitting the gpt-image-2 generation task to ShuYou."})
	}
	endpoint := strings.TrimSpace(r.predictionEndpoint)
	if endpoint == "" {
		endpoint = shuyouPredictionEndpoint
	}
	response, err := r.runShuYouPrediction(generationCtx, endpoint, apiKey, request, call, executionContext)
	if err != nil {
		if generationCtx.Err() != nil {
			return failedResult(call, "image_generation_timeout", fmt.Sprintf("ShuYou image generation did not finish within %s", timeout)), nil
		}
		return failedResult(call, "image_generation_failed", err.Error()), nil
	}
	if len(response.ImageURLs) == 0 {
		return failedResult(call, "empty_image_generation", "The ShuYou task succeeded but returned no image URLs."), nil
	}
	exports := make([]ArtifactExport, 0, len(response.ImageURLs))
	for index, imageURL := range response.ImageURLs {
		image, contentType, err := r.downloadGeneratedImage(generationCtx, endpoint, imageURL)
		if err != nil {
			return failedResult(call, "image_download_failed", fmt.Sprintf("download generated image %d: %v", index+1, err)), nil
		}
		extension := imageExtension(contentType, request.Input.OutputFormat)
		exports = append(exports, ArtifactExport{
			Name: fmt.Sprintf("generated-image-%02d.%s", index+1, extension), ArtifactType: "file",
			ContentType: contentType, Content: image, Description: "Image generated by ShuYou " + shuyouImageModel,
		})
	}
	state, _ := json.Marshal(map[string]any{
		"provider": "shuyou", "model": shuyouImageModel, "task_id": response.TaskID,
		"count": len(exports), "final_prompt": finalPrompt, "original_urls": response.ImageURLs,
	})
	return ExecutionResult{
		ID: call.ID, Identifier: ImageIdentifier, APIName: "generate",
		Content: fmt.Sprintf("Generated %d image(s) with ShuYou %s task %s. The image files are attached as artifacts.", len(exports), shuyouImageModel, response.TaskID),
		State:   state, ExportedFiles: exports,
	}, nil
}

type shuyouPredictionResult struct {
	TaskID    string
	ImageURLs []string
}

func (r ImageRuntime) runShuYouPrediction(ctx context.Context, endpoint string, apiKey string, request shuyouCreateRequest, call Call, executionContext ExecutionContext) (shuyouPredictionResult, error) {
	var created shuyouPredictionResponse
	if err := r.shuyouJSONRequest(ctx, http.MethodPost, endpoint, apiKey, request, &created); err != nil {
		return shuyouPredictionResult{}, fmt.Errorf("create ShuYou prediction: %w", err)
	}
	taskID := strings.TrimSpace(created.Data.TaskID)
	if taskID == "" {
		return shuyouPredictionResult{}, fmt.Errorf("create ShuYou prediction: response did not contain data.task_id")
	}

	interval := r.pollInterval
	if interval <= 0 {
		interval = defaultImagePollInterval
	}
	pollURL := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(taskID)
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return shuyouPredictionResult{}, ctx.Err()
		case <-timer.C:
		}

		var statusResponse shuyouPredictionResponse
		if err := r.shuyouJSONRequest(ctx, http.MethodGet, pollURL, apiKey, nil, &statusResponse); err != nil {
			return shuyouPredictionResult{}, fmt.Errorf("poll ShuYou prediction %s: %w", taskID, err)
		}
		status := strings.ToLower(strings.TrimSpace(statusResponse.Data.TaskStatus))
		if status == "" {
			status = strings.ToLower(strings.TrimSpace(statusResponse.Data.Status))
		}
		if executionContext.Progress != nil {
			executionContext.Progress(ctx, ToolProgress{
				CallID: call.ID, Tool: ModelToolName(ImageIdentifier, "generate"), Stage: "polling",
				Message: fmt.Sprintf("ShuYou task %s status: %s", taskID, defaultImageOption(status, "unknown")),
			})
		}
		switch status {
		case "success":
			imageURLs, err := extractShuYouImageURLs(statusResponse.Data.Output)
			if err != nil {
				return shuyouPredictionResult{}, fmt.Errorf("decode ShuYou task %s output: %w", taskID, err)
			}
			return shuyouPredictionResult{TaskID: taskID, ImageURLs: imageURLs}, nil
		case "failed", "error", "cancelled", "canceled":
			detail := ""
			if statusResponse.Data.Error != nil {
				encoded, _ := json.Marshal(statusResponse.Data.Error)
				detail = ": " + string(encoded)
			}
			return shuyouPredictionResult{}, fmt.Errorf("ShuYou task %s ended with status %s%s", taskID, status, detail)
		}
	}
}

func (r ImageRuntime) shuyouJSONRequest(ctx context.Context, method string, target string, apiKey string, payload any, output any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := r.shuyouHTTPClient().Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxShuYouResponseBytes))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode JSON response: %w", err)
	}
	return nil
}

func (r ImageRuntime) shuyouHTTPClient() *http.Client {
	if r.httpClient != nil {
		return r.httpClient
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (r ImageRuntime) downloadGeneratedImage(ctx context.Context, endpoint string, imageURL string) ([]byte, string, error) {
	if err := validateShuYouOutputURL(imageURL, endpoint); err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}
	response, err := r.shuyouHTTPClient().Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("send download request: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && response.Request.URL != nil {
		if err := validateShuYouOutputURL(response.Request.URL.String(), endpoint); err != nil {
			return nil, "", fmt.Errorf("unsafe download redirect: %w", err)
		}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxImageDownloadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image: %w", err)
	}
	if len(content) == 0 {
		return nil, "", fmt.Errorf("downloaded image is empty")
	}
	if len(content) > maxImageDownloadBytes {
		return nil, "", fmt.Errorf("downloaded image exceeds %d bytes", maxImageDownloadBytes)
	}
	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if !supportedImageContentType(contentType) {
		contentType = http.DetectContentType(content)
	}
	if !supportedImageContentType(contentType) {
		return nil, "", fmt.Errorf("download returned unsupported content type %q", contentType)
	}
	return content, contentType, nil
}

func extractShuYouImageURLs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var output any
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, err
	}
	var values []string
	var collect func(any)
	collect = func(value any) {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				values = append(values, strings.TrimSpace(typed))
			}
		case []any:
			for _, item := range typed {
				collect(item)
			}
		case map[string]any:
			for _, key := range []string{"image", "url", "image_url", "download_url"} {
				if candidate, ok := typed[key]; ok {
					collect(candidate)
					return
				}
			}
		}
	}
	collect(output)
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique, nil
}

func validateImageReferenceURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("image URL is empty")
	}
	return validateVisionImageURL(value)
}

func validateShuYouOutputURL(value string, endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("generated image URL is invalid")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	// Tests can use an injected loopback HTTP endpoint; production remains HTTPS-only.
	configured, _ := url.Parse(endpoint)
	if parsed.Scheme == "http" && configured != nil && configured.Scheme == "http" && parsed.Host == configured.Host {
		return nil
	}
	return fmt.Errorf("generated image URL must use HTTPS")
}

func buildImageGenerationPrompt(input imageGenerateRequest) (string, error) {
	useCase := strings.TrimSpace(input.UseCase)
	if !imageGenerateUseCases[useCase] && !imageEditUseCases[useCase] {
		return "", fmt.Errorf("use_case must be one exact imagegen taxonomy slug")
	}
	if imageEditUseCases[useCase] {
		if len(input.InputImages) == 0 {
			return "", fmt.Errorf("edit use case %q requires at least one labeled input image", useCase)
		}
		if strings.TrimSpace(input.Constraints) == "" {
			return "", fmt.Errorf("edit use case %q requires constraints that state what changes and what remains unchanged", useCase)
		}
	}
	if (strings.TrimSpace(input.MaskPath) != "" || strings.TrimSpace(input.MaskURL) != "") && len(input.InputImages) == 0 {
		return "", fmt.Errorf("a mask requires at least one input image")
	}

	inputLabels := make([]string, 0, len(input.InputImages))
	for index, image := range input.InputImages {
		path := strings.TrimSpace(image.Path)
		imageURL := strings.TrimSpace(image.URL)
		if (path == "" && imageURL == "") || (path != "" && imageURL != "") || strings.TrimSpace(image.Role) == "" {
			return "", fmt.Errorf("input image %d requires role and exactly one of path or url", index+1)
		}
		label := fmt.Sprintf("Image %d: %s", index+1, strings.TrimSpace(image.Role))
		if description := strings.TrimSpace(image.Description); description != "" {
			label += " (" + description + ")"
		}
		inputLabels = append(inputLabels, label)
	}

	constraints := strings.TrimSpace(input.Constraints)
	if strings.TrimSpace(input.TextVerbatim) != "" {
		textConstraint := "render the quoted text verbatim with no extra characters"
		if constraints == "" {
			constraints = textConstraint
		} else {
			constraints += "; " + textConstraint
		}
	}
	lines := make([]string, 0, 14)
	appendPromptLine := func(label string, value string) {
		if value = strings.TrimSpace(value); value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	appendPromptLine("Use case", useCase)
	appendPromptLine("Asset type", input.AssetType)
	appendPromptLine("Primary request", input.Prompt)
	if len(inputLabels) > 0 {
		appendPromptLine("Input images", strings.Join(inputLabels, "; "))
	}
	appendPromptLine("Scene/backdrop", input.SceneBackdrop)
	appendPromptLine("Subject", input.Subject)
	appendPromptLine("Style/medium", input.StyleMedium)
	appendPromptLine("Composition/framing", input.Composition)
	appendPromptLine("Lighting/mood", input.LightingMood)
	appendPromptLine("Color palette", input.ColorPalette)
	appendPromptLine("Materials/textures", input.MaterialsTextures)
	if text := strings.TrimSpace(input.TextVerbatim); text != "" {
		appendPromptLine("Text (verbatim)", strconv.Quote(text))
	}
	appendPromptLine("Constraints", constraints)
	appendPromptLine("Avoid", input.Avoid)
	return strings.Join(lines, "\n"), nil
}

func (r ImageRuntime) analyze(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	if r.Client == nil || strings.TrimSpace(r.Vision.Provider) == "" || strings.TrimSpace(r.Vision.Model) == "" {
		return failedResult(call, "vision_model_not_configured", "No default text_image vision model is configured."), nil
	}
	var input imageAnalyzeRequest
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return failedResult(call, "invalid_arguments", err.Error()), nil
	}
	if len(input.Paths)+len(input.ImageURLs) == 0 {
		return failedResult(call, "invalid_arguments", "at least one path or image_url is required"), nil
	}
	if len(input.Paths)+len(input.ImageURLs) > maxImageInputs {
		return failedResult(call, "invalid_arguments", fmt.Sprintf("at most %d images may be analyzed", maxImageInputs)), nil
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		prompt = "Analyze these images accurately. Describe visible content, extract readable text, and report relevant details."
	}
	detail := defaultImageOption(input.Detail, "auto")
	content := []llm.ContentPart{{Type: "text", Text: prompt}}
	for _, path := range input.Paths {
		image, err := workspaceImage(ctx, executionContext, path)
		if err != nil {
			return failedResult(call, "image_input_failed", err.Error()), nil
		}
		content = append(content, llm.ContentPart{Type: "image_url", ImageURL: &llm.ImageURL{URL: imageDataURL(image), Detail: detail}})
	}
	for _, value := range input.ImageURLs {
		if err := validateVisionImageURL(value); err != nil {
			return failedResult(call, "invalid_image_url", err.Error()), nil
		}
		content = append(content, llm.ContentPart{Type: "image_url", ImageURL: &llm.ImageURL{URL: value, Detail: detail}})
	}
	response, err := r.Client.Generate(ctx, llm.Request{
		Provider: r.Vision.Provider, ProviderType: r.Vision.ProviderType, Model: r.Vision.Model,
		BaseURL: r.Vision.BaseURL, APIKey: r.Vision.APIKey,
		Messages: []llm.Message{{Role: "user", Content: content}},
	})
	if err != nil {
		return failedResult(call, "vision_analysis_failed", err.Error()), nil
	}
	analysis := strings.TrimSpace(textContentParts(response.Message.Content))
	if analysis == "" {
		return failedResult(call, "empty_vision_analysis", "The configured vision model returned no analysis text."), nil
	}
	state, _ := json.Marshal(map[string]any{
		"provider": r.Vision.Provider, "model": r.Vision.Model,
		"image_count": len(input.Paths) + len(input.ImageURLs), "usage": response.Usage,
	})
	return ExecutionResult{ID: call.ID, Identifier: ImageIdentifier, APIName: "analyze", Content: analysis, State: state}, nil
}

func workspaceImage(ctx context.Context, executionContext ExecutionContext, path string) (workspaceImageData, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return workspaceImageData{}, fmt.Errorf("image path is empty")
	}
	var content []byte
	var contentType string
	if exporter, ok := executionContext.Provider.(capability.ArtifactExportProvider); ok {
		file, err := exporter.ExportArtifactFile(ctx, capability.ExportArtifactFileRequest{Path: path})
		if err != nil {
			return workspaceImageData{}, fmt.Errorf("read image %q: %w", path, err)
		}
		content, contentType = file.Content, file.ContentType
	} else if executionContext.Provider != nil {
		const pageBytes = 1 << 20
		var offset int64
		var revision string
		for {
			file, err := executionContext.Provider.ReadFile(ctx, capability.ReadFileRequest{
				Meta: capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline),
				Path: path, OffsetBytes: &offset, MaxBytes: intPointer(pageBytes), FileRevision: revision,
			})
			if err != nil {
				return workspaceImageData{}, fmt.Errorf("read image %q: %w", path, err)
			}
			content = append(content, file.Content...)
			if len(content) > maxImageInputBytes {
				return workspaceImageData{}, fmt.Errorf("image %q exceeds %d bytes", path, maxImageInputBytes)
			}
			if file.EOF {
				break
			}
			if file.NextOffsetBytes <= offset {
				return workspaceImageData{}, fmt.Errorf("image %q read did not advance", path)
			}
			offset = file.NextOffsetBytes
			revision = file.FileRevision
		}
	} else {
		return workspaceImageData{}, fmt.Errorf("workspace image provider is unavailable")
	}
	if len(content) == 0 {
		return workspaceImageData{}, fmt.Errorf("image %q is empty", path)
	}
	if len(content) > maxImageInputBytes {
		return workspaceImageData{}, fmt.Errorf("image %q exceeds %d bytes", path, maxImageInputBytes)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(content)
	}
	if !supportedImageContentType(contentType) {
		return workspaceImageData{}, fmt.Errorf("image %q has unsupported content type %q", path, contentType)
	}
	return workspaceImageData{Name: filepath.Base(path), ContentType: contentType, Content: content}, nil
}

func intPointer(value int) *int {
	return &value
}

func validateVisionImageURL(value string) error {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "data:image/") {
		comma := strings.IndexByte(value, ',')
		if comma < 0 || !strings.Contains(value[:comma], ";base64") {
			return fmt.Errorf("image data URL must use base64 encoding")
		}
		decoded, err := base64.StdEncoding.DecodeString(value[comma+1:])
		if err != nil {
			return fmt.Errorf("invalid image data URL: %w", err)
		}
		if len(decoded) > maxImageInputBytes {
			return fmt.Errorf("image data URL exceeds %d bytes", maxImageInputBytes)
		}
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("image URL must be a base64 data:image URL or an HTTPS URL without credentials")
	}
	return nil
}

func supportedImageContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch value {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func imageDataURL(image workspaceImageData) string {
	return "data:" + image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(image.Content)
}

func textContentParts(parts []llm.ContentPart) string {
	var values []string
	for _, part := range parts {
		if (part.Type == "" || part.Type == "text") && strings.TrimSpace(part.Text) != "" {
			values = append(values, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(values, "\n")
}

func defaultImageOption(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func imageExtension(contentType string, format string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/png":
		return "png"
	}
	if strings.EqualFold(format, "jpeg") {
		return "jpg"
	}
	return defaultImageOption(format, "png")
}
