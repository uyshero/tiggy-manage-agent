package managedagents

import "context"

type TraceIndexContextStore interface {
	UpsertTraceIndexContext(ctx context.Context, input UpsertTraceIndexInput) error
	ListTraceIndexesContext(ctx context.Context, input ListTraceIndexInput) ([]TraceIndexEntry, error)
	ListTraceSpanIndexesContext(ctx context.Context, input ListTraceSpanIndexInput) ([]TraceSpanIndexEntry, error)
}

func UpsertTraceIndexWithContext(ctx context.Context, store TraceIndexStore, input UpsertTraceIndexInput) error {
	if scoped, ok := store.(TraceIndexContextStore); ok {
		return scoped.UpsertTraceIndexContext(ctx, input)
	}
	return store.UpsertTraceIndex(input)
}

func ListTraceIndexesWithContext(ctx context.Context, store TraceIndexStore, input ListTraceIndexInput) ([]TraceIndexEntry, error) {
	if scoped, ok := store.(TraceIndexContextStore); ok {
		return scoped.ListTraceIndexesContext(ctx, input)
	}
	return store.ListTraceIndexes(input)
}

func ListTraceSpanIndexesWithContext(ctx context.Context, store TraceIndexStore, input ListTraceSpanIndexInput) ([]TraceSpanIndexEntry, error) {
	if scoped, ok := store.(TraceIndexContextStore); ok {
		return scoped.ListTraceSpanIndexesContext(ctx, input)
	}
	return store.ListTraceSpanIndexes(input)
}
