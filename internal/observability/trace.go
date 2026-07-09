package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type TurnTrace struct {
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	TraceID   string          `json:"trace_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Stats     TurnTraceStats  `json:"stats,omitempty"`
	Turns     []TraceTurnInfo `json:"turns,omitempty"`
	Steps     []TraceStep     `json:"steps"`
	Spans     []TraceSpan     `json:"spans,omitempty"`
}

type TurnTraceStats struct {
	StartTime        time.Time `json:"start_time,omitempty"`
	EndTime          time.Time `json:"end_time,omitempty"`
	DurationMillis   int64     `json:"duration_ms"`
	StepCount        int       `json:"step_count"`
	SpanCount        int       `json:"span_count"`
	LLMRequests      int       `json:"llm_requests"`
	ToolCalls        int       `json:"tool_calls"`
	ApprovalWaits    int       `json:"approval_waits"`
	PendingApprovals int       `json:"pending_approvals"`
	Errors           int       `json:"errors"`
	ArtifactCount    int       `json:"artifact_count"`
}

type TraceTurnInfo struct {
	TurnID         string    `json:"turn_id"`
	Status         string    `json:"status,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	DurationMillis int64     `json:"duration_ms"`
	StepCount      int       `json:"step_count"`
	SpanCount      int       `json:"span_count"`
	ToolCalls      int       `json:"tool_calls"`
	Errors         int       `json:"errors"`
}

type TraceStep struct {
	Seq            int64           `json:"seq"`
	Type           string          `json:"type"`
	CreatedAt      time.Time       `json:"created_at"`
	Message        string          `json:"message,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	CallID         string          `json:"call_id,omitempty"`
	Identifier     string          `json:"identifier,omitempty"`
	APIName        string          `json:"api_name,omitempty"`
	Outcome        string          `json:"outcome,omitempty"`
	ApprovalSource string          `json:"approval_source,omitempty"`
	DecisionReason string          `json:"decision_reason,omitempty"`
	ArtifactError  string          `json:"artifact_error,omitempty"`
	Artifacts      []TraceArtifact `json:"artifacts,omitempty"`
}

type TraceArtifact struct {
	ArtifactID   string `json:"artifact_id,omitempty"`
	ObjectRefID  string `json:"object_ref_id,omitempty"`
	Name         string `json:"name,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	DownloadPath string `json:"download_path,omitempty"`
}

