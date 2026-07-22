package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditPreviewIsReadOnlyAndMatchesExecutionPatch(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "preview.txt")
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := LocalSystemProvider{}
	request := EditFileRequest{Path: path, Edits: []EditOperation{
		{OldString: "alpha", NewString: "ALPHA"},
		{OldString: "gamma", NewString: "GAMMA"},
	}}
	preview, err := provider.PreviewEditFile(t.Context(), request)
	if err != nil {
		t.Fatalf("PreviewEditFile() error = %v", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != original {
		t.Fatalf("preview changed file: content=%q err=%v", content, readErr)
	}
	sum := sha256.Sum256([]byte(preview.UnifiedDiff))
	if !preview.Success || preview.BaseRevision == "" || preview.BaseContentSHA256 == "" ||
		preview.PatchSHA256 != hex.EncodeToString(sum[:]) || preview.LinesAdded != 2 || preview.LinesDeleted != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	request.ExpectedRevision = preview.BaseRevision
	request.ExpectedContentSHA256 = preview.BaseContentSHA256
	result, err := provider.EditFile(t.Context(), request)
	if err != nil {
		t.Fatalf("EditFile() error = %v", err)
	}
	if !result.Success || result.DiffText != preview.UnifiedDiff || result.PatchSHA256 != preview.PatchSHA256 ||
		result.BaseRevision != preview.BaseRevision || result.BaseContentSHA256 != preview.BaseContentSHA256 {
		t.Fatalf("result = %+v preview = %+v", result, preview)
	}
}

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

func TestEditLocalFileAppliesMultipleReplacementsAtomically(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "multi.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := editLocalFile(EditFileRequest{
		Path: path,
		Edits: []EditOperation{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "gamma", NewString: "GAMMA"},
		},
	})
	if !result.Success || result.Replacements != 2 || result.LinesAdded != 2 || result.LinesDeleted != 2 {
		t.Fatalf("multi edit result = %+v", result)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("multi edit content = %q err=%v", content, err)
	}
}

func TestEditLocalFileRejectsOverlappingMultiEditWithoutWriting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "overlap.txt")
	original := "first second third\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	result := editLocalFile(EditFileRequest{
		Path: path,
		Edits: []EditOperation{
			{OldString: "first second", NewString: "FIRST"},
			{OldString: "second third", NewString: "THIRD"},
		},
	})
	if result.Success || result.Code != "overlapping_edits" {
		t.Fatalf("overlapping edit result = %+v", result)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != original {
		t.Fatalf("overlapping edit changed content = %q err=%v", content, err)
	}
}

func TestEditLocalFileRejectsIncompleteMultiEditWithoutWriting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.txt")
	original := "alpha\nbeta\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	result := editLocalFile(EditFileRequest{
		Path: path,
		Edits: []EditOperation{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "missing", NewString: "MISSING"},
		},
	})
	if result.Success || result.Code != "match_not_found" {
		t.Fatalf("incomplete edit result = %+v", result)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != original {
		t.Fatalf("incomplete edit changed content = %q err=%v", content, err)
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
