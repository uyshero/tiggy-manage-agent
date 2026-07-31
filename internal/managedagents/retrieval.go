package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RetrievalCollection struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	DocumentCount int       `json:"document_count,omitempty"`
}

type RetrievalDocument struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	CollectionID string    `json:"collection_id"`
	ObjectRefID  string    `json:"object_ref_id"`
	Name         string    `json:"name"`
	ContentType  string    `json:"content_type"`
	SizeBytes    int64     `json:"size_bytes"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	ChunkCount   int       `json:"chunk_count"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RetrievalChunkInput struct {
	Content        string
	Embedding      []float64
	EmbeddingModel string
}

type RetrievalSearchResult struct {
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	CollectionID string  `json:"collection_id"`
	ChunkIndex   int     `json:"chunk_index"`
	Content      string  `json:"content"`
	KeywordScore float64 `json:"keyword_score"`
	VectorScore  float64 `json:"vector_score"`
	Score        float64 `json:"score"`
}

type RetrievalIngestionJob struct {
	ID           string     `json:"id"`
	WorkspaceID  string     `json:"workspace_id"`
	CollectionID string     `json:"collection_id"`
	DocumentID   string     `json:"document_id,omitempty"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"error_message,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type RetrievalStore interface {
	CreateRetrievalCollection(ctx context.Context, workspaceID, name, description, createdBy string) (RetrievalCollection, error)
	ListRetrievalCollections(ctx context.Context, workspaceID string) ([]RetrievalCollection, error)
	DeleteRetrievalCollection(ctx context.Context, workspaceID, id string) error
	CreateRetrievalDocument(ctx context.Context, document RetrievalDocument, chunks []RetrievalChunkInput) (RetrievalDocument, error)
	ListRetrievalDocuments(ctx context.Context, workspaceID, collectionID string) ([]RetrievalDocument, error)
	GetRetrievalDocument(ctx context.Context, workspaceID, id string) (RetrievalDocument, error)
	DeleteRetrievalDocument(ctx context.Context, workspaceID, id string) (RetrievalDocument, error)
	CreateRetrievalIngestionJob(ctx context.Context, workspaceID, collectionID, createdBy string) (RetrievalIngestionJob, error)
	CompleteRetrievalIngestionJob(ctx context.Context, workspaceID, id, documentID string) (RetrievalIngestionJob, error)
	FailRetrievalIngestionJob(ctx context.Context, workspaceID, id, message string) (RetrievalIngestionJob, error)
	GetRetrievalIngestionJob(ctx context.Context, workspaceID, id string) (RetrievalIngestionJob, error)
	SearchRetrieval(ctx context.Context, workspaceID string, collectionIDs, documentIDs []string, query string, embedding []float64, limit int) ([]RetrievalSearchResult, error)
}

func retrievalCollectionFromKnowledge(value KnowledgeBase) RetrievalCollection {
	return RetrievalCollection{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Name: value.Name, Description: value.Description,
		CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		DocumentCount: value.DocumentCount,
	}
}

func retrievalDocumentFromKnowledge(value KnowledgeDocument) RetrievalDocument {
	return RetrievalDocument{
		ID: value.ID, WorkspaceID: value.WorkspaceID, CollectionID: value.KnowledgeBaseID,
		ObjectRefID: value.ObjectRefID, Name: value.Name, ContentType: value.ContentType,
		SizeBytes: value.SizeBytes, Status: value.Status, ErrorMessage: value.ErrorMessage,
		ChunkCount: value.ChunkCount, CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt,
	}
}

func (s *PostgresStore) CreateRetrievalCollection(ctx context.Context, workspaceID, name, description, createdBy string) (RetrievalCollection, error) {
	value, err := s.CreateKnowledgeBase(ctx, workspaceID, name, description, createdBy)
	if err != nil {
		return RetrievalCollection{}, err
	}
	if err := s.ensureRetrievalIndexes(ctx, value.WorkspaceID, value.ID); err != nil {
		return RetrievalCollection{}, err
	}
	return retrievalCollectionFromKnowledge(value), nil
}

func (s *PostgresStore) ensureRetrievalIndexes(ctx context.Context, workspaceID, collectionID string) error {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, indexType := range []string{"keyword", "vector"} {
		id, idErr := nextSequenceID(ctx, tx, "ridx", "tma_retrieval_index_id_seq")
		if idErr != nil {
			return idErr
		}
		if _, execErr := tx.ExecContext(ctx, `
			INSERT INTO retrieval_indexes (id,workspace_id,collection_id,index_type)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (workspace_id,collection_id,index_type) DO NOTHING`, id, scope.WorkspaceID, collectionID, indexType); execErr != nil {
			return execErr
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) ListRetrievalCollections(ctx context.Context, workspaceID string) ([]RetrievalCollection, error) {
	values, err := s.ListKnowledgeBases(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]RetrievalCollection, 0, len(values))
	for _, value := range values {
		result = append(result, retrievalCollectionFromKnowledge(value))
	}
	return result, nil
}

func (s *PostgresStore) DeleteRetrievalCollection(ctx context.Context, workspaceID, id string) error {
	return s.DeleteKnowledgeBase(ctx, workspaceID, id)
}

func (s *PostgresStore) CreateRetrievalDocument(ctx context.Context, document RetrievalDocument, chunks []RetrievalChunkInput) (RetrievalDocument, error) {
	compatChunks := make([]KnowledgeChunkInput, 0, len(chunks))
	for _, chunk := range chunks {
		compatChunks = append(compatChunks, KnowledgeChunkInput(chunk))
	}
	value, err := s.CreateKnowledgeDocument(ctx, KnowledgeDocument{
		ID: document.ID, WorkspaceID: document.WorkspaceID, KnowledgeBaseID: document.CollectionID,
		ObjectRefID: document.ObjectRefID, Name: document.Name, ContentType: document.ContentType,
		SizeBytes: document.SizeBytes, Status: document.Status, ErrorMessage: document.ErrorMessage,
		ChunkCount: document.ChunkCount, CreatedBy: document.CreatedBy, CreatedAt: document.CreatedAt,
		UpdatedAt: document.UpdatedAt,
	}, compatChunks)
	if err != nil {
		return RetrievalDocument{}, err
	}
	return retrievalDocumentFromKnowledge(value), nil
}

func (s *PostgresStore) ListRetrievalDocuments(ctx context.Context, workspaceID, collectionID string) ([]RetrievalDocument, error) {
	values, err := s.ListKnowledgeDocuments(ctx, workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	result := make([]RetrievalDocument, 0, len(values))
	for _, value := range values {
		result = append(result, retrievalDocumentFromKnowledge(value))
	}
	return result, nil
}

func (s *PostgresStore) GetRetrievalDocument(ctx context.Context, workspaceID, id string) (RetrievalDocument, error) {
	value, err := s.GetKnowledgeDocument(ctx, workspaceID, id)
	if err != nil {
		return RetrievalDocument{}, err
	}
	return retrievalDocumentFromKnowledge(value), nil
}

func (s *PostgresStore) DeleteRetrievalDocument(ctx context.Context, workspaceID, id string) (RetrievalDocument, error) {
	value, err := s.DeleteKnowledgeDocument(ctx, workspaceID, id)
	if err != nil {
		return RetrievalDocument{}, err
	}
	return retrievalDocumentFromKnowledge(value), nil
}

const retrievalIngestionJobColumns = `id,workspace_id,collection_id,COALESCE(document_id,''),status,error_message,created_by,created_at,started_at,completed_at`

func scanRetrievalIngestionJob(scanner interface{ Scan(...any) error }) (RetrievalIngestionJob, error) {
	var value RetrievalIngestionJob
	err := scanner.Scan(&value.ID, &value.WorkspaceID, &value.CollectionID, &value.DocumentID, &value.Status,
		&value.ErrorMessage, &value.CreatedBy, &value.CreatedAt, &value.StartedAt, &value.CompletedAt)
	return value, err
}

func (s *PostgresStore) CreateRetrievalIngestionJob(ctx context.Context, workspaceID, collectionID, createdBy string) (RetrievalIngestionJob, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	defer tx.Rollback()
	var collectionExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM retrieval_collections WHERE workspace_id=$1 AND id=$2)`, scope.WorkspaceID, collectionID).Scan(&collectionExists); err != nil {
		return RetrievalIngestionJob{}, err
	}
	if !collectionExists {
		return RetrievalIngestionJob{}, ErrNotFound
	}
	id, err := nextSequenceID(ctx, tx, "rijob", "tma_retrieval_ingestion_job_id_seq")
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	now := time.Now().UTC()
	createdBy = defaultString(strings.TrimSpace(createdBy), "system")
	value, err := scanRetrievalIngestionJob(tx.QueryRowContext(ctx, `
		INSERT INTO retrieval_ingestion_jobs (id,workspace_id,collection_id,status,created_by,created_at,started_at)
		VALUES ($1,$2,$3,'processing',$4,$5,$5)
		RETURNING `+retrievalIngestionJobColumns, id, scope.WorkspaceID, collectionID, createdBy, now))
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetrievalIngestionJob{}, err
	}
	return value, nil
}

