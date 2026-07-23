package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/sdk/tma"
)

const defaultBaseURL = "http://localhost:8080"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "tma: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	baseURL := os.Getenv("TMA_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	authToken := os.Getenv("TMA_AUTH_TOKEN")
	if authToken == "" {
		authToken = os.Getenv("TMA_WORKER_CONTROL_TOKEN")
	}

	global := flag.NewFlagSet("tma", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	global.StringVar(&baseURL, "base-url", baseURL, "TMA API base URL")
	global.StringVar(&authToken, "auth-token", authToken, "API bearer token")

	if err := global.Parse(args); err != nil {
		return err
	}

	remaining := global.Args()
	if len(remaining) == 0 {
		return usageError()
	}

	client := &apiClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		authToken: strings.TrimSpace(authToken),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
		streamHTTP:  &http.Client{}, // SSE 是长连接，不能设置全局 Client.Timeout。
		credentials: keyringCredentialStore{},
	}
	if client.authToken == "" {
		client.tokenSource = (&keyringTokenSource{baseURL: client.baseURL, store: client.credentials, httpClient: client.http}).Token
	}

	switch remaining[0] {
	case "auth":
		return commandAuth(client, remaining[1:])
	case "health":
		return commandHealth(client, remaining[1:])
	case "sandbox":
		return commandSandbox(remaining[1:])
	case "web":
		return commandWeb(remaining[1:])
	case "provider":
		return commandProvider(client, remaining[1:])
	case "model":
		return commandModel(client, remaining[1:])
	case "object":
		return commandObject(client, remaining[1:])
	case "worker":
		return commandWorker(client, remaining[1:])
	case "work":
		return commandWork(client, remaining[1:])
	case "agent":
		return commandAgent(client, remaining[1:])
	case "env":
		return commandEnvironment(client, remaining[1:])
	case "session":
		return commandSession(client, remaining[1:])
	case "usage":
		return commandUsage(client, remaining[1:])
	case "event":
		return commandEvent(client, remaining[1:])
	case "trace":
		return commandTrace(client, remaining[1:])
	case "observability":
		return commandObservability(client, remaining[1:])
	case "skill":
		return commandSkill(client, remaining[1:])
	case "marketplace":
		return commandMarketplace(client, remaining[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func commandHealth(client *apiClient, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("health does not accept arguments")
	}

	var response any
	if err := client.do(http.MethodGet, "/health", nil, &response); err != nil {
		return err
	}

	return printJSON(response)
}

func commandProvider(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("provider command requires a subcommand")
	}

	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("provider list does not accept arguments")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		providers, err := sdk.LLM.ListProviders(context.Background())
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"providers": providers})
	case "get":
		flags := flag.NewFlagSet("provider get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "provider id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("provider get requires --id")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		provider, err := sdk.LLM.GetProvider(context.Background(), id)
		if err != nil {
			return err
		}
		return printJSON(provider)
	case "create":
		flags := flag.NewFlagSet("provider create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		var providerType string
		var baseURL string
		var apiKeyEnv string
		var disabled bool
		flags.StringVar(&id, "id", "", "provider id")
		flags.StringVar(&providerType, "type", "", "provider protocol type")
		flags.StringVar(&baseURL, "base-url", "", "provider base URL")
		flags.StringVar(&apiKeyEnv, "api-key-env", "", "environment variable name that stores the API key")
		flags.BoolVar(&disabled, "disabled", false, "create provider as disabled")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" || providerType == "" {
			return fmt.Errorf("provider create requires --id and --type")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		enabled := !disabled
		provider, err := sdk.LLM.CreateProvider(context.Background(), tma.CreateLLMProviderRequest{
			ID: id, ProviderType: providerType, BaseURL: baseURL, APIKeyEnv: apiKeyEnv, Enabled: &enabled,
		})
		if err != nil {
			return err
		}
		return printJSON(provider)
	case "update":
		flags := flag.NewFlagSet("provider update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		var providerType string
		var baseURL string
		var apiKeyEnv string
		flags.StringVar(&id, "id", "", "provider id")
		flags.StringVar(&providerType, "type", "", "provider protocol type")
		flags.StringVar(&baseURL, "base-url", "", "provider base URL")
		flags.StringVar(&apiKeyEnv, "api-key-env", "", "environment variable name that stores the API key")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("provider update requires --id")
		}

		request := tma.UpdateLLMProviderRequest{}
		if flagWasPassed(flags, "type") {
			request.ProviderType = &providerType
		}
		if flagWasPassed(flags, "base-url") {
			request.BaseURL = &baseURL
		}
		if flagWasPassed(flags, "api-key-env") {
			request.APIKeyEnv = &apiKeyEnv
		}
		if request.ProviderType == nil && request.BaseURL == nil && request.APIKeyEnv == nil {
			return fmt.Errorf("provider update requires at least one field flag")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		current, err := sdk.LLM.GetProvider(context.Background(), id)
		if err != nil {
			return err
		}
		provider, err := sdk.LLM.UpdateProvider(context.Background(), id, current.Revision, request)
		if err != nil {
			return err
		}
		return printJSON(provider)
	case "enable", "disable":
		flags := flag.NewFlagSet("provider "+args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "provider id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("provider %s requires --id", args[0])
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		current, err := sdk.LLM.GetProvider(context.Background(), id)
		if err != nil {
			return err
		}
		provider, err := sdk.LLM.SetProviderEnabled(context.Background(), id, current.Revision, args[0] == "enable")
		if err != nil {
			return err
		}
		return printJSON(provider)
	default:
		return fmt.Errorf("unknown provider subcommand %q", args[0])
	}
}

func commandModel(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("model command requires a subcommand")
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("model list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var providerID string
		flags.StringVar(&providerID, "provider", "", "provider id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		models, err := sdk.LLM.ListModels(context.Background(), providerID)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"models": models})
	case "upsert":
		flags := flag.NewFlagSet("model upsert", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var providerID string
		var model string
		var contextWindow int
		flags.StringVar(&providerID, "provider", "", "provider id")
		flags.StringVar(&model, "model", "", "model id")
		flags.IntVar(&contextWindow, "context-window", 0, "model total context window tokens")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if providerID == "" || model == "" {
			return fmt.Errorf("model upsert requires --provider and --model")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		models, err := sdk.LLM.ListModels(context.Background(), providerID)
		if err != nil {
			return err
		}
		request := tma.PutLLMModelRequest{ProviderID: providerID, Model: model, ContextWindowTokens: contextWindow}
		for _, existing := range models {
			if existing.Model != model {
				continue
			}
			if request.ContextWindowTokens == 0 {
				request.ContextWindowTokens = existing.ContextWindowTokens
			}
			request.CapabilityType = existing.CapabilityType
			isDefaultVision := existing.IsDefaultVision
			request.IsDefaultVision = &isDefaultVision
			updated, updateErr := sdk.LLM.UpdateModel(context.Background(), existing.Revision, request)
			if updateErr != nil {
				return updateErr
			}
			return printJSON(updated)
		}
		created, err := sdk.LLM.CreateModel(context.Background(), request)
		if err != nil {
			return err
		}
		return printJSON(created)
	default:
		return fmt.Errorf("unknown model subcommand %q", args[0])
	}
}

func commandAgent(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent command requires a subcommand")
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("agent create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var name string
		var llmProvider string
		var llmModel string
		var model string
		var system string
		var environmentID string
		flags.StringVar(&name, "name", "", "agent name")
		flags.StringVar(&llmProvider, "llm-provider", "", "llm provider id")
		flags.StringVar(&llmModel, "llm-model", "", "llm model id")
		flags.StringVar(&model, "model", "", "model id")
		flags.StringVar(&system, "system", "", "system prompt")
		flags.StringVar(&environmentID, "env", "", "bound environment id")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if llmModel == "" {
			llmModel = model
		}
		if name == "" || llmModel == "" || environmentID == "" {
			return fmt.Errorf("agent create requires --name, --env, and --model (or --llm-model)")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		agent, err := sdk.Agents.Create(context.Background(), tma.CreateAgentRequest{
			Name: name, EnvironmentID: environmentID, LLMProvider: llmProvider, LLMModel: llmModel, Model: model, System: system,
		})
		if err != nil {
			return err
		}
		return printJSON(agent)
	case "get":
		flags := flag.NewFlagSet("agent get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "agent id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("agent get requires --id")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		agent, err := sdk.Agents.Get(context.Background(), id)
		if err != nil {
			return err
		}
		return printJSON(agent)
	case "export":
		flags := flag.NewFlagSet("agent export", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var id string
		var outputPath string
		flags.StringVar(&id, "id", "", "agent id")
		flags.StringVar(&outputPath, "output", "", "write portable agent JSON to file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("agent export requires --id")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		document, err := sdk.Agents.Export(context.Background(), id)
		if err != nil {
			return err
		}
		if strings.TrimSpace(outputPath) != "" {
			return writeJSONFile(outputPath, document)
		}
		return printJSON(document)
	case "import":
		flags := flag.NewFlagSet("agent import", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var inputPath string
		var name string
		var workspaceID string
		flags.StringVar(&inputPath, "file", "", "portable agent JSON file")
		flags.StringVar(&name, "name", "", "override imported agent name")
		flags.StringVar(&workspaceID, "workspace", "", "target workspace id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(inputPath) == "" {
			return fmt.Errorf("agent import requires --file")
		}
		raw, err := os.ReadFile(inputPath)
		if err != nil {
			return fmt.Errorf("read agent import file: %w", err)
		}
		var document tma.AgentExportDocument
		if err := json.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("decode agent import file: %w", err)
		}
		if document.Format == "" || document.SchemaVersion <= 0 || document.Agent.Name == "" {
			return fmt.Errorf("agent import file is not a portable agent document")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		agent, err := sdk.Agents.Import(context.Background(), tma.AgentImportRequest{
			Format: document.Format, SchemaVersion: document.SchemaVersion,
			WorkspaceID: workspaceID, Name: name, Agent: document.Agent,
		})
		if err != nil {
			return err
		}
		return printJSON(agent)
	case "config":
		return commandAgentConfig(client, args[1:])
	default:
		return fmt.Errorf("unknown agent subcommand %q", args[0])
	}
}

func commandAgentConfig(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent config command requires a subcommand")
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("agent config list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var agentID string
		flags.StringVar(&agentID, "agent", "", "agent id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if agentID == "" {
			return fmt.Errorf("agent config list requires --agent")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		versions, err := sdk.Agents.ListConfigVersions(context.Background(), agentID)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"config_versions": versions})
	case "update":
		flags := flag.NewFlagSet("agent config update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var agentID string
		var llmProvider string
		var llmModel string
		var model string
		var system string
		var toolsConfig string
		var mcpConfig string
		flags.StringVar(&agentID, "agent", "", "agent id")
		flags.StringVar(&llmProvider, "llm-provider", "", "llm provider id")
		flags.StringVar(&llmModel, "llm-model", "", "llm model id")
		flags.StringVar(&model, "model", "", "model id")
		flags.StringVar(&system, "system", "", "system prompt")
		flags.StringVar(&toolsConfig, "tools", "", "tools config JSON")
		flags.StringVar(&mcpConfig, "mcp", "", "mcp config JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if agentID == "" {
			return fmt.Errorf("agent config update requires --agent")
		}

		request := tma.CreateAgentConfigVersionRequest{}
		if flagWasPassed(flags, "llm-provider") {
			request.LLMProvider = &llmProvider
		}
		if flagWasPassed(flags, "llm-model") {
			request.LLMModel = &llmModel
		}
		if flagWasPassed(flags, "model") {
			request.Model = &model
		}
		if flagWasPassed(flags, "system") {
			request.System = &system
		}
		if flagWasPassed(flags, "tools") {
			rawTools, err := parseOptionalJSONObjectFlag(toolsConfig, "tools")
			if err != nil {
				return err
			}
			if rawTools == nil {
				return fmt.Errorf("agent config update --tools requires a JSON object")
			}
			request.Tools = &rawTools
		}
		if flagWasPassed(flags, "mcp") {
			rawMCP, err := parseOptionalJSONObjectFlag(mcpConfig, "mcp")
			if err != nil {
				return err
			}
			if rawMCP == nil {
				return fmt.Errorf("agent config update --mcp requires a JSON object")
			}
			request.MCP = &rawMCP
		}
		if request.LLMProvider == nil && request.LLMModel == nil && request.Model == nil && request.System == nil && request.Tools == nil && request.MCP == nil {
			return fmt.Errorf("agent config update requires at least one field flag")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		agent, err := sdk.Agents.CreateConfigVersion(context.Background(), agentID, request)
		if err != nil {
			return err
		}
		return printJSON(agent)
	default:
		return fmt.Errorf("unknown agent config subcommand %q", args[0])
	}
}

func commandEnvironment(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("env command requires a subcommand")
	}

	switch args[0] {
	case "list":
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		environments, err := sdk.Environments.List(context.Background())
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"environments": environments})
	case "create":
		flags := flag.NewFlagSet("env create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var name string
		var config string
		flags.StringVar(&name, "name", "", "environment name")
		flags.StringVar(&config, "config", `{"type":"cloud","networking":{"type":"limited","allowed_hosts":[]}}`, "environment config JSON")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("env create requires --name")
		}

		var rawConfig json.RawMessage
		if err := json.Unmarshal([]byte(config), &rawConfig); err != nil {
			return fmt.Errorf("invalid --config JSON: %w", err)
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		environment, err := sdk.Environments.Create(context.Background(), tma.CreateEnvironmentRequest{Name: name, Config: rawConfig})
		if err != nil {
			return err
		}
		return printJSON(environment)
	default:
		return fmt.Errorf("unknown env subcommand %q", args[0])
	}
}

func commandSession(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("session command requires a subcommand")
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("session create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var agentID string
		var environmentID string
		var title string
		flags.StringVar(&agentID, "agent", "", "agent id")
		flags.StringVar(&environmentID, "env", "", "environment id")
		flags.StringVar(&title, "title", "", "session title")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if agentID == "" {
			return fmt.Errorf("session create requires --agent")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.Create(context.Background(), tma.CreateSessionRequest{
			AgentID: agentID, EnvironmentID: environmentID, Title: title,
		})
		if err != nil {
			return err
		}

		return printJSON(response)
	case "get":
		flags := flag.NewFlagSet("session get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session get requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.Get(context.Background(), sessionID)
		if err != nil {
			return err
		}

		return printJSON(response)
	case "archive":
		flags := flag.NewFlagSet("session archive", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session archive requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.Archive(context.Background(), sessionID)
		if err != nil {
			return err
		}

		return printJSON(response)
	case "delete":
		flags := flag.NewFlagSet("session delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session delete requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		if err := sdk.Sessions.Delete(context.Background(), sessionID); err != nil {
			return err
		}

		fmt.Printf("deleted session %s\n", sessionID)
		return nil
	case "compare":
		flags := flag.NewFlagSet("session compare", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var leftSessionID string
		var rightSessionID string
		flags.StringVar(&leftSessionID, "left", "", "left session id")
		flags.StringVar(&rightSessionID, "right", "", "right session id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if leftSessionID == "" || rightSessionID == "" {
			return fmt.Errorf("session compare requires --left and --right")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		comparison, err := sdk.Sessions.Compare(context.Background(), leftSessionID, rightSessionID)
		if err != nil {
			return err
		}
		return printJSON(comparison)
	case "runtime":
		return commandSessionRuntime(client, args[1:])
	case "config":
		return commandSessionConfig(client, args[1:])
	case "intervention":
		return commandSessionIntervention(client, args[1:])
	case "attach":
		return commandSessionAttach(client, args[1:])
	case "summary":
		return commandSessionSummary(client, args[1:])
	case "artifact":
		return commandSessionArtifact(client, args[1:])
	default:
		return fmt.Errorf("unknown session subcommand %q", args[0])
	}
}

func commandSessionConfig(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("session config command requires a subcommand")
	}
	switch args[0] {
	case "upgrade":
		flags := flag.NewFlagSet("session config upgrade", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var toCurrent bool
		var toVersion int
		var updatedBy string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.BoolVar(&toCurrent, "to-current", false, "upgrade session to agent current config version")
		flags.IntVar(&toVersion, "to-version", 0, "upgrade session to an exact agent config version")
		flags.StringVar(&updatedBy, "updated-by", "", "updater id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session config upgrade requires --session")
		}
		if toCurrent == (toVersion > 0) {
			return fmt.Errorf("session config upgrade requires exactly one of --to-current or --to-version")
		}
		request := tma.UpgradeSessionConfigRequest{UpdatedBy: updatedBy}
		if toCurrent {
			request.ToCurrent = &toCurrent
		} else {
			request.ToVersion = toVersion
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.UpgradeConfig(context.Background(), sessionID, request)
		if err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown session config subcommand %q", args[0])
	}
}

func commandSessionRuntime(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("session runtime command requires a subcommand")
	}

	switch args[0] {
	case "get":
		flags := flag.NewFlagSet("session runtime get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session runtime get requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.Get(context.Background(), sessionID)
		if err != nil {
			return err
		}
		return printRawJSON(defaultJSONObject(response.RuntimeSettings))
	case "update":
		flags := flag.NewFlagSet("session runtime update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var interventionMode string
		var cloudSandboxRoot string
		var cloudSandboxImage string
		var cloudSandboxAllowNetwork bool
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&interventionMode, "intervention-mode", "", "request_approval | approve_for_me | full_access")
		flags.StringVar(&cloudSandboxRoot, "cloud-sandbox-root", "", "isolated workspace base path for cloud_sandbox runtime")
		flags.StringVar(&cloudSandboxImage, "cloud-sandbox-image", "", "Onlyboxes image for cloud_sandbox runtime")
		flags.BoolVar(&cloudSandboxAllowNetwork, "cloud-sandbox-allow-network", false, "allow full outbound network access for cloud_sandbox")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session runtime update requires --session")
		}
		if interventionMode == "" && cloudSandboxRoot == "" && cloudSandboxImage == "" && !flagWasPassed(flags, "cloud-sandbox-allow-network") {
			return fmt.Errorf("session runtime update requires at least one runtime setting flag")
		}

		request := tma.UpdateSessionRuntimeSettingsRequest{}
		if interventionMode != "" {
			mode, ok := normalizeInterventionModeArg(interventionMode)
			if !ok {
				return fmt.Errorf("unsupported intervention mode %q", interventionMode)
			}
			request.InterventionMode = &mode
		}
		if cloudSandboxRoot != "" {
			request.CloudSandboxRoot = &cloudSandboxRoot
		}
		if cloudSandboxImage != "" {
			request.CloudSandboxImage = &cloudSandboxImage
		}
		if flagWasPassed(flags, "cloud-sandbox-allow-network") {
			request.AllowNetwork = &cloudSandboxAllowNetwork
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		current, err := sdk.Sessions.Get(context.Background(), sessionID)
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.UpdateRuntimeSettings(context.Background(), sessionID, current.RuntimeSettingsRevision, request)
		if err != nil {
			return err
		}
		return printRawJSON(defaultJSONObject(response.RuntimeSettings))
	default:
		return fmt.Errorf("unknown session runtime subcommand %q", args[0])
	}
}

func commandSessionIntervention(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("session intervention command requires a subcommand")
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("session intervention list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var status string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&status, "status", "", "pending | approved | rejected")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session intervention list requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		interventions, err := sdk.Interventions.List(context.Background(), sessionID, status)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"interventions": interventions})
	case "approve", "reject":
		flags := flag.NewFlagSet("session intervention "+args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var turnID string
		var callID string
		var reason string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.StringVar(&callID, "call", "", "tool call id")
		flags.StringVar(&reason, "reason", "", "decision reason")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" || turnID == "" || callID == "" {
			return fmt.Errorf("session intervention %s requires --session, --turn, and --call", args[0])
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Interventions.DecideResult(context.Background(), sessionID, turnID, callID, args[0], reason)
		if err != nil {
			return err
		}
		return printJSON(response)
	case "reconcile":
		flags := flag.NewFlagSet("session intervention reconcile", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var turnID string
		var callID string
		var outcome string
		var summary string
		var evidence string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.StringVar(&callID, "call", "", "reconciliation intervention id")
		flags.StringVar(&outcome, "outcome", "", "executed | not_executed | compensated")
		flags.StringVar(&summary, "summary", "", "external-state verification summary")
		flags.StringVar(&evidence, "evidence", "", "ticket, log, transaction, or artifact reference")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" || turnID == "" || callID == "" || summary == "" {
			return fmt.Errorf("session intervention reconcile requires --session, --turn, --call, --outcome, and --summary")
		}
		rawOutcome := outcome
		outcome, ok := normalizeToolReconciliationOutcome(outcome)
		if !ok {
			return fmt.Errorf("unsupported reconciliation outcome %q", rawOutcome)
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Interventions.Respond(context.Background(), sessionID, turnID, callID, map[string]string{
			"outcome": outcome, "summary": summary, "evidence": evidence,
		})
		if err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown session intervention subcommand %q", args[0])
	}
}

func commandSessionSummary(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("session summary command requires a subcommand")
	}

	switch args[0] {
	case "get":
		flags := flag.NewFlagSet("session summary get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session summary get requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.GetSummary(context.Background(), sessionID)
		if err != nil {
			return err
		}
		return printJSON(response)
	case "upsert":
		flags := flag.NewFlagSet("session summary upsert", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var text string
		var untilSeq int64
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&text, "text", "", "summary text")
		flags.Int64Var(&untilSeq, "until", 0, "source until event seq")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" || text == "" {
			return fmt.Errorf("session summary upsert requires --session and --text")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.UpsertSummary(context.Background(), sessionID, tma.UpsertSessionSummaryRequest{
			SummaryText: text, SourceUntilSeq: untilSeq,
		})
		if err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown session summary subcommand %q", args[0])
	}
}

func commandUsage(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage command requires a subcommand")
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("usage list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("usage list requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.Usage(context.Background(), sessionID)
		if err != nil {
			return err
		}
		return printJSON(response)
	case "summary":
		flags := flag.NewFlagSet("usage summary", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workspaceID string
		var providerID string
		var model string
		var status string
		var groupBy string
		var from string
		var to string
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		flags.StringVar(&providerID, "provider", "", "provider id")
		flags.StringVar(&model, "model", "", "model name")
		flags.StringVar(&status, "status", "", "usage status")
		flags.StringVar(&groupBy, "group-by", "", "provider, model, or provider_model")
		flags.StringVar(&from, "from", "", "RFC3339 inclusive start time")
		flags.StringVar(&to, "to", "", "RFC3339 exclusive end time")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}

		query := tma.LLMUsageQuery{
			WorkspaceID: workspaceID, ProviderID: providerID, Model: model, Status: status, GroupBy: groupBy,
		}
		if from != "" {
			parsed, err := time.Parse(time.RFC3339, from)
			if err != nil {
				return fmt.Errorf("invalid --from RFC3339 time: %w", err)
			}
			query.From = &parsed
		}
		if to != "" {
			parsed, err := time.Parse(time.RFC3339, to)
			if err != nil {
				return fmt.Errorf("invalid --to RFC3339 time: %w", err)
			}
			query.To = &parsed
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		report, err := sdk.LLM.Usage(context.Background(), query)
		if err != nil {
			return err
		}
		return printJSON(report)
	default:
		return fmt.Errorf("unknown usage subcommand %q", args[0])
	}
}

func commandEvent(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("event command requires a subcommand")
	}

	switch args[0] {
	case "send":
		flags := flag.NewFlagSet("event send", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var text string
		var eventType string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&text, "text", "", "text content for user.message")
		flags.StringVar(&eventType, "type", "user.message", "event type")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("event send requires --session")
		}
		if eventType == "user.message" && text == "" {
			return fmt.Errorf("event send requires --text for user.message")
		}

		payload := map[string]any{}
		if text != "" {
			payload["content"] = []map[string]string{
				{"type": "text", "text": text},
			}
		}

		encodedPayload, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode event payload: %w", err)
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.AppendEvents(context.Background(), sessionID, tma.AppendEventsRequest{Events: []tma.AppendEvent{{
			Type: eventType, Payload: encodedPayload,
		}}})
		if err != nil {
			return err
		}

		return printJSON(response)
	case "list":
		flags := flag.NewFlagSet("event list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var afterSeq int64
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.Int64Var(&afterSeq, "after", 0, "return events after this seq")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("event list requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		events, err := sdk.Sessions.ListEvents(context.Background(), sessionID, afterSeq)
		if err != nil {
			return err
		}

		return printJSON(map[string]any{"events": events})
	case "stream":
		flags := flag.NewFlagSet("event stream", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var afterSeq int64
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.Int64Var(&afterSeq, "after", 0, "stream events after this seq")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("event stream requires --session")
		}

		return client.streamSessionEvents(context.Background(), sessionID, afterSeq, os.Stdout)
	case "interrupt":
		flags := flag.NewFlagSet("event interrupt", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		flags.StringVar(&sessionID, "session", "", "session id")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("event interrupt requires --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Sessions.AppendEvents(context.Background(), sessionID, tma.AppendEventsRequest{Events: []tma.AppendEvent{{Type: "user.interrupt"}}})
		if err != nil {
			return err
		}

		return printJSON(response)
	default:
		return fmt.Errorf("unknown event subcommand %q", args[0])
	}
}

type apiClient struct {
	baseURL     string
	authToken   string
	http        *http.Client
	streamHTTP  *http.Client
	sdk         *tma.Client
	credentials credentialStore
	tokenSource tma.TokenSource
}

func (c *apiClient) do(method, path string, requestBody any, responseBody any) error {
	sdk, err := c.sdkClient()
	if err != nil {
		return err
	}
	return sdk.DoJSON(context.Background(), method, sdkAPIPath(method, path), requestBody, responseBody)
}

func (c *apiClient) streamSessionEvents(ctx context.Context, sessionID string, afterSeq int64, output io.Writer) error {
	sdk, err := c.sdkClient()
	if err != nil {
		return err
	}
	stream, err := sdk.Sessions.Events(ctx, sessionID, afterSeq)
	if err != nil {
		return err
	}
	defer stream.Close()
	return streamSDKEvents(ctx, stream, output, nil)
}

func (c *apiClient) sdkClient() (*tma.Client, error) {
	if c.sdk != nil {
		return c.sdk, nil
	}
	options := []tma.Option{}
	if c.authToken != "" {
		options = append(options, tma.WithBearerToken(c.authToken))
	} else if c.tokenSource != nil {
		options = append(options, tma.WithTokenSource(c.tokenSource))
	}
	if c.http != nil {
		options = append(options, tma.WithHTTPClient(c.http))
	}
	if c.streamHTTP != nil {
		options = append(options, tma.WithStreamHTTPClient(c.streamHTTP))
	}
	client, err := tma.NewClient(c.baseURL, options...)
	if err != nil {
		return nil, err
	}
	c.sdk = client
	return client, nil
}

func (c *apiClient) unauthenticatedSDKClient() (*tma.Client, error) {
	options := []tma.Option{}
	if c.http != nil {
		options = append(options, tma.WithHTTPClient(c.http))
	}
	if c.streamHTTP != nil {
		options = append(options, tma.WithStreamHTTPClient(c.streamHTTP))
	}
	return tma.NewClient(c.baseURL, options...)
}

func sdkAPIPath(method string, path string) string {
	if !strings.HasPrefix(path, "/v1/") {
		return path
	}
	if method == http.MethodPost && path == "/v1/workers" {
		return path
	}
	if method == http.MethodPost && strings.HasPrefix(path, "/v1/workers/") && strings.HasSuffix(path, "/heartbeat") {
		return path
	}
	if strings.Contains(path, "/work/poll") || strings.Contains(path, "/work/") && (strings.HasSuffix(path, "/ack") || strings.HasSuffix(path, "/heartbeat") || strings.HasSuffix(path, "/result")) {
		return path
	}
	return "/v2/" + strings.TrimPrefix(path, "/v1/")
}

func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}

	fmt.Println(string(encoded))
	return nil
}

func printRawJSON(raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode output: %w", err)
	}
	return printJSON(value)
}

func defaultJSONObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func normalizeInterventionModeArg(value string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "request_approval":
		return "request_approval", true
	case "approve_for_me":
		return "approve_for_me", true
	case "full_access":
		return "full_access", true
	default:
		return "", false
	}
}

func normalizeToolReconciliationOutcome(value string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "executed":
		return "executed", true
	case "not_executed":
		return "not_executed", true
	case "compensated":
		return "compensated", true
	default:
		return "", false
	}
}

func isHelpArg(value string) bool {
	switch value {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func flagWasPassed(flags *flag.FlagSet, name string) bool {
	passed := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			passed = true
		}
	})
	return passed
}

func usageError() error {
	printUsage()
	return errors.New("missing command")
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
	  tma [--base-url URL] auth login [--timeout DURATION] [--no-browser]
	  tma [--base-url URL] auth status
	  tma [--base-url URL] auth logout
	  tma [--base-url URL] [--auth-token TOKEN] health
  tma sandbox doctor [--runtime auto|cloud_sandbox|local_system] [--root PATH] [--image IMAGE] [--docker COMMAND] [--pull=false]
  tma web doctor [--searxng-url URL] [--query TEXT] [--timeout SECONDS]
  tma web search --query TEXT [--limit N] [--categories LIST] [--engines LIST] [--time-range RANGE] [--timeout SECONDS]
  tma web crawl --url URL [--impl IMPL] [--max-pages N] [--timeout SECONDS] [--attempts-only|--content-only]
  tma [--base-url URL] [--auth-token TOKEN] provider list
  tma [--base-url URL] [--auth-token TOKEN] provider get --id PROVIDER
  tma [--base-url URL] [--auth-token TOKEN] provider create --id PROVIDER --type TYPE [--base-url URL] [--api-key-env ENV] [--disabled]
  tma [--base-url URL] [--auth-token TOKEN] provider update --id PROVIDER [--type TYPE] [--base-url URL] [--api-key-env ENV]
  tma [--base-url URL] [--auth-token TOKEN] provider enable --id PROVIDER
  tma [--base-url URL] [--auth-token TOKEN] provider disable --id PROVIDER
  tma [--base-url URL] [--auth-token TOKEN] model list [--provider PROVIDER]
  tma [--base-url URL] [--auth-token TOKEN] model upsert --provider PROVIDER --model MODEL [--context-window TOKENS]
  tma [--base-url URL] [--auth-token TOKEN] object create --bucket BUCKET --key KEY [--workspace WORKSPACE] [--size BYTES] [--content-type TYPE] [--sha256 HEX] [--metadata JSON]
  tma [--base-url URL] [--auth-token TOKEN] object get --id OBJECT_REF_ID
  tma [--base-url URL] [--auth-token TOKEN] object download --id OBJECT_REF_ID [--session SESSION_ID] [--output PATH]
  tma [--base-url URL] [--auth-token TOKEN] object delete --id OBJECT_REF_ID
  tma [--base-url URL] [--auth-token TOKEN] worker register --name NAME [--workspace WORKSPACE] [--type local|shared|cloud] [--capabilities JSON] [--metadata JSON] [--lease-seconds N]
  tma [--base-url URL] [--auth-token TOKEN] worker list [--workspace WORKSPACE] [--status STATUS] [--json]
  tma [--base-url URL] [--auth-token TOKEN] worker get --id WORKER_ID
  tma [--base-url URL] [--auth-token TOKEN] worker heartbeat --id WORKER_ID [--status online|offline|draining] [--lease-seconds N]
  tma [--base-url URL] [--auth-token TOKEN] worker archive --id WORKER_ID
  tma [--base-url URL] [--auth-token TOKEN] worker reap-expired [--limit N]
  tma [--base-url URL] [--auth-token TOKEN] worker diagnose --api API [--namespace default] [--capabilities LIST] [--runtime auto|cloud_sandbox|local_system] [--workspace WORKSPACE] [--json]
  tma [--base-url URL] [--auth-token TOKEN] work enqueue [--workspace WORKSPACE] [--worker WORKER_ID] [--env ENV_ID] [--session SESSION_ID] [--turn TURN_ID] [--type tool_execution|sandbox_command|artifact_sync] [--payload JSON]
  tma [--base-url URL] [--auth-token TOKEN] work enqueue --api API [--namespace default] [--capabilities LIST] [--risk read|write|exec] [--runtime auto|cloud_sandbox|local_system] [--input JSON]
  tma [--base-url URL] [--auth-token TOKEN] work get --work WORK_ID
  tma [--base-url URL] [--auth-token TOKEN] work diagnose --work WORK_ID [--json]
  tma [--base-url URL] [--auth-token TOKEN] work cancel --work WORK_ID [--reason TEXT]
  tma [--base-url URL] [--auth-token TOKEN] work requeue --work WORK_ID [--worker WORKER_ID|--clear-worker]
  tma [--base-url URL] [--auth-token TOKEN] work reap-expired [--limit N]
  tma [--base-url URL] [--auth-token TOKEN] work poll --worker WORKER_ID [--lease-seconds N]
  tma [--base-url URL] [--auth-token TOKEN] work ack --worker WORKER_ID --work WORK_ID
  tma [--base-url URL] [--auth-token TOKEN] work heartbeat --worker WORKER_ID --work WORK_ID [--lease-seconds N]
  tma [--base-url URL] [--auth-token TOKEN] work result --worker WORKER_ID --work WORK_ID --success|--failure [--error TEXT] [--result JSON]
  tma [--base-url URL] [--auth-token TOKEN] agent create --name NAME --env ENVIRONMENT_ID --model MODEL [--llm-provider PROVIDER] [--system TEXT]
  tma [--base-url URL] [--auth-token TOKEN] agent get --id AGENT_ID
  tma [--base-url URL] [--auth-token TOKEN] agent export --id AGENT_ID [--output FILE]
  tma [--base-url URL] [--auth-token TOKEN] agent import --file FILE [--name NAME] [--workspace WORKSPACE_ID]
  tma [--base-url URL] [--auth-token TOKEN] agent config list --agent AGENT_ID
  tma [--base-url URL] [--auth-token TOKEN] agent config update --agent AGENT_ID [--llm-provider PROVIDER] [--llm-model MODEL] [--system TEXT] [--tools JSON] [--mcp JSON]
  tma [--base-url URL] [--auth-token TOKEN] env list
  tma [--base-url URL] [--auth-token TOKEN] env create --name NAME [--config JSON]
  tma [--base-url URL] [--auth-token TOKEN] session create --agent AGENT_ID [--env ENV_ID] [--title TITLE]
  tma [--base-url URL] [--auth-token TOKEN] session get --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session compare --left SESSION_ID --right SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session attach --session SESSION_ID [--after SEQ]
  tma [--base-url URL] [--auth-token TOKEN] session config upgrade --session SESSION_ID (--to-current | --to-version VERSION)
  tma [--base-url URL] [--auth-token TOKEN] session runtime get --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session runtime update --session SESSION_ID [--intervention-mode MODE] [--cloud-sandbox-root PATH] [--cloud-sandbox-image IMAGE]
  tma [--base-url URL] [--auth-token TOKEN] session intervention list --session SESSION_ID [--status STATUS]
  tma [--base-url URL] [--auth-token TOKEN] session intervention approve --session SESSION_ID --turn TURN_ID --call CALL_ID [--reason TEXT]
  tma [--base-url URL] [--auth-token TOKEN] session intervention reject --session SESSION_ID --turn TURN_ID --call CALL_ID [--reason TEXT]
  tma [--base-url URL] [--auth-token TOKEN] session intervention reconcile --session SESSION_ID --turn TURN_ID --call INTERVENTION_ID --outcome executed|not_executed|compensated --summary TEXT [--evidence REF]
  tma [--base-url URL] [--auth-token TOKEN] session archive --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session delete --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session summary get --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session summary upsert --session SESSION_ID --text TEXT [--until SEQ]
  tma [--base-url URL] [--auth-token TOKEN] session artifact create --session SESSION_ID --object OBJECT_REF_ID [--name NAME] [--type file|snapshot|asset] [--metadata JSON]
  tma [--base-url URL] [--auth-token TOKEN] session artifact list --session SESSION_ID [--json]
  tma [--base-url URL] [--auth-token TOKEN] session artifact download --session SESSION_ID --artifact ARTIFACT_ID [--output PATH]
  tma [--base-url URL] [--auth-token TOKEN] session artifact delete --session SESSION_ID --artifact ARTIFACT_ID
  tma [--base-url URL] [--auth-token TOKEN] usage list --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] usage summary [--group-by provider|model|provider_model] [--provider PROVIDER] [--model MODEL] [--from RFC3339] [--to RFC3339]
  tma [--base-url URL] [--auth-token TOKEN] event send --session SESSION_ID --text TEXT
  tma [--base-url URL] [--auth-token TOKEN] event interrupt --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] event list --session SESSION_ID [--after SEQ]
  tma [--base-url URL] [--auth-token TOKEN] event stream --session SESSION_ID [--after SEQ]
  tma [--base-url URL] [--auth-token TOKEN] trace list [--workspace ID] [--session ID] [--turn ID] [--status STATUS] [--limit N] [--cursor CURSOR]
  tma [--base-url URL] [--auth-token TOKEN] trace show (--trace TRACE_ID | --session SESSION_ID [--turn TURN_ID]) [--json]
  tma [--base-url URL] [--auth-token TOKEN] trace export --session SESSION_ID [--turn TURN_ID] [--format perfetto|otel|json] [--output FILE] [--otlp-endpoint URL]
  tma [--base-url URL] [--auth-token TOKEN] observability status
  tma [--base-url URL] [--auth-token TOKEN] observability retry
  tma [--base-url URL] [--auth-token TOKEN] observability integrity-keys
  tma [--base-url URL] [--auth-token TOKEN] skill create --identifier ID --title TITLE [--workspace ID] [--description TEXT]
  tma [--base-url URL] [--auth-token TOKEN] skill list [--workspace ID] [--include-archived]
  tma [--base-url URL] [--auth-token TOKEN] skill get|archive --skill SKILL_ID
  tma [--base-url URL] [--auth-token TOKEN] skill version create --skill SKILL_ID --content TEXT [--format FORMAT] [--manifest JSON] [--assets JSON]
  tma [--base-url URL] [--auth-token TOKEN] skill version list --skill SKILL_ID
  tma [--base-url URL] [--auth-token TOKEN] skill version get --skill SKILL_ID --version N
  tma [--base-url URL] [--auth-token TOKEN] skill version download --skill SKILL_ID --version N --output FILE
  tma [--base-url URL] [--auth-token TOKEN] skill resolve --skills JSON [--workspace ID] [--max-tokens N]
  tma [--base-url URL] [--auth-token TOKEN] skill usage --session SESSION_ID [--turn TURN_ID]
  tma [--base-url URL] [--auth-token TOKEN] skill package backfill [--workspace ID] [--limit N]
  tma [--base-url URL] [--auth-token TOKEN] skill retention effective --workspace ID
  tma [--base-url URL] [--auth-token TOKEN] skill retention create|list|get|publish|get-version|archive [flags]
  tma [--base-url URL] [--auth-token TOKEN] skill gc preview|run|list|get|tombstones [flags]
  tma [--base-url URL] [--auth-token TOKEN] marketplace discover --session SESSION_ID (--query TEXT | --repository OWNER/REPO) [--limit N]
  tma [--base-url URL] [--auth-token TOKEN] marketplace preview|install --session SESSION_ID --source JSON [flags]
  tma [--base-url URL] [--auth-token TOKEN] marketplace internal list --session SESSION_ID [--query TEXT] [--category TEXT] [--tags LIST] [--limit N]
  tma [--base-url URL] [--auth-token TOKEN] marketplace internal preview|install --session SESSION_ID --source JSON [flags]
  tma [--base-url URL] [--auth-token TOKEN] marketplace enable|disable --skill SKILL_ID --session SESSION_ID [flags]
  tma [--base-url URL] [--auth-token TOKEN] marketplace entry create|list|get|update|submit|publish|withdraw [flags]
  tma [--base-url URL] [--auth-token TOKEN] marketplace policy create|list|get|publish|get-version|archive [flags]

Environment:
	TMA_BASE_URL               API base URL. Defaults to http://localhost:8080
	TMA_AUTH_TOKEN             Preferred API bearer token for CLI requests
	TMA_AUTH_LOGIN_TIMEOUT     Device authorization login timeout. Defaults to 5m
  TMA_WORKER_CONTROL_TOKEN   Legacy fallback control-plane bearer token
  TMA_OTEL_EXPORTER_OTLP_ENDPOINT  Optional OTLP/HTTP base URL for trace export push`)
}
