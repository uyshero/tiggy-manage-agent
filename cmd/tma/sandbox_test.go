package main

import (
	"errors"
	"strings"
	"testing"
)

func TestRunSandboxDoctorCloudSandboxOK(t *testing.T) {
	restore := stubSandboxDoctor(t, map[string]error{
		"docker info": nil,
		"docker image inspect onlyboxes/test:latest": nil,
	})
	defer restore()

	report := runSandboxDoctor("auto", t.TempDir(), "onlyboxes/test:latest", "docker", true)
	if !report.OK {
		t.Fatalf("expected doctor to pass: %#v", report)
	}
	if report.Runtime != "cloud_sandbox" {
		t.Fatalf("expected auto to resolve to cloud_sandbox, got %q", report.Runtime)
	}
	if len(report.Checks) != 5 {
		t.Fatalf("expected 5 checks, got %#v", report.Checks)
	}
	if report.Checks[4].Name != "sandbox_image" || report.Checks[4].Status != "ok" {
		t.Fatalf("unexpected image check: %#v", report.Checks[4])
	}
}

func TestRunSandboxDoctorImageMissingPullsByDefault(t *testing.T) {
	restore := stubSandboxDoctor(t, map[string]error{
		"docker info":                         nil,
		"docker image inspect missing:latest": errors.New("no such image"),
		"docker pull missing:latest":          nil,
	})
	defer restore()

	report := runSandboxDoctor("cloud_sandbox", t.TempDir(), "missing:latest", "docker", true)
	if !report.OK {
		t.Fatalf("expected doctor to pass after pull: %#v", report)
	}
	last := report.Checks[len(report.Checks)-1]
	if last.Name != "sandbox_image" || last.Status != "ok" || !strings.Contains(last.Message, "pulled image") {
		t.Fatalf("unexpected pulled image check: %#v", last)
	}
}

func TestRunSandboxDoctorImageMissingNoPullFails(t *testing.T) {
	restore := stubSandboxDoctor(t, map[string]error{
		"docker info":                         nil,
		"docker image inspect missing:latest": errors.New("no such image"),
	})
	defer restore()

	report := runSandboxDoctor("cloud_sandbox", t.TempDir(), "missing:latest", "docker", false)
	if report.OK {
		t.Fatalf("expected doctor to fail without pull: %#v", report)
	}
	if report.Pull {
		t.Fatalf("expected pull=false in report: %#v", report)
	}
	last := report.Checks[len(report.Checks)-1]
	if last.Name != "sandbox_image" || last.Status != "failed" || !strings.Contains(last.Message, "not found locally") {
		t.Fatalf("unexpected missing image check: %#v", last)
	}
}

func TestRunSandboxDoctorLocalSystemSkipsDocker(t *testing.T) {
	called := false
	oldRun := runDoctorCommand
	oldLookPath := execLookPath
	runDoctorCommand = func(command string, args ...string) error {
		called = true
		return nil
	}
	execLookPath = func(file string) (string, error) {
		called = true
		return file, nil
	}
	defer func() {
		runDoctorCommand = oldRun
		execLookPath = oldLookPath
	}()

	report := runSandboxDoctor("local_system", "", "", "docker", true)
	if !report.OK {
		t.Fatalf("expected local_system doctor to pass: %#v", report)
	}
	if called {
		t.Fatal("expected local_system doctor to skip docker checks")
	}
}

func stubSandboxDoctor(t *testing.T, results map[string]error) func() {
	t.Helper()
	oldRun := runDoctorCommand
	oldLookPath := execLookPath
	runDoctorCommand = func(command string, args ...string) error {
		key := strings.TrimSpace(command + " " + strings.Join(args, " "))
		if err, ok := results[key]; ok {
			return err
		}
		t.Fatalf("unexpected doctor command %q", key)
		return nil
	}
	execLookPath = func(file string) (string, error) {
		if file != "docker" {
			t.Fatalf("unexpected look path %q", file)
		}
		return "/usr/local/bin/docker", nil
	}
	return func() {
		runDoctorCommand = oldRun
		execLookPath = oldLookPath
	}
}
