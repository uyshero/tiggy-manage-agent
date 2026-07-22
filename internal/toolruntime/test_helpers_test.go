package toolruntime_test

import (
	"testing"

	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

func mustToolSnapshot(t testing.TB, registry tools.Registry, policy tools.InterventionPolicy) toolruntime.Snapshot {
	t.Helper()
	snapshot, err := toolruntime.NewSnapshot(registry, policy)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	return snapshot
}

func fullAccessSnapshot(t testing.TB, registry tools.Registry) toolruntime.Snapshot {
	t.Helper()
	return mustToolSnapshot(t, registry, tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess})
}
