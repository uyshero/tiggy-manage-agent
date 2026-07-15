package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/sdk/tma"
)

type workerListResponse struct {
	Workers []workerSummary `json:"workers"`
}

type workerSummary = tma.Worker
type workerDiagnoseResponse = tma.WorkerDiagnoseResponse
type workerDiagnosisResult = tma.WorkerDiagnosisResult

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

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		workers, err := sdk.Workers.List(context.Background(), tma.WorkerListQuery{WorkspaceID: workspaceID, Status: status})
		if err != nil {
			return err
		}
		response := workerListResponse{Workers: workers}
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

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Workers.Get(context.Background(), id)
		if err != nil {
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

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Workers.Archive(context.Background(), id)
		if err != nil {
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
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Workers.ReapExpired(context.Background(), tma.ReapExpiredWorkersRequest{Limit: limit})
		if err != nil {
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
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Workers.Diagnose(context.Background(), tma.WorkerDiagnoseRequest{
			WorkspaceID: workspaceID, Namespace: namespace, API: api, Runtime: runtime,
			Capabilities: splitCSV(capabilities), Input: rawInput,
		})
		if err != nil {
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
		if worker.LastSeenAt != nil {
			fmt.Printf("    last_seen: %s\n", worker.LastSeenAt.UTC().Format(time.RFC3339))
		}
		if worker.LeaseExpiresAt != nil {
			fmt.Printf("    lease_expires: %s\n", worker.LeaseExpiresAt.UTC().Format(time.RFC3339))
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
	printWorkerPluginManifests(capabilities.Manifests, indent)
}

func printWorkerPluginManifests(manifests []tools.Manifest, indent string) {
	if len(manifests) == 0 {
		return
	}
	parts := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		apis := make([]string, 0, len(manifest.API))
		for _, api := range manifest.API {
			apiName := api.APIName
			if strings.TrimSpace(apiName) == "" {
				apiName = api.Name
			}
			if strings.TrimSpace(apiName) != "" {
				apis = append(apis, apiName)
			}
		}
		if len(apis) == 0 {
			parts = append(parts, manifest.Identifier)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", manifest.Identifier, strings.Join(apis, ", ")))
	}
	fmt.Printf("%stool_manifests: %s\n", indent, strings.Join(parts, "; "))
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
