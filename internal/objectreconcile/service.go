package objectreconcile

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/objectstore"
)

const (
	defaultPreviewLimit = 100
	maxPreviewLimit     = 500
	statConcurrency     = 8
)

type Service struct {
	store              Store
	inventory          objectstore.InventoryClient
	configuredProvider string
	configuredBucket   string
	now                func() time.Time
}

func NewService(store Store, inventory objectstore.InventoryClient, provider, bucket string) (*Service, error) {
	provider = strings.TrimSpace(provider)
	bucket = strings.TrimSpace(bucket)
	if store == nil || inventory == nil || provider == "" || provider == objectstore.ProviderNoop || bucket == "" {
		return nil, ErrUnsupported
	}
	return &Service{store: store, inventory: inventory, configuredProvider: provider, configuredBucket: bucket, now: time.Now}, nil
}

func (s *Service) Preview(ctx context.Context, input PreviewInput) (Report, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.StorageProvider = strings.TrimSpace(input.StorageProvider)
	input.Bucket = strings.TrimSpace(input.Bucket)
	input.Prefix = strings.TrimSpace(input.Prefix)
	if input.WorkspaceID == "" {
		return Report{}, fmt.Errorf("%w: workspace_id is required", ErrInvalid)
	}
	if input.StorageProvider == "" {
		input.StorageProvider = s.configuredProvider
	}
	if input.StorageProvider != s.configuredProvider {
		return Report{}, fmt.Errorf("%w: storage_provider must match configured provider %q", ErrInvalid, s.configuredProvider)
	}
	if input.Bucket == "" {
		input.Bucket = s.configuredBucket
	}
	if input.Bucket != s.configuredBucket {
		return Report{}, fmt.Errorf("%w: bucket must match configured bucket %q", ErrInvalid, s.configuredBucket)
	}
	workspacePrefix := input.WorkspaceID + "/"
	if input.Prefix == "" {
		input.Prefix = workspacePrefix
	}
	if !strings.HasPrefix(input.Prefix, workspacePrefix) {
		return Report{}, fmt.Errorf("%w: prefix must remain under %q", ErrInvalid, workspacePrefix)
	}
	if err := objectstore.ValidateObjectKey(input.Prefix); err != nil {
		return Report{}, fmt.Errorf("%w: invalid prefix: %v", ErrInvalid, err)
	}
	if input.Limit == 0 {
		input.Limit = defaultPreviewLimit
	}
	if input.Limit < 1 || input.Limit > maxPreviewLimit {
		return Report{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maxPreviewLimit)
	}

	referencePage, err := s.store.ListObjectReconciliationReferences(ctx, ListReferencesInput{
		WorkspaceID: input.WorkspaceID, StorageProvider: input.StorageProvider,
		Bucket: input.Bucket, Prefix: input.Prefix, Limit: input.Limit,
	})
	if err != nil {
		return Report{}, err
	}
	report := Report{
		DryRun: true, WorkspaceID: input.WorkspaceID, StorageProvider: input.StorageProvider,
		Bucket: input.Bucket, Prefix: input.Prefix, GeneratedAt: s.now().UTC(),
		Findings: []Finding{},
	}
	report.Scan.ObjectRefs = ScanSide{Scanned: len(referencePage.References), Truncated: referencePage.Truncated}

	providerPage, listErr := s.inventory.ListObjects(ctx, objectstore.ListObjectsInput{
		Bucket: input.Bucket, Prefix: input.Prefix, Cursor: input.ProviderCursor, Limit: input.Limit,
	})
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if listErr == nil {
		report.Scan.ProviderObjects = ScanSide{
			Scanned: len(providerPage.Objects), Truncated: providerPage.Truncated, NextCursor: providerPage.NextCursor,
		}
	} else {
		report.Findings = append(report.Findings, providerErrorFinding("list provider objects", "", "", listErr))
	}

	report.Findings = append(report.Findings, s.inspectReferences(ctx, referencePage.References)...)
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if listErr == nil && len(providerPage.Objects) > 0 {
		keys := make([]string, 0, len(providerPage.Objects))
		for _, object := range providerPage.Objects {
			keys = append(keys, object.Key)
		}
		matches, err := s.store.LookupObjectReconciliationReferences(ctx, LookupReferencesInput{
			WorkspaceID: input.WorkspaceID, StorageProvider: input.StorageProvider,
			Bucket: input.Bucket, ObjectKeys: keys,
		})
		if err != nil {
			return Report{}, err
		}
		referencedKeys := make(map[string]struct{}, len(matches))
		for _, reference := range matches {
			referencedKeys[reference.ObjectKey] = struct{}{}
		}
		for _, object := range providerPage.Objects {
			if _, ok := referencedKeys[object.Key]; ok {
				continue
			}
			actual := providerMetadata(object)
			report.Findings = append(report.Findings, Finding{
				Kind: FindingOrphanObject, ObjectKey: object.Key, ObjectVersion: object.Version, Actual: &actual,
				Message:     "provider object has no ObjectRef in this workspace",
				Remediation: "review ownership before creating an ObjectRef or scheduling cleanup",
			})
		}
	}
	report.Summary = summarize(report.Findings)
	return report, nil
}

