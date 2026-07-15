package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skills"
)

type marketplaceCatalogHTTPTestStore struct {
	*testStore
	catalogMu sync.Mutex
	nextEntry int
	entries   map[string]skillmarketplace.MarketplaceEntry
}

func newMarketplaceCatalogHTTPTestStore() *marketplaceCatalogHTTPTestStore {
	return &marketplaceCatalogHTTPTestStore{testStore: newTestStore(), entries: map[string]skillmarketplace.MarketplaceEntry{}}
}

func (s *marketplaceCatalogHTTPTestStore) CreateMarketplaceEntry(_ context.Context, input skillmarketplace.CreateMarketplaceEntryInput) (skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	summary, category, tags, err := skillmarketplace.NormalizeMarketplaceEntryMetadata(input.Summary, input.Category, input.Tags)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	skill, err := s.GetSkill(context.Background(), input.SkillID)
	if err != nil || skill.WorkspaceID != input.WorkspaceID {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrNotFound
	}
	version, err := s.GetSkillVersion(context.Background(), input.SkillID, input.SkillVersion)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrNotFound
	}
	if skill.Status != skills.StatusActive {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrConflict
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	for _, entry := range s.entries {
		if entry.WorkspaceID == input.WorkspaceID && entry.SkillID == input.SkillID && entry.SkillVersion == input.SkillVersion {
			return skillmarketplace.MarketplaceEntry{}, managedagents.ErrConflict
		}
	}
	s.nextEntry++
	now := time.Now().UTC()
	entry := skillmarketplace.MarketplaceEntry{
		ID: fmt.Sprintf("sment_%06d", s.nextEntry), WorkspaceID: input.WorkspaceID,
		SkillID: skill.ID, SkillVersion: version.Version, SkillIdentifier: skill.Identifier,
		SkillTitle: skill.Title, SkillDescription: skill.Description, SkillStatus: skill.Status,
		VersionChecksum: version.Checksum, PackageFormat: version.PackageFormat,
		Summary: summary, Category: category, Tags: tags, Status: skillmarketplace.MarketplaceEntryStatusDraft,
		CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedBy: input.CreatedBy, UpdatedAt: now,
	}
	s.entries[entry.ID] = entry
	return entry, nil
}

func (s *marketplaceCatalogHTTPTestStore) GetMarketplaceEntry(_ context.Context, workspaceID string, entryID string) (skillmarketplace.MarketplaceEntry, error) {
	workspaceID = defaultString(workspaceID, managedagents.DefaultWorkspaceID)
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	entry, ok := s.entries[entryID]
	if !ok || entry.WorkspaceID != workspaceID {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrNotFound
	}
	return entry, nil
}

