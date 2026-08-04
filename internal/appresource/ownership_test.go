package appresource

import "testing"

func TestNormalizeApplicationOwnership(t *testing.T) {
	ownership, err := Normalize(" app_000001 ", " biography/interviewer ", map[string]string{" release ": " alpha "})
	if err != nil {
		t.Fatal(err)
	}
	if ownership.AppID != "app_000001" || ownership.ExternalRef != "biography/interviewer" || ownership.Labels["release"] != "alpha" {
		t.Fatalf("unexpected ownership: %#v", ownership)
	}
	if _, err := Normalize("", "resource", nil); err == nil {
		t.Fatal("expected external_ref without app_id to fail")
	}
}

func TestNormalizeApplicationOwnershipClonesLabels(t *testing.T) {
	labels := map[string]string{"channel": "stable"}
	ownership, err := Normalize("app_000001", "agent", labels)
	if err != nil {
		t.Fatal(err)
	}
	labels["channel"] = "changed"
	if ownership.Labels["channel"] != "stable" {
		t.Fatal("normalized labels must not alias the caller map")
	}
}
