package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type fileClassification struct {
	Kind                string
	ContentType         string
	Encoding            string
	SuggestedCapability string
}

func classifyOpenedFile(file *os.File, path string, size int64, binary bool) fileClassification {
	sampleSize := int64(512)
	if size < sampleSize {
		sampleSize = size
	}
	sample := make([]byte, int(sampleSize))
	if sampleSize > 0 {
		n, _ := file.ReadAt(sample, 0)
		sample = sample[:n]
	}
	return classifyFile(path, sample, binary)
}

func openedFileRequiresBinaryRouting(ctx context.Context, file *os.File, path string, size int64) (bool, error) {
	binary, err := openedFileIsBinary(ctx, file, size)
	if err != nil || binary {
		return binary, err
	}
	sampleSize := int64(512)
	if size < sampleSize {
		sampleSize = size
	}
	sample := make([]byte, int(sampleSize))
	if sampleSize > 0 {
		n, _ := file.ReadAt(sample, 0)
		sample = sample[:n]
	}
	classification := classifyFile(path, sample, true)
	extension := strings.ToLower(filepath.Ext(path))
	knownBinaryExtension := map[string]bool{
		".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
		".zip": true, ".gz": true, ".tgz": true, ".tar": true, ".docx": true, ".xlsx": true, ".pptx": true,
		".sqlite": true, ".sqlite3": true, ".db": true,
	}
	return knownBinaryExtension[extension] || classification.Kind == "image" || classification.Kind == "document" || classification.Kind == "archive", nil
}

func classifyFile(path string, sample []byte, binary bool) fileClassification {
	extension := strings.ToLower(filepath.Ext(path))
	contentType := mime.TypeByExtension(extension)
	if contentType == "" && len(sample) > 0 {
		contentType = http.DetectContentType(sample)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])

	classification := fileClassification{Kind: "text", ContentType: contentType, Encoding: "utf-8"}
	if !binary {
		if extension == ".docx" || extension == ".xlsx" || extension == ".pptx" {
			classification.Kind = "document"
			classification.Encoding = ""
			classification.SuggestedCapability = "document_skill"
		}
		return classification
	}

	classification.Kind = "binary"
	classification.Encoding = ""
	switch {
	case strings.HasPrefix(contentType, "image/"):
		classification.Kind = "image"
		classification.SuggestedCapability = "vision"
	case contentType == "application/pdf" || extension == ".pdf" || extension == ".docx" || extension == ".xlsx" || extension == ".pptx":
		classification.Kind = "document"
		classification.SuggestedCapability = "document_skill"
	case extension == ".zip" || extension == ".tar" || extension == ".gz" || extension == ".tgz" || contentType == "application/zip" || contentType == "application/x-gzip":
		classification.Kind = "archive"
		classification.SuggestedCapability = "execute_code"
	default:
		classification.SuggestedCapability = "execute_code"
	}
	return classification
}

func applyFileClassification(result *FileResult, classification fileClassification) {
	result.Kind = classification.Kind
	result.ContentType = classification.ContentType
	result.Encoding = classification.Encoding
	result.SuggestedCapability = classification.SuggestedCapability
}

func contentSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
