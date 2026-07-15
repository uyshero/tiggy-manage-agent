package managedagents

import "context"

// LLMControlAuditContextStore atomically commits successful catalog mutations
// with their operator audit record.
type LLMControlAuditContextStore interface {
	UpsertLLMProviderWithAuditContext(context.Context, UpsertLLMProviderInput, RecordOperatorAuditInput) (LLMProvider, error)
	CreateLLMProviderWithAuditContext(context.Context, UpsertLLMProviderInput, RecordOperatorAuditInput) (LLMProvider, error)
	UpdateLLMProviderWithAuditContext(context.Context, UpdateLLMProviderInput, RecordOperatorAuditInput) (LLMProvider, error)
	SetLLMProviderEnabledWithAuditContext(context.Context, string, bool, RecordOperatorAuditInput) (LLMProvider, error)
	SetLLMProviderEnabledIfRevisionWithAuditContext(context.Context, string, bool, int64, RecordOperatorAuditInput) (LLMProvider, error)
	DeleteLLMProviderWithAuditContext(context.Context, string, RecordOperatorAuditInput) error
	DeleteLLMProviderIfRevisionWithAuditContext(context.Context, string, int64, RecordOperatorAuditInput) error
	UpsertLLMModelWithAuditContext(context.Context, UpsertLLMModelInput, RecordOperatorAuditInput) (LLMModel, error)
	CreateLLMModelWithAuditContext(context.Context, UpsertLLMModelInput, RecordOperatorAuditInput) (LLMModel, error)
	UpdateLLMModelWithAuditContext(context.Context, UpdateLLMModelInput, RecordOperatorAuditInput) (LLMModel, error)
	DeleteLLMModelWithAuditContext(context.Context, string, string, RecordOperatorAuditInput) error
	DeleteLLMModelIfRevisionWithAuditContext(context.Context, string, string, int64, RecordOperatorAuditInput) error
}
