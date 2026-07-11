package main

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseServerCLIOptionsRequiresPIDFileForRestart(t *testing.T) {
	_, err := parseServerCLIOptions([]string{"--restart"})
	if err == nil || err.Error() != "--restart requires --pid-file" {
		t.Fatalf("expected pid file validation error, got %v", err)
	}
}

func TestParseServerCLIOptionsParsesRestartFlags(t *testing.T) {
	options, err := parseServerCLIOptions([]string{"--pid-file", ".tma-server.pid", "--restart", "--restart-wait", "20s"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if options.PIDFile != ".tma-server.pid" || !options.Restart || options.RestartWait != 20*time.Second {
		t.Fatalf("unexpected options: %#v", options)
	}
}

func TestParseServerCLIOptionsHelp(t *testing.T) {
	_, err := parseServerCLIOptions([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
}

func TestWritePIDFileCleanupOnlyRemovesOwnedFile(t *testing.T) {
	tempDir := t.TempDir()
	pidFile := filepath.Join(tempDir, "server.pid")

	cleanup, err := writePIDFile(pidFile, 12345)
	if err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if pid, err := readPIDFile(pidFile); err != nil || pid != 12345 {
		t.Fatalf("unexpected pid file content: pid=%d err=%v", pid, err)
	}

	if err := os.WriteFile(pidFile, []byte("99999\n"), 0o644); err != nil {
		t.Fatalf("overwrite pid file: %v", err)
	}
	cleanup()

	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("expected foreign pid file to remain, got %v", err)
	}

	removePIDFileIfOwned(pidFile, 99999)
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected pid file to be removed, got %v", err)
	}
}

func TestMaybeRestartExistingServerIgnoresMissingPIDFile(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	options := serverCLIOptions{
		PIDFile:     filepath.Join(t.TempDir(), "missing.pid"),
		Restart:     true,
		RestartWait: time.Second,
	}
	if err := maybeRestartExistingServer(options, logger); err != nil {
		t.Fatalf("expected missing pid file to be ignored, got %v", err)
	}
}