type TraceSpan struct {
	TraceID        string            `json:"trace_id"`
	SpanID         string            `json:"span_id"`
	ParentSpanID   string            `json:"parent_span_id,omitempty"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Status         string            `json:"status,omitempty"`
	StartSeq       int64             `json:"start_seq,omitempty"`
	EndSeq         int64             `json:"end_seq,omitempty"`
	StartTime      time.Time         `json:"start_time"`
	EndTime        time.Time         `json:"end_time"`
	DurationMillis int64             `json:"duration_ms"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type summaryStore interface {
	GetSessionSummary(sessionID string) (managedagents.SessionSummary, error)
	SaveSessionSummary(sessionID string, input managedagents.UpsertSessionSummaryInput) (managedagents.SessionSummary, error)
	ListEvents(sessionID string, afterSeq int64) ([]managedagents.Event, error)
}

func ProjectTurnTrace(sessionID string, turnID string, events []managedagents.Event) TurnTrace {
	if turnID == "" {
		turnID = latestTurnID(events)
	}
	trace := projectTurnTraceBase(sessionID, turnID, events)
	trace.Turns = BuildTurnCatalog(sessionID, events)
	if len(trace.Steps) == 0 {
		return trace
	}
	if trace.Status == "" {
		trace.Status = inferTraceStatus(trace.Steps)
	}
	trace.Summary = BuildTurnSummary(trace)
	trace.Spans = BuildTraceSpans(trace)
	trace.Stats = BuildTraceStats(trace)
	return trace
}

func BuildTurnCatalog(sessionID string, events []managedagents.Event) []TraceTurnInfo {
	turnIDs := orderedTurnIDs(events)
	if len(turnIDs) == 0 {
		return nil
	}
	turns := make([]TraceTurnInfo, 0, len(turnIDs))
	for index := len(turnIDs) - 1; index >= 0; index-- {
		base := projectTurnTraceBase(sessionID, turnIDs[index], events)
		if len(base.Steps) == 0 {
			continue
		}
		if base.Status == "" {
			base.Status = inferTraceStatus(base.Steps)
		}
		base.Summary = BuildTurnSummary(base)
		base.Spans = BuildTraceSpans(base)
		base.Stats = BuildTraceStats(base)
		turns = append(turns, TraceTurnInfo{
			TurnID:         base.TurnID,
			Status:         base.Status,
			Summary:        base.Summary,
			StartedAt:      base.Stats.StartTime,
			EndedAt:        base.Stats.EndTime,
			DurationMillis: base.Stats.DurationMillis,
			StepCount:      base.Stats.StepCount,
			SpanCount:      base.Stats.SpanCount,
			ToolCalls:      base.Stats.ToolCalls,
			Errors:         base.Stats.Errors,
		})
	}
	return turns
}

func BuildTraceStats(trace TurnTrace) TurnTraceStats {
	stats := TurnTraceStats{
		StepCount: len(trace.Steps),
		SpanCount: len(trace.Spans),
	}
	if len(trace.Steps) == 0 {
		return stats
	}
	stats.StartTime = firstStepTime(trace.Steps)
	stats.EndTime = lastStepTime(trace.Steps)
	stats.DurationMillis = durationMillis(stats.StartTime, stats.EndTime)
	pendingApprovals := map[string]struct{}{}
	for _, step := range trace.Steps {
		switch step.Type {
		case managedagents.EventRuntimeLLMRequest:
			stats.LLMRequests++
		case managedagents.EventRuntimeToolCall:
			stats.ToolCalls++
		case managedagents.EventRuntimeToolInterventionRequired:
			stats.ApprovalWaits++
			if step.CallID != "" {
				pendingApprovals[step.CallID] = struct{}{}
			}
		case managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
			if step.CallID != "" {
				delete(pendingApprovals, step.CallID)
			}
			if step.Type == managedagents.EventRuntimeToolInterventionRejected {
				stats.Errors++
			}
		case managedagents.EventRuntimeToolResult:
			stats.ArtifactCount += len(step.Artifacts)
			if step.CallID != "" && step.Outcome != "pending_intervention" {
				delete(pendingApprovals, step.CallID)
			}
			if step.Outcome == "error" || step.ArtifactError != "" {
				stats.Errors++
			}
		case managedagents.EventRuntimeFailed:
			stats.Errors++
		}
	}
	stats.PendingApprovals = len(pendingApprovals)
	return stats
}

func BuildTraceSpans(trace TurnTrace) []TraceSpan {
	if trace.TraceID == "" || trace.TurnID == "" || len(trace.Steps) == 0 {
		return nil
	}

	start := firstStepTime(trace.Steps)
	end := lastStepTime(trace.Steps)
	if end.Before(start) {
		end = start
	}
	if trace.Status == "" {
		trace.Status = inferTraceStatus(trace.Steps)
	}

	rootSpanID := spanIDFromKey("interaction:" + trace.TurnID)
	spans := []TraceSpan{{
		TraceID:        trace.TraceID,
		SpanID:         rootSpanID,
		Name:           "tma.interaction",
		Kind:           "interaction",
		Status:         defaultString(trace.Status, "running"),
		StartSeq:       trace.Steps[0].Seq,
		EndSeq:         trace.Steps[len(trace.Steps)-1].Seq,
		StartTime:      start,
		EndTime:        end,
		DurationMillis: durationMillis(start, end),
		Attributes: map[string]string{
			"session_id": trace.SessionID,
			"turn_id":    trace.TurnID,
			"status":     defaultString(trace.Status, "running"),
			"summary":    singleLineSummary(trace.Summary),
		},
	}}

	type openSpan struct {
		Step         TraceStep
		ParentSpanID string
	}

	var llmOpen []TraceStep
	toolOpen := map[string]TraceStep{}
	approvalOpen := map[string]openSpan{}
	var compactOpen []TraceStep

	appendPointSpan := func(step TraceStep) {
		spans = append(spans, traceSpanForStep(trace, rootSpanID, step, step.CreatedAt, step.CreatedAt, step.Seq, step.Seq, pointSpanStatus(step)))
	}

	appendPairedSpan := func(parentSpanID string, name string, kind string, status string, startStep TraceStep, endStep TraceStep, attrs map[string]string, key string) {
		startTime := startStep.CreatedAt
		if startTime.IsZero() {
			startTime = start
		}
		endTime := endStep.CreatedAt
		if endTime.IsZero() {
			endTime = end
		}
		attributes := cloneAttributes(attrs)
		attributes["start_event_type"] = startStep.Type
		attributes["end_event_type"] = endStep.Type
		attributes["start_event_seq"] = fmt.Sprintf("%d", startStep.Seq)
		attributes["end_event_seq"] = fmt.Sprintf("%d", endStep.Seq)
		if startStep.Message != "" {
			attributes["message"] = singleLineSummary(startStep.Message)
		}
		spans = append(spans, TraceSpan{
			TraceID:        trace.TraceID,
			SpanID:         spanIDFromKey(key),
			ParentSpanID:   parentSpanID,
			Name:           name,
			Kind:           kind,
			Status:         status,
			StartSeq:       startStep.Seq,
			EndSeq:         endStep.Seq,
			StartTime:      startTime,
			EndTime:        clampEnd(startTime, endTime),
			DurationMillis: durationMillis(startTime, clampEnd(startTime, endTime)),
			Attributes:     attributes,
		})
	}

	for _, step := range trace.Steps {
		switch step.Type {
		case managedagents.EventUserMessage, managedagents.EventAgentMessage, managedagents.EventRuntimeStarted, managedagents.EventRuntimeThinking, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
			appendPointSpan(step)
		case managedagents.EventRuntimeLLMRequest:
			llmOpen = append(llmOpen, step)
		case managedagents.EventRuntimeLLMResponse:
			if len(llmOpen) == 0 {
				appendPointSpan(step)
				continue
			}
			startStep := llmOpen[0]
			llmOpen = llmOpen[1:]
			appendPairedSpan(rootSpanID, "tma.llm", "llm", "ok", startStep, step, map[string]string{
				"model_request": singleLineSummary(startStep.Message),
				"model_reply":   singleLineSummary(step.Message),
			}, "llm:"+trace.TurnID+":"+fmt.Sprintf("%d", startStep.Seq))
		case managedagents.EventRuntimeToolCall:
			toolOpen[defaultString(step.CallID, fmt.Sprintf("tool-%d", step.Seq))] = step
		case managedagents.EventRuntimeToolInterventionRequired:
			callKey := defaultString(step.CallID, fmt.Sprintf("approval-%d", step.Seq))
			approvalOpen[callKey] = openSpan{
				Step:         step,
				ParentSpanID: toolSpanID(trace.TurnID, step),
			}
		case managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
			callKey := defaultString(step.CallID, fmt.Sprintf("approval-%d", step.Seq))
			open, ok := approvalOpen[callKey]
			if !ok {
				appendPointSpan(step)
				continue
			}
			delete(approvalOpen, callKey)
			appendPairedSpan(open.ParentSpanID, "tma.tool.blocked_on_user", "approval", approvalSpanStatus(step), open.Step, step, map[string]string{
				"tool_call_id":    step.CallID,
				"tool_identifier": defaultString(step.Identifier, defaultString(open.Step.Identifier, "default")),
				"tool_api":        defaultString(step.APIName, open.Step.APIName),
				"approval_source": step.ApprovalSource,
				"decision_reason": step.DecisionReason,
			}, "approval:"+trace.TurnID+":"+callKey)
		case managedagents.EventRuntimeToolResult:
			callKey := defaultString(step.CallID, fmt.Sprintf("tool-%d", step.Seq))
			startStep, ok := toolOpen[callKey]
			if !ok {
				appendPointSpan(step)
				continue
			}
			delete(toolOpen, callKey)
			attributes := map[string]string{
				"tool_call_id":    step.CallID,
				"tool_identifier": defaultString(step.Identifier, defaultString(startStep.Identifier, "default")),
				"tool_api":        defaultString(step.APIName, startStep.APIName),
				"outcome":         defaultString(step.Outcome, "unknown"),
				"decision_reason": step.DecisionReason,
			}
			if len(step.Artifacts) > 0 {
				attributes["artifact_count"] = fmt.Sprintf("%d", len(step.Artifacts))
			}
			if step.ArtifactError != "" {
				attributes["artifact_error"] = step.ArtifactError
			}
			appendPairedSpan(rootSpanID, toolSpanName(step, startStep), "tool", toolSpanStatus(step), startStep, step, attributes, "tool:"+trace.TurnID+":"+callKey)
		case managedagents.EventRuntimeContextCompacting:
			compactOpen = append(compactOpen, step)
		case managedagents.EventRuntimeContextCompacted, managedagents.EventRuntimeContextCompactionFailed:
			if len(compactOpen) == 0 {
				appendPointSpan(step)
				continue
			}
			startStep := compactOpen[0]
			compactOpen = compactOpen[1:]
			appendPairedSpan(rootSpanID, "tma.context.compact", "context", compactSpanStatus(step), startStep, step, nil, "context:"+trace.TurnID+":"+fmt.Sprintf("%d", startStep.Seq))
		}
	}

	for _, startStep := range llmOpen {
		appendPairedSpan(rootSpanID, "tma.llm", "llm", "open", startStep, syntheticTraceEndStep(end, trace.Steps[len(trace.Steps)-1].Seq), map[string]string{
			"model_request": singleLineSummary(startStep.Message),
		}, "llm:"+trace.TurnID+":"+fmt.Sprintf("%d", startStep.Seq))
	}

	for _, key := range sortedKeys(toolOpen) {
		startStep := toolOpen[key]
		appendPairedSpan(rootSpanID, toolSpanName(startStep, startStep), "tool", "open", startStep, syntheticTraceEndStep(end, trace.Steps[len(trace.Steps)-1].Seq), map[string]string{
			"tool_call_id":    startStep.CallID,
			"tool_identifier": defaultString(startStep.Identifier, "default"),
			"tool_api":        startStep.APIName,
		}, "tool:"+trace.TurnID+":"+key)
	}

	for _, key := range sortedKeys(approvalOpen) {
		open := approvalOpen[key]
		appendPairedSpan(open.ParentSpanID, "tma.tool.blocked_on_user", "approval", "waiting", open.Step, syntheticTraceEndStep(end, trace.Steps[len(trace.Steps)-1].Seq), map[string]string{
			"tool_call_id":    open.Step.CallID,
			"tool_identifier": defaultString(open.Step.Identifier, "default"),
			"tool_api":        open.Step.APIName,
		}, "approval:"+trace.TurnID+":"+key)
	}

	for _, startStep := range compactOpen {
		appendPairedSpan(rootSpanID, "tma.context.compact", "context", "open", startStep, syntheticTraceEndStep(end, trace.Steps[len(trace.Steps)-1].Seq), nil, "context:"+trace.TurnID+":"+fmt.Sprintf("%d", startStep.Seq))
	}

	sort.SliceStable(spans, func(i int, j int) bool {
		if spans[i].ParentSpanID == "" && spans[j].ParentSpanID != "" {
			return true
		}
		if spans[j].ParentSpanID == "" && spans[i].ParentSpanID != "" {
			return false
		}
		if !spans[i].StartTime.Equal(spans[j].StartTime) {
			return spans[i].StartTime.Before(spans[j].StartTime)
		}
		if spans[i].DurationMillis != spans[j].DurationMillis {
			return spans[i].DurationMillis > spans[j].DurationMillis
		}
		if spans[i].StartSeq != spans[j].StartSeq {
			return spans[i].StartSeq < spans[j].StartSeq
		}
		return spans[i].Name < spans[j].Name
	})
	return spans
}

func ExportPerfetto(trace TurnTrace) map[string]any {
	events := []map[string]any{
		{
			"name": "process_name",
			"ph":   "M",
			"pid":  trace.SessionID,
			"tid":  trace.TurnID,
			"args": map[string]any{"name": "session " + trace.SessionID},
		},
		{
			"name": "thread_name",
			"ph":   "M",
			"pid":  trace.SessionID,
			"tid":  trace.TurnID,
			"args": map[string]any{"name": "turn " + trace.TurnID},
		},
	}
	for _, span := range trace.Spans {
		args := map[string]any{
			"status": span.Status,
		}
		for key, value := range span.Attributes {
			args[key] = value
		}
		events = append(events, map[string]any{
			"name": span.Name,
			"cat":  span.Kind,
			"ph":   "X",
			"ts":   span.StartTime.UnixMicro(),
			"dur":  maxInt64(1, span.EndTime.Sub(span.StartTime).Microseconds()),
			"pid":  trace.SessionID,
			"tid":  trace.TurnID,
			"args": args,
		})
	}
	return map[string]any{
		"traceEvents":     events,
		"displayTimeUnit": "ms",
		"metadata": map[string]any{
			"trace_id": trace.TraceID,
			"summary":  trace.Summary,
			"stats":    trace.Stats,
		},
	}
}

func ExportOTel(trace TurnTrace) map[string]any {
	spans := make([]map[string]any, 0, len(trace.Spans))
	for _, span := range trace.Spans {
		attributes := make([]map[string]any, 0, len(span.Attributes)+6)
		attributes = append(attributes,
			stringAttribute("tma.session_id", trace.SessionID),
			stringAttribute("tma.turn_id", trace.TurnID),
			stringAttribute("tma.span_kind", span.Kind),
			stringAttribute("tma.status", span.Status),
		)
		for key, value := range span.Attributes {
			if value == "" {
				continue
			}
			attributes = append(attributes, stringAttribute("tma."+key, value))
		}
		spans = append(spans, map[string]any{
			"traceId":           span.TraceID,
			"spanId":            span.SpanID,
			"parentSpanId":      span.ParentSpanID,
			"name":              span.Name,
			"kind":              span.Kind,
			"startTimeUnixNano": fmt.Sprintf("%d", span.StartTime.UnixNano()),
			"endTimeUnixNano":   fmt.Sprintf("%d", span.EndTime.UnixNano()),
			"attributes":        attributes,
			"status": map[string]any{
				"code":    otelStatusCode(span.Status),
				"message": span.Status,
			},
			"events": []map[string]any{
				{
					"name":              "tma.span_range",
					"timeUnixNano":      fmt.Sprintf("%d", span.EndTime.UnixNano()),
					"attributes":        []map[string]any{stringAttribute("tma.start_seq", fmt.Sprintf("%d", span.StartSeq)), stringAttribute("tma.end_seq", fmt.Sprintf("%d", span.EndSeq))},
					"droppedAttributes": 0,
				},
			},
		})
	}
	return map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": []map[string]any{
					stringAttribute("service.name", "tiggy-manage-agent"),
					stringAttribute("service.instance.id", trace.SessionID),
					stringAttribute("tma.trace_id", trace.TraceID),
				},
			},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{
					"name":    "tiggy-manage-agent/internal/observability",
					"version": "v1",
				},
				"spans": spans,
			}},
		}},
		"metadata": map[string]any{
			"summary": trace.Summary,
			"stats":   trace.Stats,
		},
	}
}

