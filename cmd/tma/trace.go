package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/sdk/tma"
)

type turnTraceResponse = tma.TurnTrace
type turnTraceSpan = tma.TurnTraceSpan

func commandTrace(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("trace command requires a subcommand")
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("trace list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var query tma.TraceListQuery
		var limit int
		flags.StringVar(&query.WorkspaceID, "workspace", "", "workspace id")
		flags.StringVar(&query.SessionID, "session", "", "session id")
		flags.StringVar(&query.TurnID, "turn", "", "turn id")
		flags.StringVar(&query.SessionStatus, "status", "", "session status")
		flags.BoolVar(&query.IncludeArchived, "include-archived", false, "include archived sessions")
		flags.IntVar(&limit, "limit", 50, "page size")
		flags.StringVar(&query.Cursor, "cursor", "", "opaque cursor from the previous page")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if limit <= 0 || limit > 100 {
			return fmt.Errorf("trace list --limit must be between 1 and 100")
		}
		query.Limit = int32(limit)
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		page, err := sdk.Traces.List(context.Background(), query)
		if err != nil {
			return err
		}
		return printJSON(page)
	case "show":
		flags := flag.NewFlagSet("trace show", flag.ContinueOnError)
		flags.SetOutput(io.Discard)

		var sessionID string
		var turnID string
		var traceID string
		var asJSON bool
		flags.StringVar(&sessionID, "session", "", "session id")
		flags.StringVar(&turnID, "turn", "", "turn id")
		flags.StringVar(&traceID, "trace", "", "trace id")
		flags.BoolVar(&asJSON, "json", false, "print raw JSON")

		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if (sessionID == "") == (traceID == "") {
			return fmt.Errorf("trace show requires exactly one of --trace or --session")
		}
		if traceID != "" && turnID != "" {
			return fmt.Errorf("trace show --turn can only be used with --session")
		}

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		var response tma.TurnTrace
		if traceID != "" {
			response, err = sdk.Traces.Get(context.Background(), traceID)
		} else {
			response, err = sdk.Traces.GetSession(context.Background(), sessionID, turnID)
		}
		if err != nil {
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

		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Traces.Export(context.Background(), sessionID, turnID, format)
		if err != nil {
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
