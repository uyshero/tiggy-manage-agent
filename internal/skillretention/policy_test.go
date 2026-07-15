package skillretention

import "testing"

func TestNormalizePolicyDefaultsAndBounds(t *testing.T) {
	policy, err := NormalizePolicy(Policy{Enabled: true})
	if err != nil {
		t.Fatalf("normalize defaults: %v", err)
	}
	if !policy.Enabled || policy.RetentionDays != DefaultRetentionDays || policy.DeleteLimit != DefaultDeleteLimit {
		t.Fatalf("unexpected normalized policy: %#v", policy)
	}
	for _, invalid := range []Policy{
		{RetentionDays: -1, DeleteLimit: 1},
		{RetentionDays: MaxRetentionDays + 1, DeleteLimit: 1},
		{RetentionDays: 1, DeleteLimit: -1},
		{RetentionDays: 1, DeleteLimit: MaxDeleteLimit + 1},
	} {
		if _, err := NormalizePolicy(invalid); err == nil {
			t.Fatalf("expected invalid policy rejection: %#v", invalid)
		}
	}
}

func TestPolicyRevisionIsStableAndConfigSensitive(t *testing.T) {
	left, err := PolicyRevision(Policy{Enabled: true, RetentionDays: 30, DeleteLimit: 100})
	if err != nil {
		t.Fatalf("first revision: %v", err)
	}
	right, err := PolicyRevision(Policy{Enabled: true, RetentionDays: 30, DeleteLimit: 100})
	if err != nil {
		t.Fatalf("second revision: %v", err)
	}
	changed, err := PolicyRevision(Policy{Enabled: false, RetentionDays: 30, DeleteLimit: 100})
	if err != nil {
		t.Fatalf("changed revision: %v", err)
	}
	if left != right || left == changed {
		t.Fatalf("unexpected revisions: left=%s right=%s changed=%s", left, right, changed)
	}
}
