package main

import (
	"strings"
	"testing"

	webtools "tiggy-manage-agent/internal/tools"
)

func TestWebDoctorSearchOrderFromEnvPrioritizesConfiguredKeys(t *testing.T) {
	clearWebDoctorEnv(t)
	t.Setenv("TMA_WEB_EXA_API_KEY", "exa-key")
	t.Setenv("TMA_WEB_TAVILY_API_KEY", "tavily-key")
	t.Setenv("TMA_WEB_BAIDU_API_KEY", "baidu-key")

	order := webDoctorSearchOrderFromEnv()
	expected := []string{"tavily", "exa", "baidu", "searxng"}
	if strings.Join(order, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected order %v, got %v", expected, order)
	}
}

func TestWebDoctorSearchOrderFromEnvRespectsExplicitOrder(t *testing.T) {
	clearWebDoctorEnv(t)
	t.Setenv("TMA_WEB_TAVILY_API_KEY", "tavily-key")
	t.Setenv("TMA_WEB_SEARCH_PROVIDERS", "searxng,brave")

	order := webDoctorSearchOrderFromEnv()
	expected := []string{"searxng", "brave"}
	if strings.Join(order, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected order %v, got %v", expected, order)
	}
}

func TestApplyWebDoctorSearXNGPayloadReportsEngines(t *testing.T) {
	result := webDoctorSearXNG{Reachable: true}
	applyWebDoctorSearXNGPayload(&result, webDoctorSearXNGPayload{
		Results: []struct {
			Engine  string   `json:"engine"`
			Engines []string `json:"engines"`
		}{
			{Engine: "baidu", Engines: []string{"baidu", "sogou"}},
			{Engine: "quark"},
		},
		UnresponsiveEngines: [][]string{{"bing", "timeout"}},
	})
	if result.ResultCount != 2 {
		t.Fatalf("unexpected result count: %#v", result)
	}
	if strings.Join(result.Engines, ",") != "baidu,bing,quark,sogou" {
		t.Fatalf("unexpected engines: %#v", result.Engines)
	}
}

func TestApplyWebDoctorSearXNGPayloadDetectsBlockedEngines(t *testing.T) {
	result := webDoctorSearXNG{Reachable: true}
	applyWebDoctorSearXNGPayload(&result, webDoctorSearXNGPayload{
		Results: []struct {
			Engine  string   `json:"engine"`
			Engines []string `json:"engines"`
		}{
			{Engine: "google"},
		},
		UnresponsiveEngines: [][]string{{"duckduckgo", "timeout"}},
	})
	if strings.Join(result.BlockedEnginesActive, ",") != "duckduckgo,google" {
		t.Fatalf("unexpected blocked engines: %#v", result.BlockedEnginesActive)
	}
}

func TestWebCrawlAttemptsOutput(t *testing.T) {
	output := webCrawlAttemptsOutput(webtools.WebCrawlResponse{
		Pages: []webtools.WebCrawlPage{{
			URL:          "https://example.com",
			Impl:         "browserless",
			Success:      false,
			ErrorType:    "crawl_impl_failed",
			ErrorMessage: "timeout",
			Attempts: []webtools.WebCrawlAttemptDiagnostic{{
				Round:        1,
				Impl:         "browserless",
				URL:          "https://example.com",
				ErrorType:    "crawl_impl_failed",
				ErrorMessage: "timeout",
			}},
		}},
	})
	if len(output) != 1 {
		t.Fatalf("expected one page output, got %#v", output)
	}
	if output[0]["impl"] != "browserless" || output[0]["attempts_count"] != 1 {
		t.Fatalf("unexpected attempts output: %#v", output[0])
	}
}

func clearWebDoctorEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TMA_WEB_SEARCH_PROVIDERS",
		"TMA_WEB_TAVILY_API_KEY",
		"TMA_WEB_BRAVE_API_KEY",
		"TMA_WEB_EXA_API_KEY",
		"TMA_WEB_BAIDU_API_KEY",
		"TMA_WEB_SEARCH1API_API_KEY",
	} {
		t.Setenv(key, "")
	}
}
