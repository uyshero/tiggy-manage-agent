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
	Graph     TraceGraph      `json:"graph,omitempty"`
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

type TraceCatalogEntry struct {
	TraceID        string    `json:"trace_id"`
	SessionID      string    `json:"session_id"`
	TurnID         string    `json:"turn_id"`
	SessionTitle   string    `json:"session_title,omitempty"`
	SessionStatus  string    `json:"session_status,omitempty"`
	TurnStatus     string    `json:"turn_status,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	DurationMillis int64     `json:"duration_ms"`
	StepCount      int       `json:"step_count"`
	SpanCount      int       `json:"span_count"`
	ToolCalls      int       `json:"tool_calls"`
	Errors         int       `json:"errors"`
}

type TraceSpanCatalogEntry struct {
	TraceID            string            `json:"trace_id"`
	SessionID          string            `json:"session_id"`
	TurnID             string            `json:"turn_id"`
	SessionTitle       string            `json:"session_title,omitempty"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	Status             string            `json:"status,omitempty"`
	Depth              int               `json:"depth,omitempty"`
	StartTime          time.Time         `json:"start_time"`
	StartOffsetMillis  int64             `json:"start_offset_ms,omitempty"`
	DurationMillis     int64             `json:"duration_ms"`
	SelfDurationMillis int64             `json:"self_duration_ms,omitempty"`
	Critical           bool              `json:"critical,omitempty"`
	EventCount         int               `json:"event_count"`
	Attributes         map[string]string `json:"attributes,omitempty"`
}

type TraceSpanCatalogFilter struct {
	TraceID               string
	SessionID             string
	TurnID                string
	Kind                  string
	Status                string
	Query                 string
	Critical              *bool
	MinDurationMillis     int64
	MaxDurationMillis     int64
	MinSelfDurationMillis int64
	Limit                 int
	Offset                int
}

type TraceSpanCatalog struct {
	Spans          []TraceSpanCatalogEntry `json:"spans"`
	KindCounts     map[string]int          `json:"kind_counts"`
	StatusCounts   map[string]int          `json:"status_counts"`
	CriticalCounts map[string]int          `json:"critical_counts"`
	Limit          int                     `json:"limit,omitempty"`
	Offset         int                     `json:"offset,omitempty"`
	NextOffset     int                     `json:"next_offset,omitempty"`
	HasMore        bool                    `json:"has_more,omitempty"`
}

type TraceGraph struct {
	RootSpanIDs                []string        `json:"root_span_ids,omitempty"`
	Edges                      []TraceSpanEdge `json:"edges,omitempty"`
	CriticalSpanIDs            []string        `json:"critical_span_ids,omitempty"`
	CriticalPathDurationMillis int64           `json:"critical_path_duration_ms,omitempty"`
	MaxDepth                   int             `json:"max_depth,omitempty"`
}

type TraceSpanEdge struct {
	ParentSpanID string `json:"parent_span_id"`
	ChildSpanID  string `json:"child_span_id"`
}

type TraceStep struct {
	Seq            int64           `json:"seq"`
	Type           string          `json:"type"`
	CreatedAt      time.Time       `json:"created_at"`
	TraceID        string          `json:"trace_id,omitempty"`
	SpanID         string          `json:"span_id,omitempty"`
	ParentSpanID   string          `json:"parent_span_id,omitempty"`
	SpanName       string          `json:"span_name,omitempty"`
	SpanKind       string          `json:"span_kind,omitempty"`
	SpanStatus     string          `json:"span_status,omitempty"`
	DurationMillis int64           `json:"duration_ms,omitempty"`
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

	ContentTruncated     bool  `json:"content_truncated,omitempty"`
	StateTruncated       bool  `json:"state_truncated,omitempty"`
	OriginalContentChars int64 `json:"original_content_chars,omitempty"`
	VisibleContentChars  int64 `json:"visible_content_chars,omitempty"`
	OriginalStateBytes   int64 `json:"original_state_bytes,omitempty"`
}

type TraceArtifact struct {
	ArtifactID   string `json:"artifact_id,omitempty"`
	ObjectRefID  string `json:"object_ref_id,omitempty"`
	Name         string `json:"name,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	DownloadPath string `json:"download_path,omitempty"`
}

type TraceSpan struct {
	TraceID            string            `json:"trace_id"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	ChildSpanIDs       []string          `json:"child_span_ids,omitempty"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	Status             string            `json:"status,omitempty"`
	StartSeq           int64             `json:"start_seq,omitempty"`
	EndSeq             int64             `json:"end_seq,omitempty"`
	Depth              int               `json:"depth,omitempty"`
	StartOffsetMillis  int64             `json:"start_offset_ms,omitempty"`
	StartTime          time.Time         `json:"start_time"`
	EndTime            time.Time         `json:"end_time"`
	DurationMillis     int64             `json:"duration_ms"`
	SelfDurationMillis int64             `json:"self_duration_ms,omitempty"`
	Critical           bool              `json:"critical,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	Events             []TraceSpanEvent  `json:"events,omitempty"`
}

