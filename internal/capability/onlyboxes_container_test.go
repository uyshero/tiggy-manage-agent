package capability

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOnlyboxesContainerManagerReusesSessionContainer(t *testing.T) {
	runner := newFakeDockerProvider()
	manager := newTestOnlyboxesContainerManager(t, OnlyboxesContainerManagerConfig{})
	provider := testManagedOnlyboxesProvider(t, manager, runner, "sesn_reuse")

	for range 2 {
		result, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh", Args: []string{"-c", "pwd"}})
		if err != nil {
			t.Fatalf("run managed command: %v", err)
		}
		if result.Stdout != "exec ok" {
			t.Fatalf("unexpected command result: %#v", result)
		}
	}

	if runner.runCount() != 1 || runner.execCount() != 2 {
		t.Fatalf("expected one container create and two execs, runs=%d execs=%d calls=%#v", runner.runCount(), runner.execCount(), runner.callsSnapshot())
	}
}

func TestOnlyboxesContainerManagerRecreatesExpiredContainer(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	runner := newFakeDockerProvider()
	manager := newTestOnlyboxesContainerManager(t, OnlyboxesContainerManagerConfig{
		IdleTTL:         30 * time.Minute,
		MaxLifetime:     4 * time.Hour,
		CleanupInterval: time.Hour,
		Now:             func() time.Time { return now },
	})
	provider := testManagedOnlyboxesProvider(t, manager, runner, "sesn_expired")

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("first command: %v", err)
	}
	now = now.Add(31 * time.Minute)
	manager.reapExpired(now)
	if runner.removeCount() != 1 {
		t.Fatalf("expected expired container removal, got %d calls=%#v", runner.removeCount(), runner.callsSnapshot())
	}
	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("second command: %v", err)
	}
	if runner.runCount() != 2 {
		t.Fatalf("expected container recreation after idle ttl, got %d", runner.runCount())
	}
}

func TestOnlyboxesContainerManagerRecreatesContainerAtMaximumLifetime(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	runner := newFakeDockerProvider()
	manager := newTestOnlyboxesContainerManager(t, OnlyboxesContainerManagerConfig{
		IdleTTL:         2 * time.Hour,
		MaxLifetime:     time.Hour,
		CleanupInterval: time.Hour,
		Now:             func() time.Time { return now },
	})
	provider := testManagedOnlyboxesProvider(t, manager, runner, "sesn_max_lifetime")

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("first command: %v", err)
	}
	now = now.Add(61 * time.Minute)
	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("second command: %v", err)
	}
	if runner.runCount() != 2 || runner.removeCount() != 1 {
		t.Fatalf("expected maximum lifetime to rebuild container, runs=%d removes=%d", runner.runCount(), runner.removeCount())
	}
}

func TestOnlyboxesContainerManagerRecreatesContainerWhenConfigChanges(t *testing.T) {
	runner := newFakeDockerProvider()
	manager := newTestOnlyboxesContainerManager(t, OnlyboxesContainerManagerConfig{})
	provider := testManagedOnlyboxesProvider(t, manager, runner, "sesn_config")

	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("first command: %v", err)
	}
	provider.Image = "onlyboxes/test:v2"
	if _, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("second command: %v", err)
	}
	if runner.runCount() != 2 || runner.removeCount() != 1 {
		t.Fatalf("expected config change to rebuild container, runs=%d removes=%d calls=%#v", runner.runCount(), runner.removeCount(), runner.callsSnapshot())
	}
}

func TestOnlyboxesContainerManagerSerializesCommandsWithinSession(t *testing.T) {
	runner := newFakeDockerProvider()
	runner.execDelay = 20 * time.Millisecond
	manager := newTestOnlyboxesContainerManager(t, OnlyboxesContainerManagerConfig{})
	provider := testManagedOnlyboxesProvider(t, manager, runner, "sesn_serial")

	start := make(chan struct{})
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := provider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"})
			errors <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent command: %v", err)
		}
	}
	if runner.runCount() != 1 || runner.maxConcurrentExecs() != 1 {
		t.Fatalf("expected one container and serialized execs, runs=%d max_execs=%d", runner.runCount(), runner.maxConcurrentExecs())
	}
}

func TestOnlyboxesContainerManagerSeparatesContainerScopes(t *testing.T) {
	runner := newFakeDockerProvider()
	manager := newTestOnlyboxesContainerManager(t, OnlyboxesContainerManagerConfig{})
	defaultProvider := testManagedOnlyboxesProvider(t, manager, runner, "sesn_scopes")
	browserProvider := defaultProvider
	browserProvider.ContainerScope = "browser"
	browserProvider.Image = "onlyboxes/browser:latest"

	if _, err := defaultProvider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("default command: %v", err)
	}
	if _, err := browserProvider.RunCommand(context.Background(), RunCommandRequest{Command: "sh"}); err != nil {
		t.Fatalf("browser command: %v", err)
	}
	if runner.runCount() != 2 || runner.containerCount() != 2 {
		t.Fatalf("expected separate default and browser containers, runs=%d containers=%d", runner.runCount(), runner.containerCount())
	}
}

