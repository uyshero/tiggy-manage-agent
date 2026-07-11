package observability

import "tiggy-manage-agent/internal/managedagents"

func TraceIndexInput(session managedagents.Session, trace TurnTrace) managedagents.UpsertTraceIndexInput {
	entry := managedagents.TraceIndexEntry{
		TraceID:        trace.TraceID,
		WorkspaceID:    session.WorkspaceID,
		SessionID:      trace.SessionID,
		TurnID:         trace.TurnID,
		SessionTitle:   session.Title,
		SessionStatus:  session.Status,
		TurnStatus:     trace.Status,
		Summary:        trace.Summary,
		StartedAt:      trace.Stats.StartTime,
		EndedAt:        trace.Stats.EndTime,
		DurationMillis: trace.Stats.DurationMillis,
		StepCount:      trace.Stats.StepCount,
		SpanCount:      trace.Stats.SpanCount,
		ToolCalls:      trace.Stats.ToolCalls,
		Errors:         trace.Stats.Errors,
	}
	spans := make([]managedagents.TraceSpanIndexEntry, 0, len(trace.Spans))
	for _, span := range trace.Spans {
		spans = append(spans, managedagents.TraceSpanIndexEntry{
			TraceID:            trace.TraceID,
			WorkspaceID:        session.WorkspaceID,
			SessionID:          trace.SessionID,
			TurnID:             trace.TurnID,
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
		})
	}
	return managedagents.UpsertTraceIndexInput{Trace: entry, Spans: spans}
}

func TraceCatalogFromIndex(entries []managedagents.TraceIndexEntry) []TraceCatalogEntry {
	catalog := make([]TraceCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		catalog = append(catalog, TraceCatalogEntry{
			TraceID:        entry.TraceID,
			SessionID:      entry.SessionID,
			TurnID:         entry.TurnID,
			SessionTitle:   entry.SessionTitle,
			SessionStatus:  entry.SessionStatus,
			TurnStatus:     entry.TurnStatus,
			Summary:        entry.Summary,
			StartedAt:      entry.StartedAt,
			EndedAt:        entry.EndedAt,
			DurationMillis: entry.DurationMillis,
			StepCount:      entry.StepCount,
			SpanCount:      entry.SpanCount,
			ToolCalls:      entry.ToolCalls,
			Errors:         entry.Errors,
		})
	}
	return catalog
}

func TraceSpanCatalogFromIndex(entries []managedagents.TraceSpanIndexEntry) TraceSpanCatalog {
	catalog := TraceSpanCatalog{
		Spans:          []TraceSpanCatalogEntry{},
		KindCounts:     map[string]int{},
		StatusCounts:   map[string]int{},
		CriticalCounts: map[string]int{},
	}
	for _, entry := range entries {
		catalog.KindCounts[defaultString(entry.Kind, "unknown")]++
		catalog.StatusCounts[defaultString(entry.Status, "unknown")]++
		if entry.Critical {
			catalog.CriticalCounts["true"]++
		} else {
			catalog.CriticalCounts["false"]++
		}
		catalog.Spans = append(catalog.Spans, TraceSpanCatalogEntry{
			TraceID:            entry.TraceID,
			SessionID:          entry.SessionID,
			TurnID:             entry.TurnID,
			SessionTitle:       entry.SessionTitle,
			SpanID:             entry.SpanID,
			ParentSpanID:       entry.ParentSpanID,
			Name:               entry.Name,
			Kind:               entry.Kind,
			Status:             entry.Status,
			Depth:              entry.Depth,
			StartTime:          entry.StartTime,
			StartOffsetMillis:  entry.StartOffsetMillis,
			DurationMillis:     entry.DurationMillis,
			SelfDurationMillis: entry.SelfDurationMillis,
			Critical:           entry.Critical,
			EventCount:         entry.EventCount,
			Attributes:         entry.Attributes,
		})
	}
	return catalog
}