func RefreshSessionSummary(store summaryStore, sessionID string, turnID string) error {
	if store == nil || sessionID == "" || turnID == "" {
		return nil
	}
	events, err := store.ListEvents(sessionID, 0)
	if err != nil {
		return err
	}
	trace := ProjectTurnTrace(sessionID, turnID, events)
	if len(trace.Steps) == 0 || trace.Summary == "" {
		return nil
	}

	existing, err := store.GetSessionSummary(sessionID)
	if err != nil && err != managedagents.ErrNotFound {
		return err
	}
	text := appendTurnSummary(existing.SummaryText, turnID, trace.Summary)
	if text == "" {
		return nil
	}
	sourceUntil := maxSeq(trace.Steps)
	if sourceUntil < existing.SourceUntilSeq {
		sourceUntil = existing.SourceUntilSeq
	}
	_, err = store.SaveSessionSummary(sessionID, managedagents.UpsertSessionSummaryInput{
		SummaryText:    text,
		SourceUntilSeq: sourceUntil,
	})
	return err
}

func BuildTurnSummary(trace TurnTrace) string {
	if len(trace.Steps) == 0 {
		return ""
	}
	lines := make([]string, 0, 8)
	interesting := false
	for _, step := range trace.Steps {
		switch step.Type {
		case managedagents.EventUserMessage:
			if step.Message != "" {
				lines = append(lines, "user: "+step.Message)
			}
		case managedagents.EventRuntimeToolCall:
			interesting = true
			lines = append(lines, fmt.Sprintf("tool requested: %s.%s", defaultString(step.Identifier, "default"), step.APIName))
		case managedagents.EventRuntimeToolInterventionApproved:
			interesting = true
			line := fmt.Sprintf("approval approved: %s.%s", defaultString(step.Identifier, "default"), step.APIName)
			if step.ApprovalSource != "" {
				line += " (" + step.ApprovalSource + ")"
			}
			lines = append(lines, line)
		case managedagents.EventRuntimeToolInterventionRejected:
			interesting = true
			line := fmt.Sprintf("approval rejected: %s.%s", defaultString(step.Identifier, "default"), step.APIName)
			if step.DecisionReason != "" {
				line += " reason=" + step.DecisionReason
			}
			lines = append(lines, line)
		case managedagents.EventRuntimeToolResult:
			interesting = true
			line := fmt.Sprintf("tool result: %s.%s %s", defaultString(step.Identifier, "default"), step.APIName, defaultString(step.Outcome, "unknown"))
			if step.DecisionReason != "" {
				line += " reason=" + step.DecisionReason
			}
			if len(step.Artifacts) > 0 {
				line += fmt.Sprintf(" artifacts=%d", len(step.Artifacts))
			}
			if step.ArtifactError != "" {
				line += " artifact_error"
			}
			lines = append(lines, line)
		case managedagents.EventAgentMessage:
			if step.Message != "" {
				lines = append(lines, "assistant: "+step.Message)
			}
		}
	}
	if len(lines) == 0 || !interesting {
		return ""
	}
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return strings.Join(lines, "\n")
}

