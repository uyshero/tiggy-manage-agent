package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"tiggy-manage-agent/internal/managedagents"
)

const artifactCompletionValidator = "builtin.artifact_delivery"

var (
	workspaceCodePathPattern = regexp.MustCompile("`(/workspace/[^`\\r\\n]+)`")
	workspacePathPattern     = regexp.MustCompile("/workspace/[^\\s`\\\"'<>]+")
)

type SessionArtifactCompletionReader interface {
	ListSessionArtifacts(string) ([]managedagents.SessionArtifact, error)
}

// ArtifactCompletionGate prevents a response from publishing a workspace file
// path that was not persisted as a Session artifact.
type ArtifactCompletionGate struct {
	Reader SessionArtifactCompletionReader
}

func (gate ArtifactCompletionGate) Validate(ctx context.Context, candidate CompletionCandidate) (CompletionVerdict, error) {
	referenced := referencedWorkspaceFiles(responseText(candidate))
	if len(referenced) == 0 {
		return artifactCompletionPass(nil), nil
	}
	if gate.Reader == nil {
		return CompletionVerdict{Validator: artifactCompletionValidator}, errors.New("artifact completion gate requires a session artifact reader")
	}

	artifacts, err := managedagents.ListSessionArtifactsWithContext(ctx, gate.Reader, candidate.SessionID)
	if err != nil {
		return CompletionVerdict{Validator: artifactCompletionValidator}, fmt.Errorf("list session artifacts: %w", err)
	}
	available := artifactCompletionPaths(artifacts)
	missing := make([]string, 0, len(referenced))
	for _, referencedPath := range referenced {
		if !artifactPathAvailable(referencedPath, available) {
			missing = append(missing, referencedPath)
		}
	}
	if len(missing) == 0 {
		return artifactCompletionPass(referenced), nil
	}

	return CompletionVerdict{
		Outcome:   CompletionOutcomeRetry,
		Validator: artifactCompletionValidator,
		Reason:    fmt.Sprintf("%d referenced workspace file(s) are not persisted as session artifacts", len(missing)),
		Feedback: fmt.Sprintf(
			"Completion is blocked because these final files are referenced but are not registered as Session artifacts: %s. Verify that each file exists, then call default.run_command with output_paths containing these exact /workspace paths (a no-op verification command is sufficient). Provide the final response only after the tool result includes the exported artifacts.",
			strings.Join(missing, ", "),
		),
		Evidence: map[string]any{
			"referenced_workspace_files": referenced,
			"missing_artifact_paths":     missing,
			"session_artifact_count":     len(artifacts),
		},
	}, nil
}

func artifactCompletionPass(referenced []string) CompletionVerdict {
	evidence := map[string]any{"referenced_workspace_file_count": len(referenced)}
	if len(referenced) > 0 {
		evidence["referenced_workspace_files"] = referenced
	}
	return CompletionVerdict{
		Outcome:   CompletionOutcomePass,
		Validator: artifactCompletionValidator,
		Evidence:  evidence,
	}
}

func responseText(candidate CompletionCandidate) string {
	var text strings.Builder
	for _, part := range candidate.Response.Message.Content {
		if part.Type != "" && part.Type != "text" {
			continue
		}
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(part.Text)
	}
	return text.String()
}

func referencedWorkspaceFiles(text string) []string {
	seen := map[string]bool{}
	add := func(raw string) {
		normalized := normalizeReferencedWorkspacePath(raw)
		if normalized != "" {
			seen[normalized] = true
		}
	}
	for _, match := range workspaceCodePathPattern.FindAllStringSubmatch(text, -1) {
		add(match[1])
	}
	for _, match := range workspacePathPattern.FindAllString(text, -1) {
		add(match)
	}
	paths := make([]string, 0, len(seen))
	for value := range seen {
		paths = append(paths, value)
	}
	sort.Strings(paths)
	return paths
}

func normalizeReferencedWorkspacePath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRightFunc(value, unicode.IsPunct)
	if value == "" || strings.ContainsAny(value, "*?[") {
		return ""
	}
	value = path.Clean(value)
	if !strings.HasPrefix(value, "/workspace/") || path.Ext(value) == "" {
		return ""
	}
	return value
}

func artifactCompletionPaths(artifacts []managedagents.SessionArtifact) map[string]bool {
	available := map[string]bool{}
	for _, artifact := range artifacts {
		addArtifactCompletionPath(available, artifact.Name)
		var metadata map[string]any
		if len(artifact.Metadata) == 0 || json.Unmarshal(artifact.Metadata, &metadata) != nil {
			continue
		}
		for _, key := range []string{"path", "file_path", "workspace_path"} {
			if value, ok := metadata[key].(string); ok {
				addArtifactCompletionPath(available, value)
			}
		}
	}
	return available
}

func addArtifactCompletionPath(available map[string]bool, value string) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return
	}
	if strings.HasPrefix(value, "/workspace/") {
		available[path.Clean(value)] = true
	} else if !path.IsAbs(value) {
		available[path.Clean("/workspace/"+value)] = true
	}
	available[path.Base(value)] = true
}

func artifactPathAvailable(referenced string, available map[string]bool) bool {
	return available[referenced] || available[path.Base(referenced)]
}
