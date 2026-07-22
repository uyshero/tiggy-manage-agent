package managedagents

import (
	"testing"

	"tiggy-manage-agent/internal/agentcore"
)

func TestAgentLoopFastCommitRoutingExcludesInitialAndTerminalOperations(t *testing.T) {
	t.Parallel()

	transition := agentcore.Transition{ExpectedRevision: 1}
	if !useAgentLoopFastCommit(agentLoopCommit{}, transition) {
		t.Fatal("ordinary non-initial commit should use fast path")
	}
	if useAgentLoopFastCommit(agentLoopCommit{}, agentcore.Transition{ExpectedRevision: 0}) {
		t.Fatal("initial commit must use the full path")
	}
	for name, action := range map[string]agentLoopAction{
		"park":     agentLoopPark{},
		"complete": agentLoopComplete{},
		"fail":     agentLoopFail{},
		"cancel":   agentLoopCancel{},
	} {
		if useAgentLoopFastCommit(action, transition) {
			t.Fatalf("%s operation must use the full path", name)
		}
	}
}