func projectTurnTraceBase(sessionID string, turnID string, events []managedagents.Event) TurnTrace {
	trace := TurnTrace{
		SessionID: sessionID,
		TurnID:    turnID,
		Steps:     []TraceStep{},
	}
	if turnID == "" {
		return trace
	}
	trace.TraceID = traceID(sessionID, turnID)
	for _, event := range events {
		if payloadTurnID(event.Payload) != turnID {
			continue
		}
		step := projectStep(event)
		if step.Type == "" {
			continue
		}
		trace.Steps = append(trace.Steps, step)
		if event.Type == managedagents.EventSessionStatusIdle {
			trace.Status = payloadString(event.Payload, "last_turn_status")
			if trace.Status == "" {
				trace.Status = managedagents.TurnStatusCompleted
			}
		}
	}
	return trace
}

func projectStep(event managedagents.Event) TraceStep {
	step := TraceStep{
		Seq:       event.Seq,
		Type:      event.Type,
		CreatedAt: event.CreatedAt,
	}
	switch event.Type {
	case managedagents.EventUserMessage, managedagents.EventAgentMessage:
		step.Message = firstTextContent(event.Payload)
		step.Summary = step.Message
	case managedagents.EventRuntimeLLMRequest, managedagents.EventRuntimeLLMResponse, managedagents.EventRuntimeStarted, managedagents.EventRuntimeThinking, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		step.Message = payloadMessage(event.Payload)
		step.Summary = step.Message
	case managedagents.EventRuntimeToolCall, managedagents.EventRuntimeToolInterventionRequired:
		step.Message = payloadMessage(event.Payload)
		step.CallID = payloadDataString(event.Payload, "id")
		step.Identifier = payloadDataString(event.Payload, "identifier")
		step.APIName = payloadDataString(event.Payload, "api_name")
		if event.Type == managedagents.EventRuntimeToolCall {
			step.Summary = fmt.Sprintf("%s.%s requested", defaultString(step.Identifier, "default"), step.APIName)
		} else {
			step.Summary = "approval requested"
		}
	case managedagents.EventRuntimeToolInterventionApproved:
		step.Message = payloadMessage(event.Payload)
		step.CallID = payloadDataString(event.Payload, "id")
		step.Identifier = payloadDataString(event.Payload, "identifier")
		step.APIName = payloadDataString(event.Payload, "api_name")
		step.ApprovalSource = payloadDataString(event.Payload, "approval_source")
		step.Summary = "approval approved"
	case managedagents.EventRuntimeToolInterventionRejected:
		step.Message = payloadMessage(event.Payload)
		step.CallID = payloadDataString(event.Payload, "id")
		step.Identifier = payloadDataString(event.Payload, "identifier")
		step.APIName = payloadDataString(event.Payload, "api_name")
		step.DecisionReason = payloadDataString(event.Payload, "decision_reason")
		step.Summary = "approval rejected"
	case managedagents.EventRuntimeToolResult:
		step.Message = payloadMessage(event.Payload)
		step.CallID = payloadDataString(event.Payload, "id")
		step.Identifier = payloadDataString(event.Payload, "identifier")
		step.APIName = payloadDataString(event.Payload, "api_name")
		step.DecisionReason = payloadDataString(event.Payload, "decision_reason")
		step.ArtifactError = payloadDataString(event.Payload, "artifact_error")
		step.Artifacts = payloadDataArtifacts(event.Payload)
		switch payloadDataBoolPtr(event.Payload, "success"); {
		case payloadDataBool(event.Payload, "pending_intervention"):
			step.Outcome = "pending_intervention"
		case payloadDataBoolPtr(event.Payload, "success") != nil && *payloadDataBoolPtr(event.Payload, "success"):
			step.Outcome = "success"
		case payloadDataBoolPtr(event.Payload, "success") != nil:
			step.Outcome = "error"
		}
		step.Summary = fmt.Sprintf("%s.%s %s", defaultString(step.Identifier, "default"), step.APIName, defaultString(step.Outcome, "result"))
	case managedagents.EventSessionStatusIdle:
		step.Summary = payloadString(event.Payload, "last_turn_status")
	}
	return step
}

