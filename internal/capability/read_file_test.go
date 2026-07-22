package capability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReadFileSmallFileBackwardCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.txt")
	want := []byte("hello, 世界 🙂\nlast line")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read small file: %v", err)
	}
	if string(result.Content) != string(want) {
		t.Fatalf("content changed: got %q want %q", result.Content, want)
	}
	if !result.EOF || result.Truncated || result.ReturnedBytes != len(want) || result.NextOffsetBytes != int64(len(want)) {
		t.Fatalf("unexpected metadata: %#v", result)
	}
	if result.FileRevision == "" || result.StartLine != 1 || result.EndLine != 2 {
		t.Fatalf("missing stable metadata: %#v", result)
	}
	if result.ContentSHA256 != contentSHA256(want) {
		t.Fatalf("full read content hash = %q, want %q", result.ContentSHA256, contentSHA256(want))
	}
}

func TestReadFileLargeSparseFileReturnsBoundedFirstPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte(strings.Repeat("a", DefaultReadFileDefaultMaxBytes*2))
	if _, err := file.Write(prefix); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(100 << 20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read sparse file: %v", err)
	}
	if result.SizeBytes != 100<<20 || len(result.Content) != DefaultReadFileDefaultMaxBytes {
		t.Fatalf("large file was not page-bounded: %#v", result)
	}
	if result.EOF || !result.Truncated || result.NextOffsetBytes != DefaultReadFileDefaultMaxBytes {
		t.Fatalf("unexpected first-page metadata: %#v", result)
	}
	if result.ContentSHA256 != "" {
		t.Fatalf("partial reads must not claim a whole-file content hash: %#v", result)
	}
}

func TestReadFileBytePagesConvergeToEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pages.txt")
	want := strings.Repeat("0123456789", 9) + "END"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := LocalSystemProvider{}
	offset := int64(0)
	maxBytes := 11
	revision := ""
	var got strings.Builder
	for page := 0; page < 20; page++ {
		result, err := provider.ReadFile(t.Context(), ReadFileRequest{
			Path: path, OffsetBytes: &offset, MaxBytes: &maxBytes, FileRevision: revision,
		})
		if err != nil {
			t.Fatalf("read page %d: %v", page, err)
		}
		if revision == "" {
			revision = result.FileRevision
		}
		if result.OffsetBytes != offset || result.NextOffsetBytes <= offset && !result.EOF {
			t.Fatalf("page did not advance: %#v", result)
		}
		got.Write(result.Content)
		offset = result.NextOffsetBytes
		if result.EOF {
			if offset != int64(len(want)) || got.String() != want {
				t.Fatalf("pagination did not converge: offset=%d content=%q", offset, got.String())
			}
			return
		}
	}
	t.Fatal("pagination did not reach eof")
}

func TestReadFileAlignsUTF8OffsetsAndPageEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "utf8.txt")
	content := "A中文🙂B"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := LocalSystemProvider{}

	insideChinese := int64(2)
	maxBytes := 8
	result, err := provider.ReadFile(t.Context(), ReadFileRequest{Path: path, OffsetBytes: &insideChinese, MaxBytes: &maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	if result.OffsetBytes != 4 || result.RequestedOffsetBytes == nil || *result.RequestedOffsetBytes != insideChinese {
		t.Fatalf("offset was not aligned forward: %#v", result)
	}
	if !utf8.Valid(result.Content) || string(result.Content) != "文🙂B" {
		t.Fatalf("invalid aligned content %q", result.Content)
	}

	start := int64(1)
	maxBytes = 4
	result, err = provider.ReadFile(t.Context(), ReadFileRequest{Path: path, OffsetBytes: &start, MaxBytes: &maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Content) != "中" || result.NextOffsetBytes != 4 || !utf8.Valid(result.Content) {
		t.Fatalf("page end split a UTF-8 rune: %#v", result)
	}
}

func TestReadFileLineModePreservesCRLFAndUnterminatedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lines.txt")
	if err := os.WriteFile(path, []byte("一\r\nsecond\r\n🙂last"), 0o644); err != nil {
		t.Fatal(err)
	}
	startLine, maxLines := 2, 2
	result, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{
		Path: path, StartLine: &startLine, MaxLines: &maxLines,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Content) != "second\r\n🙂last" {
		t.Fatalf("line mode changed bytes: %q", result.Content)
	}
	if result.StartLine != 2 || result.EndLine != 3 || !result.EOF || result.LineTruncated {
		t.Fatalf("unexpected line metadata: %#v", result)
	}
}

func TestReadFileLineModeBoundsVeryLongSingleLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one-line.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("界", 10000)), 0o644); err != nil {
		t.Fatal(err)
	}
	limits := ReadFileLimits{DefaultMaxBytes: 300, HardMaxBytes: 512, SmallFileBytes: 256, MaxLines: 10}
	startLine, maxLines := 1, 1
	result, err := (LocalSystemProvider{ReadFileLimits: limits}).ReadFile(t.Context(), ReadFileRequest{
		Path: path, StartLine: &startLine, MaxLines: &maxLines,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) > limits.DefaultMaxBytes || !utf8.Valid(result.Content) || !result.LineTruncated {
		t.Fatalf("long line was not safely bounded: %#v", result)
	}
	if result.EOF || result.NextOffsetBytes != int64(len(result.Content)) {
		t.Fatalf("unexpected long-line continuation: %#v", result)
	}
}

