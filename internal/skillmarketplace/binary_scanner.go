package skillmarketplace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	BinaryScannerProviderBuiltin    = "builtin"
	BinaryScannerProviderClamAVHTTP = "clamav_http"

	BinaryScanPending = "pending"
	BinaryScanError   = "error"

	defaultBinaryScannerTimeout      = 30 * time.Second
	defaultBinaryScannerMaxAttempts  = 3
	defaultBinaryScannerPollInterval = 500 * time.Millisecond
	maxBinaryScannerResponseBytes    = 64 << 10
)

var (
	ErrBinaryScanFailed              = errors.New("external binary scan failed")
	ErrExternalBinaryScannerDeferred = errors.New("external binary scanner is deferred in this release")
)

type BinaryScanner interface {
	Provider() string
	Scan(context.Context, BinaryScannerInput) (ExternalBinaryScanResult, error)
}

type BinaryScannerInput struct {
	Path           string
	ContentType    string
	ChecksumSHA256 string
	Content        []byte
}

type ExternalBinaryScanResult struct {
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	Scanner    string `json:"scanner,omitempty"`
	ScanID     string `json:"scan_id,omitempty"`
	Signature  string `json:"signature,omitempty"`
	Message    string `json:"message,omitempty"`
	Attempts   int    `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
}

type BinaryScannerConfig struct {
	Provider     string
	Endpoint     string
	Token        string
	Timeout      time.Duration
	MaxAttempts  int
	PollInterval time.Duration
	HTTPClient   *http.Client
}

func NewBinaryScanner(config BinaryScannerConfig) (BinaryScanner, error) {
	provider := normalizeBinaryScannerProvider(config.Provider)
	switch provider {
	case "", BinaryScannerProviderBuiltin:
		return nil, nil
	case BinaryScannerProviderClamAVHTTP:
		// Keep the client implementation available for isolated development tests,
		// but do not expose a network scanner through the production factory yet.
		return nil, ErrExternalBinaryScannerDeferred
	default:
		return nil, fmt.Errorf("unsupported binary scanner provider %q", config.Provider)
	}
}

type ClamAVHTTPScanner struct {
	endpoint     *url.URL
	token        string
	timeout      time.Duration
	maxAttempts  int
	pollInterval time.Duration
	httpClient   *http.Client
}

func NewClamAVHTTPScanner(config BinaryScannerConfig) (*ClamAVHTTPScanner, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("clamav_http scanner endpoint must be an absolute HTTP(S) URL")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultBinaryScannerTimeout
	}
	maxAttempts := config.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultBinaryScannerMaxAttempts
	}
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultBinaryScannerPollInterval
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	return &ClamAVHTTPScanner{
		endpoint: endpoint, token: strings.TrimSpace(config.Token), timeout: timeout,
		maxAttempts: maxAttempts, pollInterval: pollInterval, httpClient: httpClient,
	}, nil
}

func (s *ClamAVHTTPScanner) Provider() string {
	return BinaryScannerProviderClamAVHTTP
}

func (s *ClamAVHTTPScanner) Scan(ctx context.Context, input BinaryScannerInput) (ExternalBinaryScanResult, error) {
	startedAt := time.Now()
	result := ExternalBinaryScanResult{Provider: s.Provider(), Status: BinaryScanPending}
	if strings.TrimSpace(input.Path) == "" || len(input.Content) == 0 {
		result.Status = BinaryScanError
		result.Message = "binary scanner input requires path and content"
		return result, fmt.Errorf("%w: %s", ErrBinaryScanFailed, result.Message)
	}
	scanCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	response, attempts, err := s.requestWithRetry(scanCtx, http.MethodPost, s.scanURL(), input.Content, input)
	result.Attempts += attempts
	if err != nil {
		return finishExternalScan(result, startedAt, err)
	}
	result = mergeExternalScanResponse(result, response)
	for result.Status == BinaryScanPending {
		if result.ScanID == "" {
			return finishExternalScan(result, startedAt, fmt.Errorf("%w: pending response omitted scan_id", ErrBinaryScanFailed))
		}
		if err := waitForBinaryScanPoll(scanCtx, s.pollInterval); err != nil {
			return finishExternalScan(result, startedAt, fmt.Errorf("%w: %v", ErrBinaryScanFailed, err))
		}
		response, attempts, err = s.requestWithRetry(scanCtx, http.MethodGet, s.resultURL(result.ScanID), nil, input)
		result.Attempts += attempts
		if err != nil {
			return finishExternalScan(result, startedAt, err)
		}
		result = mergeExternalScanResponse(result, response)
	}
	result.DurationMS = time.Since(startedAt).Milliseconds()
	switch result.Status {
	case BinaryScanPassed:
		return result, nil
	case BinaryScanBlocked:
		return result, nil
	case BinaryScanError:
		return result, fmt.Errorf("%w: %s", ErrBinaryScanFailed, fallbackScannerMessage(result.Message, "scanner returned error"))
	default:
		result.Status = BinaryScanError
		return result, fmt.Errorf("%w: scanner returned unsupported status", ErrBinaryScanFailed)
	}
}

type binaryScannerHTTPResponse struct {
	Status    string `json:"status"`
	Scanner   string `json:"scanner,omitempty"`
	ScanID    string `json:"scan_id,omitempty"`
	Signature string `json:"signature,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (s *ClamAVHTTPScanner) requestWithRetry(ctx context.Context, method string, requestURL string, body []byte, input BinaryScannerInput) (binaryScannerHTTPResponse, int, error) {
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		response, retryable, err := s.request(ctx, method, requestURL, body, input)
		if err == nil {
			return response, attempt, nil
		}
		if !retryable || attempt == s.maxAttempts {
			return binaryScannerHTTPResponse{}, attempt, err
		}
		if err := waitForBinaryScanPoll(ctx, time.Duration(attempt)*s.pollInterval); err != nil {
			return binaryScannerHTTPResponse{}, attempt, fmt.Errorf("%w: %v", ErrBinaryScanFailed, err)
		}
	}
	return binaryScannerHTTPResponse{}, s.maxAttempts, fmt.Errorf("%w: scanner retries exhausted", ErrBinaryScanFailed)
}

