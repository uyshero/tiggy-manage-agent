package objectreconcile

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

func TestServicePreviewReportsMissingOrphanMismatchAndProviderError(t *testing.T) {
	store := &fakeStore{
		page: ReferencePage{References: []Reference{
			{ID: "obj_missing", WorkspaceID: "wksp", StorageProvider: "s3", Bucket: "artifacts", ObjectKey: "wksp/missing.bin", SizeBytes: 10},
			{ID: "obj_mismatch", WorkspaceID: "wksp", StorageProvider: "s3", Bucket: "artifacts", ObjectKey: "wksp/report.docx", ContentType: "application/docx", SizeBytes: 12, ChecksumSHA256: "old"},
			{ID: "obj_error", WorkspaceID: "wksp", StorageProvider: "s3", Bucket: "artifacts", ObjectKey: "wksp/provider-error.bin", SizeBytes: 3},
		}, Truncated: true},
		lookup: []Reference{{ID: "obj_mismatch", ObjectKey: "wksp/report.docx"}},
	}
	inventory := &fakeInventory{
		list: objectstore.ListObjectsResult{Objects: []objectstore.ObjectInfo{
			{Bucket: "artifacts", Key: "wksp/report.docx", SizeBytes: 14},
			{Bucket: "artifacts", Key: "wksp/orphan.pdf", SizeBytes: 99},
		}, Truncated: true, NextCursor: "next-provider-page"},
		stats: map[string]objectstore.ObjectInfo{
			"wksp/report.docx": {Bucket: "artifacts", Key: "wksp/report.docx", ContentType: "application/docx", SizeBytes: 14, ChecksumSHA256: "new"},
		},
		errors: map[string]error{
			"wksp/missing.bin":        objectstore.ErrNotFound,
			"wksp/provider-error.bin": errors.New("provider unavailable"),
		},
	}
	service, err := NewService(store, inventory, "s3", "artifacts")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC) }

	report, err := service.Preview(context.Background(), PreviewInput{WorkspaceID: "wksp", Limit: 2})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !report.DryRun || report.Prefix != "wksp/" || report.GeneratedAt.IsZero() {
		t.Fatalf("unexpected report identity: %+v", report)
	}
	if report.Scan.ObjectRefs.Scanned != 3 || !report.Scan.ObjectRefs.Truncated || report.Scan.ProviderObjects.NextCursor != "next-provider-page" {
		t.Fatalf("unexpected scan stats: %+v", report.Scan)
	}
	if report.Summary != (Summary{Total: 4, MissingObjects: 1, OrphanObjects: 1, MetadataMismatches: 1, ProviderErrors: 1}) {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	kinds := []string{}
	for _, finding := range report.Findings {
		kinds = append(kinds, finding.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{FindingMissingObject, FindingMetadataMismatch, FindingProviderError, FindingOrphanObject}) {
		t.Fatalf("unexpected finding order: %v", kinds)
	}
	if fields := report.Findings[1].Differences; len(fields) != 2 || fields[0].Field != "size_bytes" || fields[1].Field != "checksum_sha256" {
		t.Fatalf("unexpected metadata differences: %+v", fields)
	}
	if store.listInput.Prefix != "wksp/" || !reflect.DeepEqual(store.lookupInput.ObjectKeys, []string{"wksp/report.docx", "wksp/orphan.pdf"}) {
		t.Fatalf("unexpected store inputs: list=%+v lookup=%+v", store.listInput, store.lookupInput)
	}
}

func TestServicePreviewRejectsCrossWorkspaceAndCrossProviderScans(t *testing.T) {
	service, err := NewService(&fakeStore{}, &fakeInventory{}, "localfs", "artifacts")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	for _, input := range []PreviewInput{
		{WorkspaceID: "wksp", Prefix: "other/"},
		{WorkspaceID: "wksp", StorageProvider: "s3"},
		{WorkspaceID: "wksp", Bucket: "other"},
		{WorkspaceID: "wksp", Limit: 501},
	} {
		if _, err := service.Preview(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid input for %+v, got %v", input, err)
		}
	}
}

func TestServicePreviewKeepsListFailureAsProviderFinding(t *testing.T) {
	service, err := NewService(&fakeStore{}, &fakeInventory{listErr: errors.New("list denied")}, "s3", "artifacts")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	report, err := service.Preview(context.Background(), PreviewInput{WorkspaceID: "wksp"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if report.Summary.ProviderErrors != 1 || report.Findings[0].Kind != FindingProviderError {
		t.Fatalf("unexpected provider failure report: %+v", report)
	}
}

func TestServicePreviewReturnsRequestCancellation(t *testing.T) {
	service, err := NewService(&fakeStore{}, &fakeInventory{}, "s3", "artifacts")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Preview(ctx, PreviewInput{WorkspaceID: "wksp"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

type fakeStore struct {
	page        ReferencePage
	lookup      []Reference
	listInput   ListReferencesInput
	lookupInput LookupReferencesInput
}

func (store *fakeStore) ListObjectReconciliationReferences(_ context.Context, input ListReferencesInput) (ReferencePage, error) {
	store.listInput = input
	return store.page, nil
}

func (store *fakeStore) LookupObjectReconciliationReferences(_ context.Context, input LookupReferencesInput) ([]Reference, error) {
	store.lookupInput = input
	return store.lookup, nil
}

type fakeInventory struct {
	list    objectstore.ListObjectsResult
	listErr error
	stats   map[string]objectstore.ObjectInfo
	errors  map[string]error
}

func (inventory *fakeInventory) ListObjects(context.Context, objectstore.ListObjectsInput) (objectstore.ListObjectsResult, error) {
	return inventory.list, inventory.listErr
}

func (inventory *fakeInventory) StatObject(_ context.Context, input objectstore.StatObjectInput) (objectstore.ObjectInfo, error) {
	if err := inventory.errors[input.Key]; err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return inventory.stats[input.Key], nil
}
