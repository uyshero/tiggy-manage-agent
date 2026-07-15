package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	RoleViewer   = "viewer"
	RoleMember   = "member"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

type OIDCClaimMapping struct {
	SubjectClaim        string                    `json:"subject_claim"`
	OrganizationClaim   string                    `json:"organization_claim,omitempty"`
	WorkspaceClaim      string                    `json:"workspace_claim,omitempty"`
	OwnerClaim          string                    `json:"owner_claim,omitempty"`
	RolesClaim          string                    `json:"roles_claim,omitempty"`
	GroupsClaim         string                    `json:"groups_claim,omitempty"`
	RoleMappings        map[string]string         `json:"role_mappings,omitempty"`
	GroupMappings       map[string]OIDCGroupGrant `json:"group_mappings,omitempty"`
	AllowedWorkspaceIDs []string                  `json:"allowed_workspace_ids,omitempty"`
	RequireGroupMapping bool                      `json:"require_group_mapping,omitempty"`
}

type OIDCGroupGrant struct {
	OrganizationID string   `json:"organization_id,omitempty"`
	WorkspaceID    string   `json:"workspace_id,omitempty"`
	Roles          []string `json:"roles,omitempty"`
}

type ResolvedIdentity struct {
	Subject              string
	OrganizationID       string
	WorkspaceID          string
	OwnerID              string
	Roles                []string
	Groups               []string
	AuthorizationSources []string
}

func DefaultOIDCClaimMapping() OIDCClaimMapping {
	return OIDCClaimMapping{
		SubjectClaim: "sub", OrganizationClaim: "organization_id", WorkspaceClaim: "workspace_id",
		OwnerClaim: "owner_id", RolesClaim: "roles", GroupsClaim: "groups",
	}
}

func ParseOIDCClaimMapping(raw string) (OIDCClaimMapping, error) {
	mapping := DefaultOIDCClaimMapping()
	if strings.TrimSpace(raw) != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&mapping); err != nil {
			return OIDCClaimMapping{}, fmt.Errorf("decode OIDC claim mapping: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return OIDCClaimMapping{}, errors.New("decode OIDC claim mapping: multiple JSON values")
		}
	}
	if err := mapping.Validate(); err != nil {
		return OIDCClaimMapping{}, err
	}
	return mapping.normalized(), nil
}

func (m OIDCClaimMapping) Validate() error {
	m = m.normalized()
	if m.SubjectClaim == "" {
		return errors.New("OIDC subject_claim is required")
	}
	if m.WorkspaceClaim == "" && len(m.GroupMappings) == 0 {
		return errors.New("OIDC workspace_claim or group_mappings is required")
	}
	if m.RolesClaim == "" && !groupMappingsContainRoles(m.GroupMappings) {
		return errors.New("OIDC roles_claim or group mapping roles are required")
	}
	for source, target := range m.RoleMappings {
		if strings.TrimSpace(source) == "" || !isRole(target) {
			return fmt.Errorf("invalid OIDC role mapping %q -> %q", source, target)
		}
	}
	for group, grant := range m.GroupMappings {
		if strings.TrimSpace(group) == "" {
			return errors.New("OIDC group mapping name cannot be empty")
		}
		if strings.TrimSpace(grant.WorkspaceID) == "" && strings.TrimSpace(grant.OrganizationID) == "" && len(grant.Roles) == 0 {
			return fmt.Errorf("OIDC group mapping %q has no grant", group)
		}
		for _, role := range grant.Roles {
			if !isRole(role) {
				return fmt.Errorf("OIDC group mapping %q contains unsupported role %q", group, role)
			}
		}
	}
	for _, workspaceID := range m.AllowedWorkspaceIDs {
		if strings.TrimSpace(workspaceID) == "" {
			return errors.New("OIDC allowed_workspace_ids cannot contain an empty value")
		}
	}
	return nil
}

func (m OIDCClaimMapping) HasTenantRestriction() bool {
	m = m.normalized()
	if len(m.AllowedWorkspaceIDs) > 0 {
		return true
	}
	for _, grant := range m.GroupMappings {
		if grant.WorkspaceID != "" {
			return true
		}
	}
	return false
}

