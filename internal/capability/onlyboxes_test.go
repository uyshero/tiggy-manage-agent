package capability

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

func TestOnlyboxesProviderRunsCommandThroughDocker(t *testing.T) {
	root := t.TempDir()
	runner := &captureProvider{}
	provider := OnlyboxesProvider{
		Image:         "onlyboxes/test:latest",
		WorkspaceRoot: root,
		Runner:        runner,
	}

	result, err := provider.RunCommand(context.Background(), RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "pwd"},
		WorkDir: ".",
		Env:     map[string]string{"TMA_TEST": "1"},
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.Stdout != "docker ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if runner.request.Command != "docker" {
		t.Fatalf("expected docker command, got %#v", runner.request)
	}
	for _, expected := range []string{"run", "--pull", "missing", "--rm", "--cpus", "1", "--memory", "512m", "--pids-limit", "256", "--workdir", "/workspace", "--volume", resolvedRoot(t, root) + ":/workspace:rw", "--env", "TMA_TEST=1", "onlyboxes/test:latest", "sh"} {
		if !slices.Contains(runner.request.Args, expected) {
			t.Fatalf("expected docker args to contain %q, got %#v", expected, runner.request.Args)
		}
	}
	for _, arg := range runner.request.Args {
		if arg == "none" {
			t.Fatalf("expected default sandbox network to be enabled, got args %#v", runner.request.Args)
		}
		if strings.Contains(arg, ":/mnt/data:rw") {
			t.Fatalf("expected no session data volume without data root, got args %#v", runner.request.Args)
		}
	}
}

func TestOnlyboxesProviderDisablesNetworkWhenConfigured(t *testing.T) {
	root := t.TempDir()
	runner := &captureProvider{}
	provider := OnlyboxesProvider{
		Image:          "onlyboxes/test:latest",
		WorkspaceRoot:  root,
		DisableNetwork: true,
		Runner:         runner,
	}

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "python3 -c 'print(1)'"},
		WorkDir: ".",
	}); err != nil {
		t.Fatalf("run command: %v", err)
	}

	if !slices.Contains(runner.request.Args, "--network") || !slices.Contains(runner.request.Args, "none") {
		t.Fatalf("expected network isolation flag when disabled, got %#v", runner.request.Args)
	}
}

func TestOnlyboxesProviderMountsSessionDataDir(t *testing.T) {
	root := t.TempDir()
	dataRoot := t.TempDir()
	runner := &captureProvider{}
	provider := OnlyboxesProvider{
		Image:         "onlyboxes/test:latest",
		WorkspaceRoot: root,
		DataRoot:      dataRoot,
		SessionID:     "user:one/session 42",
		Runner:        runner,
	}

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("run command: %v", err)
	}

	expectedDir, err := provider.sessionDataDir()
	if err != nil {
		t.Fatalf("resolve session data dir: %v", err)
	}
	expectedVolume := expectedDir + ":/mnt/data:rw"
	if !slices.Contains(runner.request.Args, expectedVolume) {
		t.Fatalf("expected docker args to contain data volume %q, got %#v", expectedVolume, runner.request.Args)
	}
	if info, err := os.Stat(expectedDir); err != nil || !info.IsDir() {
		t.Fatalf("expected session data dir to exist, info=%#v err=%v", info, err)
	}
}

