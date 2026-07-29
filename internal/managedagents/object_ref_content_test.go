package managedagents_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

func TestReadVerifiedObjectRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()

	client, err := objectstore.NewLocalFSClient(objectstore.Config{Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir(), Bucket: "artifacts"})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("verified content")
	put, err := client.PutObject(context.Background(), objectstore.PutObjectInput{
		Bucket: "artifacts", Key: "skills/test/SKILL.md", Body: bytes.NewReader(content),
		SizeBytes: int64(len(content)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := managedagents.ObjectRef{
		ID: "obj_bad_checksum", Bucket: put.Bucket, ObjectKey: put.Key, SizeBytes: int64(len(content)),
		ChecksumSHA256: strings.Repeat("0", 64),
	}
	if _, err := managedagents.ReadVerifiedObject(context.Background(), client, ref, 0); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestReadVerifiedObjectReturnsVerifiedContent(t *testing.T) {
	t.Parallel()

	client, err := objectstore.NewLocalFSClient(objectstore.Config{Provider: objectstore.ProviderLocalFS, RootDir: t.TempDir(), Bucket: "artifacts"})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("verified content")
	checksum := sha256.Sum256(content)
	put, err := client.PutObject(context.Background(), objectstore.PutObjectInput{
		Bucket: "artifacts", Key: "skills/test/SKILL.md", Body: bytes.NewReader(content),
		ContentType: "text/markdown", SizeBytes: int64(len(content)), ChecksumSHA256: fmt.Sprintf("%x", checksum),
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := managedagents.ObjectRef{
		ID: "obj_ok", Bucket: put.Bucket, ObjectKey: put.Key, SizeBytes: int64(len(content)),
		ChecksumSHA256: fmt.Sprintf("%x", checksum), ContentType: "text/markdown",
	}
	verified, err := managedagents.ReadVerifiedObject(context.Background(), client, ref, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if string(verified.Content) != string(content) || verified.ChecksumSHA256 != fmt.Sprintf("%x", checksum) || verified.ContentType != "text/markdown" {
		t.Fatalf("unexpected verified object: %+v", verified)
	}
}