func (s *marketplaceCatalogHTTPTestStore) ListMarketplaceEntries(_ context.Context, input skillmarketplace.ListMarketplaceEntriesInput) ([]skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	items := []skillmarketplace.MarketplaceEntry{}
	for _, entry := range s.entries {
		if entry.WorkspaceID != input.WorkspaceID || (input.Status != "" && entry.Status != input.Status) {
			continue
		}
		if !input.IncludeWithdrawn && entry.Status == skillmarketplace.MarketplaceEntryStatusWithdrawn {
			continue
		}
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *marketplaceCatalogHTTPTestStore) BrowsePublishedMarketplaceEntries(_ context.Context, input skillmarketplace.BrowseMarketplaceEntriesInput) ([]skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	query := strings.ToLower(strings.TrimSpace(input.Query))
	category := strings.ToLower(strings.TrimSpace(input.Category))
	requestedTags := map[string]bool{}
	for _, tag := range input.Tags {
		requestedTags[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	items := []skillmarketplace.MarketplaceEntry{}
	for _, entry := range s.entries {
		if entry.Status != skillmarketplace.MarketplaceEntryStatusPublished {
			continue
		}
		searchable := strings.ToLower(entry.SkillIdentifier + " " + entry.SkillTitle + " " + entry.SkillDescription + " " + entry.Summary + " " + entry.Category)
		if query != "" && !strings.Contains(searchable, query) {
			continue
		}
		if category != "" && strings.ToLower(entry.Category) != category {
			continue
		}
		if len(requestedTags) > 0 {
			matched := false
			for _, tag := range entry.Tags {
				matched = matched || requestedTags[strings.ToLower(tag)]
			}
			if !matched {
				continue
			}
		}
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if input.Limit > 0 && len(items) > input.Limit {
		items = items[:input.Limit]
	}
	return items, nil
}

func (s *marketplaceCatalogHTTPTestStore) GetPublishedMarketplaceEntry(_ context.Context, _ string, entryID string) (skillmarketplace.MarketplaceEntry, error) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	entry, ok := s.entries[entryID]
	if !ok || entry.Status != skillmarketplace.MarketplaceEntryStatusPublished || entry.SkillStatus != skills.StatusActive {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrNotFound
	}
	return entry, nil
}

func (s *marketplaceCatalogHTTPTestStore) UpdateMarketplaceEntry(_ context.Context, input skillmarketplace.UpdateMarketplaceEntryInput) (skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	summary, category, tags, err := skillmarketplace.NormalizeMarketplaceEntryMetadata(input.Summary, input.Category, input.Tags)
	if err != nil {
		return skillmarketplace.MarketplaceEntry{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	entry, ok := s.entries[input.EntryID]
	if !ok || entry.WorkspaceID != input.WorkspaceID {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrNotFound
	}
	if entry.Status != skillmarketplace.MarketplaceEntryStatusDraft {
		return entry, managedagents.ErrConflict
	}
	entry.Summary, entry.Category, entry.Tags = summary, category, tags
	entry.UpdatedBy, entry.UpdatedAt = input.UpdatedBy, time.Now().UTC()
	s.entries[entry.ID] = entry
	return entry, nil
}

func (s *marketplaceCatalogHTTPTestStore) TransitionMarketplaceEntry(_ context.Context, input skillmarketplace.TransitionMarketplaceEntryInput) (skillmarketplace.MarketplaceEntry, error) {
	input.WorkspaceID = defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	expected, ok := skillmarketplace.MarketplaceEntryTransitionSource(input.TargetStatus)
	if !ok {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrInvalid
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	entry, ok := s.entries[input.EntryID]
	if !ok || entry.WorkspaceID != input.WorkspaceID {
		return skillmarketplace.MarketplaceEntry{}, managedagents.ErrNotFound
	}
	if entry.Status == input.TargetStatus {
		return entry, nil
	}
	if entry.Status != expected {
		return entry, managedagents.ErrConflict
	}
	if input.TargetStatus == skillmarketplace.MarketplaceEntryStatusPublished {
		for _, existing := range s.entries {
			if existing.ID != entry.ID && existing.WorkspaceID == entry.WorkspaceID && existing.SkillID == entry.SkillID && existing.Status == skillmarketplace.MarketplaceEntryStatusPublished {
				return entry, managedagents.ErrConflict
			}
		}
	}
	now := time.Now().UTC()
	entry.Status, entry.UpdatedBy, entry.UpdatedAt = input.TargetStatus, input.Actor, now
	switch input.TargetStatus {
	case skillmarketplace.MarketplaceEntryStatusPendingReview:
		entry.SubmittedBy, entry.SubmittedAt = input.Actor, &now
	case skillmarketplace.MarketplaceEntryStatusPublished:
		entry.PublishedBy, entry.PublishedAt, entry.ReviewNote = input.Actor, &now, input.Note
	case skillmarketplace.MarketplaceEntryStatusWithdrawn:
		entry.WithdrawnBy, entry.WithdrawnAt, entry.WithdrawalReason = input.Actor, &now, input.Note
	}
	s.entries[entry.ID] = entry
	return entry, nil
}

func TestMarketplaceEntryHTTPLifecycle(t *testing.T) {
	store := newMarketplaceCatalogHTTPTestStore()
	skill, err := store.CreateSkill(t.Context(), skills.CreateSkillInput{
		WorkspaceID: managedagents.DefaultWorkspaceID, Identifier: "internal-review", Title: "Internal Review", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	for version := 1; version <= 2; version++ {
		if _, err := store.CreateSkillVersion(t.Context(), skills.CreateVersionInput{
			SkillID: skill.ID, ContentFormat: "markdown", ContentText: fmt.Sprintf("version %d", version), CreatedBy: "test",
		}); err != nil {
			t.Fatalf("create skill version %d: %v", version, err)
		}
	}
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, runner.DefaultMockTurnDelay, nil), nil)
	secured := &Server{mux: http.NewServeMux(), store: store, logger: slog.Default(), controlAuthToken: "control-secret"}
	secured.routes()
	unauthorized := httptest.NewRecorder()
	secured.mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/skill-marketplace-entries", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected marketplace management auth, got %d", unauthorized.Code)
	}
	created := postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost, "/v1/skill-marketplace-entries", `{
		"workspace_id":"wksp_default","skill_id":"`+skill.ID+`","skill_version":1,
		"summary":" First release ","category":" Engineering ","tags":["Go","go","Review"]
	}`, http.StatusCreated)
	if created.Status != skillmarketplace.MarketplaceEntryStatusDraft || len(created.Tags) != 2 || created.SkillVersion != 1 {
		t.Fatalf("unexpected draft: %#v", created)
	}

	updated := postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPatch,
		"/v1/skill-marketplace-entries/"+created.ID, `{"summary":"Ready for review","category":"Quality","tags":["review"]}`, http.StatusOK)
	if updated.Summary != "Ready for review" || updated.Category != "Quality" {
		t.Fatalf("unexpected draft update: %#v", updated)
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+created.ID+"/publish", `{}`, http.StatusConflict)

	submitted := postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+created.ID+"/submit", `{}`, http.StatusOK)
	if submitted.Status != skillmarketplace.MarketplaceEntryStatusPendingReview || submitted.SubmittedAt == nil {
		t.Fatalf("unexpected pending entry: %#v", submitted)
	}
	operatorPublishRequest := httptest.NewRequest(http.MethodPost,
		"/v1/skill-marketplace-entries/"+created.ID+"/publish", strings.NewReader(`{"note":"operator cannot approve"}`))
	operatorPublishRequest.Header.Set("Content-Type", "application/json")
	operatorPublishRequest = operatorPublishRequest.WithContext(context.WithValue(operatorPublishRequest.Context(), principalContextKey{}, Principal{
		Subject: "publisher", WorkspaceID: managedagents.DefaultWorkspaceID, OwnerID: "publisher", Roles: []string{RoleOperator}, AuthType: AuthModeJWT,
	}))
	operatorPublishResponse := httptest.NewRecorder()
	server.ServeHTTP(operatorPublishResponse, operatorPublishRequest)
	if operatorPublishResponse.Code != http.StatusForbidden {
		t.Fatalf("expected admin review boundary, got %d %s", operatorPublishResponse.Code, operatorPublishResponse.Body.String())
	}
	postJSONWithStatus[map[string]string](t, server, http.MethodPatch,
		"/v1/skill-marketplace-entries/"+created.ID, `{"summary":"too late"}`, http.StatusConflict)
	published := postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+created.ID+"/publish", `{"note":"approved"}`, http.StatusOK)
	if published.Status != skillmarketplace.MarketplaceEntryStatusPublished || published.ReviewNote != "approved" || published.PublishedAt == nil {
		t.Fatalf("unexpected published entry: %#v", published)
	}

	second := postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost, "/v1/skill-marketplace-entries", `{
		"workspace_id":"wksp_default","skill_id":"`+skill.ID+`","skill_version":2
	}`, http.StatusCreated)
	postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+second.ID+"/submit", `{}`, http.StatusOK)
	postJSONWithStatus[map[string]string](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+second.ID+"/publish", `{}`, http.StatusConflict)

	withdrawn := postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+created.ID+"/withdraw", `{"note":"superseded"}`, http.StatusOK)
	if withdrawn.Status != skillmarketplace.MarketplaceEntryStatusWithdrawn || withdrawn.WithdrawalReason != "superseded" {
		t.Fatalf("unexpected withdrawn entry: %#v", withdrawn)
	}
	postJSONWithStatus[skillmarketplace.MarketplaceEntry](t, server, http.MethodPost,
		"/v1/skill-marketplace-entries/"+second.ID+"/publish", `{"note":"v2 approved"}`, http.StatusOK)

	publishedList := getJSON[struct {
		Entries []skillmarketplace.MarketplaceEntry `json:"entries"`
	}](t, server, "/v1/skill-marketplace-entries?status=published")
	if len(publishedList.Entries) != 1 || publishedList.Entries[0].SkillVersion != 2 {
		t.Fatalf("unexpected published list: %#v", publishedList.Entries)
	}
	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 50})
	if err != nil || len(audits) < 10 {
		t.Fatalf("expected lifecycle success and failure audits: count=%d err=%v", len(audits), err)
	}
}