func TestOnlyboxesProviderReusesSessionDataDir(t *testing.T) {
	dataRoot := t.TempDir()
	provider := OnlyboxesProvider{
		DataRoot:  dataRoot,
		SessionID: "sesn_000001",
	}

	first, err := provider.sessionDataDir()
	if err != nil {
		t.Fatalf("first session data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first, "state.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	second, err := provider.sessionDataDir()
	if err != nil {
		t.Fatalf("second session data dir: %v", err)
	}

	if first != second {
		t.Fatalf("expected same session data dir, first=%q second=%q", first, second)
	}
	content, err := os.ReadFile(filepath.Join(second, "state.txt"))
	if err != nil {
		t.Fatalf("read kept state: %v", err)
	}
	if string(content) != "kept" {
		t.Fatalf("unexpected kept state %q", string(content))
	}
}

func TestOnlyboxesProviderIsolatesWorkspaceByWorkspaceOwnerAndSession(t *testing.T) {
	base := t.TempDir()
	provider := func(workspaceID, ownerID, sessionID string) OnlyboxesProvider {
		return OnlyboxesProvider{
			WorkspaceRoot:    base,
			IsolateWorkspace: true,
			WorkspaceID:      workspaceID,
			OwnerID:          ownerID,
			SessionID:        sessionID,
		}
	}

	first := provider("wksp-one", "owner-one", "session-one")
	firstDir, err := first.workspaceDir()
	if err != nil {
		t.Fatalf("resolve first workspace: %v", err)
	}
	if _, err := first.WriteFile(context.Background(), WriteFileRequest{Path: "/workspace/private.txt", Content: []byte("first")}); err != nil {
		t.Fatalf("write first workspace: %v", err)
	}

	for name, other := range map[string]OnlyboxesProvider{
		"session":   provider("wksp-one", "owner-one", "session-two"),
		"owner":     provider("wksp-one", "owner-two", "session-one"),
		"workspace": provider("wksp-two", "owner-one", "session-one"),
	} {
		otherDir, err := other.workspaceDir()
		if err != nil {
			t.Fatalf("resolve %s workspace: %v", name, err)
		}
		if otherDir == firstDir {
			t.Fatalf("expected %s scope to use a different workspace", name)
		}
		if _, err := other.ReadFile(context.Background(), ReadFileRequest{Path: "/workspace/private.txt"}); !os.IsNotExist(err) {
			t.Fatalf("expected %s scope not to see first file, err=%v", name, err)
		}
	}

	reusedDir, err := provider("wksp-one", "owner-one", "session-one").workspaceDir()
	if err != nil {
		t.Fatalf("resolve reused workspace: %v", err)
	}
	if reusedDir != firstDir {
		t.Fatalf("expected same scope to reuse workspace, first=%q reused=%q", firstDir, reusedDir)
	}
}

func TestOnlyboxesProviderIsolatesSessionDataByOwner(t *testing.T) {
	dataRoot := t.TempDir()
	first := OnlyboxesProvider{DataRoot: dataRoot, WorkspaceID: "wksp", OwnerID: "owner-one", SessionID: "same-session"}
	second := OnlyboxesProvider{DataRoot: dataRoot, WorkspaceID: "wksp", OwnerID: "owner-two", SessionID: "same-session"}

	firstDir, err := first.sessionDataDir()
	if err != nil {
		t.Fatalf("resolve first data dir: %v", err)
	}
	secondDir, err := second.sessionDataDir()
	if err != nil {
		t.Fatalf("resolve second data dir: %v", err)
	}
	if firstDir == secondDir {
		t.Fatalf("expected different owners to use different data dirs: %q", firstDir)
	}
}

func TestOnlyboxesProviderRejectsScopeDirectorySymlink(t *testing.T) {
	base := t.TempDir()
	out := t.TempDir()
	provider := OnlyboxesProvider{
		WorkspaceRoot:    base,
		IsolateWorkspace: true,
		WorkspaceID:      "wksp",
		OwnerID:          "owner",
		SessionID:        "session",
	}
	if err := os.Symlink(out, filepath.Join(base, provider.sandboxScopeDirName())); err != nil {
		t.Fatalf("create scope symlink: %v", err)
	}
	if _, err := provider.workspaceDir(); err == nil {
		t.Fatal("expected scope symlink to be rejected")
	}
}

func TestCleanupExpiredSessionDataDirs(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	freshDir := filepath.Join(root, "fresh")
	for _, path := range []string{oldDir, freshDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldDir, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("touch old dir: %v", err)
	}
	if err := os.Chtimes(freshDir, now.Add(-10*time.Minute), now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("touch fresh dir: %v", err)
	}

	if err := cleanupExpiredSessionDataDirs(root, time.Hour, now); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expected old dir to be removed, err=%v", err)
	}
	if info, err := os.Stat(freshDir); err != nil || !info.IsDir() {
		t.Fatalf("expected fresh dir to remain, info=%#v err=%v", info, err)
	}
}

func TestOnlyboxesProviderSyncsSessionFilesIntoWorkspace(t *testing.T) {
	root := t.TempDir()
	dataRoot := t.TempDir()
	session := managedagents.Session{
		ID:          "sesn_000001",
		WorkspaceID: managedagents.DefaultWorkspaceID,
	}
	store := &sessionDataStoreFake{
		session: session,
		artifacts: []managedagents.SessionArtifact{
			{
				ID:           "art_000001",
				WorkspaceID:  session.WorkspaceID,
				SessionID:    session.ID,
				ObjectRefID:  "obj_000001",
				Name:         "input.csv",
				ArtifactType: managedagents.ArtifactTypeFile,
			},
		},
		objectRefs: map[string]managedagents.ObjectRef{
			"obj_000001": {
				ID:              "obj_000001",
				WorkspaceID:     session.WorkspaceID,
				Bucket:          "tma-artifacts",
				ObjectKey:       "wksp_default/sesn_000001/uploads/input.csv",
				ChecksumSHA256:  "sha-1",
				StorageProvider: managedagents.ObjectStorageProviderS3,
			},
		},
	}
	objectStore := &fakeSessionObjectStore{
		objects: map[string]string{
			"tma-artifacts|wksp_default/sesn_000001/uploads/input.csv|": "name,value\nalpha,1\n",
		},
	}
	runner := &captureProvider{}
	provider := OnlyboxesProvider{
		Image:         "onlyboxes/test:latest",
		WorkspaceRoot: root,
		DataRoot:      dataRoot,
		SessionID:     session.ID,
		Store:         store,
		ObjectStore:   objectStore,
		Runner:        runner,
	}

	read, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "/workspace/uploads/art_000001/input.csv"})
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(read.Content) != "name,value\nalpha,1\n" {
		t.Fatalf("unexpected uploaded file content %q", string(read.Content))
	}

	resolvedWorkspaceDir, err := provider.workspaceDir()
	if err != nil {
		t.Fatalf("resolve session workspace dir: %v", err)
	}
	firstPath := filepath.Join(resolvedWorkspaceDir, "uploads", "art_000001", "input.csv")
	content, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("read synced file: %v", err)
	}
	if string(content) != "name,value\nalpha,1\n" {
		t.Fatalf("unexpected synced file content %q", string(content))
	}
	if err := os.RemoveAll(dataRoot); err != nil {
		t.Fatalf("remove temporary data root: %v", err)
	}
	persisted, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "/workspace/uploads/art_000001/input.csv"})
	if err != nil || string(persisted.Content) != "name,value\nalpha,1\n" {
		t.Fatalf("expected upload to survive temporary data cleanup, content=%q err=%v", persisted.Content, err)
	}

	if err := os.WriteFile(firstPath, []byte("edited\n"), 0o644); err != nil {
		t.Fatalf("edit synced file: %v", err)
	}

	store.artifacts = append(store.artifacts, managedagents.SessionArtifact{
		ID:           "art_000002",
		WorkspaceID:  session.WorkspaceID,
		SessionID:    session.ID,
		ObjectRefID:  "obj_000002",
		Name:         "notes.txt",
		ArtifactType: managedagents.ArtifactTypeFile,
	})
	store.objectRefs["obj_000002"] = managedagents.ObjectRef{
		ID:              "obj_000002",
		WorkspaceID:     session.WorkspaceID,
		Bucket:          "tma-artifacts",
		ObjectKey:       "wksp_default/sesn_000001/uploads/notes.txt",
		ChecksumSHA256:  "sha-2",
		StorageProvider: managedagents.ObjectStorageProviderS3,
	}
	objectStore.objects["tma-artifacts|wksp_default/sesn_000001/uploads/notes.txt|"] = "second file\n"

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("second run command: %v", err)
	}

	edited, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(edited) != "edited\n" {
		t.Fatalf("expected existing synced file to stay edited, got %q", string(edited))
	}

	secondPath := filepath.Join(resolvedWorkspaceDir, "uploads", "art_000002", "notes.txt")
	secondContent, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("read second synced file: %v", err)
	}
	if string(secondContent) != "second file\n" {
		t.Fatalf("unexpected second synced file content %q", string(secondContent))
	}
}

