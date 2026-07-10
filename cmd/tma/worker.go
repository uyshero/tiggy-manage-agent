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

type workerListResponse struct {
	Workers []workerSummary `json:"workers"`
}

type workerSummary struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	Name           string          `json:"name"`
	WorkerType     string          `json:"worker_type"`
	Status         string          `json:"status"`
	Capabilities   json.RawMessage `json:"capabilities,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	RegisteredBy   string          `json:"registered_by"`
	RegisteredAt   string          `json:"registered_at"`
	LastSeenAt     string          `json:"last_seen_at,omitempty"`
	LeaseExpiresAt string          `json:"lease_expires_at,omitempty"`
	ArchivedAt     string          `json:"archived_at,omitempty"`
}

type workerDiagnoseResponse struct {
	Invocation  tools.WorkInvocation    `json:"invocation"`
	Matches     int                     `json:"matches"`
	Diagnostics []workerDiagnosisResult `json:"diagnostics"`
}

type workerDiagnosisResult struct {
	WorkerID       string   `json:"worker_id"`
	WorkspaceID    string   `json:"workspace_id"`
	Name           string   `json:"name"`
	WorkerType     string   `json:"worker_type"`
	Status         string   `json:"status"`
	Match          bool     `json:"match"`
	Reasons        []string `json:"reasons,omitempty"`
	Runtimes       []string `json:"runtimes,omitempty"`
	APIs           []string `json:"apis,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	LeaseExpiresAt string   `json:"lease_expires_at,omitempty"`
	LastSeenAt     string   `json:"last_seen_at,omitempty"`
	RegisteredBy   string   `json:"registered_by,omitempty"`
}