func (s *Service) inspectReferences(ctx context.Context, references []Reference) []Finding {
	findings := make([][]Finding, len(references))
	jobs := make(chan int)
	var workers sync.WaitGroup
	workerCount := statConcurrency
	if len(references) < workerCount {
		workerCount = len(references)
	}
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				findings[index] = s.inspectReference(ctx, references[index])
			}
		}()
	}
	for index := range references {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return flattenFindings(findings)
		}
	}
	close(jobs)
	workers.Wait()
	return flattenFindings(findings)
}

func (s *Service) inspectReference(ctx context.Context, reference Reference) []Finding {
	actual, err := s.inventory.StatObject(ctx, objectstore.StatObjectInput{
		Bucket: reference.Bucket, Key: reference.ObjectKey, Version: reference.ObjectVersion,
	})
	if errors.Is(err, objectstore.ErrNotFound) {
		expected := referenceMetadata(reference)
		return []Finding{{
			Kind: FindingMissingObject, ObjectRefID: reference.ID, ObjectKey: reference.ObjectKey,
			ObjectVersion: reference.ObjectVersion, Expected: &expected,
			Message:     "ObjectRef points to an object version that the provider cannot find",
			Remediation: "restore the object or repair references after confirming affected owners",
		}}
	}
	if err != nil {
		return []Finding{providerErrorFinding("stat referenced object", reference.ID, reference.ObjectKey, err)}
	}
	differences := metadataDifferences(reference, actual)
	if len(differences) == 0 {
		return nil
	}
	expected := referenceMetadata(reference)
	provider := providerMetadata(actual)
	return []Finding{{
		Kind: FindingMetadataMismatch, ObjectRefID: reference.ID, ObjectKey: reference.ObjectKey,
		ObjectVersion: reference.ObjectVersion, Expected: &expected, Actual: &provider, Differences: differences,
		Message:     "ObjectRef metadata differs from provider metadata",
		Remediation: "verify object identity before updating ObjectRef metadata or restoring content",
	}}
}

func metadataDifferences(reference Reference, actual objectstore.ObjectInfo) []MetadataDifference {
	differences := []MetadataDifference{}
	compare := func(field, expected, provider string, requireBoth bool) {
		if requireBoth && (expected == "" || provider == "") {
			return
		}
		if expected != provider {
			differences = append(differences, MetadataDifference{Field: field, Expected: expected, Actual: provider})
		}
	}
	compare("version", reference.ObjectVersion, actual.Version, true)
	compare("content_type", reference.ContentType, actual.ContentType, true)
	compare("size_bytes", strconv.FormatInt(reference.SizeBytes, 10), strconv.FormatInt(actual.SizeBytes, 10), false)
	compare("checksum_sha256", strings.ToLower(reference.ChecksumSHA256), strings.ToLower(actual.ChecksumSHA256), true)
	compare("etag", strings.Trim(reference.ETag, `"`), strings.Trim(actual.ETag, `"`), true)
	return differences
}

func referenceMetadata(reference Reference) ObjectMetadata {
	return ObjectMetadata{Version: reference.ObjectVersion, ContentType: reference.ContentType, SizeBytes: reference.SizeBytes, ChecksumSHA256: reference.ChecksumSHA256, ETag: reference.ETag}
}

func providerMetadata(object objectstore.ObjectInfo) ObjectMetadata {
	metadata := ObjectMetadata{Version: object.Version, ContentType: object.ContentType, SizeBytes: object.SizeBytes, ChecksumSHA256: object.ChecksumSHA256, ETag: object.ETag}
	if !object.LastModified.IsZero() {
		metadata.LastModified = object.LastModified.UTC().Format(time.RFC3339Nano)
	}
	return metadata
}

func providerErrorFinding(operation, objectRefID, objectKey string, err error) Finding {
	return Finding{Kind: FindingProviderError, ObjectRefID: objectRefID, ObjectKey: objectKey,
		Message: fmt.Sprintf("%s: %v", operation, err), Remediation: "retry after checking object store availability and credentials"}
}

func flattenFindings(grouped [][]Finding) []Finding {
	flattened := []Finding{}
	for _, findings := range grouped {
		flattened = append(flattened, findings...)
	}
	return flattened
}

func summarize(findings []Finding) Summary {
	summary := Summary{Total: len(findings)}
	for _, finding := range findings {
		switch finding.Kind {
		case FindingMissingObject:
			summary.MissingObjects++
		case FindingOrphanObject:
			summary.OrphanObjects++
		case FindingMetadataMismatch:
			summary.MetadataMismatches++
		case FindingProviderError:
			summary.ProviderErrors++
		}
	}
	return summary
}
