package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/observability"
)

type authorizationAuditKey struct {
	Outcome  string
	Reason   string
	AuthType string
}

type authorizationAudit struct {
	mu     sync.RWMutex
	counts map[authorizationAuditKey]int64
	sink   observability.AuthorizationDecisionSink
}

func newAuthorizationAudit(sink observability.AuthorizationDecisionSink) *authorizationAudit {
	return &authorizationAudit{counts: map[authorizationAuditKey]int64{}, sink: sink}
}

func (a *authorizationAudit) record(outcome string, reason string, authType string) {
	if a == nil {
		return
	}
	key := authorizationAuditKey{
		Outcome: strings.TrimSpace(outcome), Reason: strings.TrimSpace(reason), AuthType: strings.TrimSpace(authType),
	}
	if key.AuthType == "" {
		key.AuthType = "unknown"
	}
	a.mu.Lock()
	a.counts[key]++
	a.mu.Unlock()
}

func (a *authorizationAudit) snapshot() []observability.AuthorizationDecisionMetric {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	metrics := make([]observability.AuthorizationDecisionMetric, 0, len(a.counts))
	for key, count := range a.counts {
		metrics = append(metrics, observability.AuthorizationDecisionMetric{
			Outcome: key.Outcome, Reason: key.Reason, AuthType: key.AuthType, Count: count,
		})
	}
	a.mu.RUnlock()
	sort.Slice(metrics, func(i, j int) bool {
		left := metrics[i].AuthType + "\x00" + metrics[i].Outcome + "\x00" + metrics[i].Reason
		right := metrics[j].AuthType + "\x00" + metrics[j].Outcome + "\x00" + metrics[j].Reason
		return left < right
	})
	return metrics
}

func (a *authorizationAudit) exporterMetrics() observability.SecurityAuditExporterMetrics {
	if a == nil || a.sink == nil {
		return observability.SecurityAuditExporterMetrics{}
	}
	provider, ok := a.sink.(observability.SecurityAuditMetricsProvider)
	if !ok {
		return observability.SecurityAuditExporterMetrics{Enabled: true}
	}
	return provider.SecurityAuditMetrics()
}

func (s *Server) auditAuthorizationDecision(r *http.Request, principal Principal, outcome string, reason string, requiredRole string, decisionErr error) {
	if s == nil {
		return
	}
	authType := strings.TrimSpace(principal.AuthType)
	if authType == "" && s.authenticator != nil {
		authType = s.authenticator.config.Mode
	}
	if authType == "" {
		authType = "unknown"
	}
	if s.authorizationAudit != nil {
		s.authorizationAudit.record(outcome, reason, authType)
	}
	event := observability.AuthorizationDecisionEvent{
		At: time.Now().UTC(), Outcome: outcome, Reason: reason, AuthType: authType, RequiredRole: requiredRole,
		Subject: principal.Subject, OrganizationID: principal.OrganizationID, WorkspaceID: principal.WorkspaceID,
		OwnerID: principal.OwnerID, Roles: normalizedStringList(principal.Roles),
		AuthorizationSources: normalizedStringList(principal.AuthorizationSources),
	}
	if r != nil {
		event.Method = r.Method
		event.Path = r.URL.EscapedPath()
	}
	if decisionErr != nil {
		event.Detail = decisionErr.Error()
	}
	if s.authorizationAudit != nil && s.authorizationAudit.sink != nil {
		s.authorizationAudit.sink.EnqueueAuthorizationDecision(event)
	}
	if s.logger == nil {
		return
	}
	attributes := []any{
		"event", "authorization_decision",
		"outcome", outcome,
		"reason", reason,
		"auth_type", authType,
	}
	if r != nil {
		attributes = append(attributes, "method", r.Method, "path", r.URL.EscapedPath())
	}
	if requiredRole != "" {
		attributes = append(attributes, "required_role", requiredRole)
	}
	if principal.Subject != "" {
		attributes = append(attributes, "subject", principal.Subject)
	}
	if principal.OrganizationID != "" {
		attributes = append(attributes, "organization_id", principal.OrganizationID)
	}
	if principal.WorkspaceID != "" {
		attributes = append(attributes, "workspace_id", principal.WorkspaceID)
	}
	if principal.OwnerID != "" {
		attributes = append(attributes, "owner_id", principal.OwnerID)
	}
	if len(principal.Roles) > 0 {
		attributes = append(attributes, "roles", normalizedStringList(principal.Roles))
	}
	if len(principal.AuthorizationSources) > 0 {
		attributes = append(attributes, "authorization_sources", normalizedStringList(principal.AuthorizationSources))
	}
	if decisionErr != nil {
		attributes = append(attributes, "detail", decisionErr.Error())
	}
	if outcome == "allowed" {
		s.logger.Info("authorization decision", attributes...)
		return
	}
	s.logger.Warn("authorization decision", attributes...)
}

func normalizedStringList(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func authorizationOutcomeForError(err error) string {
	if errors.Is(err, managedagents.ErrForbidden) || errors.Is(err, managedagents.ErrNotFound) || errors.Is(err, managedagents.ErrInvalid) {
		return "denied"
	}
	return "error"
}

func authorizationReasonForResourceError(err error) string {
	switch {
	case errors.Is(err, managedagents.ErrForbidden):
		return "resource_scope_denied"
	case errors.Is(err, managedagents.ErrNotFound):
		return "resource_not_found"
	case errors.Is(err, managedagents.ErrInvalid):
		return "resource_scope_invalid"
	default:
		return "resource_scope_error"
	}
}