func commandWorker(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("worker command requires a subcommand")
	}

	switch args[0] {
	case "register":
		flags := flag.NewFlagSet("worker register", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workspaceID string
		var name string
		var workerType string
		var capabilities string
		var metadata string
		var registeredBy string
		var leaseSeconds int
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		flags.StringVar(&name, "name", "", "worker name")
		flags.StringVar(&workerType, "type", "", "local | shared | cloud")
		flags.StringVar(&capabilities, "capabilities", "", "capabilities JSON object")
		flags.StringVar(&metadata, "metadata", "", "metadata JSON object")
		flags.StringVar(&registeredBy, "registered-by", "", "registrar id")
		flags.IntVar(&leaseSeconds, "lease-seconds", 0, "heartbeat lease seconds")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("worker register requires --name")
		}
		rawCapabilities, err := parseOptionalJSONObjectFlag(capabilities, "capabilities")
		if err != nil {
			return err
		}
		rawMetadata, err := parseOptionalJSONObjectFlag(metadata, "metadata")
		if err != nil {
			return err
		}

		request := map[string]any{"name": name}
		setStringIfNotEmpty(request, "workspace_id", workspaceID)
		setStringIfNotEmpty(request, "worker_type", workerType)
		setStringIfNotEmpty(request, "registered_by", registeredBy)
		if leaseSeconds > 0 {
			request["lease_seconds"] = leaseSeconds
		}
		if rawCapabilities != nil {
			request["capabilities"] = rawCapabilities
		}
		if rawMetadata != nil {
			request["metadata"] = rawMetadata
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/workers", request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "list":
		flags := flag.NewFlagSet("worker list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workspaceID string
		var status string
		var jsonOutput bool
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		flags.StringVar(&status, "status", "", "worker status")
		flags.BoolVar(&jsonOutput, "json", false, "print raw JSON response")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}

		query := url.Values{}
		if workspaceID != "" {
			query.Set("workspace_id", workspaceID)
		}
		if status != "" {
			query.Set("status", status)
		}
		path := "/v1/workers"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}

		var response workerListResponse
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(response)
		}
		printWorkerList(response.Workers)
		return nil
	case "get":
		flags := flag.NewFlagSet("worker get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "worker id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("worker get requires --id")
		}

		var response any
		if err := client.do(http.MethodGet, "/v1/workers/"+url.PathEscape(id), nil, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "heartbeat":
		flags := flag.NewFlagSet("worker heartbeat", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		var status string
		var capabilities string
		var metadata string
		var leaseSeconds int
		flags.StringVar(&id, "id", "", "worker id")
		flags.StringVar(&status, "status", "", "online | offline | draining")
		flags.StringVar(&capabilities, "capabilities", "", "capabilities JSON object")
		flags.StringVar(&metadata, "metadata", "", "metadata JSON object")
		flags.IntVar(&leaseSeconds, "lease-seconds", 0, "heartbeat lease seconds")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("worker heartbeat requires --id")
		}
		rawCapabilities, err := parseOptionalJSONObjectFlag(capabilities, "capabilities")
		if err != nil {
			return err
		}
		rawMetadata, err := parseOptionalJSONObjectFlag(metadata, "metadata")
		if err != nil {
			return err
		}

		request := map[string]any{}
		setStringIfNotEmpty(request, "status", status)
		if leaseSeconds > 0 {
			request["lease_seconds"] = leaseSeconds
		}
		if rawCapabilities != nil {
			request["capabilities"] = rawCapabilities
		}
		if rawMetadata != nil {
			request["metadata"] = rawMetadata
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/workers/"+url.PathEscape(id)+"/heartbeat", request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "archive":
		flags := flag.NewFlagSet("worker archive", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "worker id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("worker archive requires --id")
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/workers/"+url.PathEscape(id)+"/archive", map[string]any{}, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "reap-expired":
		flags := flag.NewFlagSet("worker reap-expired", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var limit int
		flags.IntVar(&limit, "limit", 0, "maximum expired workers to mark offline")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		request := map[string]any{}
		if limit > 0 {
			request["limit"] = limit
		}
		var response any
		if err := client.do(http.MethodPost, "/v1/workers/reap-expired", request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "diagnose":
		flags := flag.NewFlagSet("worker diagnose", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workspaceID string
		var namespace string
		var api string
		var capabilities string
		var runtime string
		var input string
		var jsonOutput bool
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		flags.StringVar(&namespace, "namespace", tools.NamespaceDefault, "tool namespace")
		flags.StringVar(&api, "api", "", "tool api")
		flags.StringVar(&capabilities, "capabilities", "", "comma-separated required capabilities")
		flags.StringVar(&runtime, "runtime", tools.ToolRuntimeLocalSystem, "auto | cloud_sandbox | local_system")
		flags.StringVar(&input, "input", "{}", "tool input JSON object")
		flags.BoolVar(&jsonOutput, "json", false, "print raw JSON diagnosis")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(api) == "" {
			return fmt.Errorf("worker diagnose requires --api")
		}
		rawInput, err := parseOptionalJSONObjectFlag(input, "input")
		if err != nil {
			return err
		}
		if rawInput == nil {
			rawInput = json.RawMessage(`{}`)
		}
		invocation := tools.WorkInvocation{
			ProtocolVersion: tools.WorkProtocolVersion,
			Namespace:       namespace,
			API:             api,
			Capabilities:    splitCSV(capabilities),
			Runtime:         runtime,
			Input:           rawInput,
		}
		if err := tools.ValidateWorkInvocation(invocation); err != nil {
			return err
		}
		request := map[string]any{
			"namespace":    namespace,
			"api":          api,
			"runtime":      runtime,
			"capabilities": splitCSV(capabilities),
			"input":        rawInput,
		}
		setStringIfNotEmpty(request, "workspace_id", workspaceID)
		var response workerDiagnoseResponse
		if err := client.do(http.MethodPost, "/v1/workers/diagnose", request, &response); err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(response)
		}
		printWorkerDiagnosis(response)
		return nil
	default:
		return fmt.Errorf("unknown worker subcommand %q", args[0])
	}
}

func printWorkerList(workers []workerSummary) {
	fmt.Println("workers:")
	if len(workers) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, worker := range workers {
		fmt.Printf("  %s %s [%s/%s]\n", worker.ID, worker.Name, worker.WorkerType, worker.Status)
		fmt.Printf("    workspace: %s\n", worker.WorkspaceID)
		if worker.LastSeenAt != "" {
			fmt.Printf("    last_seen: %s\n", worker.LastSeenAt)
		}
		if worker.LeaseExpiresAt != "" {
			fmt.Printf("    lease_expires: %s\n", worker.LeaseExpiresAt)
		}
		printWorkerCapabilities(worker.Capabilities, "    ")
	}
}

func printWorkerCapabilities(raw json.RawMessage, indent string) {
	capabilities, err := tools.DecodeWorkerCapabilities(raw)
	if err != nil {
		if len(raw) > 0 {
			fmt.Printf("%scapabilities: invalid (%v)\n", indent, err)
		}
		return
	}
	if len(capabilities.Runtimes) > 0 {
		fmt.Printf("%sruntimes: %s\n", indent, strings.Join(capabilities.Runtimes, ", "))
	}
	if len(capabilities.APIs) > 0 {
		fmt.Printf("%sapis: %s\n", indent, strings.Join(capabilities.APIs, ", "))
	}
	if len(capabilities.Capabilities) > 0 {
		fmt.Printf("%scapabilities: %s\n", indent, strings.Join(capabilities.Capabilities, ", "))
	}
}

func printWorkerDiagnosis(response workerDiagnoseResponse) {
	invocation := response.Invocation
	fmt.Printf("diagnose %s.%s runtime=%s capabilities=%s\n",
		invocation.Namespace,
		invocation.API,
		invocation.Runtime,
		strings.Join(invocation.Capabilities, ","),
	)
	if len(response.Diagnostics) == 0 {
		fmt.Println("  no online workers returned by server")
		return
	}
	for _, diagnosis := range response.Diagnostics {
		status := "no"
		if diagnosis.Match {
			status = "yes"
		}
		fmt.Printf("  %s %s [%s/%s] match=%s\n", diagnosis.WorkerID, diagnosis.Name, diagnosis.WorkerType, diagnosis.Status, status)
		if len(diagnosis.Reasons) == 0 {
			fmt.Println("    reasons: (match)")
		} else {
			fmt.Printf("    reasons: %s\n", strings.Join(diagnosis.Reasons, "; "))
		}
		printWorkerDiagnosisCapabilities(diagnosis, "    ")
	}
}

func printWorkerDiagnosisCapabilities(diagnosis workerDiagnosisResult, indent string) {
	if len(diagnosis.Runtimes) > 0 {
		fmt.Printf("%sruntimes: %s\n", indent, strings.Join(diagnosis.Runtimes, ", "))
	}
	if len(diagnosis.APIs) > 0 {
		fmt.Printf("%sapis: %s\n", indent, strings.Join(diagnosis.APIs, ", "))
	}
	if len(diagnosis.Capabilities) > 0 {
		fmt.Printf("%scapabilities: %s\n", indent, strings.Join(diagnosis.Capabilities, ", "))
	}
}
