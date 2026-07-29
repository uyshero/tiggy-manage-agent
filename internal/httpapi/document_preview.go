package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const maxDocumentPreviewBytes int64 = 25 * 1024 * 1024

var errDocumentPreviewUnsupported = errors.New("document preview unsupported")

type documentPreviewConverter interface {
	ConvertDOCXToPDF(ctx context.Context, input documentPreviewInput) ([]byte, error)
}

type documentPreviewInput struct {
	Filename string
	Content  []byte
}

type libreOfficeDocumentPreviewConverter struct {
	binary string
}

func defaultDocumentPreviewConverter() documentPreviewConverter {
	return libreOfficeDocumentPreviewConverter{}
}

func (c libreOfficeDocumentPreviewConverter) ConvertDOCXToPDF(ctx context.Context, input documentPreviewInput) ([]byte, error) {
	binary := strings.TrimSpace(c.binary)
	if binary == "" {
		binary = documentPreviewBinary()
	}
	if binary == "" {
		return nil, fmt.Errorf("%w: soffice/libreoffice binary was not found", errDocumentPreviewUnsupported)
	}
	tmpDir, err := os.MkdirTemp("", "tma-docx-preview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	inputName := safeArtifactFileName(input.Filename)
	if !isDOCXName(inputName) {
		inputName = "document.docx"
	}
	inputPath := filepath.Join(tmpDir, inputName)
	if err := os.WriteFile(inputPath, input.Content, 0600); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, binary, "--headless", "--convert-to", "pdf", "--outdir", tmpDir, inputPath)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("convert DOCX preview: %w: %s", err, strings.TrimSpace(string(output)))
	}
	pdfPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".pdf"
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("read converted PDF preview: %w", err)
	}
	if len(pdf) == 0 {
		return nil, fmt.Errorf("converted PDF preview is empty")
	}
	return pdf, nil
}

func documentPreviewBinary() string {
	for _, name := range []string{"soffice", "libreoffice"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func contentDispositionInline(filename string) string {
	filename = safeArtifactFileName(filename)
	return fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(filename, `"`, "_"))
}

func sha256Hex(content []byte) string {
	checksum := sha256.Sum256(content)
	return hex.EncodeToString(checksum[:])
}

func isDOCXArtifact(artifact managedagents.SessionArtifact, objectRef managedagents.ObjectRef, contentType string) bool {
	return isDOCXName(artifact.Name) || isDOCXName(objectRef.ObjectKey) || isDOCXContentType(contentType) || isDOCXContentType(objectRef.ContentType)
}

func isDOCXName(name string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(name)), ".docx")
}

func isDOCXContentType(contentType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(contentType))
	return normalized == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
}

func (s *Server) previewSessionArtifact(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "pdf"
	}
	if format != "pdf" {
		writeError(w, fmt.Errorf("%w: unsupported artifact preview format %q", managedagents.ErrInvalid, format))
		return
	}

	sessionID := r.PathValue("session_id")
	artifactID := r.PathValue("artifact_id")
	artifact, err := managedagents.GetSessionArtifactWithContext(r.Context(), s.store, sessionID, artifactID)
	if err != nil {
		writeError(w, err)
		return
	}
	objectRef, err := s.getObjectRefForRequest(r, artifact.ObjectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	if objectRef.WorkspaceID != artifact.WorkspaceID {
		writeError(w, fmt.Errorf("%w: artifact workspace mismatch", managedagents.ErrInvalid))
		return
	}
	if objectRef.SizeBytes > maxDocumentPreviewBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "artifact is too large for document preview"})
		return
	}

	verified, err := managedagents.ReadVerifiedObject(r.Context(), s.objectStore, objectRef, maxDocumentPreviewBytes)
	if err != nil {
		writeError(w, err)
		return
	}

	contentType := verified.ContentType
	if contentType == "" {
		contentType = objectRef.ContentType
	}
	if !isDOCXArtifact(artifact, objectRef, contentType) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "only DOCX artifacts can be converted to PDF previews"})
		return
	}
	content := verified.Content
	if int64(len(content)) > maxDocumentPreviewBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "artifact is too large for document preview"})
		return
	}
	previewer := s.documentPreviewer
	if previewer == nil {
		previewer = defaultDocumentPreviewConverter()
	}
	pdf, err := previewer.ConvertDOCXToPDF(r.Context(), documentPreviewInput{
		Filename: artifact.Name,
		Content:  content,
	})
	if err != nil {
		if errors.Is(err, errDocumentPreviewUnsupported) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		writeError(w, err)
		return
	}
	filename := strings.TrimSuffix(artifact.Name, filepath.Ext(artifact.Name)) + ".pdf"
	if strings.TrimSpace(filename) == ".pdf" {
		filename = artifact.ID + ".pdf"
	}
	checksum := sha256Hex(pdf)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(len(pdf)))
	w.Header().Set("Content-Disposition", contentDispositionInline(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Digest", "sha-256="+checksum)
	http.ServeContent(w, r, filename, artifact.CreatedAt, bytes.NewReader(pdf))
}
