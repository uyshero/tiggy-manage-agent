package skillmarketplace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewBinaryScannerDefersExternalProvider(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scanner, err := NewBinaryScanner(BinaryScannerConfig{
		Provider: BinaryScannerProviderClamAVHTTP, Endpoint: server.URL,
	})
	if scanner != nil || !errors.Is(err, ErrExternalBinaryScannerDeferred) {
		t.Fatalf("expected deferred external scanner, scanner=%T err=%v", scanner, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("external scanner factory made %d network requests", requests.Load())
	}
}

func TestClamAVHTTPScannerReturnsSynchronousVerdict(t *testing.T) {
	content := []byte("safe-pdf-content")
	digest := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gateway/scan" {
			t.Fatalf("unexpected scanner request: %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != string(content) {
			t.Fatalf("unexpected scanner body: %q err=%v", body, err)
		}
		if r.Header.Get("Authorization") != "Bearer scanner-secret" || r.Header.Get("X-TMA-Asset-Path") != "showcase.pdf" || r.Header.Get("X-TMA-Content-SHA256") != hex.EncodeToString(digest[:]) {
			t.Fatalf("unexpected scanner headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"passed","scanner":"ClamAV 1.4.3","message":"clean"}`)
	}))
	defer server.Close()

	scanner, err := NewClamAVHTTPScanner(BinaryScannerConfig{
		Endpoint: server.URL + "/gateway", Token: "scanner-secret", HTTPClient: server.Client(),
		Timeout: time.Second, MaxAttempts: 2, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create scanner: %v", err)
	}
	result, err := scanner.Scan(t.Context(), BinaryScannerInput{
		Path: "showcase.pdf", ContentType: "application/pdf", ChecksumSHA256: hex.EncodeToString(digest[:]), Content: content,
	})
	if err != nil || result.Status != BinaryScanPassed || result.Scanner != "ClamAV 1.4.3" || result.Attempts != 1 {
		t.Fatalf("unexpected synchronous verdict: result=%#v err=%v", result, err)
	}
}

func TestClamAVHTTPScannerPollsPendingVerdict(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/scan":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"status":"pending","scan_id":"scan-123","scanner":"ClamAV gateway"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/scans/scan-123":
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprint(w, `{"status":"pending","scan_id":"scan-123"}`)
				return
			}
			fmt.Fprint(w, `{"status":"passed","scan_id":"scan-123","scanner":"ClamAV 1.4.3"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	scanner, err := NewClamAVHTTPScanner(BinaryScannerConfig{
		Endpoint: server.URL, HTTPClient: server.Client(), Timeout: time.Second,
		MaxAttempts: 2, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create scanner: %v", err)
	}
	result, err := scanner.Scan(t.Context(), BinaryScannerInput{Path: "asset.png", ContentType: "image/png", Content: []byte("content")})
	if err != nil || result.Status != BinaryScanPassed || result.ScanID != "scan-123" || result.Attempts != 3 || polls.Load() != 2 {
		t.Fatalf("unexpected pending verdict: result=%#v polls=%d err=%v", result, polls.Load(), err)
	}
}

func TestClamAVHTTPScannerRetriesTransientFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"passed","scanner":"ClamAV"}`)
	}))
	defer server.Close()

	scanner, err := NewClamAVHTTPScanner(BinaryScannerConfig{
		Endpoint: server.URL, HTTPClient: server.Client(), Timeout: time.Second,
		MaxAttempts: 2, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create scanner: %v", err)
	}
	result, err := scanner.Scan(t.Context(), BinaryScannerInput{Path: "asset.pdf", Content: []byte("content")})
	if err != nil || result.Status != BinaryScanPassed || result.Attempts != 2 || attempts.Load() != 2 {
		t.Fatalf("unexpected retry result: result=%#v attempts=%d err=%v", result, attempts.Load(), err)
	}
}

