package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	WebIdentifier                = NamespaceWeb
	defaultCrawlerImplList       = "jina,naive,search1api,browserless"
	defaultSearchItemLimit       = 30
	defaultSearchProviderLimit   = 10
	defaultCrawlContentLimit     = 25000
	defaultCrawlerRetry          = 1
	minCrawlerSuccessContentSize = 100
	maxCrawlResponseBodyBytes    = 2 << 20
	defaultHTTPTimeout           = 15 * time.Second
)

type WebToolService interface {
	Search(ctx context.Context, request WebSearchRequest) (WebSearchResponse, error)
	Crawl(ctx context.Context, request WebCrawlRequest) (WebCrawlResponse, error)
}

type WebRuntime struct {
	Service WebToolService
}

type WebSearchRequest struct {
	Query      string   `json:"query"`
	Categories []string `json:"categories,omitempty"`
	Engines    []string `json:"engines,omitempty"`
	TimeRange  string   `json:"time_range,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

type WebSearchResult struct {
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
	Source      string `json:"source,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type WebSearchAttemptDiagnostic struct {
	Provider      string   `json:"provider"`
	Attempt       int      `json:"attempt"`
	Categories    []string `json:"categories,omitempty"`
	Engines       []string `json:"engines,omitempty"`
	TimeRange     string   `json:"time_range,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	CostTimeMS    int64    `json:"cost_time_ms"`
	ResultNumbers int      `json:"result_numbers"`
	ErrorDetail   string   `json:"error_detail,omitempty"`
}

type WebSearchUnresponsiveEngine struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
}

type WebSearchResponse struct {
	Query               string                        `json:"query"`
	Provider            string                        `json:"provider,omitempty"`
	CostTimeMS          int64                         `json:"cost_time_ms"`
	ResultNumbers       int                           `json:"result_numbers"`
	Results             []WebSearchResult             `json:"results,omitempty"`
	UnresponsiveEngines []WebSearchUnresponsiveEngine `json:"unresponsive_engines,omitempty"`
	Diagnostics         []WebSearchAttemptDiagnostic  `json:"diagnostics,omitempty"`
	ErrorDetail         string                        `json:"error_detail,omitempty"`
}

type WebCrawlRequest struct {
	URL      string   `json:"url,omitempty"`
	URLs     []string `json:"urls,omitempty"`
	MaxPages int      `json:"max_pages,omitempty"`
}

type WebCrawlAttemptDiagnostic struct {
	Round         int    `json:"round"`
	Impl          string `json:"impl"`
	URL           string `json:"url"`
	FinalURL      string `json:"final_url,omitempty"`
	CostTimeMS    int64  `json:"cost_time_ms"`
	ContentLength int    `json:"content_length,omitempty"`
	ErrorType     string `json:"error_type,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

type WebCrawlPage struct {
	URL          string                      `json:"url"`
	FinalURL     string                      `json:"final_url,omitempty"`
	Title        string                      `json:"title,omitempty"`
	Content      string                      `json:"content,omitempty"`
	ContentType  string                      `json:"content_type,omitempty"`
	Impl         string                      `json:"impl,omitempty"`
	Success      bool                        `json:"success"`
	ErrorType    string                      `json:"error_type,omitempty"`
	ErrorMessage string                      `json:"error_message,omitempty"`
	Attempts     []WebCrawlAttemptDiagnostic `json:"attempts,omitempty"`
}

type WebCrawlResponse struct {
	Pages []WebCrawlPage `json:"pages"`
}

type webSearchProvider interface {
	Name() string
	Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error)
}

type webSearchQueryParams struct {
	Categories []string
	Engines    []string
	TimeRange  string
	Limit      int
}

type webCrawlerImpl interface {
	Name() string
	Crawl(ctx context.Context, url string) (WebCrawlPage, error)
}

type HTTPWebService struct {
	SearchProviders   []webSearchProvider
	CrawlImplementors map[string]webCrawlerImpl
	SearchOrder       []string
	CrawlerOrder      []string
	SearchItemLimit   int
	CrawlContentLimit int
	CrawlerRetry      int
}

type httpWebClient struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

var (
	defaultWebService     WebToolService
	defaultWebServiceOnce sync.Once
)

func (WebRuntime) Manifest() Manifest {
	return Manifest{
		Identifier: WebIdentifier,
		Type:       "builtin",
		Meta: Meta{
			Title:       "Web Tools",
			Description: "Search the web and crawl pages with layered provider and fetch fallbacks.",
		},
		SystemRole:     "Use web.search to find fresh public web results, then use web.crawl to read specific pages. Prefer search before crawling unknown URLs, and keep crawls focused to the pages you actually need.",
		Executors:      []string{ExecutorServer},
		ApprovalPolicy: ApprovalPolicyAlways,
		ApprovalReason: InterventionReasonNetworkAccess,
		API: []API{
			{
				Name:           "search",
				Namespace:      NamespaceWeb,
				APIName:        "search",
				Description:    "Search the public web with provider fallback and automatic retry using broader filters when needed.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"categories":{"type":"array","items":{"type":"string"}},"engines":{"type":"array","items":{"type":"string"}},"time_range":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":30}},"required":["query"]}`),
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
			{
				Name:           "crawl",
				Namespace:      NamespaceWeb,
				APIName:        "crawl",
				Description:    "Fetch one or more URLs with crawl implementation fallback, URL-specific rewrites, retries, and content truncation.",
				Parameters:     json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"urls":{"type":"array","items":{"type":"string"}},"max_pages":{"type":"integer","minimum":1,"maximum":10}},"anyOf":[{"required":["url"]},{"required":["urls"]}]}`),
				Risk:           ToolRiskRead,
				Runtime:        &RuntimePolicy{Allowed: []string{ToolRuntimeAuto, ToolRuntimeCloudSandbox, ToolRuntimeLocalSystem}, Preferred: ToolRuntimeAuto},
				Implementation: ToolImplementationServerBuiltin,
			},
		},
	}
}

