package tma

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyErrorStatusDefaultsMatchV2Policy(t *testing.T) {
	tests := []struct {
		status    int
		code      string
		retryable bool
	}{
		{http.StatusMethodNotAllowed, "method_not_allowed", false},
		{http.StatusPreconditionFailed, "revision_conflict", false},
		{http.StatusRequestEntityTooLarge, "payload_too_large", false},
		{http.StatusUnsupportedMediaType, "unsupported_media_type", false},
		{http.StatusUnprocessableEntity, "unprocessable_entity", false},
		{http.StatusTooManyRequests, "rate_limited", true},
		{http.StatusInternalServerError, "internal_error", false},
		{http.StatusBadGateway, "upstream_error", true},
		{http.StatusServiceUnavailable, "service_unavailable", true},
		{http.StatusGatewayTimeout, "upstream_timeout", true},
	}
	for _, test := range tests {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				fmt.Fprint(w, `{"error":"failure"}`)
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			err := client.DoJSON(t.Context(), http.MethodGet, "/test", nil, nil)
			var apiError *APIError
			if !errors.As(err, &apiError) || apiError.Code != test.code || apiError.Retryable != test.retryable {
				t.Fatalf("unexpected error: %#v", err)
			}
		})
	}
}
