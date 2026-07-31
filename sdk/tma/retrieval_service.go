package tma

import (
	"context"
	"net/http"
	"net/url"
)

type RetrievalService struct {
	Collections   *RetrievalCollectionsService
	Documents     *RetrievalDocumentsService
	IngestionJobs *RetrievalIngestionJobsService
	client        *Client
}

type RetrievalCollectionsService struct{ client *Client }
type RetrievalDocumentsService struct{ client *Client }
type RetrievalIngestionJobsService struct{ client *Client }

func newRetrievalService(client *Client) *RetrievalService {
	return &RetrievalService{
		Collections:   &RetrievalCollectionsService{client: client},
		Documents:     &RetrievalDocumentsService{client: client},
		IngestionJobs: &RetrievalIngestionJobsService{client: client},
		client:        client,
	}
}

func (s *RetrievalCollectionsService) Create(ctx context.Context, request CreateRetrievalCollectionRequest) (RetrievalCollection, error) {
	var result RetrievalCollection
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/retrieval/collections", request, &result)
	return result, err
}

func (s *RetrievalCollectionsService) List(ctx context.Context) ([]RetrievalCollection, error) {
	var response struct {
		Collections []RetrievalCollection `json:"collections"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/retrieval/collections", nil, &response)
	return response.Collections, err
}

func (s *RetrievalCollectionsService) Delete(ctx context.Context, collectionID string) error {
	return s.client.DoJSON(ctx, http.MethodDelete, retrievalCollectionPath(collectionID), nil, nil)
}

func (s *RetrievalDocumentsService) List(ctx context.Context, collectionID string) ([]RetrievalDocument, error) {
	var response struct {
		Documents []RetrievalDocument `json:"documents"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, retrievalCollectionPath(collectionID)+"/documents", nil, &response)
	return response.Documents, err
}

func (s *RetrievalDocumentsService) Get(ctx context.Context, documentID string) (RetrievalDocument, error) {
	var result RetrievalDocument
	err := s.client.DoJSON(ctx, http.MethodGet, retrievalDocumentPath(documentID), nil, &result)
	return result, err
}

func (s *RetrievalDocumentsService) Upload(ctx context.Context, collectionID string, fields map[string]string, file UploadFile) (RetrievalDocumentUploadResult, error) {
	var result RetrievalDocumentUploadResult
	err := s.client.Upload(ctx, retrievalCollectionPath(collectionID)+"/documents", fields, file, &result)
	return result, err
}

func (s *RetrievalDocumentsService) Delete(ctx context.Context, documentID string) error {
	return s.client.DoJSON(ctx, http.MethodDelete, retrievalDocumentPath(documentID), nil, nil)
}

func (s *RetrievalIngestionJobsService) Get(ctx context.Context, jobID string) (RetrievalIngestionJob, error) {
	var result RetrievalIngestionJob
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/retrieval/ingestion-jobs/"+url.PathEscape(jobID), nil, &result)
	return result, err
}

func (s *RetrievalService) Search(ctx context.Context, request RetrievalSearchRequest) (RetrievalSearchResponse, error) {
	var result RetrievalSearchResponse
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/retrieval/search", request, &result)
	return result, err
}

func retrievalCollectionPath(collectionID string) string {
	return "/v2/retrieval/collections/" + url.PathEscape(collectionID)
}

func retrievalDocumentPath(documentID string) string {
	return "/v2/retrieval/documents/" + url.PathEscape(documentID)
}