type TraceSpanEvent struct {
	Seq        int64             `json:"seq"`
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Time       time.Time         `json:"time"`
	Message    string            `json:"message,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
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
	trace.Graph = BuildTraceGraph(trace.Spans)
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

func BuildTraceCatalog(sessions []managedagents.Session, eventsBySession map[string][]managedagents.Event, limit int) []TraceCatalogEntry {
	return BuildTraceCatalogPage(sessions, eventsBySession, limit, 0)
}

func BuildTraceCatalogPage(sessions []managedagents.Session, eventsBySession map[string][]managedagents.Event, limit int, offset int) []TraceCatalogEntry {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	entries := []TraceCatalogEntry{}
	for _, session := range sessions {
		turns := BuildTurnCatalog(session.ID, eventsBySession[session.ID])
		for _, turn := range turns {
			entries = append(entries, TraceCatalogEntry{
				TraceID:        TraceIDForTurn(session.ID, turn.TurnID),
				SessionID:      session.ID,
				TurnID:         turn.TurnID,
				SessionTitle:   session.Title,
				SessionStatus:  session.Status,
				TurnStatus:     turn.Status,
				Summary:        turn.Summary,
				StartedAt:      turn.StartedAt,
				EndedAt:        turn.EndedAt,
				DurationMillis: turn.DurationMillis,
				StepCount:      turn.StepCount,
				SpanCount:      turn.SpanCount,
				ToolCalls:      turn.ToolCalls,
				Errors:         turn.Errors,
			})
		}
	}
	sort.SliceStable(entries, func(i int, j int) bool {
		left := entries[i].StartedAt
		right := entries[j].StartedAt
		if left.Equal(right) {
			if entries[i].SessionID == entries[j].SessionID {
				return entries[i].TurnID > entries[j].TurnID
			}
			return entries[i].SessionID > entries[j].SessionID
		}
		return left.After(right)
	})
	if offset >= len(entries) {
		return []TraceCatalogEntry{}
	}
	entries = entries[offset:]
	if len(entries) > limit {
		return entries[:limit]
	}
	return entries
}

func BuildTraceSpanCatalog(sessions []managedagents.Session, eventsBySession map[string][]managedagents.Event, filter TraceSpanCatalogFilter) TraceSpanCatalog {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	traceID := strings.TrimSpace(filter.TraceID)
	sessionID := strings.TrimSpace(filter.SessionID)
	turnID := strings.TrimSpace(filter.TurnID)
	kind := strings.TrimSpace(strings.ToLower(filter.Kind))
	status := strings.TrimSpace(strings.ToLower(filter.Status))
	query := strings.TrimSpace(strings.ToLower(filter.Query))
	catalog := TraceSpanCatalog{
		Spans:          []TraceSpanCatalogEntry{},
		KindCounts:     map[string]int{},
		StatusCounts:   map[string]int{},
		CriticalCounts: map[string]int{},
	}
	for _, session := range sessions {
		if sessionID != "" && session.ID != sessionID {
			continue
		}
		for _, turn := range BuildTurnCatalog(session.ID, eventsBySession[session.ID]) {
			if turnID != "" && turn.TurnID != turnID {
				continue
			}
			trace := ProjectTurnTrace(session.ID, turn.TurnID, eventsBySession[session.ID])
			if traceID != "" && trace.TraceID != traceID {
				continue
			}
			for _, span := range trace.Spans {
				catalog.KindCounts[defaultString(span.Kind, "unknown")]++
				catalog.StatusCounts[defaultString(span.Status, "unknown")]++
				if span.Critical {
					catalog.CriticalCounts["true"]++
				} else {
					catalog.CriticalCounts["false"]++
				}
				entry := TraceSpanCatalogEntry{
					TraceID:            trace.TraceID,
					SessionID:          session.ID,
					TurnID:             turn.TurnID,
					SessionTitle:       session.Title,
					SpanID:             span.SpanID,
					ParentSpanID:       span.ParentSpanID,
					Name:               span.Name,
					Kind:               span.Kind,
					Status:             span.Status,
					Depth:              span.Depth,
					StartTime:          span.StartTime,
					StartOffsetMillis:  span.StartOffsetMillis,
					DurationMillis:     span.DurationMillis,
					SelfDurationMillis: span.SelfDurationMillis,
					Critical:           span.Critical,
					EventCount:         len(span.Events),
					Attributes:         span.Attributes,
				}
				if !spanCatalogEntryMatches(entry, filter, kind, status, query) {
					continue
				}
				catalog.Spans = append(catalog.Spans, entry)
			}
		}
	}
	sort.SliceStable(catalog.Spans, func(i int, j int) bool {
		if catalog.Spans[i].StartTime.Equal(catalog.Spans[j].StartTime) {
			return catalog.Spans[i].SpanID < catalog.Spans[j].SpanID
		}
		return catalog.Spans[i].StartTime.After(catalog.Spans[j].StartTime)
	})
	catalog.Limit = limit
	catalog.Offset = offset
	if offset >= len(catalog.Spans) {
		catalog.Spans = []TraceSpanCatalogEntry{}
		return catalog
	}
	hasMore := len(catalog.Spans) > offset+limit
	catalog.Spans = catalog.Spans[offset:]
	if len(catalog.Spans) > limit {
		catalog.Spans = catalog.Spans[:limit]
	}
	catalog.HasMore = hasMore
	catalog.NextOffset = offset + len(catalog.Spans)
	return catalog
}

func spanCatalogEntryMatches(entry TraceSpanCatalogEntry, filter TraceSpanCatalogFilter, kind string, status string, query string) bool {
	if kind != "" && strings.ToLower(entry.Kind) != kind {
		return false
	}
	if status != "" && strings.ToLower(entry.Status) != status {
		return false
	}
	if filter.Critical != nil && entry.Critical != *filter.Critical {
		return false
	}
	if filter.MinDurationMillis > 0 && entry.DurationMillis < filter.MinDurationMillis {
		return false
	}
	if filter.MaxDurationMillis > 0 && entry.DurationMillis > filter.MaxDurationMillis {
		return false
	}
	if filter.MinSelfDurationMillis > 0 && entry.SelfDurationMillis < filter.MinSelfDurationMillis {
		return false
	}
	if query == "" {
		return true
	}
	values := []string{entry.TraceID, entry.SessionID, entry.TurnID, entry.SpanID, entry.ParentSpanID, entry.Name, entry.Kind, entry.Status, entry.SessionTitle}
	if entry.Critical {
		values = append(values, "critical")
	}
	for key, value := range entry.Attributes {
		values = append(values, key, value)
	}
	return strings.Contains(strings.ToLower(strings.Join(values, " ")), query)
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

func BuildTraceGraph(spans []TraceSpan) TraceGraph {
	indexes := buildSpanGraphIndexes(spans)
	return TraceGraph{
		RootSpanIDs:                indexes.rootSpanIDs,
		Edges:                      indexes.edges,
		CriticalSpanIDs:            indexes.criticalSpanIDs,
		CriticalPathDurationMillis: indexes.criticalPathDurationMillis,
		MaxDepth:                   indexes.maxDepth,
	}
}

type spanGraphIndexes struct {
	rootSpanIDs                []string
	edges                      []TraceSpanEdge
	childIDsByParent           map[string][]string
	depthBySpanID              map[string]int
	criticalSpanIDs            []string
	criticalSpanIDSet          map[string]struct{}
	criticalPathDurationMillis int64
	maxDepth                   int
}

func buildSpanGraphIndexes(spans []TraceSpan) spanGraphIndexes {
	indexes := spanGraphIndexes{
		childIDsByParent:  map[string][]string{},
		depthBySpanID:     map[string]int{},
		criticalSpanIDSet: map[string]struct{}{},
	}
	byID := make(map[string]TraceSpan, len(spans))
	for _, span := range spans {
		if span.SpanID == "" {
			continue
		}
		byID[span.SpanID] = span
	}
	for _, span := range spans {
		if span.SpanID == "" {
			continue
		}
		if span.ParentSpanID == "" {
			indexes.rootSpanIDs = append(indexes.rootSpanIDs, span.SpanID)
			continue
		}
		if _, ok := byID[span.ParentSpanID]; !ok {
			indexes.rootSpanIDs = append(indexes.rootSpanIDs, span.SpanID)
			continue
		}
		indexes.childIDsByParent[span.ParentSpanID] = append(indexes.childIDsByParent[span.ParentSpanID], span.SpanID)
		indexes.edges = append(indexes.edges, TraceSpanEdge{ParentSpanID: span.ParentSpanID, ChildSpanID: span.SpanID})
	}
	sort.Strings(indexes.rootSpanIDs)
	for parentID := range indexes.childIDsByParent {
		sort.Strings(indexes.childIDsByParent[parentID])
	}
	for _, rootID := range indexes.rootSpanIDs {
		assignSpanDepth(rootID, 0, indexes.childIDsByParent, indexes.depthBySpanID, map[string]struct{}{})
	}
	for _, depth := range indexes.depthBySpanID {
		if depth > indexes.maxDepth {
			indexes.maxDepth = depth
		}
	}
	indexes.criticalPathDurationMillis, indexes.criticalSpanIDs = longestSpanPath(indexes.rootSpanIDs, byID, indexes.childIDsByParent)
	for _, spanID := range indexes.criticalSpanIDs {
		indexes.criticalSpanIDSet[spanID] = struct{}{}
	}
	return indexes
}

func assignSpanDepth(spanID string, depth int, childIDsByParent map[string][]string, depthBySpanID map[string]int, visiting map[string]struct{}) {
	if _, ok := visiting[spanID]; ok {
		return
	}
	if current, ok := depthBySpanID[spanID]; ok && current <= depth {
		return
	}
	depthBySpanID[spanID] = depth
	visiting[spanID] = struct{}{}
	for _, childID := range childIDsByParent[spanID] {
		assignSpanDepth(childID, depth+1, childIDsByParent, depthBySpanID, visiting)
	}
	delete(visiting, spanID)
}

func longestSpanPath(rootSpanIDs []string, byID map[string]TraceSpan, childIDsByParent map[string][]string) (int64, []string) {
	var bestDuration int64
	var bestPath []string
	for _, rootID := range rootSpanIDs {
		duration, path := longestSpanPathFrom(rootID, byID, childIDsByParent, map[string]struct{}{})
		if len(bestPath) == 0 || duration > bestDuration || (duration == bestDuration && strings.Join(path, "\x00") < strings.Join(bestPath, "\x00")) {
			bestDuration = duration
			bestPath = path
		}
	}
	return bestDuration, bestPath
}

func longestSpanPathFrom(spanID string, byID map[string]TraceSpan, childIDsByParent map[string][]string, visiting map[string]struct{}) (int64, []string) {
	span, ok := byID[spanID]
	if !ok {
		return 0, nil
	}
	if _, ok := visiting[spanID]; ok {
		return 0, nil
	}
	visiting[spanID] = struct{}{}
	var childBestDuration int64
	var childBestPath []string
	for _, childID := range childIDsByParent[spanID] {
		duration, path := longestSpanPathFrom(childID, byID, childIDsByParent, visiting)
		if len(childBestPath) == 0 || duration > childBestDuration || (duration == childBestDuration && strings.Join(path, "\x00") < strings.Join(childBestPath, "\x00")) {
			childBestDuration = duration
			childBestPath = path
		}
	}
	delete(visiting, spanID)
	return span.DurationMillis + childBestDuration, append([]string{spanID}, childBestPath...)
}

func buildNativeTraceSpans(trace TurnTrace) []TraceSpan {
	type aggregate struct {
		span       TraceSpan
		attributes map[string]string
		expectsEnd bool
		ended      bool
	}
	aggregates := map[string]*aggregate{}
	order := []string{}
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.SpanID) == "" {
			continue
		}
		spanID := step.SpanID
		current, ok := aggregates[spanID]
		if !ok {
			start := step.CreatedAt
			if start.IsZero() {
				start = firstStepTime(trace.Steps)
			}
			current = &aggregate{
				span: TraceSpan{
					TraceID:        defaultString(step.TraceID, trace.TraceID),
					SpanID:         spanID,
					ParentSpanID:   step.ParentSpanID,
					Name:           defaultString(step.SpanName, spanName(step)),
					Kind:           defaultString(step.SpanKind, spanKind(step.Type)),
					Status:         defaultString(step.SpanStatus, nativeSpanStatus(step, "")),
					StartSeq:       step.Seq,
					EndSeq:         step.Seq,
					StartTime:      start,
					EndTime:        start,
					DurationMillis: step.DurationMillis,
					Attributes:     map[string]string{},
				},
				attributes: map[string]string{},
			}
			aggregates[spanID] = current
			order = append(order, spanID)
		}
		if nativeSpanStartEvent(step.Type) {
			current.expectsEnd = true
		}
		if nativeSpanEndEvent(step.Type) {
			current.ended = true
		}
		if step.ParentSpanID != "" {
			current.span.ParentSpanID = step.ParentSpanID
		}
		if step.SpanName != "" {
			current.span.Name = step.SpanName
		}
		if step.SpanKind != "" {
			current.span.Kind = step.SpanKind
		}
		if status := nativeSpanStatus(step, current.span.Status); status != "" {
			current.span.Status = status
		}
		if step.Seq < current.span.StartSeq {
			current.span.StartSeq = step.Seq
		}
		if step.Seq > current.span.EndSeq {
			current.span.EndSeq = step.Seq
		}
		if !step.CreatedAt.IsZero() {
			if current.span.StartTime.IsZero() || step.CreatedAt.Before(current.span.StartTime) {
				current.span.StartTime = step.CreatedAt
			}
			if step.CreatedAt.After(current.span.EndTime) {
				current.span.EndTime = step.CreatedAt
			}
		}
		for key, value := range nativeStepAttributes(step) {
			if value != "" {
				current.attributes[key] = value
			}
		}
		if step.DurationMillis > current.span.DurationMillis {
			current.span.DurationMillis = step.DurationMillis
		}
	}
	if len(order) == 0 {
		return nil
	}
	terminalStatus := terminalTraceStatus(trace.Status)
	if terminalStatus != "" {
		traceEnd := lastStepTime(trace.Steps)
		traceEndSeq := trace.Steps[len(trace.Steps)-1].Seq
		for _, spanID := range order {
			current := aggregates[spanID]
			if !current.expectsEnd || current.ended {
				continue
			}
			current.span.EndSeq = max(current.span.EndSeq, traceEndSeq)
			current.span.EndTime = clampEnd(current.span.StartTime, traceEnd)
			if current.span.SpanID == InteractionSpanID(trace.TurnID) {
				current.span.Status = terminalStatus
			} else if terminalStatus == "canceled" {
				current.span.Status = "canceled"
			} else {
				current.span.Status = "error"
			}
		}
	}
	for _, spanID := range order {
		current := aggregates[spanID]
		if current.span.DurationMillis > 0 {
			current.span.EndTime = current.span.StartTime.Add(time.Duration(current.span.DurationMillis) * time.Millisecond)
		} else {
			current.span.DurationMillis = durationMillis(current.span.StartTime, current.span.EndTime)
		}
		current.span.Attributes = current.attributes
	}
	sort.SliceStable(order, func(i int, j int) bool {
		left := aggregates[order[i]].span
		right := aggregates[order[j]].span
		if left.ParentSpanID == "" && right.ParentSpanID != "" {
			return true
		}
		if right.ParentSpanID == "" && left.ParentSpanID != "" {
			return false
		}
		if !left.StartTime.Equal(right.StartTime) {
			return left.StartTime.Before(right.StartTime)
		}
		if left.StartSeq != right.StartSeq {
			return left.StartSeq < right.StartSeq
		}
		return left.Name < right.Name
	})
	spans := make([]TraceSpan, 0, len(order))
	for _, spanID := range order {
		spans = append(spans, aggregates[spanID].span)
	}
	return spans
}

func enrichTraceSpans(trace TurnTrace, spans []TraceSpan) []TraceSpan {
	if len(spans) == 0 {
		return spans
	}
	childIDs := map[string][]string{}
	for _, span := range spans {
		if span.ParentSpanID == "" {
			continue
		}
		childIDs[span.ParentSpanID] = append(childIDs[span.ParentSpanID], span.SpanID)
	}
	nativeSpanIDs := map[string]struct{}{}
	for _, step := range trace.Steps {
		if step.SpanID != "" {
			nativeSpanIDs[step.SpanID] = struct{}{}
		}
	}
	usesNativeStepIDs := len(nativeSpanIDs) > 0
	for index := range spans {
		span := &spans[index]
		span.ChildSpanIDs = append([]string(nil), childIDs[span.SpanID]...)
		for _, step := range trace.Steps {
			if !stepBelongsToSpan(step, *span, usesNativeStepIDs) {
				continue
			}
			span.Events = append(span.Events, traceSpanEventForStep(step))
		}
	}
	annotateTraceSpans(trace, spans)
	return spans
}

func annotateTraceSpans(trace TurnTrace, spans []TraceSpan) {
	indexes := buildSpanGraphIndexes(spans)
	traceStart := firstStepTime(trace.Steps)
	durationByID := make(map[string]int64, len(spans))
	for _, span := range spans {
		durationByID[span.SpanID] = span.DurationMillis
	}
	for index := range spans {
		span := &spans[index]
		span.Depth = indexes.depthBySpanID[span.SpanID]
		if !traceStart.IsZero() && !span.StartTime.IsZero() {
			span.StartOffsetMillis = durationMillis(traceStart, span.StartTime)
		}
		childDurationMillis := int64(0)
		for _, childID := range indexes.childIDsByParent[span.SpanID] {
			childDurationMillis += durationByID[childID]
		}
		span.SelfDurationMillis = span.DurationMillis - childDurationMillis
		if span.SelfDurationMillis < 0 {
			span.SelfDurationMillis = 0
		}
		_, span.Critical = indexes.criticalSpanIDSet[span.SpanID]
	}
}

func stepBelongsToSpan(step TraceStep, span TraceSpan, usesNativeStepIDs bool) bool {
	if usesNativeStepIDs {
		return step.SpanID != "" && step.SpanID == span.SpanID
	}
	if span.StartSeq == 0 && span.EndSeq == 0 {
		return false
	}
	return step.Seq >= span.StartSeq && step.Seq <= span.EndSeq
}

func traceSpanEventForStep(step TraceStep) TraceSpanEvent {
	attributes := nativeStepAttributes(step)
	if step.TraceID != "" {
		attributes["trace_id"] = step.TraceID
	}
	if step.SpanID != "" {
		attributes["span_id"] = step.SpanID
	}
	if step.ParentSpanID != "" {
		attributes["parent_span_id"] = step.ParentSpanID
	}
	if step.DurationMillis > 0 {
		attributes["duration_ms"] = fmt.Sprintf("%d", step.DurationMillis)
	}
	return TraceSpanEvent{
		Seq:        step.Seq,
		Type:       step.Type,
		Name:       spanName(step),
		Time:       step.CreatedAt,
		Message:    step.Message,
		Summary:    step.Summary,
		Attributes: attributes,
	}
}

func nativeStepAttributes(step TraceStep) map[string]string {
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
	return attributes
}

func BuildTraceSpans(trace TurnTrace) []TraceSpan {
	if trace.TraceID == "" || trace.TurnID == "" || len(trace.Steps) == 0 {
		return nil
	}
	if trace.Status == "" {
		trace.Status = inferTraceStatus(trace.Steps)
	}
	trace = normalizeMixedNativeTraceSteps(trace)
	if native := buildNativeTraceSpans(trace); len(native) > 0 {
		return enrichTraceSpans(trace, native)
	}

	start := firstStepTime(trace.Steps)
	end := lastStepTime(trace.Steps)
	if end.Before(start) {
		end = start
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
	return enrichTraceSpans(trace, spans)
}

func normalizeMixedNativeTraceSteps(trace TurnTrace) TurnTrace {
	hasNative := false
	for _, step := range trace.Steps {
		if step.SpanID != "" {
			hasNative = true
			break
		}
	}
	if !hasNative {
		return trace
	}

	steps := append([]TraceStep(nil), trace.Steps...)
	for index := range steps {
		step := &steps[index]
		if step.SpanID != "" {
			continue
		}
		step.TraceID = trace.TraceID
		switch step.Type {
		case managedagents.EventUserInterrupt, managedagents.EventSessionStatusIdle, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
			step.SpanID = InteractionSpanID(trace.TurnID)
			step.SpanName = "tma.interaction"
			step.SpanKind = "interaction"
			if step.Type == managedagents.EventRuntimeCompleted {
				step.SpanStatus = "ok"
			} else if step.Type == managedagents.EventRuntimeFailed {
				step.SpanStatus = "error"
			} else if step.Type == managedagents.EventSessionStatusIdle {
				step.SpanStatus = terminalTraceStatus(trace.Status)
			}
		case managedagents.EventRuntimeToolCall, managedagents.EventRuntimeToolResult:
			if step.CallID == "" {
				continue
			}
			step.SpanID = ToolSpanID(trace.TurnID, step.CallID, step.Seq)
			step.ParentSpanID = InteractionSpanID(trace.TurnID)
			step.SpanName = toolSpanName(*step, *step)
			step.SpanKind = "tool"
			if step.Type == managedagents.EventRuntimeToolResult {
				step.SpanStatus = toolSpanStatus(*step)
			}
		case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
			if step.CallID == "" {
				continue
			}
			step.SpanID = ApprovalSpanID(trace.TurnID, step.CallID)
			step.ParentSpanID = ToolSpanID(trace.TurnID, step.CallID, step.Seq)
			step.SpanName = "tma.tool.blocked_on_user"
			step.SpanKind = "approval"
			step.SpanStatus = approvalSpanStatus(*step)
		}
	}
	trace.Steps = steps
	return trace
}

func nativeSpanStartEvent(eventType string) bool {
	switch eventType {
	case managedagents.EventRuntimeStarted,
		managedagents.EventRuntimeLLMRequest,
		managedagents.EventRuntimeToolCall,
		managedagents.EventRuntimeToolInterventionRequired,
		managedagents.EventRuntimeContextCompacting,
		managedagents.EventRuntimeSpanStarted:
		return true
	default:
		return false
	}
}

func nativeSpanEndEvent(eventType string) bool {
	switch eventType {
	case managedagents.EventRuntimeCompleted,
		managedagents.EventRuntimeFailed,
		managedagents.EventSessionStatusIdle,
		managedagents.EventRuntimeLLMResponse,
		managedagents.EventRuntimeToolResult,
		managedagents.EventRuntimeToolInterventionApproved,
		managedagents.EventRuntimeToolInterventionRejected,
		managedagents.EventRuntimeContextCompacted,
		managedagents.EventRuntimeContextCompactionFailed,
		managedagents.EventRuntimeSpanEnded:
		return true
	default:
		return false
	}
}

func terminalTraceStatus(status string) string {
	switch status {
	case managedagents.TurnStatusCompleted:
		return "ok"
	case managedagents.TurnStatusFailed:
		return "error"
	case managedagents.TurnStatusInterrupted, "canceled", "terminated":
		return "canceled"
	default:
		return ""
	}
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
			"status":           span.Status,
			"span_id":          span.SpanID,
			"parent_span_id":   span.ParentSpanID,
			"depth":            span.Depth,
			"start_offset_ms":  span.StartOffsetMillis,
			"duration_ms":      span.DurationMillis,
			"self_duration_ms": span.SelfDurationMillis,
			"critical":         span.Critical,
			"event_count":      len(span.Events),
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
			"graph":    trace.Graph,
		},
	}
}

func ExportOTel(trace TurnTrace) map[string]any {
	spans := make([]map[string]any, 0, len(trace.Spans))
	for _, span := range trace.Spans {
		attributes := make([]map[string]any, 0, len(span.Attributes)+12)
		attributes = append(attributes,
			stringAttribute("tma.session_id", trace.SessionID),
			stringAttribute("tma.turn_id", trace.TurnID),
			stringAttribute("tma.span_kind", span.Kind),
			stringAttribute("tma.status", span.Status),
			intAttribute("tma.span_depth", int64(span.Depth)),
			intAttribute("tma.start_offset_ms", span.StartOffsetMillis),
			intAttribute("tma.duration_ms", span.DurationMillis),
			intAttribute("tma.self_duration_ms", span.SelfDurationMillis),
			boolAttribute("tma.critical", span.Critical),
			intAttribute("tma.event_count", int64(len(span.Events))),
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
			"events": otelSpanEvents(span),
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
			"graph":   trace.Graph,
		},
	}
}

func otelSpanEvents(span TraceSpan) []map[string]any {
	events := []map[string]any{
		{
			"name":         "tma.span_range",
			"timeUnixNano": fmt.Sprintf("%d", span.EndTime.UnixNano()),
			"attributes": []map[string]any{
				stringAttribute("tma.start_seq", fmt.Sprintf("%d", span.StartSeq)),
				stringAttribute("tma.end_seq", fmt.Sprintf("%d", span.EndSeq)),
				intAttribute("tma.span_depth", int64(span.Depth)),
				intAttribute("tma.start_offset_ms", span.StartOffsetMillis),
				intAttribute("tma.self_duration_ms", span.SelfDurationMillis),
				boolAttribute("tma.critical", span.Critical),
			},
			"droppedAttributes": 0,
		},
	}
	for _, event := range span.Events {
		attributes := []map[string]any{
			stringAttribute("tma.event_type", event.Type),
			stringAttribute("tma.event_seq", fmt.Sprintf("%d", event.Seq)),
		}
		if event.Message != "" {
			attributes = append(attributes, stringAttribute("tma.message", singleLineSummary(event.Message)))
		}
		if event.Summary != "" {
			attributes = append(attributes, stringAttribute("tma.summary", singleLineSummary(event.Summary)))
		}
		for key, value := range event.Attributes {
			if value == "" || key == "event_type" || key == "event_seq" || key == "message" {
				continue
			}
			attributes = append(attributes, stringAttribute("tma."+key, value))
		}
		eventTime := event.Time
		if eventTime.IsZero() {
			eventTime = span.StartTime
		}
		events = append(events, map[string]any{
			"name":              event.Name,
			"timeUnixNano":      fmt.Sprintf("%d", eventTime.UnixNano()),
			"attributes":        attributes,
			"droppedAttributes": 0,
		})
	}
	return events
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
	step.TraceID = payloadString(event.Payload, "trace_id")
	step.SpanID = payloadString(event.Payload, "span_id")
	step.ParentSpanID = payloadString(event.Payload, "parent_span_id")
	step.SpanName = payloadString(event.Payload, "span_name")
	step.SpanKind = payloadString(event.Payload, "span_kind")
	step.SpanStatus = payloadString(event.Payload, "span_status")
	step.DurationMillis = payloadInt64(event.Payload, "duration_ms")
	switch event.Type {
	case managedagents.EventUserMessage, managedagents.EventAgentMessage:
		step.Message = firstTextContent(event.Payload)
		step.Summary = step.Message
	case managedagents.EventRuntimeLLMRequest, managedagents.EventRuntimeStarted, managedagents.EventRuntimeThinking, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		step.Message = payloadMessage(event.Payload)
		step.Summary = step.Message
	case managedagents.EventRuntimeLLMResponse:
		step.Message = llmResponseStepSummary(event.Payload)
		step.Summary = step.Message
	case managedagents.EventRuntimeSpanStarted, managedagents.EventRuntimeSpanEvent, managedagents.EventRuntimeSpanEnded:
		step.Message = payloadMessage(event.Payload)
		step.Summary = defaultString(step.Message, step.SpanName)
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
		context := payloadDataContext(event.Payload)
		step.ContentTruncated = mapBool(context, "content_truncated")
		step.StateTruncated = mapBool(context, "state_truncated")
		step.OriginalContentChars = mapInt64(context, "original_content_chars")
		step.VisibleContentChars = mapInt64(context, "visible_content_chars")
		step.OriginalStateBytes = mapInt64(context, "original_state_bytes")
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

func llmResponseStepSummary(raw json.RawMessage) string {
	data := payloadData(raw)
	stream, _ := data["stream"].(map[string]any)
	usage, _ := data["usage"].(map[string]any)
	facts := []string{"LLM response"}
	if mapBool(stream, "streamed") {
		facts = append(facts, fmt.Sprintf("%d chunks", mapInt64(stream, "chunk_count")))
		facts = append(facts, fmt.Sprintf("%d output chars", mapInt64(stream, "output_chars")))
		if mapInt64(stream, "text_chunk_count") > 0 {
			facts = append(facts, fmt.Sprintf("TTFT %d ms", mapInt64(stream, "ttft_ms")))
		}
		if finishReason, _ := stream["finish_reason"].(string); finishReason != "" {
			facts = append(facts, "finish "+finishReason)
		}
	} else {
		facts = append(facts, "non-streamed")
	}
	if totalTokens := mapInt64(usage, "total_tokens"); totalTokens > 0 {
		facts = append(facts, fmt.Sprintf("%d tokens", totalTokens))
	}
	return strings.Join(facts, ", ")
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

func TraceIDForTurn(sessionID string, turnID string) string {
	return traceID(sessionID, turnID)
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

func nativeSpanStatus(step TraceStep, current string) string {
	if step.SpanStatus != "" {
		return step.SpanStatus
	}
	switch step.Type {
	case managedagents.EventRuntimeSpanStarted:
		if current == "" {
			return "open"
		}
	case managedagents.EventRuntimeSpanEnded:
		return "ok"
	case managedagents.EventRuntimeFailed:
		return "error"
	}
	return current
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
	case managedagents.EventRuntimeLLMRequest, managedagents.EventRuntimeLLMResponse:
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

func intAttribute(key string, value int64) map[string]any {
	return map[string]any{
		"key": key,
		"value": map[string]any{
			"intValue": fmt.Sprintf("%d", value),
		},
	}
}

func boolAttribute(key string, value bool) map[string]any {
	return map[string]any{
		"key": key,
		"value": map[string]any{
			"boolValue": value,
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

func payloadInt64(raw json.RawMessage, key string) int64 {
	if len(raw) == 0 {
		return 0
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return 0
	}
	switch value := object[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
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

func payloadDataContext(raw json.RawMessage) map[string]any {
	data := payloadData(raw)
	context, _ := data["context"].(map[string]any)
	return context
}

func mapBool(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func mapInt64(values map[string]any, key string) int64 {
	switch value := values[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
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
