package capability

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathGuardPolicy struct {
	RootDir       string
	WritableRoots []string
}

type WorkspacePathGuardProvider struct {
	Inner         Provider
	RootDir       string
	WritableRoots []string
}

func NewWorkspacePathGuardProvider(inner Provider, rootDir string) (WorkspacePathGuardProvider, error) {
	if inner == nil {
		inner = LocalSystemProvider{}
	}
	root, err := cleanWorkspaceRoot(rootDir)
	if err != nil {
		return WorkspacePathGuardProvider{}, err
	}
	return WorkspacePathGuardProvider{Inner: inner, RootDir: root, WritableRoots: []string{root}}, nil
}

func NewDefaultWorkspacePathGuardProvider() Provider {
	provider, err := NewWorkspacePathGuardProvider(LocalSystemProvider{}, "")
	if err != nil {
		return LocalSystemProvider{}
	}
	return provider
}

func (p WorkspacePathGuardProvider) RunCommand(ctx context.Context, request RunCommandRequest) (CommandResult, error) {
	workDir, root, err := p.resolveReadPathWithRoot(defaultGuardString(request.WorkDir, "."))
	if err != nil {
		return CommandResult{}, fmt.Errorf("workspace path guard work_dir denied: %w", err)
	}
	request.WorkDir = workDir
	request.guardedRoot = root
	return p.inner().RunCommand(ctx, request)
}

func (p WorkspacePathGuardProvider) ExecuteCode(ctx context.Context, request ExecuteCodeRequest) (CommandResult, error) {
	workDir, root, err := p.resolveReadPathWithRoot(defaultGuardString(request.WorkDir, "."))
	if err != nil {
		return CommandResult{}, fmt.Errorf("workspace path guard work_dir denied: %w", err)
	}
	request.WorkDir = workDir
	request.guardedRoot = root
	return p.inner().ExecuteCode(ctx, request)
}

func (p WorkspacePathGuardProvider) ReadFile(ctx context.Context, request ReadFileRequest) (FileResult, error) {
	displayPath := request.Path
	path, root, err := p.resolveReadPathWithRoot(request.Path)
	if err != nil {
		return FileResult{}, fmt.Errorf("workspace path guard read denied: %w", err)
	}
	request.Path = path
	request.guardedRoot = root
	result, err := p.inner().ReadFile(ctx, request)
	if err != nil {
		return FileResult{}, remapFileReadErrorPath(err, displayPath)
	}
	return result, nil
}

func (p WorkspacePathGuardProvider) SearchFile(ctx context.Context, request SearchFileRequest) (SearchFileResult, error) {
	displayPath := request.Path
	path, root, err := p.resolveReadPathWithRoot(request.Path)
	if err != nil {
		return SearchFileResult{}, fmt.Errorf("workspace path guard search denied: %w", err)
	}
	provider, ok := p.inner().(FileSearchProvider)
	if !ok {
		return SearchFileResult{}, fmt.Errorf("workspace file search is unavailable")
	}
	request.Path = path
	request.guardedRoot = root
	result, err := provider.SearchFile(ctx, request)
	if err != nil {
		return SearchFileResult{}, remapFileReadErrorPath(err, displayPath)
	}
	return result, nil
}

func (p WorkspacePathGuardProvider) FindFiles(ctx context.Context, request FindFilesRequest) (FindFilesResult, error) {
	displayRoot := defaultGuardString(request.Root, ".")
	root, guardedRoot, err := p.resolveReadPathWithRoot(displayRoot)
	if err != nil {
		return FindFilesResult{}, fmt.Errorf("workspace path guard discovery denied: %w", err)
	}
	provider, ok := p.inner().(FileDiscoveryProvider)
	if !ok {
		return FindFilesResult{}, fmt.Errorf("workspace file discovery is unavailable")
	}
	request.Root = root
	request.guardedRoot = guardedRoot
	result, err := provider.FindFiles(ctx, request)
	if err != nil {
		return FindFilesResult{}, remapFileReadErrorPath(err, displayRoot)
	}
	result.Root = displayRoot
	return result, nil
}

func (p WorkspacePathGuardProvider) SearchFiles(ctx context.Context, request SearchFilesRequest) (SearchFilesResult, error) {
	displayRoot := defaultGuardString(request.Root, ".")
	root, guardedRoot, err := p.resolveReadPathWithRoot(displayRoot)
	if err != nil {
		return SearchFilesResult{}, fmt.Errorf("workspace path guard search denied: %w", err)
	}
	provider, ok := p.inner().(FileTreeSearchProvider)
	if !ok {
		return SearchFilesResult{}, fmt.Errorf("workspace file tree search is unavailable")
	}
	request.Root = root
	request.guardedRoot = guardedRoot
	result, err := provider.SearchFiles(ctx, request)
	if err != nil {
		return SearchFilesResult{}, remapFileReadErrorPath(err, displayRoot)
	}
	return result, nil
}

func (p WorkspacePathGuardProvider) WriteFile(ctx context.Context, request WriteFileRequest) (FileResult, error) {
	path, root, err := p.resolveWritePathWithRoot(request.Path)
	if err != nil {
		return FileResult{}, fmt.Errorf("workspace path guard write denied: %w", err)
	}
	request.Path = path
	request.guardedRoot = root
	return p.inner().WriteFile(ctx, request)
}

