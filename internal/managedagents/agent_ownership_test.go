package managedagents

import "testing"

func TestNormalizeAgentOwnership(t *testing.T) {
	personal, err := NormalizeAgentOwnership("wksp_1", AgentOwnership{OwnerType: AgentOwnerUser, OwnerID: "usr_1", AgentKind: AgentKindGeneral})
	if err != nil || personal.Visibility != AgentVisibilityPrivate || personal.AgentKind != AgentKindGeneral {
		t.Fatalf("normalize personal ownership: %+v err=%v", personal, err)
	}
	workspace, err := NormalizeAgentOwnership("wksp_1", AgentOwnership{})
	if err != nil || workspace.OwnerType != AgentOwnerWorkspace || workspace.OwnerID != "wksp_1" || workspace.Visibility != AgentVisibilityWorkspace || workspace.AgentKind != AgentKindCustom {
		t.Fatalf("normalize workspace ownership: %+v err=%v", workspace, err)
	}
	if _, err := NormalizeAgentOwnership("wksp_1", AgentOwnership{OwnerType: AgentOwnerUser, OwnerID: "usr_1", Visibility: AgentVisibilityWorkspace}); err == nil {
		t.Fatal("expected public personal Agent rejection")
	}
	if _, err := NormalizeAgentOwnership("wksp_1", AgentOwnership{OwnerType: AgentOwnerWorkspace, OwnerID: "wksp_2"}); err == nil {
		t.Fatal("expected mismatched workspace owner rejection")
	}
}
