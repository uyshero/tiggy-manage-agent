package capability

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

const DefaultOnlyboxesImage = "coolfan1024/onlyboxes-runtime:default"
const DefaultOnlyboxesDataDirTTL = time.Hour

type OnlyboxesProvider struct {
	Image            string
	WorkspaceRoot    string
	IsolateWorkspace bool
	DataRoot         string
	DataDirTTL       time.Duration
	DisableNetwork   bool
	WorkspaceID      string
	OwnerID          string
	SessionID        string
	ContainerScope   string
	DockerCommand    string
	Store            SessionDataStore
	ObjectStore      objectstore.Client
	Runner           Provider
	ContainerManager *OnlyboxesContainerManager
}

type SessionDataStore interface {
	GetSession(id string) (managedagents.Session, error)
	ListSessionArtifacts(sessionID string) ([]managedagents.SessionArtifact, error)
	GetObjectRef(id string) (managedagents.ObjectRef, error)
}

type scopedSessionDataStore interface {
	GetSessionScoped(id string, scope managedagents.AccessScope) (managedagents.Session, error)
}

func (OnlyboxesProvider) ToolRuntime() string {
	return "cloud_sandbox"
}

func (OnlyboxesProvider) ToolCapabilities() []string {
	return []string{
		"filesystem.read",
		"filesystem.write",
		"exec",
		"code.execute",
		"browser.open",
		"browser.read",
		"browser.interact",
		"browser.capture",
	}
}

func (p OnlyboxesProvider) RequiresNetworkApproval() bool {
	return !p.DisableNetwork
}

func (p OnlyboxesProvider) RunCommand(ctx context.Context, request RunCommandRequest) (CommandResult, error) {
	if strings.TrimSpace(request.Command) == "" {
		return CommandResult{}, fmt.Errorf("onlyboxes command is required")
	}
	sessionID := strings.TrimSpace(p.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(request.Meta.SessionID)
		p.SessionID = sessionID
	}
	root, err := p.workspaceDir()
	if err != nil {
		return CommandResult{}, err
	}
	workDir, err := resolvePathInside(root, defaultGuardString(request.WorkDir, "."))
	if err != nil {
		return CommandResult{}, fmt.Errorf("onlyboxes work_dir denied: %w", err)
	}
	containerWorkDir, err := containerPathForHostPath(root, workDir)
	if err != nil {
		return CommandResult{}, err
	}
	dataDir, err := p.sessionDataDir()
	if err != nil {
		return CommandResult{}, err
	}
	if err := p.syncSessionFiles(ctx, root); err != nil {
		return CommandResult{}, err
	}
	if p.ContainerManager != nil && sessionID != "" {
		return p.ContainerManager.RunCommand(ctx, onlyboxesContainerCommand{
			Provider:         p,
			SessionID:        sessionID,
			IsolationKey:     p.sandboxIsolationKey(),
			Scope:            p.ContainerScope,
			WorkspaceRoot:    root,
			ContainerWorkDir: containerWorkDir,
			DataDir:          dataDir,
			Request:          request,
		})
	}

	args := []string{
		"run",
		"--pull", "missing",
		"--rm",
		"--cpus", "1",
		"--memory", "512m",
		"--pids-limit", "256",
		"--workdir", containerWorkDir,
		"--volume", root + ":/workspace:rw",
	}
	if len(request.Stdin) > 0 {
		args = append(args, "--interactive")
	}
	if p.DisableNetwork {
		args = append(args, "--network", "none")
	}
	if dataDir != "" {
		args = append(args, "--volume", dataDir+":/mnt/data:rw")
	}
	for key, value := range request.Env {
		if !validEnvKey(key) {
			return CommandResult{}, fmt.Errorf("invalid env key %q", key)
		}
		args = append(args, "--env", key+"="+value)
	}
	args = append(args, p.image(), request.Command)
	args = append(args, request.Args...)

	return p.runner().RunCommand(ctx, RunCommandRequest{
		Meta:    request.Meta,
		Command: p.dockerCommand(),
		Args:    args,
		Stdin:   request.Stdin,
	})
}