func TestOnlyboxesProviderExportsSandboxDataFile(t *testing.T) {
	root := t.TempDir()
	dataRoot := t.TempDir()
	sessionID := "sesn_000001"
	provider := OnlyboxesProvider{
		WorkspaceRoot: root,
		DataRoot:      dataRoot,
		SessionID:     sessionID,
	}
	dataDir, err := provider.sessionDataDir()
	if err != nil {
		t.Fatalf("resolve session data dir: %v", err)
	}
	exportPath := filepath.Join(dataDir, "result.txt")
	if err := os.WriteFile(exportPath, []byte("sandbox export"), 0o644); err != nil {
		t.Fatalf("write export file: %v", err)
	}

	result, err := provider.ExportArtifactFile(context.Background(), ExportArtifactFileRequest{
		Path: "/mnt/data/result.txt",
	})
	if err != nil {
		t.Fatalf("export sandbox file: %v", err)
	}
	if result.Name != "result.txt" || string(result.Content) != "sandbox export" {
		t.Fatalf("unexpected export result: %#v", result)
	}
}

func TestOnlyboxesProviderExportsWorkspaceRelativeFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "report.txt"), []byte("workspace export"), 0o644); err != nil {
		t.Fatalf("write workspace export: %v", err)
	}
	provider := OnlyboxesProvider{WorkspaceRoot: root}

	result, err := provider.ExportArtifactFile(context.Background(), ExportArtifactFileRequest{
		Path:    "report.txt",
		WorkDir: "dist",
	})
	if err != nil {
		t.Fatalf("export workspace file: %v", err)
	}
	if result.Name != "report.txt" || string(result.Content) != "workspace export" {
		t.Fatalf("unexpected workspace export result: %#v", result)
	}
}

