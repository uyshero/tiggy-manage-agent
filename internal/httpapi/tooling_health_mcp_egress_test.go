package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	mcppkg "tiggy-manage-agent/internal/mcp"
)

func TestCheckMCPHealthUsesServerEgressPolicy(t *testing.T) {
	policy, err := mcppkg.NewEgressPolicy(mcppkg.EgressPolicyConfig{AllowHTTP: true})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	items := checkMCPHealthWithLookupEgressPolicy(t.Context(), json.RawMessage(`{
		"servers": [{
			"identifier": "metadata",
			"transport": "streamable_http",
			"url": "http://169.254.169.254/latest/meta-data"
		}]
	}`), "", nil, policy)
	if len(items) != 1 || items[0].Status == "online" || !strings.Contains(items[0].Detail, "egress policy blocked") {
		t.Fatalf("expected tooling health egress rejection, got %#v", items)
	}
	if strings.Contains(items[0].Detail, "169.254.169.254") || strings.Contains(items[0].Detail, "latest/meta-data") {
		t.Fatalf("tooling health leaked blocked target: %#v", items[0])
	}
	if summary := policy.Summary(); summary.BlockedTotal != 1 {
		t.Fatalf("expected health check block to be counted, got %+v", summary)
	}
}
