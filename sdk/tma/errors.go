package tma

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxErrorBodyBytes = 1 << 20

type APIError struct {
	StatusCode int            `json:"-"`
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	RequestID  string         `json:"request_id,omitempty"`
	Retryable  bool           `json:"retryable"`
	Details    map[string]any `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	if e == nil {
		return "tma API error"
	}
	if e.Code == "" {
		return fmt.Sprintf("tma: HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("tma: %s: %s", e.Code, e.Message)
}

func decodeResponse(response *http.Response, target any) error {
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
	if err != nil {
		return fmt.Errorf("tma: read response: %w", err)
	}
	if len(payload) > maxErrorBodyBytes {
		return &APIError{StatusCode: response.StatusCode, Code: "response_too_large", Message: "response exceeded SDK limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if target != nil && len(payload) > 0 {
			_ = json.Unmarshal(payload, target)
		}
		return parseAPIError(response, payload)
	}
	if target == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("tma: decode response: %w", err)
	}
	return nil
}

func parseAPIError(response *http.Response, payload []byte) error {
	apiError := &APIError{
		StatusCode: response.StatusCode,
		Code:       defaultErrorCode(response.StatusCode),
		Message:    strings.TrimSpace(string(payload)),
		RequestID:  response.Header.Get("X-Request-ID"),
		Retryable:  defaultRetryable(response.StatusCode),
	}
	var v2 struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			RequestID string         `json:"request_id"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &v2) == nil && v2.Error.Message != "" {
		apiError.Code = v2.Error.Code
		apiError.Message = v2.Error.Message
		apiError.RequestID = v2.Error.RequestID
		apiError.Retryable = v2.Error.Retryable
		apiError.Details = v2.Error.Details
		return apiError
	}
	var legacy struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(payload, &legacy) == nil && legacy.Error != "" {
		apiError.Message = legacy.Error
	}
	if apiError.Message == "" {
		apiError.Message = http.StatusText(response.StatusCode)
	}
	return apiError
}

func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusPreconditionFailed:
		return "revision_conflict"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusBadGateway:
		return "upstream_error"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "upstream_timeout"
	default:
		return "internal_error"
	}
}

func defaultRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}
