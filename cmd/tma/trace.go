package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/internal/observability"
)

type turnTraceResponse struct {
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Status    string          `json:"status,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Stats     turnTraceStats  `json:"stats,omitempty"`
	Graph     turnTraceGraph  `json:"graph,omitempty"`
	Steps     []turnTraceStep `json:"steps"`
	Spans     []turnTraceSpan `json:"spans,omitempty"`
}

type turnTraceStats struct {
	DurationMillis   int64 `json:"duration_ms"`
	StepCount        int   `json:"step_count"`
	SpanCount        int   `json:"span_count"`
	ToolCalls        int   `json:"tool_calls"`
	PendingApprovals int   `json:"pending_approvals"`
	Errors           int   `json:"errors"`
}

type turnTraceGraph struct {
	RootSpanIDs                []string            `json:"root_span_ids,omitempty"`
	Edges                      []turnTraceSpanEdge `json:"edges,omitempty"`
	CriticalSpanIDs            []string            `json:"critical_span_ids,omitempty"`
	CriticalPathDurationMillis int64               `json:"critical_path_duration_ms,omitempty"`
	MaxDepth                   int                 `json:"max_depth,omitempty"`
}

type turnTraceSpanEdge struct {
	ParentSpanID string `json:"parent_span_id"`
	ChildSpanID  string `json:"child_span_id"`
}

type turnTraceSpan struct {
	SpanID             string `json:"span_id"`
	ParentSpanID       string `json:"parent_span_id,omitempty"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	Status             string `json:"status,omitempty"`
	Depth              int    `json:"depth,omitempty"`
	StartOffsetMillis  int64  `json:"start_offset_ms,omitempty"`
	DurationMillis     int64  `json:"duration_ms"`
	SelfDurationMillis int64  `json:"self_duration_ms,omitempty"`
	Critical           bool   `json:"critical,omitempty"`
	EventCount         int    `json:"event_count,omitempty"`
}

type turnTraceStep struct {
	Seq            int64               `json:"seq"`
	Type           string              `json:"type"`
	CreatedAt      time.Time           `json:"created_at"`
	Message        string              `json:"message,omitempty"`
	Summary        string              `json:"summary,omitempty"`
	CallID         string              `json:"call_id,omitempty"`
	Identifier     string              `json:"identifier,omitempty"`
	APIName        string              `json:"api_name,omitempty"`
	Outcome        string              `json:"outcome,omitempty"`
	ApprovalSource string              `json:"approval_source,omitempty"`
	DecisionReason string              `json:"decision_reason,omitempty"`
	ArtifactError  string              `json:"artifact_error,omitempty"`
	Artifacts      []turnTraceArtifact `json:"artifacts,omitempty"`
}

