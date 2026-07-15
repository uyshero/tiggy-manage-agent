package managedagents

import (
	"testing"
	"time"
)

func TestFairSubagentPromotionOrderRoundRobinsAcrossOwnersWithinPriority(t *testing.T) {
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	candidates := []subagentPromotionCandidate{
		{ID: "req-a1", WorkspaceID: "wksp", OwnerID: "owner-a", Priority: 10, QueuedAt: base.Add(0 * time.Second)},
		{ID: "req-a2", WorkspaceID: "wksp", OwnerID: "owner-a", Priority: 10, QueuedAt: base.Add(1 * time.Second)},
		{ID: "req-a3", WorkspaceID: "wksp", OwnerID: "owner-a", Priority: 10, QueuedAt: base.Add(2 * time.Second)},
		{ID: "req-b1", WorkspaceID: "wksp", OwnerID: "owner-b", Priority: 10, QueuedAt: base.Add(3 * time.Second)},
		{ID: "req-b2", WorkspaceID: "wksp", OwnerID: "owner-b", Priority: 10, QueuedAt: base.Add(4 * time.Second)},
	}

	ordered := fairSubagentPromotionOrder(candidates, len(candidates))
	expected := []string{"req-a1", "req-b1", "req-a2", "req-b2", "req-a3"}
	if len(ordered) != len(expected) {
		t.Fatalf("expected %d ordered ids, got %d: %#v", len(expected), len(ordered), ordered)
	}
	for index := range expected {
		if ordered[index] != expected[index] {
			t.Fatalf("expected order %v, got %v", expected, ordered)
		}
	}
}

func TestFairSubagentPromotionOrderPreservesHigherPriorityFirst(t *testing.T) {
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	candidates := []subagentPromotionCandidate{
		{ID: "req-low", WorkspaceID: "wksp", OwnerID: "owner-a", Priority: 1, QueuedAt: base},
		{ID: "req-high-a", WorkspaceID: "wksp", OwnerID: "owner-a", Priority: 5, QueuedAt: base.Add(1 * time.Second)},
		{ID: "req-high-b", WorkspaceID: "wksp", OwnerID: "owner-b", Priority: 5, QueuedAt: base.Add(2 * time.Second)},
	}

	ordered := fairSubagentPromotionOrder(candidates, len(candidates))
	expected := []string{"req-high-a", "req-high-b", "req-low"}
	for index := range expected {
		if ordered[index] != expected[index] {
			t.Fatalf("expected order %v, got %v", expected, ordered)
		}
	}
}