func TestReadFileLineModeMarksExactFullBufferLineAsTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exact-buffer-line.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", readFileBufferBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	limits := ReadFileLimits{
		DefaultMaxBytes: readFileBufferBytes,
		HardMaxBytes:    readFileBufferBytes,
		SmallFileBytes:  MinReadFileConfiguredBytes,
		MaxLines:        10,
	}
	startLine, maxLines := 1, 1
	result, err := (LocalSystemProvider{ReadFileLimits: limits}).ReadFile(t.Context(), ReadFileRequest{
		Path: path, StartLine: &startLine, MaxLines: &maxLines,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != readFileBufferBytes || !result.LineTruncated || result.EOF {
		t.Fatalf("expected an exact-buffer partial line, got %#v", result)
	}
}

func TestReadFileRejectsInvalidRangesAndHardLimitByCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ranges.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	negative := int64(-1)
	offset := int64(0)
	tooFar := int64(100)
	bytesLimit := 16
	tooManyBytes := DefaultReadFileHardMaxBytes + 1
	line := 1
	tooManyLines := DefaultReadFileMaxLines + 1
	tests := []struct {
		name string
		req  ReadFileRequest
		code string
	}{
		{name: "negative", req: ReadFileRequest{Path: path, OffsetBytes: &negative}, code: "invalid_read_range"},
		{name: "mixed", req: ReadFileRequest{Path: path, OffsetBytes: &offset, StartLine: &line, MaxBytes: &bytesLimit}, code: "invalid_read_range"},
		{name: "byte hard limit", req: ReadFileRequest{Path: path, MaxBytes: &tooManyBytes}, code: "read_limit_exceeded"},
		{name: "line hard limit", req: ReadFileRequest{Path: path, StartLine: &line, MaxLines: &tooManyLines}, code: "read_limit_exceeded"},
		{name: "offset", req: ReadFileRequest{Path: path, OffsetBytes: &tooFar, MaxBytes: &bytesLimit}, code: "offset_out_of_range"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (LocalSystemProvider{}).ReadFile(t.Context(), test.req)
			var readErr *FileReadError
			if !errors.As(err, &readErr) || readErr.Code != test.code {
				t.Fatalf("got err=%v, want code=%s", err, test.code)
			}
		})
	}
}

func TestReadFileRejectsStaleRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changing.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := int64(0)
	maxBytes := 10
	first, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{Path: path, OffsetBytes: &offset, MaxBytes: &maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("b", 101)), 0o644); err != nil {
		t.Fatal(err)
	}
	offset = first.NextOffsetBytes
	_, err = (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{
		Path: path, OffsetBytes: &offset, MaxBytes: &maxBytes, FileRevision: first.FileRevision,
	})
	var readErr *FileReadError
	if !errors.As(err, &readErr) || readErr.Code != "stale_file_revision" {
		t.Fatalf("expected stale revision, got %v", err)
	}
}

func TestReadFileHonorsCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cancel.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (LocalSystemProvider{}).ReadFile(ctx, ReadFileRequest{Path: path})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestReadFileAndSearchFileHonorRequestDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deadline.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(-time.Second)
	meta := NewRequestMeta("session", "turn", &deadline)

	_, err := (LocalSystemProvider{}).ReadFile(t.Context(), ReadFileRequest{Meta: meta, Path: path})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read_file ignored request deadline: %v", err)
	}
	_, err = (LocalSystemProvider{}).SearchFile(t.Context(), SearchFileRequest{Meta: meta, Path: path, Query: "hello"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("search_file ignored request deadline: %v", err)
	}
}

func TestSearchFileReturnsLineAndRawByteOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.log")
	longPrefix := strings.Repeat("x", maxSearchFileLineBytes+100)
	content := "first\n" + longPrefix + "关键字🙂tail\nlast"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (LocalSystemProvider{}).SearchFile(t.Context(), SearchFileRequest{Path: path, Query: "关键字🙂"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("unexpected matches: %#v", result)
	}
	match := result.Matches[0]
	wantOffset := int64(len("first\n") + len(longPrefix))
	if match.LineNumber != 2 || match.OffsetBytes != wantOffset || !match.LineTruncated || !utf8.ValidString(match.Line) {
		t.Fatalf("unexpected match: %#v wantOffset=%d", match, wantOffset)
	}
	if result.FileRevision == "" || result.Truncated {
		t.Fatalf("unexpected search metadata: %#v", result)
	}
}
