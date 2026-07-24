package capability

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	DefaultOnlyboxesContainerIdleTTL         = 30 * time.Minute
	DefaultOnlyboxesContainerMaxLifetime     = 4 * time.Hour
	DefaultOnlyboxesContainerCleanupInterval = time.Minute
	onlyboxesContainerCleanupTimeout         = 30 * time.Second
)

type OnlyboxesContainerManagerConfig struct {
	IdleTTL         time.Duration
	MaxLifetime     time.Duration
	CleanupInterval time.Duration
	Logger          *slog.Logger
	Now             func() time.Time
}

type OnlyboxesContainerManager struct {
	idleTTL         time.Duration
	maxLifetime     time.Duration
	cleanupInterval time.Duration
	logger          *slog.Logger
	now             func() time.Time

	mu        sync.Mutex
	states    map[string]*onlyboxesContainerState
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type onlyboxesContainerState struct {
	mu            sync.Mutex
	name          string
	fingerprint   string
	createdAt     time.Time
	lastUsedAt    time.Time
	running       bool
	runner        Provider
	dockerCommand string
}

type onlyboxesContainerCommand struct {
	Provider         OnlyboxesProvider
	SessionID        string
	IsolationKey     string
	Scope            string
	WorkspaceRoot    string
	SkillsRoot       string
	ContainerWorkDir string
	DataDir          string
	TempDir          string
	Request          RunCommandRequest
}

type onlyboxesContainerInspection struct {
	exists      bool
	running     bool
	fingerprint string
	createdAt   time.Time
}

func NewOnlyboxesContainerManager(config OnlyboxesContainerManagerConfig) *OnlyboxesContainerManager {
	if config.IdleTTL <= 0 {
		config.IdleTTL = DefaultOnlyboxesContainerIdleTTL
	}
	if config.MaxLifetime <= 0 {
		config.MaxLifetime = DefaultOnlyboxesContainerMaxLifetime
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = DefaultOnlyboxesContainerCleanupInterval
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	manager := &OnlyboxesContainerManager{
		idleTTL:         config.IdleTTL,
		maxLifetime:     config.MaxLifetime,
		cleanupInterval: config.CleanupInterval,
		logger:          config.Logger,
		now:             config.Now,
		states:          make(map[string]*onlyboxesContainerState),
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
	go manager.reapLoop()
	return manager
}

func (m *OnlyboxesContainerManager) RunCommand(ctx context.Context, command onlyboxesContainerCommand) (CommandResult, error) {
	for key := range command.Request.Env {
		if !validEnvKey(key) {
			return CommandResult{}, fmt.Errorf("invalid env key %q", key)
		}
	}
	state := m.state(command.SessionID, command.IsolationKey, command.Scope)
	state.mu.Lock()
	defer state.mu.Unlock()

	now := m.now()
	fingerprint := onlyboxesContainerFingerprint(command)
	if err := m.ensureContainer(ctx, state, command, fingerprint, now); err != nil {
		return CommandResult{}, err
	}

	args := []string{"exec"}
	if len(command.Request.Stdin) > 0 {
		args = append(args, "--interactive")
	}
	args = append(args, "--workdir", command.ContainerWorkDir)
	requestEnv := sandboxTemporaryEnvironment(command.Request.Env)
	for key, value := range requestEnv {
		args = append(args, "--env", key+"="+value)
	}
	args = append(args, state.name, command.Request.Command)
	args = append(args, command.Request.Args...)
	result, err := state.runner.RunCommand(ctx, RunCommandRequest{
		Meta:           command.Request.Meta,
		Command:        state.dockerCommand,
		Args:           args,
		Stdin:          command.Request.Stdin,
		TimeoutMS:      command.Request.TimeoutMS,
		MaxOutputBytes: command.Request.MaxOutputBytes,
	})
	state.lastUsedAt = m.now()
	if result.TimedOut || result.Canceled || (err != nil && ctx.Err() != nil) {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), onlyboxesContainerCleanupTimeout)
		cleanupErr := m.removeContainer(cleanupCtx, state)
		cleanupCancel()
		if cleanupErr != nil {
			return result, fmt.Errorf("sandbox command stopped but container cleanup failed: %w", cleanupErr)
		}
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (m *OnlyboxesContainerManager) Close() {
	m.closeOnce.Do(func() {
		close(m.stop)
		<-m.done
		for _, state := range m.snapshotStates() {
			state.mu.Lock()
			if state.running {
				ctx, cancel := context.WithTimeout(context.Background(), onlyboxesContainerCleanupTimeout)
				err := m.removeContainer(ctx, state)
				cancel()
				if err != nil {
					m.logger.Warn("remove onlyboxes container during shutdown failed", "container", state.name, "error", err)
				}
			}
			state.mu.Unlock()
		}
	})
}

func (m *OnlyboxesContainerManager) ensureContainer(ctx context.Context, state *onlyboxesContainerState, command onlyboxesContainerCommand, fingerprint string, now time.Time) error {
	inspection, err := inspectOnlyboxesContainer(ctx, command.Provider.runner(), command.Provider.dockerCommand(), state.name)
	if err != nil {
		return err
	}
	expired := state.running && (now.Sub(state.lastUsedAt) > m.idleTTL || now.Sub(state.createdAt) > m.maxLifetime)
	if inspection.exists && !inspection.createdAt.IsZero() && now.Sub(inspection.createdAt) > m.maxLifetime {
		expired = true
	}
	mismatch := inspection.exists && inspection.fingerprint != fingerprint
	if inspection.exists && (!inspection.running || mismatch || expired) {
		state.runner = command.Provider.runner()
		state.dockerCommand = command.Provider.dockerCommand()
		state.running = true
		if err := m.removeContainer(ctx, state); err != nil {
			return err
		}
		inspection = onlyboxesContainerInspection{}
	}
	if inspection.exists && inspection.running {
		state.running = true
		state.fingerprint = fingerprint
		state.runner = command.Provider.runner()
		state.dockerCommand = command.Provider.dockerCommand()
		if !inspection.createdAt.IsZero() {
			state.createdAt = inspection.createdAt
		} else if state.createdAt.IsZero() {
			state.createdAt = now
		}
		if state.lastUsedAt.IsZero() {
			state.lastUsedAt = now
		}
		return nil
	}

	args := []string{
		"run",
		"--pull", "missing",
		"--detach",
		"--name", state.name,
		"--label", "tma.onlyboxes.managed=true",
		"--label", "tma.onlyboxes.session=" + safeSandboxSessionID(command.SessionID),
		"--label", "tma.onlyboxes.scope=" + onlyboxesContainerScope(command.Scope),
		"--label", "tma.onlyboxes.fingerprint=" + fingerprint,
		"--cpus", "1",
		"--memory", command.Provider.memoryLimit(),
		"--pids-limit", "256",
		"--workdir", "/workspace",
		"--volume", command.WorkspaceRoot + ":/workspace:rw",
		"--env", "TMPDIR=/tmp",
		"--env", "TMP=/tmp",
		"--env", "TEMP=/tmp",
	}
	if command.SkillsRoot != "" {
		args = append(args, "--volume", command.SkillsRoot+":/tma/skills:ro")
	}
	if command.Provider.DisableNetwork {
		args = append(args, "--network", "none")
	}
	if command.DataDir != "" {
		args = append(args, "--volume", command.DataDir+":/mnt/data:rw")
	}
	if command.TempDir != "" {
		args = append(args, "--volume", command.TempDir+":/tmp:rw")
	}
	args = append(args, command.Provider.image(), "sh", "-c", "while :; do sleep 3600; done")
	state.runner = command.Provider.runner()
	state.dockerCommand = command.Provider.dockerCommand()
	result, err := state.runner.RunCommand(ctx, RunCommandRequest{
		Meta:    command.Request.Meta,
		Command: state.dockerCommand,
		Args:    args,
	})
	if err != nil {
		return fmt.Errorf("create onlyboxes session container: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("create onlyboxes session container failed: %s", strings.TrimSpace(result.Stderr))
	}
	state.running = true
	state.fingerprint = fingerprint
	state.createdAt = now
	state.lastUsedAt = now
	m.logger.Info("onlyboxes session container created", "container", state.name, "session_id", command.SessionID, "scope", onlyboxesContainerScope(command.Scope))
	return nil
}

func (m *OnlyboxesContainerManager) state(sessionID string, isolationKey string, scope string) *onlyboxesContainerState {
	key := isolationKey + "\x00" + onlyboxesContainerScope(scope)
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.states[key]; state != nil {
		return state
	}
	state := &onlyboxesContainerState{name: onlyboxesContainerName(sessionID, isolationKey, scope)}
	m.states[key] = state
	return state
}

func (m *OnlyboxesContainerManager) reapLoop() {
	defer close(m.done)
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.reapExpired(m.now())
		}
	}
}

func (m *OnlyboxesContainerManager) reapExpired(now time.Time) {
	for _, state := range m.snapshotStates() {
		if !state.mu.TryLock() {
			continue
		}
		expired := state.running && (now.Sub(state.lastUsedAt) > m.idleTTL || now.Sub(state.createdAt) > m.maxLifetime)
		if expired {
			ctx, cancel := context.WithTimeout(context.Background(), onlyboxesContainerCleanupTimeout)
			err := m.removeContainer(ctx, state)
			cancel()
			if err != nil {
				m.logger.Warn("remove expired onlyboxes container failed", "container", state.name, "error", err)
			}
		}
		state.mu.Unlock()
	}
}

func (m *OnlyboxesContainerManager) snapshotStates() []*onlyboxesContainerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := make([]*onlyboxesContainerState, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, state)
	}
	return states
}