func (s *ClamAVHTTPScanner) request(ctx context.Context, method string, requestURL string, body []byte, input BinaryScannerInput) (binaryScannerHTTPResponse, bool, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return binaryScannerHTTPResponse{}, false, err
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", fallbackScannerMessage(input.ContentType, "application/octet-stream"))
		request.Header.Set("X-TMA-Asset-Path", input.Path)
		request.Header.Set("X-TMA-Content-SHA256", input.ChecksumSHA256)
		request.ContentLength = int64(len(body))
	}
	request.Header.Set("Accept", "application/json")
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return binaryScannerHTTPResponse{}, true, fmt.Errorf("%w: request scanner: %v", ErrBinaryScanFailed, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBinaryScannerResponseBytes+1))
	if err != nil {
		return binaryScannerHTTPResponse{}, false, fmt.Errorf("%w: read scanner response: %v", ErrBinaryScanFailed, err)
	}
	if len(responseBody) > maxBinaryScannerResponseBytes {
		return binaryScannerHTTPResponse{}, false, fmt.Errorf("%w: scanner response exceeds %d bytes", ErrBinaryScanFailed, maxBinaryScannerResponseBytes)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError {
		return binaryScannerHTTPResponse{}, true, fmt.Errorf("%w: scanner returned HTTP %d", ErrBinaryScanFailed, response.StatusCode)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return binaryScannerHTTPResponse{}, false, fmt.Errorf("%w: scanner returned HTTP %d", ErrBinaryScanFailed, response.StatusCode)
	}
	var decoded binaryScannerHTTPResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return binaryScannerHTTPResponse{}, false, fmt.Errorf("%w: decode scanner response: %v", ErrBinaryScanFailed, err)
	}
	decoded.Status = normalizeBinaryScanStatus(decoded.Status)
	if response.StatusCode == http.StatusAccepted && decoded.Status == "" {
		decoded.Status = BinaryScanPending
	}
	if decoded.Status == "" {
		return binaryScannerHTTPResponse{}, false, fmt.Errorf("%w: scanner response omitted status", ErrBinaryScanFailed)
	}
	return decoded, false, nil
}

func (s *ClamAVHTTPScanner) scanURL() string {
	endpoint := *s.endpoint
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/scan"
	return endpoint.String()
}

func (s *ClamAVHTTPScanner) resultURL(scanID string) string {
	endpoint := *s.endpoint
	endpoint.Path = path.Join(strings.TrimRight(endpoint.Path, "/"), "scans", url.PathEscape(scanID))
	return endpoint.String()
}

