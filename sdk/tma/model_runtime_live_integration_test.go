package tma

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const liveModelTestEnabled = "TMA_RUN_LIVE_MODEL_TESTS"

func TestLiveModelRuntimeGenerateAndAudit(t *testing.T) {
	if os.Getenv(liveModelTestEnabled) != "1" {
		t.Skip("set TMA_RUN_LIVE_MODEL_TESTS=1 to run the live Platform model smoke test")
	}

	baseURL := strings.TrimSpace(os.Getenv("TMA_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18090"
	}
	providerID := strings.TrimSpace(os.Getenv("TMA_LLM_PROVIDER"))
	modelName := strings.TrimSpace(os.Getenv("TMA_LLM_MODEL"))
	if providerID == "" || modelName == "" {
		t.Fatal("TMA_LLM_PROVIDER and TMA_LLM_MODEL are required")
	}

	options := []Option{WithHTTPClient(&http.Client{Timeout: 2 * time.Minute})}
	if token := strings.TrimSpace(os.Getenv("TMA_AUTH_TOKEN")); token != "" {
		options = append(options, WithBearerToken(token))
	}
	client, err := NewClient(baseURL, options...)
	if err != nil {
		t.Fatal("create Platform SDK client")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	models, err := client.LLM.ListModels(ctx, providerID)
	if err != nil {
		failLiveModelTest(t, "list registered models", err)
	}
	model := findLiveModel(models, modelName)
	if model == nil {
		t.Fatalf("model %s/%s is not registered", providerID, modelName)
	}
	if model.CapabilityType != "text_image" {
		t.Fatalf("model %s/%s has capability %q, want text_image", providerID, modelName, model.CapabilityType)
	}

	startedAt := time.Now().UTC().Add(-time.Second)
	textResponse, err := client.ModelRuntime.Generate(ctx, ModelGenerateRequest{
		ProviderID: providerID,
		Model:      modelName,
		Messages: []ModelMessage{{
			Role:    "user",
			Content: "Reply with exactly TMA_PLATFORM_SMOKE_OK and no other text.",
		}},
		MaxOutputTokens: 32,
	})
	if err != nil {
		failLiveModelTest(t, "generate text", err)
	}
	if !strings.Contains(textResponse.Text, "TMA_PLATFORM_SMOKE_OK") {
		t.Fatalf("text response did not contain the smoke marker: %q", textResponse.Text)
	}
	assertLiveModelRouteAndUsage(t, textResponse, providerID, modelName)

	visionResponse, err := client.ModelRuntime.Generate(ctx, ModelGenerateRequest{
		ProviderID: providerID,
		Model:      modelName,
		Messages: []ModelMessage{{
			Role: "user",
			Parts: []ModelContentPart{
				{Type: "text", Text: "Identify the solid color on the left and the solid color on the right. Reply only as LEFT_<COLOR>_RIGHT_<COLOR>, using uppercase English color names."},
				{Type: "image_url", ImageURL: &ModelImageURL{URL: redBlueImageDataURL(t), Detail: "high"}},
			},
		}},
		MaxOutputTokens: 32,
	})
	if err != nil {
		failLiveModelTest(t, "generate image understanding", err)
	}
	if !strings.Contains(visionResponse.Text, "LEFT_RED_RIGHT_BLUE") {
		t.Fatalf("vision response did not contain the image marker: %q", visionResponse.Text)
	}
	assertLiveModelRouteAndUsage(t, visionResponse, providerID, modelName)

	report, err := client.ModelRuntime.Invocations(ctx, ModelInvocationQuery{
		Capability: "generate",
		ProviderID: providerID,
		Model:      modelName,
		Status:     "completed",
		From:       &startedAt,
		Limit:      10,
	})
	if err != nil {
		failLiveModelTest(t, "list model invocations", err)
	}
	if report.Summary.CompletedCount < 2 || len(report.Records) < 2 {
		t.Fatalf("expected at least two completed invocation records, summary=%+v records=%d", report.Summary, len(report.Records))
	}
	if report.Summary.TotalTokens <= 0 {
		t.Fatalf("expected invocation audit to include token usage, summary=%+v", report.Summary)
	}
}

func findLiveModel(models []LLMModel, modelName string) *LLMModel {
	for index := range models {
		if models[index].Model == modelName {
			return &models[index]
		}
	}
	return nil
}

func assertLiveModelRouteAndUsage(t *testing.T, response ModelGenerateResponse, providerID string, modelName string) {
	t.Helper()
	if response.ProviderID != providerID || response.Model != modelName {
		t.Fatalf("unexpected model route %s/%s", response.ProviderID, response.Model)
	}
	if response.Usage.TotalTokens <= 0 {
		t.Fatalf("model response did not include token usage: %+v", response.Usage)
	}
}

func failLiveModelTest(t *testing.T, operation string, err error) {
	t.Helper()
	if secret := os.Getenv("TMA_LLM_API_KEY"); secret != "" && strings.Contains(err.Error(), secret) {
		t.Fatalf("%s failed and exposed the configured API key", operation)
	}
	t.Fatalf("%s failed: %v", operation, err)
}

func redBlueImageDataURL(t *testing.T) string {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 256, 128))
	for y := 0; y < canvas.Bounds().Dy(); y++ {
		for x := 0; x < canvas.Bounds().Dx(); x++ {
			pixel := color.RGBA{R: 230, G: 30, B: 40, A: 255}
			if x >= canvas.Bounds().Dx()/2 {
				pixel = color.RGBA{R: 25, G: 80, B: 220, A: 255}
			}
			canvas.SetRGBA(x, y, pixel)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal("encode live vision fixture")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}