func newTestOnlyboxesContainerManager(t *testing.T, config OnlyboxesContainerManagerConfig) *OnlyboxesContainerManager {
	t.Helper()
	if config.CleanupInterval == 0 {
		config.CleanupInterval = time.Hour
	}
	manager := NewOnlyboxesContainerManager(config)
	t.Cleanup(manager.Close)
	return manager
}

func testManagedOnlyboxesProvider(t *testing.T, manager *OnlyboxesContainerManager, runner Provider, sessionID string) OnlyboxesProvider {
	t.Helper()
	return OnlyboxesProvider{
		Image:            "onlyboxes/test:latest",
		WorkspaceRoot:    t.TempDir(),
		SessionID:        sessionID,
		Runner:           runner,
		ContainerManager: manager,
	}
}

type fakeDockerContainer struct {
	fingerprint string
	createdAt   time.Time
}

type fakeDockerProvider struct {
	mu                 sync.Mutex
	containers         map[string]fakeDockerContainer
	calls              [][]string
	runs               int
	execs              int
	removes            int
	activeExecs        int
	maximumActiveExecs int
	execDelay          time.Duration
}

func newFakeDockerProvider() *fakeDockerProvider {
	return &fakeDockerProvider{containers: make(map[string]fakeDockerContainer)}
}

func (p *fakeDockerProvider) RunCommand(_ context.Context, request RunCommandRequest) (CommandResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, append([]string(nil), request.Args...))
	if len(request.Args) == 0 {
		p.mu.Unlock()
		return CommandResult{}, fmt.Errorf("missing docker args")
	}
	switch request.Args[0] {
	case "inspect":
		name := request.Args[len(request.Args)-1]
		container, ok := p.containers[name]
		p.mu.Unlock()
		if !ok {
			return CommandResult{ExitCode: 1, Stderr: "not found"}, nil
		}
		return CommandResult{Stdout: fmt.Sprintf("true|%s|%s\n", container.fingerprint, container.createdAt.Format(time.RFC3339Nano))}, nil
	case "run":
		name := argumentValue(request.Args, "--name")
		fingerprint := strings.TrimPrefix(argumentValueWithPrefix(request.Args, "tma.onlyboxes.fingerprint="), "tma.onlyboxes.fingerprint=")
		p.containers[name] = fakeDockerContainer{fingerprint: fingerprint, createdAt: time.Now().UTC()}
		p.runs++
		p.mu.Unlock()
		return CommandResult{Stdout: "container-id"}, nil
	case "exec":
		p.execs++
		p.activeExecs++
		if p.activeExecs > p.maximumActiveExecs {
			p.maximumActiveExecs = p.activeExecs
		}
		delay := p.execDelay
		p.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		p.mu.Lock()
		p.activeExecs--
		p.mu.Unlock()
		return CommandResult{Stdout: "exec ok"}, nil
	case "rm":
		name := request.Args[len(request.Args)-1]
		delete(p.containers, name)
		p.removes++
		p.mu.Unlock()
		return CommandResult{}, nil
	default:
		p.mu.Unlock()
		return CommandResult{}, fmt.Errorf("unexpected docker command %q", request.Args[0])
	}
}

func (p *fakeDockerProvider) ExecuteCode(context.Context, ExecuteCodeRequest) (CommandResult, error) {
	return CommandResult{}, nil
}

func (p *fakeDockerProvider) ReadFile(context.Context, ReadFileRequest) (FileResult, error) {
	return FileResult{}, nil
}

func (p *fakeDockerProvider) WriteFile(context.Context, WriteFileRequest) (FileResult, error) {
	return FileResult{}, nil
}

func (p *fakeDockerProvider) EditFile(context.Context, EditFileRequest) (EditFileResult, error) {
	return EditFileResult{}, nil
}

func (p *fakeDockerProvider) runCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs
}

func (p *fakeDockerProvider) execCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.execs
}

func (p *fakeDockerProvider) removeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.removes
}

func (p *fakeDockerProvider) maxConcurrentExecs() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maximumActiveExecs
}

func (p *fakeDockerProvider) containerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.containers)
}

func (p *fakeDockerProvider) callsSnapshot() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	calls := make([][]string, 0, len(p.calls))
	for _, call := range p.calls {
		calls = append(calls, append([]string(nil), call...))
	}
	return calls
}

func argumentValue(args []string, key string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key {
			return args[index+1]
		}
	}
	return ""
}

func argumentValueWithPrefix(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return arg
		}
	}
	return ""
}

func TestOnlyboxesContainerNameIsStableAndScoped(t *testing.T) {
	defaultName := onlyboxesContainerName("session/with spaces", "")
	if defaultName != onlyboxesContainerName("session/with spaces", "default") {
		t.Fatalf("expected empty and default scope to share a name")
	}
	if defaultName == onlyboxesContainerName("session/with spaces", "browser") {
		t.Fatalf("expected browser scope to use a different name")
	}
	if strings.Contains(filepath.Base(defaultName), " ") {
		t.Fatalf("expected docker-safe container name %q", defaultName)
	}
}