func (p OnlyboxesProvider) ExecuteCode(ctx context.Context, request ExecuteCodeRequest) (CommandResult, error) {
	switch strings.ToLower(request.Language) {
	case "sh", "shell":
		return p.RunCommand(ctx, RunCommandRequest{
			Meta:    request.Meta,
			Command: "sh",
			Args:    []string{"-c", request.Code},
			WorkDir: request.WorkDir,
			Env:     request.Env,
		})
	case "python", "python3":
		return p.RunCommand(ctx, RunCommandRequest{
			Meta:    request.Meta,
			Command: "python3",
			Args:    []string{"-c", request.Code},
			WorkDir: request.WorkDir,
			Env:     request.Env,
		})
	default:
		return CommandResult{}, fmt.Errorf("unsupported onlyboxes code language %q", request.Language)
	}
}

func (p OnlyboxesProvider) ReadFile(ctx context.Context, request ReadFileRequest) (FileResult, error) {
	workspaceDir, err := p.workspaceDir()
	if err != nil {
		return FileResult{}, err
	}
	if err := p.syncSessionFiles(ctx, workspaceDir); err != nil {
		return FileResult{}, err
	}
	path, err := p.resolveSandboxFilePath(request.Path, "")
	if err != nil {
		return FileResult{}, err
	}
	request.Path = path
	result, err := LocalSystemProvider{}.ReadFile(ctx, request)
	if err != nil {
		return FileResult{}, err
	}
	if displayPath, displayErr := p.hostPathToSandboxPath(path); displayErr == nil {
		result.Path = displayPath
	}
	return result, nil
}

func (p OnlyboxesProvider) WriteFile(ctx context.Context, request WriteFileRequest) (FileResult, error) {
	request.Path = p.normalizeLegacySandboxWritePath(request.Path)
	path, err := p.resolveSandboxFilePath(request.Path, "")
	if err != nil {
		return FileResult{}, err
	}
	request.Path = path
	result, err := LocalSystemProvider{}.WriteFile(ctx, request)
	if err != nil {
		return FileResult{}, err
	}
	if displayPath, displayErr := p.hostPathToSandboxPath(path); displayErr == nil {
		result.Path = displayPath
	}
	return result, nil
}

func (p OnlyboxesProvider) EditFile(ctx context.Context, request EditFileRequest) (EditFileResult, error) {
	rawPath := request.Path
	if rawPath == "" {
		rawPath = request.FilePath
	}
	workspaceDir, err := p.workspaceDir()
	if err != nil {
		return EditFileResult{}, err
	}
	if err := p.syncSessionFiles(ctx, workspaceDir); err != nil {
		return EditFileResult{}, err
	}
	path, err := p.resolveSandboxFilePath(rawPath, request.WorkDir)
	if err != nil {
		return EditFileResult{}, err
	}
	request.Path = path
	request.FilePath = ""
	request.WorkDir = ""
	result, err := LocalSystemProvider{}.EditFile(ctx, request)
	if err != nil {
		return EditFileResult{}, err
	}
	if displayPath, displayErr := p.hostPathToSandboxPath(path); displayErr == nil {
		result.Path = displayPath
	}
	return result, nil
}

func (p OnlyboxesProvider) ExportArtifactFile(_ context.Context, request ExportArtifactFileRequest) (ExportArtifactFileResult, error) {
	hostPath, err := p.resolveArtifactExportPath(request)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	info, err := os.Stat(hostPath)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	if info.IsDir() {
		return ExportArtifactFileResult{}, fmt.Errorf("artifact export path %q is a directory", request.Path)
	}
	content, err := os.ReadFile(hostPath)
	if err != nil {
		return ExportArtifactFileResult{}, err
	}
	return ExportArtifactFileResult{
		Path:        strings.TrimSpace(request.Path),
		Name:        filepath.Base(hostPath),
		ContentType: http.DetectContentType(content),
		Content:     content,
	}, nil
}

