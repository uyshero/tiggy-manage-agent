package managedagents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"tiggy-manage-agent/internal/objectstore"
)

// VerifiedObjectContent is an objectstore payload read through an ObjectRef and
// verified against the metadata persisted in Postgres.
type VerifiedObjectContent struct {
	ObjectRef      ObjectRef
	Content        []byte
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	ETag           string
	Metadata       map[string]string
}

// ReadVerifiedObject loads an object through its durable ObjectRef and verifies
// the payload size/checksum before returning bytes to higher-level runtime code.
// maxBytes is an additional caller limit; use <=0 when the ObjectRef size is the
// only expected bound.
func ReadVerifiedObject(ctx context.Context, client objectstore.Client, objectRef ObjectRef, maxBytes int64) (VerifiedObjectContent, error) {
	if client == nil {
		return VerifiedObjectContent{}, objectstore.ErrNotConfigured
	}
	if strings.TrimSpace(objectRef.Bucket) == "" || strings.TrimSpace(objectRef.ObjectKey) == "" {
		return VerifiedObjectContent{}, fmt.Errorf("%w: object ref %q is missing bucket or key", ErrInvalid, objectRef.ID)
	}
	if maxBytes > 0 && objectRef.SizeBytes > maxBytes {
		return VerifiedObjectContent{}, fmt.Errorf("%w: object ref %q exceeds read limit", ErrInvalid, objectRef.ID)
	}
	object, err := client.GetObject(ctx, objectstore.GetObjectInput{
		Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
	})
	if err != nil {
		return VerifiedObjectContent{}, err
	}
	defer object.Body.Close()

	readLimit := maxBytes
	if readLimit <= 0 && objectRef.SizeBytes > 0 {
		readLimit = objectRef.SizeBytes
	}
	var reader io.Reader = object.Body
	if readLimit > 0 {
		reader = io.LimitReader(object.Body, readLimit+1)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return VerifiedObjectContent{}, err
	}
	if readLimit > 0 && int64(len(content)) > readLimit {
		return VerifiedObjectContent{}, fmt.Errorf("%w: object ref %q exceeds read limit", ErrInvalid, objectRef.ID)
	}
	if objectRef.SizeBytes > 0 && int64(len(content)) != objectRef.SizeBytes {
		return VerifiedObjectContent{}, fmt.Errorf("%w: object ref %q size mismatch", ErrInvalid, objectRef.ID)
	}
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	if strings.TrimSpace(objectRef.ChecksumSHA256) != "" && !strings.EqualFold(objectRef.ChecksumSHA256, checksumHex) {
		return VerifiedObjectContent{}, fmt.Errorf("%w: object ref %q checksum mismatch", ErrInvalid, objectRef.ID)
	}
	contentType := object.ContentType
	if strings.TrimSpace(contentType) == "" {
		contentType = objectRef.ContentType
	}
	return VerifiedObjectContent{
		ObjectRef: objectRef, Content: content, ContentType: contentType,
		SizeBytes: int64(len(content)), ChecksumSHA256: checksumHex,
		ETag: object.ETag, Metadata: object.Metadata,
	}, nil
}
