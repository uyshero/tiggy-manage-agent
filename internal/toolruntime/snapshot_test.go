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
	if second[0].Name != "read_inspect" || bytes.Equal(first[0].InputSchema, second[0].InputSchema) ||
		bytes.Equal(firstContext, secondContext) || bytes.Equal(firstModelTools[0].Function.Parameters, secondModelTools[0].Function.Parameters) {
		t.Fatalf("snapshot definitions were mutated: first=%+v second=%+v", first, second)
	}
}

func TestSnapshotMiddlewareRevisionIncludesVersionAndOrder(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(readRuntime{})
	policy := tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess}
	left, err := toolruntime.NewSnapshotWithMiddleware(registry, policy, []toolruntime.ToolMiddleware{
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "audit", Version: "1"}},
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "guard", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	versionChanged, err := toolruntime.NewSnapshotWithMiddleware(registry, policy, []toolruntime.ToolMiddleware{
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "audit", Version: "2"}},
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "guard", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := toolruntime.NewSnapshotWithMiddleware(registry, policy, []toolruntime.ToolMiddleware{
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "guard", Version: "1"}},
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "audit", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.MiddlewareRevision() == versionChanged.MiddlewareRevision() || left.MiddlewareRevision() == reordered.MiddlewareRevision() {
		t.Fatalf("middleware revision did not bind version/order: left=%s version=%s order=%s", left.MiddlewareRevision(), versionChanged.MiddlewareRevision(), reordered.MiddlewareRevision())
	}
	descriptors := left.Middleware()
	descriptors[0].ID = "mutated"
	if left.Middleware()[0].ID != "audit" {
		t.Fatal("middleware descriptors were not defensively copied")
	}
}

func TestSnapshotRejectsDuplicateMiddlewareIdentity(t *testing.T) {
	t.Parallel()

	_, err := toolruntime.NewSnapshotWithMiddleware(tools.NewRegistry(readRuntime{}), tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess}, []toolruntime.ToolMiddleware{
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "audit", Version: "1"}},
		recordingMiddleware{descriptor: toolruntime.MiddlewareDescriptor{ID: "audit", Version: "2"}},
	})
	var contractError *tools.ToolContractError
	if !errors.As(err, &contractError) || contractError.ErrorCode() != "invalid_tool_middleware" {
		t.Fatalf("NewSnapshotWithMiddleware() error = %T %v", err, err)
	}
}