func appendTurnSummary(existing string, turnID string, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return strings.TrimSpace(existing)
	}
	section := "Turn " + turnID + ":\n" + summary
	if strings.Contains(existing, "Turn "+turnID+":") {
		return strings.TrimSpace(existing)
	}
	if strings.TrimSpace(existing) == "" {
		return section
	}
	merged := strings.TrimSpace(existing) + "\n\n" + section
	if len(merged) <= 4000 {
		return merged
	}
	return merged[len(merged)-4000:]
}

func latestTurnID(events []managedagents.Event) string {
	for index := len(events) - 1; index >= 0; index-- {
		if turnID := payloadTurnID(events[index].Payload); turnID != "" {
			return turnID
		}
	}
	return ""
}

func orderedTurnIDs(events []managedagents.Event) []string {
	seen := map[string]struct{}{}
	turnIDs := make([]string, 0, 8)
	for _, event := range events {
		turnID := payloadTurnID(event.Payload)
		if turnID == "" {
			continue
		}
		if _, ok := seen[turnID]; ok {
			continue
		}
		seen[turnID] = struct{}{}
		turnIDs = append(turnIDs, turnID)
	}
	return turnIDs
}

func maxSeq(steps []TraceStep) int64 {
	var max int64
	for _, step := range steps {
		if step.Seq > max {
			max = step.Seq
		}
	}
	return max
}

