package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
)

type EgressResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type EgressPolicyConfig struct {
	AllowHTTP            bool
	AllowPrivateNetworks bool
	AllowedHosts         []string
	AllowedCIDRs         []string
	BaseHTTPClient       *http.Client
	Resolver             EgressResolver
	OnBlock              func(EgressBlockEvent)
}

type EgressBlockEvent struct {
	Reason string
}

type EgressPolicySummary struct {
	Enabled              bool
	AllowHTTP            bool
	AllowPrivateNetworks bool
	AllowedHostCount     int
	AllowedCIDRCount     int
	BlockedTotal         int64
}

type EgressPolicy struct {
	allowHTTP            bool
	allowPrivateNetworks bool
	allowedHosts         []string
	allowedCIDRs         []netip.Prefix
	baseHTTPClient       *http.Client
	resolver             EgressResolver
	onBlock              func(EgressBlockEvent)
	blockedTotal         atomic.Int64
}

type EgressBlockedError struct {
	Reason string
}

var specialUsePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func (e EgressBlockedError) Error() string {
	return fmt.Sprintf("mcp egress policy blocked request (%s)", e.Reason)
}

func IsEgressBlocked(err error) bool {
	var blocked EgressBlockedError
	return errors.As(err, &blocked)
}

func sanitizeEgressError(err error) error {
	var blocked EgressBlockedError
	if errors.As(err, &blocked) {
		return blocked
	}
	return err
}

func NewEgressPolicy(config EgressPolicyConfig) (*EgressPolicy, error) {
	for _, raw := range config.AllowedHosts {
		if err := validateAllowedHostPattern(raw); err != nil {
			return nil, err
		}
	}
	hosts := normalizeAllowedHosts(config.AllowedHosts)
	prefixes := make([]netip.Prefix, 0, len(config.AllowedCIDRs))
	for _, raw := range config.AllowedCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid MCP egress allowed CIDR %q: %w", raw, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return prefixes[i].String() < prefixes[j].String()
	})
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &EgressPolicy{
		allowHTTP:            config.AllowHTTP,
		allowPrivateNetworks: config.AllowPrivateNetworks,
		allowedHosts:         hosts,
		allowedCIDRs:         prefixes,
		baseHTTPClient:       config.BaseHTTPClient,
		resolver:             resolver,
		onBlock:              config.OnBlock,
	}, nil
}

func (p *EgressPolicy) Summary() EgressPolicySummary {
	if p == nil {
		return EgressPolicySummary{}
	}
	return EgressPolicySummary{
		Enabled:              true,
		AllowHTTP:            p.allowHTTP,
		AllowPrivateNetworks: p.allowPrivateNetworks,
		AllowedHostCount:     len(p.allowedHosts),
		AllowedCIDRCount:     len(p.allowedCIDRs),
		BlockedTotal:         p.blockedTotal.Load(),
	}
}

func (p *EgressPolicy) Fingerprint() string {
	if p == nil {
		return "disabled"
	}
	parts := []string{
		fmt.Sprintf("http=%t", p.allowHTTP),
		fmt.Sprintf("private=%t", p.allowPrivateNetworks),
		"hosts=" + strings.Join(p.allowedHosts, ","),
	}
	for _, prefix := range p.allowedCIDRs {
		parts = append(parts, "cidr="+prefix.String())
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (p *EgressPolicy) ValidateURL(ctx context.Context, raw string) error {
	if p == nil {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return p.block("invalid_url")
	}
	return p.validateURL(ctx, parsed)
}

func (p *EgressPolicy) HTTPClient(base *http.Client) *http.Client {
	if p == nil {
		if base != nil {
			return base
		}
		return http.DefaultClient
	}
	client := &http.Client{}
	if base == nil {
		base = p.baseHTTPClient
	}
	if base != nil {
		*client = *base
	}
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	if transport, ok := baseTransport.(*http.Transport); ok {
		cloned := transport.Clone()
		cloned.Proxy = nil
		cloned.DialContext = p.dialContext
		baseTransport = cloned
	}
	client.Transport = egressRoundTripper{policy: p, base: baseTransport}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return p.block("too_many_redirects")
		}
		if len(via) > 0 && !sameURLAuthority(via[len(via)-1].URL, request.URL) {
			return p.block("cross_authority_redirect")
		}
		if err := p.validateURL(request.Context(), request.URL); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	return client
}

type egressRoundTripper struct {
	policy *EgressPolicy
	base   http.RoundTripper
}

func (t egressRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.policy.validateURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func (p *EgressPolicy) validateURL(ctx context.Context, target *url.URL) error {
	if target == nil || target.User != nil || strings.TrimSpace(target.Hostname()) == "" {
		return p.block("invalid_url")
	}
	switch strings.ToLower(strings.TrimSpace(target.Scheme)) {
	case "https":
	case "http":
		if !p.allowHTTP {
			return p.block("http_not_allowed")
		}
	default:
		return p.block("scheme_not_allowed")
	}
	host := normalizeHostname(target.Hostname())
	if !p.hostAllowed(host) {
		return p.block("host_not_allowed")
	}
	addresses, err := p.resolve(ctx, host)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if !p.addressAllowed(address) {
			return p.block("non_public_address")
		}
	}
	return nil
}

func (p *EgressPolicy) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	addresses, err := p.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve MCP egress destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("resolve MCP egress destination: no addresses")
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.Unmap())
	}
	return result, nil
}

func (p *EgressPolicy) dialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, p.block("invalid_dial_address")
	}
	host = normalizeHostname(host)
	if !p.hostAllowed(host) {
		return nil, p.block("host_not_allowed")
	}
	addresses, err := p.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, resolved := range addresses {
		if !p.addressAllowed(resolved) {
			return nil, p.block("non_public_address")
		}
	}
	dialer := net.Dialer{}
	var lastErr error
	for _, resolved := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	return nil, lastErr
}

func (p *EgressPolicy) hostAllowed(host string) bool {
	if len(p.allowedHosts) == 0 {
		return true
	}
	for _, pattern := range p.allowedHosts {
		if pattern == host {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func (p *EgressPolicy) addressAllowed(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range p.allowedCIDRs {
		if prefix.Contains(address) {
			return true
		}
	}
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return false
	}
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	if address.IsPrivate() {
		return p.allowPrivateNetworks
	}
	return address.IsGlobalUnicast()
}

func (p *EgressPolicy) block(reason string) error {
	p.blockedTotal.Add(1)
	if p.onBlock != nil {
		p.onBlock(EgressBlockEvent{Reason: reason})
	}
	return EgressBlockedError{Reason: reason}
}

func normalizeAllowedHosts(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimSuffix(value, ".")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateAllowedHostPattern(raw string) error {
	host := normalizeHostname(raw)
	if strings.HasPrefix(host, "*.") {
		host = strings.TrimPrefix(host, "*.")
	}
	if host == "" || strings.ContainsAny(host, "*/?#@") || strings.Contains(host, "://") {
		return fmt.Errorf("invalid MCP egress allowed host %q", raw)
	}
	if strings.Contains(host, ":") {
		if _, err := netip.ParseAddr(host); err != nil {
			return fmt.Errorf("invalid MCP egress allowed host %q", raw)
		}
	}
	return nil
}

func normalizeHostname(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func sameURLAuthority(left *url.URL, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
