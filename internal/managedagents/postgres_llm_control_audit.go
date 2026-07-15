package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type llmProviderAuditState struct {
	ProviderType         string `json:"provider_type"`
	Enabled              bool   `json:"enabled"`
	Revision             int64  `json:"revision"`
	BaseURLConfigured    bool   `json:"base_url_configured"`
	CredentialConfigured bool   `json:"credential_configured"`
}

type llmModelAuditState struct {
	ContextWindowTokens int                  `json:"context_window_tokens"`
	CapabilityType      string               `json:"capability_type"`
	Capabilities        LLMModelCapabilities `json:"capabilities"`
	IsDefaultVision     bool                 `json:"is_default_vision"`
	IsDefaultEmbedding  bool                 `json:"is_default_embedding"`
	IsDefaultReranker   bool                 `json:"is_default_reranker"`
	Revision            int64                `json:"revision"`
}

func beginLLMControlAuditTx(ctx context.Context, store *PostgresStore, audit RecordOperatorAuditInput) (*sql.Tx, AccessScope, RecordOperatorAuditInput, error) {
	audit.Outcome = "succeeded"
	normalized, err := normalizeOperatorAuditInput(audit)
	if err != nil {
		return nil, AccessScope{}, RecordOperatorAuditInput{}, err
	}
	tx, scope, err := store.beginAuditScopeTx(ctx, normalized.WorkspaceID)
	if err != nil {
		return nil, AccessScope{}, RecordOperatorAuditInput{}, err
	}
	return tx, scope, normalized, nil
}