func inferTraceStatus(steps []TraceStep) string {
	if len(steps) == 0 {
		return ""
	}
	pendingApprovals := map[string]struct{}{}
	status := managedagents.TurnStatusRunning
	for _, step := range steps {
		switch step.Type {
		case managedagents.EventRuntimeToolInterventionRequired:
			pendingApprovals[defaultString(step.CallID, fmt.Sprintf("approval-%d", step.Seq))] = struct{}{}
		case managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
			delete(pendingApprovals, defaultString(step.CallID, fmt.Sprintf("approval-%d", step.Seq)))
			if step.Type == managedagents.EventRuntimeToolInterventionRejected {
				status = managedagents.TurnStatusFailed
			}
		case managedagents.EventRuntimeToolResult:
			if step.CallID != "" && step.Outcome != "pending_intervention" {
				delete(pendingApprovals, defaultString(step.CallID, fmt.Sprintf("approval-%d", step.Seq)))
			}
			if step.Outcome == "error" {
				status = managedagents.TurnStatusFailed
			}
		case managedagents.EventRuntimeFailed:
			status = managedagents.TurnStatusFailed
		case managedagents.EventRuntimeCompleted, managedagents.EventAgentMessage:
			if status != managedagents.TurnStatusFailed {
				status = managedagents.TurnStatusCompleted
			}
		}
	}
	if len(pendingApprovals) > 0 {
		return managedagents.TurnStatusWaitingApproval
	}
	return status
}

