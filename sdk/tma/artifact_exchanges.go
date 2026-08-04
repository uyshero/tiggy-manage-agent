package tma

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type ArtifactExchangesService struct{ client *Client }

func (s *ArtifactExchangesService) CreateImport(ctx context.Context, request CreateArtifactImportExchangeRequest) (ArtifactExchangeGrant, error) {
	var grant ArtifactExchangeGrant
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/artifact-exchanges/imports", request, &grant)
	return grant, err
}

func (s *ArtifactExchangesService) CreateExport(ctx context.Context, request CreateArtifactExportExchangeRequest) (ArtifactExchangeGrant, error) {
	var grant ArtifactExchangeGrant
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/artifact-exchanges/exports", request, &grant)
	return grant, err
}

func (s *ArtifactExchangesService) Get(ctx context.Context, exchangeID string) (ArtifactExchange, error) {
	var exchange ArtifactExchange
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/artifact-exchanges/"+url.PathEscape(exchangeID), nil, &exchange)
	return exchange, err
}

func (s *ArtifactExchangesService) Upload(ctx context.Context, grant ArtifactExchangeGrant, contentType string, sizeBytes int64, body io.Reader) (ArtifactExchangeImportResult, error) {
	var result ArtifactExchangeImportResult
	if body == nil {
		return result, errors.New("tma: artifact exchange upload body is required")
	}
	if strings.TrimSpace(grant.ContentURL) == "" {
		return result, errors.New("tma: artifact exchange content URL is required")
	}
	request, err := s.client.newRequest(ctx, http.MethodPut, grant.ContentURL, body)
	if err != nil {
		return result, err
	}
	if contentType = strings.TrimSpace(contentType); contentType == "" {
		contentType = "application/octet-stream"
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	if sizeBytes >= 0 {
		request.ContentLength = sizeBytes
	}
	response, err := s.client.httpClient.Do(request)
	if err != nil {
		return result, fmt.Errorf("tma: artifact exchange upload failed: %w", err)
	}
	defer response.Body.Close()
	if err := decodeResponse(response, &result); err != nil {
		return ArtifactExchangeImportResult{}, err
	}
	return result, nil
}

func (s *ArtifactExchangesService) Download(ctx context.Context, grant ArtifactExchangeGrant, output io.Writer) error {
	if strings.TrimSpace(grant.ContentURL) == "" {
		return errors.New("tma: artifact exchange content URL is required")
	}
	return s.client.Download(ctx, grant.ContentURL, output)
}
