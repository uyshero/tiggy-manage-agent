package objectstore

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestNewClientReturnsLocalFSForDefaultProvider(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderLocalFS, RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, ok := client.(*LocalFSClient); !ok {
		t.Fatalf("expected localfs client, got %T", client)
	}
}

func TestLocalFSClientPutGetDeleteRoundTrip(t *testing.T) {
	client, err := NewLocalFSClient(Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new localfs client: %v", err)
	}

	put, err := client.PutObject(context.Background(), PutObjectInput{
		Bucket:      "artifacts",
		Key:         "wksp/session/output.txt",
		Body:        strings.NewReader("hello world"),
		ContentType: "text/plain",
		Metadata:    map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}
	if put.Bucket != "artifacts" || put.Key != "wksp/session/output.txt" || put.SizeBytes != 11 {
		t.Fatalf("unexpected put result: %+v", put)
	}

	get, err := client.GetObject(context.Background(), GetObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"})
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	defer get.Body.Close()
	content, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatalf("read object body: %v", err)
	}
	if string(content) != "hello world" {
		t.Fatalf("unexpected body: %q", string(content))
	}
	if get.ContentType != "text/plain" {
		t.Fatalf("unexpected content type: %q", get.ContentType)
	}
	if get.ChecksumSHA256 == "" || get.ETag == "" {
		t.Fatalf("expected checksum and etag, got %+v", get)
	}
	if get.Metadata["source"] != "test" {
		t.Fatalf("unexpected metadata: %+v", get.Metadata)
	}

	if err := client.DeleteObject(context.Background(), DeleteObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"}); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if _, err := client.GetObject(context.Background(), GetObjectInput{Bucket: "artifacts", Key: "wksp/session/output.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestNoopClientReturnsNotConfigured(t *testing.T) {
	client := NewNoopClient(Config{
		Provider: "s3",
		Endpoint: "http://localhost:9000",
		Bucket:   "tma-artifacts",
	})

	if _, err := client.PutObject(context.Background(), PutObjectInput{Key: "file.txt", Body: strings.NewReader("hello")}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected PutObject ErrNotConfigured, got %v", err)
	}
	if _, err := client.GetObject(context.Background(), GetObjectInput{Key: "file.txt"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected GetObject ErrNotConfigured, got %v", err)
	}
	if err := client.DeleteObject(context.Background(), DeleteObjectInput{Key: "file.txt"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected DeleteObject ErrNotConfigured, got %v", err)
	}
	if _, err := client.PresignGetObject(context.Background(), PresignGetObjectInput{Key: "file.txt", TTL: time.Minute}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected PresignGetObject ErrNotConfigured, got %v", err)
	}
}

func TestResolveBucket(t *testing.T) {
	bucket, err := ResolveBucket("explicit", "default")
	if err != nil {
		t.Fatalf("resolve explicit bucket: %v", err)
	}
	if bucket != "explicit" {
		t.Fatalf("expected explicit bucket, got %q", bucket)
	}

	bucket, err = ResolveBucket("", "default")
	if err != nil {
		t.Fatalf("resolve default bucket: %v", err)
	}
	if bucket != "default" {
		t.Fatalf("expected default bucket, got %q", bucket)
	}

	if _, err := ResolveBucket("", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected missing bucket ErrInvalid, got %v", err)
	}
}

func TestValidateObjectKey(t *testing.T) {
	if err := ValidateObjectKey("workspace/session/file.txt"); err != nil {
		t.Fatalf("expected relative key to be valid: %v", err)
	}
	if err := ValidateObjectKey("workspace/../file.txt"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected parent segment key ErrInvalid, got %v", err)
	}
	if err := ValidateObjectKey(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected empty key ErrInvalid, got %v", err)
	}
	if err := ValidateObjectKey("/absolute/file.txt"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected absolute key ErrInvalid, got %v", err)
	}
}