func commitLLMControlAuditTx(ctx context.Context, tx *sql.Tx, scope AccessScope, audit RecordOperatorAuditInput, before any, after any) error {
	details, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		return err
	}
	audit.Details = details
	if _, err := recordOperatorAuditTx(ctx, tx, scope, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func llmProviderAuditSnapshot(provider LLMProvider) llmProviderAuditState {
	return llmProviderAuditState{
		ProviderType:         provider.ProviderType,
		Enabled:              provider.Enabled,
		Revision:             provider.Revision,
		BaseURLConfigured:    strings.TrimSpace(provider.BaseURL) != "",
		CredentialConfigured: strings.TrimSpace(provider.APIKeyEnv) != "",
	}
}

func llmModelAuditSnapshot(model LLMModel) llmModelAuditState {
	return llmModelAuditState{
		ContextWindowTokens: model.ContextWindowTokens,
		CapabilityType:      model.CapabilityType,
		Capabilities:        model.Capabilities,
		IsDefaultVision:     model.IsDefaultVision,
		IsDefaultEmbedding:  model.IsDefaultEmbedding,
		IsDefaultReranker:   model.IsDefaultReranker,
		Revision:            model.Revision,
	}
}

func queryLLMProvider(ctx context.Context, q queryer, id string) (LLMProvider, error) {
	var provider LLMProvider
	err := q.QueryRowContext(ctx, `
		SELECT id, provider_type, base_url, api_key_env, enabled, revision, created_at, updated_at
		FROM llm_providers WHERE id = $1
	`, id).Scan(
		&provider.ID, &provider.ProviderType, &provider.BaseURL, &provider.APIKeyEnv,
		&provider.Enabled, &provider.Revision, &provider.CreatedAt, &provider.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return LLMProvider{}, ErrNotFound
	}
	return provider, err
}

func queryLLMModel(ctx context.Context, q queryer, providerID string, modelName string) (LLMModel, error) {
	var model LLMModel
	var capabilitiesJSON []byte
	err := q.QueryRowContext(ctx, `
		SELECT provider_id, model, context_window_tokens, capability_type, capabilities_json,
		       is_default_vision, is_default_embedding, is_default_reranker, revision, created_at, updated_at
		FROM llm_models WHERE provider_id = $1 AND model = $2
	`, providerID, modelName).Scan(
		&model.ProviderID, &model.Model, &model.ContextWindowTokens, &model.CapabilityType,
		&capabilitiesJSON, &model.IsDefaultVision, &model.IsDefaultEmbedding, &model.IsDefaultReranker,
		&model.Revision, &model.CreatedAt, &model.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return LLMModel{}, ErrNotFound
	}
	if err != nil {
		return LLMModel{}, err
	}
	if err := scanLLMModelCapabilities(capabilitiesJSON, &model); err != nil {
		return LLMModel{}, err
	}
	return model, nil
}

func (s *PostgresStore) UpsertLLMProviderWithAuditContext(ctx context.Context, input UpsertLLMProviderInput, audit RecordOperatorAuditInput) (LLMProvider, error) {
	if input.ID == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if input.ProviderType == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider type is required", ErrInvalid)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMProvider{}, err
	}
	defer tx.Rollback()
	var before any
	if existing, err := queryLLMProvider(ctx, tx, input.ID); err == nil {
		before = llmProviderAuditSnapshot(existing)
	} else if err != ErrNotFound {
		return LLMProvider{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT tma_control_upsert_llm_provider($1, $2, $3, $4, $5)`,
		input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled); err != nil {
		return LLMProvider{}, err
	}
	provider, err := queryLLMProvider(ctx, tx, input.ID)
	if err != nil {
		return LLMProvider{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, before, llmProviderAuditSnapshot(provider)); err != nil {
		return LLMProvider{}, err
	}
	return provider, nil
}

func (s *PostgresStore) CreateLLMProviderWithAuditContext(ctx context.Context, input UpsertLLMProviderInput, audit RecordOperatorAuditInput) (LLMProvider, error) {
	if input.ID == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if input.ProviderType == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider type is required", ErrInvalid)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMProvider{}, err
	}
	defer tx.Rollback()
	var created bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_create_llm_provider($1, $2, $3, $4, $5)`,
		input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled).Scan(&created); err != nil {
		return LLMProvider{}, err
	}
	if !created {
		return LLMProvider{}, fmt.Errorf("%w: llm provider %s already exists", ErrConflict, input.ID)
	}
	provider, err := queryLLMProvider(ctx, tx, input.ID)
	if err != nil {
		return LLMProvider{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, nil, llmProviderAuditSnapshot(provider)); err != nil {
		return LLMProvider{}, err
	}
	return provider, nil
}

func (s *PostgresStore) UpdateLLMProviderWithAuditContext(ctx context.Context, input UpdateLLMProviderInput, audit RecordOperatorAuditInput) (LLMProvider, error) {
	if input.ID == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if input.ProviderType == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider type is required", ErrInvalid)
	}
	if input.ExpectedRevision <= 0 {
		return LLMProvider{}, fmt.Errorf("%w: expected llm provider revision must be positive", ErrInvalid)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMProvider{}, err
	}
	defer tx.Rollback()
	before, err := queryLLMProvider(ctx, tx, input.ID)
	if err != nil {
		return LLMProvider{}, err
	}
	var updated bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_update_llm_provider($1, $2, $3, $4, $5, $6)`,
		input.ID, input.ProviderType, input.BaseURL, input.APIKeyEnv, input.Enabled, input.ExpectedRevision).Scan(&updated); err != nil {
		return LLMProvider{}, err
	}
	if !updated {
		current, err := queryLLMProvider(ctx, tx, input.ID)
		if err != nil {
			return LLMProvider{}, err
		}
		return LLMProvider{}, fmt.Errorf("%w: llm provider %s revision changed from %d to %d", ErrRevisionConflict, input.ID, input.ExpectedRevision, current.Revision)
	}
	after, err := queryLLMProvider(ctx, tx, input.ID)
	if err != nil {
		return LLMProvider{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, llmProviderAuditSnapshot(before), llmProviderAuditSnapshot(after)); err != nil {
		return LLMProvider{}, err
	}
	return after, nil
}

func (s *PostgresStore) SetLLMProviderEnabledWithAuditContext(ctx context.Context, id string, enabled bool, audit RecordOperatorAuditInput) (LLMProvider, error) {
	if id = strings.TrimSpace(id); id == "" {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMProvider{}, err
	}
	defer tx.Rollback()
	before, err := queryLLMProvider(ctx, tx, id)
	if err != nil {
		return LLMProvider{}, err
	}
	var updated bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_set_llm_provider_enabled($1, $2)`, id, enabled).Scan(&updated); err != nil {
		return LLMProvider{}, err
	}
	if !updated {
		return LLMProvider{}, ErrNotFound
	}
	after, err := queryLLMProvider(ctx, tx, id)
	if err != nil {
		return LLMProvider{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, llmProviderAuditSnapshot(before), llmProviderAuditSnapshot(after)); err != nil {
		return LLMProvider{}, err
	}
	return after, nil
}

func (s *PostgresStore) SetLLMProviderEnabledIfRevisionWithAuditContext(ctx context.Context, id string, enabled bool, expectedRevision int64, audit RecordOperatorAuditInput) (LLMProvider, error) {
	if id = strings.TrimSpace(id); id == "" || expectedRevision <= 0 {
		return LLMProvider{}, fmt.Errorf("%w: llm provider id and positive expected revision are required", ErrInvalid)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMProvider{}, err
	}
	defer tx.Rollback()
	before, err := queryLLMProvider(ctx, tx, id)
	if err != nil {
		return LLMProvider{}, err
	}
	var updated bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_set_llm_provider_enabled($1, $2, $3)`, id, enabled, expectedRevision).Scan(&updated); err != nil {
		return LLMProvider{}, err
	}
	if !updated {
		current, err := queryLLMProvider(ctx, tx, id)
		if err != nil {
			return LLMProvider{}, err
		}
		return LLMProvider{}, fmt.Errorf("%w: llm provider %s revision changed from %d to %d", ErrRevisionConflict, id, expectedRevision, current.Revision)
	}
	after, err := queryLLMProvider(ctx, tx, id)
	if err != nil {
		return LLMProvider{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, llmProviderAuditSnapshot(before), llmProviderAuditSnapshot(after)); err != nil {
		return LLMProvider{}, err
	}
	return after, nil
}

func (s *PostgresStore) DeleteLLMProviderWithAuditContext(ctx context.Context, id string, audit RecordOperatorAuditInput) error {
	if id = strings.TrimSpace(id); id == "" {
		return fmt.Errorf("%w: llm provider id is required", ErrInvalid)
	}
	if referenced, err := s.llmProviderReferencedAcrossWorkspaces(context.Background(), id); err != nil {
		return err
	} else if referenced {
		return fmt.Errorf("%w: llm provider %s is referenced by an agent configuration or session", ErrConflict, id)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := queryLLMProvider(ctx, tx, id)
	if err != nil {
		return err
	}
	var deleted bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_delete_llm_provider($1)`, id).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm provider "+id)
	}
	if !deleted {
		return ErrNotFound
	}
	return commitLLMControlAuditTx(ctx, tx, scope, audit, llmProviderAuditSnapshot(before), nil)
}

