package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type defaultLLMModelStoreStub struct {
	models      []managedagents.LLMModel
	listErr     error
	upserted    managedagents.UpsertLLMModelInput
	upsertCalls int
}

func (s *defaultLLMModelStoreStub) ListLLMModels(string) ([]managedagents.LLMModel, error) {
	return s.models, s.listErr
}

func (s *defaultLLMModelStoreStub) UpsertLLMModel(input managedagents.UpsertLLMModelInput) (managedagents.LLMModel, error) {
	s.upsertCalls++
	s.upserted = input
	return managedagents.LLMModel{
		ProviderID:          input.ProviderID,
		Model:               input.Model,
		ContextWindowTokens: input.ContextWindowTokens,
	}, nil
}

type databaseTenantIsolationValidatorStub struct {
	err   error
	calls int
}

func (s *databaseTenantIsolationValidatorStub) ValidateDatabaseTenantIsolation(context.Context) error {
	s.calls++
	return s.err
}

func TestParseServerCLIOptionsRequiresPIDFileForRestart(t *testing.T) {
	_, err := parseServerCLIOptions([]string{"--restart"})
	if err == nil || err.Error() != "--restart requires --pid-file" {
		t.Fatalf("expected pid file validation error, got %v", err)
	}
}

func TestBuildMCPHTTPBaseClientRejectsInvalidCABundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write invalid CA bundle: %v", err)
	}
	if _, err := buildMCPHTTPBaseClient(path); err == nil || !strings.Contains(err.Error(), "no valid PEM certificates") {
		t.Fatalf("expected invalid MCP CA bundle error, got %v", err)
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

func TestValidateProductionDatabaseTenantIsolation(t *testing.T) {
	validator := &databaseTenantIsolationValidatorStub{}
	if err := validateProductionDatabaseTenantIsolation("development", validator); err != nil || validator.calls != 0 {
		t.Fatalf("development should not require database RLS validation: calls=%d err=%v", validator.calls, err)
	}
	if err := validateProductionDatabaseTenantIsolation("production", struct{}{}); err == nil {
		t.Fatal("expected unsupported production store rejection")
	}
	validator.err = errors.New("rls unavailable")
	if err := validateProductionDatabaseTenantIsolation("prod", validator); err == nil || err.Error() != "rls unavailable" || validator.calls != 1 {
		t.Fatalf("expected production RLS validation failure: calls=%d err=%v", validator.calls, err)
	}
	validator.err = nil
	if err := validateProductionDatabaseTenantIsolation("production", validator); err != nil || validator.calls != 2 {
		t.Fatalf("expected production RLS validation success: calls=%d err=%v", validator.calls, err)
	}
}

func TestEnsureDefaultLLMModelPreservesExistingConfiguration(t *testing.T) {
	store := &defaultLLMModelStoreStub{models: []managedagents.LLMModel{{
		ProviderID:          "volcengine-agent-plan",
		Model:               "doubao-seed-2.0-pro",
		ContextWindowTokens: 256000,
		CapabilityType:      managedagents.LLMModelCapabilityTextImage,
	}}}

	model, err := ensureDefaultLLMModel(store, managedagents.UpsertLLMModelInput{
		ProviderID:          "volcengine-agent-plan",
		Model:               "doubao-seed-2.0-pro",
		ContextWindowTokens: 128000,
	})
	if err != nil {
		t.Fatalf("ensure default model: %v", err)
	}
	if model.ContextWindowTokens != 256000 {
		t.Fatalf("expected existing context window to be preserved, got %d", model.ContextWindowTokens)
	}
	if store.upsertCalls != 0 {
		t.Fatalf("expected existing model not to be upserted, got %d calls", store.upsertCalls)
	}
}

func TestEnsureDefaultLLMModelCreatesMissingModel(t *testing.T) {
	store := &defaultLLMModelStoreStub{}
	input := managedagents.UpsertLLMModelInput{
		ProviderID:          "volcengine-agent-plan",
		Model:               "doubao-seed-2.0-pro",
		ContextWindowTokens: 128000,
	}

	model, err := ensureDefaultLLMModel(store, input)
	if err != nil {
		t.Fatalf("ensure default model: %v", err)
	}
	if store.upsertCalls != 1 || store.upserted != input {
		t.Fatalf("expected missing model to be created once, calls=%d input=%#v", store.upsertCalls, store.upserted)
	}
	if model.ContextWindowTokens != 128000 {
		t.Fatalf("expected default context window, got %d", model.ContextWindowTokens)
	}
}
