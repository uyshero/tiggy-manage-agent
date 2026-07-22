package capability

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileModesRevisionAndChecksum(t *testing.T) {
	provider := LocalSystemProvider{}
	path := filepath.Join(t.TempDir(), "note.txt")
	created, err := provider.WriteFile(t.Context(), WriteFileRequest{Path: path, Content: []byte("first"), Mode: WriteModeCreate})
	if err != nil {
		t.Fatal(err)
	}
	if created.FileRevision == "" || created.ContentSHA256 == "" || created.Kind != "text" || created.Encoding != "utf-8" {
		t.Fatalf("missing write metadata: %#v", created)
	}
	if _, err := provider.WriteFile(t.Context(), WriteFileRequest{Path: path, Content: []byte("again"), Mode: WriteModeCreate}); fileErrorCode(err) != "file_already_exists" {
		t.Fatalf("expected file_already_exists, got %v", err)
	}
	updated, err := provider.WriteFile(t.Context(), WriteFileRequest{
		Path: path, Content: []byte("second"), Mode: WriteModeOverwrite, ExpectedRevision: created.FileRevision,
	})
	if err != nil || updated.FileRevision == created.FileRevision {
		t.Fatalf("expected revision-changing overwrite, result=%#v err=%v", updated, err)
	}
	if err := os.WriteFile(path, []byte("external replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.WriteFile(t.Context(), WriteFileRequest{
		Path: path, Content: []byte("third"), Mode: WriteModeOverwrite, ExpectedRevision: updated.FileRevision,
	}); fileErrorCode(err) != "stale_file_revision" {
		t.Fatalf("expected stale_file_revision, got %v", err)
	}
	before, _ := os.ReadFile(path)
	if _, err := provider.WriteFile(t.Context(), WriteFileRequest{Path: path, Content: []byte("bad"), ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); fileErrorCode(err) != "content_checksum_mismatch" {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("failed atomic precondition changed original: before=%q after=%q", before, after)
	}
}

func TestEditFileRequiresUniqueMatchAndRevision(t *testing.T) {
	provider := LocalSystemProvider{}
	path := filepath.Join(t.TempDir(), "note.txt")
	created, err := provider.WriteFile(t.Context(), WriteFileRequest{Path: path, Content: []byte("foo foo")})
	if err != nil {
		t.Fatal(err)
	}
	notUnique, err := provider.EditFile(t.Context(), EditFileRequest{Path: path, OldString: "foo", NewString: "bar", ExpectedRevision: created.FileRevision})
	if err != nil || notUnique.Code != "match_not_unique" || notUnique.Success {
		t.Fatalf("unexpected non-unique result: %#v err=%v", notUnique, err)
	}
	expected := 2
	edited, err := provider.EditFile(t.Context(), EditFileRequest{
		Path: path, OldString: "foo", NewString: "bar", ReplaceAll: true,
		ExpectedMatchCount: &expected, ExpectedRevision: created.FileRevision,
	})
	if err != nil || !edited.Success || edited.Replacements != 2 || edited.FileRevision == "" || edited.ContentSHA256 == "" {
		t.Fatalf("unexpected edit result: %#v err=%v", edited, err)
	}
	stale, err := provider.EditFile(t.Context(), EditFileRequest{Path: path, OldString: "bar", NewString: "baz", ExpectedRevision: created.FileRevision})
	if err != nil || stale.Code != "stale_file_revision" {
		t.Fatalf("unexpected stale edit result: %#v err=%v", stale, err)
	}
}

func TestEditFileRejectsStaleContentHash(t *testing.T) {
	provider := LocalSystemProvider{}
	path := filepath.Join(t.TempDir(), "note.txt")
	created, err := provider.WriteFile(t.Context(), WriteFileRequest{Path: path, Content: []byte("old value")})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.EditFile(t.Context(), EditFileRequest{
		Path: path, OldString: "old", NewString: "new", ExpectedRevision: created.FileRevision,
		ExpectedContentSHA256: strings.Repeat("0", 64),
	})
	if err != nil || result.Success || result.Code != "stale_file_content" {
		t.Fatalf("unexpected stale-content result: %#v err=%v", result, err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "old value" {
		t.Fatalf("stale-content edit changed file: %q err=%v", content, err)
	}
}

func TestReadFileClassifiesPDFWithoutReturningBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nASCII body"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Binary || len(result.Content) != 0 || result.Kind != "document" || result.ContentType != "application/pdf" || result.SuggestedCapability != "document_skill" {
		t.Fatalf("unexpected PDF classification: %#v", result)
	}
}

func TestReadFileRoutesArchivesToRunCommandWithoutReturningBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(path, []byte{'P', 'K', 3, 4, 0, 0, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Binary || len(result.Content) != 0 || result.Kind != "archive" || result.SuggestedCapability != "run_command" {
		t.Fatalf("unexpected archive classification: %#v", result)
	}
}

func TestEditFileRejectsBinaryContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte{'\x89', 'P', 'N', 'G', '\r', '\n', 0, 1}, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (LocalSystemProvider{}).EditFile(t.Context(), EditFileRequest{Path: path, OldString: "PNG", NewString: "BAD"})
	if err != nil || result.Success || result.Code != "unsupported_binary_edit" {
		t.Fatalf("unexpected binary edit result: %#v err=%v", result, err)
	}
}

func fileErrorCode(err error) string {
	var fileErr *FileReadError
	if errors.As(err, &fileErr) {
		return fileErr.Code
	}
	return ""
}