func (runtime WebRuntime) Execute(ctx context.Context, call Call, _ ExecutionContext) (ExecutionResult, error) {
	service := runtime.Service
	if service == nil {
		service = DefaultWebService()
	}
	if service == nil {
		return ExecutionResult{}, errors.New("web service is not configured")
	}

	switch normalizeWebAPIName(call.APIName) {
	case "search":
		var request WebSearchRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode search arguments: %w", err)
		}
		response, err := service.Search(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(response)
		if err != nil {
			return ExecutionResult{}, err
		}
		result := ExecutionResult{
			ID:         call.ID,
			Identifier: WebIdentifier,
			APIName:    "search",
			Content:    formatSearchContent(response),
			State:      state,
		}
		if len(response.Results) == 0 && strings.TrimSpace(response.ErrorDetail) != "" {
			result.Error = &ExecutionError{
				Type:    "search_failed",
				Message: response.ErrorDetail,
			}
		}
		return result, nil
	case "crawl":
		var request WebCrawlRequest
		if err := json.Unmarshal(call.Arguments, &request); err != nil {
			return ExecutionResult{}, fmt.Errorf("decode crawl arguments: %w", err)
		}
		response, err := service.Crawl(ctx, request)
		if err != nil {
			return ExecutionResult{}, err
		}
		state, err := json.Marshal(response)
		if err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{
			ID:         call.ID,
			Identifier: WebIdentifier,
			APIName:    "crawl",
			Content:    formatCrawlContent(response),
			State:      state,
		}, nil
	default:
		return ExecutionResult{}, fmt.Errorf("unsupported web api %q", call.APIName)
	}
}

func normalizeWebAPIName(value string) string {
	switch value {
	case "crawl_url":
		return "crawl"
	default:
		return value
	}
}

func DefaultWebService() WebToolService {
	defaultWebServiceOnce.Do(func() {
		defaultWebService = NewHTTPWebServiceFromEnv()
	})
	return defaultWebService
}

