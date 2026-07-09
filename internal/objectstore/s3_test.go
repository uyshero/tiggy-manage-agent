package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewClientReturnsS3ForProviderS3(t *testing.T) {
	client, err := NewClient(Config{
		Provider:     ProviderS3,
		Endpoint:     "http://127.0.0.1:9000",
		Region:       "local",
		AccessKey:    "access",
		SecretKey:    "secret",
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, ok := client.(*S3Client); !ok {
		t.Fatalf("expected s3 client, got %T", client)
	}
}

func TestS3ClientPutGetDeleteRoundTrip(t *testing.T) {
	client := newTestS3Client(t, "http://s3.local")
	client.httpClient = &http.Client{Transport: newFakeS3Transport(t)}

	put, err := client.PutObject(context.Background(), PutObjectInput{
		Bucket:      "tma-artifacts",
		Key:         "workspace/session/output file.txt",
		Body:        strings.NewReader("hello s3"),
		ContentType: "text/plain",
		SizeBytes:   8,
		Metadata:    map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}
	if put.Bucket != "tma-artifacts" || put.Key != "workspace/session/output file.txt" || put.SizeBytes != 8 {
		t.Fatalf("unexpected put result: %+v", put)
	}
	if put.ETag != "fake-etag" {
		t.Fatalf("expected trimmed etag, got %q", put.ETag)
	}

	get, err := client.GetObject(context.Background(), GetObjectInput{Bucket: "tma-artifacts", Key: "workspace/session/output file.txt"})
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	defer get.Body.Close()
	body, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "hello s3" {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if get.ContentType != "text/plain" || get.Metadata["source"] != "test" || get.ETag != "fake-etag" {
		t.Fatalf("unexpected get metadata: %+v", get)
	}

	if err := client.DeleteObject(context.Background(), DeleteObjectInput{Bucket: "tma-artifacts", Key: "workspace/session/output file.txt"}); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if _, err := client.GetObject(context.Background(), GetObjectInput{Bucket: "tma-artifacts", Key: "workspace/session/output file.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestS3ClientPresignGetObject(t *testing.T) {
	client := newTestS3Client(t, "http://127.0.0.1:9000")
	client.now = func() time.Time {
		return time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
	}

	presigned, err := client.PresignGetObject(context.Background(), PresignGetObjectInput{
		Bucket:  "tma-artifacts",
		Key:     "workspace/session/output.txt",
		Version: "v1",
		TTL:     10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("presign get object: %v", err)
	}
	parsed, err := url.Parse(presigned.URL)
	if err != nil {
		t.Fatalf("parse presigned url: %v", err)
	}
	query := parsed.Query()
	if parsed.Path != "/tma-artifacts/workspace/session/output.txt" {
		t.Fatalf("unexpected path: %q", parsed.Path)
	}
	if query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Fatalf("missing algorithm query: %s", presigned.URL)
	}
	if query.Get("X-Amz-Expires") != "600" || query.Get("versionId") != "v1" {
		t.Fatalf("unexpected expires/version query: %s", presigned.URL)
	}
	if !strings.Contains(query.Get("X-Amz-Credential"), "access/20260709/local/s3/aws4_request") {
		t.Fatalf("unexpected credential query: %q", query.Get("X-Amz-Credential"))
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Fatalf("missing signature query: %s", presigned.URL)
	}
	if strings.Contains(presigned.URL, "secret") {
		t.Fatalf("presigned url must not expose secret: %s", presigned.URL)
	}
	if !presigned.ExpiresAt.Equal(time.Date(2026, 7, 9, 8, 10, 0, 0, time.UTC)) {
		t.Fatalf("unexpected expires at: %s", presigned.ExpiresAt)
	}
}

func TestS3ClientObjectURLStyles(t *testing.T) {
	pathStyle := newTestS3Client(t, "http://127.0.0.1:9000/base")
	pathURL := pathStyle.objectURL("bucket", "a/b c.txt")
	if pathURL.String() != "http://127.0.0.1:9000/base/bucket/a/b%20c.txt" {
		t.Fatalf("unexpected path-style url: %s", pathURL.String())
	}

	virtualHost, err := NewS3Client(Config{
		Provider:     ProviderS3,
		Endpoint:     "https://s3.example.test/base",
		Region:       "us-east-1",
		AccessKey:    "access",
		SecretKey:    "secret",
		UsePathStyle: false,
	})
	if err != nil {
		t.Fatalf("new virtual-host client: %v", err)
	}
	virtualURL := virtualHost.objectURL("bucket", "a/b c.txt")
	if virtualURL.String() != "https://bucket.s3.example.test/base/a/b%20c.txt" {
		t.Fatalf("unexpected virtual-host url: %s", virtualURL.String())
	}
}

func newTestS3Client(t *testing.T, endpoint string) *S3Client {
	t.Helper()
	client, err := NewS3Client(Config{
		Provider:     ProviderS3,
		Endpoint:     endpoint,
		Region:       "local",
		AccessKey:    "access",
		SecretKey:    "secret",
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("new s3 client: %v", err)
	}
	return client
}

type fakeS3Object struct {
	body        []byte
	contentType string
	metadata    map[string]string
}

func newFakeS3Transport(t *testing.T) http.RoundTripper {
	t.Helper()
	transport := &fakeS3Transport{
		t:       t,
		objects: map[string]fakeS3Object{},
	}
	return transport
}

type fakeS3Transport struct {
	t       *testing.T
	mu      sync.Mutex
	objects map[string]fakeS3Object
}

func (t *fakeS3Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.t.Helper()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access/") {
		t.t.Errorf("missing sigv4 authorization: %q", r.Header.Get("Authorization"))
	}
	if !strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=") || !strings.Contains(r.Header.Get("Authorization"), "Signature=") {
		t.t.Errorf("incomplete sigv4 authorization: %q", r.Header.Get("Authorization"))
	}
	if r.Header.Get("x-amz-date") == "" {
		t.t.Errorf("missing x-amz-date")
	}

	key := r.URL.Path
	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.t.Errorf("read put body: %v", err)
		}
		sum := sha256.Sum256(body)
		if got := r.Header.Get("x-amz-content-sha256"); got != hex.EncodeToString(sum[:]) {
			t.t.Errorf("unexpected payload hash: %q", got)
		}
		t.mu.Lock()
		t.objects[key] = fakeS3Object{
			body:        body,
			contentType: r.Header.Get("Content-Type"),
			metadata:    map[string]string{"source": r.Header.Get("x-amz-meta-source")},
		}
		t.mu.Unlock()
		return fakeS3Response(http.StatusOK, map[string]string{"ETag": `"fake-etag"`}, ""), nil
	case http.MethodGet:
		t.mu.Lock()
		object, ok := t.objects[key]
		t.mu.Unlock()
		if !ok {
			return fakeS3Response(http.StatusNotFound, nil, "not found"), nil
		}
		headers := map[string]string{
			"Content-Type": object.contentType,
			"ETag":         `"fake-etag"`,
		}
		for metaKey, metaValue := range object.metadata {
			headers["x-amz-meta-"+metaKey] = metaValue
		}
		return fakeS3Response(http.StatusOK, headers, string(object.body)), nil
	case http.MethodDelete:
		t.mu.Lock()
		delete(t.objects, key)
		t.mu.Unlock()
		return fakeS3Response(http.StatusNoContent, nil, ""), nil
	default:
		t.t.Errorf("unexpected method: %s", r.Method)
		return fakeS3Response(http.StatusMethodNotAllowed, nil, ""), nil
	}
}

func fakeS3Response(status int, headers map[string]string, body string) *http.Response {
	responseHeaders := http.Header{}
	for key, value := range headers {
		responseHeaders.Set(key, value)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        responseHeaders,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}
