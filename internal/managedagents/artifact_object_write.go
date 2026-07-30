package managedagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectcleanup"
	"tiggy-manage-agent/internal/objectstore"
)

const objectWriteCleanupTimeout = 10 * time.Second

const (
	objectLifecycleMetadataKey = "object_lifecycle"
	objectLifecycleClassKey    = "class"
	objectLifecycleManaged     = "managed"
	objectLifecycleExternal    = "external"
)

type sessionArtifactObjectWriteStore interface {
	objectRefCreator
	objectRefDeleter
	sessionArtifactCreator
}

// PersistSessionArtifactObject coordinates object storage and database writes.
// The object is removed when metadata persistence fails, and an unlinked
// ObjectRef is removed before its body so the database never points at a
// deliberately deleted object.
type PersistSessionArtifactObjectInput struct {
	PutObject       objectstore.PutObjectInput
	ObjectRef       CreateObjectRefInput
	SessionArtifact CreateSessionArtifactInput
	// DeleteObjectOnFailure is only safe for keys owned uniquely by this write.
	DeleteObjectOnFailure bool
}

func PersistSessionArtifactObject(ctx context.Context, store sessionArtifactObjectWriteStore, client objectstore.Client, input PersistSessionArtifactObjectInput) (ObjectRef, SessionArtifact, error) {
	objectRefInput := input.ObjectRef
	lifecycleClass := objectLifecycleExternal
	if input.DeleteObjectOnFailure {
		lifecycleClass = objectLifecycleManaged
	}
	var err error
	objectRefInput.Metadata, err = mergeObjectLifecycleMetadata(objectRefInput.Metadata, lifecycleClass)
	if err != nil {
		return ObjectRef{}, SessionArtifact{}, err
	}

	put, err := client.PutObject(ctx, input.PutObject)
	if err != nil {
		return ObjectRef{}, SessionArtifact{}, err
	}

	objectRefInput.StorageProvider = fallbackObjectStorageProvider(objectRefInput.StorageProvider, client)
	objectRefInput.Bucket = fallbackObjectWriteValue(put.Bucket, input.PutObject.Bucket)
	objectRefInput.ObjectKey = fallbackObjectWriteValue(put.Key, input.PutObject.Key)
	objectRefInput.ObjectVersion = put.Version
	objectRefInput.ContentType = fallbackObjectWriteValue(objectRefInput.ContentType, input.PutObject.ContentType)
	if put.SizeBytes > 0 || objectRefInput.SizeBytes == 0 {
		objectRefInput.SizeBytes = put.SizeBytes
	}
	if objectRefInput.SizeBytes == 0 {
		objectRefInput.SizeBytes = input.PutObject.SizeBytes
	}
	objectRefInput.ChecksumSHA256 = fallbackObjectWriteValue(put.ChecksumSHA256, input.PutObject.ChecksumSHA256)
	objectRefInput.ETag = put.ETag

	objectRef, err := CreateObjectRefWithContext(ctx, store, objectRefInput)
	if err != nil {
		var cleanupErr error
		if input.DeleteObjectOnFailure {
			cleanupErr = deleteStoredObjectAfterFailure(ctx, client, put, input.PutObject)
		}
		var journalErr error
		if !input.DeleteObjectOnFailure || cleanupErr != nil {
			reason := objectcleanup.ReasonObjectRefCreateFailed
			if !input.DeleteObjectOnFailure {
				reason = objectcleanup.ReasonUnsafeCustomKey
			}
			journalErr = recordObjectCleanup(ctx, store, client, objectRefInput.WorkspaceID, "", put, input.PutObject, reason, input.DeleteObjectOnFailure, errors.Join(err, cleanupErr))
		}
		return ObjectRef{}, SessionArtifact{}, errors.Join(err, cleanupErr, journalErr)
	}

	artifactInput := input.SessionArtifact
	artifactInput.ObjectRefID = objectRef.ID
	artifact, err := CreateSessionArtifactWithContext(ctx, store, artifactInput)
	if err != nil {
		objectRefRemoved, cleanupErr := rollbackUnlinkedObjectRef(ctx, store, client, objectRef, put, input.PutObject, input.DeleteObjectOnFailure)
		var journalErr error
		if !objectRefRemoved || !input.DeleteObjectOnFailure || cleanupErr != nil {
			reason := objectcleanup.ReasonArtifactCreateFailed
			safeToDelete := objectRefRemoved && input.DeleteObjectOnFailure
			switch {
			case !objectRefRemoved:
				reason = objectcleanup.ReasonObjectRefRollbackFailed
			case !input.DeleteObjectOnFailure:
				reason = objectcleanup.ReasonUnsafeCustomKey
			}
			journalErr = recordObjectCleanup(ctx, store, client, objectRef.WorkspaceID, objectRef.ID, put, input.PutObject, reason, safeToDelete, errors.Join(err, cleanupErr))
		}
		return ObjectRef{}, SessionArtifact{}, errors.Join(err, cleanupErr, journalErr)
	}
	return objectRef, artifact, nil
}

