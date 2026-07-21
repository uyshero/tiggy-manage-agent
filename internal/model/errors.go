package model

import (
	"fmt"
	"strings"
	"time"
)

type ErrorClass string

const (
	ErrorAuth             ErrorClass = "auth"
	ErrorPermission       ErrorClass = "permission"
	ErrorRateLimit        ErrorClass = "rate_limit"
	ErrorQuota            ErrorClass = "quota"
	ErrorContextLength    ErrorClass = "context_length"
	ErrorInvalidRequest   ErrorClass = "invalid_request"
	ErrorModelUnavailable ErrorClass = "model_unavailable"
	ErrorSafety           ErrorClass = "safety"
	ErrorTimeout          ErrorClass = "timeout"
	ErrorNetwork          ErrorClass = "network"
	ErrorServer           ErrorClass = "server"
	ErrorStreamProtocol   ErrorClass = "stream_protocol"
	ErrorCanceled         ErrorClass = "canceled"
	ErrorUnknown          ErrorClass = "unknown"
)

type ProviderError struct {
	Class      ErrorClass    `json:"class"`
	Code       string        `json:"code,omitempty"`
	Retryable  bool          `json:"retryable"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	Attempt    int           `json:"attempt,omitempty"`
	RequestID  string        `json:"request_id,omitempty"`
	SafeDetail string        `json:"safe_detail,omitempty"`
	Cause      error         `json:"-"`
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider request failed"
	}
	detail := strings.TrimSpace(e.SafeDetail)
	if detail == "" {
		detail = "provider request failed"
	}
	if e.Code != "" {
		return fmt.Sprintf("provider request failed (%s/%s): %s", e.Class, e.Code, detail)
	}
	return fmt.Sprintf("provider request failed (%s): %s", e.Class, detail)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
