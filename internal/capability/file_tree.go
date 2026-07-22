package capability

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	defaultFindFilesMaxResults   = 200
	hardFindFilesMaxResults      = 1000
	hardFindFilesScanned         = 100000
	defaultSearchFilesMaxFiles   = 1000
	hardSearchFilesMaxFiles      = 5000
	defaultSearchFilesMaxResults = 100
	hardSearchFilesMaxResults    = 500
	hardSearchFilesScannedBytes  = 64 << 20
	maxSearchFilesRegexLineBytes = 1 << 20
)

type fileCandidate struct {
	absolute    string
	relative    string
	info        fs.FileInfo
	guardedRoot string
}

func (provider LocalSystemProvider) FindFiles(ctx context.Context, request FindFilesRequest) (FindFilesResult, error) {
	return findLocalFiles(ctx, request)
}

func findLocalFiles(ctx context.Context, request FindFilesRequest) (FindFilesResult, error) {
	return findLocalFilesWithDiscoveryHook(ctx, request, nil)
}

func findLocalFilesWithDiscoveryHook(ctx context.Context, request FindFilesRequest, afterRootOpen func()) (FindFilesResult, error) {
	ctx, cancel := contextWithRequestDeadline(ctx, request.Meta.Deadline)
	defer cancel()
	root := strings.TrimSpace(request.Root)
	if root == "" {
		root = "."
	}
	pattern := normalizeGlob(request.Pattern)
	if pattern == "" {
		return FindFilesResult{}, newFileReadError("invalid_glob_pattern", "find_files pattern is required", nil)
	}
	if err := validateGlob(pattern); err != nil {
		return FindFilesResult{}, err
	}
	for _, exclude := range request.Exclude {
		if err := validateGlob(normalizeGlob(exclude)); err != nil {
			return FindFilesResult{}, err
		}
	}
	maxResults := request.MaxResults
	if maxResults == 0 {
		maxResults = defaultFindFilesMaxResults
	}
	if maxResults < 1 || maxResults > hardFindFilesMaxResults {
		return FindFilesResult{}, newFileReadError("search_limit_exceeded", fmt.Sprintf("max_results must be between 1 and %d", hardFindFilesMaxResults), nil)
	}

	candidates, scanned, truncated, err := discoverLocalFiles(
		ctx, root, []string{pattern}, request.Exclude, request.IncludeHidden,
		hardFindFilesScanned, maxResults+1, request.AfterPath, request.guardedRoot, afterRootOpen,
	)
	if err != nil {
		return FindFilesResult{}, err
	}
	result := FindFilesResult{Root: request.Root, Pattern: request.Pattern, Files: []FoundFile{}, Scanned: scanned, Truncated: truncated}
	if result.Root == "" {
		result.Root = "."
	}
	if len(candidates) > maxResults {
		candidates = candidates[:maxResults]
		result.Truncated = true
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return FindFilesResult{}, err
		}
		file, info, err := openDiscoveredCandidate(candidate)
		if err != nil {
			return FindFilesResult{}, err
		}
		binary, binaryErr := openedFileRequiresBinaryRouting(ctx, file, candidate.absolute, info.Size())
		classification := classifyOpenedFile(file, candidate.absolute, info.Size(), binary)
		_ = file.Close()
		if binaryErr != nil {
			return FindFilesResult{}, binaryErr
		}
		result.Files = append(result.Files, FoundFile{
			Path: candidate.relative, SizeBytes: info.Size(), FileRevision: fileRevision(info),
			Kind: classification.Kind, ContentType: classification.ContentType,
		})
	}
	if result.Truncated && len(result.Files) > 0 {
		result.NextPath = result.Files[len(result.Files)-1].Path
	}
	return result, nil
}

