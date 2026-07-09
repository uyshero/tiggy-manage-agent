package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/serverconfig"
	webtools "tiggy-manage-agent/internal/tools"
)

const (
	defaultWebDoctorSearXNGURL = "http://localhost:8180"
	defaultWebDoctorQuery      = "测试"
	defaultWebDoctorTimeout    = 15
)

type webDoctorProvider struct {
	Name             string `json:"name"`
	BaseURL          string `json:"base_url,omitempty"`
	APIKeyEnv        string `json:"api_key_env,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	InSearchOrder    bool   `json:"in_search_order"`
}

type webDoctorUnresponsiveEngine struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
}

type webDoctorSearXNG struct {
	BaseURL              string                        `json:"base_url"`
	Query                string                        `json:"query"`
	Reachable            bool                          `json:"reachable"`
	ResultCount          int                           `json:"result_count"`
	Engines              []string                      `json:"engines,omitempty"`
	UnresponsiveEngines  []webDoctorUnresponsiveEngine `json:"unresponsive_engines,omitempty"`
	BlockedEnginesActive []string                      `json:"blocked_engines_active,omitempty"`
	Error                string                        `json:"error,omitempty"`
}

type webDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type webDoctorSearXNGPayload struct {
	Results []struct {
		Engine  string   `json:"engine"`
		Engines []string `json:"engines"`
	} `json:"results"`
	UnresponsiveEngines [][]string `json:"unresponsive_engines"`
}

type webDoctorReport struct {
	SearchOrder  []string            `json:"search_order"`
	Providers    []webDoctorProvider `json:"providers"`
	CrawlerOrder []string            `json:"crawler_order"`
	SearXNG      webDoctorSearXNG    `json:"searxng"`
	Checks       []webDoctorCheck    `json:"checks"`
	OK           bool                `json:"ok"`
}

type webDoctorConfig struct {
	SearXNGURL string
	Query      string
	Timeout    time.Duration
}

func commandWeb(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("web command requires a subcommand")
	}
	switch args[0] {
	case "doctor":
		return commandWebDoctor(args[1:])
	case "search":
		return commandWebSearch(args[1:])
	case "crawl":
		return commandWebCrawl(args[1:])
	default:
		return fmt.Errorf("unknown web subcommand %q", args[0])
	}
}

func commandWebSearch(args []string) error {
	if err := serverconfig.LoadDotEnv(".env"); err != nil {
		return err
	}

	flags := flag.NewFlagSet("web search", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	var query string
	var categories string
	var engines string
	var timeRange string
	var limit int
	var timeoutSeconds int
	flags.StringVar(&query, "query", "", "search query")
	flags.StringVar(&categories, "categories", "", "comma-separated search categories")
	flags.StringVar(&engines, "engines", "", "comma-separated search engines")
	flags.StringVar(&timeRange, "time-range", "", "search time range")
	flags.IntVar(&limit, "limit", 10, "max result count")
	flags.IntVar(&timeoutSeconds, "timeout", 30, "timeout in seconds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("web search requires --query")
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	response, err := webtools.NewHTTPWebServiceFromEnv().Search(ctx, webtools.WebSearchRequest{
		Query:      query,
		Categories: webDoctorCleanList(strings.Split(categories, ",")),
		Engines:    webDoctorCleanList(strings.Split(engines, ",")),
		TimeRange:  timeRange,
		Limit:      limit,
	})
	if printErr := printJSON(response); printErr != nil {
		return printErr
	}
	if err != nil {
		return err
	}
	if len(response.Results) == 0 && strings.TrimSpace(response.ErrorDetail) != "" {
		return fmt.Errorf("web search failed: %s", response.ErrorDetail)
	}
	return nil
}

func commandWebCrawl(args []string) error {
	if err := serverconfig.LoadDotEnv(".env"); err != nil {
		return err
	}

	flags := flag.NewFlagSet("web crawl", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	var targetURL string
	var urls string
	var impl string
	var maxPages int
	var timeoutSeconds int
	var attemptsOnly bool
	var contentOnly bool
	flags.StringVar(&targetURL, "url", "", "URL to crawl")
	flags.StringVar(&urls, "urls", "", "comma-separated URLs to crawl")
	flags.StringVar(&impl, "impl", "", "force a single crawler implementation: jina|naive|search1api|browserless")
	flags.IntVar(&maxPages, "max-pages", 1, "max pages to crawl")
	flags.IntVar(&timeoutSeconds, "timeout", 45, "timeout in seconds")
	flags.BoolVar(&attemptsOnly, "attempts-only", false, "print only crawl attempt diagnostics")
	flags.BoolVar(&contentOnly, "content-only", false, "print only crawled page content")
	if err := flags.Parse(args); err != nil {
		return err
	}
	allURLs := webDoctorCleanList(append([]string{targetURL}, strings.Split(urls, ",")...))
	if len(allURLs) == 0 {
		return fmt.Errorf("web crawl requires --url or --urls")
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 45
	}
	if attemptsOnly && contentOnly {
		return fmt.Errorf("web crawl accepts only one of --attempts-only or --content-only")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	service := webtools.NewHTTPWebServiceFromEnv()
	if strings.TrimSpace(impl) != "" {
		service.CrawlerOrder = []string{strings.TrimSpace(impl)}
	}
	response, err := service.Crawl(ctx, webtools.WebCrawlRequest{
		URL:      targetURL,
		URLs:     webDoctorCleanList(strings.Split(urls, ",")),
		MaxPages: maxPages,
	})
	if attemptsOnly {
		if printErr := printJSON(webCrawlAttemptsOutput(response)); printErr != nil {
			return printErr
		}
	} else if contentOnly {
		if printErr := printWebCrawlContentOnly(response); printErr != nil {
			return printErr
		}
	} else {
		if printErr := printJSON(response); printErr != nil {
			return printErr
		}
	}
	if err != nil {
		return err
	}
	for _, page := range response.Pages {
		if !page.Success {
			return fmt.Errorf("web crawl failed for %s: %s", page.URL, page.ErrorMessage)
		}
	}
	return nil
}

func webCrawlAttemptsOutput(response webtools.WebCrawlResponse) []map[string]any {
	items := make([]map[string]any, 0, len(response.Pages))
	for _, page := range response.Pages {
		items = append(items, map[string]any{
			"url":            page.URL,
			"success":        page.Success,
			"impl":           page.Impl,
			"error_type":     page.ErrorType,
			"error_message":  page.ErrorMessage,
			"attempts":       page.Attempts,
			"attempts_count": len(page.Attempts),
		})
	}
	return items
}

func printWebCrawlContentOnly(response webtools.WebCrawlResponse) error {
	for index, page := range response.Pages {
		if index > 0 {
			fmt.Println()
		}
		if page.Title != "" {
			fmt.Printf("# %s\n\n", page.Title)
		}
		fmt.Println(page.Content)
	}
	return nil
}

func commandWebDoctor(args []string) error {
	if err := serverconfig.LoadDotEnv(".env"); err != nil {
		return err
	}

	flags := flag.NewFlagSet("web doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	searxngURL := envOrDefaultCLI("TMA_WEB_SEARXNG_BASE_URL", defaultWebDoctorSearXNGURL)
	query := defaultWebDoctorQuery
	timeoutSeconds := defaultWebDoctorTimeout
	flags.StringVar(&searxngURL, "searxng-url", searxngURL, "SearXNG base URL")
	flags.StringVar(&query, "query", query, "probe query")
	flags.IntVar(&timeoutSeconds, "timeout", timeoutSeconds, "HTTP timeout in seconds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultWebDoctorTimeout
	}

	report := runWebDoctor(webDoctorConfig{
		SearXNGURL: searxngURL,
		Query:      query,
		Timeout:    time.Duration(timeoutSeconds) * time.Second,
	})
	if err := printJSON(report); err != nil {
		return err
	}
	if !report.OK {
		return fmt.Errorf("web doctor found failed checks")
	}
	return nil
}

func runWebDoctor(config webDoctorConfig) webDoctorReport {
	report := webDoctorReport{
		SearchOrder:  webDoctorSearchOrderFromEnv(),
		CrawlerOrder: webDoctorEnvListOrDefault("TMA_WEB_CRAWLER_IMPLS", "jina,naive,search1api,browserless"),
		OK:           true,
	}
	report.Providers = webDoctorProviders(report.SearchOrder)
	report.SearXNG = probeWebDoctorSearXNG(config)

	report.addCheck("search_order", len(report.SearchOrder) > 0, strings.Join(report.SearchOrder, " -> "))
	report.addCheck("searxng_json", report.SearXNG.Reachable, report.SearXNG.Error)
	report.addCheck("blocked_engines", len(report.SearXNG.BlockedEnginesActive) == 0, strings.Join(report.SearXNG.BlockedEnginesActive, ", "))
	return report
}

func (report *webDoctorReport) addCheck(name string, ok bool, message string) {
	status := "ok"
	if !ok {
		status = "failed"
		report.OK = false
	}
	report.Checks = append(report.Checks, webDoctorCheck{
		Name:    name,
		Status:  status,
		Message: strings.TrimSpace(message),
	})
}

func webDoctorSearchOrderFromEnv() []string {
	if configured := strings.TrimSpace(os.Getenv("TMA_WEB_SEARCH_PROVIDERS")); configured != "" {
		return webDoctorCleanList(strings.Split(configured, ","))
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
	return webDoctorOrderedUnique(order, []string{"searxng"})
}

func webDoctorProviders(searchOrder []string) []webDoctorProvider {
	inOrder := map[string]bool{}
	for _, name := range searchOrder {
		inOrder[name] = true
	}
	providers := []webDoctorProvider{
		{Name: "tavily", BaseURL: envOrDefaultCLI("TMA_WEB_TAVILY_BASE_URL", "https://api.tavily.com/search"), APIKeyEnv: "TMA_WEB_TAVILY_API_KEY"},
		{Name: "brave", BaseURL: envOrDefaultCLI("TMA_WEB_BRAVE_BASE_URL", "https://api.search.brave.com/res/v1/web/search"), APIKeyEnv: "TMA_WEB_BRAVE_API_KEY"},
		{Name: "exa", BaseURL: envOrDefaultCLI("TMA_WEB_EXA_BASE_URL", "https://api.exa.ai/search"), APIKeyEnv: "TMA_WEB_EXA_API_KEY"},
		{Name: "baidu", BaseURL: envOrDefaultCLI("TMA_WEB_BAIDU_BASE_URL", "https://qianfan.baidubce.com/v2/ai_search"), APIKeyEnv: "TMA_WEB_BAIDU_API_KEY"},
		{Name: "search1api", BaseURL: envOrDefaultCLI("TMA_WEB_SEARCH1API_BASE_URL", "https://api.search1api.com/search"), APIKeyEnv: "TMA_WEB_SEARCH1API_API_KEY"},
		{Name: "searxng", BaseURL: envOrDefaultCLI("TMA_WEB_SEARXNG_BASE_URL", defaultWebDoctorSearXNGURL)},
	}
	for index := range providers {
		providers[index].InSearchOrder = inOrder[providers[index].Name]
		if providers[index].APIKeyEnv != "" {
			providers[index].APIKeyConfigured = strings.TrimSpace(os.Getenv(providers[index].APIKeyEnv)) != ""
		}
	}
	return providers
}

func probeWebDoctorSearXNG(config webDoctorConfig) webDoctorSearXNG {
	baseURL := strings.TrimRight(strings.TrimSpace(config.SearXNGURL), "/")
	if baseURL == "" {
		baseURL = defaultWebDoctorSearXNGURL
	}
	query := strings.TrimSpace(config.Query)
	if query == "" {
		query = defaultWebDoctorQuery
	}
	result := webDoctorSearXNG{
		BaseURL: baseURL,
		Query:   query,
	}
	values := url.Values{}
	values.Set("q", query)
	values.Set("format", "json")
	target := baseURL + "/search?" + values.Encode()

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = time.Duration(defaultWebDoctorTimeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		result.Error = "http " + strconv.Itoa(response.StatusCode)
		return result
	}
	var payload webDoctorSearXNGPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Reachable = true
	applyWebDoctorSearXNGPayload(&result, payload)
	return result
}

func applyWebDoctorSearXNGPayload(result *webDoctorSearXNG, payload webDoctorSearXNGPayload) {
	result.ResultCount = len(payload.Results)
	engineSet := map[string]bool{}
	for _, item := range payload.Results {
		for _, engine := range item.Engines {
			engine = strings.TrimSpace(engine)
			if engine != "" {
				engineSet[engine] = true
			}
		}
		if strings.TrimSpace(item.Engine) != "" {
			engineSet[strings.TrimSpace(item.Engine)] = true
		}
	}
	for _, item := range payload.UnresponsiveEngines {
		if len(item) == 0 {
			continue
		}
		name := strings.TrimSpace(item[0])
		message := ""
		if len(item) > 1 {
			message = strings.TrimSpace(item[1])
		}
		if name != "" {
			engineSet[name] = true
			result.UnresponsiveEngines = append(result.UnresponsiveEngines, webDoctorUnresponsiveEngine{Name: name, Error: message})
		}
	}
	for engine := range engineSet {
		result.Engines = append(result.Engines, engine)
	}
	sort.Strings(result.Engines)
	result.BlockedEnginesActive = webDoctorBlockedEngines(result.Engines)
}

func webDoctorBlockedEngines(engines []string) []string {
	blocked := map[string]bool{
		"google":     true,
		"duckduckgo": true,
		"brave":      true,
		"startpage":  true,
		"youtube":    true,
	}
	var active []string
	for _, engine := range engines {
		if blocked[strings.ToLower(engine)] {
			active = append(active, engine)
		}
	}
	sort.Strings(active)
	return active
}

func webDoctorEnvListOrDefault(key string, fallback string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		value = fallback
	}
	return webDoctorCleanList(strings.Split(value, ","))
}

func webDoctorCleanList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func webDoctorOrderedUnique(prefix []string, rest []string) []string {
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
