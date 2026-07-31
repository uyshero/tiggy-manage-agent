package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
)

type createRetrievalCollectionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type retrievalSearchRequest struct {
	CollectionIDs []string `json:"collection_ids"`
	DocumentIDs   []string `json:"document_ids,omitempty"`
	Query         string   `json:"query"`
	Limit         int      `json:"limit,omitempty"`
}

type retrievalCitation struct {
	CollectionID string  `json:"collection_id"`
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	ChunkIndex   int     `json:"chunk_index"`
	Score        float64 `json:"score"`
}

func (s *Server) retrievalStore() (managedagents.RetrievalStore, error) {
	store, ok := s.store.(managedagents.RetrievalStore)
	if !ok {
		return nil, fmt.Errorf("%w: retrieval store is unavailable", managedagents.ErrInvalid)
	}
	return store, nil
}

func (s *Server) listRetrievalCollections(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListRetrievalCollections(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": nonNilSlice(items)})
}

func (s *Server) createRetrievalCollection(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createRetrievalCollectionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	item, err := store.CreateRetrievalCollection(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), request.Name, request.Description, requestActorID(r, "system"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) deleteRetrievalCollection(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.DeleteRetrievalCollection(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("collection_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRetrievalDocuments(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListRetrievalDocuments(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("collection_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": nonNilSlice(items)})
}

func (s *Server) getRetrievalDocument(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.GetRetrievalDocument(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("document_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) uploadRetrievalDocument(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "retrieval document upload requires multipart/form-data"})
		return
	}
	if r.ContentLength > maxKnowledgeUploadBytes+1024 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "retrieval document upload exceeds size limit"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxKnowledgeUploadBytes+1024)
	if err := r.ParseMultipartForm(maxKnowledgeUploadBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "retrieval document upload exceeds size limit"})
			return
		}
		writeError(w, fmt.Errorf("%w: parse multipart retrieval document upload: %v", managedagents.ErrInvalid, err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, fmt.Errorf("%w: retrieval document upload requires file field", managedagents.ErrInvalid))
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, err)
		return
	}
	contentType := fallbackString(r.FormValue("content_type"), header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	name := fallbackString(strings.TrimSpace(r.FormValue("name")), safeArtifactFileName(header.Filename))
	text, err := extractKnowledgeText(name, contentType, content)
	if err != nil {
		writeError(w, err)
		return
	}
	chunkTexts := splitKnowledgeText(text)
	if len(chunkTexts) == 0 {
		writeError(w, fmt.Errorf("%w: document contains no readable text", managedagents.ErrInvalid))
		return
	}

	workspaceID := requestWorkspaceID(r, managedagents.DefaultWorkspaceID)
	actorID := requestActorID(r, "system")
	collectionID := r.PathValue("collection_id")
	job, err := store.CreateRetrievalIngestionJob(r.Context(), workspaceID, collectionID, actorID)
	if err != nil {
		writeError(w, err)
		return
	}
	failJob := func(cause error) {
		if _, failErr := store.FailRetrievalIngestionJob(r.Context(), workspaceID, job.ID, cause.Error()); failErr != nil {
			s.logger.Warn("mark retrieval ingestion job failed", "job_id", job.ID, "error", failErr)
		}
	}

	chunks := make([]managedagents.RetrievalChunkInput, 0, len(chunkTexts))
	for _, chunk := range chunkTexts {
		embedding := s.embedKnowledgeText(r.Context(), workspaceID, chunk)
		chunks = append(chunks, managedagents.RetrievalChunkInput{Content: chunk, Embedding: embedding.Vector, EmbeddingModel: embedding.Model})
	}

	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	bucket, err := objectstore.ResolveBucket(r.FormValue("bucket"), s.defaultObjectStoreBucket())
	if err != nil {
		failJob(err)
		writeError(w, err)
		return
	}
	objectKey := strings.TrimSpace(r.FormValue("object_key"))
	if objectKey == "" {
		objectKey = fmt.Sprintf("%s/retrieval/%s/%d-%s", workspaceID, collectionID, time.Now().UTC().UnixNano(), safeArtifactFileName(name))
	}
	if err := objectstore.ValidateObjectKey(objectKey); err != nil {
		failJob(err)
		writeError(w, err)
		return
	}
	putResult, err := s.objectStore.PutObject(r.Context(), objectstore.PutObjectInput{
		Bucket: bucket, Key: objectKey, Body: bytes.NewReader(content), ContentType: contentType,
		SizeBytes: int64(len(content)), ChecksumSHA256: checksumHex,
	})
	if err != nil {
		failJob(err)
		writeError(w, err)
		return
	}
	objectRef, err := managedagents.CreateObjectRefWithContext(r.Context(), s.store, managedagents.CreateObjectRefInput{
		WorkspaceID: workspaceID, StorageProvider: managedagents.ObjectStorageProviderS3,
		Bucket: fallbackString(putResult.Bucket, bucket), ObjectKey: fallbackString(putResult.Key, objectKey), ObjectVersion: putResult.Version,
		ContentType: contentType, SizeBytes: int64(len(content)), ChecksumSHA256: fallbackString(putResult.ChecksumSHA256, checksumHex),
		ETag: putResult.ETag, Visibility: managedagents.ObjectVisibilityWorkspace, CreatedBy: actorID,
	})
	if err != nil {
		failJob(err)
		writeError(w, err)
		return
	}
	document, err := store.CreateRetrievalDocument(r.Context(), managedagents.RetrievalDocument{
		WorkspaceID: workspaceID, CollectionID: collectionID, ObjectRefID: objectRef.ID,
		Name: name, ContentType: contentType, SizeBytes: int64(len(content)), CreatedBy: actorID,
	}, chunks)
	if err != nil {
		failJob(err)
		writeError(w, err)
		return
	}
	job, err = store.CompleteRetrievalIngestionJob(r.Context(), workspaceID, job.ID, document.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"document": document, "object_ref": objectRef, "ingestion_job": job})
}

func (s *Server) deleteRetrievalDocument(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	document, err := store.DeleteRetrievalDocument(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("document_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if document.ObjectRefID != "" {
		if err := managedagents.DeleteObjectRefWithContext(r.Context(), s.store, document.ObjectRefID); err != nil && !errors.Is(err, managedagents.ErrConflict) && !errors.Is(err, managedagents.ErrNotFound) {
			s.logger.Warn("delete retrieval document object ref failed", "document_id", document.ID, "object_ref_id", document.ObjectRefID, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getRetrievalIngestionJob(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.GetRetrievalIngestionJob(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), r.PathValue("job_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) searchRetrieval(w http.ResponseWriter, r *http.Request) {
	store, err := s.retrievalStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request retrievalSearchRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	workspaceID := requestWorkspaceID(r, managedagents.DefaultWorkspaceID)
	embedding := s.embedKnowledgeText(r.Context(), workspaceID, request.Query)
	items, err := store.SearchRetrieval(r.Context(), workspaceID, request.CollectionIDs, request.DocumentIDs, request.Query, embedding.Vector, request.Limit)
	if err != nil {
		writeError(w, err)
		return
	}
	citations := make([]retrievalCitation, 0, len(items))
	for _, item := range items {
		citations = append(citations, retrievalCitation{
			CollectionID: item.CollectionID, DocumentID: item.DocumentID,
			DocumentName: item.DocumentName, ChunkIndex: item.ChunkIndex, Score: item.Score,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": nonNilSlice(items), "citations": nonNilSlice(citations)})
}
