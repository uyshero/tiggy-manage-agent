package skillmarketplace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	PolicyCheckRepository = "repository_allowlist"
	PolicyCheckRefPin     = "commit_ref_pin"
	PolicyCheckLicense    = "license"
)

var commitSHARefPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var githubOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type Policy struct {
	AllowedOwners           []string          `json:"allowed_owners,omitempty"`
	AllowedRepositories     []string          `json:"allowed_repositories,omitempty"`
	RequireCommitSHA        bool              `json:"require_commit_sha,omitempty"`
	AllowedLicenses         []string          `json:"allowed_licenses,omitempty"`
	DeniedLicenses          []string          `json:"denied_licenses,omitempty"`
	RequireLicense          bool              `json:"require_license,omitempty"`
	RequireAttestation      bool              `json:"require_attestation,omitempty"`
	TrustedAttestationKeys  map[string]string `json:"trusted_attestation_keys,omitempty"`
	StaticScanBlockSeverity string            `json:"static_scan_block_severity,omitempty"`
}

type PolicyDecision struct {
	Allowed        bool          `json:"allowed"`
	PolicySource   string        `json:"policy_source,omitempty"`
	PolicyID       string        `json:"policy_id,omitempty"`
	PolicyVersion  int           `json:"policy_version,omitempty"`
	PolicyRevision string        `json:"policy_revision,omitempty"`
	Checks         []PolicyCheck `json:"checks"`
	Violations     []string      `json:"violations,omitempty"`
}

type PolicyCheck struct {
	Name     string `json:"name"`
	Enforced bool   `json:"enforced"`
	Passed   bool   `json:"passed"`
	Message  string `json:"message"`
}

func (p Policy) EvaluateSource(source Source) PolicyDecision {
	provider := strings.ToLower(strings.TrimSpace(source.Provider))
	if provider == ArtifactProvider || provider == CatalogProvider {
		label := "local artifact"
		if provider == CatalogProvider {
			label = "internal catalog"
		}
		return newPolicyDecision([]PolicyCheck{
			{Name: PolicyCheckRepository, Passed: true, Message: label + " source does not use an external repository"},
			{Name: PolicyCheckRefPin, Passed: true, Message: label + " is pinned by its SHA-256 revision"},
		})
	}
	repository := strings.ToLower(strings.TrimSpace(source.Repository))
	owner := repository
	if before, _, ok := strings.Cut(repository, "/"); ok {
		owner = before
	}
	repositories := normalizedPolicyValues(p.AllowedRepositories)
	owners := normalizedPolicyValues(p.AllowedOwners)
	repositoryEnforced := len(repositories) > 0 || len(owners) > 0
	repositoryPassed := !repositoryEnforced || policyValueContains(repositories, repository) || policyValueContains(owners, owner)
	repositoryMessage := "repository allowlist is not configured"
	if repositoryEnforced && repositoryPassed {
		repositoryMessage = fmt.Sprintf("repository %s is allowed", repository)
	} else if repositoryEnforced {
		repositoryMessage = fmt.Sprintf("repository %s is not allowed", repository)
	}

	ref := strings.TrimSpace(source.Ref)
	refPassed := !p.RequireCommitSHA || commitSHARefPattern.MatchString(ref)
	refMessage := "commit SHA pin is not required"
	if p.RequireCommitSHA && refPassed {
		refMessage = "source ref is pinned to a full commit SHA"
	} else if p.RequireCommitSHA {
		refMessage = "source ref must be a full 40-character commit SHA"
	}

	return newPolicyDecision([]PolicyCheck{
		{Name: PolicyCheckRepository, Enforced: repositoryEnforced, Passed: repositoryPassed, Message: repositoryMessage},
		{Name: PolicyCheckRefPin, Enforced: p.RequireCommitSHA, Passed: refPassed, Message: refMessage},
	})
}