func (s *PostgresStore) CompleteRetrievalIngestionJob(ctx context.Context, workspaceID, id, documentID string) (RetrievalIngestionJob, error) {
	return s.finishRetrievalIngestionJob(ctx, workspaceID, id, documentID, "ready", "")
}

func (s *PostgresStore) FailRetrievalIngestionJob(ctx context.Context, workspaceID, id, message string) (RetrievalIngestionJob, error) {
	return s.finishRetrievalIngestionJob(ctx, workspaceID, id, "", "failed", message)
}

func (s *PostgresStore) finishRetrievalIngestionJob(ctx context.Context, workspaceID, id, documentID, status, message string) (RetrievalIngestionJob, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	defer tx.Rollback()
	value, err := scanRetrievalIngestionJob(tx.QueryRowContext(ctx, `
		UPDATE retrieval_ingestion_jobs
		SET document_id=NULLIF($3,''),status=$4,error_message=$5,completed_at=now()
		WHERE workspace_id=$1 AND id=$2 AND status IN ('queued','processing')
		RETURNING `+retrievalIngestionJobColumns, scope.WorkspaceID, id, documentID, status, strings.TrimSpace(message)))
	if errors.Is(err, sql.ErrNoRows) {
		return RetrievalIngestionJob{}, ErrNotFound
	}
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetrievalIngestionJob{}, err
	}
	return value, nil
}

