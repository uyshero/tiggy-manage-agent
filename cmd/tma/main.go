package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
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
	authToken := os.Getenv("TMA_WORKER_CONTROL_TOKEN")

	global := flag.NewFlagSet("tma", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	global.StringVar(&baseURL, "base-url", baseURL, "TMA API base URL")
	global.StringVar(&authToken, "auth-token", authToken, "control-plane bearer token")

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
		streamHTTP: &http.Client{}, // SSE 是长连接，不能设置全局 Client.Timeout。
	}

	switch remaining[0] {
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

		var response any
		if err := client.do(http.MethodGet, "/v1/llm-providers", nil, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		var response any
		if err := client.do(http.MethodGet, "/v1/llm-providers/"+url.PathEscape(id), nil, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		request := map[string]any{
			"id":            id,
			"provider_type": providerType,
			"base_url":      baseURL,
			"api_key_env":   apiKeyEnv,
			"enabled":       !disabled,
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/llm-providers", request, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		request := map[string]any{}
		if flagWasPassed(flags, "type") {
			request["provider_type"] = providerType
		}
		if flagWasPassed(flags, "base-url") {
			request["base_url"] = baseURL
		}
		if flagWasPassed(flags, "api-key-env") {
			request["api_key_env"] = apiKeyEnv
		}
		if len(request) == 0 {
			return fmt.Errorf("provider update requires at least one field flag")
		}

		var response any
		if err := client.do(http.MethodPatch, "/v1/llm-providers/"+url.PathEscape(id), request, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		var response any
		path := "/v1/llm-providers/" + url.PathEscape(id) + "/" + args[0]
		if err := client.do(http.MethodPost, path, map[string]any{}, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		path := "/v1/llm-models"
		if providerID != "" {
			path += "?provider_id=" + url.QueryEscape(providerID)
		}
		var response any
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		request := map[string]any{
			"provider_id": providerID,
			"model":       model,
		}
		if contextWindow > 0 {
			request["context_window_tokens"] = contextWindow
		}
		var response any
		if err := client.do(http.MethodPost, "/v1/llm-models", request, &response); err != nil {
			return err
		}
		return printJSON(response)
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
		flags.StringVar(&name, "name", "", "agent name")
		flags.StringVar(&llmProvider, "llm-provider", "", "llm provider id")
		flags.StringVar(&llmModel, "llm-model", "", "llm model id")
		flags.StringVar(&model, "model", "", "model id")
		flags.StringVar(&system, "system", "", "system prompt")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if llmModel == "" {
			llmModel = model
		}
		if name == "" || llmModel == "" {
			return fmt.Errorf("agent create requires --name and --model (or --llm-model)")
		}

		request := map[string]string{
			"name":         name,
			"llm_provider": llmProvider,
			"llm_model":    llmModel,
			"model":        model,
			"system":       system,
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/agents", request, &response); err != nil {
			return err
		}

		return printJSON(response)
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

		var response any
		if err := client.do(http.MethodGet, "/v1/agents/"+url.PathEscape(id), nil, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		var response any
		if err := client.do(http.MethodGet, "/v1/agents/"+url.PathEscape(agentID)+"/config-versions", nil, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "update":
		flags := flag.NewFlagSet("agent config update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var agentID string
		var llmProvider string
		var llmModel string
		var model string
		var system string
		var toolsConfig string
		flags.StringVar(&agentID, "agent", "", "agent id")
		flags.StringVar(&llmProvider, "llm-provider", "", "llm provider id")
		flags.StringVar(&llmModel, "llm-model", "", "llm model id")
		flags.StringVar(&model, "model", "", "model id")
		flags.StringVar(&system, "system", "", "system prompt")
		flags.StringVar(&toolsConfig, "tools", "", "tools config JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if agentID == "" {
			return fmt.Errorf("agent config update requires --agent")
		}

		request := map[string]any{}
		if flagWasPassed(flags, "llm-provider") {
			request["llm_provider"] = llmProvider
		}
		if flagWasPassed(flags, "llm-model") {
			request["llm_model"] = llmModel
		}
		if flagWasPassed(flags, "model") {
			request["model"] = model
		}
		if flagWasPassed(flags, "system") {
			request["system"] = system
		}
		if flagWasPassed(flags, "tools") {
			rawTools, err := parseOptionalJSONObjectFlag(toolsConfig, "tools")
			if err != nil {
				return err
			}
			if rawTools == nil {
				return fmt.Errorf("agent config update --tools requires a JSON object")
			}
			request["tools"] = rawTools
		}
		if len(request) == 0 {
			return fmt.Errorf("agent config update requires at least one field flag")
		}

		var response any
		path := "/v1/agents/" + url.PathEscape(agentID) + "/config-versions"
		if err := client.do(http.MethodPost, path, request, &response); err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown agent config subcommand %q", args[0])
	}
}

func commandEnvironment(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("env command requires a subcommand")
	}

	switch args[0] {
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

		request := map[string]any{
			"name":   name,
			"config": rawConfig,
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/environments", request, &response); err != nil {
			return err
		}

		return printJSON(response)
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
		if agentID == "" || environmentID == "" {
			return fmt.Errorf("session create requires --agent and --env")
		}

		request := map[string]string{
			"agent_id":       agentID,
			"environment_id": environmentID,
			"title":          title,
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/sessions", request, &response); err != nil {
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

		var response any
		if err := client.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID), nil, &response); err != nil {
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

		var response any
		if err := client.do(http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/archive", map[string]any{}, &response); err != nil {
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

		if err := client.do(http.MethodDelete, "/v1/sessions/"+url.PathEscape(sessionID), nil, nil); err != nil {
			return err
		}

		fmt.Printf("deleted session %s\n", sessionID)
		return nil
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
		var updatedBy string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.BoolVar(&toCurrent, "to-current", false, "upgrade session to agent current config version")
		flags.StringVar(&updatedBy, "updated-by", "", "updater id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session config upgrade requires --session")
		}
		if !toCurrent {
			return fmt.Errorf("session config upgrade requires --to-current")
		}
		request := map[string]any{"to_current": true}
		setStringIfNotEmpty(request, "updated_by", updatedBy)
		var response any
		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/config/upgrade"
		if err := client.do(http.MethodPost, path, request, &response); err != nil {
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

		var response struct {
			RuntimeSettings json.RawMessage `json:"runtime_settings"`
		}
		if err := client.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID), nil, &response); err != nil {
			return err
		}
		return printRawJSON(defaultJSONObject(response.RuntimeSettings))
	case "update":
		flags := flag.NewFlagSet("session runtime update", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var interventionMode string
		var toolRuntime string
		var cloudSandboxRoot string
		var cloudSandboxImage string
		var cloudSandboxAllowNetwork bool
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&interventionMode, "intervention-mode", "", "request_approval | approve_for_me | full_access")
		flags.StringVar(&toolRuntime, "tool-runtime", "", "auto | cloud_sandbox | local_system")
		flags.StringVar(&cloudSandboxRoot, "cloud-sandbox-root", "", "workspace root path for cloud_sandbox runtime")
		flags.StringVar(&cloudSandboxImage, "cloud-sandbox-image", "", "Onlyboxes image for cloud_sandbox runtime")
		flags.BoolVar(&cloudSandboxAllowNetwork, "cloud-sandbox-allow-network", false, "allow full outbound network access for cloud_sandbox")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session runtime update requires --session")
		}
		if interventionMode == "" && toolRuntime == "" && cloudSandboxRoot == "" && cloudSandboxImage == "" && !flagWasPassed(flags, "cloud-sandbox-allow-network") {
			return fmt.Errorf("session runtime update requires at least one runtime setting flag")
		}

		request := map[string]any{}
		if interventionMode != "" {
			mode, ok := normalizeInterventionModeArg(interventionMode)
			if !ok {
				return fmt.Errorf("unsupported intervention mode %q", interventionMode)
			}
			request["intervention_mode"] = mode
		}
		if toolRuntime != "" {
			mode, ok := normalizeToolRuntimeArg(toolRuntime)
			if !ok {
				return fmt.Errorf("unsupported tool runtime %q", toolRuntime)
			}
			request["tool_runtime"] = mode
		}
		if cloudSandboxRoot != "" {
			request["cloud_sandbox_root"] = cloudSandboxRoot
		}
		if cloudSandboxImage != "" {
			request["cloud_sandbox_image"] = cloudSandboxImage
		}
		if flagWasPassed(flags, "cloud-sandbox-allow-network") {
			request["cloud_sandbox_allow_network"] = cloudSandboxAllowNetwork
		}

		var response struct {
			RuntimeSettings json.RawMessage `json:"runtime_settings"`
		}
		if err := client.do(http.MethodPatch, "/v1/sessions/"+url.PathEscape(sessionID)+"/runtime-settings", request, &response); err != nil {
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

		query := url.Values{}
		if status != "" {
			query.Set("status", status)
		}
		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/interventions"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}

		var response any
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/interventions/" + url.PathEscape(turnID) + "/" + url.PathEscape(callID) + "/" + args[0]
		var response any
		if err := client.do(http.MethodPost, path, map[string]any{"reason": reason}, &response); err != nil {
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

		var response any
		if err := client.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID)+"/summary", nil, &response); err != nil {
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

		request := map[string]any{
			"summary_text":     text,
			"source_until_seq": untilSeq,
		}
		var response any
		if err := client.do(http.MethodPut, "/v1/sessions/"+url.PathEscape(sessionID)+"/summary", request, &response); err != nil {
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

		var response any
		if err := client.do(http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID)+"/usage", nil, &response); err != nil {
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

		query := url.Values{}
		if workspaceID != "" {
			query.Set("workspace_id", workspaceID)
		}
		if providerID != "" {
			query.Set("provider_id", providerID)
		}
		if model != "" {
			query.Set("model", model)
		}
		if status != "" {
			query.Set("status", status)
		}
		if groupBy != "" {
			query.Set("group_by", groupBy)
		}
		if from != "" {
			query.Set("from", from)
		}
		if to != "" {
			query.Set("to", to)
		}

		path := "/v1/llm-usage"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
		var response any
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		return printJSON(response)
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

		request := map[string]any{
			"events": []map[string]any{
				{"type": eventType, "payload": payload},
			},
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/events", request, &response); err != nil {
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

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/events"
		if afterSeq > 0 {
			path += "?after_seq=" + strconv.FormatInt(afterSeq, 10)
		}

		var response any
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}

		return printJSON(response)
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

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/events/stream"
		if afterSeq > 0 {
			path += "?after_seq=" + strconv.FormatInt(afterSeq, 10)
		}

		return client.stream(path, os.Stdout)
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

		request := map[string]any{
			"events": []map[string]any{
				{"type": "user.interrupt"},
			},
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/events", request, &response); err != nil {
			return err
		}

		return printJSON(response)
	default:
		return fmt.Errorf("unknown event subcommand %q", args[0])
	}
}

type apiClient struct {
	baseURL    string
	authToken  string
	http       *http.Client
	streamHTTP *http.Client
}

func (c *apiClient) do(method, path string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if responseBody != nil && len(responseBytes) > 0 {
			_ = json.Unmarshal(responseBytes, responseBody)
		}
		return fmt.Errorf("%s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(responseBytes)))
	}

	if responseBody == nil {
		return nil
	}
	if len(responseBytes) == 0 {
		return nil
	}

	if err := json.Unmarshal(responseBytes, responseBody); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func (c *apiClient) download(path string, output io.Writer) error {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBytes, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return fmt.Errorf("read error response: %w", readErr)
		}
		return fmt.Errorf("GET %s returned %s: %s", path, response.Status, strings.TrimSpace(string(responseBytes)))
	}

	if output == nil {
		output = os.Stdout
	}
	if _, err := io.Copy(output, response.Body); err != nil {
		return fmt.Errorf("copy download response: %w", err)
	}
	return nil
}

func (c *apiClient) stream(path string, output io.Writer) error {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.streamHTTP.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBytes, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return fmt.Errorf("read error response: %w", readErr)
		}
		return fmt.Errorf("GET %s returned %s: %s", path, response.Status, strings.TrimSpace(string(responseBytes)))
	}

	return streamSSE(response.Body, output)
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

func normalizeToolRuntimeArg(value string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "auto":
		return "auto", true
	case "cloud_sandbox":
		return "cloud_sandbox", true
	case "local_system":
		return "local_system", true
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
  tma [--base-url URL] [--auth-token TOKEN] work reap-expired [--limit N]
  tma [--base-url URL] [--auth-token TOKEN] work poll --worker WORKER_ID [--lease-seconds N]
  tma [--base-url URL] [--auth-token TOKEN] work ack --worker WORKER_ID --work WORK_ID
  tma [--base-url URL] [--auth-token TOKEN] work heartbeat --worker WORKER_ID --work WORK_ID [--lease-seconds N]
  tma [--base-url URL] [--auth-token TOKEN] work result --worker WORKER_ID --work WORK_ID --success|--failure [--error TEXT] [--result JSON]
  tma [--base-url URL] [--auth-token TOKEN] agent create --name NAME --model MODEL [--llm-provider PROVIDER] [--system TEXT]
  tma [--base-url URL] [--auth-token TOKEN] agent get --id AGENT_ID
  tma [--base-url URL] [--auth-token TOKEN] agent config list --agent AGENT_ID
  tma [--base-url URL] [--auth-token TOKEN] agent config update --agent AGENT_ID [--llm-provider PROVIDER] [--llm-model MODEL] [--system TEXT] [--tools JSON]
  tma [--base-url URL] [--auth-token TOKEN] env create --name NAME [--config JSON]
  tma [--base-url URL] [--auth-token TOKEN] session create --agent AGENT_ID --env ENV_ID [--title TITLE]
  tma [--base-url URL] [--auth-token TOKEN] session get --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session attach --session SESSION_ID [--after SEQ]
  tma [--base-url URL] [--auth-token TOKEN] session config upgrade --session SESSION_ID --to-current
  tma [--base-url URL] [--auth-token TOKEN] session runtime get --session SESSION_ID
  tma [--base-url URL] [--auth-token TOKEN] session runtime update --session SESSION_ID [--intervention-mode MODE] [--tool-runtime auto|cloud_sandbox|local_system] [--cloud-sandbox-root PATH] [--cloud-sandbox-image IMAGE]
  tma [--base-url URL] [--auth-token TOKEN] session intervention list --session SESSION_ID [--status STATUS]
  tma [--base-url URL] [--auth-token TOKEN] session intervention approve --session SESSION_ID --turn TURN_ID --call CALL_ID [--reason TEXT]
  tma [--base-url URL] [--auth-token TOKEN] session intervention reject --session SESSION_ID --turn TURN_ID --call CALL_ID [--reason TEXT]
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
  tma [--base-url URL] [--auth-token TOKEN] trace show --session SESSION_ID [--turn TURN_ID] [--json]
  tma [--base-url URL] [--auth-token TOKEN] trace export --session SESSION_ID [--turn TURN_ID] [--format perfetto|otel|json] [--output FILE] [--otlp-endpoint URL]
  tma [--base-url URL] [--auth-token TOKEN] observability status
  tma [--base-url URL] [--auth-token TOKEN] observability retry

Environment:
  TMA_BASE_URL               API base URL. Defaults to http://localhost:8080
  TMA_WORKER_CONTROL_TOKEN   Optional control-plane bearer token for CLI requests
  TMA_OTEL_EXPORTER_OTLP_ENDPOINT  Optional OTLP/HTTP base URL for trace export push`)
}
