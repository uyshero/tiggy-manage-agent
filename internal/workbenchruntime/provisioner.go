package workbenchruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type TemplateFile struct {
	Path    string
	Content string
}

type StartInput struct {
	ProjectID string
	Files     []TemplateFile
}

type StartResult struct {
	RuntimeID string
	Status    string
	URL       string
}

type StopInput struct {
	RuntimeID string
}

type RunCleaningInput struct {
	RuntimeID string
	ProjectID string
	Files     []TemplateFile
}

type RunCleaningResult struct {
	ExitCode int            `json:"exit_code"`
	Stdout   string         `json:"stdout,omitempty"`
	Stderr   string         `json:"stderr,omitempty"`
	Files    []TemplateFile `json:"files,omitempty"`
}

type Provisioner interface {
	Start(context.Context, StartInput) (StartResult, error)
	Stop(context.Context, StopInput) error
	RunCleaning(context.Context, RunCleaningInput) (RunCleaningResult, error)
}

type CommandRunner interface {
	Run(context.Context, string, ...string) (CommandResult, error)
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type DockerConfig struct {
	Command       string
	Image         string
	WorkspaceRoot string
	NotebookPath  string
	PortBase      int
	PortRange     int
	Runner        CommandRunner
	Now           func() time.Time
}

type DockerProvisioner struct {
	config DockerConfig
}

func DockerProvisionerFromEnv(runner CommandRunner) (*DockerProvisioner, error) {
	managed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("TMA_R_NOTEBOOK_RUNTIME_MANAGED")))
	if err != nil || !managed {
		return nil, nil
	}
	portBase := 18888
	if value := strings.TrimSpace(os.Getenv("TMA_R_NOTEBOOK_RUNTIME_PORT_BASE")); value != "" {
		portBase, err = strconv.Atoi(value)
		if err != nil || portBase < 1024 || portBase > 65500 {
			return nil, errors.New("TMA_R_NOTEBOOK_RUNTIME_PORT_BASE must be a valid non-privileged port")
		}
	}
	workspaceRoot := strings.TrimSpace(os.Getenv("TMA_R_NOTEBOOK_RUNTIME_WORKSPACE_ROOT"))
	if workspaceRoot == "" {
		workspaceRoot = filepath.Join(os.TempDir(), "tma-r-workbench")
	}
	return NewDockerProvisioner(DockerConfig{
		Command:       defaultString(strings.TrimSpace(os.Getenv("TMA_R_NOTEBOOK_RUNTIME_DOCKER")), "docker"),
		Image:         defaultString(strings.TrimSpace(os.Getenv("TMA_R_NOTEBOOK_RUNTIME_IMAGE")), "tma-r-notebook:local"),
		WorkspaceRoot: workspaceRoot,
		NotebookPath:  defaultString(strings.TrimSpace(os.Getenv("TMA_R_NOTEBOOK_RUNTIME_PATH")), "/lab"),
		PortBase:      portBase,
		PortRange:     1000,
		Runner:        runner,
		Now:           time.Now,
	})
}