func (m OIDCClaimMapping) Resolve(claims map[string]any) (ResolvedIdentity, error) {
	m = m.normalized()
	if err := m.Validate(); err != nil {
		return ResolvedIdentity{}, err
	}
	resolved := ResolvedIdentity{
		Subject:        claimString(claims, m.SubjectClaim),
		OrganizationID: claimString(claims, m.OrganizationClaim),
		WorkspaceID:    claimString(claims, m.WorkspaceClaim),
		OwnerID:        claimString(claims, m.OwnerClaim),
		Groups:         claimStringList(claims, m.GroupsClaim),
	}
	if resolved.Subject == "" {
		return ResolvedIdentity{}, fmt.Errorf("OIDC claim %q did not resolve a subject", m.SubjectClaim)
	}
	sources := map[string]bool{"subject_claim:" + m.SubjectClaim: true}
	if resolved.OrganizationID != "" {
		sources["organization_claim:"+m.OrganizationClaim] = true
	}
	if resolved.WorkspaceID != "" {
		sources["workspace_claim:"+m.WorkspaceClaim] = true
	}
	if resolved.OwnerID != "" {
		sources["owner_claim:"+m.OwnerClaim] = true
	}
	roles := make(map[string]bool)
	for _, externalRole := range claimStringList(claims, m.RolesClaim) {
		if role := m.resolveRole(externalRole); role != "" {
			roles[role] = true
			sources["roles_claim:"+m.RolesClaim] = true
			if _, mapped := m.RoleMappings[externalRole]; mapped {
				sources["role_mapping:"+externalRole] = true
			}
		}
	}
	matchedGroup := false
	for _, group := range resolved.Groups {
		grant, ok := m.GroupMappings[group]
		if !ok {
			continue
		}
		matchedGroup = true
		sources["group_mapping:"+group] = true
		var err error
		resolved.OrganizationID, err = mergeTenantValue("organization", resolved.OrganizationID, grant.OrganizationID)
		if err != nil {
			return ResolvedIdentity{}, fmt.Errorf("OIDC group %q: %w", group, err)
		}
		resolved.WorkspaceID, err = mergeTenantValue("workspace", resolved.WorkspaceID, grant.WorkspaceID)
		if err != nil {
			return ResolvedIdentity{}, fmt.Errorf("OIDC group %q: %w", group, err)
		}
		for _, role := range grant.Roles {
			roles[strings.ToLower(strings.TrimSpace(role))] = true
		}
	}
	if m.RequireGroupMapping && !matchedGroup {
		return ResolvedIdentity{}, errors.New("OIDC identity did not match a required group mapping")
	}
	if resolved.WorkspaceID == "" {
		return ResolvedIdentity{}, errors.New("OIDC identity did not resolve a workspace")
	}
	if len(m.AllowedWorkspaceIDs) > 0 && !containsString(m.AllowedWorkspaceIDs, resolved.WorkspaceID) {
		return ResolvedIdentity{}, fmt.Errorf("OIDC workspace %q is not allowed", resolved.WorkspaceID)
	}
	if len(m.AllowedWorkspaceIDs) > 0 {
		sources["workspace_allowlist"] = true
	}
	if len(roles) == 0 {
		return ResolvedIdentity{}, errors.New("OIDC identity did not resolve a supported role")
	}
	if resolved.OwnerID == "" {
		resolved.OwnerID = resolved.Subject
	}
	resolved.Roles = sortedRoles(roles)
	resolved.AuthorizationSources = sortedStringSet(sources)
	return resolved, nil
}

func (m OIDCClaimMapping) normalized() OIDCClaimMapping {
	m.SubjectClaim = strings.TrimSpace(m.SubjectClaim)
	m.OrganizationClaim = strings.TrimSpace(m.OrganizationClaim)
	m.WorkspaceClaim = strings.TrimSpace(m.WorkspaceClaim)
	m.OwnerClaim = strings.TrimSpace(m.OwnerClaim)
	m.RolesClaim = strings.TrimSpace(m.RolesClaim)
	m.GroupsClaim = strings.TrimSpace(m.GroupsClaim)
	roles := make(map[string]string, len(m.RoleMappings))
	for source, target := range m.RoleMappings {
		roles[strings.TrimSpace(source)] = strings.ToLower(strings.TrimSpace(target))
	}
	m.RoleMappings = roles
	groups := make(map[string]OIDCGroupGrant, len(m.GroupMappings))
	for group, grant := range m.GroupMappings {
		grant.OrganizationID = strings.TrimSpace(grant.OrganizationID)
		grant.WorkspaceID = strings.TrimSpace(grant.WorkspaceID)
		for index := range grant.Roles {
			grant.Roles[index] = strings.ToLower(strings.TrimSpace(grant.Roles[index]))
		}
		groups[strings.TrimSpace(group)] = grant
	}
	m.GroupMappings = groups
	allowed := make([]string, 0, len(m.AllowedWorkspaceIDs))
	seen := map[string]bool{}
	for _, workspaceID := range m.AllowedWorkspaceIDs {
		workspaceID = strings.TrimSpace(workspaceID)
		if !seen[workspaceID] {
			seen[workspaceID] = true
			allowed = append(allowed, workspaceID)
		}
	}
	m.AllowedWorkspaceIDs = allowed
	return m
}

func (m OIDCClaimMapping) resolveRole(external string) string {
	external = strings.TrimSpace(external)
	if mapped := m.RoleMappings[external]; mapped != "" {
		return mapped
	}
	direct := strings.ToLower(external)
	if isRole(direct) {
		return direct
	}
	return ""
}

func sortedStringSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value, included := range values {
		if included && strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func lookupClaim(claims map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	if value, ok := claims[path]; ok {
		return value, true
	}
	var current any = claims
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func claimString(claims map[string]any, path string) string {
	value, ok := lookupClaim(claims, path)
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func claimStringList(claims map[string]any, path string) []string {
	value, ok := lookupClaim(claims, path)
	if !ok {
		return nil
	}
	values := []string{}
	switch typed := value.(type) {
	case string:
		values = strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	case []string:
		values = append(values, typed...)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func mergeTenantValue(name string, current string, granted string) (string, error) {
	current = strings.TrimSpace(current)
	granted = strings.TrimSpace(granted)
	if granted == "" {
		return current, nil
	}
	if current != "" && current != granted {
		return "", fmt.Errorf("conflicting %s values %q and %q", name, current, granted)
	}
	return granted, nil
}

func groupMappingsContainRoles(groups map[string]OIDCGroupGrant) bool {
	for _, grant := range groups {
		if len(grant.Roles) > 0 {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleViewer, RoleMember, RoleOperator, RoleAdmin:
		return true
	default:
		return false
	}
}

func sortedRoles(roles map[string]bool) []string {
	result := make([]string, 0, len(roles))
	for role := range roles {
		if roles[role] && isRole(role) {
			result = append(result, role)
		}
	}
	sort.Slice(result, func(i, j int) bool { return roleLevel(result[i]) > roleLevel(result[j]) })
	return result
}

func roleLevel(role string) int {
	switch role {
	case RoleAdmin:
		return 4
	case RoleOperator:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}
