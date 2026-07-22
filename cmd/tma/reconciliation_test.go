package main

import "testing"

func TestNormalizeToolReconciliationOutcome(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"executed", "not_executed", "compensated"} {
		if got, ok := normalizeToolReconciliationOutcome("  " + value + "  "); !ok || got != value {
			t.Fatalf("normalizeToolReconciliationOutcome(%q) = (%q, %t)", value, got, ok)
		}
	}
	if got, ok := normalizeToolReconciliationOutcome("unknown"); ok || got != "" {
		t.Fatalf("normalizeToolReconciliationOutcome(unknown) = (%q, %t)", got, ok)
	}
}
