package tma

import "time"

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

type CreateRetrievalCollectionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
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

type RetrievalDocumentUploadResult struct {
	Document     RetrievalDocument     `json:"document"`
	ObjectRef    ObjectRef             `json:"object_ref"`
	IngestionJob RetrievalIngestionJob `json:"ingestion_job"`
}

type RetrievalSearchRequest struct {
	CollectionIDs []string `json:"collection_ids"`
	DocumentIDs   []string `json:"document_ids,omitempty"`
	Query         string   `json:"query"`
	Limit         int      `json:"limit,omitempty"`
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

type RetrievalCitation struct {
	CollectionID string  `json:"collection_id"`
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	ChunkIndex   int     `json:"chunk_index"`
	Score        float64 `json:"score"`
}

type RetrievalSearchResponse struct {
	Results   []RetrievalSearchResult `json:"results"`
	Citations []RetrievalCitation     `json:"citations"`
}