func (p Policy) EvaluatePackage(source Source, license string) PolicyDecision {
	decision := p.EvaluateSource(source)
	allowedLicenses := normalizedPolicyValues(p.AllowedLicenses)
	deniedLicenses := normalizedPolicyValues(p.DeniedLicenses)
	licenseEnforced := p.RequireLicense || len(allowedLicenses) > 0 || len(deniedLicenses) > 0
	normalizedLicense := strings.ToLower(strings.TrimSpace(license))
	licensePassed := true
	licenseMessage := "license policy is not configured"
	switch {
	case !licenseEnforced:
	case normalizedLicense == "" && (p.RequireLicense || len(allowedLicenses) > 0):
		licensePassed = false
		licenseMessage = "skill package must declare a license"
	case normalizedLicense == "":
		licenseMessage = "package has no declared license and missing licenses are allowed"
	case policyValueMatchesExpression(deniedLicenses, normalizedLicense):
		licensePassed = false
		licenseMessage = fmt.Sprintf("license %q matches the deny list", strings.TrimSpace(license))
	case len(allowedLicenses) > 0 && !policyValueMatchesExpression(allowedLicenses, normalizedLicense):
		licensePassed = false
		licenseMessage = fmt.Sprintf("license %q does not match the allow list", strings.TrimSpace(license))
	default:
		licenseMessage = fmt.Sprintf("license %q is allowed", strings.TrimSpace(license))
	}
	decision.Checks = append(decision.Checks, PolicyCheck{
		Name: PolicyCheckLicense, Enforced: licenseEnforced, Passed: licensePassed, Message: licenseMessage,
	})
	return newPolicyDecision(decision.Checks)
}

func NormalizePolicy(policy Policy) (Policy, error) {
	policy.AllowedOwners = normalizedPolicyValues(policy.AllowedOwners)
	policy.AllowedRepositories = normalizedPolicyValues(policy.AllowedRepositories)
	policy.AllowedLicenses = normalizedPolicyValues(policy.AllowedLicenses)
	policy.DeniedLicenses = normalizedPolicyValues(policy.DeniedLicenses)
	for _, values := range [][]string{policy.AllowedOwners, policy.AllowedRepositories, policy.AllowedLicenses, policy.DeniedLicenses} {
		if len(values) > 100 {
			return Policy{}, fmt.Errorf("policy lists cannot exceed 100 entries")
		}
	}
	for _, owner := range policy.AllowedOwners {
		if !githubOwnerPattern.MatchString(owner) {
			return Policy{}, fmt.Errorf("invalid GitHub owner %q", owner)
		}
	}
	for _, repository := range policy.AllowedRepositories {
		if !githubRepositoryPattern.MatchString(repository) {
			return Policy{}, fmt.Errorf("invalid GitHub repository %q", repository)
		}
	}
	for _, license := range append(append([]string{}, policy.AllowedLicenses...), policy.DeniedLicenses...) {
		if len(license) > 100 || strings.ContainsAny(license, ", \t\r\n") {
			return Policy{}, fmt.Errorf("license policy entry %q must be one token up to 100 characters", license)
		}
	}
	trustedKeys, err := normalizeTrustedAttestationKeys(policy.TrustedAttestationKeys)
	if err != nil {
		return Policy{}, err
	}
	policy.TrustedAttestationKeys = trustedKeys
	if policy.RequireAttestation && len(policy.TrustedAttestationKeys) == 0 {
		return Policy{}, fmt.Errorf("require_attestation requires at least one trusted attestation key")
	}
	policy.StaticScanBlockSeverity = strings.ToLower(strings.TrimSpace(policy.StaticScanBlockSeverity))
	switch policy.StaticScanBlockSeverity {
	case "", SeverityMedium, SeverityHigh, SeverityCritical:
	default:
		return Policy{}, fmt.Errorf("static_scan_block_severity must be medium, high, or critical")
	}
	sort.Strings(policy.AllowedOwners)
	sort.Strings(policy.AllowedRepositories)
	sort.Strings(policy.AllowedLicenses)
	sort.Strings(policy.DeniedLicenses)
	return policy, nil
}

func PolicyRevision(policy Policy) (string, error) {
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func BindPolicyDecision(decision PolicyDecision, effective EffectivePolicy) PolicyDecision {
	decision.PolicySource = effective.Source
	decision.PolicyID = effective.Policy.ID
	decision.PolicyVersion = effective.Version.Version
	decision.PolicyRevision = effective.Revision
	return decision
}

func newPolicyDecision(checks []PolicyCheck) PolicyDecision {
	decision := PolicyDecision{Allowed: true, Checks: checks, Violations: []string{}}
	for _, check := range checks {
		if check.Enforced && !check.Passed {
			decision.Allowed = false
			decision.Violations = append(decision.Violations, check.Message)
		}
	}
	return decision
}

func normalizedPolicyValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func policyValueContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func policyValueMatchesExpression(values []string, expression string) bool {
	tokens := strings.FieldsFunc(expression, func(char rune) bool {
		return !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '.' || char == '+')
	})
	for index := range tokens {
		tokens[index] = strings.Trim(tokens[index], ".-+")
	}
	for _, value := range values {
		for _, token := range tokens {
			if value == token {
				return true
			}
		}
	}
	return false
}