func NewDockerProvisioner(config DockerConfig) (*DockerProvisioner, error) {
	if strings.TrimSpace(config.Command) == "" || strings.TrimSpace(config.Image) == "" {
		return nil, errors.New("Docker command and image are required")
	}
	if strings.TrimSpace(config.WorkspaceRoot) == "" {
		return nil, errors.New("runtime workspace root is required")
	}
	if config.PortBase < 1024 || config.PortBase > 65500 {
		return nil, errors.New("runtime port base must be a valid non-privileged port")
	}
	if config.PortRange <= 0 {
		config.PortRange = 1000
	}
	if config.PortBase+config.PortRange > 65535 {
		return nil, errors.New("runtime port range exceeds the TCP port limit")
	}
	if config.Runner == nil {
		config.Runner = execCommandRunner{}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &DockerProvisioner{config: config}, nil
}

func (p *DockerProvisioner) Start(ctx context.Context, input StartInput) (StartResult, error) {
	projectID, err := safeProjectID(input.ProjectID)
	if err != nil {
		return StartResult{}, err
	}
	if len(input.Files) == 0 {
		return StartResult{}, errors.New("runtime template is empty")
	}
	workspace := filepath.Join(p.config.WorkspaceRoot, projectID)
	if err := writeTemplate(workspace, input.Files); err != nil {
		return StartResult{}, err
	}
	runtimeID := "tma-r-" + projectID
	port := p.config.PortBase + stablePortOffset(projectID, p.config.PortRange)
	token := runtimeToken(projectID, p.config.Now())
	result, err := p.config.Runner.Run(ctx, p.config.Command,
		"run", "--detach", "--restart", "unless-stopped", "--name", runtimeID,
		"--label", "tma.workbench.runtime=true", "--label", "tma.workbench.project="+projectID,
		"--publish", fmt.Sprintf("127.0.0.1:%d:8888", port),
		"--volume", workspace+":/workspace:rw",
		"--env", "TMA_NOTEBOOK_TOKEN="+token,
		p.config.Image,
	)
	if err != nil {
		return StartResult{}, fmt.Errorf("start R notebook container: %w", err)
	}
	if result.ExitCode != 0 {
		return StartResult{}, fmt.Errorf("start R notebook container failed: %s", strings.TrimSpace(result.Stderr))
	}
	return StartResult{RuntimeID: runtimeID, Status: "running", URL: fmt.Sprintf("http://127.0.0.1:%d%s?token=%s", port, notebookPath(p.config.NotebookPath), token)}, nil
}

func (p *DockerProvisioner) Stop(ctx context.Context, input StopInput) error {
	runtimeID, err := safeRuntimeID(input.RuntimeID)
	if err != nil {
		return err
	}
	result, err := p.config.Runner.Run(ctx, p.config.Command, "rm", "--force", runtimeID)
	if err != nil {
		return fmt.Errorf("stop R notebook container: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("stop R notebook container failed: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func (p *DockerProvisioner) RunCleaning(ctx context.Context, input RunCleaningInput) (RunCleaningResult, error) {
	runtimeID, err := safeRuntimeID(input.RuntimeID)
	if err != nil {
		return RunCleaningResult{}, err
	}
	if strings.TrimSpace(input.ProjectID) != "" && len(input.Files) > 0 {
		projectID, err := safeProjectID(input.ProjectID)
		if err != nil {
			return RunCleaningResult{}, err
		}
		if err := writeTemplate(filepath.Join(p.config.WorkspaceRoot, projectID), input.Files); err != nil {
			return RunCleaningResult{}, err
		}
	}
	script := strings.Join([]string{
		"mkdir -p reports data/processed",
		"Rscript R/clean-data.R > reports/data-cleaning-summary.md 2>&1",
		"status=$?",
		"cat reports/data-cleaning-summary.md",
		"exit $status",
	}, "; ")
	result, err := p.config.Runner.Run(ctx, p.config.Command, "exec", "--workdir", "/workspace", runtimeID, "sh", "-lc", script)
	if err != nil {
		return RunCleaningResult{}, fmt.Errorf("run R data cleaning script: %w", err)
	}
	files := []TemplateFile{}
	if result.ExitCode == 0 {
		processed, readErr := p.config.Runner.Run(ctx, p.config.Command, "exec", "--workdir", "/workspace", runtimeID, "sh", "-lc", "test -f data/processed/followup_clean.csv && cat data/processed/followup_clean.csv")
		if readErr != nil {
			return RunCleaningResult{}, fmt.Errorf("read cleaned followup dataset: %w", readErr)
		}
		if processed.ExitCode == 0 {
			files = append(files, TemplateFile{Path: "data/processed/followup_clean.csv", Content: processed.Stdout})
		}
	}
	return RunCleaningResult{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr, Files: files}, nil
}

func writeTemplate(root string, files []TemplateFile) error {
	for _, file := range files {
		paths := []string{file.Path}
		if file.Path == "data/raw/随访数据.csv" {
			paths = append(paths, "data/raw/followup.csv")
		}
		for _, templatePath := range paths {
			path, err := safeTemplatePath(root, templatePath)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return fmt.Errorf("create runtime workspace directory: %w", err)
			}
			if err := os.WriteFile(path, []byte(file.Content), 0o640); err != nil {
				return fmt.Errorf("write runtime template %s: %w", templatePath, err)
			}
		}
	}
	return nil
}

func safeTemplatePath(root, relative string) (string, error) {
	relative = filepath.ToSlash(strings.Trim(strings.TrimSpace(relative), "/"))
	if relative == "" || relative == "." || strings.Contains(relative, "..") {
		return "", errors.New("runtime template contains an unsafe path")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
		return "", errors.New("runtime template escapes its workspace")
	}
	return cleanPath, nil
}

func safeProjectID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 120 || strings.ContainsAny(value, "/\\") {
		return "", errors.New("invalid runtime project id")
	}
	return value, nil
}

func safeRuntimeID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "/\\ ") {
		return "", errors.New("invalid runtime id")
	}
	return value, nil
}

func stablePortOffset(projectID string, portRange int) int {
	value := 0
	for _, char := range projectID {
		value = (value*31 + int(char)) % portRange
	}
	return value
}

func runtimeToken(projectID string, now time.Time) string {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err == nil {
		return "tma-" + hex.EncodeToString(buffer) + "-runtime"
	}
	return "tma-" + fmt.Sprintf("%x", stablePortOffset(projectID+now.UTC().Format(time.RFC3339Nano), 1_000_000_000)) + "-runtime"
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func notebookPath(value string) string {
	value = "/" + strings.Trim(strings.TrimSpace(value), "/")
	if value == "/" {
		return "/lab"
	}
	return value
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, command string, args ...string) (CommandResult, error) {
	process := exec.CommandContext(ctx, command, args...)
	stdout, stderr := bytes.Buffer{}, bytes.Buffer{}
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			return CommandResult{}, err
		}
	}
	return CommandResult{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}
