package workbenchruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingRunner struct {
	commands [][]string
}

func (r *recordingRunner) Run(_ context.Context, command string, args ...string) (CommandResult, error) {
	r.commands = append(r.commands, append([]string{command}, args...))
	return CommandResult{ExitCode: 0, Stdout: "container-id"}, nil
}

func TestDockerProvisionerWritesIsolatedTemplateAndStartsContainer(t *testing.T) {
	root := t.TempDir()
	recorder := &recordingRunner{}
	provisioner, err := NewDockerProvisioner(DockerConfig{
		Command: "docker", Image: "tma-r-notebook:test", WorkspaceRoot: root,
		PortBase: 19000, PortRange: 100, Runner: recorder,
		Now: func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provisioner.Start(context.Background(), StartInput{
		ProjectID: "wbp_000001",
		Files:     []TemplateFile{{Path: "R/model.R", Content: "fit <- function() {}"}, {Path: "README.md", Content: "demo"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RuntimeID != "tma-r-wbp_000001" || result.Status != "running" || !strings.Contains(result.URL, "127.0.0.1:") {
		t.Fatalf("unexpected runtime result: %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(root, "wbp_000001", "R", "model.R"))
	if err != nil || string(content) != "fit <- function() {}" {
		t.Fatalf("runtime template was not written: %v %q", err, content)
	}
	if len(recorder.commands) != 1 || recorder.commands[0][0] != "docker" {
		t.Fatalf("unexpected docker command: %#v", recorder.commands)
	}
	command := strings.Join(recorder.commands[0], " ")
	if !strings.Contains(command, "--label tma.workbench.project=wbp_000001") || !strings.Contains(command, "tma-r-notebook:test") {
		t.Fatalf("docker command is missing isolation arguments: %s", command)
	}
}

func TestDockerProvisionerStopsOnlySafeRuntimeIDs(t *testing.T) {
	recorder := &recordingRunner{}
	provisioner, err := NewDockerProvisioner(DockerConfig{Command: "docker", Image: "image", WorkspaceRoot: t.TempDir(), PortBase: 19000, Runner: recorder})
	if err != nil {
		t.Fatal(err)
	}
	if err := provisioner.Stop(context.Background(), StopInput{RuntimeID: "tma-r-wbp_000001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := provisioner.Start(context.Background(), StartInput{ProjectID: "../escape", Files: []TemplateFile{{Path: "README.md", Content: "x"}}}); err == nil {
		t.Fatal("unsafe project id was accepted")
	}
	if len(recorder.commands) != 1 || strings.Join(recorder.commands[0], " ") != "docker rm --force tma-r-wbp_000001" {
		t.Fatalf("unexpected stop command: %#v", recorder.commands)
	}
}

func TestDockerProvisionerRunsCleaningScriptInWorkspace(t *testing.T) {
	root := t.TempDir()
	recorder := &recordingRunner{}
	provisioner, err := NewDockerProvisioner(DockerConfig{Command: "docker", Image: "image", WorkspaceRoot: root, PortBase: 19000, Runner: recorder})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provisioner.RunCleaning(context.Background(), RunCleaningInput{
		RuntimeID: "tma-r-wbp_000001",
		ProjectID: "wbp_000001",
		Files:     []TemplateFile{{Path: "R/clean-data.R", Content: "print('clean')"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(recorder.commands) != 2 {
		t.Fatalf("unexpected commands: %#v", recorder.commands)
	}
	command := strings.Join(recorder.commands[0], " ")
	if !strings.Contains(command, "docker exec --workdir /workspace tma-r-wbp_000001") || !strings.Contains(command, "Rscript R/clean-data.R") {
		t.Fatalf("unexpected cleaning command: %s", command)
	}
	readCommand := strings.Join(recorder.commands[1], " ")
	if !strings.Contains(readCommand, "cat data/processed/followup_clean.csv") {
		t.Fatalf("unexpected processed dataset read command: %s", readCommand)
	}
	content, err := os.ReadFile(filepath.Join(root, "wbp_000001", "R", "clean-data.R"))
	if err != nil || string(content) != "print('clean')" {
		t.Fatalf("runtime files were not refreshed: %v %q", err, content)
	}
}

func TestDockerProvisionerFromEnvIsDisabledByDefault(t *testing.T) {
	t.Setenv("TMA_R_NOTEBOOK_RUNTIME_MANAGED", "false")
	provisioner, err := DockerProvisionerFromEnv(nil)
	if err != nil || provisioner != nil {
		t.Fatalf("runtime should be disabled by default: %#v %v", provisioner, err)
	}
}
