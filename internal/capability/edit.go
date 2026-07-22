package capability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const maxEditableFileBytes int64 = 64 << 20

// EditFileRequest 描述一次精确字符串替换编辑。
type EditFileRequest struct {
	Meta                  RequestMeta     `json:"meta"`
	Path                  string          `json:"path,omitempty"`
	Edits                 []EditOperation `json:"edits,omitempty"`
	OldString             string          `json:"old_string"`
	NewString             string          `json:"new_string"`
	ReplaceAll            bool            `json:"replace_all,omitempty"`
	WorkDir               string          `json:"work_dir,omitempty"`
	ExpectedRevision      string          `json:"expected_revision,omitempty"`
	ExpectedContentSHA256 string          `json:"expected_content_sha256,omitempty"`
	ExpectedMatchCount    *int            `json:"expected_match_count,omitempty"`
	guardedRoot           string
}

type EditOperation struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// EditFileResult 对齐 local-file-shell editLocalFile 的返回结构。
type EditFileResult struct {
	Path              string `json:"path"`
	DiffText          string `json:"diff_text,omitempty"`
	PatchSHA256       string `json:"patch_sha256,omitempty"`
	BaseRevision      string `json:"base_revision,omitempty"`
	BaseContentSHA256 string `json:"base_sha256,omitempty"`
	LinesAdded        int    `json:"lines_added,omitempty"`
	LinesDeleted      int    `json:"lines_deleted,omitempty"`
	Replacements      int    `json:"replacements"`
	AlreadyApplied    bool   `json:"already_applied,omitempty"`
	Success           bool   `json:"success"`
	Code              string `json:"code,omitempty"`
	Error             string `json:"error,omitempty"`
	FileRevision      string `json:"file_revision,omitempty"`
	ContentSHA256     string `json:"content_sha256,omitempty"`
}

type EditFilePreview struct {
	Path              string `json:"path"`
	BaseRevision      string `json:"base_revision,omitempty"`
	BaseContentSHA256 string `json:"base_sha256,omitempty"`
	UnifiedDiff       string `json:"unified_diff,omitempty"`
	PatchSHA256       string `json:"patch_sha256,omitempty"`
	LinesAdded        int    `json:"lines_added,omitempty"`
	LinesDeleted      int    `json:"lines_deleted,omitempty"`
	Replacements      int    `json:"replacements"`
	Success           bool   `json:"success"`
	Code              string `json:"code,omitempty"`
	Error             string `json:"error,omitempty"`
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
	prepared, failure := prepareLocalFileEdit(ctx, request, beforeOpen)
	if failure != nil {
		return *failure
	}
	written, err := writeLocalFileAtomic(ctx, WriteFileRequest{
		Meta: request.Meta, Path: prepared.path, Content: []byte(prepared.newContent), Mode: WriteModeOverwrite,
		ExpectedRevision: prepared.baseRevision, guardedRoot: request.guardedRoot,
	})
	if err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			return editFailure(prepared.path, fileErr.Code, fileErr.Message)
		}
		return editFailure(prepared.path, "edit_write_failed", err.Error())
	}
	return EditFileResult{
		Path: prepared.path, DiffText: prepared.unifiedDiff, PatchSHA256: prepared.patchSHA256,
		BaseRevision: prepared.baseRevision, BaseContentSHA256: prepared.baseContentSHA256,
		LinesAdded: prepared.linesAdded, LinesDeleted: prepared.linesDeleted,
		Replacements: prepared.replacements, Success: true,
		FileRevision: written.FileRevision, ContentSHA256: written.ContentSHA256,
	}
}

func previewLocalFileContext(ctx context.Context, request EditFileRequest) EditFilePreview {
	prepared, failure := prepareLocalFileEdit(ctx, request, nil)
	if failure != nil {
		return EditFilePreview{
			Path: failure.Path, Replacements: failure.Replacements, Success: false,
			Code: failure.Code, Error: failure.Error,
		}
	}
	return EditFilePreview{
		Path: prepared.path, BaseRevision: prepared.baseRevision, BaseContentSHA256: prepared.baseContentSHA256,
		UnifiedDiff: prepared.unifiedDiff, PatchSHA256: prepared.patchSHA256,
		LinesAdded: prepared.linesAdded, LinesDeleted: prepared.linesDeleted,
		Replacements: prepared.replacements, Success: true,
	}
}

type preparedFileEdit struct {
	path              string
	baseRevision      string
	baseContentSHA256 string
	newContent        string
	unifiedDiff       string
	patchSHA256       string
	linesAdded        int
	linesDeleted      int
	replacements      int
}

