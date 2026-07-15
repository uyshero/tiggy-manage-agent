package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	filesystemServerPackage         = "@modelcontextprotocol/server-filesystem@2026.7.10"
	memoryServerPackage             = "@modelcontextprotocol/server-memory@2026.7.4"
	sequentialThinkingServerPackage = "@modelcontextprotocol/server-sequential-thinking@2026.7.4"
	everythingServerPackage         = "@modelcontextprotocol/server-everything@2026.7.4"
	postgresServerPackage           = "@yawlabs/postgres-mcp@0.6.20"
)

func TestExternalMCPCompatibility(t *testing.T) {
	if os.Getenv("TMA_RUN_MCP_COMPATIBILITY") != "1" {
		t.Skip("set TMA_RUN_MCP_COMPATIBILITY=1 to run pinned third-party MCP servers")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Fatal("npx is required for third-party MCP compatibility tests")
	}

	host := NewStdioHost(StdioHostOptions{IdleTimeout: time.Minute, SweepInterval: time.Hour})
	defer host.Close()

	t.Run("official-filesystem", func(t *testing.T) {
		root := t.TempDir()
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		root = canonicalRoot
		markerPath := filepath.Join(root, "compatibility.txt")
		if err := os.WriteFile(markerPath, []byte("tma-mcp-filesystem-compatibility-ok"), 0o600); err != nil {
			t.Fatal(err)
		}
		client := externalNPMClient(filesystemServerPackage)
		client.Args = append(client.Args, root)
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		initialized, tools, err := host.Client("compatibility/filesystem", client).ListTools(ctx)
		if err != nil {
			t.Fatalf("list official filesystem tools: %v", err)
		}
		assertExternalInitialize(t, initialized)
		assertExternalTools(t, tools, "read_text_file", "list_allowed_directories")
		result, err := host.Client("compatibility/filesystem", client).CallTool(ctx, "read_text_file", json.RawMessage(`{"path":`+mustJSON(markerPath)+`}`))
		if err != nil {
			t.Fatalf("call official filesystem read_text_file: %v", err)
		}
		if !strings.Contains(externalResultText(result), "tma-mcp-filesystem-compatibility-ok") {
			t.Fatalf("official filesystem result missing marker: %#v", result)
		}
		t.Logf("server=%s version=%s tools=%d framing=%s", initialized.ServerInfo.Name, initialized.ServerInfo.Version, len(tools), client.StdioFraming)
	})

	t.Run("official-filesystem-initial-roots", func(t *testing.T) {
		root := t.TempDir()
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		root = canonicalRoot
		markerPath := filepath.Join(root, "roots-compatibility.txt")
		if err := os.WriteFile(markerPath, []byte("tma-mcp-filesystem-roots-ok"), 0o600); err != nil {
			t.Fatal(err)
		}
		client := externalNPMClient(filesystemServerPackage)
		client.Roots = []Root{{
			URI:  (&url.URL{Scheme: "file", Path: root}).String(),
			Name: "Compatibility Root",
		}}
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		initialized, tools, err := host.Client("compatibility/filesystem-initial-roots", client).ListTools(ctx)
		if err != nil {
			t.Fatalf("list official filesystem tools with initial roots: %v", err)
		}
		assertExternalInitialize(t, initialized)
		assertExternalTools(t, tools, "read_text_file", "list_allowed_directories")
		var allowed ToolCallResult
		rootDeadline := time.Now().Add(5 * time.Second)
		for {
			allowed, err = host.Client("compatibility/filesystem-initial-roots", client).CallTool(ctx, "list_allowed_directories", json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("list official filesystem root-backed directories: %v", err)
			}
			if strings.Contains(externalResultText(allowed), root) {
				break
			}
			if time.Now().After(rootDeadline) {
				t.Fatalf("official filesystem allowed directories did not converge to initial root %q: %#v", root, allowed)
			}
			time.Sleep(50 * time.Millisecond)
		}
		result, err := host.Client("compatibility/filesystem-initial-roots", client).CallTool(ctx, "read_text_file", json.RawMessage(`{"path":`+mustJSON(markerPath)+`}`))
		if err != nil {
			t.Fatalf("call official filesystem read_text_file with initial roots: %v", err)
		}
		if !strings.Contains(externalResultText(result), "tma-mcp-filesystem-roots-ok") {
			t.Fatalf("official filesystem roots result missing marker: %#v", result)
		}
		t.Logf("server=%s version=%s tools=%d framing=%s roots=initial", initialized.ServerInfo.Name, initialized.ServerInfo.Version, len(tools), client.StdioFraming)
	})

	t.Run("official-memory", func(t *testing.T) {
		client := externalNPMClient(memoryServerPackage)
		client.Env = map[string]string{"MEMORY_FILE_PATH": filepath.Join(t.TempDir(), "memory.jsonl")}
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		initialized, tools, err := host.Client("compatibility/memory", client).ListTools(ctx)
		if err != nil {
			t.Fatalf("list official memory tools: %v", err)
		}
		assertExternalInitialize(t, initialized)
		assertExternalTools(t, tools, "create_entities", "read_graph")
		createArgs := json.RawMessage(`{"entities":[{"name":"compatibility-marker","entityType":"verification","observations":["tma-mcp-memory-compatibility-ok"]}]}`)
		if _, err := host.Client("compatibility/memory", client).CallTool(ctx, "create_entities", createArgs); err != nil {
			t.Fatalf("call official memory create_entities: %v", err)
		}
		result, err := host.Client("compatibility/memory", client).CallTool(ctx, "read_graph", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("call official memory read_graph: %v", err)
		}
		if !strings.Contains(externalResultText(result), "tma-mcp-memory-compatibility-ok") {
			t.Fatalf("official memory result missing marker: %#v", result)
		}
		t.Logf("server=%s version=%s tools=%d framing=%s", initialized.ServerInfo.Name, initialized.ServerInfo.Version, len(tools), client.StdioFraming)
	})

	t.Run("official-sequential-thinking", func(t *testing.T) {
		client := externalNPMClient(sequentialThinkingServerPackage)
		client.Env = map[string]string{"DISABLE_THOUGHT_LOGGING": "true"}
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		initialized, tools, err := host.Client("compatibility/sequential-thinking", client).ListTools(ctx)
		if err != nil {
			t.Fatalf("list official sequential thinking tools: %v", err)
		}
		assertExternalInitialize(t, initialized)
		assertExternalTools(t, tools, "sequentialthinking")
		args := json.RawMessage(`{"thought":"TMA MCP compatibility probe","nextThoughtNeeded":false,"thoughtNumber":1,"totalThoughts":1}`)
		result, err := host.Client("compatibility/sequential-thinking", client).CallTool(ctx, "sequentialthinking", args)
		if err != nil {
			t.Fatalf("call official sequential thinking tool: %v", err)
		}
		var structured struct {
			ThoughtNumber int `json:"thoughtNumber"`
		}
		if err := json.Unmarshal(result.StructuredContent, &structured); err != nil || structured.ThoughtNumber != 1 {
			t.Fatalf("official sequential thinking result is unexpected: %#v", result)
		}
		t.Logf("server=%s version=%s tools=%d framing=%s", initialized.ServerInfo.Name, initialized.ServerInfo.Version, len(tools), client.StdioFraming)
	})

	t.Run("official-everything-streamable-http", func(t *testing.T) {
		endpoint := startExternalStreamableHTTPServer(t, everythingServerPackage)
		httpHost := NewStreamableHTTPHost(StreamableHTTPHostOptions{IdleTimeout: time.Minute, SweepInterval: time.Hour})
		defer httpHost.Close()
		client := Client{Transport: TransportStreamableHTTP, URL: endpoint}
		hosted := httpHost.Client("compatibility/everything-streamable-http", client)
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		initialized, tools, err := hosted.ListTools(ctx)
		if err != nil {
			t.Fatalf("list official everything HTTP tools: %v", err)
		}
		assertExternalInitialize(t, initialized)
		assertExternalTools(t, tools, "echo")
		_, resources, err := hosted.ListResources(ctx)
		if err != nil || len(resources) == 0 {
			t.Fatalf("list official everything HTTP resources: count=%d err=%v", len(resources), err)
		}
		_, prompts, err := hosted.ListPrompts(ctx)
		if err != nil || len(prompts) == 0 {
			t.Fatalf("list official everything HTTP prompts: count=%d err=%v", len(prompts), err)
		}
		_, templates, err := hosted.ListResourceTemplates(ctx)
		if err != nil || len(templates) != 2 {
			t.Fatalf("list official everything HTTP resource templates: count=%d err=%v", len(templates), err)
		}
		promptCompletion, err := hosted.Complete(ctx, CompletionReference{
			Type: CompletionReferencePrompt,
			Name: "completable-prompt",
		}, CompletionArgument{Name: "department", Value: "Eng"}, CompletionContext{})
		if err != nil || len(promptCompletion.Completion.Values) != 1 || promptCompletion.Completion.Values[0] != "Engineering" {
			t.Fatalf("complete official everything HTTP prompt argument: result=%#v err=%v", promptCompletion, err)
		}
		resourceCompletion, err := hosted.Complete(ctx, CompletionReference{
			Type: CompletionReferenceResource,
			URI:  "demo://resource/dynamic/text/{resourceId}",
		}, CompletionArgument{Name: "resourceId", Value: "12"}, CompletionContext{})
		if err != nil || len(resourceCompletion.Completion.Values) != 1 || resourceCompletion.Completion.Values[0] != "12" {
			t.Fatalf("complete official everything HTTP resource argument: result=%#v err=%v", resourceCompletion, err)
		}
		result, err := hosted.CallTool(ctx, "echo", json.RawMessage(`{"message":"tma-mcp-streamable-http-ok"}`))
		if err != nil {
			t.Fatalf("call official everything HTTP echo: %v", err)
		}
		if !strings.Contains(externalResultText(result), "tma-mcp-streamable-http-ok") {
			t.Fatalf("official everything HTTP result missing marker: %#v", result)
		}
		t.Logf("server=%s version=%s tools=%d resources=%d prompts=%d templates=%d completions=2 transport=streamable_http", initialized.ServerInfo.Name, initialized.ServerInfo.Version, len(tools), len(resources), len(prompts), len(templates))
	})

	t.Run("yawlabs-postgres-read-only", func(t *testing.T) {
		fixture := setupExternalPostgresFixture(t)
		databaseURLEnv := "TMA_MCP_COMPAT_POSTGRES_DATABASE_URL"
		t.Setenv(databaseURLEnv, fixture.DatabaseURL)
		resolvedEnv, err := ResolveEnv(ServerConfig{
			Identifier: "postgres",
			Env: map[string]EnvValue{
				"ALLOW_WRITES":                   LiteralEnv("0"),
				"DATABASE_URL":                   SecretRef("env:" + databaseURLEnv),
				"POSTGRES_CONNECTION_TIMEOUT_MS": LiteralEnv("5000"),
				"POSTGRES_MAX_ROWS":              LiteralEnv("2"),
				"POSTGRES_POOL_MAX":              LiteralEnv("1"),
				"POSTGRES_STATEMENT_TIMEOUT_MS":  LiteralEnv("5000"),
			},
		})
		if err != nil {
			t.Fatalf("resolve PostgreSQL MCP environment references: %v", err)
		}
		client := externalNPMClient(postgresServerPackage)
		client.Env = resolvedEnv
		postgresHost := NewStdioHost(StdioHostOptions{IdleTimeout: time.Minute, SweepInterval: time.Hour})
		defer postgresHost.Close()
		hosted := postgresHost.Client("compatibility/yawlabs-postgres-read-only", client)
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()

		initialized, tools, err := hosted.ListTools(ctx)
		if err != nil {
			t.Fatalf("list YawLabs PostgreSQL tools: %v", err)
		}
		assertExternalInitialize(t, initialized)
		assertExternalTools(t, tools, "pg_readonly", "pg_query", "pg_list_tables", "pg_describe_table")
		if len(tools) != 21 {
			t.Fatalf("YawLabs PostgreSQL tool count changed: got %d, want 21", len(tools))
		}

		result, err := hosted.CallTool(ctx, "pg_readonly", json.RawMessage(`{"sql":"SELECT id, marker FROM public.compatibility_markers ORDER BY id"}`))
		if err != nil {
			t.Fatalf("query PostgreSQL compatibility markers: %v", err)
		}
		if result.IsError {
			t.Fatalf("PostgreSQL read-only query returned a tool error: %s", externalResultText(result))
		}
		var bounded struct {
			Rows      []map[string]any `json:"rows"`
			Truncated bool             `json:"truncated"`
		}
		if err := json.Unmarshal([]byte(externalResultText(result)), &bounded); err != nil {
			t.Fatalf("decode PostgreSQL bounded result: %v", err)
		}
		if len(bounded.Rows) != 2 || !bounded.Truncated || bounded.Rows[0]["marker"] != "tma-mcp-postgres-1" || bounded.Rows[1]["marker"] != "tma-mcp-postgres-2" {
			t.Fatalf("unexpected PostgreSQL bounded result: %#v", bounded)
		}

		parameterized, err := hosted.CallTool(ctx, "pg_readonly", json.RawMessage(`{"sql":"SELECT marker FROM public.compatibility_markers WHERE id = $1","params":[3]}`))
		if err != nil || parameterized.IsError || !strings.Contains(externalResultText(parameterized), "tma-mcp-postgres-3") {
			t.Fatalf("PostgreSQL parameterized query failed: result=%#v err=%v", parameterized, err)
		}

		writeProbe := json.RawMessage(`{"sql":"INSERT INTO public.compatibility_markers(id, marker) VALUES (4, 'forbidden')"}`)
		for _, testCase := range []struct {
			name      string
			tool      string
			arguments json.RawMessage
		}{
			{name: "pg-query-write", tool: "pg_query", arguments: writeProbe},
			{name: "pg-readonly-write", tool: "pg_readonly", arguments: writeProbe},
			{name: "stacked-query", tool: "pg_query", arguments: json.RawMessage(`{"sql":"SELECT 1; DELETE FROM public.compatibility_markers"}`)},
		} {
			rejected, callErr := hosted.CallTool(ctx, testCase.tool, testCase.arguments)
			if callErr != nil {
				t.Fatalf("PostgreSQL %s returned protocol error: %v", testCase.name, callErr)
			}
			if !rejected.IsError {
				t.Fatalf("PostgreSQL %s was not rejected: %#v", testCase.name, rejected)
			}
		}

		countResult, err := hosted.CallTool(ctx, "pg_readonly", json.RawMessage(`{"sql":"SELECT count(*)::int AS count FROM public.compatibility_markers"}`))
		if err != nil || countResult.IsError || !strings.Contains(externalResultText(countResult), `"count": 3`) {
			t.Fatalf("PostgreSQL row count changed after rejected writes: result=%#v err=%v", countResult, err)
		}
		t.Logf("server=%s version=%s tools=%d framing=%s rows_capped=2 secret=env_ref writes=rejected", initialized.ServerInfo.Name, initialized.ServerInfo.Version, len(tools), client.StdioFraming)
	})
}