type turnTraceArtifact struct {
	ArtifactID   string `json:"artifact_id,omitempty"`
	ObjectRefID  string `json:"object_ref_id,omitempty"`
	Name         string `json:"name,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	DownloadPath string `json:"download_path,omitempty"`
}

func commandTrace(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("trace command requires a subcommand")
	}

	switch args[0] {
	case "show":
		flags := flag.NewFlagSet("trace show", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var turnID string
		var asJSON bool
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.BoolVar(&asJSON, "json", false, "print raw JSON")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("trace show requires --session")
		}

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/trace"
		if turnID != "" {
			path += "?turn_id=" + url.QueryEscape(turnID)
		}

		var response turnTraceResponse
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if asJSON {
			return printJSON(response)
		}
		return printTrace(response, os.Stdout)
	case "export":
		flags := flag.NewFlagSet("trace export", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var turnID string
		var format string
		var outputPath string
		var otlpEndpoint string
		var otlpToken string
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.StringVar(&format, "format", "", "perfetto | otel | json")
		flags.StringVar(&outputPath, "output", "", "write export to file")
		flags.StringVar(&otlpEndpoint, "otlp-endpoint", observability.DefaultOTLPEndpoint(), "push OTel export to an OTLP/HTTP endpoint")
		flags.StringVar(&otlpToken, "otlp-token", defaultOTLPToken(), "bearer token for OTLP/HTTP push")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if sessionID == "" {
			return fmt.Errorf("trace export requires --session")
		}
		format = strings.TrimSpace(strings.ToLower(format))
		otlpEndpoint = strings.TrimSpace(otlpEndpoint)
		if format == "" {
			if otlpEndpoint != "" {
				format = "otel"
			} else {
				format = "perfetto"
			}
		}
		if otlpEndpoint != "" && format != "otel" && format != "otlp" {
			return fmt.Errorf("trace export --otlp-endpoint requires --format otel")
		}

		path := "/v1/sessions/" + url.PathEscape(sessionID) + "/trace"
		query := url.Values{}
		if turnID != "" {
			query.Set("turn_id", turnID)
		}
		if format != "" {
			query.Set("format", format)
		}
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}

		var response any
		if err := client.do(http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if strings.TrimSpace(outputPath) != "" {
			if err := writeJSONFile(outputPath, response); err != nil {
				return err
			}
		}
		if otlpEndpoint != "" {
			result, err := observability.PushOTLPHTTP(client.http, otlpEndpoint, otlpToken, response)
			if err != nil {
				return err
			}
			if outputPath != "" {
				fmt.Fprintf(os.Stdout, "pushed otel trace to %s (%s)\n", result.Endpoint, result.Status)
				return nil
			}
			return printJSON(result)
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown trace subcommand %q", args[0])
	}
}

func printTrace(trace turnTraceResponse, output io.Writer) error {
	if _, err := fmt.Fprintf(output, "trace session=%s turn=%s", trace.SessionID, trace.TurnID); err != nil {
		return err
	}
	if trace.Status != "" {
		if _, err := fmt.Fprintf(output, " status=%s", trace.Status); err != nil {
			return err
		}
	}
	if trace.Stats.DurationMillis > 0 {
		if _, err := fmt.Fprintf(output, " duration=%dms", trace.Stats.DurationMillis); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(output); err != nil {
		return err
	}
	if trace.Stats.StepCount > 0 || trace.Stats.SpanCount > 0 || trace.Stats.ToolCalls > 0 || trace.Stats.PendingApprovals > 0 || trace.Stats.Errors > 0 {
		if _, err := fmt.Fprintf(output, "stats: steps=%d spans=%d tools=%d pending_approvals=%d errors=%d\n", trace.Stats.StepCount, trace.Stats.SpanCount, trace.Stats.ToolCalls, trace.Stats.PendingApprovals, trace.Stats.Errors); err != nil {
			return err
		}
	}
	if len(trace.Spans) > 0 || len(trace.Graph.Edges) > 0 || len(trace.Graph.CriticalSpanIDs) > 0 {
		if err := printTraceGraph(trace, output); err != nil {
			return err
		}
	}
	if trace.Summary != "" {
		if _, err := fmt.Fprintf(output, "summary:\n%s\n", indentTraceText(trace.Summary)); err != nil {
			return err
		}
	}
	if len(trace.Steps) > 0 {
		if _, err := fmt.Fprintln(output, "timeline:"); err != nil {
			return err
		}
	}
	for _, step := range trace.Steps {
		line := fmt.Sprintf("- seq=%d %s", step.Seq, step.Type)
		if step.Identifier != "" || step.APIName != "" {
			line += fmt.Sprintf(" %s.%s", defaultLabel(step.Identifier, "default"), step.APIName)
		}
		if step.Outcome != "" {
			line += " outcome=" + step.Outcome
		}
		if step.ApprovalSource != "" {
			line += " source=" + step.ApprovalSource
		}
		if step.DecisionReason != "" {
			line += " reason=" + step.DecisionReason
		}
		if _, err := fmt.Fprintln(output, line); err != nil {
			return err
		}
		if step.Message != "" {
			if _, err := fmt.Fprintf(output, "  %s\n", strings.ReplaceAll(step.Message, "\n", "\n  ")); err != nil {
				return err
			}
		}
		if len(step.Artifacts) > 0 {
			if _, err := fmt.Fprintln(output, "  artifacts:"); err != nil {
				return err
			}
			for _, artifact := range step.Artifacts {
				item := "    - " + defaultLabel(artifact.ArtifactID, "(unknown)")
				if artifact.Name != "" {
					item += " " + artifact.Name
				}
				if artifact.ArtifactType != "" {
					item += " [" + artifact.ArtifactType + "]"
				}
				if artifact.DownloadPath != "" {
					item += " download: " + artifact.DownloadPath
				}
				if _, err := fmt.Fprintln(output, item); err != nil {
					return err
				}
				if command := sessionArtifactDownloadCommand(artifact.DownloadPath); command != "" {
					if _, err := fmt.Fprintf(output, "      cli: %s\n", command); err != nil {
						return err
					}
				}
			}
		}
		if step.ArtifactError != "" {
			if _, err := fmt.Fprintf(output, "  artifact error: %s\n", step.ArtifactError); err != nil {
				return err
			}
		}
	}
	return nil
}

func printTraceGraph(trace turnTraceResponse, output io.Writer) error {
	if _, err := fmt.Fprintf(output, "graph: roots=%d edges=%d max_depth=%d critical_path=%dms critical_spans=%d\n", len(trace.Graph.RootSpanIDs), len(trace.Graph.Edges), trace.Graph.MaxDepth, trace.Graph.CriticalPathDurationMillis, len(trace.Graph.CriticalSpanIDs)); err != nil {
		return err
	}
	spansByID := make(map[string]turnTraceSpan, len(trace.Spans))
	for _, span := range trace.Spans {
		spansByID[span.SpanID] = span
	}
	if len(trace.Graph.CriticalSpanIDs) > 0 {
		if _, err := fmt.Fprintln(output, "critical path:"); err != nil {
			return err
		}
		for _, spanID := range trace.Graph.CriticalSpanIDs {
			span := spansByID[spanID]
			if span.SpanID == "" {
				span.SpanID = spanID
				span.Name = spanID
			}
			if _, err := fmt.Fprintf(output, "  - %s %s duration=%dms self=%dms\n", span.SpanID, defaultLabel(span.Name, "(unknown)"), span.DurationMillis, span.SelfDurationMillis); err != nil {
				return err
			}
		}
	}
	if len(trace.Spans) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(output, "spans:"); err != nil {
		return err
	}
	for _, span := range trace.Spans {
		prefix := strings.Repeat("  ", span.Depth)
		marker := "-"
		if span.Critical {
			marker = "*"
		}
		if _, err := fmt.Fprintf(output, "%s%s %s %s kind=%s status=%s duration=%dms self=%dms events=%d\n", prefix, marker, span.SpanID, defaultLabel(span.Name, "(unknown)"), defaultLabel(span.Kind, "unknown"), defaultLabel(span.Status, "unknown"), span.DurationMillis, span.SelfDurationMillis, span.EventCount); err != nil {
			return err
		}
	}
	return nil
}

func indentTraceText(text string) string {
	return "  " + strings.ReplaceAll(text, "\n", "\n  ")
}

func defaultLabel(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func writeJSONFile(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func defaultOTLPToken() string {
	if value := strings.TrimSpace(os.Getenv("TMA_OTEL_EXPORTER_OTLP_TOKEN")); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TOKEN"))
}