func TestOnlyboxesProviderDeniesEscapedWorkDir(t *testing.T) {
	root := t.TempDir()
	provider := OnlyboxesProvider{WorkspaceRoot: root, Runner: &captureProvider{}}

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh", WorkDir: "../"}); err == nil {
		t.Fatal("expected escaped workdir to be denied")
	}
}

func TestOnlyboxesProviderUsesWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	provider := OnlyboxesProvider{WorkspaceRoot: root, Runner: &captureProvider{}}

	if _, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "../outside.txt"}); err == nil {
		t.Fatal("expected escaped read to be denied")
	}
	result, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "file.txt"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if result.Path != "/workspace/file.txt" {
		t.Fatalf("unexpected read path: %#v", result)
	}
	if string(result.Content) != "hello" {
		t.Fatalf("unexpected file content: %q", string(result.Content))
	}
}

func TestOnlyboxesProviderSupportsSandboxDataFiles(t *testing.T) {
	root := t.TempDir()
	dataRoot := t.TempDir()
	provider := OnlyboxesProvider{
		WorkspaceRoot: root,
		DataRoot:      dataRoot,
		SessionID:     "sesn_000001",
	}

	written, err := provider.WriteFile(context.Background(), WriteFileRequest{
		Path:    "/mnt/data/baidu_image_downloader.py",
		Content: []byte("#!/usr/bin/env python3\nprint('ok')\n"),
	})
	if err != nil {
		t.Fatalf("write sandbox file: %v", err)
	}
	if written.Path != "/mnt/data/baidu_image_downloader.py" {
		t.Fatalf("unexpected written path: %#v", written)
	}

	read, err := provider.ReadFile(context.Background(), ReadFileRequest{Path: "/mnt/data/baidu_image_downloader.py"})
	if err != nil {
		t.Fatalf("read sandbox file: %v", err)
	}
	if string(read.Content) != "#!/usr/bin/env python3\nprint('ok')\n" {
		t.Fatalf("unexpected sandbox file content: %q", string(read.Content))
	}

	edited, err := provider.EditFile(context.Background(), EditFileRequest{
		Path:      "/mnt/data/baidu_image_downloader.py",
		OldString: "print('ok')",
		NewString: "print('ready')",
	})
	if err != nil {
		t.Fatalf("edit sandbox file: %v", err)
	}
	if !edited.Success {
		t.Fatalf("expected sandbox edit success, got %#v", edited)
	}
	if edited.Path != "/mnt/data/baidu_image_downloader.py" {
		t.Fatalf("unexpected edited path: %#v", edited)
	}

	dataDir, err := provider.sessionDataDir()
	if err != nil {
		t.Fatalf("resolve session data dir: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, "baidu_image_downloader.py"))
	if err != nil {
		t.Fatalf("read edited sandbox file: %v", err)
	}
	if string(content) != "#!/usr/bin/env python3\nprint('ready')\n" {
		t.Fatalf("unexpected edited sandbox content: %q", string(content))
	}
}

func TestSessionArtifactSandboxPathIsStableAndSafe(t *testing.T) {
	artifact := managedagents.SessionArtifact{ID: "art_000001", Name: "2026 中国人工智能报告.pdf"}
	if got, want := SessionArtifactSandboxPath(artifact), "/workspace/uploads/art_000001/2026_中国人工智能报告.pdf"; got != want {
		t.Fatalf("unexpected sandbox artifact path: got %q want %q", got, want)
	}
}

func TestOnlyboxesProviderRewritesLegacyRootWritePathToWorkspace(t *testing.T) {
	root := t.TempDir()
	dataRoot := t.TempDir()
	provider := OnlyboxesProvider{
		WorkspaceRoot: root,
		DataRoot:      dataRoot,
		SessionID:     "sesn_000001",
	}

	written, err := provider.WriteFile(context.Background(), WriteFileRequest{
		Path:    "/root/reports/AI新闻精选.md",
		Content: []byte("weekly brief"),
	})
	if err != nil {
		t.Fatalf("write legacy root path: %v", err)
	}
	if written.Path != "/workspace/reports/AI新闻精选.md" {
		t.Fatalf("unexpected written path: %#v", written)
	}

	workspaceDir, err := provider.workspaceDir()
	if err != nil {
		t.Fatalf("resolve workspace dir: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workspaceDir, "reports", "AI新闻精选.md"))
	if err != nil {
		t.Fatalf("read rewritten sandbox file: %v", err)
	}
	if string(content) != "weekly brief" {
		t.Fatalf("unexpected rewritten sandbox content: %q", string(content))
	}
}

func TestOnlyboxesProviderRealDocker(t *testing.T) {
	if os.Getenv("TMA_RUN_ONLYBOXES_TESTS") != "1" {
		t.Skip("set TMA_RUN_ONLYBOXES_TESTS=1 to run real Onlyboxes verification")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("host-input"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	image := os.Getenv("TMA_ONLYBOXES_TEST_IMAGE")
	if image == "" {
		image = DefaultOnlyboxesImage
	}
	dockerCommand := os.Getenv("TMA_ONLYBOXES_DOCKER_COMMAND")
	manager := NewOnlyboxesContainerManager(OnlyboxesContainerManagerConfig{CleanupInterval: time.Hour})
	t.Cleanup(manager.Close)
	provider := OnlyboxesProvider{
		Image:            image,
		WorkspaceRoot:    root,
		SessionID:        "sesn_real_docker",
		DockerCommand:    dockerCommand,
		ContainerManager: manager,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := provider.RunCommand(ctx, RunCommandRequest{
		Command: "sh",
		Args: []string{
			"-c",
			"pwd && cat marker.txt && printf container-output > out.txt && printf session-state > /tmp/tma-session-state",
		},
		WorkDir: ".",
	})
	if err != nil {
		t.Fatalf("run onlyboxes command: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code %d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "/workspace") || !strings.Contains(result.Stdout, "host-input") {
		t.Fatalf("expected command to run in /workspace, stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
	content, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatalf("read generated output: %v", err)
	}
	if string(content) != "container-output" {
		t.Fatalf("unexpected generated output %q", string(content))
	}
	second, err := provider.RunCommand(ctx, RunCommandRequest{
		Command: "sh",
		Args:    []string{"-c", "cat /tmp/tma-session-state"},
		WorkDir: ".",
	})
	if err != nil {
		t.Fatalf("reuse onlyboxes container: %v", err)
	}
	if second.ExitCode != 0 || strings.TrimSpace(second.Stdout) != "session-state" {
		t.Fatalf("expected second command to reuse container, result=%#v", second)
	}
}

type captureProvider struct {
	request RunCommandRequest
}

func (p *captureProvider) RunCommand(_ context.Context, request RunCommandRequest) (CommandResult, error) {
	p.request = request
	return CommandResult{Stdout: "docker ok"}, nil
}

func (p *captureProvider) ExecuteCode(context.Context, ExecuteCodeRequest) (CommandResult, error) {
	return CommandResult{}, nil
}

func (p *captureProvider) ReadFile(context.Context, ReadFileRequest) (FileResult, error) {
	return FileResult{}, nil
}

func (p *captureProvider) WriteFile(context.Context, WriteFileRequest) (FileResult, error) {
	return FileResult{}, nil
}

func (p *captureProvider) EditFile(context.Context, EditFileRequest) (EditFileResult, error) {
	return EditFileResult{}, nil
}

func resolvedRoot(t *testing.T, root string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	return resolved
}

type sessionDataStoreFake struct {
	session    managedagents.Session
	artifacts  []managedagents.SessionArtifact
	objectRefs map[string]managedagents.ObjectRef
}

func (s *sessionDataStoreFake) GetSession(string) (managedagents.Session, error) {
	return s.session, nil
}

func (s *sessionDataStoreFake) ListSessionArtifacts(string) ([]managedagents.SessionArtifact, error) {
	return append([]managedagents.SessionArtifact(nil), s.artifacts...), nil
}

func (s *sessionDataStoreFake) GetObjectRef(id string) (managedagents.ObjectRef, error) {
	if ref, ok := s.objectRefs[id]; ok {
		return ref, nil
	}
	return managedagents.ObjectRef{}, os.ErrNotExist
}

type fakeSessionObjectStore struct {
	objects   map[string]string
	requested []objectstore.GetObjectInput
}

func (s *fakeSessionObjectStore) PutObject(context.Context, objectstore.PutObjectInput) (objectstore.PutObjectResult, error) {
	return objectstore.PutObjectResult{}, objectstore.ErrNotConfigured
}

func (s *fakeSessionObjectStore) GetObject(_ context.Context, input objectstore.GetObjectInput) (objectstore.GetObjectResult, error) {
	if s.objects == nil {
		return objectstore.GetObjectResult{}, objectstore.ErrNotFound
	}
	s.requested = append(s.requested, input)
	key := input.Bucket + "|" + input.Key + "|" + input.Version
	content, ok := s.objects[key]
	if !ok {
		return objectstore.GetObjectResult{}, objectstore.ErrNotFound
	}
	return objectstore.GetObjectResult{
		Bucket:      input.Bucket,
		Key:         input.Key,
		Version:     input.Version,
		Body:        io.NopCloser(strings.NewReader(content)),
		ContentType: "text/plain",
		SizeBytes:   int64(len(content)),
	}, nil
}

func (s *fakeSessionObjectStore) DeleteObject(context.Context, objectstore.DeleteObjectInput) error {
	return objectstore.ErrNotConfigured
}

func (s *fakeSessionObjectStore) PresignGetObject(context.Context, objectstore.PresignGetObjectInput) (objectstore.PresignedURL, error) {
	return objectstore.PresignedURL{}, objectstore.ErrNotConfigured
}