func (s *PostgresStore) DeleteLLMProviderIfRevisionWithAuditContext(ctx context.Context, id string, expectedRevision int64, audit RecordOperatorAuditInput) error {
	if id = strings.TrimSpace(id); id == "" || expectedRevision <= 0 {
		return fmt.Errorf("%w: llm provider id and positive expected revision are required", ErrInvalid)
	}
	if referenced, err := s.llmProviderReferencedAcrossWorkspaces(context.Background(), id); err != nil {
		return err
	} else if referenced {
		return fmt.Errorf("%w: llm provider %s is referenced by an agent configuration or session", ErrConflict, id)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := queryLLMProvider(ctx, tx, id)
	if err != nil {
		return err
	}
	var deleted bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_delete_llm_provider($1, $2)`, id, expectedRevision).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm provider "+id)
	}
	if !deleted {
		current, err := queryLLMProvider(ctx, tx, id)
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: llm provider %s revision changed from %d to %d", ErrRevisionConflict, id, expectedRevision, current.Revision)
	}
	return commitLLMControlAuditTx(ctx, tx, scope, audit, llmProviderAuditSnapshot(before), nil)
}

func (s *PostgresStore) UpsertLLMModelWithAuditContext(ctx context.Context, input UpsertLLMModelInput, audit RecordOperatorAuditInput) (LLMModel, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.ProviderID == "" {
		return LLMModel{}, fmt.Errorf("%w: llm model provider_id is required", ErrInvalid)
	}
	if input.Model == "" {
		return LLMModel{}, fmt.Errorf("%w: llm model is required", ErrInvalid)
	}
	if input.ContextWindowTokens <= 0 {
		input.ContextWindowTokens = DefaultContextWindowTokens
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMModel{}, err
	}
	defer tx.Rollback()
	if _, err := queryLLMProvider(ctx, tx, input.ProviderID); err != nil {
		return LLMModel{}, err
	}
	var before any
	existing, existingErr := queryLLMModel(ctx, tx, input.ProviderID, input.Model)
	if existingErr == nil {
		before = llmModelAuditSnapshot(existing)
	} else if existingErr != ErrNotFound {
		return LLMModel{}, existingErr
	}
	var existingModel *LLMModel
	if existingErr == nil {
		existingModel = &existing
	}
	normalized, defaults, err := normalizeLLMModelMutationInput(input, existingModel)
	if err != nil {
		return LLMModel{}, err
	}
	capabilitiesJSON, err := llmModelCapabilitiesJSON(*normalized.Capabilities)
	if err != nil {
		return LLMModel{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT tma_control_upsert_llm_model($1, $2, $3, $4, $5::jsonb, $6, $7, $8)`,
		normalized.ProviderID, normalized.Model, normalized.ContextWindowTokens, normalized.CapabilityType,
		capabilitiesJSON, defaults.Vision, defaults.Embedding, defaults.Reranker); err != nil {
		return LLMModel{}, err
	}
	after, err := queryLLMModel(ctx, tx, input.ProviderID, input.Model)
	if err != nil {
		return LLMModel{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, before, llmModelAuditSnapshot(after)); err != nil {
		return LLMModel{}, err
	}
	return after, nil
}

func (s *PostgresStore) CreateLLMModelWithAuditContext(ctx context.Context, input UpsertLLMModelInput, audit RecordOperatorAuditInput) (LLMModel, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.ProviderID == "" || input.Model == "" {
		return LLMModel{}, fmt.Errorf("%w: llm model provider_id and model are required", ErrInvalid)
	}
	normalized, defaults, err := normalizeLLMModelMutationInput(input, nil)
	if err != nil {
		return LLMModel{}, err
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMModel{}, err
	}
	defer tx.Rollback()
	if _, err := queryLLMProvider(ctx, tx, normalized.ProviderID); err != nil {
		return LLMModel{}, err
	}
	capabilitiesJSON, err := llmModelCapabilitiesJSON(*normalized.Capabilities)
	if err != nil {
		return LLMModel{}, err
	}
	var created bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_create_llm_model($1, $2, $3, $4, $5::jsonb, $6, $7, $8)`,
		normalized.ProviderID, normalized.Model, normalized.ContextWindowTokens, normalized.CapabilityType,
		capabilitiesJSON, defaults.Vision, defaults.Embedding, defaults.Reranker).Scan(&created); err != nil {
		return LLMModel{}, err
	}
	if !created {
		return LLMModel{}, fmt.Errorf("%w: llm model %s/%s already exists", ErrConflict, normalized.ProviderID, normalized.Model)
	}
	model, err := queryLLMModel(ctx, tx, normalized.ProviderID, normalized.Model)
	if err != nil {
		return LLMModel{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, nil, llmModelAuditSnapshot(model)); err != nil {
		return LLMModel{}, err
	}
	return model, nil
}

func (s *PostgresStore) UpdateLLMModelWithAuditContext(ctx context.Context, input UpdateLLMModelInput, audit RecordOperatorAuditInput) (LLMModel, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.ProviderID == "" || input.Model == "" || input.ExpectedRevision <= 0 {
		return LLMModel{}, fmt.Errorf("%w: llm model identity and positive expected revision are required", ErrInvalid)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return LLMModel{}, err
	}
	defer tx.Rollback()
	if _, err := queryLLMProvider(ctx, tx, input.ProviderID); err != nil {
		return LLMModel{}, err
	}
	before, err := queryLLMModel(ctx, tx, input.ProviderID, input.Model)
	if err != nil {
		return LLMModel{}, err
	}
	normalized, defaults, err := normalizeLLMModelMutationInput(input.UpsertLLMModelInput, &before)
	if err != nil {
		return LLMModel{}, err
	}
	capabilitiesJSON, err := llmModelCapabilitiesJSON(*normalized.Capabilities)
	if err != nil {
		return LLMModel{}, err
	}
	var updated bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_update_llm_model($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)`,
		normalized.ProviderID, normalized.Model, normalized.ContextWindowTokens, normalized.CapabilityType,
		capabilitiesJSON, defaults.Vision, defaults.Embedding, defaults.Reranker, input.ExpectedRevision).Scan(&updated); err != nil {
		return LLMModel{}, err
	}
	if !updated {
		current, err := queryLLMModel(ctx, tx, input.ProviderID, input.Model)
		if err != nil {
			return LLMModel{}, err
		}
		return LLMModel{}, fmt.Errorf("%w: llm model %s/%s revision changed from %d to %d", ErrRevisionConflict, input.ProviderID, input.Model, input.ExpectedRevision, current.Revision)
	}
	after, err := queryLLMModel(ctx, tx, input.ProviderID, input.Model)
	if err != nil {
		return LLMModel{}, err
	}
	if err := commitLLMControlAuditTx(ctx, tx, scope, audit, llmModelAuditSnapshot(before), llmModelAuditSnapshot(after)); err != nil {
		return LLMModel{}, err
	}
	return after, nil
}

