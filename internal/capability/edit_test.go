package capability

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestEditLocalFileProducesStandardUnifiedDiffForInsertion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result := editLocalFile(EditFileRequest{
		Path:      path,
		OldString: "alpha\n",
		NewString: "alpha\ninserted\n",
	})
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	if result.LinesAdded != 1 || result.LinesDeleted != 0 {
		t.Fatalf("unexpected line counts: +%d/-%d\n%s", result.LinesAdded, result.LinesDeleted, result.DiffText)
	}
	if !strings.Contains(result.DiffText, "@@ -") || !strings.Contains(result.DiffText, "+inserted\n") {
		t.Fatalf("expected standard unified diff, got:\n%s", result.DiffText)
	}
	if strings.Contains(result.DiffText, "-beta\n") || strings.Contains(result.DiffText, "+beta\n") {
		t.Fatalf("unchanged lines must remain context, got:\n%s", result.DiffText)
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
		Path:      path,
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

func TestEditLocalFileRejectsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := editLocalFile(EditFileRequest{Path: path, OldString: "same", NewString: "same"})
	if result.Success || result.Code != "invalid_edit_noop" {
		t.Fatalf("expected no-op rejection, got %#v", result)
	}
}

func TestEditLocalFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxEditableFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result := editLocalFile(EditFileRequest{Path: path, OldString: "old", NewString: "new"})
	if result.Success || result.Code != "file_too_large" {
		t.Fatalf("expected oversized-file rejection, got %#v", result)
	}
}
