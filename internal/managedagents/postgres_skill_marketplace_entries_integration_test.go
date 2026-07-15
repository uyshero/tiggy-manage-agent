package managedagents

import (
	"context"
	"errors"
	"testing"

	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skills"
)

func TestPostgresSkillMarketplaceEntryLifecycle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	ctx := context.Background()
	workspaceID := createPostgresIntegrationWorkspace(t, store, "skill-marketplace-entry")
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM skill_marketplace_entries WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM skills WHERE workspace_id = $1`, workspaceID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	})

	skill, err := store.CreateSkill(ctx, skills.CreateSkillInput{
		WorkspaceID: workspaceID, Identifier: "marketplace-lifecycle", Title: "Marketplace Lifecycle", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	for version := 1; version <= 2; version++ {
		if _, err := store.CreateSkillVersion(ctx, skills.CreateVersionInput{
			SkillID: skill.ID, ContentFormat: "markdown", ContentText: "lifecycle", CreatedBy: "integration-test",
		}); err != nil {
			t.Fatalf("create skill version %d: %v", version, err)
		}
	}

	first, err := store.CreateMarketplaceEntry(ctx, skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: workspaceID, SkillID: skill.ID, SkillVersion: 1,
		Summary: " First release ", Category: "Engineering", Tags: []string{"Review", "review"}, CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create marketplace draft: %v", err)
	}
	if first.Status != skillmarketplace.MarketplaceEntryStatusDraft || len(first.Tags) != 1 {
		t.Fatalf("unexpected marketplace draft: %#v", first)
	}
	if _, err := store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: first.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished, Actor: "reviewer",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected cross-state publish conflict, got %v", err)
	}
	first, err = store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: first.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPendingReview, Actor: "publisher",
	})
	if err != nil || first.SubmittedAt == nil {
		t.Fatalf("submit marketplace entry: entry=%#v err=%v", first, err)
	}
	if _, err := store.UpdateMarketplaceEntry(ctx, skillmarketplace.UpdateMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: first.ID, Summary: "too late", UpdatedBy: "publisher",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected pending entry edit conflict, got %v", err)
	}
	first, err = store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: first.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished,
		Actor: "reviewer", Note: "approved",
	})
	if err != nil || first.PublishedAt == nil || first.ReviewNote != "approved" {
		t.Fatalf("publish marketplace entry: entry=%#v err=%v", first, err)
	}

	second, err := store.CreateMarketplaceEntry(ctx, skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: workspaceID, SkillID: skill.ID, SkillVersion: 2, CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create second marketplace draft: %v", err)
	}
	second, err = store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: second.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPendingReview, Actor: "publisher",
	})
	if err != nil {
		t.Fatalf("submit second marketplace entry: %v", err)
	}
	if _, err := store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: second.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished, Actor: "reviewer",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected one published version conflict, got %v", err)
	}
	first, err = store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: first.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusWithdrawn,
		Actor: "operator", Note: "superseded",
	})
	if err != nil || first.WithdrawnAt == nil || first.WithdrawalReason != "superseded" {
		t.Fatalf("withdraw first marketplace entry: entry=%#v err=%v", first, err)
	}
	second, err = store.TransitionMarketplaceEntry(ctx, skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: workspaceID, EntryID: second.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished,
		Actor: "reviewer", Note: "v2 approved",
	})
	if err != nil || second.Status != skillmarketplace.MarketplaceEntryStatusPublished {
		t.Fatalf("publish second marketplace entry: entry=%#v err=%v", second, err)
	}

	listed, err := store.ListMarketplaceEntries(ctx, skillmarketplace.ListMarketplaceEntriesInput{
		WorkspaceID: workspaceID, Status: skillmarketplace.MarketplaceEntryStatusPublished,
	})
	if err != nil || len(listed) != 1 || listed[0].SkillVersion != 2 {
		t.Fatalf("unexpected published entries: entries=%#v err=%v", listed, err)
	}
}