func TestApplyExternalBinaryScannerBlocksMalwareAndErrors(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 24)...)
	digest := sha256.Sum256(png)
	pkg := Package{Content: "# Visual", Files: []PackageFile{{
		Path: "asset.png", Binary: true, ContentBase64: base64.StdEncoding.EncodeToString(png),
		ContentType: "image/png", ChecksumSHA256: hex.EncodeToString(digest[:]), Size: len(png),
	}}}
	decision, report := (Policy{}).EvaluatePackageSecurity(Source{Repository: "acme/visual"}, "MIT", pkg)
	if !decision.Allowed {
		t.Fatalf("built-in scan should pass: %#v", report)
	}

	blockedScanner := staticExternalScanner{result: ExternalBinaryScanResult{
		Provider: BinaryScannerProviderClamAVHTTP, Status: BinaryScanBlocked,
		Scanner: "ClamAV 1.4.3", Signature: "Eicar-Signature", Message: "infected",
	}}
	report, err := ApplyExternalBinaryScanner(t.Context(), pkg, report, blockedScanner)
	if err != nil {
		t.Fatalf("malware verdict should not be a transport error: %v", err)
	}
	decision = UpdateBinaryScanPolicyDecision(decision, report)
	if decision.Allowed || report.BinaryFiles[0].Status != BinaryScanBlocked || report.BinaryFiles[0].ExternalScan == nil || !hasPackageSecurityRule(report.Findings, "external_malware") {
		t.Fatalf("external malware verdict was not enforced: decision=%#v report=%#v", decision, report)
	}

	_, cleanReport := (Policy{}).EvaluatePackageSecurity(Source{Repository: "acme/visual"}, "MIT", pkg)
	errorScanner := staticExternalScanner{
		result: ExternalBinaryScanResult{Provider: BinaryScannerProviderClamAVHTTP, Status: BinaryScanError, Message: "scanner unavailable"},
		err:    ErrBinaryScanFailed,
	}
	cleanReport, err = ApplyExternalBinaryScanner(t.Context(), pkg, cleanReport, errorScanner)
	if !errors.Is(err, ErrBinaryScanFailed) || cleanReport.BinaryFiles[0].Status != BinaryScanError || !hasPackageSecurityRule(cleanReport.Findings, "external_scan_error") {
		t.Fatalf("external scanner error was not fail closed: report=%#v err=%v", cleanReport, err)
	}
}

func TestClamAVHTTPScannerTimeoutFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"pending","scan_id":"never-ready"}`)
	}))
	defer server.Close()

	scanner, err := NewClamAVHTTPScanner(BinaryScannerConfig{
		Endpoint: server.URL, HTTPClient: server.Client(), Timeout: 10 * time.Millisecond,
		MaxAttempts: 1, PollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create scanner: %v", err)
	}
	result, err := scanner.Scan(context.Background(), BinaryScannerInput{Path: "asset.pdf", Content: []byte("content")})
	if !errors.Is(err, ErrBinaryScanFailed) || result.Status != BinaryScanError || !strings.Contains(result.Message, "deadline") {
		t.Fatalf("expected timeout to fail closed: result=%#v err=%v", result, err)
	}
}

func TestClamAVHTTPScannerRejectsUntrustedResponse(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
	}{
		{name: "invalid pending scan id", statusCode: http.StatusAccepted, response: `{"status":"pending","scan_id":"../../secret"}`},
		{name: "invalid JSON", statusCode: http.StatusOK, response: `{not-json`},
		{name: "oversized response", statusCode: http.StatusOK, response: `{"status":"passed","message":"` + strings.Repeat("x", maxBinaryScannerResponseBytes) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.statusCode)
				fmt.Fprint(w, test.response)
			}))
			defer server.Close()
			scanner, err := NewClamAVHTTPScanner(BinaryScannerConfig{
				Endpoint: server.URL, HTTPClient: server.Client(), Timeout: 50 * time.Millisecond,
				MaxAttempts: 1, PollInterval: time.Millisecond,
			})
			if err != nil {
				t.Fatalf("create scanner: %v", err)
			}
			result, err := scanner.Scan(t.Context(), BinaryScannerInput{Path: "asset.pdf", Content: []byte("content")})
			if !errors.Is(err, ErrBinaryScanFailed) || result.Status != BinaryScanError {
				t.Fatalf("expected untrusted response rejection: result=%#v err=%v", result, err)
			}
		})
	}
}

type staticExternalScanner struct {
	result ExternalBinaryScanResult
	err    error
}

func (s staticExternalScanner) Provider() string {
	return BinaryScannerProviderClamAVHTTP
}

func (s staticExternalScanner) Scan(context.Context, BinaryScannerInput) (ExternalBinaryScanResult, error) {
	return s.result, s.err
}