func prepareLocalFileEdit(ctx context.Context, request EditFileRequest, beforeOpen func()) (preparedFileEdit, *EditFileResult) {
	filePath := request.resolvedPath()
	if filePath == "" {
		failure := editFailure(filePath, "invalid_edit_path", "file path is required")
		return preparedFileEdit{}, &failure
	}
	operations, failure := normalizedEditOperations(request)
	if failure != nil {
		result := editFailure(filePath, failure.Code, failure.Message)
		return preparedFileEdit{}, &result
	}
	if err := ctx.Err(); err != nil {
		failure := editFailure(filePath, "edit_canceled", err.Error())
		return preparedFileEdit{}, &failure
	}
	if err := ensureGuardedMutationPath(filePath, request.guardedRoot); err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			failure := editFailure(filePath, fileErr.Code, fileErr.Message)
			return preparedFileEdit{}, &failure
		}
		failure := editFailure(filePath, "workspace_path_changed", err.Error())
		return preparedFileEdit{}, &failure
	}

	file, err := openLocalFileForEdit(request, beforeOpen)
	if err != nil {
		var fileErr *FileReadError
		if errors.As(err, &fileErr) {
			failure := editFailure(filePath, fileErr.Code, fileErr.Message)
			return preparedFileEdit{}, &failure
		}
		failure := editFailure(filePath, "file_not_found", err.Error())
		return preparedFileEdit{}, &failure
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		failure := editFailure(filePath, "edit_read_failed", err.Error())
		return preparedFileEdit{}, &failure
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		failure := editFailure(filePath, "unsupported_file_type", "edit_file only supports regular files")
		return preparedFileEdit{}, &failure
	}
	if info.Size() > maxEditableFileBytes {
		_ = file.Close()
		failure := editFailure(filePath, "file_too_large", fmt.Sprintf("edit_file supports files up to %d bytes", maxEditableFileBytes))
		return preparedFileEdit{}, &failure
	}
	revision := fileRevision(info)
	if request.ExpectedRevision != "" && request.ExpectedRevision != revision {
		_ = file.Close()
		failure := editFailure(filePath, "stale_file_revision", "file changed since it was read")
		return preparedFileEdit{}, &failure
	}
	binary, err := openedFileRequiresBinaryRouting(ctx, file, filePath, info.Size())
	if err != nil {
		_ = file.Close()
		failure := editFailure(filePath, "edit_read_failed", err.Error())
		return preparedFileEdit{}, &failure
	}
	if binary {
		_ = file.Close()
		failure := editFailure(filePath, "unsupported_binary_edit", "edit_file only supports UTF-8 text; use a format-specific tool to create a new binary artifact")
		return preparedFileEdit{}, &failure
	}
	contentBytes, err := io.ReadAll(file)
	if err != nil {
		_ = file.Close()
		failure := editFailure(filePath, "edit_read_failed", err.Error())
		return preparedFileEdit{}, &failure
	}
	if err := ensureFileRevision(file, filePath, revision); err != nil {
		_ = file.Close()
		failure := editFailure(filePath, "stale_file_revision", err.Error())
		return preparedFileEdit{}, &failure
	}
	actualContentSHA256 := contentSHA256(contentBytes)
	if expected := strings.ToLower(strings.TrimSpace(request.ExpectedContentSHA256)); expected != "" {
		if actualContentSHA256 != expected {
			_ = file.Close()
			failure := editFailure(filePath, "stale_file_content", "file content changed since it was read")
			return preparedFileEdit{}, &failure
		}
	}
	_ = file.Close()
	content := string(contentBytes)

	newContent, replacements, failure := applyEditOperations(content, operations, request.ExpectedMatchCount, len(request.Edits) == 0)
	if failure != nil {
		result := editFailure(filePath, failure.Code, failure.Message)
		return preparedFileEdit{}, &result
	}
	patch := createPatch(filePath, content, newContent)
	diffText := fmt.Sprintf("diff --git a%s b%s\n%s", filePath, filePath, patch)
	linesAdded, linesDeleted := countPatchLineChanges(patch)
	return preparedFileEdit{
		path: filePath, baseRevision: revision, baseContentSHA256: actualContentSHA256,
		newContent: newContent, unifiedDiff: diffText, patchSHA256: contentSHA256([]byte(diffText)),
		linesAdded: linesAdded, linesDeleted: linesDeleted, replacements: replacements,
	}, nil
}

type editOperationFailure struct {
	Code    string
	Message string
}

