package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var segmentedEditHashes sync.Map

// EditFileRequest 描述一次精确字符串替换编辑。
type EditFileRequest struct {
	Meta               RequestMeta `json:"meta"`
	Path               string      `json:"path,omitempty"`
	FilePath           string      `json:"file_path,omitempty"`
	OldString          string      `json:"old_string"`
	NewString          string      `json:"new_string"`
	ReplaceAll         bool        `json:"replace_all,omitempty"`
	WorkDir            string      `json:"work_dir,omitempty"`
	Idempotent         bool        `json:"idempotent,omitempty"`
	ExpectedRevision   string      `json:"expected_revision,omitempty"`
	ExpectedMatchCount *int        `json:"expected_match_count,omitempty"`
}

// EditFileResult 对齐 local-file-shell editLocalFile 的返回结构。
type EditFileResult struct {
	Path           string `json:"path"`
	DiffText       string `json:"diff_text,omitempty"`
	LinesAdded     int    `json:"lines_added,omitempty"`
	LinesDeleted   int    `json:"lines_deleted,omitempty"`
	Replacements   int    `json:"replacements"`
	AlreadyApplied bool   `json:"already_applied,omitempty"`
	Success        bool   `json:"success"`
	Code           string `json:"code,omitempty"`
	Error          string `json:"error,omitempty"`
	FileRevision   string `json:"file_revision,omitempty"`
	ContentSHA256  string `json:"content_sha256,omitempty"`
}

func (r EditFileRequest) resolvedPath() string {
	raw := r.Path
	if raw == "" {
		raw = r.FilePath
	}
	return resolveAgainstWorkDir(raw, r.WorkDir)
}

func resolveAgainstWorkDir(path, workDir string) string {
	if path == "" {
		return path
	}
	if filepath.IsAbs(path) || workDir == "" {
		return path
	}
	return filepath.Join(workDir, path)
}

// editLocalFile 读取文件、做字面量替换并写回，逻辑对齐 packages/local-file-shell/src/file/edit.ts。
func editLocalFile(request EditFileRequest) EditFileResult {
	return editLocalFileContext(context.Background(), request)
}

func editLocalFileContext(ctx context.Context, request EditFileRequest) EditFileResult {
	filePath := request.resolvedPath()
	if filePath == "" {
		return editFailure(filePath, "invalid_edit_path", "file path is required")
	}
	if request.OldString == "" {
		return editFailure(filePath, "invalid_edit_match", "old_string is required")
	}
	if err := ctx.Err(); err != nil {
		return editFailure(filePath, "edit_canceled", err.Error())
	}

	file, err := os.Open(filePath)
	if err != nil {
		return editFailure(filePath, "file_not_found", err.Error())
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return editFailure(filePath, "edit_read_failed", err.Error())
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return editFailure(filePath, "unsupported_file_type", "edit_file only supports regular files")
	}
	revision := fileRevision(info)
	if request.ExpectedRevision != "" && request.ExpectedRevision != revision {
		_ = file.Close()
		return editFailure(filePath, "stale_file_revision", "file changed since it was read")
	}
	binary, err := openedFileRequiresBinaryRouting(ctx, file, filePath, info.Size())
	if err != nil {
		_ = file.Close()
		return editFailure(filePath, "edit_read_failed", err.Error())
	}
	if binary {
		_ = file.Close()
		return editFailure(filePath, "unsupported_binary_edit", "edit_file only supports UTF-8 text; use a format-specific tool to create a new binary artifact")
	}
	contentBytes, err := io.ReadAll(file)
	if err != nil {
		_ = file.Close()
		return editFailure(filePath, "edit_read_failed", err.Error())
	}
	if err := ensureFileRevision(file, filePath, revision); err != nil {
		_ = file.Close()
		return editFailure(filePath, "stale_file_revision", err.Error())
	}
	_ = file.Close()
	content := string(contentBytes)

	search := request.OldString
	replace := request.NewString
	if !strings.Contains(content, search) && strings.Contains(content, "\r\n") {
		crlfSearch := toCRLF(search)
		if strings.Contains(content, crlfSearch) {
			search = crlfSearch
			replace = toCRLF(replace)
		}
	}

	segmentHash := editSegmentHash(request.NewString)
	matchCount := strings.Count(content, search)
	if matchCount == 0 {
		if request.Idempotent && recordedSegmentEdit(filePath, request.OldString, segmentHash) {
			return EditFileResult{
				Path:           filePath,
				Replacements:   0,
				AlreadyApplied: true,
				Success:        true,
			}
		}
		return editFailure(filePath, "match_not_found", "The specified old_string was not found in the file")
	}
	expectedMatches := 1
	validateMatchCount := !request.ReplaceAll
	if request.ExpectedMatchCount != nil {
		expectedMatches = *request.ExpectedMatchCount
		validateMatchCount = true
	}
	if expectedMatches < 1 {
		return editFailure(filePath, "invalid_edit_match", "expected_match_count must be at least 1")
	}
	if validateMatchCount && matchCount != expectedMatches {
		return editFailure(filePath, "match_not_unique", fmt.Sprintf("old_string matched %d times; expected %d", matchCount, expectedMatches))
	}

	var newContent string
	var replacements int

	if request.ReplaceAll {
		replacements = matchCount
		newContent = strings.ReplaceAll(content, search, replace)
	} else {
		index := strings.Index(content, search)
		if index == -1 {
			return editFailure(filePath, "match_not_found", "Old string not found")
		}
		newContent = content[:index] + replace + content[index+len(search):]
		replacements = 1
	}

	written, err := writeLocalFileAtomic(ctx, WriteFileRequest{
		Meta: request.Meta, Path: filePath, Content: []byte(newContent), Mode: WriteModeOverwrite,
		ExpectedRevision: revision,
	})
	if err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			return editFailure(filePath, fileErr.Code, fileErr.Message)
		}
		return editFailure(filePath, "edit_write_failed", err.Error())
	}
	if request.Idempotent {
		recordSegmentEdit(filePath, request.OldString, segmentHash)
	}

	patch := createPatch(filePath, content, newContent)
	diffText := fmt.Sprintf("diff --git a%s b%s\n%s", filePath, filePath, patch)
	linesAdded, linesDeleted := countPatchLineChanges(patch)

	return EditFileResult{
		Path:          filePath,
		DiffText:      diffText,
		LinesAdded:    linesAdded,
		LinesDeleted:  linesDeleted,
		Replacements:  replacements,
		Success:       true,
		FileRevision:  written.FileRevision,
		ContentSHA256: written.ContentSHA256,
	}
}

