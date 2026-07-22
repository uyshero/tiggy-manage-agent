package capability

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultReadFileDefaultMaxBytes  = 8 << 10
	DefaultReadFileHardMaxBytes     = 64 << 10
	DefaultReadFileSmallFileBytes   = 8 << 10
	DefaultReadFileMaxLines         = 400
	DefaultReadFilePageLines        = 200
	DefaultReadFileDocumentMaxBytes = 64 << 20

	MinReadFileConfiguredBytes = 256
	MaxReadFileConfiguredBytes = 1 << 20
	MinReadFileConfiguredLines = 1
	MaxReadFileConfiguredLines = 5000
	MinReadFileRequestBytes    = utf8.UTFMax

	readFileBufferBytes = 32 << 10
	readFileSampleBytes = 8 << 10
)

type ReadFileLimits struct {
	DefaultMaxBytes int `json:"default_max_bytes"`
	HardMaxBytes    int `json:"hard_max_bytes"`
	SmallFileBytes  int `json:"small_file_bytes"`
	MaxLines        int `json:"max_lines"`
}

func DefaultReadFileLimits() ReadFileLimits {
	return ReadFileLimits{
		DefaultMaxBytes: DefaultReadFileDefaultMaxBytes,
		HardMaxBytes:    DefaultReadFileHardMaxBytes,
		SmallFileBytes:  DefaultReadFileSmallFileBytes,
		MaxLines:        DefaultReadFileMaxLines,
	}
}

func (limits ReadFileLimits) Effective() ReadFileLimits {
	defaults := DefaultReadFileLimits()
	if limits.DefaultMaxBytes <= 0 {
		limits.DefaultMaxBytes = defaults.DefaultMaxBytes
	}
	if limits.HardMaxBytes <= 0 {
		limits.HardMaxBytes = defaults.HardMaxBytes
	}
	if limits.SmallFileBytes <= 0 {
		limits.SmallFileBytes = defaults.SmallFileBytes
	}
	if limits.MaxLines <= 0 {
		limits.MaxLines = defaults.MaxLines
	}
	return limits
}

func (limits ReadFileLimits) Validate() error {
	limits = limits.Effective()
	if limits.HardMaxBytes < MinReadFileConfiguredBytes || limits.HardMaxBytes > MaxReadFileConfiguredBytes {
		return fmt.Errorf("read_file_hard_max_bytes must be between %d and %d", MinReadFileConfiguredBytes, MaxReadFileConfiguredBytes)
	}
	if limits.DefaultMaxBytes < MinReadFileConfiguredBytes || limits.DefaultMaxBytes > limits.HardMaxBytes {
		return fmt.Errorf("read_file_default_max_bytes must be between %d and read_file_hard_max_bytes", MinReadFileConfiguredBytes)
	}
	if limits.SmallFileBytes < MinReadFileConfiguredBytes || limits.SmallFileBytes > limits.DefaultMaxBytes {
		return fmt.Errorf("read_file_small_file_bytes must be between %d and read_file_default_max_bytes", MinReadFileConfiguredBytes)
	}
	if limits.MaxLines < MinReadFileConfiguredLines || limits.MaxLines > MaxReadFileConfiguredLines {
		return fmt.Errorf("read_file_max_lines must be between %d and %d", MinReadFileConfiguredLines, MaxReadFileConfiguredLines)
	}
	return nil
}

