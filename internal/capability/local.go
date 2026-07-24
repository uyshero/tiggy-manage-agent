package capability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultRunCommandTimeout  = 120 * time.Second
	MinRunCommandTimeout      = 100 * time.Millisecond
	MaxRunCommandTimeout      = 10 * time.Minute
	DefaultCommandOutputBytes = 64 * 1024
	MaxCommandOutputBytes     = 1024 * 1024
)

type boundedCommandOutput struct {
	buffer    bytes.Buffer
	total     int64
	limit     int
	truncated bool
}

func (w *boundedCommandOutput) Write(data []byte) (int, error) {
	w.total += int64(len(data))
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		writeCount := len(data)
		if writeCount > remaining {
			writeCount = remaining
		}
		_, _ = w.buffer.Write(data[:writeCount])
	}
	if w.total > int64(w.limit) {
		w.truncated = true
	}
	return len(data), nil
}

func effectiveRunCommandLimits(request RunCommandRequest) (time.Duration, int, error) {
	timeout := DefaultRunCommandTimeout
	if request.TimeoutMS != 0 {
		timeout = time.Duration(request.TimeoutMS) * time.Millisecond
		if timeout < MinRunCommandTimeout || timeout > MaxRunCommandTimeout {
			return 0, 0, fmt.Errorf("run command timeout_ms must be between %d and %d", MinRunCommandTimeout.Milliseconds(), MaxRunCommandTimeout.Milliseconds())
		}
	}
	outputBytes := DefaultCommandOutputBytes
	if request.MaxOutputBytes != 0 {
		outputBytes = request.MaxOutputBytes
		if outputBytes < 1024 || outputBytes > MaxCommandOutputBytes {
			return 0, 0, fmt.Errorf("run command max_output_bytes must be between %d and %d", 1024, MaxCommandOutputBytes)
		}
	}
	return timeout, outputBytes, nil
}

// LocalSystemProvider 使用当前机器的进程和文件系统实现能力面。
// 它适合本地开发和受信任环境；需要隔离时应换成 OnlyboxesProvider / RemoteProvider。
type LocalSystemProvider struct {
	ReadFileLimits ReadFileLimits
}

func (LocalSystemProvider) ToolRuntime() string {
	return "local_system"
}

func (LocalSystemProvider) ToolCapabilities() []string {
	return []string{
		"filesystem.read",
		"filesystem.write",
		"exec",
		"code.execute",
		"browser.open",
		"browser.read",
		"browser.interact",
		"browser.capture",
		"browser.takeover",
		"browser.close",
	}
}

func (LocalSystemProvider) ResolveFileReference(_ context.Context, value string) (string, error) {
	reference, recognized, err := ParseFileReference(value)
	if err != nil || !recognized {
		return value, err
	}
	switch reference.Scope {
	case "absolute":
		return reference.Path, nil
	case "workspace":
		root, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve local workspace: %w", err)
		}
		return filepath.Join(root, filepath.FromSlash(reference.Path)), nil
	case "tmp":
		return filepath.Join(os.TempDir(), filepath.FromSlash(reference.Path)), nil
	case "data":
		root, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve local data directory: %w", err)
		}
		return filepath.Join(root, ".tma", "data", filepath.FromSlash(reference.Path)), nil
	case "artifact":
		return "", fmt.Errorf("artifact file references require a session-aware provider")
	default:
		return "", fmt.Errorf("unsupported file reference scope %q", reference.Scope)
	}
}

func (LocalSystemProvider) RunCommand(ctx context.Context, request RunCommandRequest) (CommandResult, error) {
	return runLocalCommand(ctx, request, nil)
}