func discoverLocalFiles(
	ctx context.Context,
	root string,
	patterns, excludes []string,
	includeHidden bool,
	maxScanned, maxMatches int,
	afterPath, guardedRoot string,
	afterRootOpen func(),
) ([]fileCandidate, int, bool, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, 0, false, err
	}
	normalizedPatterns := make([]string, 0, len(patterns))
	for _, value := range patterns {
		value = normalizeGlob(value)
		if value != "" {
			if err := validateGlob(value); err != nil {
				return nil, 0, false, err
			}
			normalizedPatterns = append(normalizedPatterns, value)
		}
	}
	if len(normalizedPatterns) == 0 {
		return nil, 0, false, newFileReadError("invalid_glob_pattern", "at least one path pattern is required", nil)
	}
	normalizedExcludes := make([]string, 0, len(excludes))
	for _, value := range excludes {
		value = normalizeGlob(value)
		if value != "" {
			normalizedExcludes = append(normalizedExcludes, value)
		}
	}
	afterPath = normalizeGlob(afterPath)
	if strings.TrimSpace(guardedRoot) != "" {
		return discoverLocalFilesGuarded(
			ctx, guardedRoot, absoluteRoot, normalizedPatterns, normalizedExcludes,
			includeHidden, maxScanned, maxMatches, afterPath, afterRootOpen,
		)
	}
	return discoverLocalFilesPath(ctx, absoluteRoot, root, normalizedPatterns, normalizedExcludes, includeHidden, maxScanned, maxMatches, afterPath, afterRootOpen)
}

func discoverLocalFilesPath(
	ctx context.Context,
	absoluteRoot, displayRoot string,
	patterns, excludes []string,
	includeHidden bool,
	maxScanned, maxMatches int,
	afterPath string,
	afterRootOpen func(),
) ([]fileCandidate, int, bool, error) {
	rootInfo, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, 0, false, err
	}
	if !rootInfo.IsDir() {
		return nil, 0, false, newFileReadError("unsupported_file_type", "file discovery root must be a directory", map[string]any{"root": displayRoot})
	}
	if afterRootOpen != nil {
		afterRootOpen()
	}
	candidates := make([]fileCandidate, 0, minInt(maxMatches, defaultFindFilesMaxResults))
	scanned := 0
	truncated := false
	stop := errors.New("file discovery complete")
	err = filepath.WalkDir(absoluteRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == absoluteRoot {
			return nil
		}
		relative, err := filepath.Rel(absoluteRoot, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if (!includeHidden && pathHasHiddenComponent(relative)) || matchesAnyGlob(excludes, relative) {
				return filepath.SkipDir
			}
			return nil
		}
		scanned++
		if scanned > maxScanned {
			truncated = true
			return stop
		}
		if !includeHidden && pathHasHiddenComponent(relative) || matchesAnyGlob(excludes, relative) || !matchesAnyGlob(patterns, relative) || relative <= afterPath {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		candidates = append(candidates, fileCandidate{absolute: name, relative: relative, info: info})
		if len(candidates) >= maxMatches {
			truncated = true
			return stop
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return nil, scanned, truncated, err
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].relative < candidates[right].relative })
	return candidates, scanned, truncated, nil
}

func openDiscoveredCandidate(candidate fileCandidate) (*os.File, fs.FileInfo, error) {
	file, err := openLocalFileForRead(ReadFileRequest{Path: candidate.absolute, guardedRoot: candidate.guardedRoot}, nil)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, newFileReadError("unsupported_file_type", "discovered path is no longer a regular file", map[string]any{"path": candidate.relative})
	}
	expected := fileRevision(candidate.info)
	if actual := fileRevision(info); actual != expected {
		_ = file.Close()
		return nil, nil, staleFileRevisionError(candidate.relative, expected, actual)
	}
	return file, info, nil
}

func normalizeGlob(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "./")
	return strings.TrimPrefix(value, "/")
}

func validateGlob(pattern string) error {
	if pattern == "" {
		return nil
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, "x"); err != nil {
			return newFileReadError("invalid_glob_pattern", err.Error(), map[string]any{"pattern": pattern})
		}
	}
	return nil
}

