package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchFallbackDropsEngineConstraintBeforeChangingProvider(t *testing.T) {
	provider := &stubSearchProvider{
		name: "searxng",
		fn: func(_ context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
			if query != "golang" {
				t.Fatalf("unexpected query: %q", query)
			}
			if len(params.Engines) > 0 {
				return WebSearchResponse{}, nil
			}
			return WebSearchResponse{
				Results: []WebSearchResult{{
					Title:   "The Go Programming Language",
					URL:     "https://go.dev/",
					Snippet: "Official Go site",
				}},
			}, nil
		},
	}
	service := &HTTPWebService{
		SearchProviders: []webSearchProvider{provider},
		SearchOrder:     []string{"searxng"},
		SearchItemLimit: 30,
	}

	response, err := service.Search(context.Background(), WebSearchRequest{
		Query:   "golang",
		Engines: []string{"google"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.Results) != 1 || response.Provider != "searxng" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(provider.calls) != 2 || len(provider.calls[0].Engines) != 1 || len(provider.calls[1].Engines) != 0 {
		t.Fatalf("expected fallback to retry without engines, got %#v", provider.calls)
	}
}

func TestSearchFallbackContinuesToNextProviderAfterError(t *testing.T) {
	first := &stubSearchProvider{
		name: "searxng",
		fn: func(context.Context, string, webSearchQueryParams) (WebSearchResponse, error) {
			return WebSearchResponse{}, errors.New("provider down")
		},
	}
	second := &stubSearchProvider{
		name: "brave",
		fn: func(_ context.Context, _ string, _ webSearchQueryParams) (WebSearchResponse, error) {
			return WebSearchResponse{
				Results: []WebSearchResult{{Title: "Harness", URL: "https://example.com"}},
			}, nil
		},
	}
	service := &HTTPWebService{
		SearchProviders: []webSearchProvider{first, second},
		SearchOrder:     []string{"searxng", "brave"},
		SearchItemLimit: 30,
	}

	response, err := service.Search(context.Background(), WebSearchRequest{Query: "harness"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if response.Provider != "brave" || len(response.Results) != 1 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(response.Diagnostics) != 2 {
		t.Fatalf("expected diagnostics for both providers, got %#v", response.Diagnostics)
	}
	if response.Diagnostics[0].Provider != "searxng" || response.Diagnostics[0].ErrorDetail != "provider down" {
		t.Fatalf("unexpected first diagnostic: %#v", response.Diagnostics[0])
	}
	if response.Diagnostics[1].Provider != "brave" || response.Diagnostics[1].ResultNumbers != 1 {
		t.Fatalf("unexpected second diagnostic: %#v", response.Diagnostics[1])
	}
}

func TestSearchFallbackDropsAllFiltersOnFinalAttempt(t *testing.T) {
	provider := &stubSearchProvider{
		name: "searxng",
		fn: func(_ context.Context, _ string, params webSearchQueryParams) (WebSearchResponse, error) {
			if len(params.Categories) == 0 && len(params.Engines) == 0 && params.TimeRange == "" {
				return WebSearchResponse{
					Results: []WebSearchResult{{Title: "Fallback result", URL: "https://example.com/fallback"}},
				}, nil
			}
			return WebSearchResponse{}, nil
		},
	}
	service := &HTTPWebService{
		SearchProviders: []webSearchProvider{provider},
		SearchOrder:     []string{"searxng"},
		SearchItemLimit: 30,
	}

	response, err := service.Search(context.Background(), WebSearchRequest{
		Query:      "fallback",
		Categories: []string{"news"},
		Engines:    []string{"google"},
		TimeRange:  "day",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected final unfiltered attempt to succeed, got %#v", response)
	}
	if len(provider.calls) != 3 {
		t.Fatalf("expected three fallback attempts, got %#v", provider.calls)
	}
	if len(response.Diagnostics) != 3 {
		t.Fatalf("expected diagnostics for three attempts, got %#v", response.Diagnostics)
	}
	final := provider.calls[2]
	if len(final.Categories) != 0 || len(final.Engines) != 0 || final.TimeRange != "" {
		t.Fatalf("expected final attempt to drop all filters, got %#v", final)
	}
	finalDiagnostic := response.Diagnostics[2]
	if finalDiagnostic.Attempt != 3 || len(finalDiagnostic.Categories) != 0 || len(finalDiagnostic.Engines) != 0 || finalDiagnostic.TimeRange != "" || finalDiagnostic.ResultNumbers != 1 {
		t.Fatalf("unexpected final diagnostic: %#v", finalDiagnostic)
	}
}

func TestSearchPreservesProviderUnresponsiveEnginesInState(t *testing.T) {
	provider := &stubSearchProvider{
		name: "searxng",
		fn: func(context.Context, string, webSearchQueryParams) (WebSearchResponse, error) {
			return WebSearchResponse{
				Results: []WebSearchResult{{Title: "Harness", URL: "https://example.com"}},
				UnresponsiveEngines: []WebSearchUnresponsiveEngine{{
					Name:  "sogou",
					Error: "Suspended: CAPTCHA",
				}},
			}, nil
		},
	}
	service := &HTTPWebService{
		SearchProviders: []webSearchProvider{provider},
		SearchOrder:     []string{"searxng"},
		SearchItemLimit: 30,
	}

	response, err := service.Search(context.Background(), WebSearchRequest{Query: "harness"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.UnresponsiveEngines) != 1 || response.UnresponsiveEngines[0].Name != "sogou" {
		t.Fatalf("expected provider unresponsive engines in response state, got %#v", response)
	}
}

func TestSearchReturnsEmptyResponseWhenAllProvidersHaveNoResults(t *testing.T) {
	first := &stubSearchProvider{
		name: "searxng",
		fn: func(context.Context, string, webSearchQueryParams) (WebSearchResponse, error) {
			return WebSearchResponse{}, nil
		},
	}
	second := &stubSearchProvider{
		name: "brave",
		fn: func(context.Context, string, webSearchQueryParams) (WebSearchResponse, error) {
			return WebSearchResponse{}, nil
		},
	}
	service := &HTTPWebService{
		SearchProviders: []webSearchProvider{first, second},
		SearchOrder:     []string{"searxng", "brave"},
		SearchItemLimit: 30,
	}

	response, err := service.Search(context.Background(), WebSearchRequest{Query: "no hits"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if response.ResultNumbers != 0 || len(response.Results) != 0 || response.Query != "no hits" {
		t.Fatalf("expected empty search response, got %#v", response)
	}
	if len(first.calls) != 1 || len(second.calls) != 1 {
		t.Fatalf("expected each provider to be tried once, first=%#v second=%#v", first.calls, second.calls)
	}
}

func TestSearchProviderOrderFromEnvPrioritizesConfiguredKeys(t *testing.T) {
	clearWebSearchProviderEnv(t)
	t.Setenv("TMA_WEB_TAVILY_API_KEY", "tavily-key")
	t.Setenv("TMA_WEB_BRAVE_API_KEY", "brave-key")
	t.Setenv("TMA_WEB_EXA_API_KEY", "exa-key")
	t.Setenv("TMA_WEB_BAIDU_API_KEY", "baidu-key")

	order := searchProviderOrderFromEnv()
	expected := []string{"tavily", "brave", "exa", "baidu", "searxng"}
	if strings.Join(order, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected auto provider order %v, got %v", expected, order)
	}
}

func TestSearchProviderOrderFromEnvRespectsExplicitOrder(t *testing.T) {
	clearWebSearchProviderEnv(t)
	t.Setenv("TMA_WEB_TAVILY_API_KEY", "tavily-key")
	t.Setenv("TMA_WEB_SEARCH_PROVIDERS", "searxng,exa")

	order := searchProviderOrderFromEnv()
	expected := []string{"searxng", "exa"}
	if strings.Join(order, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected explicit provider order %v, got %v", expected, order)
	}
}

func TestNewHTTPWebServiceFromEnvUsesConfiguredKeyProvidersBeforeSearXNG(t *testing.T) {
	clearWebSearchProviderEnv(t)
	t.Setenv("TMA_WEB_EXA_API_KEY", "exa-key")
	t.Setenv("TMA_WEB_BRAVE_API_KEY", "brave-key")

	service := NewHTTPWebServiceFromEnv()
	names := make([]string, 0, len(service.SearchProviders))
	for _, provider := range service.SearchProviders {
		names = append(names, provider.Name())
	}
	expected := []string{"brave", "exa", "searxng"}
	if strings.Join(names, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected service providers %v, got %v", expected, names)
	}
}

func TestCrawlerOrderFromEnvCanPrioritizeBrowserlessAfterNaive(t *testing.T) {
	t.Setenv("TMA_WEB_CRAWLER_IMPLS", "")
	t.Setenv("TMA_WEB_BROWSERLESS_PRIORITY", "after_naive")

	order := crawlerOrderFromEnv()
	expected := []string{"jina", "naive", "browserless", "search1api"}
	if strings.Join(order, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected crawler order %v, got %v", expected, order)
	}
}

func TestBrowserlessCrawlerBuildsDynamicPayload(t *testing.T) {
	crawler := browserlessCrawler{
		waitSelector:          "#app",
		waitForTimeoutMS:      1200,
		waitSelectorTimeoutMS: 8000,
		gotoTimeoutMS:         15000,
		waitUntil:             "networkidle2",
		userAgent:             "TMA Browserless Test",
		rejectResourceTypes:   []string{"image", "font"},
		bestAttempt:           true,
	}

	payload := crawler.payload("https://example.com/app")
	if payload["url"] != "https://example.com/app" || payload["bestAttempt"] != true || payload["userAgent"] != "TMA Browserless Test" {
		t.Fatalf("unexpected browserless payload: %#v", payload)
	}
	if payload["waitForTimeout"] != 1200 {
		t.Fatalf("expected waitForTimeout, got %#v", payload["waitForTimeout"])
	}
	waitForSelector, ok := payload["waitForSelector"].(map[string]any)
	if !ok || waitForSelector["selector"] != "#app" || waitForSelector["timeout"] != 8000 {
		t.Fatalf("unexpected waitForSelector: %#v", payload["waitForSelector"])
	}
	gotoOptions, ok := payload["gotoOptions"].(map[string]any)
	if !ok || gotoOptions["waitUntil"] != "networkidle2" || gotoOptions["timeout"] != 15000 {
		t.Fatalf("unexpected gotoOptions: %#v", payload["gotoOptions"])
	}
	rejectResourceTypes, ok := payload["rejectResourceTypes"].([]string)
	if !ok || strings.Join(rejectResourceTypes, ",") != "image,font" {
		t.Fatalf("unexpected rejectResourceTypes: %#v", payload["rejectResourceTypes"])
	}
}

func TestExaProviderParsesSearchResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "exa-key" {
			t.Fatalf("expected exa key header, got %q", r.Header.Get("x-api-key"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["query"] != "harness" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Harness docs","url":"https://example.com/harness","text":"Exa result text","publishedDate":"2026-07-09"}]}`))
	}))
	defer server.Close()

	response, err := newExaProvider(server.Client(), server.URL, "exa-key").Query(context.Background(), "harness", webSearchQueryParams{Limit: 2})
	if err != nil {
		t.Fatalf("query exa: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].Source != "exa" || response.Results[0].Snippet != "Exa result text" || response.Results[0].PublishedAt != "2026-07-09" {
		t.Fatalf("unexpected exa response: %#v", response)
	}
}

func TestBaiduProviderParsesCompatibleSearchResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer baidu-key" {
			t.Fatalf("expected baidu bearer header, got %q", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["query"] != "harness" || payload["q"] != "harness" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"search_results":[{"title":"百度结果","url":"https://example.com/baidu","summary":"Baidu result text"}]}}`))
	}))
	defer server.Close()

	response, err := newBaiduProvider(server.Client(), server.URL, "baidu-key").Query(context.Background(), "harness", webSearchQueryParams{Limit: 2})
	if err != nil {
		t.Fatalf("query baidu: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].Source != "baidu" || response.Results[0].Snippet != "Baidu result text" {
		t.Fatalf("unexpected baidu response: %#v", response)
	}
}

func TestCrawlRewritesGitHubBlobURLAndPrioritizesNaive(t *testing.T) {
	naive := &stubCrawler{
		name: "naive",
		fn: func(_ context.Context, url string) (WebCrawlPage, error) {
			if url != "https://raw.githubusercontent.com/owner/repo/main/README.md" {
				t.Fatalf("unexpected rewritten url: %q", url)
			}
			return WebCrawlPage{
				FinalURL:    url,
				ContentType: "text/plain",
				Content:     strings.Repeat("A", 120),
			}, nil
		},
	}
	jina := &stubCrawler{
		name: "jina",
		fn: func(context.Context, string) (WebCrawlPage, error) {
			t.Fatal("jina should not run after successful naive crawl")
			return WebCrawlPage{}, nil
		},
	}
	service := &HTTPWebService{
		CrawlImplementors: map[string]webCrawlerImpl{
			"naive": naive,
			"jina":  jina,
		},
		CrawlerOrder:      []string{"jina", "naive"},
		CrawlContentLimit: 25000,
		CrawlerRetry:      1,
	}

	response, err := service.Crawl(context.Background(), WebCrawlRequest{
		URL: "https://github.com/owner/repo/blob/main/README.md",
	})
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if len(response.Pages) != 1 || !response.Pages[0].Success || response.Pages[0].Impl != "naive" {
		t.Fatalf("unexpected crawl response: %#v", response)
	}
}

func TestCrawlUsesNextImplementationWhenContentIsTooShort(t *testing.T) {
	short := &stubCrawler{
		name: "jina",
		fn: func(context.Context, string) (WebCrawlPage, error) {
			return WebCrawlPage{
				FinalURL:    "https://example.com",
				ContentType: "text/plain",
				Content:     "too short",
			}, nil
		},
	}
	long := &stubCrawler{
		name: "naive",
		fn: func(context.Context, string) (WebCrawlPage, error) {
			return WebCrawlPage{
				FinalURL:    "https://example.com",
				ContentType: "text/plain",
				Content:     strings.Repeat("B", 120),
			}, nil
		},
	}
	service := &HTTPWebService{
		CrawlImplementors: map[string]webCrawlerImpl{
			"jina":  short,
			"naive": long,
		},
		CrawlerOrder:      []string{"jina", "naive"},
		CrawlContentLimit: 25000,
	}

	response, err := service.Crawl(context.Background(), WebCrawlRequest{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if len(response.Pages) != 1 || !response.Pages[0].Success || response.Pages[0].Impl != "naive" {
		t.Fatalf("expected crawl to fall through to long implementation, got %#v", response)
	}
	attempts := response.Pages[0].Attempts
	if len(attempts) != 2 {
		t.Fatalf("expected two crawl attempt diagnostics, got %#v", attempts)
	}
	if attempts[0].Impl != "jina" || attempts[0].ErrorType != "content_too_short" {
		t.Fatalf("unexpected first crawl diagnostic: %#v", attempts[0])
	}
	if attempts[1].Impl != "naive" || attempts[1].ContentLength != 120 {
		t.Fatalf("unexpected second crawl diagnostic: %#v", attempts[1])
	}
}

func TestCrawlReturnsStructuredErrorWhenAllImplementationsFail(t *testing.T) {
	failing := &stubCrawler{
		name: "naive",
		fn: func(context.Context, string) (WebCrawlPage, error) {
			return WebCrawlPage{}, errors.New("blocked by target")
		},
	}
	service := &HTTPWebService{
		CrawlImplementors: map[string]webCrawlerImpl{"naive": failing},
		CrawlerOrder:      []string{"naive"},
		CrawlContentLimit: 25000,
	}

	response, err := service.Crawl(context.Background(), WebCrawlRequest{URL: "https://example.com/protected"})
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if len(response.Pages) != 1 {
		t.Fatalf("expected one page result, got %#v", response)
	}
	page := response.Pages[0]
	if page.Success || page.ErrorType != "crawl_impl_failed" || !strings.Contains(page.ErrorMessage, "blocked by target") {
		t.Fatalf("expected structured crawl failure, got %#v", page)
	}
	if len(page.Attempts) != 1 || page.Attempts[0].Impl != "naive" || page.Attempts[0].ErrorType != "crawl_impl_failed" {
		t.Fatalf("expected crawl failure diagnostics, got %#v", page.Attempts)
	}

	content := formatCrawlContent(response)
	if !strings.Contains(content, `<error type="crawl_impl_failed">blocked by target</error>`) {
		t.Fatalf("expected model-readable error XML, got %s", content)
	}
}

func TestCrawlReturnsInvalidURLErrorWithoutTryingImplementations(t *testing.T) {
	called := false
	service := &HTTPWebService{
		CrawlImplementors: map[string]webCrawlerImpl{
			"naive": &stubCrawler{
				name: "naive",
				fn: func(context.Context, string) (WebCrawlPage, error) {
					called = true
					return WebCrawlPage{}, nil
				},
			},
		},
		CrawlerOrder:      []string{"naive"},
		CrawlContentLimit: 25000,
	}

	response, err := service.Crawl(context.Background(), WebCrawlRequest{URL: "javascript:alert(1)"})
	if err != nil {
		t.Fatalf("crawl: %v", err)
	}
	if called {
		t.Fatal("crawler implementation should not run for invalid URL")
	}
	if len(response.Pages) != 1 || response.Pages[0].ErrorType != "invalid_url" {
		t.Fatalf("expected invalid_url page result, got %#v", response)
	}
}

func TestWebRuntimeSearchShapesStructuredFailureForModel(t *testing.T) {
	runtime := WebRuntime{
		Service: stubWebService{
			searchResponse: WebSearchResponse{
				Query:       "latest",
				ErrorDetail: "searxng base url is not configured",
			},
		},
	}

	result, err := runtime.Execute(context.Background(), Call{
		ID:         "call_search",
		Identifier: WebIdentifier,
		APIName:    "search",
		Arguments:  json.RawMessage(`{"query":"latest"}`),
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("execute search: %v", err)
	}
	if result.Error == nil || result.Error.Type != "search_failed" {
		t.Fatalf("expected structured search failure, got %#v", result)
	}
	if !strings.Contains(result.Content, "not configured") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestNaiveCrawlerExtractsReadableTextFromHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Harness</title></head><body><h1>Hello</h1><p>World</p></body></html>`))
	}))
	defer server.Close()

	page, err := newNaiveCrawler(server.Client()).Crawl(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("crawl html: %v", err)
	}
	if page.Title != "Harness" || !strings.Contains(page.Content, "Hello World") {
		t.Fatalf("unexpected parsed page: %#v", page)
	}
}

type stubSearchProvider struct {
	name  string
	calls []webSearchQueryParams
	fn    func(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error)
}

func (provider *stubSearchProvider) Name() string { return provider.name }

func (provider *stubSearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	provider.calls = append(provider.calls, params)
	return provider.fn(ctx, query, params)
}

type stubCrawler struct {
	name string
	fn   func(ctx context.Context, url string) (WebCrawlPage, error)
}

func (crawler *stubCrawler) Name() string { return crawler.name }

func (crawler *stubCrawler) Crawl(ctx context.Context, url string) (WebCrawlPage, error) {
	return crawler.fn(ctx, url)
}

type stubWebService struct {
	searchResponse WebSearchResponse
	searchError    error
	crawlResponse  WebCrawlResponse
	crawlError     error
}

func (service stubWebService) Search(context.Context, WebSearchRequest) (WebSearchResponse, error) {
	return service.searchResponse, service.searchError
}

func (service stubWebService) Crawl(context.Context, WebCrawlRequest) (WebCrawlResponse, error) {
	return service.crawlResponse, service.crawlError
}

func clearWebSearchProviderEnv(t *testing.T) {
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
