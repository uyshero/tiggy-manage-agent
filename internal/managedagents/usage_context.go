package managedagents

import "context"

type LLMUsageContextStore interface {
	RecordLLMUsageContext(ctx context.Context, input RecordLLMUsageInput) (LLMUsageRecord, error)
	ListLLMUsageContext(ctx context.Context, input ListLLMUsageInput) (LLMUsageAggregateReport, error)
}

func RecordLLMUsageWithContext(ctx context.Context, store Store, input RecordLLMUsageInput) (LLMUsageRecord, error) {
	if scoped, ok := store.(LLMUsageContextStore); ok {
		return scoped.RecordLLMUsageContext(ctx, input)
	}
	return store.RecordLLMUsage(input)
}

func ListLLMUsageWithContext(ctx context.Context, store Store, input ListLLMUsageInput) (LLMUsageAggregateReport, error) {
	if scoped, ok := store.(LLMUsageContextStore); ok {
		return scoped.ListLLMUsageContext(ctx, input)
	}
	return store.ListLLMUsage(input)
}
