package capability

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

const maxWorkspaceSnapshotBytes = 512 << 20
const workspaceSnapshotRestoreMarker = ".tma-workspace-restored"

const DefaultOnlyboxesImage = "coolfan1024/onlyboxes-runtime:default"
const DefaultOnlyboxesDataDirTTL = time.Hour
const DefaultOnlyboxesMemoryLimit = "512m"

const runtimeSkillsSandboxRoot = "/tma/skills"

var runtimeSkillIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)
var runtimeSkillIdentifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
var runtimeSkillHexPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

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
	MemoryLimit      string
	DockerCommand    string
	Store            SessionDataStore
	ObjectStore      objectstore.Client
	Runner           Provider
	ContainerManager *OnlyboxesContainerManager
	SkillCacheRoot   string
	ReadFileLimits   ReadFileLimits
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

func (p OnlyboxesProvider) MaterializeRuntimeSkills(ctx context.Context, packages []RuntimeSkillPackage) ([]MaterializedRuntimeSkill, error) {
	hostSkillsRoot, err := p.runtimeSkillCacheDir()
	if err != nil {
		return nil, err
	}

	result := make([]MaterializedRuntimeSkill, 0, len(packages))
	for _, pkg := range packages {
		materialized, err := materializeRuntimeSkillPackage(ctx, hostSkillsRoot, pkg)
		if err != nil {
			return nil, err
		}
		result = append(result, materialized)
	}
	return result, nil
}

func (p OnlyboxesProvider) CreateWorkspaceSnapshot(_ context.Context) ([]byte, int, error) {
	root, err := p.workspaceDir()
	if err != nil {
		return nil, 0, err
	}
	paths := make([]string, 0)
	err = filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace snapshot does not support symbolic link %q", name)
		}
		if name == root {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relative) == workspaceSnapshotRestoreMarker {
			return nil
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(paths)
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	fileCount := 0
	for _, relative := range paths {
		name := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(name)
		if err != nil {
			return nil, 0, err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, 0, err
		}
		header.Name = relative
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if err := writer.WriteHeader(header); err != nil {
			return nil, 0, err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(name)
			if err != nil {
				return nil, 0, err
			}
			_, copyErr := io.Copy(writer, io.LimitReader(file, maxWorkspaceSnapshotBytes-int64(buffer.Len())+1))
			closeErr := file.Close()
			if copyErr != nil {
				return nil, 0, copyErr
			}
			if closeErr != nil {
				return nil, 0, closeErr
			}
			fileCount++
		}
		if buffer.Len() > maxWorkspaceSnapshotBytes {
			return nil, 0, fmt.Errorf("workspace snapshot exceeds %d bytes", maxWorkspaceSnapshotBytes)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, 0, err
	}
	return buffer.Bytes(), fileCount, nil
}

func (p OnlyboxesProvider) RestoreWorkspaceSnapshot(_ context.Context, archive []byte) error {
	root, err := p.workspaceDir()
	if err != nil {
		return err
	}
	marker := filepath.Join(root, workspaceSnapshotRestoreMarker)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		clean, err := cleanRuntimeSkillFilePath(header.Name)
		if err != nil {
			return fmt.Errorf("restore workspace snapshot: %w", err)
		}
		target := filepath.Join(root, filepath.FromSlash(clean))
		if !pathInsideRoot(target, root) {
			return fmt.Errorf("workspace snapshot path escapes root")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, io.LimitReader(reader, maxWorkspaceSnapshotBytes+1))
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("workspace snapshot contains unsupported entry %q", header.Name)
		}
	}
	return os.WriteFile(marker, []byte("restored\n"), 0o600)
}

