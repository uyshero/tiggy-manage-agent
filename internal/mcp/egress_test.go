package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

type staticEgressResolver map[string][]netip.Addr

func (r staticEgressResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r[host]...), nil
}

func TestEgressPolicyBlocksUnsafeDestinations(t *testing.T) {
	resolver := staticEgressResolver{
		"public.example":  {netip.MustParseAddr("8.8.8.8")},
		"private.example": {netip.MustParseAddr("10.0.0.5")},
		"mixed.example":   {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")},
	}
	policy, err := NewEgressPolicy(EgressPolicyConfig{AllowHTTP: true, Resolver: resolver})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "private DNS", url: "http://private.example/mcp", want: "non_public_address"},
		{name: "metadata in mixed DNS", url: "http://mixed.example/mcp", want: "non_public_address"},
		{name: "loopback literal", url: "http://127.0.0.1/mcp", want: "non_public_address"},
		{name: "metadata literal", url: "http://169.254.169.254/latest/meta-data", want: "non_public_address"},
		{name: "URL userinfo", url: "http://user:password@public.example/mcp", want: "invalid_url"},
		{name: "file scheme", url: "file:///etc/passwd", want: "invalid_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := policy.ValidateURL(t.Context(), test.url)
			if err == nil || !IsEgressBlocked(err) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s block, got %v", test.want, err)
			}
		})
	}
}

func TestEgressPolicyRequiresHTTPSAndHostAllowlist(t *testing.T) {
	resolver := staticEgressResolver{
		"api.example.com":     {netip.MustParseAddr("8.8.8.8")},
		"mcp.example.com":     {netip.MustParseAddr("8.8.4.4")},
		"nested.example.com":  {netip.MustParseAddr("1.1.1.1")},
		"example.com":         {netip.MustParseAddr("1.0.0.1")},
		"outside.example.net": {netip.MustParseAddr("9.9.9.9")},
	}
	policy, err := NewEgressPolicy(EgressPolicyConfig{AllowedHosts: []string{"api.example.com", "*.example.com"}, Resolver: resolver})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	if err := policy.ValidateURL(t.Context(), "http://api.example.com/mcp"); err == nil || !strings.Contains(err.Error(), "http_not_allowed") {
		t.Fatalf("expected HTTP rejection, got %v", err)
	}
	for _, target := range []string{"https://api.example.com/mcp", "https://mcp.example.com/mcp", "https://nested.example.com/mcp"} {
		if err := policy.ValidateURL(t.Context(), target); err != nil {
			t.Fatalf("expected %s to be allowed: %v", target, err)
		}
	}
	if err := policy.ValidateURL(t.Context(), "https://outside.example.net/mcp"); err == nil || !strings.Contains(err.Error(), "host_not_allowed") {
		t.Fatalf("expected host allowlist rejection, got %v", err)
	}
}

func TestEgressPolicyAllowsExplicitPrivateCIDR(t *testing.T) {
	policy, err := NewEgressPolicy(EgressPolicyConfig{
		AllowHTTP:    true,
		AllowedHosts: []string{"mcp.internal.example"},
		AllowedCIDRs: []string{"10.20.0.0/16"},
		Resolver: staticEgressResolver{
			"mcp.internal.example": {netip.MustParseAddr("10.20.4.5")},
		},
	})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	if err := policy.ValidateURL(t.Context(), "http://mcp.internal.example/mcp"); err != nil {
		t.Fatalf("expected allowlisted private MCP target: %v", err)
	}
}

func TestEgressPolicyPrivateNetworkSwitchDoesNotAllowMetadataOrLoopback(t *testing.T) {
	policy, err := NewEgressPolicy(EgressPolicyConfig{AllowHTTP: true, AllowPrivateNetworks: true})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	if err := policy.ValidateURL(t.Context(), "http://10.20.4.5/mcp"); err != nil {
		t.Fatalf("expected RFC1918 address to be allowed: %v", err)
	}
	for _, target := range []string{"http://127.0.0.1/mcp", "http://169.254.169.254/latest/meta-data"} {
		if err := policy.ValidateURL(t.Context(), target); err == nil || !IsEgressBlocked(err) {
			t.Fatalf("expected high-risk address %s to remain blocked, got %v", target, err)
		}
	}
}

func TestEgressHTTPClientRejectsCrossAuthorityRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	policy, err := NewEgressPolicy(EgressPolicyConfig{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, source.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, err = policy.HTTPClient(nil).Do(request)
	if err == nil || !IsEgressBlocked(err) || !strings.Contains(err.Error(), "cross_authority_redirect") {
		t.Fatalf("expected cross-authority redirect rejection, got %v", err)
	}
}

func TestEgressHTTPClientUsesConfiguredBaseTLSClient(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	policy, err := NewEgressPolicy(EgressPolicyConfig{
		AllowedCIDRs:   []string{"127.0.0.0/8"},
		BaseHTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	response, err := policy.HTTPClient(nil).Get(server.URL)
	if err != nil {
		t.Fatalf("request fixture with configured TLS roots: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
}

func TestEgressPolicyRejectsInvalidCIDR(t *testing.T) {
	_, err := NewEgressPolicy(EgressPolicyConfig{AllowedCIDRs: []string{"not-a-cidr"}})
	if err == nil || !strings.Contains(err.Error(), "allowed CIDR") {
		t.Fatalf("expected invalid CIDR error, got %v", err)
	}
}

func TestEgressPolicyRejectsInvalidHostPattern(t *testing.T) {
	_, err := NewEgressPolicy(EgressPolicyConfig{AllowedHosts: []string{"*.*.example.com"}})
	if err == nil || !strings.Contains(err.Error(), "allowed host") {
		t.Fatalf("expected invalid host pattern error, got %v", err)
	}
}

func TestEgressPolicyBlockCallbackIsSanitized(t *testing.T) {
	var events []EgressBlockEvent
	policy, err := NewEgressPolicy(EgressPolicyConfig{
		AllowHTTP: true,
		OnBlock: func(event EgressBlockEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("new egress policy: %v", err)
	}
	if err := policy.ValidateURL(t.Context(), "http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatal("expected metadata request to be blocked")
	}
	if len(events) != 1 || events[0].Reason != "non_public_address" {
		t.Fatalf("unexpected sanitized block events: %#v", events)
	}
}