func NewHTTPWebServiceFromEnv() *HTTPWebService {
	httpClient := &http.Client{Timeout: defaultHTTPTimeout}
	searchImpls := map[string]webSearchProvider{
		"searxng":    newSearXNGProvider(httpClient, fallbackString(strings.TrimSpace(os.Getenv("TMA_WEB_SEARXNG_BASE_URL")), "http://localhost:8180")),
		"tavily":     newTavilyProvider(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_TAVILY_BASE_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_TAVILY_API_KEY"))),
		"brave":      newBraveProvider(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_BRAVE_BASE_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_BRAVE_API_KEY"))),
		"exa":        newExaProvider(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_EXA_BASE_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_EXA_API_KEY"))),
		"baidu":      newBaiduProvider(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_BAIDU_BASE_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_BAIDU_API_KEY"))),
		"search1api": newSearch1APIProvider(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_SEARCH1API_BASE_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_SEARCH1API_API_KEY"))),
	}
	crawlImpls := map[string]webCrawlerImpl{
		"naive":       newNaiveCrawler(httpClient),
		"jina":        newJinaCrawler(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_JINA_BASE_URL"))),
		"search1api":  newSearch1APICrawler(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_SEARCH1API_CRAWL_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_SEARCH1API_API_KEY"))),
		"browserless": newBrowserlessCrawler(httpClient, strings.TrimSpace(os.Getenv("TMA_WEB_BROWSERLESS_BASE_URL")), strings.TrimSpace(os.Getenv("TMA_WEB_BROWSERLESS_API_KEY"))),
	}

	service := &HTTPWebService{
		SearchOrder:       searchProviderOrderFromEnv(),
		CrawlerOrder:      crawlerOrderFromEnv(),
		SearchItemLimit:   envIntOrDefault("TMA_WEB_SEARCH_ITEM_LIMIT", defaultSearchItemLimit),
		CrawlContentLimit: envIntOrDefault("TMA_WEB_CRAWL_CONTENT_LIMIT", defaultCrawlContentLimit),
		CrawlerRetry:      envIntOrDefault("TMA_WEB_CRAWLER_RETRY", defaultCrawlerRetry),
	}
	for _, name := range service.SearchOrder {
		if provider := searchImpls[name]; provider != nil {
			service.SearchProviders = append(service.SearchProviders, provider)
		}
	}
	service.CrawlImplementors = map[string]webCrawlerImpl{}
	for _, name := range service.CrawlerOrder {
		if impl := crawlImpls[name]; impl != nil {
			service.CrawlImplementors[name] = impl
		}
	}
	return service
}

func (service *HTTPWebService) Search(ctx context.Context, request WebSearchRequest) (WebSearchResponse, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return WebSearchResponse{}, fmt.Errorf("search query is required")
	}
	startedAt := time.Now()
	params := webSearchQueryParams{
		Categories: cleanStringList(request.Categories),
		Engines:    cleanStringList(request.Engines),
		TimeRange:  strings.TrimSpace(request.TimeRange),
		Limit:      service.searchLimit(request.Limit),
	}

	lastResponse := WebSearchResponse{Query: query}
	diagnostics := []WebSearchAttemptDiagnostic{}
	for _, provider := range service.SearchProviders {
		for attemptIndex, attempt := range buildSearchFallbackAttempts(params) {
			attemptStartedAt := time.Now()
			diagnostic := WebSearchAttemptDiagnostic{
				Provider:   provider.Name(),
				Attempt:    attemptIndex + 1,
				Categories: append([]string(nil), attempt.Categories...),
				Engines:    append([]string(nil), attempt.Engines...),
				TimeRange:  attempt.TimeRange,
				Limit:      attempt.Limit,
			}
			response, err := provider.Query(ctx, query, attempt)
			if err != nil {
				diagnostic.CostTimeMS = time.Since(attemptStartedAt).Milliseconds()
				diagnostic.ErrorDetail = err.Error()
				diagnostics = append(diagnostics, diagnostic)
				lastResponse = WebSearchResponse{
					Query:       query,
					Provider:    provider.Name(),
					ErrorDetail: err.Error(),
					Diagnostics: append([]WebSearchAttemptDiagnostic(nil), diagnostics...),
				}
				continue
			}
			response.Query = query
			response.Provider = provider.Name()
			response.Results = truncateSearchResults(response.Results, service.searchLimit(request.Limit))
			response.ResultNumbers = len(response.Results)
			diagnostic.CostTimeMS = time.Since(attemptStartedAt).Milliseconds()
			diagnostic.ResultNumbers = response.ResultNumbers
			diagnostics = append(diagnostics, diagnostic)
			response.Diagnostics = append([]WebSearchAttemptDiagnostic(nil), diagnostics...)
			if response.ResultNumbers > 0 {
				response.CostTimeMS = time.Since(startedAt).Milliseconds()
				return response, nil
			}
			lastResponse = response
		}
	}
	lastResponse.Query = query
	lastResponse.CostTimeMS = time.Since(startedAt).Milliseconds()
	lastResponse.ResultNumbers = len(lastResponse.Results)
	lastResponse.Diagnostics = append([]WebSearchAttemptDiagnostic(nil), diagnostics...)
	return lastResponse, nil
}

func (service *HTTPWebService) Crawl(ctx context.Context, request WebCrawlRequest) (WebCrawlResponse, error) {
	urls := cleanStringList(append([]string{request.URL}, request.URLs...))
	if len(urls) == 0 {
		return WebCrawlResponse{}, fmt.Errorf("crawl url is required")
	}
	maxPages := request.MaxPages
	if maxPages <= 0 || maxPages > 10 {
		maxPages = len(urls)
	}
	if maxPages < len(urls) {
		urls = urls[:maxPages]
	}

	pages := make([]WebCrawlPage, 0, len(urls))
	for _, rawURL := range urls {
		page := service.crawlWithRetry(ctx, rawURL)
		page.Content = truncateText(page.Content, service.crawlContentLimit())
		pages = append(pages, page)
	}
	return WebCrawlResponse{Pages: pages}, nil
}

func (service *HTTPWebService) crawlWithRetry(ctx context.Context, rawURL string) WebCrawlPage {
	attempts := service.CrawlerRetry + 1
	if attempts < 1 {
		attempts = 1
	}
	var last WebCrawlPage
	var diagnostics []WebCrawlAttemptDiagnostic
	for index := 0; index < attempts; index++ {
		last = service.crawlOnce(ctx, rawURL, index+1)
		diagnostics = append(diagnostics, last.Attempts...)
		last.Attempts = append([]WebCrawlAttemptDiagnostic(nil), diagnostics...)
		if last.ContentType != "" {
			return last
		}
	}
	return last
}

func (service *HTTPWebService) crawlOnce(ctx context.Context, rawURL string, round int) WebCrawlPage {
	target, err := normalizeWebURL(rawURL)
	if err != nil {
		return WebCrawlPage{
			URL:          rawURL,
			Success:      false,
			ErrorType:    "invalid_url",
			ErrorMessage: err.Error(),
		}
	}
	rewrittenURL, implOrder := service.rewriteCrawlTarget(target)
	lastFailure := WebCrawlPage{
		URL:          target,
		FinalURL:     rewrittenURL,
		Success:      false,
		ErrorType:    "crawl_failed",
		ErrorMessage: "all crawl implementations failed",
	}
	var diagnostics []WebCrawlAttemptDiagnostic
	for _, name := range implOrder {
		impl := service.CrawlImplementors[name]
		if impl == nil {
			continue
		}
		attemptStartedAt := time.Now()
		diagnostic := WebCrawlAttemptDiagnostic{
			Round:    round,
			Impl:     name,
			URL:      target,
			FinalURL: rewrittenURL,
		}
		page, err := impl.Crawl(ctx, rewrittenURL)
		diagnostic.CostTimeMS = time.Since(attemptStartedAt).Milliseconds()
		if err != nil {
			diagnostic.ErrorType = "crawl_impl_failed"
			diagnostic.ErrorMessage = err.Error()
			diagnostics = append(diagnostics, diagnostic)
			lastFailure = WebCrawlPage{
				URL:          target,
				FinalURL:     rewrittenURL,
				Impl:         name,
				Success:      false,
				ErrorType:    "crawl_impl_failed",
				ErrorMessage: err.Error(),
				Attempts:     append([]WebCrawlAttemptDiagnostic(nil), diagnostics...),
			}
			continue
		}
		page.URL = target
		page.Impl = name
		page.Content = normalizeWhitespace(page.Content)
		diagnostic.ContentLength = len(page.Content)
		if len(page.Content) >= minCrawlerSuccessContentSize {
			page.Success = true
			diagnostics = append(diagnostics, diagnostic)
			page.Attempts = append([]WebCrawlAttemptDiagnostic(nil), diagnostics...)
			return page
		}
		diagnostic.ErrorType = "content_too_short"
		diagnostic.ErrorMessage = fmt.Sprintf("%s returned only %d characters", name, len(page.Content))
		diagnostics = append(diagnostics, diagnostic)
		lastFailure = page
		lastFailure.Success = false
		lastFailure.ErrorType = "content_too_short"
		lastFailure.ErrorMessage = fmt.Sprintf("%s returned only %d characters", name, len(page.Content))
		lastFailure.Attempts = append([]WebCrawlAttemptDiagnostic(nil), diagnostics...)
	}
	return lastFailure
}

func (service *HTTPWebService) rewriteCrawlTarget(target string) (string, []string) {
	order := append([]string(nil), service.CrawlerOrder...)
	if len(order) == 0 {
		order = envListOrDefault("TMA_WEB_CRAWLER_IMPLS", defaultCrawlerImplList)
	}
	lower := strings.ToLower(target)
	if rawURL, ok := transformGitHubBlobURL(target); ok {
		return rawURL, orderedUnique([]string{"naive", "jina"}, order)
	}
	if strings.Contains(lower, "youtube.com/") || strings.Contains(lower, "youtu.be/") || strings.Contains(lower, "reddit.com/") {
		return target, orderedUnique([]string{"search1api"}, order)
	}
	if strings.Contains(lower, "xiaohongshu.com/") || strings.Contains(lower, "xhslink.com/") {
		return target, orderedUnique([]string{"search1api", "jina"}, order)
	}
	return target, order
}

func (service *HTTPWebService) searchLimit(requested int) int {
	limit := service.SearchItemLimit
	if limit <= 0 {
		limit = defaultSearchItemLimit
	}
	if requested > 0 && requested < limit {
		limit = requested
	}
	if limit > defaultSearchItemLimit {
		limit = defaultSearchItemLimit
	}
	if limit <= 0 {
		limit = defaultSearchProviderLimit
	}
	return limit
}

func (service *HTTPWebService) crawlContentLimit() int {
	if service.CrawlContentLimit <= 0 {
		return defaultCrawlContentLimit
	}
	return service.CrawlContentLimit
}

func buildSearchFallbackAttempts(base webSearchQueryParams) []webSearchQueryParams {
	attempts := []webSearchQueryParams{base}
	if len(base.Engines) > 0 {
		next := base
		next.Engines = nil
		attempts = append(attempts, next)
	}
	if len(base.Categories) > 0 || len(base.Engines) > 0 || base.TimeRange != "" {
		attempts = append(attempts, webSearchQueryParams{Limit: base.Limit})
	}
	return uniqueSearchAttempts(attempts)
}

func searchProviderOrderFromEnv() []string {
	if configured := strings.TrimSpace(os.Getenv("TMA_WEB_SEARCH_PROVIDERS")); configured != "" {
		return cleanStringList(strings.Split(configured, ","))
	}
	keyedProviders := []struct {
		name   string
		envKey string
	}{
		{name: "tavily", envKey: "TMA_WEB_TAVILY_API_KEY"},
		{name: "brave", envKey: "TMA_WEB_BRAVE_API_KEY"},
		{name: "exa", envKey: "TMA_WEB_EXA_API_KEY"},
		{name: "baidu", envKey: "TMA_WEB_BAIDU_API_KEY"},
		{name: "search1api", envKey: "TMA_WEB_SEARCH1API_API_KEY"},
	}
	order := make([]string, 0, len(keyedProviders)+1)
	for _, provider := range keyedProviders {
		if strings.TrimSpace(os.Getenv(provider.envKey)) != "" {
			order = append(order, provider.name)
		}
	}
	return orderedUnique(order, []string{"searxng"})
}

func crawlerOrderFromEnv() []string {
	order := envListOrDefault("TMA_WEB_CRAWLER_IMPLS", defaultCrawlerImplList)
	switch strings.TrimSpace(strings.ToLower(os.Getenv("TMA_WEB_BROWSERLESS_PRIORITY"))) {
	case "first":
		return orderedUnique([]string{"browserless"}, order)
	case "after_naive", "after-naive":
		withoutBrowserless := make([]string, 0, len(order))
		for _, name := range order {
			if name != "browserless" {
				withoutBrowserless = append(withoutBrowserless, name)
			}
		}
		reordered := make([]string, 0, len(order)+1)
		inserted := false
		for _, name := range withoutBrowserless {
			reordered = append(reordered, name)
			if name == "naive" {
				reordered = append(reordered, "browserless")
				inserted = true
			}
		}
		if !inserted {
			reordered = append(reordered, "browserless")
		}
		return orderedUnique(nil, reordered)
	case "last", "":
		return order
	default:
		return order
	}
}

func uniqueSearchAttempts(values []webSearchQueryParams) []webSearchQueryParams {
	unique := make([]webSearchQueryParams, 0, len(values))
	for _, value := range values {
		duplicate := false
		for _, existing := range unique {
			if slices.Equal(existing.Categories, value.Categories) &&
				slices.Equal(existing.Engines, value.Engines) &&
				existing.TimeRange == value.TimeRange &&
				existing.Limit == value.Limit {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, value)
		}
	}
	return unique
}

func newSearXNGProvider(client *http.Client, baseURL string) webSearchProvider {
	return searxngSearchProvider{client: httpWebClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client:  client,
	}}
}

type searxngSearchProvider struct {
	client httpWebClient
}

func (provider searxngSearchProvider) Name() string { return "searxng" }

func (provider searxngSearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	if provider.client.BaseURL == "" {
		return WebSearchResponse{}, errors.New("searxng base url is not configured")
	}
	values := neturl.Values{}
	values.Set("q", query)
	values.Set("format", "json")
	if len(params.Categories) > 0 {
		values.Set("categories", strings.Join(params.Categories, ","))
	}
	if len(params.Engines) > 0 {
		values.Set("engines", strings.Join(params.Engines, ","))
	}
	if params.TimeRange != "" {
		values.Set("time_range", normalizeSearXNGTimeRange(params.TimeRange))
	}
	body, err := provider.client.doJSONRequest(ctx, http.MethodGet, provider.client.BaseURL+"/search?"+values.Encode(), nil, nil)
	if err != nil {
		return WebSearchResponse{}, err
	}
	var payload struct {
		Results             []map[string]any `json:"results"`
		UnresponsiveEngines [][]string       `json:"unresponsive_engines"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebSearchResponse{}, err
	}
	return WebSearchResponse{
		Results:             searchResultsFromMaps(payload.Results, provider.Name()),
		UnresponsiveEngines: webSearchUnresponsiveEnginesFromPairs(payload.UnresponsiveEngines),
	}, nil
}

func normalizeSearXNGTimeRange(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "d1":
		return "day"
	case "w1":
		return "week"
	case "m1":
		return "month"
	case "y1":
		return "year"
	default:
		return value
	}
}

func newTavilyProvider(client *http.Client, baseURL string, apiKey string) webSearchProvider {
	if baseURL == "" {
		baseURL = "https://api.tavily.com/search"
	}
	return tavilySearchProvider{client: httpWebClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	}}
}

type tavilySearchProvider struct {
	client httpWebClient
}

func (provider tavilySearchProvider) Name() string { return "tavily" }

func (provider tavilySearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	if provider.client.APIKey == "" {
		return WebSearchResponse{}, errors.New("tavily api key is not configured")
	}
	payload := map[string]any{
		"api_key":     provider.client.APIKey,
		"query":       query,
		"max_results": params.Limit,
		"topic":       "general",
	}
	body, err := provider.client.doJSONRequest(ctx, http.MethodPost, provider.client.BaseURL, payload, nil)
	if err != nil {
		return WebSearchResponse{}, err
	}
	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return WebSearchResponse{}, err
	}
	return WebSearchResponse{Results: searchResultsFromMaps(decoded.Results, provider.Name())}, nil
}

func newBraveProvider(client *http.Client, baseURL string, apiKey string) webSearchProvider {
	if baseURL == "" {
		baseURL = "https://api.search.brave.com/res/v1/web/search"
	}
	return braveSearchProvider{client: httpWebClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	}}
}

type braveSearchProvider struct {
	client httpWebClient
}

func (provider braveSearchProvider) Name() string { return "brave" }

func (provider braveSearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	if provider.client.APIKey == "" {
		return WebSearchResponse{}, errors.New("brave api key is not configured")
	}
	values := neturl.Values{}
	values.Set("q", query)
	values.Set("count", strconv.Itoa(maxInt(params.Limit, 1)))
	if params.TimeRange != "" {
		values.Set("freshness", params.TimeRange)
	}
	body, err := provider.client.doJSONRequest(ctx, http.MethodGet, provider.client.BaseURL+"?"+values.Encode(), nil, map[string]string{
		"X-Subscription-Token": provider.client.APIKey,
	})
	if err != nil {
		return WebSearchResponse{}, err
	}
	var decoded struct {
		Web struct {
			Results []map[string]any `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return WebSearchResponse{}, err
	}
	return WebSearchResponse{Results: searchResultsFromMaps(decoded.Web.Results, provider.Name())}, nil
}

func newExaProvider(client *http.Client, baseURL string, apiKey string) webSearchProvider {
	if baseURL == "" {
		baseURL = "https://api.exa.ai/search"
	}
	return exaSearchProvider{client: httpWebClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	}}
}

type exaSearchProvider struct {
	client httpWebClient
}

func (provider exaSearchProvider) Name() string { return "exa" }

func (provider exaSearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	if provider.client.APIKey == "" {
		return WebSearchResponse{}, errors.New("exa api key is not configured")
	}
	payload := map[string]any{
		"query":      query,
		"type":       "auto",
		"numResults": maxInt(params.Limit, 1),
		"contents": map[string]any{
			"text": map[string]any{
				"maxCharacters": 1000,
			},
		},
	}
	body, err := provider.client.doJSONRequest(ctx, http.MethodPost, provider.client.BaseURL, payload, map[string]string{
		"x-api-key":     provider.client.APIKey,
		"Authorization": "Bearer " + provider.client.APIKey,
	})
	if err != nil {
		return WebSearchResponse{}, err
	}
	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return WebSearchResponse{}, err
	}
	return WebSearchResponse{Results: searchResultsFromMaps(decoded.Results, provider.Name())}, nil
}

func newBaiduProvider(client *http.Client, baseURL string, apiKey string) webSearchProvider {
	if baseURL == "" {
		baseURL = "https://qianfan.baidubce.com/v2/ai_search"
	}
	return baiduSearchProvider{client: httpWebClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	}}
}

type baiduSearchProvider struct {
	client httpWebClient
}

func (provider baiduSearchProvider) Name() string { return "baidu" }

func (provider baiduSearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	if provider.client.APIKey == "" {
		return WebSearchResponse{}, errors.New("baidu api key is not configured")
	}
	payload := map[string]any{
		"query": query,
		"q":     query,
		"count": maxInt(params.Limit, 1),
	}
	body, err := provider.client.doJSONRequest(ctx, http.MethodPost, provider.client.BaseURL, payload, map[string]string{
		"Authorization": "Bearer " + provider.client.APIKey,
		"X-API-Key":     provider.client.APIKey,
	})
	if err != nil {
		return WebSearchResponse{}, err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return WebSearchResponse{}, err
	}
	return WebSearchResponse{Results: searchResultsFromAny(decoded, provider.Name())}, nil
}

func newSearch1APIProvider(client *http.Client, baseURL string, apiKey string) webSearchProvider {
	if baseURL == "" {
		baseURL = "https://api.search1api.com/search"
	}
	return search1apiSearchProvider{client: httpWebClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	}}
}

type search1apiSearchProvider struct {
	client httpWebClient
}

func (provider search1apiSearchProvider) Name() string { return "search1api" }

func (provider search1apiSearchProvider) Query(ctx context.Context, query string, params webSearchQueryParams) (WebSearchResponse, error) {
	if provider.client.APIKey == "" {
		return WebSearchResponse{}, errors.New("search1api api key is not configured")
	}
	payload := map[string]any{
		"query": query,
		"size":  params.Limit,
	}
	if len(params.Engines) > 0 {
		payload["search_engines"] = params.Engines
	}
	if params.TimeRange != "" {
		payload["time_range"] = params.TimeRange
	}
	body, err := provider.client.doJSONRequest(ctx, http.MethodPost, provider.client.BaseURL, payload, map[string]string{
		"Authorization": "Bearer " + provider.client.APIKey,
	})
	if err != nil {
		return WebSearchResponse{}, err
	}
	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return WebSearchResponse{}, err
	}
	return WebSearchResponse{Results: searchResultsFromMaps(decoded.Results, provider.Name())}, nil
}

