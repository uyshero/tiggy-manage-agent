package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

type sessionArtifactListResponse struct {
	Artifacts []sessionArtifactSummary `json:"artifacts"`
}

type sessionArtifactSummary struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	SessionID     string          `json:"session_id"`
	EnvironmentID string          `json:"environment_id"`
	ObjectRefID   string          `json:"object_ref_id"`
	TurnID        string          `json:"turn_id"`
	ToolCallID    string          `json:"tool_call_id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	ArtifactType  string          `json:"artifact_type"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     string          `json:"created_at"`
}

func commandObject(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("object command requires a subcommand")
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("object create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var workspaceID string
		var storageProvider string
		var bucket string
		var objectKey string
		var objectVersion string
		var contentType string
		var sizeBytes int64
		var checksumSHA256 string
		var etag string
		var visibility string
		var metadata string
		var createdBy string
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		flags.StringVar(&storageProvider, "storage-provider", "", "storage provider")
		flags.StringVar(&bucket, "bucket", "", "object storage bucket")
		flags.StringVar(&objectKey, "key", "", "object storage key")
		flags.StringVar(&objectVersion, "version", "", "object version")
		flags.StringVar(&contentType, "content-type", "", "content type")
		flags.Int64Var(&sizeBytes, "size", 0, "object size in bytes")
		flags.StringVar(&checksumSHA256, "sha256", "", "sha256 checksum")
		flags.StringVar(&etag, "etag", "", "object etag")
		flags.StringVar(&visibility, "visibility", "", "session | workspace")
		flags.StringVar(&metadata, "metadata", "", "metadata JSON object")
		flags.StringVar(&createdBy, "created-by", "", "creator id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if bucket == "" || objectKey == "" {
			return fmt.Errorf("object create requires --bucket and --key")
		}
		rawMetadata, err := parseOptionalJSONObjectFlag(metadata, "metadata")
		if err != nil {
			return err
		}

		request := map[string]any{
			"bucket":     bucket,
			"object_key": objectKey,
			"size_bytes": sizeBytes,
		}
		setStringIfNotEmpty(request, "workspace_id", workspaceID)
		setStringIfNotEmpty(request, "storage_provider", storageProvider)
		setStringIfNotEmpty(request, "object_version", objectVersion)
		setStringIfNotEmpty(request, "content_type", contentType)
		setStringIfNotEmpty(request, "checksum_sha256", checksumSHA256)
		setStringIfNotEmpty(request, "etag", etag)
		setStringIfNotEmpty(request, "visibility", visibility)
		setStringIfNotEmpty(request, "created_by", createdBy)
		if rawMetadata != nil {
			request["metadata"] = rawMetadata
		}

		var response any
		if err := client.do(http.MethodPost, "/v1/object-refs", request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "get":
		flags := flag.NewFlagSet("object get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "object ref id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("object get requires --id")
		}

		var response any
		if err := client.do(http.MethodGet, "/v1/object-refs/"+url.PathEscape(id), nil, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "delete":
		flags := flag.NewFlagSet("object delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		flags.StringVar(&id, "id", "", "object ref id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("object delete requires --id")
		}

		if err := client.do(http.MethodDelete, "/v1/object-refs/"+url.PathEscape(id), nil, nil); err != nil {
			return err
		}
		fmt.Printf("deleted object ref %s\n", id)
		return nil
	case "download":
		flags := flag.NewFlagSet("object download", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var id string
		var sessionID string
		var outputPath string
		flags.StringVar(&id, "id", "", "object ref id")
		flags.StringVar(&sessionID, "session", "", "session id for visibility check")
		flags.StringVar(&outputPath, "output", "", "output file path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("object download requires --id")
		}

		path := "/v1/object-refs/" + url.PathEscape(id) + "/download"
		if sessionID != "" {
			path += "?session_id=" + url.QueryEscape(sessionID)
		}

		var writer io.Writer = os.Stdout
		if outputPath != "" {
			file, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer file.Close()
			writer = file
		}

		return client.download(path, writer)
	default:
		return fmt.Errorf("unknown object subcommand %q", args[0])
	}
}

func commandSessionArtifact(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("session artifact command requires a subcommand")
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("session artifact create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var objectRefID string
		var environmentID string
		var turnID string
		var toolCallID string
		var name string
		var description string
		var artifactType string
		var metadata string
		var createdBy string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&objectRefID, "object", "", "object ref id")
		flags.StringVar(&environmentID, "env", "", "environment id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.StringVar(&toolCallID, "call", "", "tool call id")
		flags.StringVar(&name, "name", "", "artifact name")
		flags.StringVar(&description, "description", "", "artifact description")
		flags.StringVar(&artifactType, "type", "", "file | snapshot | asset")
		flags.StringVar(&metadata, "metadata", "", "metadata JSON object")
		flags.StringVar(&createdBy, "created-by", "", "creator id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" || objectRefID == "" {
			return fmt.Errorf("session artifact create requires --session and --object")
		}
		rawMetadata, err := parseOptionalJSONObjectFlag(metadata, "metadata")
		if err != nil {
			return err
		}

		request := map[string]any{
			"object_ref_id": objectRefID,
		}
		setStringIfNotEmpty(request, "environment_id", environmentID)
		setStringIfNotEmpty(request, "turn_id", turnID)
		setStringIfNotEmpty(request, "tool_call_id", toolCallID)
		setStringIfNotEmpty(request, "name", name)
		setStringIfNotEmpty(request, "description", description)
		setStringIfNotEmpty(request, "artifact_type", artifactType)
		setStringIfNotEmpty(request, "created_by", createdBy)
		if rawMetadata != nil {
			request["metadata"] = rawMetadata
		}

		var response any
		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/artifacts"
		if err := client.do(http.MethodPost, path, request, &response); err != nil {
			return err
		}
		return printJSON(response)
	case "list":
		flags := flag.NewFlagSet("session artifact list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var jsonOutput bool
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.BoolVar(&jsonOutput, "json", false, "print raw JSON response")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("session artifact list requires --session")
		}

		var response sessionArtifactListResponse
		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/artifacts"
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(response)
		}
		printSessionArtifactList(sessionID, response.Artifacts)
		return nil
	case "delete":
		flags := flag.NewFlagSet("session artifact delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var artifactID string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&artifactID, "artifact", "", "artifact id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" || artifactID == "" {
			return fmt.Errorf("session artifact delete requires --session and --artifact")
		}

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/artifacts/" + url.PathEscape(artifactID)
		if err := client.do(http.MethodDelete, path, nil, nil); err != nil {
			return err
		}
		fmt.Printf("deleted session artifact %s\n", artifactID)
		return nil
	case "download":
		flags := flag.NewFlagSet("session artifact download", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var artifactID string
		var outputPath string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&artifactID, "artifact", "", "artifact id")
		flags.StringVar(&outputPath, "output", "", "output file path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" || artifactID == "" {
			return fmt.Errorf("session artifact download requires --session and --artifact")
		}

		var writer io.Writer = os.Stdout
		if outputPath != "" {
			file, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer file.Close()
			writer = file
		}

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/artifacts/" + url.PathEscape(artifactID) + "/download"
		if err := client.download(path, writer); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown session artifact subcommand %q", args[0])
	}
}

func printSessionArtifactList(sessionID string, artifacts []sessionArtifactSummary) {
	fmt.Printf("session artifacts: %s\n", sessionID)
	if len(artifacts) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, artifact := range artifacts {
		name := artifact.Name
		if name == "" {
			name = "(unnamed)"
		}
		artifactType := artifact.ArtifactType
		if artifactType == "" {
			artifactType = "artifact"
		}
		fmt.Printf("  - %s %s [%s]\n", artifact.ID, name, artifactType)
		if artifact.ObjectRefID != "" {
			fmt.Printf("    object: %s\n", artifact.ObjectRefID)
		}
		if artifact.TurnID != "" || artifact.ToolCallID != "" {
			fmt.Printf("    turn: %s", artifact.TurnID)
			if artifact.ToolCallID != "" {
				fmt.Printf(" call: %s", artifact.ToolCallID)
			}
			fmt.Println()
		}
		if artifact.Description != "" {
			fmt.Printf("    description: %s\n", artifact.Description)
		}
		if artifact.ID != "" {
			fmt.Printf("    download: /v1/sessions/%s/artifacts/%s/download\n", sessionID, artifact.ID)
		}
	}
}

func parseOptionalJSONObjectFlag(value string, name string) (json.RawMessage, error) {
	if value == "" {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, fmt.Errorf("invalid --%s JSON object: %w", name, err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("encode --%s JSON object: %w", name, err)
	}
	return json.RawMessage(encoded), nil
}

func setStringIfNotEmpty(values map[string]any, key string, value string) {
	if value != "" {
		values[key] = value
	}
}