func (m *OnlyboxesContainerManager) removeContainer(ctx context.Context, state *onlyboxesContainerState) error {
	if state.runner == nil || state.dockerCommand == "" {
		state.running = false
		return nil
	}
	result, err := state.runner.RunCommand(ctx, RunCommandRequest{
		Command: state.dockerCommand,
		Args:    []string{"rm", "--force", state.name},
	})
	if err != nil {
		return fmt.Errorf("remove onlyboxes container %s: %w", state.name, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("remove onlyboxes container %s failed: %s", state.name, strings.TrimSpace(result.Stderr))
	}
	state.running = false
	m.logger.Info("onlyboxes session container removed", "container", state.name)
	return nil
}

func inspectOnlyboxesContainer(ctx context.Context, runner Provider, dockerCommand string, name string) (onlyboxesContainerInspection, error) {
	result, err := runner.RunCommand(ctx, RunCommandRequest{
		Command: dockerCommand,
		Args: []string{
			"inspect",
			"--format", `{{.State.Running}}|{{index .Config.Labels "tma.onlyboxes.fingerprint"}}|{{.Created}}`,
			name,
		},
	})
	if err != nil {
		return onlyboxesContainerInspection{}, fmt.Errorf("inspect onlyboxes container %s: %w", name, err)
	}
	if result.ExitCode != 0 {
		return onlyboxesContainerInspection{}, nil
	}
	parts := strings.SplitN(strings.TrimSpace(result.Stdout), "|", 3)
	if len(parts) != 3 {
		return onlyboxesContainerInspection{}, fmt.Errorf("inspect onlyboxes container %s returned unexpected output %q", name, result.Stdout)
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, parts[2])
	return onlyboxesContainerInspection{
		exists:      true,
		running:     parts[0] == "true",
		fingerprint: parts[1],
		createdAt:   createdAt,
	}, nil
}

func onlyboxesContainerFingerprint(command onlyboxesContainerCommand) string {
	payload := strings.Join([]string{
		command.Provider.image(),
		"memory=" + command.Provider.memoryLimit(),
		command.WorkspaceRoot,
		command.SkillsRoot,
		command.DataDir,
		command.TempDir,
		fmt.Sprintf("network_disabled=%t", command.Provider.DisableNetwork),
	}, "\x00")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
}

func sandboxTemporaryEnvironment(values map[string]string) map[string]string {
	result := make(map[string]string, len(values)+3)
	for key, value := range values {
		result[key] = value
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		result[key] = "/tmp"
	}
	return result
}

func onlyboxesContainerName(sessionID string, isolationKey string, scope string) string {
	safeSessionID := safeSandboxSessionID(sessionID)
	if len(safeSessionID) > 32 {
		safeSessionID = safeSessionID[:32]
	}
	hash := sha256.Sum256([]byte(isolationKey + "\x00" + onlyboxesContainerScope(scope)))
	return fmt.Sprintf("tma-onlyboxes-%s-%x", safeSessionID, hash[:6])
}

func onlyboxesContainerScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "default"
	}
	return safeSandboxSessionID(scope)
}
