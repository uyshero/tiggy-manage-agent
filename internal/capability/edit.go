package capability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const maxEditableFileBytes int64 = 64 << 20

// EditFileRequest 描述一次精确字符串替换编辑。
type EditFileRequest struct {
	Meta                  RequestMeta `json:"meta"`
	Path                  string      `json:"path,omitempty"`
	OldString             string      `json:"old_string"`
	NewString             string      `json:"new_string"`
	ReplaceAll            bool        `json:"replace_all,omitempty"`
	WorkDir               string      `json:"work_dir,omitempty"`
	ExpectedRevision      string      `json:"expected_revision,omitempty"`
	ExpectedContentSHA256 string      `json:"expected_content_sha256,omitempty"`
	ExpectedMatchCount    *int        `json:"expected_match_count,omitempty"`
	guardedRoot           string
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
	return resolveAgainstWorkDir(r.Path, r.WorkDir)
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
	return editLocalFileContextWithOpenHook(ctx, request, nil)
}

func editLocalFileContextWithOpenHook(ctx context.Context, request EditFileRequest, beforeOpen func()) EditFileResult {
	filePath := request.resolvedPath()
	if filePath == "" {
		return editFailure(filePath, "invalid_edit_path", "file path is required")
	}
	if request.OldString == "" {
		return editFailure(filePath, "invalid_edit_match", "old_string is required")
	}
	if request.OldString == request.NewString {
		return editFailure(filePath, "invalid_edit_noop", "old_string and new_string must be different")
	}
	if err := ctx.Err(); err != nil {
		return editFailure(filePath, "edit_canceled", err.Error())
	}
	if err := ensureGuardedMutationPath(filePath, request.guardedRoot); err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			return editFailure(filePath, fileErr.Code, fileErr.Message)
		}
		return editFailure(filePath, "workspace_path_changed", err.Error())
	}

	file, err := openLocalFileForEdit(request, beforeOpen)
	if err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			return editFailure(filePath, fileErr.Code, fileErr.Message)
		}
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
	if info.Size() > maxEditableFileBytes {
		_ = file.Close()
		return editFailure(filePath, "file_too_large", fmt.Sprintf("edit_file supports files up to %d bytes", maxEditableFileBytes))
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
	if expected := strings.ToLower(strings.TrimSpace(request.ExpectedContentSHA256)); expected != "" {
		actual := contentSHA256(contentBytes)
		if actual != expected {
			_ = file.Close()
			return editFailure(filePath, "stale_file_content", "file content changed since it was read")
		}
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

	matchCount := strings.Count(content, search)
	if matchCount == 0 {
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
		ExpectedRevision: revision, guardedRoot: request.guardedRoot,
	})
	if err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			return editFailure(filePath, fileErr.Code, fileErr.Message)
		}
		return editFailure(filePath, "edit_write_failed", err.Error())
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

func toCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func createPatch(filePath, oldContent, newContent string) string {
	patch, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldContent),
		B:        difflib.SplitLines(newContent),
		FromFile: filePath,
		ToFile:   filePath,
		Context:  3,
	})
	return patch
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
