package capability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalSystemProvider 使用当前机器的进程和文件系统实现能力面。
// 它适合本地开发和受信任环境；需要隔离时应换成 OnlyboxesProvider / RemoteProvider。
type LocalSystemProvider struct{}

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

func (LocalSystemProvider) RunCommand(ctx context.Context, request RunCommandRequest) (CommandResult, error) {
	if request.Command == "" {
		return CommandResult{}, fmt.Errorf("local system command is required")
	}

	cmd := exec.CommandContext(ctx, request.Command, request.Args...)
	if request.WorkDir != "" {
		cmd.Dir = request.WorkDir
	}
	if len(request.Env) > 0 {
		env := os.Environ()
		for key, value := range request.Env {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
	if len(request.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(request.Stdin)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return CommandResult{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return CommandResult{}, err
}

func (provider LocalSystemProvider) ExecuteCode(ctx context.Context, request ExecuteCodeRequest) (CommandResult, error) {
	switch strings.ToLower(request.Language) {
	case "sh", "shell":
		return provider.RunCommand(ctx, RunCommandRequest{
			Meta:    request.Meta,
			Command: "sh",
			Args:    []string{"-c", request.Code},
			WorkDir: request.WorkDir,
			Env:     request.Env,
		})
	case "python", "python3":
		return provider.RunCommand(ctx, RunCommandRequest{
			Meta:    request.Meta,
			Command: "python3",
			Args:    []string{"-c", request.Code},
			WorkDir: request.WorkDir,
			Env:     request.Env,
		})
	default:
		return CommandResult{}, fmt.Errorf("unsupported local code language %q", request.Language)
	}
}

func (LocalSystemProvider) ReadFile(_ context.Context, request ReadFileRequest) (FileResult, error) {
	content, err := os.ReadFile(request.Path)
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: request.Path, Content: content}, nil
}

func (LocalSystemProvider) WriteFile(_ context.Context, request WriteFileRequest) (FileResult, error) {
	if err := os.WriteFile(request.Path, request.Content, 0o644); err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: request.Path}, nil
}

func (LocalSystemProvider) EditFile(_ context.Context, request EditFileRequest) (EditFileResult, error) {
	return editLocalFile(request), nil
}

func (LocalSystemProvider) ExportArtifactFile(_ context.Context, request ExportArtifactFileRequest) (ExportArtifactFileResult, error) {
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
	content, err := os.ReadFile(path)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	if info.IsDir() {
		return ExportArtifactFileResult{}, fmt.Errorf("artifact export path %q is a directory", path)
	}
	return ExportArtifactFileResult{
		Path:        strings.TrimSpace(request.Path),
		Name:        filepath.Base(path),
		ContentType: http.DetectContentType(content),
		Content:     content,
	}, nil
}