func mergeExternalScanResponse(result ExternalBinaryScanResult, response binaryScannerHTTPResponse) ExternalBinaryScanResult {
	result.Status = response.Status
	result.Scanner = sanitizeScannerText(response.Scanner, 120)
	if response.ScanID != "" {
		result.ScanID = sanitizeScannerID(response.ScanID)
	}
	result.Signature = sanitizeScannerText(response.Signature, 120)
	result.Message = sanitizeScannerText(response.Message, 300)
	return result
}

func finishExternalScan(result ExternalBinaryScanResult, startedAt time.Time, err error) (ExternalBinaryScanResult, error) {
	result.Status = BinaryScanError
	result.DurationMS = time.Since(startedAt).Milliseconds()
	if result.Message == "" {
		result.Message = sanitizeScannerText(err.Error(), 300)
	}
	return result, err
}

func waitForBinaryScanPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeBinaryScannerProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "none", "static", BinaryScannerProviderBuiltin:
		return BinaryScannerProviderBuiltin
	case "clamav", "clamav-http", BinaryScannerProviderClamAVHTTP:
		return BinaryScannerProviderClamAVHTTP
	default:
		return value
	}
}

func normalizeBinaryScanStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "clean", "ok", BinaryScanPassed:
		return BinaryScanPassed
	case "infected", "malicious", BinaryScanBlocked:
		return BinaryScanBlocked
	case "queued", "running", BinaryScanPending:
		return BinaryScanPending
	case "failed", BinaryScanError:
		return BinaryScanError
	default:
		return ""
	}
}