type editReplacement struct {
	start   int
	end     int
	content string
	index   int
}

func normalizedEditOperations(request EditFileRequest) ([]EditOperation, *editOperationFailure) {
	if len(request.Edits) > 0 {
		if request.OldString != "" || request.NewString != "" || request.ReplaceAll || request.ExpectedMatchCount != nil {
			return nil, &editOperationFailure{Code: "invalid_edit_arguments", Message: "edits cannot be combined with old_string, new_string, replace_all, or expected_match_count"}
		}
		operations := append([]EditOperation(nil), request.Edits...)
		for index, operation := range operations {
			if failure := validateEditOperation(operation, index); failure != nil {
				return nil, failure
			}
		}
		return operations, nil
	}
	operation := EditOperation{OldString: request.OldString, NewString: request.NewString, ReplaceAll: request.ReplaceAll}
	if failure := validateEditOperation(operation, -1); failure != nil {
		return nil, failure
	}
	return []EditOperation{operation}, nil
}

func validateEditOperation(operation EditOperation, index int) *editOperationFailure {
	label := "edit"
	if index >= 0 {
		label = fmt.Sprintf("edits[%d]", index)
	}
	if operation.OldString == "" {
		return &editOperationFailure{Code: "invalid_edit_match", Message: label + ".old_string is required"}
	}
	if operation.OldString == operation.NewString {
		return &editOperationFailure{Code: "invalid_edit_noop", Message: label + ".old_string and new_string must be different"}
	}
	return nil
}

func applyEditOperations(content string, operations []EditOperation, expectedMatchCount *int, legacySingleEdit bool) (string, int, *editOperationFailure) {
	replacements := make([]editReplacement, 0, len(operations))
	for operationIndex, operation := range operations {
		search := operation.OldString
		replacement := operation.NewString
		if !strings.Contains(content, search) && strings.Contains(content, "\r\n") {
			crlfSearch := toCRLF(search)
			if strings.Contains(content, crlfSearch) {
				search = crlfSearch
				replacement = toCRLF(replacement)
			}
		}
		positions := editMatchPositions(content, search)
		if len(positions) == 0 {
			if legacySingleEdit {
				return "", 0, &editOperationFailure{Code: "match_not_found", Message: "The specified old_string was not found in the file"}
			}
			return "", 0, &editOperationFailure{Code: "match_not_found", Message: fmt.Sprintf("edits[%d].old_string was not found in the file", operationIndex)}
		}
		expectedMatches := 1
		validateMatchCount := !operation.ReplaceAll
		if len(operations) == 1 && expectedMatchCount != nil {
			expectedMatches = *expectedMatchCount
			validateMatchCount = true
		}
		if expectedMatches < 1 {
			return "", 0, &editOperationFailure{Code: "invalid_edit_match", Message: "expected_match_count must be at least 1"}
		}
		if validateMatchCount && len(positions) != expectedMatches {
			if legacySingleEdit {
				return "", 0, &editOperationFailure{Code: "match_not_unique", Message: fmt.Sprintf("old_string matched %d times; expected %d", len(positions), expectedMatches)}
			}
			return "", 0, &editOperationFailure{Code: "match_not_unique", Message: fmt.Sprintf("edits[%d].old_string matched %d times; expected %d", operationIndex, len(positions), expectedMatches)}
		}
		if !operation.ReplaceAll {
			positions = positions[:1]
		}
		for _, position := range positions {
			replacements = append(replacements, editReplacement{
				start: position, end: position + len(search), content: replacement, index: operationIndex,
			})
		}
	}

	slices.SortFunc(replacements, func(left, right editReplacement) int {
		if left.start != right.start {
			return left.start - right.start
		}
		return left.end - right.end
	})
	for index := 1; index < len(replacements); index++ {
		if replacements[index].start < replacements[index-1].end {
			return "", 0, &editOperationFailure{
				Code:    "overlapping_edits",
				Message: fmt.Sprintf("edits[%d] overlaps edits[%d]; merge overlapping replacements", replacements[index].index, replacements[index-1].index),
			}
		}
	}

	updated := content
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		updated = updated[:replacement.start] + replacement.content + updated[replacement.end:]
	}
	return updated, len(replacements), nil
}

func editMatchPositions(content, search string) []int {
	positions := make([]int, 0, 1)
	for offset := 0; offset <= len(content)-len(search); {
		index := strings.Index(content[offset:], search)
		if index < 0 {
			break
		}
		position := offset + index
		positions = append(positions, position)
		offset = position + len(search)
	}
	return positions
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