func newNaiveCrawler(client *http.Client) webCrawlerImpl {
	return naiveCrawler{client: client}
}

type naiveCrawler struct {
	client *http.Client
}

func (crawler naiveCrawler) Name() string { return "naive" }

func (crawler naiveCrawler) Crawl(ctx context.Context, url string) (WebCrawlPage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return WebCrawlPage{}, err
	}
	response, err := crawler.client.Do(request)
	if err != nil {
		return WebCrawlPage{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return WebCrawlPage{}, fmt.Errorf("http %d", response.StatusCode)
	}
	contentType := response.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCrawlResponseBodyBytes))
	if err != nil {
		return WebCrawlPage{}, err
	}
	page := WebCrawlPage{
		FinalURL:    response.Request.URL.String(),
		ContentType: contentType,
	}
	if looksLikeHTML(contentType, body) {
		page.Title, page.Content = extractHTMLDocument(body)
		return page, nil
	}
	page.Content = string(body)
	return page, nil
}

func newJinaCrawler(client *http.Client, baseURL string) webCrawlerImpl {
	if baseURL == "" {
		baseURL = "https://r.jina.ai/http://"
	}
	return jinaCrawler{client: client, baseURL: baseURL}
}

type jinaCrawler struct {
	client  *http.Client
	baseURL string
}