func matchesAnyGlob(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchGlob(pattern, value) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, value string) bool {
	patternParts := strings.Split(normalizeGlob(pattern), "/")
	valueParts := strings.Split(normalizeGlob(value), "/")
	var match func(int, int) bool
	match = func(patternIndex, valueIndex int) bool {
		if patternIndex == len(patternParts) {
			return valueIndex == len(valueParts)
		}
		if patternParts[patternIndex] == "**" {
			return match(patternIndex+1, valueIndex) || valueIndex < len(valueParts) && match(patternIndex, valueIndex+1)
		}
		if valueIndex >= len(valueParts) {
			return false
		}
		matched, err := path.Match(patternParts[patternIndex], valueParts[valueIndex])
		return err == nil && matched && match(patternIndex+1, valueIndex+1)
	}
	return match(0, 0)
}

func pathHasHiddenComponent(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			return true
		}
	}
	return false
}

func (provider LocalSystemProvider) SearchFiles(ctx context.Context, request SearchFilesRequest) (SearchFilesResult, error) {
	return searchLocalFiles(ctx, request)
}

func searchLocalFiles(ctx context.Context, request SearchFilesRequest) (SearchFilesResult, error) {
	return searchLocalFilesWithDiscoveryHook(ctx, request, nil)
}

func searchLocalFilesWithDiscoveryHook(ctx context.Context, request SearchFilesRequest, afterRootOpen func()) (SearchFilesResult, error) {
	ctx, cancel := contextWithRequestDeadline(ctx, request.Meta.Deadline)
	defer cancel()
	mode := NormalizeSearchMode(request.Mode)
	if mode != "literal" && mode != "regex" {
		return SearchFilesResult{}, newFileReadError("invalid_search_mode", "search mode must be literal or regex", map[string]any{"mode": request.Mode})
	}
	if request.Query == "" || !utf8.ValidString(request.Query) || strings.ContainsAny(request.Query, "\r\n") {
		return SearchFilesResult{}, newFileReadError("invalid_search_query", "search_files query must be non-empty single-line UTF-8 text", nil)
	}
	if len(request.Query) > maxSearchFileQueryBytes {
		return SearchFilesResult{}, newFileReadError("search_limit_exceeded", fmt.Sprintf("query exceeds %d bytes", maxSearchFileQueryBytes), nil)
	}
	maxFiles := request.MaxFiles
	if maxFiles == 0 {
		maxFiles = defaultSearchFilesMaxFiles
	}
	if maxFiles < 1 || maxFiles > hardSearchFilesMaxFiles {
		return SearchFilesResult{}, newFileReadError("search_limit_exceeded", fmt.Sprintf("max_files must be between 1 and %d", hardSearchFilesMaxFiles), nil)
	}
	maxResults := request.MaxResults
	if maxResults == 0 {
		maxResults = defaultSearchFilesMaxResults
	}
	if maxResults < 1 || maxResults > hardSearchFilesMaxResults {
		return SearchFilesResult{}, newFileReadError("search_limit_exceeded", fmt.Sprintf("max_results must be between 1 and %d", hardSearchFilesMaxResults), nil)
	}
	root := request.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	candidates, _, discoveryTruncated, err := discoverLocalFiles(
		ctx, root, request.Paths, request.Exclude, request.IncludeHidden,
		hardFindFilesScanned, maxFiles+1, "", request.guardedRoot, afterRootOpen,
	)
	if err != nil {
		return SearchFilesResult{}, err
	}
	result := SearchFilesResult{Query: request.Query, Mode: mode, Matches: []SearchFilesMatch{}, Truncated: discoveryTruncated}
	if len(candidates) > maxFiles {
		candidates = candidates[:maxFiles]
		result.Truncated = true
	}
	var expression *regexp.Regexp
	if mode == "regex" || !request.CaseSensitiveValue() {
		pattern := request.Query
		if mode == "literal" {
			pattern = regexp.QuoteMeta(pattern)
		}
		if !request.CaseSensitiveValue() {
			pattern = "(?i)" + pattern
		}
		expression, err = regexp.Compile(pattern)
		if err != nil {
			return SearchFilesResult{}, newFileReadError("invalid_search_regex", err.Error(), nil)
		}
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return SearchFilesResult{}, err
		}
		if result.ScannedBytes+candidate.info.Size() > hardSearchFilesScannedBytes {
			result.Truncated = true
			break
		}
		remaining := maxResults - len(result.Matches)
		matches, binary, candidateTruncated, err := searchCandidate(ctx, candidate, request.Query, mode, request.CaseSensitiveValue(), expression, remaining)
		if err != nil {
			return SearchFilesResult{}, err
		}
		result.ScannedFiles++
		result.ScannedBytes += candidate.info.Size()
		if binary {
			result.SkippedBinaryFiles++
			continue
		}
		result.Matches = append(result.Matches, matches...)
		if candidateTruncated {
			result.Truncated = true
			break
		}
		if len(result.Matches) >= maxResults {
			result.Truncated = true
			break
		}
	}
	return result, nil
}

