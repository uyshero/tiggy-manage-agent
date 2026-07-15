package skillmarketplace

import (
	"reflect"
	"testing"
)

func TestMarketplaceEntryLifecycleAndMetadata(t *testing.T) {
	transitions := []struct {
		target string
		source string
	}{
		{MarketplaceEntryStatusPendingReview, MarketplaceEntryStatusDraft},
		{MarketplaceEntryStatusPublished, MarketplaceEntryStatusPendingReview},
		{MarketplaceEntryStatusWithdrawn, MarketplaceEntryStatusPublished},
	}
	for _, transition := range transitions {
		source, ok := MarketplaceEntryTransitionSource(transition.target)
		if !ok || source != transition.source {
			t.Fatalf("unexpected transition source for %q: %q %v", transition.target, source, ok)
		}
	}
	if _, ok := MarketplaceEntryTransitionSource(MarketplaceEntryStatusDraft); ok {
		t.Fatal("draft must not be a transition target")
	}
	for _, status := range []string{
		MarketplaceEntryStatusDraft, MarketplaceEntryStatusPendingReview,
		MarketplaceEntryStatusPublished, MarketplaceEntryStatusWithdrawn,
	} {
		if !ValidMarketplaceEntryStatus(status) {
			t.Fatalf("expected valid status %q", status)
		}
	}
	if ValidMarketplaceEntryStatus("deprecated") {
		t.Fatal("deprecated must not be part of the marketplace lifecycle")
	}

	summary, category, tags, err := NormalizeMarketplaceEntryMetadata(
		"  Review safely.  ", " Engineering ", []string{"Go", " go ", "Security", ""},
	)
	if err != nil {
		t.Fatalf("normalize metadata: %v", err)
	}
	if summary != "Review safely." || category != "Engineering" || !reflect.DeepEqual(tags, []string{"Go", "Security"}) {
		t.Fatalf("unexpected normalized metadata: %q %q %#v", summary, category, tags)
	}
}