func (crawler jinaCrawler) Name() string { return "jina" }

func (crawler jinaCrawler) Crawl(ctx context.Context, url string) (WebCrawlPage, error) {
	target := crawler.baseURL + strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return WebCrawlPage{}, err
	}
	response, err := crawler.client.Do(request)
	if err != nil {
		return WebCrawlPage{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return WebCrawlPage{}, fmt.Errorf("http %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCrawlResponseBodyBytes))
	if err != nil {
		return WebCrawlPage{}, err
	}
	return WebCrawlPage{
		FinalURL:    url,
		ContentType: fallbackString(response.Header.Get("Content-Type"), "text/plain"),
		Content:     string(body),
	}, nil
}

func newSearch1APICrawler(client *http.Client, baseURL string, apiKey string) webCrawlerImpl {
	if baseURL == "" {
		baseURL = "https://api.search1api.com/crawl"
	}
	return search1apiCrawler{client: httpWebClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	}}
}

type search1apiCrawler struct {
	client httpWebClient
}

func (crawler search1apiCrawler) Name() string { return "search1api" }

func (crawler search1apiCrawler) Crawl(ctx context.Context, url string) (WebCrawlPage, error) {
	if crawler.client.APIKey == "" {
		return WebCrawlPage{}, errors.New("search1api api key is not configured")
	}
	body, err := crawler.client.doJSONRequest(ctx, http.MethodPost, crawler.client.BaseURL, map[string]any{
		"url": url,
	}, map[string]string{
		"Authorization": "Bearer " + crawler.client.APIKey,
	})
	if err != nil {
		return WebCrawlPage{}, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return WebCrawlPage{}, err
	}
	page := WebCrawlPage{
		FinalURL:    stringValue(decoded["url"]),
		Title:       stringValue(decoded["title"]),
		ContentType: fallbackString(stringValue(decoded["content_type"]), "text/plain"),
		Content:     firstNonEmptyWebString(decoded["content"], decoded["text"], decoded["markdown"]),
	}
	return page, nil
}

func newBrowserlessCrawler(client *http.Client, baseURL string, apiKey string) webCrawlerImpl {
	if baseURL == "" {
		baseURL = "https://chrome.browserless.io/content"
	}
	return browserlessCrawler{
		client: httpWebClient{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Client:  client,
		},
		waitSelector:          strings.TrimSpace(os.Getenv("TMA_WEB_BROWSERLESS_WAIT_SELECTOR")),
		waitForTimeoutMS:      envIntOrDefault("TMA_WEB_BROWSERLESS_WAIT_TIMEOUT_MS", 0),
		waitSelectorTimeoutMS: envIntOrDefault("TMA_WEB_BROWSERLESS_WAIT_SELECTOR_TIMEOUT_MS", 30000),
		gotoTimeoutMS:         envIntOrDefault("TMA_WEB_BROWSERLESS_GOTO_TIMEOUT_MS", 15000),
		requestTimeoutMS:      envIntOrDefault("TMA_WEB_BROWSERLESS_REQUEST_TIMEOUT_MS", 0),
		waitUntil:             fallbackString(strings.TrimSpace(os.Getenv("TMA_WEB_BROWSERLESS_WAIT_UNTIL")), "networkidle0"),
		userAgent:             strings.TrimSpace(os.Getenv("TMA_WEB_BROWSERLESS_USER_AGENT")),
		rejectResourceTypes:   envListOrDefault("TMA_WEB_BROWSERLESS_REJECT_RESOURCE_TYPES", "image,media,font,stylesheet"),
		bestAttempt:           envBoolOrDefault("TMA_WEB_BROWSERLESS_BEST_ATTEMPT", true),
	}
}

type browserlessCrawler struct {
	client                httpWebClient
	waitSelector          string
	waitForTimeoutMS      int
	waitSelectorTimeoutMS int
	gotoTimeoutMS         int
	requestTimeoutMS      int
	waitUntil             string
	userAgent             string
	rejectResourceTypes   []string
	bestAttempt           bool
}

func (crawler browserlessCrawler) Name() string { return "browserless" }

func (crawler browserlessCrawler) Crawl(ctx context.Context, url string) (WebCrawlPage, error) {
	target := crawler.client.BaseURL
	if crawler.client.APIKey != "" {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + "token=" + neturl.QueryEscape(crawler.client.APIKey)
	}
	if crawler.requestTimeoutMS > 0 {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + "timeout=" + strconv.Itoa(crawler.requestTimeoutMS)
	}
	body, err := crawler.client.doJSONRequest(ctx, http.MethodPost, target, crawler.payload(url), nil)
	if err != nil {
		return WebCrawlPage{}, err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err == nil {
		if object, ok := decoded.(map[string]any); ok {
			content := firstNonEmptyWebString(object["content"], object["html"], object["data"])
			title, text := extractHTMLDocument([]byte(content))
			return WebCrawlPage{
				FinalURL:    url,
				Title:       title,
				ContentType: "text/html",
				Content:     text,
			}, nil
		}
	}
	title, text := extractHTMLDocument(body)
	return WebCrawlPage{
		FinalURL:    url,
		Title:       title,
		ContentType: "text/html",
		Content:     text,
	}, nil
}

func (crawler browserlessCrawler) payload(url string) map[string]any {
	payload := map[string]any{
		"url":         url,
		"bestAttempt": crawler.bestAttempt,
		"gotoOptions": map[string]any{
			"waitUntil": crawler.waitUntil,
			"timeout":   maxInt(crawler.gotoTimeoutMS, 1),
		},
	}
	if crawler.waitForTimeoutMS > 0 {
		payload["waitForTimeout"] = crawler.waitForTimeoutMS
	}
	if crawler.waitSelector != "" {
		payload["waitForSelector"] = map[string]any{
			"selector": crawler.waitSelector,
			"timeout":  maxInt(crawler.waitSelectorTimeoutMS, 1),
		}
	}
	if crawler.userAgent != "" {
		payload["userAgent"] = crawler.userAgent
	}
	if len(crawler.rejectResourceTypes) > 0 {
		payload["rejectResourceTypes"] = crawler.rejectResourceTypes
	}
	return payload
}

func (client httpWebClient) doJSONRequest(ctx context.Context, method string, target string, payload any, headers map[string]string) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
	}
	if client.Client == nil {
		client.Client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	httpClient := client.Client
	if _, hasDeadline := ctx.Deadline(); hasDeadline && httpClient.Timeout > 0 {
		cloned := *httpClient
		cloned.Timeout = 0
		httpClient = &cloned
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("http %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(response.Body, maxCrawlResponseBodyBytes))
}

func formatSearchContent(response WebSearchResponse) string {
	if len(response.Results) == 0 {
		if strings.TrimSpace(response.ErrorDetail) != "" {
			return response.ErrorDetail
		}
		return "No search results found."
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Provider: %s\n", response.Provider)
	for index, result := range response.Results {
		fmt.Fprintf(&builder, "%d. %s\n%s\n", index+1, fallbackString(result.Title, result.URL), result.URL)
		if result.Snippet != "" {
			builder.WriteString(result.Snippet)
			builder.WriteByte('\n')
		}
	}
	return strings.TrimSpace(builder.String())
}

func formatCrawlContent(response WebCrawlResponse) string {
	if len(response.Pages) == 0 {
		return "<pages />"
	}
	var builder strings.Builder
	builder.WriteString("<pages>\n")
	for _, page := range response.Pages {
		fmt.Fprintf(&builder, "  <page url=%q success=%q impl=%q>\n", page.URL, strconv.FormatBool(page.Success), page.Impl)
		if page.Title != "" {
			fmt.Fprintf(&builder, "    <title>%s</title>\n", escapeXMLText(page.Title))
		}
		if page.Success {
			fmt.Fprintf(&builder, "    <content>%s</content>\n", escapeXMLText(page.Content))
		} else {
			fmt.Fprintf(&builder, "    <error type=%q>%s</error>\n", page.ErrorType, escapeXMLText(page.ErrorMessage))
		}
		builder.WriteString("  </page>\n")
	}
	builder.WriteString("</pages>")
	return builder.String()
}

func searchResultsFromMaps(results []map[string]any, fallbackSource string) []WebSearchResult {
	items := make([]WebSearchResult, 0, len(results))
	for _, result := range results {
		item := WebSearchResult{
			Title:       firstNonEmptyWebString(result["title"], result["name"]),
			URL:         firstNonEmptyWebString(result["url"], result["link"], result["href"], result["uri"]),
			Snippet:     firstNonEmptyWebString(result["content"], result["snippet"], result["description"], result["summary"], result["text"], result["markdown"]),
			Source:      firstNonEmptyWebString(result["source"], result["engine"], fallbackSource),
			PublishedAt: firstNonEmptyWebString(result["published_at"], result["published_date"], result["publishedDate"], result["datePublished"], result["age"]),
		}
		if item.URL == "" {
			continue
		}
		item.Snippet = normalizeWhitespace(item.Snippet)
		items = append(items, item)
	}
	return items
}

func webSearchUnresponsiveEnginesFromPairs(values [][]string) []WebSearchUnresponsiveEngine {
	items := make([]WebSearchUnresponsiveEngine, 0, len(values))
	for _, value := range values {
		if len(value) == 0 || strings.TrimSpace(value[0]) == "" {
			continue
		}
		item := WebSearchUnresponsiveEngine{Name: strings.TrimSpace(value[0])}
		if len(value) > 1 {
			item.Error = strings.TrimSpace(value[1])
		}
		items = append(items, item)
	}
	return items
}

func searchResultsFromAny(value any, fallbackSource string) []WebSearchResult {
	return searchResultsFromMaps(searchResultMapsFromAny(value, 0), fallbackSource)
}

func searchResultMapsFromAny(value any, depth int) []map[string]any {
	if depth > 4 {
		return nil
	}
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		results := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			results = append(results, searchResultMapsFromAny(item, depth+1)...)
		}
		return results
	case map[string]any:
		if firstNonEmptyWebString(typed["url"], typed["link"], typed["href"], typed["uri"]) != "" {
			return []map[string]any{typed}
		}
		for _, key := range []string{"results", "search_results", "organic_results", "items", "references", "documents", "list", "data"} {
			if nested, ok := typed[key]; ok {
				if results := searchResultMapsFromAny(nested, depth+1); len(results) > 0 {
					return results
				}
			}
		}
	}
	return nil
}

func truncateSearchResults(results []WebSearchResult, limit int) []WebSearchResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return append([]WebSearchResult(nil), results[:limit]...)
}

func transformGitHubBlobURL(value string) (string, bool) {
	parsed, err := neturl.Parse(value)
	if err != nil {
		return "", false
	}
	if !strings.EqualFold(parsed.Host, "github.com") {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "blob" {
		return "", false
	}
	return "https://raw.githubusercontent.com/" + strings.Join([]string{parts[0], parts[1], parts[3], strings.Join(parts[4:], "/")}, "/"), true
}

func normalizeWebURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("url is empty")
	}
	parsed, err := neturl.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		parsed, err = neturl.Parse("https://" + value)
		if err != nil {
			return "", err
		}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("url host is required")
	}
	return parsed.String(), nil
}