type FileReadError struct {
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (e *FileReadError) Error() string {
	if e == nil {
		return "file read failed"
	}
	return e.Message
}

func newFileReadError(code string, message string, metadata map[string]any) error {
	return &FileReadError{Code: code, Message: message, Metadata: metadata}
}

func readLocalFile(ctx context.Context, request ReadFileRequest, limits ReadFileLimits) (FileResult, error) {
	return readLocalFileWithOpenHook(ctx, request, limits, nil)
}

func readLocalFileWithOpenHook(ctx context.Context, request ReadFileRequest, limits ReadFileLimits, beforeOpen func()) (FileResult, error) {
	ctx, cancel := contextWithRequestDeadline(ctx, request.Meta.Deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return FileResult{}, err
	}
	limits = limits.Effective()
	if err := limits.Validate(); err != nil {
		return FileResult{}, newFileReadError("invalid_read_file_config", err.Error(), nil)
	}
	mode, err := validateReadFileRequest(request, limits)
	if err != nil {
		return FileResult{}, err
	}

	file, err := openLocalFileForRead(request, beforeOpen)
	if err != nil {
		return FileResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return FileResult{}, err
	}
	if !info.Mode().IsRegular() {
		return FileResult{}, newFileReadError("unsupported_file_type", "read_file only supports regular files", map[string]any{"path": request.Path})
	}
	revision := fileRevision(info)
	if request.FileRevision != "" && request.FileRevision != revision {
		return FileResult{}, staleFileRevisionError(request.Path, request.FileRevision, revision)
	}

	if strings.EqualFold(filepath.Ext(request.Path), ".docx") {
		if mode != "auto" {
			return FileResult{}, newFileReadError(
				"unsupported_read_pagination",
				"DOCX text extraction does not support byte or line pagination; call read_file with path only",
				map[string]any{"path": request.Path, "format": "docx"},
			)
		}
		if info.Size() > DefaultReadFileDocumentMaxBytes {
			return FileResult{}, newFileReadError(
				"file_too_large",
				fmt.Sprintf("DOCX package exceeds the %d byte extraction limit", DefaultReadFileDocumentMaxBytes),
				map[string]any{"path": request.Path, "size_bytes": info.Size(), "max_bytes": DefaultReadFileDocumentMaxBytes},
			)
		}
		content, readErr := readOpenedFileBounded(ctx, file, info.Size())
		if readErr != nil {
			return FileResult{}, readErr
		}
		if err := ensureFileRevision(file, request.Path, revision); err != nil {
			return FileResult{}, err
		}
		return FileResult{
			Path: request.Path, Content: content, SizeBytes: info.Size(), ReturnedBytes: len(content),
			NextOffsetBytes: info.Size(), EOF: true, FileRevision: revision, Mode: "document",
			Kind: "document", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			Encoding: "utf-8", SuggestedCapability: "document_skill",
		}, nil
	}

	binary, err := openedFileRequiresBinaryRouting(ctx, file, request.Path, info.Size())
	if err != nil {
		return FileResult{}, err
	}
	if binary {
		if err := ensureFileRevision(file, request.Path, revision); err != nil {
			return FileResult{}, err
		}
		result := binaryFileResult(request.Path, info.Size(), revision)
		applyFileClassification(&result, classifyOpenedFile(file, request.Path, info.Size(), true))
		return result, nil
	}

	var result FileResult
	switch mode {
	case "line":
		result, err = readFileLinePage(ctx, file, request, info.Size(), revision, limits)
	default:
		result, err = readFileBytePage(ctx, file, request, info.Size(), revision, limits, mode == "auto")
	}
	if err != nil {
		return FileResult{}, err
	}
	if err := ensureFileRevision(file, request.Path, revision); err != nil {
		return FileResult{}, err
	}
	applyFileClassification(&result, classifyOpenedFile(file, request.Path, info.Size(), false))
	if result.Mode == "byte" && result.OffsetBytes == 0 && result.EOF && int64(len(result.Content)) == info.Size() {
		result.ContentSHA256 = contentSHA256(result.Content)
	}
	return result, nil
}

func contextWithRequestDeadline(ctx context.Context, deadline *time.Time) (context.Context, context.CancelFunc) {
	if deadline == nil {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, *deadline)
}

func validateReadFileRequest(request ReadFileRequest, limits ReadFileLimits) (string, error) {
	byteMode := request.OffsetBytes != nil || request.MaxBytes != nil
	lineMode := request.StartLine != nil || request.MaxLines != nil
	if byteMode && lineMode {
		return "", newFileReadError(
			"invalid_read_range",
			"byte mode (offset_bytes/max_bytes) and line mode (start_line/max_lines) are mutually exclusive",
			map[string]any{"path": request.Path},
		)
	}
	if request.OffsetBytes != nil && *request.OffsetBytes < 0 {
		return "", newFileReadError("invalid_read_range", "offset_bytes must be non-negative", map[string]any{"offset_bytes": *request.OffsetBytes})
	}
	if request.MaxBytes != nil {
		if *request.MaxBytes < MinReadFileRequestBytes {
			return "", newFileReadError("invalid_read_range", fmt.Sprintf("max_bytes must be at least %d", MinReadFileRequestBytes), map[string]any{"max_bytes": *request.MaxBytes})
		}
		if *request.MaxBytes > limits.HardMaxBytes {
			return "", newFileReadError(
				"read_limit_exceeded",
				fmt.Sprintf("max_bytes exceeds the server hard limit of %d", limits.HardMaxBytes),
				map[string]any{"max_bytes": *request.MaxBytes, "hard_max_bytes": limits.HardMaxBytes},
			)
		}
	}
	if request.StartLine != nil && *request.StartLine < 1 {
		return "", newFileReadError("invalid_read_range", "start_line must be at least 1", map[string]any{"start_line": *request.StartLine})
	}
	if request.MaxLines != nil {
		if *request.MaxLines < 1 {
			return "", newFileReadError("invalid_read_range", "max_lines must be at least 1", map[string]any{"max_lines": *request.MaxLines})
		}
		if *request.MaxLines > limits.MaxLines {
			return "", newFileReadError(
				"read_limit_exceeded",
				fmt.Sprintf("max_lines exceeds the server hard limit of %d", limits.MaxLines),
				map[string]any{"max_lines": *request.MaxLines, "hard_max_lines": limits.MaxLines},
			)
		}
	}
	if lineMode {
		return "line", nil
	}
	if byteMode {
		return "byte", nil
	}
	return "auto", nil
}

func readFileBytePage(ctx context.Context, file *os.File, request ReadFileRequest, size int64, revision string, limits ReadFileLimits, automatic bool) (FileResult, error) {
	requestedOffset := int64(0)
	if request.OffsetBytes != nil {
		requestedOffset = *request.OffsetBytes
	}
	if requestedOffset > size {
		return FileResult{}, newFileReadError(
			"offset_out_of_range",
			fmt.Sprintf("offset_bytes %d exceeds file size %d", requestedOffset, size),
			map[string]any{"path": request.Path, "offset_bytes": requestedOffset, "size_bytes": size, "file_revision": revision},
		)
	}
	actualOffset, err := alignUTF8Offset(ctx, file, requestedOffset, size)
	if err != nil {
		return FileResult{}, err
	}
	maxBytes := requestMaxBytes(request, size, automatic, limits)
	content, binary, err := readUTF8PageAt(ctx, file, actualOffset, maxBytes, size)
	if err != nil {
		return FileResult{}, err
	}
	if binary {
		return binaryFileResult(request.Path, size, revision), nil
	}
	next := actualOffset + int64(len(content))
	result := FileResult{
		Path: request.Path, Content: content, SizeBytes: size, OffsetBytes: actualOffset,
		ReturnedBytes: len(content), NextOffsetBytes: next, EOF: next >= size,
		Truncated: next < size, FileRevision: revision, Mode: "byte",
	}
	if actualOffset != requestedOffset {
		requested := requestedOffset
		result.RequestedOffsetBytes = &requested
	}
	if actualOffset == 0 {
		result.StartLine = 1
		result.EndLine = pageEndLine(1, content)
	}
	return result, nil
}

func requestMaxBytes(request ReadFileRequest, size int64, automatic bool, limits ReadFileLimits) int {
	if request.MaxBytes != nil {
		return *request.MaxBytes
	}
	if automatic && size <= int64(limits.SmallFileBytes) {
		return maxInt(1, int(size))
	}
	return limits.DefaultMaxBytes
}

func readFileLinePage(ctx context.Context, file *os.File, request ReadFileRequest, size int64, revision string, limits ReadFileLimits) (FileResult, error) {
	startLine := 1
	if request.StartLine != nil {
		startLine = *request.StartLine
	}
	maxLines := DefaultReadFilePageLines
	if maxLines > limits.MaxLines {
		maxLines = limits.MaxLines
	}
	if request.MaxLines != nil {
		maxLines = *request.MaxLines
	}
	reader := bufio.NewReaderSize(file, readFileBufferBytes)
	pageOffset := int64(0)
	for line := 1; line < startLine; line++ {
		consumed, err := discardLine(ctx, reader)
		pageOffset += int64(consumed)
		if errors.Is(err, io.EOF) || pageOffset >= size {
			return FileResult{}, newFileReadError(
				"line_out_of_range",
				fmt.Sprintf("start_line %d exceeds the file's available lines", startLine),
				map[string]any{"path": request.Path, "start_line": startLine, "size_bytes": size, "file_revision": revision},
			)
		}
		if err != nil {
			return FileResult{}, err
		}
	}
	if startLine > 1 && pageOffset >= size {
		return FileResult{}, newFileReadError(
			"line_out_of_range",
			fmt.Sprintf("start_line %d exceeds the file's available lines", startLine),
			map[string]any{"path": request.Path, "start_line": startLine, "size_bytes": size, "file_revision": revision},
		)
	}

	page := make([]byte, 0, limits.DefaultMaxBytes)
	lineCount := 0
	lineTruncated := false
	for lineCount < maxLines && len(page) < limits.DefaultMaxBytes {
		if err := ctx.Err(); err != nil {
			return FileResult{}, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		remaining := limits.DefaultMaxBytes - len(page)
		take := len(fragment)
		if take > remaining {
			take = remaining
			lineTruncated = true
		}
		page = append(page, fragment[:take]...)
		if take < len(fragment) {
			break
		}
		switch {
		case readErr == nil:
			lineCount++
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if len(fragment) > 0 {
				lineCount++
			}
			lineCount = maxLines
		default:
			return FileResult{}, readErr
		}
	}
	content, valid := trimToUTF8Boundary(page)
	if !valid || bytes.IndexByte(content, 0) >= 0 {
		return binaryFileResult(request.Path, size, revision), nil
	}
	if len(content) != len(page) {
		lineTruncated = true
	}
	next := pageOffset + int64(len(content))
	if next < size && len(content) > 0 && content[len(content)-1] != '\n' {
		lineTruncated = true
	}
	return FileResult{
		Path: request.Path, Content: content, SizeBytes: size, OffsetBytes: pageOffset,
		ReturnedBytes: len(content), StartLine: startLine, EndLine: pageEndLine(startLine, content),
		NextOffsetBytes: next, EOF: next >= size, Truncated: next < size, FileRevision: revision,
		Mode: "line", LineTruncated: lineTruncated,
	}, nil
}

func discardLine(ctx context.Context, reader *bufio.Reader) (int, error) {
	consumed := 0
	for {
		if err := ctx.Err(); err != nil {
			return consumed, err
		}
		fragment, err := reader.ReadSlice('\n')
		consumed += len(fragment)
		switch {
		case err == nil:
			return consumed, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return consumed, io.EOF
		default:
			return consumed, err
		}
	}
}

func readUTF8PageAt(ctx context.Context, file *os.File, offset int64, maxBytes int, size int64) ([]byte, bool, error) {
	if offset >= size {
		return []byte{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	available := size - offset
	readBytes := int64(maxBytes + utf8.UTFMax - 1)
	if readBytes > available {
		readBytes = available
	}
	buffer := make([]byte, int(readBytes))
	n, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	buffer = buffer[:n]
	limit := maxBytes
	if len(buffer) < limit {
		limit = len(buffer)
	}
	content, valid := trimToUTF8Boundary(buffer[:limit])
	if !valid || bytes.IndexByte(content, 0) >= 0 {
		return nil, true, nil
	}
	return content, false, nil
}

func trimToUTF8Boundary(content []byte) ([]byte, bool) {
	if utf8.Valid(content) {
		return content, true
	}
	for trim := 1; trim < utf8.UTFMax && trim <= len(content); trim++ {
		candidate := content[:len(content)-trim]
		if utf8.Valid(candidate) {
			return candidate, true
		}
	}
	return nil, false
}

func alignUTF8Offset(ctx context.Context, file *os.File, offset int64, size int64) (int64, error) {
	if offset <= 0 || offset >= size {
		return offset, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	buffer := make([]byte, utf8.UTFMax)
	n, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	for index := 0; index < n; index++ {
		if buffer[index]&0xC0 != 0x80 {
			return offset + int64(index), nil
		}
	}
	return offset, nil
}

func openedFileIsBinary(ctx context.Context, file *os.File, size int64) (bool, error) {
	if size == 0 {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	length := int64(readFileSampleBytes + utf8.UTFMax - 1)
	if length > size {
		length = size
	}
	buffer := make([]byte, int(length))
	n, err := file.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	buffer = buffer[:n]
	if bytes.IndexByte(buffer, 0) >= 0 {
		return true, nil
	}
	sampleLength := readFileSampleBytes
	if len(buffer) < sampleLength {
		sampleLength = len(buffer)
	}
	_, valid := trimToUTF8Boundary(buffer[:sampleLength])
	return !valid, nil
}

func readOpenedFileBounded(ctx context.Context, file *os.File, size int64) ([]byte, error) {
	content := make([]byte, 0, int(size))
	buffer := make([]byte, readFileBufferBytes)
	for int64(len(content)) < size {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := size - int64(len(content))
		chunk := buffer
		if int64(len(chunk)) > remaining {
			chunk = chunk[:int(remaining)]
		}
		n, err := file.Read(chunk)
		content = append(content, chunk[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	return content, nil
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func pageEndLine(startLine int, content []byte) int {
	if startLine <= 0 || len(content) == 0 {
		return 0
	}
	lines := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		lines++
	}
	return startLine + lines - 1
}

func binaryFileResult(path string, size int64, revision string) FileResult {
	result := FileResult{
		Path: path, SizeBytes: size, NextOffsetBytes: size, EOF: true,
		FileRevision: revision, Mode: "binary", Binary: true,
	}
	applyFileClassification(&result, classifyFile(path, nil, true))
	return result
}

func ensureFileRevision(file *os.File, path string, expected string) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	actual := fileRevision(info)
	if actual != expected {
		return staleFileRevisionError(path, expected, actual)
	}
	return nil
}

func staleFileRevisionError(path string, expected string, actual string) error {
	return newFileReadError(
		"stale_file_revision",
		"file changed between paginated reads; restart from the first page using the new file_revision",
		map[string]any{"path": path, "expected_file_revision": expected, "actual_file_revision": actual},
	)
}

func remapFileReadErrorPath(err error, displayPath string) error {
	var readErr *FileReadError
	if !errors.As(err, &readErr) {
		return err
	}
	cloned := &FileReadError{Code: readErr.Code, Message: readErr.Message}
	if len(readErr.Metadata) > 0 {
		cloned.Metadata = make(map[string]any, len(readErr.Metadata))
		for key, value := range readErr.Metadata {
			cloned.Metadata[key] = value
		}
		cloned.Metadata["path"] = displayPath
	}
	return cloned
}

func fileRevision(info os.FileInfo) string {
	identity := []string{
		"v1",
		strconv.FormatInt(info.Size(), 10),
		strconv.FormatInt(info.ModTime().UnixNano(), 10),
		strconv.FormatUint(uint64(info.Mode()), 10),
		statIdentity(info.Sys()),
	}
	digest := sha256.Sum256([]byte(strings.Join(identity, "|")))
	return "stat-v1:" + hex.EncodeToString(digest[:16])
}

func statIdentity(value any) string {
	if value == nil {
		return ""
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return ""
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Struct {
		return fmt.Sprintf("%T", value)
	}
	fields := []string{"Dev", "Ino", "VolumeSerialNumber", "FileIndexHigh", "FileIndexLow"}
	parts := make([]string, 0, len(fields))
	for _, name := range fields {
		field := reflected.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			parts = append(parts, name+"="+strconv.FormatInt(field.Int(), 10))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			parts = append(parts, name+"="+strconv.FormatUint(field.Uint(), 10))
		}
	}
	return strings.Join(parts, ",")
}
