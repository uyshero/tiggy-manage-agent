package capability

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	defaultSearchFileMaxResults = 50
	hardSearchFileMaxResults    = 100
	maxSearchFileQueryBytes     = 1024
	maxSearchFileLineBytes      = 4096
)

func (provider LocalSystemProvider) SearchFile(ctx context.Context, request SearchFileRequest) (SearchFileResult, error) {
	return searchLocalFile(ctx, request)
}

func searchLocalFile(ctx context.Context, request SearchFileRequest) (SearchFileResult, error) {
	ctx, cancel := contextWithRequestDeadline(ctx, request.Meta.Deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return SearchFileResult{}, err
	}
	if request.Query == "" {
		return SearchFileResult{}, newFileReadError("invalid_search_query", "search_file query is required", nil)
	}
	if !utf8.ValidString(request.Query) || strings.ContainsAny(request.Query, "\r\n") {
		return SearchFileResult{}, newFileReadError("invalid_search_query", "search_file query must be single-line UTF-8 text", nil)
	}
	if len(request.Query) > maxSearchFileQueryBytes {
		return SearchFileResult{}, newFileReadError(
			"search_limit_exceeded",
			fmt.Sprintf("search_file query exceeds %d bytes", maxSearchFileQueryBytes),
			map[string]any{"query_bytes": len(request.Query), "hard_max_query_bytes": maxSearchFileQueryBytes},
		)
	}
	maxResults := request.MaxResults
	if maxResults == 0 {
		maxResults = defaultSearchFileMaxResults
	}
	if maxResults < 1 || maxResults > hardSearchFileMaxResults {
		return SearchFileResult{}, newFileReadError(
			"search_limit_exceeded",
			fmt.Sprintf("max_results must be between 1 and %d", hardSearchFileMaxResults),
			map[string]any{"max_results": maxResults, "hard_max_results": hardSearchFileMaxResults},
		)
	}
	if strings.EqualFold(filepath.Ext(request.Path), ".docx") {
		return SearchFileResult{}, newFileReadError(
			"unsupported_file_search",
			"search_file does not search inside DOCX packages; use read_file text extraction or a format-aware parser",
			map[string]any{"path": request.Path, "format": "docx"},
		)
	}

	file, err := os.Open(request.Path)
	if err != nil {
		return SearchFileResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return SearchFileResult{}, err
	}
	if !info.Mode().IsRegular() {
		return SearchFileResult{}, newFileReadError("unsupported_file_type", "search_file only supports regular files", map[string]any{"path": request.Path})
	}
	revision := fileRevision(info)
	if request.FileRevision != "" && request.FileRevision != revision {
		return SearchFileResult{}, staleFileRevisionError(request.Path, request.FileRevision, revision)
	}
	binary, err := openedFileRequiresBinaryRouting(ctx, file, request.Path, info.Size())
	if err != nil {
		return SearchFileResult{}, err
	}
	result := SearchFileResult{
		Path: request.Path, SizeBytes: info.Size(), FileRevision: revision,
		Query: request.Query, Matches: []SearchFileMatch{}, Binary: binary,
	}
	if binary {
		if err := ensureFileRevision(file, request.Path, revision); err != nil {
			return SearchFileResult{}, err
		}
		return result, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return SearchFileResult{}, err
	}

	reader := bufio.NewReaderSize(file, readFileBufferBytes)
	query := []byte(request.Query)
	lineNumber := 1
	readOffset := int64(0)
	matchOffset := int64(-1)
	preview := make([]byte, 0, maxSearchFileLineBytes)
	lineTruncated := false
	carry := make([]byte, 0, len(query)-1)
	lineHasBytes := false

	finalizeLine := func() error {
		if matchOffset >= 0 {
			line := preview
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			safe, valid := trimToUTF8Boundary(line)
			if !valid {
				return newFileReadError("invalid_utf8", "file contains invalid UTF-8 text", map[string]any{"path": request.Path, "line_number": lineNumber})
			}
			result.Matches = append(result.Matches, SearchFileMatch{
				LineNumber: lineNumber, OffsetBytes: matchOffset, Line: string(safe), LineTruncated: lineTruncated,
			})
		}
		lineNumber++
		matchOffset = -1
		preview = preview[:0]
		lineTruncated = false
		carry = carry[:0]
		lineHasBytes = false
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return SearchFileResult{}, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		fragmentStart := readOffset
		readOffset += int64(len(fragment))
		lineHasBytes = lineHasBytes || len(fragment) > 0

		searchPart := fragment
		if len(searchPart) > 0 && searchPart[len(searchPart)-1] == '\n' {
			searchPart = searchPart[:len(searchPart)-1]
		}
		combined := make([]byte, 0, len(carry)+len(searchPart))
		combined = append(combined, carry...)
		combined = append(combined, searchPart...)
		if matchOffset < 0 {
			if index := bytes.Index(combined, query); index >= 0 {
				matchOffset = fragmentStart - int64(len(carry)) + int64(index)
			}
		}
		if len(query) > 1 {
			keep := len(query) - 1
			if keep > len(combined) {
				keep = len(combined)
			}
			carry = append(carry[:0], combined[len(combined)-keep:]...)
		}

		previewPart := searchPart
		remaining := maxSearchFileLineBytes - len(preview)
		if len(previewPart) > remaining {
			previewPart = previewPart[:remaining]
			lineTruncated = true
		}
		preview = append(preview, previewPart...)
		if len(preview) >= maxSearchFileLineBytes && readErr == bufio.ErrBufferFull {
			lineTruncated = true
		}

		switch {
		case readErr == nil:
			if err := finalizeLine(); err != nil {
				return SearchFileResult{}, err
			}
			if len(result.Matches) >= maxResults {
				result.Truncated = readOffset < info.Size()
				if err := ensureFileRevision(file, request.Path, revision); err != nil {
					return SearchFileResult{}, err
				}
				return result, nil
			}
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if lineHasBytes {
				if err := finalizeLine(); err != nil {
					return SearchFileResult{}, err
				}
			}
			if err := ensureFileRevision(file, request.Path, revision); err != nil {
				return SearchFileResult{}, err
			}
			return result, nil
		default:
			return SearchFileResult{}, readErr
		}
	}
}