func extractHTMLDocument(body []byte) (string, string) {
	source := string(body)
	title := htmlEntityDecode(extractFirstSubmatch(source, `(?is)<title[^>]*>(.*?)</title>`))
	source = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(source, " ")
	source = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(source, " ")
	source = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(source, " ")
	return normalizeWhitespace(title), normalizeWhitespace(htmlEntityDecode(source))
}

func htmlEntityDecode(value string) string {
	return html.UnescapeString(value)
}

func extractFirstSubmatch(value string, pattern string) string {
	matches := regexp.MustCompile(pattern).FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func looksLikeHTML(contentType string, body []byte) bool {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml") {
		return true
	}
	sample := strings.ToLower(string(body))
	return strings.Contains(sample, "<html") || strings.Contains(sample, "<body") || strings.Contains(sample, "<title")
}

func normalizeWhitespace(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func orderedUnique(prefix []string, rest []string) []string {
	ordered := make([]string, 0, len(prefix)+len(rest))
	seen := map[string]bool{}
	for _, values := range [][]string{prefix, rest} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			ordered = append(ordered, value)
		}
	}
	return ordered
}

func envListOrDefault(key string, fallback string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		value = fallback
	}
	return cleanStringList(strings.Split(value, ","))
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstNonEmptyWebString(values ...any) string {
	for _, value := range values {
		if result := strings.TrimSpace(stringValue(value)); result != "" {
			return result
		}
	}
	return ""
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case []string:
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				return item
			}
		}
	case []any:
		for _, item := range typed {
			if result := stringValue(item); strings.TrimSpace(result) != "" {
				return result
			}
		}
	}
	return ""
}

func escapeXMLText(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func maxInt(value int, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}