func (p OnlyboxesProvider) image() string {
	if strings.TrimSpace(p.Image) == "" {
		return DefaultOnlyboxesImage
	}
	return strings.TrimSpace(p.Image)
}

func (p OnlyboxesProvider) dockerCommand() string {
	if strings.TrimSpace(p.DockerCommand) == "" {
		return "docker"
	}
	return strings.TrimSpace(p.DockerCommand)
}

func (p OnlyboxesProvider) runner() Provider {
	if p.Runner == nil {
		return LocalSystemProvider{}
	}
	return p.Runner
}

func (p OnlyboxesProvider) sessionDataDir() (string, error) {
	root := strings.TrimSpace(p.DataRoot)
	if root == "" {
		return "", nil
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox data root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create sandbox data root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox data root symlink: %w", err)
	}
	if err := cleanupExpiredSessionDataDirs(root, p.dataDirTTL(), time.Now()); err != nil {
		return "", err
	}
	path := filepath.Join(root, p.sandboxScopeDirName())
	if err := createSandboxScopeDir(root, path); err != nil {
		return "", fmt.Errorf("create sandbox session data dir: %w", err)
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return path, nil
}

func (p OnlyboxesProvider) workspaceDir() (string, error) {
	if !p.IsolateWorkspace {
		return cleanWorkspaceRoot(p.WorkspaceRoot)
	}
	base := strings.TrimSpace(p.WorkspaceRoot)
	if base == "" {
		base = "/private/tmp/tma-cloud-sandbox-workspaces"
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox workspace base: %w", err)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create sandbox workspace base: %w", err)
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox workspace base symlink: %w", err)
	}
	path := filepath.Join(base, p.sandboxScopeDirName())
	if err := createSandboxScopeDir(base, path); err != nil {
		return "", fmt.Errorf("create isolated sandbox workspace: %w", err)
	}
	return path, nil
}

func (p OnlyboxesProvider) sandboxIsolationKey() string {
	return strings.Join([]string{
		strings.TrimSpace(p.WorkspaceID),
		strings.TrimSpace(p.OwnerID),
		strings.TrimSpace(p.SessionID),
	}, "\x00")
}

func (p OnlyboxesProvider) sandboxScopeDirName() string {
	session := safeSandboxSessionID(p.SessionID)
	if len(session) > 40 {
		session = session[:40]
	}
	digest := sha256.Sum256([]byte(p.sandboxIsolationKey()))
	return fmt.Sprintf("%s-%x", session, digest[:16])
}