func (p OnlyboxesProvider) LookupMaterializedRuntimeSkill(_ context.Context, skillID string, identifier string, version int, checksum string) (MaterializedRuntimeSkill, bool, error) {
	skillID = strings.TrimSpace(skillID)
	identifier = strings.TrimSpace(identifier)
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	if skillID == "" {
		skillID = identifier
	}
	if !runtimeSkillIDPattern.MatchString(skillID) || !runtimeSkillIdentifierPattern.MatchString(identifier) || version <= 0 || !runtimeSkillHexPattern.MatchString(checksum) {
		return MaterializedRuntimeSkill{}, false, nil
	}
	root, err := p.runtimeSkillCacheDir()
	if err != nil {
		return MaterializedRuntimeSkill{}, false, err
	}
	target := filepath.Join(root, "sha256", checksum, skillID, strconv.Itoa(version))
	marker, err := os.ReadFile(filepath.Join(target, ".tma-runtime-checksum"))
	if err != nil {
		if os.IsNotExist(err) {
			return MaterializedRuntimeSkill{}, false, nil
		}
		return MaterializedRuntimeSkill{}, false, err
	}
	if strings.TrimSpace(string(marker)) != checksum {
		return MaterializedRuntimeSkill{}, false, fmt.Errorf("runtime skill cache marker checksum mismatch for %s version %d", skillID, version)
	}
	alias := filepath.Join(root, skillID, strconv.Itoa(version))
	if err := ensureRuntimeSkillAlias(alias, target); err != nil {
		return MaterializedRuntimeSkill{}, false, err
	}
	return MaterializedRuntimeSkill{
		SkillID: skillID, Identifier: identifier, Version: version,
		Directory: path.Join(runtimeSkillsSandboxRoot, skillID, strconv.Itoa(version)),
	}, true, nil
}

func materializeRuntimeSkillPackage(ctx context.Context, hostSkillsRoot string, pkg RuntimeSkillPackage) (result MaterializedRuntimeSkill, err error) {
	identifier := strings.TrimSpace(pkg.Identifier)
	skillID := strings.TrimSpace(pkg.SkillID)
	if skillID == "" {
		skillID = identifier
	}
	if !runtimeSkillIdentifierPattern.MatchString(identifier) || !runtimeSkillIDPattern.MatchString(skillID) || pkg.Version <= 0 {
		return MaterializedRuntimeSkill{}, fmt.Errorf("invalid runtime skill package %q version %d", identifier, pkg.Version)
	}
	versionName := strconv.Itoa(pkg.Version)
	checksum := runtimeSkillPackageChecksum(pkg)
	cacheRoot := filepath.Join(hostSkillsRoot, "sha256")
	contentRoot := filepath.Join(cacheRoot, checksum)
	target := filepath.Join(contentRoot, skillID, versionName)
	aliasParent := filepath.Join(hostSkillsRoot, skillID)
	alias := filepath.Join(aliasParent, versionName)
	if !pathInsideRoot(target, hostSkillsRoot) || !pathInsideRoot(alias, hostSkillsRoot) {
		return MaterializedRuntimeSkill{}, fmt.Errorf("runtime skill target is outside skills root")
	}
	if err := os.MkdirAll(aliasParent, 0o755); err != nil {
		return MaterializedRuntimeSkill{}, fmt.Errorf("create runtime skill alias parent: %w", err)
	}
	directory := path.Join(runtimeSkillsSandboxRoot, skillID, versionName)
	marker := filepath.Join(target, ".tma-runtime-checksum")
	if release, lockErr := acquireRuntimeSkillLock(ctx, filepath.Join(hostSkillsRoot, ".locks", checksum+".lock")); lockErr != nil {
		return MaterializedRuntimeSkill{}, lockErr
	} else {
		defer release()
	}
	if stored, readErr := os.ReadFile(marker); readErr == nil && strings.TrimSpace(string(stored)) == checksum {
		if err := ensureRuntimeSkillAlias(alias, target); err != nil {
			return MaterializedRuntimeSkill{}, err
		}
		return MaterializedRuntimeSkill{SkillID: skillID, Identifier: identifier, Version: pkg.Version, Directory: directory}, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return MaterializedRuntimeSkill{}, fmt.Errorf("create runtime skill cache parent: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".tmp-")
	if err != nil {
		return MaterializedRuntimeSkill{}, fmt.Errorf("create runtime skill staging directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = makeRuntimeSkillWritable(staging)
			_ = os.RemoveAll(staging)
		}
	}()
	for _, file := range pkg.Files {
		cleanPath, cleanErr := cleanRuntimeSkillFilePath(file.Path)
		if cleanErr != nil {
			return MaterializedRuntimeSkill{}, fmt.Errorf("materialize runtime skill %s: %w", identifier, cleanErr)
		}
		filePath := filepath.Join(staging, filepath.FromSlash(cleanPath))
		if !pathInsideRoot(filePath, staging) {
			return MaterializedRuntimeSkill{}, fmt.Errorf("runtime skill file %q escapes package root", file.Path)
		}
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return MaterializedRuntimeSkill{}, fmt.Errorf("create runtime skill file directory: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(filePath, file.Content, mode); err != nil {
			return MaterializedRuntimeSkill{}, fmt.Errorf("write runtime skill file %q: %w", cleanPath, err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, ".tma-runtime-checksum"), []byte(checksum+"\n"), 0o444); err != nil {
		return MaterializedRuntimeSkill{}, fmt.Errorf("write runtime skill checksum: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		if stored, readErr := os.ReadFile(marker); readErr != nil || strings.TrimSpace(string(stored)) != checksum {
			return MaterializedRuntimeSkill{}, fmt.Errorf("commit runtime skill package: %w", err)
		}
		_ = os.RemoveAll(staging)
	}
	if err := makeRuntimeSkillReadOnly(target); err != nil {
		return MaterializedRuntimeSkill{}, err
	}
	if err := ensureRuntimeSkillAlias(alias, target); err != nil {
		return MaterializedRuntimeSkill{}, err
	}
	return MaterializedRuntimeSkill{SkillID: skillID, Identifier: identifier, Version: pkg.Version, Directory: directory}, nil
}

func acquireRuntimeSkillLock(ctx context.Context, lockPath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create runtime skill lock directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open runtime skill lock: %w", err)
	}
	for {
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return func() {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
			}, nil
		} else if err != unix.EWOULDBLOCK {
			_ = file.Close()
			return nil, fmt.Errorf("lock runtime skill cache: %w", err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		}
	}
}

func ensureRuntimeSkillAlias(alias string, target string) error {
	if existing, err := os.Readlink(alias); err == nil {
		resolved := filepath.Join(filepath.Dir(alias), existing)
		if resolved == target {
			return nil
		}
		return fmt.Errorf("runtime skill version integrity conflict: alias %q already references a different package checksum", alias)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect runtime skill alias: %w", err)
	} else if info, statErr := os.Stat(alias); statErr == nil {
		if info.IsDir() {
			return fmt.Errorf("runtime skill alias %q is a writable directory", alias)
		}
		if removeErr := os.Remove(alias); removeErr != nil {
			return fmt.Errorf("remove runtime skill alias: %w", removeErr)
		}
	}
	relative, err := filepath.Rel(filepath.Dir(alias), target)
	if err != nil {
		return fmt.Errorf("resolve runtime skill alias: %w", err)
	}
	if err := os.Symlink(relative, alias); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create runtime skill alias: %w", err)
	}
	return nil
}

func makeRuntimeSkillReadOnly(root string) error {
	return filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if info.IsDir() {
			mode = 0o555
		} else if info.Mode()&0o111 != 0 {
			mode = 0o555
		}
		if err := os.Chmod(name, mode); err != nil {
			return fmt.Errorf("make runtime skill path read-only %q: %w", name, err)
		}
		return nil
	})
}