func traceID(sessionID string, turnID string) string {
	sum := sha256.Sum256([]byte(sessionID + ":" + turnID))
	return hex.EncodeToString(sum[:16])
}

func spanIDFromKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func toolSpanID(turnID string, step TraceStep) string {
	callKey := defaultString(step.CallID, fmt.Sprintf("tool-%d", step.Seq))
	return spanIDFromKey("tool:" + turnID + ":" + callKey)
}

func toolSpanName(step TraceStep, fallback TraceStep) string {
	identifier := defaultString(step.Identifier, defaultString(fallback.Identifier, "default"))
	apiName := defaultString(step.APIName, fallback.APIName)
	if apiName == "" {
		return "tma.tool"
	}
	return "tma.tool." + identifier + "." + apiName
}

func pointSpanStatus(step TraceStep) string {
	switch step.Type {
	case managedagents.EventRuntimeFailed:
		return "error"
	case managedagents.EventRuntimeCompleted:
		return "ok"
	default:
		return "point"
	}
}

func approvalSpanStatus(step TraceStep) string {
	switch step.Type {
	case managedagents.EventRuntimeToolInterventionApproved:
		return "approved"
	case managedagents.EventRuntimeToolInterventionRejected:
		return "rejected"
	default:
		return "waiting"
	}
}

func toolSpanStatus(step TraceStep) string {
	switch step.Outcome {
	case "success":
		return "ok"
	case "pending_intervention":
		return "blocked"
	case "error":
		return "error"
	default:
		return "unknown"
	}
}

func compactSpanStatus(step TraceStep) string {
	switch step.Type {
	case managedagents.EventRuntimeContextCompacted:
		return "ok"
	case managedagents.EventRuntimeContextCompactionFailed:
		return "error"
	default:
		return "open"
	}
}

func otelStatusCode(status string) string {
	switch status {
	case "error", "rejected":
		return "STATUS_CODE_ERROR"
	default:
		return "STATUS_CODE_OK"
	}
}

func traceSpanForStep(trace TurnTrace, parentSpanID string, step TraceStep, start time.Time, end time.Time, startSeq int64, endSeq int64, status string) TraceSpan {
	if start.IsZero() {
		start = time.Unix(0, 0).UTC()
	}
	if end.IsZero() || end.Before(start) {
		end = start
	}
	attributes := map[string]string{
		"event_type": step.Type,
		"event_seq":  fmt.Sprintf("%d", step.Seq),
	}
	if step.CallID != "" {
		attributes["tool_call_id"] = step.CallID
	}
	if step.Identifier != "" {
		attributes["tool_identifier"] = step.Identifier
	}
	if step.APIName != "" {
		attributes["tool_api"] = step.APIName
	}
	if step.Outcome != "" {
		attributes["outcome"] = step.Outcome
	}
	if step.DecisionReason != "" {
		attributes["decision_reason"] = step.DecisionReason
	}
	if step.ApprovalSource != "" {
		attributes["approval_source"] = step.ApprovalSource
	}
	if len(step.Artifacts) > 0 {
		attributes["artifact_count"] = fmt.Sprintf("%d", len(step.Artifacts))
	}
	if step.ArtifactError != "" {
		attributes["artifact_error"] = step.ArtifactError
	}
	if step.Message != "" {
		attributes["message"] = singleLineSummary(step.Message)
	}
	return TraceSpan{
		TraceID:        trace.TraceID,
		SpanID:         spanIDFromKey("event:" + trace.TurnID + ":" + fmt.Sprintf("%d", step.Seq)),
		ParentSpanID:   parentSpanID,
		Name:           spanName(step),
		Kind:           spanKind(step.Type),
		Status:         status,
		StartSeq:       startSeq,
		EndSeq:         endSeq,
		StartTime:      start,
		EndTime:        end,
		DurationMillis: durationMillis(start, end),
		Attributes:     attributes,
	}
}

func syntheticTraceEndStep(end time.Time, seq int64) TraceStep {
	return TraceStep{
		Seq:       seq,
		Type:      "trace.synthetic_end",
		CreatedAt: end,
	}
}