func createSandboxScopeDir(root string, path string) error {
	if !pathInsideRoot(path, root) {
		return fmt.Errorf("sandbox scope path %q is outside root %q", path, root)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("sandbox scope path %q is not a real directory", path)
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
	} else {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !pathInsideRoot(resolved, root) {
		return fmt.Errorf("sandbox scope path %q resolves outside root %q", path, root)
	}
	return nil
}

func (p OnlyboxesProvider) syncSessionFiles(ctx context.Context, workspaceDir string) error {
	if strings.TrimSpace(workspaceDir) == "" {
		return nil
	}
	if p.Store == nil || p.ObjectStore == nil {
		return nil
	}
	sessionID := strings.TrimSpace(p.SessionID)
	if sessionID == "" {
		return nil
	}
	var session managedagents.Session
	var err error
	if scoped, ok := p.Store.(scopedSessionDataStore); ok && strings.TrimSpace(p.WorkspaceID) != "" {
		session, err = scoped.GetSessionScoped(sessionID, managedagents.AccessScope{WorkspaceID: p.WorkspaceID})
	} else {
		session, err = p.Store.GetSession(sessionID)
	}
	if err != nil {
		return fmt.Errorf("load sandbox session: %w", err)
	}
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		return err
	}
	artifacts, err := managedagents.ListSessionArtifactsWithContext(databaseCtx, p.Store, sessionID)
	if err != nil {
		return fmt.Errorf("list session artifacts: %w", err)
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactType != managedagents.ArtifactTypeFile {
			continue
		}
		objectRef, err := managedagents.GetObjectRefWithContext(databaseCtx, p.Store, artifact.ObjectRefID)
		if err != nil {
			return fmt.Errorf("load object ref %s: %w", artifact.ObjectRefID, err)
		}
		if objectRef.WorkspaceID != "" && session.WorkspaceID != "" && objectRef.WorkspaceID != session.WorkspaceID {
			return fmt.Errorf("object ref %s workspace mismatch", artifact.ObjectRefID)
		}
		targetPath, err := sessionFilePath(workspaceDir, artifact)
		if err != nil {
			return err
		}
		if _, err := os.Stat(targetPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat session file %s: %w", targetPath, err)
		}
		object, err := p.ObjectStore.GetObject(ctx, objectstore.GetObjectInput{
			Bucket:  objectRef.Bucket,
			Key:     objectRef.ObjectKey,
			Version: objectRef.ObjectVersion,
		})
		if err != nil {
			return fmt.Errorf("download object ref %s: %w", artifact.ObjectRefID, err)
		}
		content, readErr := io.ReadAll(object.Body)
		_ = object.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read object ref %s: %w", artifact.ObjectRefID, readErr)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("prepare session file dir: %w", err)
		}
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return fmt.Errorf("write session file %s: %w", targetPath, err)
		}
	}
	now := time.Now()
	_ = os.Chtimes(workspaceDir, now, now)
	return nil
}

func (p OnlyboxesProvider) dataDirTTL() time.Duration {
	if p.DataDirTTL <= 0 {
		return DefaultOnlyboxesDataDirTTL
	}
	return p.DataDirTTL
}

func cleanupExpiredSessionDataDirs(root string, ttl time.Duration, now time.Time) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("list sandbox data root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > ttl {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return fmt.Errorf("cleanup expired sandbox data dir %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

func safeSandboxSessionID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "anonymous"
	}
	replacer := regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	return replacer.ReplaceAllString(value, "-")
}

func sessionFilePath(workspaceDir string, artifact managedagents.SessionArtifact) (string, error) {
	path := SessionArtifactSandboxPath(artifact)
	return resolveContainerPathInside(workspaceDir, strings.TrimPrefix(path, "/workspace"))
}

// SessionArtifactSandboxPath returns the stable path exposed to tools after an
// uploaded session artifact is synchronized into the persistent workspace.
func SessionArtifactSandboxPath(artifact managedagents.SessionArtifact) string {
	name := safeSandboxFileName(artifact.Name)
	if name == "" {
		name = artifact.ID
	}
	if strings.TrimSpace(name) == "" {
		name = "artifact"
	}
	return filepath.ToSlash(filepath.Join("/workspace/uploads", artifact.ID, name))
}

func safeSandboxFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.Base(value)
	replacer := regexp.MustCompile(`[^\pL\pN_.-]+`)
	value = replacer.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	return value
}

func (p OnlyboxesProvider) resolveArtifactExportPath(request ExportArtifactFileRequest) (string, error) {
	return p.resolveSandboxFilePath(request.Path, request.WorkDir)
}

func (p OnlyboxesProvider) normalizeLegacySandboxWritePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	legacyPrefixes := []string{"/root", "/home"}
	matchesLegacyPrefix := false
	for _, prefix := range legacyPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			matchesLegacyPrefix = true
			break
		}
	}
	if !matchesLegacyPrefix {
		return path
	}

	targetRoot := "/workspace"

	switch {
	case path == "/root":
		return targetRoot
	case strings.HasPrefix(path, "/root/"):
		return filepath.ToSlash(filepath.Join(targetRoot, strings.TrimPrefix(path, "/root/")))
	case path == "/home":
		return targetRoot
	case strings.HasPrefix(path, "/home/"):
		relative := strings.TrimPrefix(path, "/home/")
		if index := strings.Index(relative, "/"); index >= 0 {
			relative = relative[index+1:]
		} else {
			relative = ""
		}
		if strings.TrimSpace(relative) == "" {
			return targetRoot
		}
		return filepath.ToSlash(filepath.Join(targetRoot, relative))
	default:
		return path
	}
}

