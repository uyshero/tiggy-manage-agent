package objectreconcile

import (
	"context"
	"errors"
	"time"
)

const (
	FindingMissingObject    = "missing_object"
	FindingOrphanObject     = "orphan_object"
	FindingMetadataMismatch = "metadata_mismatch"
	FindingProviderError    = "provider_error"
)

var (
	ErrInvalid     = errors.New("invalid object reconciliation input")
	ErrUnsupported = errors.New("object reconciliation unsupported")
)

type Reference struct {
	ID              string
	WorkspaceID     string
	StorageProvider string
	Bucket          string
	ObjectKey       string
	ObjectVersion   string
	ContentType     string
	SizeBytes       int64
	ChecksumSHA256  string
	ETag            string
}

type ListReferencesInput struct {
	WorkspaceID     string
	StorageProvider string
	Bucket          string
	Prefix          string
	Limit           int
}

type ReferencePage struct {
	References []Reference
	Truncated  bool
}

type LookupReferencesInput struct {
	WorkspaceID     string
	StorageProvider string
	Bucket          string
	ObjectKeys      []string
}

type Store interface {
	ListObjectReconciliationReferences(context.Context, ListReferencesInput) (ReferencePage, error)
	LookupObjectReconciliationReferences(context.Context, LookupReferencesInput) ([]Reference, error)
}

type PreviewInput struct {
	WorkspaceID     string `json:"workspace_id"`
	StorageProvider string `json:"storage_provider,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	Prefix          string `json:"prefix,omitempty"`
	ProviderCursor  string `json:"provider_cursor,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type ScanSide struct {
	Scanned    int    `json:"scanned"`
	Truncated  bool   `json:"truncated"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type ScanStats struct {
	ObjectRefs      ScanSide `json:"object_refs"`
	ProviderObjects ScanSide `json:"provider_objects"`
}

type Summary struct {
	Total              int `json:"total"`
	MissingObjects     int `json:"missing_objects"`
	OrphanObjects      int `json:"orphan_objects"`
	MetadataMismatches int `json:"metadata_mismatches"`
	ProviderErrors     int `json:"provider_errors"`
}

type ObjectMetadata struct {
	Version        string `json:"version,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	ETag           string `json:"etag,omitempty"`
	LastModified   string `json:"last_modified,omitempty"`
}

type MetadataDifference struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type Finding struct {
	Kind          string               `json:"kind"`
	ObjectRefID   string               `json:"object_ref_id,omitempty"`
	ObjectKey     string               `json:"object_key,omitempty"`
	ObjectVersion string               `json:"object_version,omitempty"`
	Expected      *ObjectMetadata      `json:"expected,omitempty"`
	Actual        *ObjectMetadata      `json:"actual,omitempty"`
	Differences   []MetadataDifference `json:"differences,omitempty"`
	Message       string               `json:"message"`
	Remediation   string               `json:"remediation"`
}

type Report struct {
	DryRun          bool      `json:"dry_run"`
	WorkspaceID     string    `json:"workspace_id"`
	StorageProvider string    `json:"storage_provider"`
	Bucket          string    `json:"bucket"`
	Prefix          string    `json:"prefix"`
	GeneratedAt     time.Time `json:"generated_at"`
	Scan            ScanStats `json:"scan"`
	Summary         Summary   `json:"summary"`
	Findings        []Finding `json:"findings"`
}
