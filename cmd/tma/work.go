package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"tiggy-manage-agent/internal/tools"
)

type workerWorkDiagnoseResponse struct {
	Work struct {
		ID             string          `json:"id"`
		WorkspaceID    string          `json:"workspace_id"`
		WorkerID       string          `json:"worker_id,omitempty"`
		EnvironmentID  string          `json:"environment_id,omitempty"`
		SessionID      string          `json:"session_id,omitempty"`
		TurnID         string          `json:"turn_id,omitempty"`
		WorkType       string          `json:"work_type"`
		Status         string          `json:"status"`
		ErrorMessage   string          `json:"error_message,omitempty"`
		LeaseExpiresAt string          `json:"lease_expires_at,omitempty"`
		StartedAt      string          `json:"started_at,omitempty"`
		CompletedAt    string          `json:"completed_at,omitempty"`
		Payload        json.RawMessage `json:"payload,omitempty"`
		Result         json.RawMessage `json:"result,omitempty"`
	} `json:"work"`
	Worker *struct {
		ID             string `json:"id"`
		WorkspaceID    string `json:"workspace_id"`
		Name           string `json:"name"`
		WorkerType     string `json:"worker_type"`
		Status         string `json:"status"`
		LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
		LastSeenAt     string `json:"last_seen_at,omitempty"`
	} `json:"worker,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

func commandWork(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("work command requires a subcommand")
	}

	switch args[0] {
	case "enqueue":
		flags := flag.NewFlagSet("work enqueue", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workspaceID string
		var workerID string
		var environmentID string
		var sessionID string
		var turnID string
		var workType string
		var payload string
		var namespace string
		var api string
		var capabilities string
		var risk string
		var runtime string
		var input string
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		flags.StringVar(&workerID, "worker", "", "target worker id")
		flags.StringVar(&environmentID, "env", "", "environment id")
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.StringVar(&workType, "type", "", "tool_execution | sandbox_command | artifact_sync")
		flags.StringVar(&payload, "payload", "", "full payload JSON object")
		flags.StringVar(&namespace, "namespace", tools.NamespaceDefault, "tool namespace for tool_execution")
		flags.StringVar(&api, "api", "", "tool api for tool_execution")
		flags.StringVar(&capabilities, "capabilities", "", "comma-separated tool capabilities")
		flags.StringVar(&risk, "risk", "", "read | write | exec")
		flags.StringVar(&runtime, "runtime", "", "auto | cloud_sandbox | local_system")
		flags.StringVar(&input, "input", "", "tool input JSON object")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}

		rawPayload, err := parseOptionalJSONObjectFlag(payload, "payload")
		if err != nil {
			return err
		}
		if rawPayload == nil {
			rawPayload, err = buildToolExecutionPayload(workType, namespace, api, capabilities, risk, runtime, input)
			if err != nil {
				return err
			}
		}
		request := map[string]any{
			"payload": rawPayload,
		}
		setStringIfNotEmpty(request, "workspace_id", workspaceID)
		setStringIfNotEmpty(request, "worker_id", workerID)
		setStringIfNotEmpty(request, "environment_id", environmentID)
		setStringIfNotEmpty(request, "session_id", sessionID)
		setStringIfNotEmpty(request, "turn_id", turnID)
		setStringIfNotEmpty(request, "work_type", workType)

		var response map[string]any
		if err := client.do(http.MethodPost, "/v1/worker-work", request, &response); err != nil {
			printWorkerWorkDiagnostics(response)
			return err
		}
		return printJSON(response)
	case "get":
		flags := flag.NewFlagSet("work get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workID string
		flags.StringVar(&workID, "work", "", "work id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workID == "" {
			return fmt.Errorf("work get requires --work")
		}

		var response any
		if err := client.do(http.MethodGet, "/v1/worker-work/"+url.PathEscape(workID), nil, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "reap-expired":
		flags := flag.NewFlagSet("work reap-expired", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var limit int
		flags.IntVar(&limit, "limit", 0, "maximum expired work items to fail")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		request := map[string]any{}
		if limit > 0 {
			request["limit"] = limit
		}
		var response any
		if err := client.do(http.MethodPost, "/v1/worker-work/reap-expired", request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "cancel":
		flags := flag.NewFlagSet("work cancel", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workID string
		var reason string
		flags.StringVar(&workID, "work", "", "work id")
		flags.StringVar(&reason, "reason", "", "cancel reason")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workID == "" {
			return fmt.Errorf("work cancel requires --work")
		}
		request := map[string]any{}
		if strings.TrimSpace(reason) != "" {
			request["reason"] = reason
		}
		var response any
		if err := client.do(http.MethodPost, "/v1/worker-work/"+url.PathEscape(workID)+"/cancel", request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "diagnose":
		flags := flag.NewFlagSet("work diagnose", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workID string
		var jsonOutput bool
		flags.StringVar(&workID, "work", "", "work id")
		flags.BoolVar(&jsonOutput, "json", false, "print raw JSON response")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workID == "" {
			return fmt.Errorf("work diagnose requires --work")
		}
		var response workerWorkDiagnoseResponse
		if err := client.do(http.MethodGet, "/v1/worker-work/"+url.PathEscape(workID)+"/diagnose", nil, &response); err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(response)
		}
		printWorkerWorkDiagnosis(response)
		return nil
	case "poll":
		flags := flag.NewFlagSet("work poll", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workerID string
		var leaseSeconds int
		flags.StringVar(&workerID, "worker", "", "worker id")
		flags.IntVar(&leaseSeconds, "lease-seconds", 0, "lease seconds")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workerID == "" {
			return fmt.Errorf("work poll requires --worker")
		}

		path := "/v1/workers/" + url.PathEscape(workerID) + "/work/poll"
		if leaseSeconds > 0 {
			path += "?lease_seconds=" + fmt.Sprintf("%d", leaseSeconds)
		}
		var response struct {
			Work any `json:"work"`
		}
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "ack":
		flags := flag.NewFlagSet("work ack", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workerID string
		var workID string
		flags.StringVar(&workerID, "worker", "", "worker id")
		flags.StringVar(&workID, "work", "", "work id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workerID == "" || workID == "" {
			return fmt.Errorf("work ack requires --worker and --work")
		}

		var response any
		path := "/v1/workers/" + url.PathEscape(workerID) + "/work/" + url.PathEscape(workID) + "/ack"
		if err := client.do(http.MethodPost, path, map[string]any{}, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "heartbeat":
		flags := flag.NewFlagSet("work heartbeat", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workerID string
		var workID string
		var leaseSeconds int
		flags.StringVar(&workerID, "worker", "", "worker id")
		flags.StringVar(&workID, "work", "", "work id")
		flags.IntVar(&leaseSeconds, "lease-seconds", 0, "lease seconds")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workerID == "" || workID == "" {
			return fmt.Errorf("work heartbeat requires --worker and --work")
		}

		request := map[string]any{}
		if leaseSeconds > 0 {
			request["lease_seconds"] = leaseSeconds
		}

		var response any
		path := "/v1/workers/" + url.PathEscape(workerID) + "/work/" + url.PathEscape(workID) + "/heartbeat"
		if err := client.do(http.MethodPost, path, request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "result":
		flags := flag.NewFlagSet("work result", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workerID string
		var workID string
		var success bool
		var failure bool
		var errorMessage string
		var result string
		flags.StringVar(&workerID, "worker", "", "worker id")
		flags.StringVar(&workID, "work", "", "work id")
		flags.BoolVar(&success, "success", false, "mark work successful")
		flags.BoolVar(&failure, "failure", false, "mark work failed")
		flags.StringVar(&errorMessage, "error", "", "error message")
		flags.StringVar(&result, "result", "", "result JSON object")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workerID == "" || workID == "" {
			return fmt.Errorf("work result requires --worker and --work")
		}
		if success == failure {
			return fmt.Errorf("work result requires exactly one of --success or --failure")
		}
		rawResult, err := parseOptionalJSONObjectFlag(result, "result")
		if err != nil {
			return err
		}
		if rawResult == nil {
			rawResult = []byte(`{}`)
		}
		request := map[string]any{
			"success": success,
			"result":  rawResult,
		}
		if errorMessage != "" {
			request["error_message"] = errorMessage
		}
		var response any
		path := "/v1/workers/" + url.PathEscape(workerID) + "/work/" + url.PathEscape(workID) + "/result"
		if err := client.do(http.MethodPost, path, request, &response); err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown work subcommand %q", args[0])
	}
}

func buildToolExecutionPayload(workType string, namespace string, api string, capabilities string, risk string, runtime string, input string) (json.RawMessage, error) {
	normalizedWorkType := strings.TrimSpace(strings.ToLower(workType))
	if normalizedWorkType != "" && normalizedWorkType != "tool_execution" {
		return json.RawMessage(`{}`), nil
	}
	if strings.TrimSpace(api) == "" {
		return nil, fmt.Errorf("work enqueue tool_execution requires --api or --payload")
	}
	rawInput, err := parseOptionalJSONObjectFlag(input, "input")
	if err != nil {
		return nil, err
	}
	if rawInput == nil {
		rawInput = json.RawMessage(`{}`)
	}
	invocation := tools.WorkInvocation{
		ProtocolVersion: tools.WorkProtocolVersion,
		Namespace:       namespace,
		API:             api,
		Capabilities:    splitCSV(capabilities),
		Risk:            risk,
		Runtime:         runtime,
		Input:           rawInput,
	}
	if err := tools.ValidateWorkInvocation(invocation); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(invocation)
	if err != nil {
		return nil, fmt.Errorf("encode work invocation: %w", err)
	}
	return encoded, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func emptyString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func printWorkerWorkDiagnosis(response workerWorkDiagnoseResponse) {
	work := response.Work
	fmt.Printf("work diagnose %s\n", work.ID)
	fmt.Printf("  status: %s\n", emptyString(work.Status, "-"))
	fmt.Printf("  type: %s\n", emptyString(work.WorkType, "-"))
	if work.WorkspaceID != "" {
		fmt.Printf("  workspace: %s\n", work.WorkspaceID)
	}
	if work.SessionID != "" || work.TurnID != "" {
		fmt.Printf("  session: %s turn: %s\n", emptyString(work.SessionID, "-"), emptyString(work.TurnID, "-"))
	}
	if work.WorkerID != "" {
		fmt.Printf("  assigned_worker: %s\n", work.WorkerID)
	}
	if work.LeaseExpiresAt != "" {
		fmt.Printf("  work_lease_expires: %s\n", work.LeaseExpiresAt)
	}
	if work.StartedAt != "" {
		fmt.Printf("  started: %s\n", work.StartedAt)
	}
	if work.CompletedAt != "" {
		fmt.Printf("  completed: %s\n", work.CompletedAt)
	}
	if work.ErrorMessage != "" {
		fmt.Printf("  error: %s\n", work.ErrorMessage)
	}
	if response.Worker != nil {
		worker := response.Worker
		fmt.Printf("  worker: %s %s [%s/%s]\n", worker.ID, worker.Name, worker.WorkerType, worker.Status)
		if worker.LastSeenAt != "" {
			fmt.Printf("    last_seen: %s\n", worker.LastSeenAt)
		}
		if worker.LeaseExpiresAt != "" {
			fmt.Printf("    lease_expires: %s\n", worker.LeaseExpiresAt)
		}
	}
	if len(response.Reasons) > 0 {
		fmt.Println("  reasons:")
		for _, reason := range response.Reasons {
			fmt.Printf("    - %s\n", reason)
		}
	}
	if len(response.Actions) > 0 {
		fmt.Println("  actions:")
		for _, action := range response.Actions {
			fmt.Printf("    - %s\n", action)
		}
	}
}

func printWorkerWorkDiagnostics(response map[string]any) {
	if len(response) == 0 {
		return
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	var decoded struct {
		Error       string                  `json:"error"`
		Invocation  tools.WorkInvocation    `json:"invocation"`
		Matches     int                     `json:"matches"`
		Diagnostics []workerDiagnosisResult `json:"diagnostics"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return
	}
	if len(decoded.Diagnostics) == 0 {
		return
	}
	if decoded.Error != "" {
		fmt.Printf("worker selection failed: %s\n", decoded.Error)
	}
	printWorkerDiagnosis(workerDiagnoseResponse{
		Invocation:  decoded.Invocation,
		Matches:     decoded.Matches,
		Diagnostics: decoded.Diagnostics,
	})
}
