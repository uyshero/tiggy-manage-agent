package skillmarketplace

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	CatalogProvider                     = "catalog"
	MarketplaceEntryStatusDraft         = "draft"
	MarketplaceEntryStatusPendingReview = "pending_review"
	MarketplaceEntryStatusPublished     = "published"
	MarketplaceEntryStatusWithdrawn     = "withdrawn"
)

type MarketplaceEntry struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	SkillID          string     `json:"skill_id"`
	SkillVersion     int        `json:"skill_version"`
	SkillIdentifier  string     `json:"skill_identifier"`
	SkillTitle       string     `json:"skill_title"`
	SkillDescription string     `json:"skill_description,omitempty"`
	SkillStatus      string     `json:"skill_status"`
	VersionChecksum  string     `json:"version_checksum_sha256"`
	PackageFormat    string     `json:"package_format"`
	Summary          string     `json:"summary,omitempty"`
	Category         string     `json:"category,omitempty"`
	Tags             []string   `json:"tags"`
	Status           string     `json:"status"`
	SubmittedBy      string     `json:"submitted_by,omitempty"`
	SubmittedAt      *time.Time `json:"submitted_at,omitempty"`
	PublishedBy      string     `json:"published_by,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	WithdrawnBy      string     `json:"withdrawn_by,omitempty"`
	WithdrawnAt      *time.Time `json:"withdrawn_at,omitempty"`
	ReviewNote       string     `json:"review_note,omitempty"`
	WithdrawalReason string     `json:"withdrawal_reason,omitempty"`
	CreatedBy        string     `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedBy        string     `json:"updated_by"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CreateMarketplaceEntryInput struct {
	WorkspaceID  string
	SkillID      string
	SkillVersion int
	Summary      string
	Category     string
	Tags         []string
	CreatedBy    string
}

type UpdateMarketplaceEntryInput struct {
	WorkspaceID string
	EntryID     string
	Summary     string
	Category    string
	Tags        []string
	UpdatedBy   string
}

type ListMarketplaceEntriesInput struct {
	WorkspaceID      string
	Status           string
	IncludeWithdrawn bool
}

type BrowseMarketplaceEntriesInput struct {
	WorkspaceID string
	Query       string
	Category    string
	Tags        []string
	Limit       int
}

type TransitionMarketplaceEntryInput struct {
	WorkspaceID  string
	EntryID      string
	TargetStatus string
	Actor        string
	Note         string
}

type MarketplaceCatalogStore interface {
	CreateMarketplaceEntry(context.Context, CreateMarketplaceEntryInput) (MarketplaceEntry, error)
	GetMarketplaceEntry(context.Context, string, string) (MarketplaceEntry, error)
	ListMarketplaceEntries(context.Context, ListMarketplaceEntriesInput) ([]MarketplaceEntry, error)
	BrowsePublishedMarketplaceEntries(context.Context, BrowseMarketplaceEntriesInput) ([]MarketplaceEntry, error)
	GetPublishedMarketplaceEntry(context.Context, string, string) (MarketplaceEntry, error)
	UpdateMarketplaceEntry(context.Context, UpdateMarketplaceEntryInput) (MarketplaceEntry, error)
	TransitionMarketplaceEntry(context.Context, TransitionMarketplaceEntryInput) (MarketplaceEntry, error)
}

func ValidMarketplaceEntryStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case MarketplaceEntryStatusDraft, MarketplaceEntryStatusPendingReview, MarketplaceEntryStatusPublished, MarketplaceEntryStatusWithdrawn:
		return true
	default:
		return false
	}
}

func MarketplaceEntryTransitionSource(target string) (string, bool) {
	switch strings.TrimSpace(target) {
	case MarketplaceEntryStatusPendingReview:
		return MarketplaceEntryStatusDraft, true
	case MarketplaceEntryStatusPublished:
		return MarketplaceEntryStatusPendingReview, true
	case MarketplaceEntryStatusWithdrawn:
		return MarketplaceEntryStatusPublished, true
	default:
		return "", false
	}
}

func NormalizeMarketplaceEntryMetadata(summary string, category string, tags []string) (string, string, []string, error) {
	summary = strings.TrimSpace(summary)
	category = strings.TrimSpace(category)
	if len(summary) > 2000 {
		return "", "", nil, fmt.Errorf("summary must not exceed 2000 characters")
	}
	if len(category) > 80 {
		return "", "", nil, fmt.Errorf("category must not exceed 80 characters")
	}
	if len(tags) > 12 {
		return "", "", nil, fmt.Errorf("tags must not contain more than 12 values")
	}
	normalizedTags := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if len(tag) > 40 {
			return "", "", nil, fmt.Errorf("tag must not exceed 40 characters")
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalizedTags = append(normalizedTags, tag)
	}
	return summary, category, normalizedTags, nil
}
