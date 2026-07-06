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

	global := flag.NewFlagSet("tma", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	global.StringVar(&baseURL, "base-url", baseURL, "TMA API base URL")

	if err := global.Parse(args); err != nil {
		return err
	}

	remaining := global.Args()
	if len(remaining) == 0 {
		return usageError()
	}

	client := &apiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
		streamHTTP: &http.Client{}, // SSE 是长连接，不能设置全局 Client.Timeout。
	}

	switch remaining[0] {
	case "health":
		return commandHealth(client, remaining[1:])
	case "agent":
		return commandAgent(client, remaining[1:])
	case "env":
		return commandEnvironment(client, remaining[1:])
	case "session":
		return commandSession(client, remaining[1:])
	case "event":
		return commandEvent(client, remaining[1:])
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

func commandAgent(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent command requires a subcommand")
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("agent create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var name string
		var model string
		var system string
		flags.StringVar(&name, "name", "", "agent name")
		flags.StringVar(&model, "model", "", "model id")
		flags.StringVar(&system, "system", "", "system prompt")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if name == "" || model == "" {
			return fmt.Errorf("agent create requires --name and --model")
		}

		request := map[string]string{
			"name":   name,
			"model":  model,
			"system": system,
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/agents", request, &response); err != nil {
			return err
		}

		return printJSON(response)
	default:
		return fmt.Errorf("unknown agent subcommand %q", args[0])
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
	default:
		return fmt.Errorf("unknown session subcommand %q", args[0])
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

func (c *apiClient) stream(path string, output io.Writer) error {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")

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

	_, err = io.Copy(output, response.Body)
	if err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}

	fmt.Println(string(encoded))
	return nil
}

func usageError() error {
	printUsage()
	return errors.New("missing command")
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  tma [--base-url URL] health
  tma [--base-url URL] agent create --name NAME --model MODEL [--system TEXT]
  tma [--base-url URL] env create --name NAME [--config JSON]
  tma [--base-url URL] session create --agent AGENT_ID --env ENV_ID [--title TITLE]
  tma [--base-url URL] session get --session SESSION_ID
  tma [--base-url URL] session archive --session SESSION_ID
  tma [--base-url URL] session delete --session SESSION_ID
  tma [--base-url URL] event send --session SESSION_ID --text TEXT
  tma [--base-url URL] event interrupt --session SESSION_ID
  tma [--base-url URL] event list --session SESSION_ID [--after SEQ]
  tma [--base-url URL] event stream --session SESSION_ID [--after SEQ]

Environment:
  TMA_BASE_URL   API base URL. Defaults to http://localhost:8080`)
}