func (s *PostgresStore) DeleteLLMModelWithAuditContext(ctx context.Context, providerID string, modelName string, audit RecordOperatorAuditInput) error {
	providerID = strings.TrimSpace(providerID)
	modelName = strings.TrimSpace(modelName)
	if providerID == "" || modelName == "" {
		return fmt.Errorf("%w: llm model provider_id and model are required", ErrInvalid)
	}
	if referenced, err := s.llmModelReferencedAcrossWorkspaces(context.Background(), providerID, modelName); err != nil {
		return err
	} else if referenced {
		return fmt.Errorf("%w: llm model %s/%s is referenced by an agent configuration or session", ErrConflict, providerID, modelName)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := queryLLMModel(ctx, tx, providerID, modelName)
	if err != nil {
		return err
	}
	var deleted bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_delete_llm_model($1, $2)`, providerID, modelName).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm model "+providerID+"/"+modelName)
	}
	if !deleted {
		return ErrNotFound
	}
	return commitLLMControlAuditTx(ctx, tx, scope, audit, llmModelAuditSnapshot(before), nil)
}

func (s *PostgresStore) DeleteLLMModelIfRevisionWithAuditContext(ctx context.Context, providerID string, modelName string, expectedRevision int64, audit RecordOperatorAuditInput) error {
	providerID = strings.TrimSpace(providerID)
	modelName = strings.TrimSpace(modelName)
	if providerID == "" || modelName == "" || expectedRevision <= 0 {
		return fmt.Errorf("%w: llm model identity and positive expected revision are required", ErrInvalid)
	}
	if referenced, err := s.llmModelReferencedAcrossWorkspaces(context.Background(), providerID, modelName); err != nil {
		return err
	} else if referenced {
		return fmt.Errorf("%w: llm model %s/%s is referenced by an agent configuration or session", ErrConflict, providerID, modelName)
	}
	tx, scope, audit, err := beginLLMControlAuditTx(ctx, s, audit)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := queryLLMModel(ctx, tx, providerID, modelName)
	if err != nil {
		return err
	}
	var deleted bool
	if err := tx.QueryRowContext(ctx, `SELECT tma_control_delete_llm_model($1, $2, $3)`, providerID, modelName, expectedRevision).Scan(&deleted); err != nil {
		return normalizeLLMDeleteReferenceError(err, "llm model "+providerID+"/"+modelName)
	}
	if !deleted {
		current, err := queryLLMModel(ctx, tx, providerID, modelName)
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: llm model %s/%s revision changed from %d to %d", ErrRevisionConflict, providerID, modelName, expectedRevision, current.Revision)
	}
	return commitLLMControlAuditTx(ctx, tx, scope, audit, llmModelAuditSnapshot(before), nil)
}

var _ LLMControlAuditContextStore = (*PostgresStore)(nil)
