package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibreOfficeDocumentPreviewConverterUsesIsolatedProfile(t *testing.T) {
	testDir := t.TempDir()
	argsPath := filepath.Join(testDir, "args.txt")
	homePath := filepath.Join(testDir, "home.txt")
	binaryPath := filepath.Join(testDir, "fake-soffice")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$TMA_TEST_PREVIEW_ARGS"
printf '%s' "$HOME" > "$TMA_TEST_PREVIEW_HOME"
outdir=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--outdir' ]; then
    shift
    outdir="$1"
    break
  fi
  shift
done
printf '%%PDF-1.7\npreview' > "$outdir/document.pdf"
`
	if err := os.WriteFile(binaryPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake soffice: %v", err)
	}
	t.Setenv("TMA_TEST_PREVIEW_ARGS", argsPath)
	t.Setenv("TMA_TEST_PREVIEW_HOME", homePath)

	pdf, err := (libreOfficeDocumentPreviewConverter{binary: binaryPath}).ConvertDOCXToPDF(
		context.Background(),
		documentPreviewInput{Filename: "每日 AI 新闻.docx", Content: []byte("docx")},
	)
	if err != nil {
		t.Fatalf("convert DOCX: %v", err)
	}
	if string(pdf) != "%PDF-1.7\npreview" {
		t.Fatalf("unexpected PDF: %q", pdf)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	if !strings.Contains(string(args), "-env:UserInstallation=file://") || !strings.Contains(string(args), "document.docx") {
		t.Fatalf("conversion did not use an isolated profile and stable input name: %s", args)
	}
	home, err := os.ReadFile(homePath)
	if err != nil {
		t.Fatalf("read captured home: %v", err)
	}
	if !strings.Contains(string(home), "tma-docx-preview-") {
		t.Fatalf("conversion HOME was not isolated: %q", home)
	}
}