func fallbackScannerMessage(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

type BinaryScanMetric struct {
	Provider       string
	Outcome        string
	Count          uint64
	DurationMillis uint64
}

var binaryScanMetrics = struct {
	sync.Mutex
	values map[string]BinaryScanMetric
}{values: make(map[string]BinaryScanMetric)}

func recordBinaryScanMetric(result ExternalBinaryScanResult) {
	provider := fallbackScannerMessage(result.Provider, "unknown")
	outcome := fallbackScannerMessage(result.Status, BinaryScanError)
	key := provider + "\x00" + outcome
	binaryScanMetrics.Lock()
	metric := binaryScanMetrics.values[key]
	metric.Provider = provider
	metric.Outcome = outcome
	metric.Count++
	if result.DurationMS > 0 {
		metric.DurationMillis += uint64(result.DurationMS)
	}
	binaryScanMetrics.values[key] = metric
	binaryScanMetrics.Unlock()
}

func BinaryScanMetricsSnapshot() []BinaryScanMetric {
	binaryScanMetrics.Lock()
	defer binaryScanMetrics.Unlock()
	result := make([]BinaryScanMetric, 0, len(binaryScanMetrics.values))
	for _, metric := range binaryScanMetrics.values {
		result = append(result, metric)
	}
	return result
}

func ApplyExternalBinaryScanner(ctx context.Context, pkg Package, report PackageSecurityReport, scanner BinaryScanner) (PackageSecurityReport, error) {
	if scanner == nil || len(report.BinaryFiles) == 0 {
		return report, nil
	}
	files := make(map[string]PackageFile, len(pkg.Files))
	for _, file := range pkg.Files {
		files[file.Path] = file
	}
	var scanErrors []error
	for index := range report.BinaryFiles {
		binaryResult := &report.BinaryFiles[index]
		if binaryResult.Status != BinaryScanPassed {
			continue
		}
		file, ok := files[binaryResult.Path]
		if !ok {
			external := ExternalBinaryScanResult{
				Provider: scanner.Provider(), Status: BinaryScanError,
				Message: "binary asset is missing from the package",
			}
			binaryResult.ExternalScan = &external
			binaryResult.Status = BinaryScanError
			recordBinaryScanMetric(external)
			scanErrors = append(scanErrors, fmt.Errorf("%w: asset %q is missing", ErrBinaryScanFailed, binaryResult.Path))
			appendExternalBinaryFinding(&report, "external_scan_error", binaryResult.Path, external.Message)
			continue
		}
		content, decodeErr := base64.StdEncoding.DecodeString(file.ContentBase64)
		if decodeErr != nil {
			external := ExternalBinaryScanResult{
				Provider: scanner.Provider(), Status: BinaryScanError,
				Message: "binary asset could not be decoded for external scanning",
			}
			binaryResult.ExternalScan = &external
			binaryResult.Status = BinaryScanError
			recordBinaryScanMetric(external)
			scanErrors = append(scanErrors, fmt.Errorf("%w: decode asset %q", ErrBinaryScanFailed, binaryResult.Path))
			appendExternalBinaryFinding(&report, "external_scan_error", binaryResult.Path, external.Message)
			continue
		}
		external, scanErr := scanner.Scan(ctx, BinaryScannerInput{
			Path: binaryResult.Path, ContentType: binaryResult.ContentType,
			ChecksumSHA256: binaryResult.ChecksumSHA256, Content: content,
		})
		if external.Provider == "" {
			external.Provider = scanner.Provider()
		}
		if external.Status == "" {
			external.Status = BinaryScanError
		}
		binaryResult.ExternalScan = &external
		recordBinaryScanMetric(external)
		switch external.Status {
		case BinaryScanPassed:
			// Both built-in and external scanners passed.
		case BinaryScanBlocked:
			binaryResult.Status = BinaryScanBlocked
			message := "External malware scanner blocked the binary asset."
			if external.Signature != "" {
				message = "External malware scanner detected signature " + sanitizeScannerDetail(external.Signature) + "."
			}
			appendExternalBinaryFinding(&report, "external_malware", binaryResult.Path, message)
		case BinaryScanError:
			binaryResult.Status = BinaryScanError
			message := "External malware scanner did not return a trusted verdict."
			appendExternalBinaryFinding(&report, "external_scan_error", binaryResult.Path, message)
		default:
			binaryResult.Status = BinaryScanError
			external.Status = BinaryScanError
			binaryResult.ExternalScan = &external
			appendExternalBinaryFinding(&report, "external_scan_error", binaryResult.Path, "External malware scanner returned an unsupported status.")
		}
		if scanErr != nil {
			scanErrors = append(scanErrors, fmt.Errorf("scan binary asset %q: %w", binaryResult.Path, scanErr))
		} else if external.Status == BinaryScanError {
			scanErrors = append(scanErrors, fmt.Errorf("%w: scanner returned error for %q", ErrBinaryScanFailed, binaryResult.Path))
		}
	}
	return report, errors.Join(scanErrors...)
}

func UpdateBinaryScanPolicyDecision(decision PolicyDecision, report PackageSecurityReport) PolicyDecision {
	passed := true
	for _, file := range report.BinaryFiles {
		if file.Status != BinaryScanPassed {
			passed = false
			break
		}
	}
	message := "package contains no binary assets"
	if len(report.BinaryFiles) > 0 {
		message = fmt.Sprintf("binary scan passed for %d asset(s)", len(report.BinaryFiles))
		if !passed {
			message = "binary scan blocked or failed for one or more assets"
		}
	}
	checks := append([]PolicyCheck(nil), decision.Checks...)
	for index := range checks {
		if checks[index].Name == PolicyCheckBinaryScan {
			checks[index] = PolicyCheck{Name: PolicyCheckBinaryScan, Enforced: len(report.BinaryFiles) > 0, Passed: passed, Message: message}
			return newPolicyDecision(checks)
		}
	}
	checks = append(checks, PolicyCheck{Name: PolicyCheckBinaryScan, Enforced: len(report.BinaryFiles) > 0, Passed: passed, Message: message})
	return newPolicyDecision(checks)
}

func appendExternalBinaryFinding(report *PackageSecurityReport, ruleID string, assetPath string, message string) {
	if len(report.Findings) >= maxSecurityFindings {
		report.FindingsLimited = true
		return
	}
	report.Findings = append(report.Findings, SecurityFinding{
		RuleID: ruleID, Severity: SeverityCritical, Path: assetPath, Message: message,
	})
	report.HighestSeverity = SeverityCritical
}

func sanitizeScannerDetail(value string) string {
	return sanitizeScannerText(value, 120)
}

func sanitizeScannerText(value string, limit int) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, char := range value {
		if char >= 32 && char <= 126 {
			result.WriteRune(char)
		}
		if result.Len() == limit {
			break
		}
	}
	return result.String()
}

func sanitizeScannerID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 200 {
		return ""
	}
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' || char == ':'
		if !valid {
			return ""
		}
	}
	return value
}