func spanName(step TraceStep) string {
	switch step.Type {
	case managedagents.EventRuntimeLLMRequest:
		return "tma.llm_request"
	case managedagents.EventRuntimeLLMResponse:
		return "tma.llm_response"
	case managedagents.EventRuntimeToolCall:
		return toolSpanName(step, step)
	case managedagents.EventRuntimeToolResult:
		return "tma.tool.result"
	case managedagents.EventRuntimeToolInterventionRequired:
		return "tma.tool.blocked_on_user"
	case managedagents.EventRuntimeToolInterventionApproved:
		return "tma.tool.approved"
	case managedagents.EventRuntimeToolInterventionRejected:
		return "tma.tool.rejected"
	case managedagents.EventRuntimeContextCompacting:
		return "tma.context.compact"
	case managedagents.EventRuntimeContextCompacted:
		return "tma.context.compacted"
	case managedagents.EventRuntimeContextCompactionFailed:
		return "tma.context.compaction_failed"
	case managedagents.EventRuntimeFailed:
		return "tma.interaction.error"
	case managedagents.EventAgentMessage:
		return "tma.agent_message"
	case managedagents.EventUserMessage:
		return "tma.user_message"
	default:
		return step.Type
	}
}

func spanKind(eventType string) string {
	switch eventType {
	case managedagents.EventRuntimeLLMRequest, managedagents.EventRuntimeLLMResponse, managedagents.EventRuntimeLLMDelta:
		return "llm"
	case managedagents.EventRuntimeToolCall, managedagents.EventRuntimeToolResult:
		return "tool"
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
		return "approval"
	case managedagents.EventRuntimeContextCompacting, managedagents.EventRuntimeContextCompacted, managedagents.EventRuntimeContextCompactionFailed:
		return "context"
	case managedagents.EventUserMessage, managedagents.EventAgentMessage:
		return "message"
	default:
		return "runtime"
	}
}

func firstStepTime(steps []TraceStep) time.Time {
	for _, step := range steps {
		if !step.CreatedAt.IsZero() {
			return step.CreatedAt
		}
	}
	return time.Unix(0, 0).UTC()
}

func lastStepTime(steps []TraceStep) time.Time {
	for index := len(steps) - 1; index >= 0; index-- {
		if !steps[index].CreatedAt.IsZero() {
			return steps[index].CreatedAt
		}
	}
	return firstStepTime(steps)
}

func durationMillis(start time.Time, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func clampEnd(start time.Time, end time.Time) time.Time {
	if end.Before(start) {
		return start
	}
	return end
}

func maxInt64(left int64, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func stringAttribute(key string, value string) map[string]any {
	return map[string]any{
		"key": key,
		"value": map[string]any{
			"stringValue": value,
		},
	}
}

func payloadTurnID(raw json.RawMessage) string {
	return payloadString(raw, "turn_id")
}

func payloadMessage(raw json.RawMessage) string {
	return payloadString(raw, "message")
}

func payloadString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	value, _ := object[key].(string)
	return value
}

func payloadDataString(raw json.RawMessage, key string) string {
	data := payloadData(raw)
	value, _ := data[key].(string)
	return value
}

func payloadDataBool(raw json.RawMessage, key string) bool {
	value := payloadDataBoolPtr(raw, key)
	return value != nil && *value
}

func payloadDataBoolPtr(raw json.RawMessage, key string) *bool {
	data := payloadData(raw)
	value, ok := data[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func payloadDataArtifacts(raw json.RawMessage) []TraceArtifact {
	data := payloadDataObject(raw)
	if len(data) == 0 {
		return nil
	}
	rawArtifacts, ok := data["artifacts"]
	if !ok || rawArtifacts == nil {
		return nil
	}
	encoded, err := json.Marshal(rawArtifacts)
	if err != nil {
		return nil
	}
	var artifacts []TraceArtifact
	if err := json.Unmarshal(encoded, &artifacts); err != nil {
		return nil
	}
	filtered := artifacts[:0]
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.ArtifactID) == "" &&
			strings.TrimSpace(artifact.ObjectRefID) == "" &&
			strings.TrimSpace(artifact.Name) == "" &&
			strings.TrimSpace(artifact.ArtifactType) == "" &&
			strings.TrimSpace(artifact.DownloadPath) == "" {
			continue
		}
		filtered = append(filtered, artifact)
	}
	if len(filtered) == 0 {
		return nil
	}
	return append([]TraceArtifact(nil), filtered...)
}

func payloadDataObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload.Data
}

func payloadData(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var object struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Data == nil {
		return map[string]any{}
	}
	return object.Data
}

func firstTextContent(payload json.RawMessage) string {
	var object struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	for _, content := range object.Content {
		if content.Type == "text" && content.Text != "" {
			return content.Text
		}
	}
	return ""
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func singleLineSummary(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\n", " | ")
	if len(value) <= 180 {
		return value
	}
	return value[:177] + "..."
}

func cloneAttributes(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func sortedKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
