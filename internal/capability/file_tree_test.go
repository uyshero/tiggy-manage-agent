package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindFilesGlobExcludeHiddenAndContinuation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), "package a\n")
	writeTestFile(t, filepath.Join(root, "nested", "b.go"), "package b\n")
	writeTestFile(t, filepath.Join(root, "vendor", "ignored.go"), "package ignored\n")
	writeTestFile(t, filepath.Join(root, ".hidden", "secret.go"), "package secret\n")

	provider := LocalSystemProvider{}
	first, err := provider.FindFiles(t.Context(), FindFilesRequest{
		Root: root, Pattern: "**/*.go", Exclude: []string{"vendor/**"}, MaxResults: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 || first.Files[0].Path != "a.go" || !first.Truncated || first.NextPath != "a.go" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	second, err := provider.FindFiles(t.Context(), FindFilesRequest{
		Root: root, Pattern: "**/*.go", Exclude: []string{"vendor/**"}, MaxResults: 10, AfterPath: first.NextPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Files) != 1 || second.Files[0].Path != "nested/b.go" {
		t.Fatalf("unexpected continuation: %#v", second)
	}
}

func TestSearchFilesLiteralRegexAndBinarySkip(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), "package a\nfunc DefaultRegistry() {}\n")
	writeTestFile(t, filepath.Join(root, "nested", "b.go"), "package b\n// defaultregistry marker\n")
	if err := os.WriteFile(filepath.Join(root, "manual.pdf"), []byte("%PDF-1.7\ntext that must not be searched"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := LocalSystemProvider{}
	literal, err := provider.SearchFiles(t.Context(), SearchFilesRequest{
		Root: root, Query: "DefaultRegistry", Paths: []string{"**/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(literal.Matches) != 1 || literal.Matches[0].Path != "a.go" || literal.Matches[0].LineNumber != 2 || literal.SkippedBinaryFiles != 1 {
		t.Fatalf("unexpected literal result: %#v", literal)
	}

	caseSensitive := false
	regex, err := provider.SearchFiles(t.Context(), SearchFilesRequest{
		Root: root, Query: `default(registry)?`, Paths: []string{"**/*.go"}, Mode: "regex", CaseSensitive: &caseSensitive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(regex.Matches) != 2 {
		t.Fatalf("unexpected regex result: %#v", regex)
	}
}

func TestSearchFilesHandlesLongLiteralLine(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "long.log"), strings.Repeat("x", 128<<10)+"needle\n")
	result, err := (LocalSystemProvider{}).SearchFiles(t.Context(), SearchFilesRequest{
		Root: root, Query: "needle", Paths: []string{"*.log"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || !result.Matches[0].LineTruncated {
		t.Fatalf("unexpected long-line result: %#v", result)
	}
}

func TestSearchFilesPreservesOffsetsAndPropagatesPerFileTruncation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "unicode.txt"), "前ä后\n")
	caseSensitive := false
	unicodeResult, err := (LocalSystemProvider{}).SearchFiles(t.Context(), SearchFilesRequest{
		Root: root, Query: "Ä", Paths: []string{"unicode.txt"}, CaseSensitive: &caseSensitive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unicodeResult.Matches) != 1 || unicodeResult.Matches[0].OffsetBytes != int64(len("前")) {
		t.Fatalf("unexpected case-insensitive byte offset: %#v", unicodeResult)
	}

	writeTestFile(t, filepath.Join(root, "many.txt"), strings.Repeat("needle\n", 150))
	truncated, err := (LocalSystemProvider{}).SearchFiles(t.Context(), SearchFilesRequest{
		Root: root, Query: "needle", Paths: []string{"many.txt"}, MaxResults: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(truncated.Matches) != hardSearchFileMaxResults || !truncated.Truncated {
		t.Fatalf("per-file truncation was lost: %#v", truncated)
	}
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
