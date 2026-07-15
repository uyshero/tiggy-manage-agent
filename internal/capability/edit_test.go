package capability

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEditLocalFileSingleReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result := editLocalFile(EditFileRequest{
		Path:      path,
		OldString: "world",
		NewString: "gopher",
	})
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	if result.Replacements != 1 {
		t.Fatalf("expected 1 replacement, got %d", result.Replacements)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello gopher\n" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestEditLocalFileReplaceAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("foo bar foo\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result := editLocalFile(EditFileRequest{
		Path:       path,
		OldString:  "foo",
		NewString:  "baz",
		ReplaceAll: true,
	})
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	if result.Replacements != 2 {
		t.Fatalf("expected 2 replacements, got %d", result.Replacements)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "baz bar baz\n" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestEditLocalFileOldStringNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result := editLocalFile(EditFileRequest{
		Path:      path,
		OldString: "missing",
		NewString: "replacement",
	})
	if result.Success {
		t.Fatalf("expected failure, got %#v", result)
	}
	if result.Error != "The specified old_string was not found in the file" {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestEditLocalFileIdempotencyRequiresRecordedPlaceholderHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	placeholder := "__TMA_PLACEHOLDER_REPORT_001__"
	replacement := "same content already elsewhere"
	if err := os.WriteFile(path, []byte(replacement+"\n"+placeholder), 0o644); err != nil {
		t.Fatal(err)
	}

	first := editLocalFile(EditFileRequest{Path: path, OldString: placeholder, NewString: replacement, Idempotent: true})
	if !first.Success || first.AlreadyApplied {
		t.Fatalf("expected first replacement, got %#v", first)
	}
	retry := editLocalFile(EditFileRequest{Path: path, OldString: placeholder, NewString: replacement, Idempotent: true})
	if !retry.Success || !retry.AlreadyApplied {
		t.Fatalf("expected hash-backed replay, got %#v", retry)
	}

	ResetSegmentEditState(path)
	withoutEvidence := editLocalFile(EditFileRequest{Path: path, OldString: placeholder, NewString: replacement, Idempotent: true})
	if withoutEvidence.Success || withoutEvidence.AlreadyApplied {
		t.Fatalf("matching content elsewhere must not imply already_applied: %#v", withoutEvidence)
	}
}

func TestEditLocalFileCRLFCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("line one\r\nline two\r\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result := editLocalFile(EditFileRequest{
		Path:      path,
		OldString: "line one\nline two",
		NewString: "updated",
	})
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "updated\r\n" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestLocalSystemProviderEditFile(t *testing.T) {
	provider := LocalSystemProvider{}
	path := filepath.Join(t.TempDir(), "note.txt")
	if _, err := provider.WriteFile(context.Background(), WriteFileRequest{
		Path:    path,
		Content: []byte("alpha beta"),
	}); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := provider.EditFile(context.Background(), EditFileRequest{
		FilePath:  path,
		OldString: "beta",
		NewString: "gamma",
	})
	if err != nil {
		t.Fatalf("edit file: %v", err)
	}
	if !result.Success || result.Replacements != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}

	readResult, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(readResult.Content) != "alpha gamma" {
		t.Fatalf("unexpected content: %q", string(readResult.Content))
	}
}
