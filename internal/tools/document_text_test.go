package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/capability"
)

func TestReadableFileContentExtractsDOCXText(t *testing.T) {
	content := testDOCX(t, `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:tab/><w:t>DOCX</w:t></w:r></w:p>
    <w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
  </w:body>
</w:document>`)

	text, readable, err := readableFileContent("report.docx", content)
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if !readable || text != "Hello\tDOCX\nSecond paragraph" {
		t.Fatalf("unexpected extracted text: readable=%v text=%q", readable, text)
	}
}

func TestReadableFileContentRejectsBinaryData(t *testing.T) {
	text, readable, err := readableFileContent("archive.zip", []byte{'P', 'K', 0, 1})
	if err != nil {
		t.Fatalf("check binary data: %v", err)
	}
	if readable || text != "" {
		t.Fatalf("expected binary data to be unreadable, got readable=%v text=%q", readable, text)
	}
}

func TestReadFileExecutorEmitsPostgresSafeDOCXResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(path, testDOCX(t, `<?xml version="1.0"?>
<w:document xmlns:w="urn:word"><w:body><w:p><w:r><w:t>Report body</w:t></w:r></w:p></w:body></w:document>`), 0o644); err != nil {
		t.Fatalf("write docx: %v", err)
	}
	arguments, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	result, err := NewDefaultExecutor().Execute(context.Background(), Call{
		ID:         "call_docx",
		Identifier: DefaultIdentifier,
		APIName:    "read_file",
		Arguments:  arguments,
	}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if result.Content != "Report body" {
		t.Fatalf("unexpected docx content: %q", result.Content)
	}
	eventData, err := json.Marshal(ObservableResultData(result, ResultContextOptions{}))
	if err != nil {
		t.Fatalf("marshal observable result: %v", err)
	}
	if strings.Contains(string(eventData), `\u0000`) {
		t.Fatalf("observable result contains unsupported null escape: %s", eventData)
	}
}

func TestReadFileExecutorRejectsDOCXPaginationWithCapabilityError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(path, testDOCX(t, `<?xml version="1.0"?><w:document xmlns:w="urn:word"><w:body/></w:document>`), 0o644); err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]any{"path": path, "offset_bytes": 0, "max_bytes": 1024})
	result, err := NewDefaultExecutor().Execute(t.Context(), Call{
		ID: "call_docx_page", Identifier: DefaultIdentifier, APIName: "read_file", Arguments: arguments,
	}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == nil || result.Error.Type != "unsupported_read_pagination" || !strings.Contains(result.Content, "path only") {
		t.Fatalf("unexpected DOCX pagination result: %#v", result)
	}
}

func testDOCX(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create document.xml: %v", err)
	}
	if _, err := file.Write([]byte(documentXML)); err != nil {
		t.Fatalf("write document.xml: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close docx: %v", err)
	}
	return buffer.Bytes()
}