func mergeObjectLifecycleMetadata(metadata json.RawMessage, lifecycleClass string) (json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &root); err != nil {
			return nil, fmt.Errorf("%w: object ref metadata must be a JSON object: %v", ErrInvalid, err)
		}
		if root == nil {
			root = map[string]json.RawMessage{}
		}
	}
	lifecycle := map[string]json.RawMessage{}
	if existing, ok := root[objectLifecycleMetadataKey]; ok && string(existing) != "null" {
		if err := json.Unmarshal(existing, &lifecycle); err != nil || lifecycle == nil {
			return nil, fmt.Errorf("%w: object_lifecycle metadata must be a JSON object", ErrInvalid)
		}
	}
	encodedClass, _ := json.Marshal(lifecycleClass)
	lifecycle[objectLifecycleClassKey] = encodedClass
	encodedLifecycle, err := json.Marshal(lifecycle)
	if err != nil {
		return nil, fmt.Errorf("encode object lifecycle metadata: %w", err)
	}
	root[objectLifecycleMetadataKey] = encodedLifecycle
	merged, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode object ref metadata: %w", err)
	}
	return merged, nil
}

func rollbackUnlinkedObjectRef(ctx context.Context, store sessionArtifactObjectWriteStore, client objectstore.Client, objectRef ObjectRef, put objectstore.PutObjectResult, input objectstore.PutObjectInput, deleteObject bool) (bool, error) {
	cleanupCtx, cancel := objectWriteCleanupContext(ctx)
	defer cancel()
	if err := DeleteObjectRefWithContext(cleanupCtx, store, objectRef.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return false, fmt.Errorf("rollback object ref %s: %w", objectRef.ID, err)
	}
	if !deleteObject {
		return true, nil
	}
	return true, deleteStoredObject(cleanupCtx, client, put, input)
}

func recordObjectCleanup(ctx context.Context, store any, client objectstore.Client, workspaceID string, objectRefID string, put objectstore.PutObjectResult, input objectstore.PutObjectInput, reason string, safeToDelete bool, cause error) error {
	journal, ok := store.(objectcleanup.Enqueuer)
	if !ok {
		return nil
	}
	cleanupCtx, cancel := objectWriteCleanupContext(ctx)
	defer cancel()
	_, err := journal.EnqueueObjectCleanup(cleanupCtx, objectcleanup.EnqueueInput{
		WorkspaceID:     workspaceID,
		ObjectRefID:     objectRefID,
		StorageProvider: fallbackObjectStorageProvider("", client),
		Bucket:          fallbackObjectWriteValue(put.Bucket, input.Bucket),
		ObjectKey:       fallbackObjectWriteValue(put.Key, input.Key),
		ObjectVersion:   put.Version,
		SizeBytes:       fallbackObjectWriteSize(put.SizeBytes, input.SizeBytes),
		Reason:          reason,
		SafeToDelete:    safeToDelete,
		LastError:       cause.Error(),
		CreatedAt:       time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("record object cleanup journal: %w", err)
	}
	return nil
}

func fallbackObjectWriteSize(value int64, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func deleteStoredObjectAfterFailure(ctx context.Context, client objectstore.Client, put objectstore.PutObjectResult, input objectstore.PutObjectInput) error {
	cleanupCtx, cancel := objectWriteCleanupContext(ctx)
	defer cancel()
	return deleteStoredObject(cleanupCtx, client, put, input)
}

func deleteStoredObject(ctx context.Context, client objectstore.Client, put objectstore.PutObjectResult, input objectstore.PutObjectInput) error {
	bucket := fallbackObjectWriteValue(put.Bucket, input.Bucket)
	key := fallbackObjectWriteValue(put.Key, input.Key)
	err := client.DeleteObject(ctx, objectstore.DeleteObjectInput{Bucket: bucket, Key: key, Version: put.Version})
	if err == nil || errors.Is(err, objectstore.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("rollback stored object %s/%s: %w", bucket, key, err)
}

func objectWriteCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), objectWriteCleanupTimeout)
}

func fallbackObjectStorageProvider(provider string, client objectstore.Client) string {
	if actual := objectstore.ProviderForClient(client); actual != "" {
		return actual
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	return ObjectStorageProviderS3
}

func fallbackObjectWriteValue(value string, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}
