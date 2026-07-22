package toolruntime_test

import (
	"bytes"
	"errors"
	"testing"

	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

func TestSnapshotReturnsCodedPolicyError(t *testing.T) {
	t.Parallel()

	_, err := toolruntime.NewSnapshot(
		tools.NewRegistry(readRuntime{}),
		tools.InterventionPolicy{Mode: "unsupported"},
	)
	var contractError *tools.ToolContractError
	if !errors.As(err, &contractError) || contractError.ErrorCode() != "invalid_tool_policy" {
		t.Fatalf("NewSnapshot() error = %T %v", err, err)
	}
}

func TestSnapshotPolicyRevisionIncludesRuleSource(t *testing.T) {
	t.Parallel()

	rule := tools.PermissionRule{
		ID: "deny-config", Tool: "default.write_file", Argument: "path", Pattern: "/workspace/config/**",
		Behavior: tools.PermissionRuleDeny, Source: tools.PermissionRuleSourceSession,
	}
	left := mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{
		Mode: tools.InterventionModeFullAccess, Rules: []tools.PermissionRule{rule},
	})
	rule.Source = tools.PermissionRuleSourceWorkspace
	right := mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{
		Mode: tools.InterventionModeFullAccess, Rules: []tools.PermissionRule{rule},
	})
	if left.PolicyRevision() == right.PolicyRevision() {
		t.Fatalf("policy revisions ignore rule source: %q", left.PolicyRevision())
	}
}

func TestSnapshotDefinitionsAreDefensiveCopies(t *testing.T) {
	t.Parallel()

	snapshot, err := toolruntime.NewSnapshot(
		tools.NewRegistry(readRuntime{}),
		tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess},
	)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	first := snapshot.Definitions()
	firstContext := snapshot.ModelContext()
	firstModelTools := snapshot.ModelTools()
	first[0].Name = "mutated"
	first[0].InputSchema[0] = '['
	firstContext[0] = '['
	firstModelTools[0].Function.Parameters[0] = '['
	second := snapshot.Definitions()
	secondContext := snapshot.ModelContext()
	secondModelTools := snapshot.ModelTools()
	if second[0].Name != "read.inspect" || bytes.Equal(first[0].InputSchema, second[0].InputSchema) ||
		bytes.Equal(firstContext, secondContext) || bytes.Equal(firstModelTools[0].Function.Parameters, secondModelTools[0].Function.Parameters) {
		t.Fatalf("snapshot definitions were mutated: first=%+v second=%+v", first, second)
	}
}