func searchCandidate(ctx context.Context, candidate fileCandidate, query, mode string, caseSensitive bool, expression *regexp.Regexp, maxResults int) ([]SearchFilesMatch, bool, bool, error) {
	if mode == "literal" && caseSensitive {
		result, err := searchLocalFile(ctx, SearchFileRequest{
			Path: candidate.absolute, Query: query, MaxResults: minInt(maxResults, hardSearchFileMaxResults),
			FileRevision: fileRevision(candidate.info), guardedRoot: candidate.guardedRoot,
		})
		if err != nil {
			return nil, false, false, err
		}
		matches := make([]SearchFilesMatch, 0, len(result.Matches))
		for _, match := range result.Matches {
			matches = append(matches, SearchFilesMatch{
				Path: candidate.relative, LineNumber: match.LineNumber, OffsetBytes: match.OffsetBytes,
				Line: match.Line, LineTruncated: match.LineTruncated, FileRevision: result.FileRevision,
			})
		}
		return matches, result.Binary, result.Truncated, nil
	}

	file, info, err := openDiscoveredCandidate(candidate)
	if err != nil {
		return nil, false, false, err
	}
	defer file.Close()
	binary, err := openedFileRequiresBinaryRouting(ctx, file, candidate.absolute, info.Size())
	if err != nil || binary {
		return nil, binary, false, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, false, false, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 32<<10), maxSearchFilesRegexLineBytes)
	matches := make([]SearchFilesMatch, 0)
	revision := fileRevision(info)
	lineNumber := 0
	offset := int64(0)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, false, false, err
		}
		lineNumber++
		line := scanner.Text()
		matched := false
		matchOffset := -1
		if mode == "regex" || !caseSensitive {
			location := expression.FindStringIndex(line)
			if location != nil {
				matched, matchOffset = true, location[0]
			}
		} else {
			matchOffset = strings.Index(line, query)
			matched = matchOffset >= 0
		}
		if matched {
			preview := line
			truncated := false
			if len(preview) > maxSearchFileLineBytes {
				preview = preview[:maxSearchFileLineBytes]
				truncated = true
			}
			matches = append(matches, SearchFilesMatch{
				Path: candidate.relative, LineNumber: lineNumber, OffsetBytes: offset + int64(matchOffset),
				Line: preview, LineTruncated: truncated, FileRevision: revision,
			})
			if len(matches) >= maxResults {
				if err := ensureFileRevision(file, candidate.absolute, revision); err != nil {
					return nil, false, false, err
				}
				return matches, false, true, nil
			}
		}
		offset += int64(len(scanner.Bytes())) + 1
	}
	if err := scanner.Err(); err != nil {
		return nil, false, false, newFileReadError("search_line_too_long", fmt.Sprintf("search_files cannot evaluate a regex or case-insensitive match on a line larger than %d bytes", maxSearchFilesRegexLineBytes), map[string]any{"path": candidate.relative})
	}
	if err := ensureFileRevision(file, candidate.absolute, revision); err != nil {
		return nil, false, false, err
	}
	return matches, false, false, nil
}