func editFailure(path, code, message string) EditFileResult {
	return EditFileResult{Path: path, Replacements: 0, Success: false, Code: code, Error: message}
}

func editSegmentHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func segmentedEditKey(filePath, placeholder string) string {
	absolutePath, err := filepath.Abs(filePath)
	if err == nil {
		filePath = filepath.Clean(absolutePath)
	}
	return filePath + "\x00" + placeholder
}

func recordSegmentEdit(filePath, placeholder, hash string) {
	if filePath == "" || placeholder == "" || hash == "" {
		return
	}
	segmentedEditHashes.Store(segmentedEditKey(filePath, placeholder), hash)
}

func recordedSegmentEdit(filePath, placeholder, hash string) bool {
	value, ok := segmentedEditHashes.Load(segmentedEditKey(filePath, placeholder))
	return ok && value == hash
}

// ResetSegmentEditState clears retry evidence when a file is recreated.
func ResetSegmentEditState(filePath string) {
	prefix := segmentedEditKey(filePath, "")
	segmentedEditHashes.Range(func(key, _ any) bool {
		if text, ok := key.(string); ok && strings.HasPrefix(text, prefix) {
			segmentedEditHashes.Delete(key)
		}
		return true
	})
}

func toCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func createPatch(filePath, oldContent, newContent string) string {
	var builder strings.Builder
	builder.WriteString("--- ")
	builder.WriteString(filePath)
	builder.WriteByte('\n')
	builder.WriteString("+++ ")
	builder.WriteString(filePath)
	builder.WriteByte('\n')

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for index := 0; index < maxLen; index++ {
		hasOld := index < len(oldLines)
		hasNew := index < len(newLines)
		var oldLine, newLine string
		if hasOld {
			oldLine = oldLines[index]
		}
		if hasNew {
			newLine = newLines[index]
		}
		if hasOld && hasNew && oldLine == newLine {
			continue
		}
		if hasOld {
			builder.WriteByte('-')
			builder.WriteString(oldLine)
			builder.WriteByte('\n')
		}
		if hasNew {
			builder.WriteByte('+')
			builder.WriteString(newLine)
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

func countPatchLineChanges(patch string) (linesAdded, linesDeleted int) {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			linesAdded++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			linesDeleted++
		}
	}
	return linesAdded, linesDeleted
}

func FormatEditResult(result EditFileResult) string {
	if !result.Success {
		if result.Error != "" {
			return result.Error
		}
		return "edit file failed"
	}
	if result.AlreadyApplied {
		return fmt.Sprintf("Edit already applied to %s; no duplicate content was written.", result.Path)
	}
	return fmt.Sprintf(
		"Edited %s: %d replacement(s), +%d/-%d lines.",
		result.Path,
		result.Replacements,
		result.LinesAdded,
		result.LinesDeleted,
	)
}
