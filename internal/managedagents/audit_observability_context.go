package managedagents

import "context"

type OperatorAuditContextStore interface {
	RecordOperatorAuditContext(context.Context, RecordOperatorAuditInput) (OperatorAuditRecord, error)
	ListOperatorAuditContext(context.Context, ListOperatorAuditInput) ([]OperatorAuditRecord, error)
}

type ObservabilityExporterRunContextStore interface {
	RecordObservabilityExporterRunContext(context.Context, RecordObservabilityExporterRunInput) (ObservabilityExporterRun, error)
	ListObservabilityExporterRunsContext(context.Context, ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error)
}

func RecordOperatorAuditWithContext(ctx context.Context, store OperatorAuditStore, input RecordOperatorAuditInput) (OperatorAuditRecord, error) {
	if scoped, ok := store.(OperatorAuditContextStore); ok {
		return scoped.RecordOperatorAuditContext(ctx, input)
	}
	return store.RecordOperatorAudit(input)
}

func ListOperatorAuditWithContext(ctx context.Context, store OperatorAuditStore, input ListOperatorAuditInput) ([]OperatorAuditRecord, error) {
	if scoped, ok := store.(OperatorAuditContextStore); ok {
		return scoped.ListOperatorAuditContext(ctx, input)
	}
	return store.ListOperatorAudit(input)
}

func RecordObservabilityExporterRunWithContext(ctx context.Context, store interface {
	RecordObservabilityExporterRun(RecordObservabilityExporterRunInput) (ObservabilityExporterRun, error)
}, input RecordObservabilityExporterRunInput) (ObservabilityExporterRun, error) {
	if scoped, ok := store.(ObservabilityExporterRunContextStore); ok {
		return scoped.RecordObservabilityExporterRunContext(ctx, input)
	}
	return store.RecordObservabilityExporterRun(input)
}

func ListObservabilityExporterRunsWithContext(ctx context.Context, store interface {
	ListObservabilityExporterRuns(ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error)
}, input ListObservabilityExporterRunsInput) ([]ObservabilityExporterRun, error) {
	if scoped, ok := store.(ObservabilityExporterRunContextStore); ok {
		return scoped.ListObservabilityExporterRunsContext(ctx, input)
	}
	return store.ListObservabilityExporterRuns(input)
}