type externalPostgresFixture struct {
	DatabaseURL string
}

func setupExternalPostgresFixture(t *testing.T) externalPostgresFixture {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required for the PostgreSQL MCP compatibility test")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve compatibility test working directory: %v", err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join(workingDirectory, "..", ".."))
	if err != nil {
		t.Fatalf("resolve compatibility test repository root: %v", err)
	}
	composeFile := filepath.Join(repositoryRoot, "docker-compose.yml")
	if _, err := os.Stat(composeFile); err != nil {
		t.Fatalf("locate compatibility test compose file: %v", err)
	}
	if output, err := exec.Command("docker", "compose", "-f", composeFile, "--project-directory", repositoryRoot, "up", "-d", "postgres").CombinedOutput(); err != nil {
		t.Fatalf("start compatibility PostgreSQL: %v: %s", err, strings.TrimSpace(string(output)))
	}

	suffix := fmt.Sprintf("%d_%s", os.Getpid(), externalRandomHex(t, 4))
	database := "tma_mcp_compat_" + suffix
	role := "tma_mcp_reader_" + suffix
	password := "mcp_" + externalRandomHex(t, 16)
	t.Cleanup(func() {
		cleanupSQL := fmt.Sprintf(`
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();
DROP DATABASE IF EXISTS %s;
DROP ROLE IF EXISTS %s;
`, database, database, role)
		if err := runExternalPostgresSQL(composeFile, repositoryRoot, "postgres", cleanupSQL); err != nil {
			t.Logf("cleanup PostgreSQL MCP compatibility fixture: %v", err)
		}
	})

	adminSQL := fmt.Sprintf(`
CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE DATABASE %s OWNER tma;
REVOKE CONNECT ON DATABASE %s FROM PUBLIC;
GRANT CONNECT ON DATABASE %s TO %s;
ALTER ROLE %s IN DATABASE %s SET default_transaction_read_only = on;
`, role, password, database, database, database, role, role, database)
	if err := runExternalPostgresSQL(composeFile, repositoryRoot, "postgres", adminSQL); err != nil {
		t.Fatalf("create PostgreSQL MCP compatibility database: %v", err)
	}
	fixtureSQL := fmt.Sprintf(`
CREATE TABLE public.compatibility_markers (id integer PRIMARY KEY, marker text NOT NULL);
INSERT INTO public.compatibility_markers(id, marker) VALUES
  (1, 'tma-mcp-postgres-1'),
  (2, 'tma-mcp-postgres-2'),
  (3, 'tma-mcp-postgres-3');
GRANT USAGE ON SCHEMA public TO %s;
GRANT SELECT ON public.compatibility_markers TO %s;
`, role, role)
	if err := runExternalPostgresSQL(composeFile, repositoryRoot, database, fixtureSQL); err != nil {
		t.Fatalf("seed PostgreSQL MCP compatibility database: %v", err)
	}

	return externalPostgresFixture{
		DatabaseURL: fmt.Sprintf("postgresql://%s:%s@127.0.0.1:5432/%s?sslmode=disable", role, password, database),
	}
}

