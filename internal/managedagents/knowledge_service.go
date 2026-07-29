package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

type KnowledgeBase struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	DocumentCount int       `json:"document_count,omitempty"`
}

type KnowledgeDocument struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	ObjectRefID     string    `json:"object_ref_id"`
	Name            string    `json:"name"`
	ContentType     string    `json:"content_type"`
	SizeBytes       int64     `json:"size_bytes"`
	Status          string    `json:"status"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	ChunkCount      int       `json:"chunk_count"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type KnowledgeChunkInput struct {
	Content        string
	Embedding      []float64
	EmbeddingModel string
}

type KnowledgeSearchResult struct {
	DocumentID      string  `json:"document_id"`
	DocumentName    string  `json:"document_name"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	ChunkIndex      int     `json:"chunk_index"`
	Content         string  `json:"content"`
	KeywordScore    float64 `json:"keyword_score"`
	VectorScore     float64 `json:"vector_score"`
	Score           float64 `json:"score"`
}

type KnowledgeService struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspace_id"`
	Name                 string    `json:"name"`
	Scenario             string    `json:"scenario"`
	SystemPrompt         string    `json:"system_prompt,omitempty"`
	KnowledgeBaseIDs     []string  `json:"knowledge_base_ids"`
	KnowledgeDocumentIDs []string  `json:"knowledge_document_ids"`
	AllowWebSearch       bool      `json:"allow_web_search"`
	SensitiveTerms       []string  `json:"sensitive_terms"`
	Status               string    `json:"status"`
	CreatedBy            string    `json:"created_by"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type KnowledgeServiceShare struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id,omitempty"`
	ServiceID   string     `json:"service_id"`
	ShareURL    string     `json:"share_url,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	Token       string     `json:"-"`
}

type CreateKnowledgeServiceInput struct {
	WorkspaceID          string
	Name                 string
	Scenario             string
	SystemPrompt         string
	KnowledgeBaseIDs     []string
	KnowledgeDocumentIDs []string
	AllowWebSearch       bool
	SensitiveTerms       []string
	CreatedBy            string
}

type UpdateKnowledgeServiceInput struct {
	Name                 string
	Scenario             string
	SystemPrompt         string
	KnowledgeBaseIDs     []string
	KnowledgeDocumentIDs []string
	AllowWebSearch       bool
	SensitiveTerms       []string
}

type KnowledgeServiceStore interface {
	CreateKnowledgeBase(ctx context.Context, workspaceID, name, description, createdBy string) (KnowledgeBase, error)
	ListKnowledgeBases(ctx context.Context, workspaceID string) ([]KnowledgeBase, error)
	DeleteKnowledgeBase(ctx context.Context, workspaceID, id string) error
	CreateKnowledgeDocument(ctx context.Context, document KnowledgeDocument, chunks []KnowledgeChunkInput) (KnowledgeDocument, error)
	ListKnowledgeDocuments(ctx context.Context, workspaceID, knowledgeBaseID string) ([]KnowledgeDocument, error)
	GetKnowledgeDocument(ctx context.Context, workspaceID, id string) (KnowledgeDocument, error)
	DeleteKnowledgeDocument(ctx context.Context, workspaceID, id string) (KnowledgeDocument, error)
	CreateKnowledgeService(ctx context.Context, input CreateKnowledgeServiceInput) (KnowledgeService, error)
	ListKnowledgeServices(ctx context.Context, workspaceID string) ([]KnowledgeService, error)
	GetKnowledgeService(ctx context.Context, workspaceID, id string) (KnowledgeService, error)
	UpdateKnowledgeService(ctx context.Context, workspaceID, id string, input UpdateKnowledgeServiceInput) (KnowledgeService, error)
	DeleteKnowledgeService(ctx context.Context, workspaceID, id string) error
	CreateKnowledgeServiceShare(ctx context.Context, workspaceID, serviceID, token, tokenHash, createdBy string, expiresAt *time.Time) (KnowledgeServiceShare, error)
	ListKnowledgeServiceShares(ctx context.Context, workspaceID, serviceID string) ([]KnowledgeServiceShare, error)
	RevokeKnowledgeServiceShare(ctx context.Context, workspaceID, shareID string) error
	DeleteRevokedKnowledgeServiceShare(ctx context.Context, workspaceID, shareID string) error
	ResolveKnowledgeServiceShare(ctx context.Context, tokenHash string) (KnowledgeServiceShare, KnowledgeService, error)
	SearchKnowledge(ctx context.Context, workspaceID string, knowledgeBaseIDs []string, knowledgeDocumentIDs []string, query string, embedding []float64, limit int) ([]KnowledgeSearchResult, error)
	RecordKnowledgeQuestion(ctx context.Context, workspaceID, serviceID, shareID, question, answer string, refused bool, refusalReason string, sourceCount int) error
}

func cleanStringList(values []string, max int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if max > 0 && len(result) == max {
			break
		}
	}
	return result
}

func (s *PostgresStore) CreateKnowledgeBase(ctx context.Context, workspaceID, name, description, createdBy string) (KnowledgeBase, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return KnowledgeBase{}, fmt.Errorf("%w: knowledge base name is required and must be at most 200 characters", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return KnowledgeBase{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "kb", "tma_knowledge_base_id_seq")
	if err != nil {
		return KnowledgeBase{}, err
	}
	now := time.Now().UTC()
	item := KnowledgeBase{ID: id, WorkspaceID: scope.WorkspaceID, Name: name, Description: strings.TrimSpace(description), CreatedBy: defaultString(strings.TrimSpace(createdBy), "system"), CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_bases (id,workspace_id,name,description,created_by,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$6)`, item.ID, item.WorkspaceID, item.Name, item.Description, item.CreatedBy, now)
	if err != nil {
		return KnowledgeBase{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeBase{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListKnowledgeBases(ctx context.Context, workspaceID string) ([]KnowledgeBase, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT b.id,b.workspace_id,b.name,b.description,b.created_by,b.created_at,b.updated_at,count(d.id) FROM knowledge_bases b LEFT JOIN knowledge_documents d ON d.knowledge_base_id=b.id WHERE b.workspace_id=$1 GROUP BY b.id ORDER BY b.updated_at DESC,b.id DESC`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []KnowledgeBase{}
	for rows.Next() {
		var item KnowledgeBase
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Description, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DocumentCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PostgresStore) DeleteKnowledgeBase(ctx context.Context, workspaceID, id string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM knowledge_documents WHERE workspace_id=$1 AND knowledge_base_id=$2`, scope.WorkspaceID, id)
	if err != nil {
		return err
	}
	var documentIDs []string
	for rows.Next() {
		var documentID string
		if err := rows.Scan(&documentID); err != nil {
			rows.Close()
			return err
		}
		documentIDs = append(documentIDs, documentID)
	}
	rows.Close()
	for _, documentID := range documentIDs {
		if err := deleteObjectRefLinksByOwner(ctx, tx, scope.WorkspaceID, objectRefLinkOwnerKnowledgeDocument, documentID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM knowledge_bases WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) CreateKnowledgeDocument(ctx context.Context, document KnowledgeDocument, chunks []KnowledgeChunkInput) (KnowledgeDocument, error) {
	if strings.TrimSpace(document.Name) == "" || strings.TrimSpace(document.ObjectRefID) == "" || strings.TrimSpace(document.KnowledgeBaseID) == "" {
		return KnowledgeDocument{}, fmt.Errorf("%w: document name, object_ref_id and knowledge_base_id are required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, document.WorkspaceID)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	defer tx.Rollback()
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_bases WHERE id=$1 AND workspace_id=$2) AND EXISTS(SELECT 1 FROM object_refs WHERE id=$3 AND workspace_id=$2)`, document.KnowledgeBaseID, scope.WorkspaceID, document.ObjectRefID).Scan(&valid); err != nil {
		return KnowledgeDocument{}, err
	}
	if !valid {
		return KnowledgeDocument{}, ErrForbidden
	}
	id, err := nextSequenceID(ctx, tx, "kdoc", "tma_knowledge_document_id_seq")
	if err != nil {
		return KnowledgeDocument{}, err
	}
	now := time.Now().UTC()
	document.ID = id
	document.WorkspaceID = scope.WorkspaceID
	document.Name = strings.TrimSpace(document.Name)
	document.Status = "ready"
	document.ChunkCount = len(chunks)
	document.CreatedBy = defaultString(strings.TrimSpace(document.CreatedBy), "system")
	document.CreatedAt = now
	document.UpdatedAt = now
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_documents (id,workspace_id,knowledge_base_id,object_ref_id,name,content_type,size_bytes,status,error_message,chunk_count,created_by,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'ready','',$8,$9,$10,$10)`, document.ID, document.WorkspaceID, document.KnowledgeBaseID, document.ObjectRefID, document.Name, document.ContentType, document.SizeBytes, document.ChunkCount, document.CreatedBy, now)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	for index, chunk := range chunks {
		content := strings.TrimSpace(chunk.Content)
		if content == "" {
			continue
		}
		vector := pgtype.FlatArray[float64](chunk.Embedding)
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_chunks (document_id,workspace_id,knowledge_base_id,chunk_index,content,embedding,embedding_model,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, document.ID, document.WorkspaceID, document.KnowledgeBaseID, index, content, vector, defaultString(chunk.EmbeddingModel, "local-hash-v1"), now)
		if err != nil {
			return KnowledgeDocument{}, err
		}
	}
	if err := insertObjectRefLink(ctx, tx, document.WorkspaceID, document.ObjectRefID, objectRefLinkOwnerKnowledgeDocument, document.ID, objectRefLinkRoleKnowledgeSource); err != nil {
		return KnowledgeDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDocument{}, err
	}
	return document, nil
}

const knowledgeDocumentColumns = `id,workspace_id,knowledge_base_id,object_ref_id,name,content_type,size_bytes,status,error_message,chunk_count,created_by,created_at,updated_at`

func scanKnowledgeDocument(scanner interface{ Scan(...any) error }) (KnowledgeDocument, error) {
	var d KnowledgeDocument
	err := scanner.Scan(&d.ID, &d.WorkspaceID, &d.KnowledgeBaseID, &d.ObjectRefID, &d.Name, &d.ContentType, &d.SizeBytes, &d.Status, &d.ErrorMessage, &d.ChunkCount, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}
func (s *PostgresStore) ListKnowledgeDocuments(ctx context.Context, workspaceID, knowledgeBaseID string) ([]KnowledgeDocument, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+knowledgeDocumentColumns+` FROM knowledge_documents WHERE workspace_id=$1 AND knowledge_base_id=$2 ORDER BY updated_at DESC,id DESC`, scope.WorkspaceID, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []KnowledgeDocument{}
	for rows.Next() {
		d, err := scanKnowledgeDocument(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *PostgresStore) GetKnowledgeDocument(ctx context.Context, workspaceID, id string) (KnowledgeDocument, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	defer tx.Rollback()
	d, err := scanKnowledgeDocument(tx.QueryRowContext(ctx, `SELECT `+knowledgeDocumentColumns+` FROM knowledge_documents WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDocument{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDocument{}, err
	}
	return d, nil
}
func (s *PostgresStore) DeleteKnowledgeDocument(ctx context.Context, workspaceID, id string) (KnowledgeDocument, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	defer tx.Rollback()
	d, err := scanKnowledgeDocument(tx.QueryRowContext(ctx, `DELETE FROM knowledge_documents WHERE workspace_id=$1 AND id=$2 RETURNING `+knowledgeDocumentColumns, scope.WorkspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDocument{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if err := deleteObjectRefLinksByOwner(ctx, tx, scope.WorkspaceID, objectRefLinkOwnerKnowledgeDocument, id); err != nil {
		return KnowledgeDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDocument{}, err
	}
	return d, nil
}

const knowledgeServiceColumns = `id,workspace_id,name,scenario,system_prompt,knowledge_base_ids,knowledge_document_ids,allow_web_search,sensitive_terms,status,created_by,created_at,updated_at`

func scanKnowledgeService(scanner interface{ Scan(...any) error }) (KnowledgeService, error) {
	var item KnowledgeService
	var bases, documents, terms []byte
	err := scanner.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Scenario, &item.SystemPrompt, &bases, &documents, &item.AllowWebSearch, &terms, &item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal(bases, &item.KnowledgeBaseIDs); err != nil {
		return item, err
	}
	if err = json.Unmarshal(documents, &item.KnowledgeDocumentIDs); err != nil {
		return item, err
	}
	if err = json.Unmarshal(terms, &item.SensitiveTerms); err != nil {
		return item, err
	}
	if item.KnowledgeBaseIDs == nil {
		item.KnowledgeBaseIDs = []string{}
	}
	if item.KnowledgeDocumentIDs == nil {
		item.KnowledgeDocumentIDs = []string{}
	}
	if item.SensitiveTerms == nil {
		item.SensitiveTerms = []string{}
	}
	return item, nil
}

func validateKnowledgeServiceScope(ctx context.Context, tx *sql.Tx, workspaceID string, knowledgeBaseIDs []string, knowledgeDocumentIDs []string) error {
	baseSet := map[string]bool{}
	for _, id := range knowledgeBaseIDs {
		var ok bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_bases WHERE workspace_id=$1 AND id=$2)`, workspaceID, id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: knowledge base %s is unavailable", ErrInvalid, id)
		}
		baseSet[id] = true
	}
	for _, documentID := range knowledgeDocumentIDs {
		var baseID string
		err := tx.QueryRowContext(ctx, `SELECT knowledge_base_id FROM knowledge_documents WHERE workspace_id=$1 AND id=$2`, workspaceID, documentID).Scan(&baseID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: knowledge document %s is unavailable", ErrInvalid, documentID)
		}
		if err != nil {
			return err
		}
		if !baseSet[baseID] {
			return fmt.Errorf("%w: knowledge document %s is not in selected knowledge bases", ErrInvalid, documentID)
		}
	}
	return nil
}
func (s *PostgresStore) CreateKnowledgeService(ctx context.Context, input CreateKnowledgeServiceInput) (KnowledgeService, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Scenario = strings.TrimSpace(input.Scenario)
	if input.Name == "" || input.Scenario == "" {
		return KnowledgeService{}, fmt.Errorf("%w: service name and scenario are required", ErrInvalid)
	}
	input.KnowledgeBaseIDs = cleanStringList(input.KnowledgeBaseIDs, 50)
	input.KnowledgeDocumentIDs = cleanStringList(input.KnowledgeDocumentIDs, 500)
	input.SensitiveTerms = cleanStringList(input.SensitiveTerms, 100)
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return KnowledgeService{}, err
	}
	defer tx.Rollback()
	if err := validateKnowledgeServiceScope(ctx, tx, scope.WorkspaceID, input.KnowledgeBaseIDs, input.KnowledgeDocumentIDs); err != nil {
		return KnowledgeService{}, err
	}
	id, err := nextSequenceID(ctx, tx, "ksvc", "tma_knowledge_service_id_seq")
	if err != nil {
		return KnowledgeService{}, err
	}
	bases, _ := json.Marshal(input.KnowledgeBaseIDs)
	documents, _ := json.Marshal(input.KnowledgeDocumentIDs)
	terms, _ := json.Marshal(input.SensitiveTerms)
	now := time.Now().UTC()
	item := KnowledgeService{ID: id, WorkspaceID: scope.WorkspaceID, Name: input.Name, Scenario: input.Scenario, SystemPrompt: strings.TrimSpace(input.SystemPrompt), KnowledgeBaseIDs: input.KnowledgeBaseIDs, KnowledgeDocumentIDs: input.KnowledgeDocumentIDs, AllowWebSearch: input.AllowWebSearch, SensitiveTerms: input.SensitiveTerms, Status: "active", CreatedBy: defaultString(strings.TrimSpace(input.CreatedBy), "system"), CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_services (`+knowledgeServiceColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'active',$10,$11,$11)`, item.ID, item.WorkspaceID, item.Name, item.Scenario, item.SystemPrompt, bases, documents, item.AllowWebSearch, terms, item.CreatedBy, now)
	if err != nil {
		return KnowledgeService{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeService{}, err
	}
	return item, nil
}
func (s *PostgresStore) ListKnowledgeServices(ctx context.Context, workspaceID string) ([]KnowledgeService, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+knowledgeServiceColumns+` FROM knowledge_services WHERE workspace_id=$1 ORDER BY updated_at DESC,id DESC`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []KnowledgeService{}
	for rows.Next() {
		item, err := scanKnowledgeService(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}
func (s *PostgresStore) GetKnowledgeService(ctx context.Context, workspaceID, id string) (KnowledgeService, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return KnowledgeService{}, err
	}
	defer tx.Rollback()
	item, err := scanKnowledgeService(tx.QueryRowContext(ctx, `SELECT `+knowledgeServiceColumns+` FROM knowledge_services WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeService{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeService{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeService{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateKnowledgeService(ctx context.Context, workspaceID, id string, input UpdateKnowledgeServiceInput) (KnowledgeService, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Scenario = strings.TrimSpace(input.Scenario)
	if input.Name == "" || input.Scenario == "" {
		return KnowledgeService{}, fmt.Errorf("%w: service name and scenario are required", ErrInvalid)
	}
	input.KnowledgeBaseIDs = cleanStringList(input.KnowledgeBaseIDs, 50)
	input.KnowledgeDocumentIDs = cleanStringList(input.KnowledgeDocumentIDs, 500)
	input.SensitiveTerms = cleanStringList(input.SensitiveTerms, 100)
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return KnowledgeService{}, err
	}
	defer tx.Rollback()
	if err := validateKnowledgeServiceScope(ctx, tx, scope.WorkspaceID, input.KnowledgeBaseIDs, input.KnowledgeDocumentIDs); err != nil {
		return KnowledgeService{}, err
	}
	bases, _ := json.Marshal(input.KnowledgeBaseIDs)
	documents, _ := json.Marshal(input.KnowledgeDocumentIDs)
	terms, _ := json.Marshal(input.SensitiveTerms)
	item, err := scanKnowledgeService(tx.QueryRowContext(ctx, `
		UPDATE knowledge_services
		SET name=$3, scenario=$4, system_prompt=$5, knowledge_base_ids=$6, knowledge_document_ids=$7, allow_web_search=$8, sensitive_terms=$9, updated_at=$10
		WHERE workspace_id=$1 AND id=$2
		RETURNING `+knowledgeServiceColumns, scope.WorkspaceID, id, input.Name, input.Scenario, strings.TrimSpace(input.SystemPrompt), bases, documents, input.AllowWebSearch, terms, time.Now().UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeService{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeService{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeService{}, err
	}
	return item, nil
}

func (s *PostgresStore) DeleteKnowledgeService(ctx context.Context, workspaceID, id string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM knowledge_services WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) CreateKnowledgeServiceShare(ctx context.Context, workspaceID, serviceID, token, tokenHash, createdBy string, expiresAt *time.Time) (KnowledgeServiceShare, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(tokenHash) != 64 {
		return KnowledgeServiceShare{}, fmt.Errorf("%w: share token hash is invalid", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return KnowledgeServiceShare{}, err
	}
	defer tx.Rollback()
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_services WHERE workspace_id=$1 AND id=$2 AND status='active')`, scope.WorkspaceID, serviceID).Scan(&ok); err != nil {
		return KnowledgeServiceShare{}, err
	}
	if !ok {
		return KnowledgeServiceShare{}, ErrNotFound
	}
	id, err := nextSequenceID(ctx, tx, "kshr", "tma_knowledge_share_id_seq")
	if err != nil {
		return KnowledgeServiceShare{}, err
	}
	now := time.Now().UTC()
	item := KnowledgeServiceShare{ID: id, WorkspaceID: scope.WorkspaceID, ServiceID: serviceID, ExpiresAt: expiresAt, CreatedBy: defaultString(strings.TrimSpace(createdBy), "system"), CreatedAt: now, Token: token}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_service_shares (id,workspace_id,service_id,token,token_hash,expires_at,created_by,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, item.ID, item.WorkspaceID, item.ServiceID, item.Token, tokenHash, item.ExpiresAt, item.CreatedBy, item.CreatedAt)
	if err != nil {
		return KnowledgeServiceShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeServiceShare{}, err
	}
	return item, nil
}
func scanKnowledgeShare(scanner interface{ Scan(...any) error }) (KnowledgeServiceShare, error) {
	var item KnowledgeServiceShare
	err := scanner.Scan(&item.ID, &item.WorkspaceID, &item.ServiceID, &item.Token, &item.ExpiresAt, &item.RevokedAt, &item.CreatedBy, &item.CreatedAt, &item.LastUsedAt)
	return item, err
}
func (s *PostgresStore) ListKnowledgeServiceShares(ctx context.Context, workspaceID, serviceID string) ([]KnowledgeServiceShare, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,workspace_id,service_id,COALESCE(token,''),expires_at,revoked_at,created_by,created_at,last_used_at FROM knowledge_service_shares WHERE workspace_id=$1 AND service_id=$2 ORDER BY created_at DESC`, scope.WorkspaceID, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []KnowledgeServiceShare{}
	for rows.Next() {
		item, err := scanKnowledgeShare(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}
func (s *PostgresStore) RevokeKnowledgeServiceShare(ctx context.Context, workspaceID, shareID string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_service_shares SET revoked_at=COALESCE(revoked_at,now()) WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, shareID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) DeleteRevokedKnowledgeServiceShare(ctx context.Context, workspaceID, shareID string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM knowledge_service_shares WHERE workspace_id=$1 AND id=$2 AND revoked_at IS NOT NULL`, scope.WorkspaceID, shareID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_service_shares WHERE workspace_id=$1 AND id=$2)`, scope.WorkspaceID, shareID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return fmt.Errorf("%w: only revoked share records can be deleted", ErrConflict)
	}
	return tx.Commit()
}
func (s *PostgresStore) ResolveKnowledgeServiceShare(ctx context.Context, tokenHash string) (KnowledgeServiceShare, KnowledgeService, error) {
	var shareID, workspaceID, serviceID string
	var expiresAt *time.Time
	err := s.db.QueryRowContext(ctx, `SELECT share_id,workspace_id,service_id,expires_at FROM resolve_knowledge_service_share($1)`, tokenHash).Scan(&shareID, &workspaceID, &serviceID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeServiceShare{}, KnowledgeService{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeServiceShare{}, KnowledgeService{}, err
	}
	scoped, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return KnowledgeServiceShare{}, KnowledgeService{}, err
	}
	service, err := s.GetKnowledgeService(scoped, workspaceID, serviceID)
	if err != nil {
		return KnowledgeServiceShare{}, KnowledgeService{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(scoped, workspaceID)
	if err != nil {
		return KnowledgeServiceShare{}, KnowledgeService{}, err
	}
	defer tx.Rollback()
	share, err := scanKnowledgeShare(tx.QueryRowContext(ctx, `UPDATE knowledge_service_shares SET last_used_at=now() WHERE id=$1 AND workspace_id=$2 RETURNING id,workspace_id,service_id,COALESCE(token,''),expires_at,revoked_at,created_by,created_at,last_used_at`, shareID, workspaceID))
	if err != nil {
		return KnowledgeServiceShare{}, KnowledgeService{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeServiceShare{}, KnowledgeService{}, err
	}
	return share, service, nil
}

func (s *PostgresStore) SearchKnowledge(ctx context.Context, workspaceID string, knowledgeBaseIDs []string, knowledgeDocumentIDs []string, query string, embedding []float64, limit int) ([]KnowledgeSearchResult, error) {
	knowledgeBaseIDs = cleanStringList(knowledgeBaseIDs, 50)
	if len(knowledgeBaseIDs) == 0 {
		return []KnowledgeSearchResult{}, nil
	}
	knowledgeDocumentIDs = cleanStringList(knowledgeDocumentIDs, 500)
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids, _ := json.Marshal(knowledgeBaseIDs)
	documentIDs, _ := json.Marshal(knowledgeDocumentIDs)
	queryTerms, _ := json.Marshal(knowledgeSearchTerms(query))
	vector := pgtype.FlatArray[float64](embedding)
	rows, err := tx.QueryContext(ctx, `
		WITH query_terms AS (
			SELECT DISTINCT lower(value) AS term
			FROM jsonb_array_elements_text($6::jsonb)
			WHERE length(trim(value)) > 0
		), selected_documents AS (
			SELECT id, knowledge_base_id
			FROM knowledge_documents
			WHERE workspace_id=$1
				AND id IN (SELECT jsonb_array_elements_text($7::jsonb))
		), scored AS (
			SELECT
				c.document_id,
				d.name,
				c.knowledge_base_id,
				c.chunk_index,
				c.content,
				GREATEST(
					ts_rank_cd(c.search_vector, websearch_to_tsquery('simple', $2)),
					CASE WHEN length(trim($2)) > 0 AND strpos(lower(c.content), lower($2)) > 0 THEN 0.5 ELSE 0 END,
					LEAST(0.85, COALESCE((
						SELECT sum(CASE
							WHEN strpos(lower(c.content), qt.term) = 0 THEN 0
							WHEN length(qt.term) >= 6 THEN 0.2
							WHEN length(qt.term) >= 4 THEN 0.16
							ELSE 0.1
						END)
						FROM query_terms qt
					), 0))
				) keyword_score,
				CASE
					WHEN cardinality(c.embedding) > 0 AND cardinality(c.embedding) = cardinality($3::double precision[]) THEN
						COALESCE((
							SELECT sum(v.a*v.b)/NULLIF(sqrt(sum(v.a*v.a))*sqrt(sum(v.b*v.b)),0)
							FROM unnest(c.embedding,$3::double precision[]) AS v(a,b)
						),0)
					ELSE 0
				END vector_score
			FROM knowledge_chunks c
			JOIN knowledge_documents d ON d.id=c.document_id
			WHERE c.workspace_id=$1
				AND c.knowledge_base_id IN (SELECT jsonb_array_elements_text($4::jsonb))
				AND (
					jsonb_array_length($7::jsonb)=0
					OR c.document_id IN (SELECT id FROM selected_documents)
					OR c.knowledge_base_id NOT IN (SELECT knowledge_base_id FROM selected_documents)
				)
				AND d.status='ready'
		)
		SELECT document_id,name,knowledge_base_id,chunk_index,content,keyword_score,vector_score,
			(keyword_score*0.52+GREATEST(vector_score,0)*0.48) score
		FROM scored
		WHERE keyword_score>0 OR vector_score>0.05
		ORDER BY score DESC, keyword_score DESC, vector_score DESC, document_id, chunk_index
		LIMIT $5`, scope.WorkspaceID, strings.TrimSpace(query), vector, ids, limit, queryTerms, documentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []KnowledgeSearchResult{}
	for rows.Next() {
		var item KnowledgeSearchResult
		if err := rows.Scan(&item.DocumentID, &item.DocumentName, &item.KnowledgeBaseID, &item.ChunkIndex, &item.Content, &item.KeywordScore, &item.VectorScore, &item.Score); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func knowledgeSearchTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return []string{}
	}
	seen := map[string]bool{}
	add := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" || seen[token] {
			return
		}
		if utf8.RuneCountInString(token) < 2 && !containsHan(token) {
			return
		}
		seen[token] = true
	}
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		word := string(current)
		add(word)
		if len(current) >= 6 {
			for size := 3; size <= 5; size++ {
				for i := 0; i+size <= len(current); i++ {
					add(string(current[i : i+size]))
				}
			}
		}
		current = nil
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	runes := []rune(query)
	for i := range runes {
		if !unicode.Is(unicode.Han, runes[i]) {
			continue
		}
		if len(runes) == 1 {
			add(string(runes[i]))
		}
		for size := 2; size <= 4; size++ {
			if i+size <= len(runes) && allKnowledgeSearchHan(runes[i:i+size]) {
				add(string(runes[i : i+size]))
			}
		}
	}
	terms := make([]string, 0, len(seen))
	for token := range seen {
		terms = append(terms, token)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		ri, rj := utf8.RuneCountInString(terms[i]), utf8.RuneCountInString(terms[j])
		if ri == rj {
			return terms[i] < terms[j]
		}
		return ri > rj
	})
	if len(terms) > 32 {
		terms = terms[:32]
	}
	return terms
}

func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func allKnowledgeSearchHan(runes []rune) bool {
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}
func (s *PostgresStore) RecordKnowledgeQuestion(ctx context.Context, workspaceID, serviceID, shareID, question, answer string, refused bool, refusalReason string, sourceCount int) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var nullableShare any
	if strings.TrimSpace(shareID) != "" {
		nullableShare = shareID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_service_questions (workspace_id,service_id,share_id,question,answer,refused,refusal_reason,source_count) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, scope.WorkspaceID, serviceID, nullableShare, question, answer, refused, refusalReason, sourceCount)
	if err != nil {
		return err
	}
	return tx.Commit()
}