func (p OnlyboxesProvider) hostPathToSandboxPath(hostPath string) (string, error) {
	hostPath = strings.TrimSpace(hostPath)
	if hostPath == "" {
		return "", fmt.Errorf("sandbox path is required")
	}
	if dataDir, err := p.sessionDataDir(); err == nil && strings.TrimSpace(dataDir) != "" {
		if relative, relErr := filepath.Rel(dataDir, hostPath); relErr == nil && relative == "." {
			return "/mnt/data", nil
		} else if relErr == nil && !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative) {
			return filepath.ToSlash(filepath.Join("/mnt/data", relative)), nil
		}
	}
	root, err := p.workspaceDir()
	if err != nil {
		return "", err
	}
	return containerPathForHostPath(root, hostPath)
}

func (p OnlyboxesProvider) resolveSandboxFilePath(path string, workDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(path, "/mnt/data/") || path == "/mnt/data" {
		dataDir, err := p.sessionDataDir()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(dataDir) == "" {
			return "", fmt.Errorf("sandbox session data dir is not configured")
		}
		return resolveContainerPathInside(dataDir, strings.TrimPrefix(path, "/mnt/data"))
	}
	root, err := p.workspaceDir()
	if err != nil {
		return "", err
	}
	workDir = strings.TrimSpace(workDir)
	switch {
	case strings.HasPrefix(path, "/workspace/") || path == "/workspace":
		return resolveContainerPathInside(root, strings.TrimPrefix(path, "/workspace"))
	case filepath.IsAbs(path):
		return "", fmt.Errorf("only /workspace and /mnt/data absolute paths are supported in cloud_sandbox, got %q", path)
	case strings.HasPrefix(workDir, "/mnt/data/") || workDir == "/mnt/data":
		dataDir, err := p.sessionDataDir()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(dataDir) == "" {
			return "", fmt.Errorf("sandbox session data dir is not configured")
		}
		base, err := resolveContainerPathInside(dataDir, strings.TrimPrefix(workDir, "/mnt/data"))
		if err != nil {
			return "", err
		}
		return resolvePathRelativeToRoot(dataDir, base, path)
	case strings.HasPrefix(workDir, "/workspace/") || workDir == "/workspace":
		base, err := resolveContainerPathInside(root, strings.TrimPrefix(workDir, "/workspace"))
		if err != nil {
			return "", err
		}
		return resolvePathRelativeToRoot(root, base, path)
	default:
		base, err := resolvePathInside(root, defaultGuardString(workDir, "."))
		if err != nil {
			return "", err
		}
		return resolvePathRelativeToRoot(root, base, path)
	}
}

func isSandboxDataPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "/mnt/data" || strings.HasPrefix(path, "/mnt/data/")
}

func resolveContainerPathInside(root string, value string) (string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	if value == "" {
		return root, nil
	}
	return resolvePathInside(root, value)
}

func resolvePathRelativeToRoot(root string, base string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("artifact export path is required")
	}
	if filepath.IsAbs(value) {
		return resolvePathInside(root, value)
	}
	return resolvePathInside(root, filepath.Join(base, value))
}

func containerPathForHostPath(root string, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if relative == "." {
		return "/workspace", nil
	}
	if strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%q is outside workspace root", path)
	}
	return filepath.ToSlash(filepath.Join("/workspace", relative)), nil
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validEnvKey(key string) bool {
	return envKeyPattern.MatchString(key)
}
