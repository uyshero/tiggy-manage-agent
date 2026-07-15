package skillmarketplace

import "testing"

func TestPolicyDefaultsToAllowed(t *testing.T) {
	decision := (Policy{}).EvaluatePackage(Source{Repository: "acme/review", Ref: "main"}, "")
	if !decision.Allowed || len(decision.Violations) != 0 || len(decision.Checks) != 3 {
		t.Fatalf("unexpected default decision: %#v", decision)
	}
}

func TestPolicyChecksRepositoryAndCommitPin(t *testing.T) {
	policy := Policy{AllowedOwners: []string{"acme"}, AllowedRepositories: []string{"trusted/special"}, RequireCommitSHA: true}
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, source := range []Source{
		{Repository: "acme/review", Ref: sha},
		{Repository: "trusted/special", Ref: sha},
	} {
		if decision := policy.EvaluateSource(source); !decision.Allowed {
			t.Fatalf("expected source to be allowed: source=%#v decision=%#v", source, decision)
		}
	}
	decision := policy.EvaluateSource(Source{Repository: "other/review", Ref: "main"})
	if decision.Allowed || len(decision.Violations) != 2 {
		t.Fatalf("expected repository and ref violations: %#v", decision)
	}
}

func TestPolicyChecksLicenseWithDenyPrecedence(t *testing.T) {
	policy := Policy{
		AllowedLicenses: []string{"mit", "apache-2.0", "proprietary"},
		DeniedLicenses:  []string{"proprietary"},
		RequireLicense:  true,
	}
	if decision := policy.EvaluatePackage(Source{Repository: "acme/review"}, "MIT"); !decision.Allowed {
		t.Fatalf("expected MIT to be allowed: %#v", decision)
	}
	if decision := policy.EvaluatePackage(Source{Repository: "acme/review"}, "Limited use"); decision.Allowed {
		t.Fatalf("license token matching must not treat limited as MIT: %#v", decision)
	}
	if decision := policy.EvaluatePackage(Source{Repository: "acme/review"}, "Proprietary. See LICENSE.txt"); decision.Allowed || len(decision.Violations) != 1 {
		t.Fatalf("expected denied license: %#v", decision)
	}
	if decision := policy.EvaluatePackage(Source{Repository: "acme/review"}, ""); decision.Allowed || len(decision.Violations) != 1 {
		t.Fatalf("expected missing license violation: %#v", decision)
	}
}

func TestPolicyRevisionUsesNormalizedConfig(t *testing.T) {
	left := Policy{AllowedOwners: []string{" ACME ", "trusted", "acme"}, AllowedLicenses: []string{"MIT"}}
	right := Policy{AllowedOwners: []string{"trusted", "acme"}, AllowedLicenses: []string{"mit"}}
	leftRevision, err := PolicyRevision(left)
	if err != nil {
		t.Fatalf("left policy revision: %v", err)
	}
	rightRevision, err := PolicyRevision(right)
	if err != nil {
		t.Fatalf("right policy revision: %v", err)
	}
	if leftRevision == "" || leftRevision != rightRevision {
		t.Fatalf("expected stable normalized revision: left=%s right=%s", leftRevision, rightRevision)
	}
}