func runLocalCommand(ctx context.Context, request RunCommandRequest, beforeStart func()) (CommandResult, error) {
	if request.Command == "" {
		return CommandResult{}, fmt.Errorf("local system command is required")
	}
	timeout, outputBytes, err := effectiveRunCommandLimits(request)
	if err != nil {
		return CommandResult{}, err
	}
	baseCtx, cancelBase := contextWithRequestDeadline(ctx, request.Meta.Deadline)
	defer cancelBase()
	if err := baseCtx.Err(); err != nil {
		return CommandResult{}, err
	}
	runCtx, cancelRun := context.WithTimeout(baseCtx, timeout)
	defer cancelRun()

	cmd := exec.Command(request.Command, request.Args...)
	workDirPinned := false
	if request.WorkDir != "" {
		workDir, inheritedDir, err := prepareGuardedCommandWorkDir(request.guardedRoot, request.WorkDir, len(cmd.ExtraFiles)+3)
		if err != nil {
			return CommandResult{}, err
		}
		if inheritedDir != nil {
			if cmd.Err != nil {
				_ = inheritedDir.Close()
				return CommandResult{}, cmd.Err
			}
			arguments := []string{"-c", "cd -P -- \"$1\" || exit 125\nexec 3<&-\nshift\nexec \"$@\"", "tma-workdir", workDir, cmd.Path}
			arguments = append(arguments, request.Args...)
			cmd = exec.Command("/bin/sh", arguments...)
			defer inheritedDir.Close()
			cmd.ExtraFiles = append(cmd.ExtraFiles, inheritedDir)
			workDirPinned = true
		} else {
			cmd.Dir = workDir
		}
	}
	configureCommandProcessGroup(cmd)
	if len(request.Env) > 0 {
		env := append([]string(nil), os.Environ()...)
		for key, value := range request.Env {
			prefix := key + "="
			filtered := env[:0]
			for _, entry := range env {
				if !strings.HasPrefix(entry, prefix) {
					filtered = append(filtered, entry)
				}
			}
			env = filtered
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
	if len(request.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(request.Stdin)
	}

	stdout := boundedCommandOutput{limit: outputBytes}
	stderr := boundedCommandOutput{limit: outputBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	if beforeStart != nil {
		beforeStart()
	}
	if !workDirPinned {
		if err := ensureGuardedMutationPath(request.WorkDir, request.guardedRoot); err != nil {
			return CommandResult{}, err
		}
	}
	if err := cmd.Start(); err != nil {
		return CommandResult{}, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	var waitErr error
	status := "completed"
	timedOut := false
	canceled := false
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		select {
		case waitErr = <-done:
		default:
			_ = terminateCommandProcessGroup(cmd)
			waitErr = <-done
		}
		if baseCtx.Err() != nil {
			canceled = errors.Is(baseCtx.Err(), context.Canceled)
			status = "canceled"
			if errors.Is(baseCtx.Err(), context.DeadlineExceeded) {
				status = "timeout"
				timedOut = true
			}
		} else {
			status = "timeout"
			timedOut = true
		}
	}
	result := CommandResult{
		Status:              status,
		ExitCode:            0,
		Stdout:              strings.ToValidUTF8(stdout.buffer.String(), "\uFFFD"),
		Stderr:              strings.ToValidUTF8(stderr.buffer.String(), "\uFFFD"),
		StdoutBytes:         stdout.total,
		StderrBytes:         stderr.total,
		StdoutCapturedBytes: stdout.buffer.Len(),
		StderrCapturedBytes: stderr.buffer.Len(),
		StdoutTruncated:     stdout.truncated,
		StderrTruncated:     stderr.truncated,
		DurationMS:          time.Since(startedAt).Milliseconds(),
		TimedOut:            timedOut,
		Canceled:            canceled,
	}
	if waitErr == nil {
		return result, nil
	}
	if baseCtx.Err() != nil {
		return result, baseCtx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, waitErr
}

func (provider LocalSystemProvider) ExecuteCode(ctx context.Context, request ExecuteCodeRequest) (CommandResult, error) {
	switch strings.ToLower(request.Language) {
	case "sh", "shell":
		return provider.RunCommand(ctx, RunCommandRequest{
			Meta:           request.Meta,
			Command:        "sh",
			Args:           []string{"-c", request.Code},
			WorkDir:        request.WorkDir,
			Env:            request.Env,
			TimeoutMS:      request.TimeoutMS,
			MaxOutputBytes: request.MaxOutputBytes,
			guardedRoot:    request.guardedRoot,
		})
	case "python", "python3":
		return provider.RunCommand(ctx, RunCommandRequest{
			Meta:           request.Meta,
			Command:        "python3",
			Args:           []string{"-c", request.Code},
			WorkDir:        request.WorkDir,
			Env:            request.Env,
			TimeoutMS:      request.TimeoutMS,
			MaxOutputBytes: request.MaxOutputBytes,
			guardedRoot:    request.guardedRoot,
		})
	default:
		return CommandResult{}, fmt.Errorf("unsupported local code language %q", request.Language)
	}
}

func (provider LocalSystemProvider) ReadFile(ctx context.Context, request ReadFileRequest) (FileResult, error) {
	return readLocalFile(ctx, request, provider.ReadFileLimits)
}

func (LocalSystemProvider) WriteFile(ctx context.Context, request WriteFileRequest) (FileResult, error) {
	return writeLocalFileAtomic(ctx, request)
}

func (LocalSystemProvider) EditFile(ctx context.Context, request EditFileRequest) (EditFileResult, error) {
	return editLocalFileContext(ctx, request), nil
}

func (LocalSystemProvider) PreviewEditFile(ctx context.Context, request EditFileRequest) (EditFilePreview, error) {
	return previewLocalFileContext(ctx, request), nil
}

func (LocalSystemProvider) ExportArtifactFile(ctx context.Context, request ExportArtifactFileRequest) (ExportArtifactFileResult, error) {
	return exportLocalArtifactFile(ctx, request, nil)
}

func exportLocalArtifactFile(ctx context.Context, request ExportArtifactFileRequest, beforeOpen func()) (ExportArtifactFileResult, error) {
	path := strings.TrimSpace(request.Path)
	if path == "" {
		return ExportArtifactFileResult{}, fmt.Errorf("artifact export path is required")
	}
	if !filepath.IsAbs(path) {
		workDir := strings.TrimSpace(request.WorkDir)
		if workDir == "" {
			workDir = "."
		}
		path = filepath.Join(workDir, path)
	}
	if err := ctx.Err(); err != nil {
		return ExportArtifactFileResult{}, err
	}
	file, err := openLocalFileForRead(ReadFileRequest{Path: path, guardedRoot: request.guardedRoot}, beforeOpen)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	if !info.Mode().IsRegular() {
		return ExportArtifactFileResult{}, newFileReadError("unsupported_file_type", "artifact export only supports regular files", map[string]any{"path": path})
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExportArtifactFileResult{}, err
	}
	return ExportArtifactFileResult{
		Path:        strings.TrimSpace(request.Path),
		Name:        filepath.Base(path),
		ContentType: http.DetectContentType(content),
		Content:     content,
	}, nil
}