func (s *PostgresStore) GetRetrievalIngestionJob(ctx context.Context, workspaceID, id string) (RetrievalIngestionJob, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	defer tx.Rollback()
	value, err := scanRetrievalIngestionJob(tx.QueryRowContext(ctx, `SELECT `+retrievalIngestionJobColumns+` FROM retrieval_ingestion_jobs WHERE workspace_id=$1 AND id=$2`, scope.WorkspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RetrievalIngestionJob{}, ErrNotFound
	}
	if err != nil {
		return RetrievalIngestionJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetrievalIngestionJob{}, err
	}
	return value, nil
}

func (s *PostgresStore) SearchRetrieval(ctx context.Context, workspaceID string, collectionIDs, documentIDs []string, query string, embedding []float64, limit int) ([]RetrievalSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: retrieval query is required", ErrInvalid)
	}
	values, err := s.SearchKnowledge(ctx, workspaceID, collectionIDs, documentIDs, query, embedding, limit)
	if err != nil {
		return nil, err
	}
	result := make([]RetrievalSearchResult, 0, len(values))
	for _, value := range values {
		result = append(result, RetrievalSearchResult{
			DocumentID: value.DocumentID, DocumentName: value.DocumentName,
			CollectionID: value.KnowledgeBaseID, ChunkIndex: value.ChunkIndex,
			Content: value.Content, KeywordScore: value.KeywordScore,
			VectorScore: value.VectorScore, Score: value.Score,
		})
	}
	return result, nil
}