func (p WorkspacePathGuardProvider) EditFile(ctx context.Context, request EditFileRequest) (EditFileResult, error) {
	workDir := request.WorkDir
	if workDir == "" {
		workDir = "."
	}
	resolvedWorkDir, err := p.resolveReadPath(workDir)
	if err != nil {
		return EditFileResult{}, fmt.Errorf("workspace path guard work_dir denied: %w", err)
	}
	path, root, err := p.resolveWritePathWithRoot(resolveAgainstWorkDir(request.Path, resolvedWorkDir))
	if err != nil {
		return EditFileResult{}, fmt.Errorf("workspace path guard edit denied: %w", err)
	}
	request.Path = path
	request.WorkDir = ""
	request.guardedRoot = root
	return p.inner().EditFile(ctx, request)
}

func (p WorkspacePathGuardProvider) ExportArtifactFile(ctx context.Context, request ExportArtifactFileRequest) (ExportArtifactFileResult, error) {
	displayPath := request.Path
	workDir, err := p.resolveReadPath(defaultGuardString(request.WorkDir, "."))
	if err != nil {
		return ExportArtifactFileResult{}, fmt.Errorf("workspace path guard artifact work_dir denied: %w", err)
	}
	path, root, err := p.resolveReadPathWithRoot(resolveAgainstWorkDir(request.Path, workDir))
	if err != nil {
		return ExportArtifactFileResult{}, fmt.Errorf("workspace path guard artifact export denied: %w", err)
	}
	exporter, ok := p.inner().(ArtifactExportProvider)
	if !ok || exporter == nil {
		return ExportArtifactFileResult{}, fmt.Errorf("workspace artifact export is unavailable")
	}
	request.Path = path
	request.WorkDir = ""
	request.guardedRoot = root
	result, err := exporter.ExportArtifactFile(ctx, request)
	if err != nil {
		return ExportArtifactFileResult{}, remapFileReadErrorPath(err, displayPath)
	}
	result.Path = displayPath
	return result, nil
}

func (p WorkspacePathGuardProvider) inner() Provider {
	if p.Inner == nil {
		return LocalSystemProvider{}
	}
	return p.Inner
}

func (p WorkspacePathGuardProvider) root() (string, error) {
	return cleanWorkspaceRoot(p.RootDir)
}

func (p WorkspacePathGuardProvider) writableRoots() ([]string, error) {
	root, err := p.root()
	if err != nil {
		return nil, err
	}
	if len(p.WritableRoots) == 0 {
		return []string{root}, nil
	}
	roots := make([]string, 0, len(p.WritableRoots))
	for _, value := range p.WritableRoots {
		resolved, err := resolvePathInside(root, value)
		if err != nil {
			return nil, err
		}
		roots = append(roots, resolved)
	}
	return roots, nil
}

func (p WorkspacePathGuardProvider) resolveReadPath(value string) (string, error) {
	path, _, err := p.resolveReadPathWithRoot(value)
	return path, err
}

func (p WorkspacePathGuardProvider) resolveReadPathWithRoot(value string) (string, string, error) {
	root, err := p.root()
	if err != nil {
		return "", "", err
	}
	path, err := resolvePathInside(root, value)
	if err != nil {
		return "", "", err
	}
	return path, root, nil
}

func (p WorkspacePathGuardProvider) resolveWritePathWithRoot(value string) (string, string, error) {
	path, err := p.resolveReadPath(value)
	if err != nil {
		return "", "", err
	}
	writableRoots, err := p.writableRoots()
	if err != nil {
		return "", "", err
	}
	for _, root := range writableRoots {
		if pathInsideRoot(path, root) {
			return path, root, nil
		}
	}
	return "", "", fmt.Errorf("%q is outside writable workspace roots", value)
}

func ensureGuardedMutationPath(path, root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if _, err := resolvePathInside(root, path); err != nil {
		return newFileReadError(
			"workspace_path_changed",
			"target path changed after workspace authorization; resolve the path and retry",
			map[string]any{"path": path},
		)
	}
	return nil
}

func cleanWorkspaceRoot(rootDir string) (string, error) {
	if strings.TrimSpace(rootDir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace cwd: %w", err)
		}
		rootDir = cwd
	}
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlink: %w", err)
	}
	return filepath.Clean(root), nil
}

func resolvePathInside(root string, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("path is required")
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	cleanPath := filepath.Clean(absPath)
	if !pathInsideRoot(cleanPath, root) {
		return "", fmt.Errorf("%q is outside workspace root %q", value, root)
	}
	resolvedPath, err := resolveExistingPathPrefix(cleanPath)
	if err != nil {
		return "", fmt.Errorf("resolve path symlinks: %w", err)
	}
	if !pathInsideRoot(resolvedPath, root) {
		return "", fmt.Errorf("%q resolves outside workspace root %q", value, root)
	}
	return cleanPath, nil
}

func resolveExistingPathPrefix(path string) (string, error) {
	probe := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func pathInsideRoot(path string, root string) bool {
	if path == root {
		return true
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != "." && !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative)
}

func defaultGuardString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