func makeRuntimeSkillWritable(root string) error {
	return filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.IsDir() {
			mode = 0o755
		} else if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		return os.Chmod(name, mode)
	})
}

func runtimeSkillPackageChecksum(pkg RuntimeSkillPackage) string {
	if checksum := strings.TrimSpace(pkg.Checksum); len(checksum) == 64 && runtimeSkillHexPattern.MatchString(checksum) {
		return strings.ToLower(checksum)
	}
	hash := sha256.New()
	files := append([]RuntimeSkillFile(nil), pkg.Files...)
	slices.SortFunc(files, func(a, b RuntimeSkillFile) int { return strings.Compare(a.Path, b.Path) })
	for _, file := range files {
		fmt.Fprintf(hash, "%s\x00%d\x00", file.Path, boolInt(file.Executable))
		hash.Write(file.Content)
		hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cleanRuntimeSkillFilePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return "", fmt.Errorf("runtime skill file path must be relative and slash-separated")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("runtime skill file path %q escapes package root", value)
	}
	return cleaned, nil
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
	hostWorkDir, err := p.resolveSandboxFilePath(defaultGuardString(request.WorkDir, "."), "")
	if err != nil {
		return CommandResult{}, fmt.Errorf("onlyboxes work_dir denied: %w", err)
	}
	containerWorkDir, err := p.hostPathToSandboxPath(hostWorkDir)
	if err != nil {
		return CommandResult{}, fmt.Errorf("onlyboxes work_dir denied: %w", err)
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
			SkillsRoot:       func() string { value, _ := p.runtimeSkillCacheDir(); return value }(),
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
		"--memory", p.memoryLimit(),
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
	if skillCache, cacheErr := p.runtimeSkillCacheDir(); cacheErr != nil {
		return CommandResult{}, cacheErr
	} else {
		args = append(args, "--volume", skillCache+":/tma/skills:ro")
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

func (p OnlyboxesProvider) memoryLimit() string {
	if value := strings.TrimSpace(p.MemoryLimit); value != "" {
		return value
	}
	return DefaultOnlyboxesMemoryLimit
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
	displayPath := request.Path
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
	result, err := (LocalSystemProvider{ReadFileLimits: p.ReadFileLimits}).ReadFile(ctx, request)
	if err != nil {
		return FileResult{}, remapFileReadErrorPath(err, displayPath)
	}
	if displayPath, displayErr := p.hostPathToSandboxPath(path); displayErr == nil {
		result.Path = displayPath
	}
	return result, nil
}

func (p OnlyboxesProvider) SearchFile(ctx context.Context, request SearchFileRequest) (SearchFileResult, error) {
	displayPath := request.Path
	workspaceDir, err := p.workspaceDir()
	if err != nil {
		return SearchFileResult{}, err
	}
	if err := p.syncSessionFiles(ctx, workspaceDir); err != nil {
		return SearchFileResult{}, err
	}
	hostPath, err := p.resolveSandboxFilePath(request.Path, "")
	if err != nil {
		return SearchFileResult{}, err
	}
	request.Path = hostPath
	result, err := (LocalSystemProvider{ReadFileLimits: p.ReadFileLimits}).SearchFile(ctx, request)
	if err != nil {
		return SearchFileResult{}, remapFileReadErrorPath(err, displayPath)
	}
	if displayPath, displayErr := p.hostPathToSandboxPath(hostPath); displayErr == nil {
		result.Path = displayPath
	}
	return result, nil
}

func (p OnlyboxesProvider) FindFiles(ctx context.Context, request FindFilesRequest) (FindFilesResult, error) {
	displayRoot := request.Root
	if strings.TrimSpace(displayRoot) == "" {
		displayRoot = "/workspace"
	}
	workspaceDir, err := p.workspaceDir()
	if err != nil {
		return FindFilesResult{}, err
	}
	if err := p.syncSessionFiles(ctx, workspaceDir); err != nil {
		return FindFilesResult{}, err
	}
	hostRoot, err := p.resolveSandboxFilePath(displayRoot, "")
	if err != nil {
		return FindFilesResult{}, err
	}
	request.Root = hostRoot
	result, err := (LocalSystemProvider{ReadFileLimits: p.ReadFileLimits}).FindFiles(ctx, request)
	if err != nil {
		return FindFilesResult{}, remapFileReadErrorPath(err, displayRoot)
	}
	result.Root = displayRoot
	return result, nil
}

func (p OnlyboxesProvider) SearchFiles(ctx context.Context, request SearchFilesRequest) (SearchFilesResult, error) {
	displayRoot := request.Root
	if strings.TrimSpace(displayRoot) == "" {
		displayRoot = "/workspace"
	}
	workspaceDir, err := p.workspaceDir()
	if err != nil {
		return SearchFilesResult{}, err
	}
	if err := p.syncSessionFiles(ctx, workspaceDir); err != nil {
		return SearchFilesResult{}, err
	}
	hostRoot, err := p.resolveSandboxFilePath(displayRoot, "")
	if err != nil {
		return SearchFilesResult{}, err
	}
	request.Root = hostRoot
	result, err := (LocalSystemProvider{ReadFileLimits: p.ReadFileLimits}).SearchFiles(ctx, request)
	if err != nil {
		return SearchFilesResult{}, remapFileReadErrorPath(err, displayRoot)
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

func (p OnlyboxesProvider) runtimeSkillCacheDir() (string, error) {
	root := strings.TrimSpace(p.SkillCacheRoot)
	if root == "" {
		workspace, err := p.workspaceDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(filepath.Dir(workspace), ".tma-skill-cache")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve runtime skill cache root: %w", err)
	}
	workspaceKey := strings.TrimSpace(p.WorkspaceID)
	if workspaceKey == "" {
		workspaceKey = "default"
	}
	digest := sha256.Sum256([]byte(workspaceKey))
	root = filepath.Join(root, hex.EncodeToString(digest[:8]))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create runtime skill cache root: %w", err)
	}
	return root, nil
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