func runExternalPostgresSQL(composeFile string, repositoryRoot string, database string, sql string) error {
	cmd := exec.Command("docker", "compose", "-f", composeFile, "--project-directory", repositoryRoot, "exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-U", "tma", "-d", database)
	cmd.Stdin = strings.NewReader(sql)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

func externalRandomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate compatibility fixture identifier: %v", err)
	}
	return hex.EncodeToString(value)
}

func startExternalStreamableHTTPServer(t *testing.T, packageName string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve external MCP HTTP port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release external MCP HTTP port: %v", err)
	}

	processCtx, cancelProcess := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processCtx, "npx", "-y", packageName, "streamableHttp")
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		cancelProcess()
		t.Fatalf("start external MCP HTTP package %s: %v", packageName, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		cancelProcess()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
	})

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	probeClient := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, requestErr := probeClient.Get(endpoint)
		if requestErr == nil {
			_ = response.Body.Close()
			return endpoint
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("external MCP HTTP package %s did not become ready: stdout=%q stderr=%q", packageName, stdout.String(), stderr.String())
	return ""
}

func externalNPMClient(packageName string) Client {
	return Client{
		Transport:    TransportStdio,
		StdioFraming: StdioFramingJSONLines,
		Command:      "npx",
		Args:         []string{"-y", packageName},
	}
}

func assertExternalTools(t *testing.T, tools []ToolDefinition, expected ...string) {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		if !json.Valid(tool.InputSchema) {
			t.Fatalf("tool %q returned an invalid input schema: %s", tool.Name, tool.InputSchema)
		}
	}
	for _, name := range expected {
		if !slices.Contains(names, name) {
			t.Fatalf("expected tool %q, got %v", name, names)
		}
	}
}

func assertExternalInitialize(t *testing.T, initialized InitializeResult) {
	t.Helper()
	if initialized.ProtocolVersion != protocolVersion {
		t.Fatalf("external server negotiated protocol version %q, want %q", initialized.ProtocolVersion, protocolVersion)
	}
	if strings.TrimSpace(initialized.ServerInfo.Name) == "" || strings.TrimSpace(initialized.ServerInfo.Version) == "" {
		t.Fatalf("external server returned incomplete server info: %#v", initialized.ServerInfo)
	}
}

func externalResultText(result ToolCallResult) string {
	var builder strings.Builder
	for _, item := range result.Content {
		builder.WriteString(item.Text)
	}
	return builder.String()
}

func mustJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
