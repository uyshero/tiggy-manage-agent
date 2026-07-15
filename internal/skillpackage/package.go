// Package skillpackage materializes immutable Skill versions as standard
// SKILL.md packages in object storage.
package skillpackage

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skills"
)

const (
	FormatLegacyDB = "legacy_db"
	FormatV1       = "tma.skill-package.v1"

	SkillMarkdownPath = "SKILL.md"
	ArchivePath       = ".tma/package.zip"
)

var deterministicZIPTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

type BinaryObject struct {
	ObjectRefID string
	Bucket      string
	Key         string
	Version     string
}

type BuildInput struct {
	WorkspaceID   string
	Identifier    string
	Version       int
	SkillMarkdown string
	Assets        skills.AssetBundle
	ResolveBinary func(context.Context, string) (BinaryObject, error)
}

type File struct {
	Path           string `json:"path"`
	Role           string `json:"role"`
	ContentType    string `json:"content_type"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	ObjectRefID    string `json:"object_ref_id,omitempty"`
	ObjectKey      string `json:"object_key,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
	Executable     bool   `json:"executable,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	ScanStatus     string `json:"scan_status,omitempty"`
	ScanProvider   string `json:"scan_provider,omitempty"`
	ScanVersion    string `json:"scan_version,omitempty"`
}

type Manifest struct {
	Format          string `json:"format"`
	Root            string `json:"root"`
	PackageChecksum string `json:"package_checksum_sha256"`
	Files           []File `json:"files"`
}

type StoredObject struct {
	File              File
	Bucket            string
	Version           string
	ETag              string
	ExistingReference bool
}

type StoredPackage struct {
	Root            string
	PackageChecksum string
	Files           []StoredObject
}

type Repository struct {
	client   objectstore.Client
	bucket   string
	provider string
}

func NewRepository(client objectstore.Client, bucket string) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: skill package object store is required", objectstore.ErrInvalid)
	}
	bucket = strings.TrimSpace(bucket)
	if err := objectstore.ValidateBucketName(bucket); err != nil {
		return nil, err
	}
	provider := "s3"
	if configured, ok := client.(interface{ Config() objectstore.Config }); ok {
		provider = strings.TrimSpace(configured.Config().Provider)
		if provider == "" {
			provider = "s3"
		}
		if provider == objectstore.ProviderNoop {
			return nil, objectstore.ErrNotConfigured
		}
	}
	return &Repository{client: client, bucket: bucket, provider: provider}, nil
}

func (r *Repository) Provider() string { return r.provider }

func (r *Repository) Bucket() string { return r.bucket }

func PackageRoot(workspaceID string, identifier string, version int) (string, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(identifier) == "" || version <= 0 {
		return "", fmt.Errorf("skill package workspace, identifier, and positive version are required")
	}
	root := path.Join("skills", encodeSegment(workspaceID), encodeSegment(identifier), "versions", strconv.Itoa(version))
	if err := objectstore.ValidateObjectKey(root + "/" + SkillMarkdownPath); err != nil {
		return "", err
	}
	return root, nil
}

func (r *Repository) Store(ctx context.Context, input BuildInput) (result StoredPackage, err error) {
	root, err := PackageRoot(input.WorkspaceID, input.Identifier, input.Version)
	if err != nil {
		return StoredPackage{}, err
	}
	files, err := r.packageFiles(ctx, input)
	if err != nil {
		return StoredPackage{}, err
	}
	result.Root = root
	result.PackageChecksum = packageChecksum(files)

	uploaded := make([]objectstore.PutObjectResult, 0, len(files)+1)
	defer func() {
		if err == nil {
			return
		}
		for index := len(uploaded) - 1; index >= 0; index-- {
			item := uploaded[index]
			_ = r.client.DeleteObject(context.Background(), objectstore.DeleteObjectInput{
				Bucket: item.Bucket, Key: item.Key, Version: item.Version,
			})
		}
	}()

	result.Files = make([]StoredObject, 0, len(files)+1)
	for _, item := range files {
		if item.existing != nil {
			entry := item.file
			entry.ObjectRefID = item.existing.ObjectRefID
			result.Files = append(result.Files, StoredObject{
				File: entry, Bucket: item.existing.Bucket, Version: item.existing.Version, ExistingReference: true,
			})
			continue
		}
		key := path.Join(root, item.file.Path)
		put, putErr := r.client.PutObject(ctx, objectstore.PutObjectInput{
			Bucket: r.bucket, Key: key, Body: bytes.NewReader(item.content),
			ContentType: item.file.ContentType, SizeBytes: int64(len(item.content)),
			ChecksumSHA256: item.file.ChecksumSHA256,
			Metadata: map[string]string{
				"tma-kind": "skill-package-file", "skill-identifier": input.Identifier,
				"skill-version": strconv.Itoa(input.Version), "package-path": item.file.Path,
			},
		})
		if putErr != nil {
			return StoredPackage{}, fmt.Errorf("store skill package file %q: %w", item.file.Path, putErr)
		}
		uploaded = append(uploaded, put)
		entry := item.file
		entry.ObjectKey = fallback(put.Key, key)
		result.Files = append(result.Files, StoredObject{
			File: entry, Bucket: fallback(put.Bucket, r.bucket), Version: put.Version, ETag: put.ETag,
		})
	}

	archive, err := deterministicArchive(files)
	if err != nil {
		return StoredPackage{}, err
	}
	archiveChecksum := checksum(archive)
	archiveKey := path.Join(root, ArchivePath)
	put, err := r.client.PutObject(ctx, objectstore.PutObjectInput{
		Bucket: r.bucket, Key: archiveKey, Body: bytes.NewReader(archive),
		ContentType: "application/zip", SizeBytes: int64(len(archive)), ChecksumSHA256: archiveChecksum,
		Metadata: map[string]string{
			"tma-kind": "skill-package-archive", "skill-identifier": input.Identifier,
			"skill-version": strconv.Itoa(input.Version), "package-checksum": result.PackageChecksum,
		},
	})
	if err != nil {
		return StoredPackage{}, fmt.Errorf("store skill package archive: %w", err)
	}
	uploaded = append(uploaded, put)
	result.Files = append(result.Files, StoredObject{
		File: File{
			Path: ArchivePath, Role: "archive", ContentType: "application/zip", SizeBytes: int64(len(archive)),
			ChecksumSHA256: archiveChecksum, ObjectKey: fallback(put.Key, archiveKey),
		},
		Bucket: fallback(put.Bucket, r.bucket), Version: put.Version, ETag: put.ETag,
	})
	return result, nil
}

func (r *Repository) Read(ctx context.Context, object BinaryObject) ([]byte, error) {
	result, err := r.client.GetObject(ctx, objectstore.GetObjectInput{
		Bucket: object.Bucket, Key: object.Key, Version: object.Version,
	})
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()
	content, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (r *Repository) DeleteStored(ctx context.Context, stored StoredPackage) {
	for index := len(stored.Files) - 1; index >= 0; index-- {
		item := stored.Files[index]
		if item.ExistingReference {
			continue
		}
		_ = r.client.DeleteObject(ctx, objectstore.DeleteObjectInput{
			Bucket: item.Bucket, Key: item.File.ObjectKey, Version: item.Version,
		})
	}
}

func EncodeManifest(stored StoredPackage) (json.RawMessage, error) {
	manifest := Manifest{
		Format: FormatV1, Root: stored.Root, PackageChecksum: stored.PackageChecksum,
		Files: make([]File, 0, len(stored.Files)),
	}
	for _, object := range stored.Files {
		manifest.Files = append(manifest.Files, object.File)
	}
	encoded, err := json.Marshal(manifest)
	return json.RawMessage(encoded), err
}

type packageFile struct {
	file     File
	content  []byte
	existing *BinaryObject
}

func (r *Repository) packageFiles(ctx context.Context, input BuildInput) ([]packageFile, error) {
	files := make([]packageFile, 0, len(input.Assets.Files)+1)
	skillMD := []byte(input.SkillMarkdown)
	files = append(files, packageFile{
		file:    File{Path: SkillMarkdownPath, Role: "skill_md", ContentType: "text/markdown", SizeBytes: int64(len(skillMD)), ChecksumSHA256: checksum(skillMD)},
		content: skillMD,
	})
	assets := append([]skills.AssetFile(nil), input.Assets.Files...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	for _, asset := range assets {
		if strings.EqualFold(asset.Path, SkillMarkdownPath) || asset.Path == ArchivePath {
			return nil, fmt.Errorf("skill asset path %q is reserved", asset.Path)
		}
		entry := File{
			Path: asset.Path, Role: "asset", ContentType: fallback(asset.ContentType, "text/plain"),
			SizeBytes: int64(asset.Size), ChecksumSHA256: strings.ToLower(asset.ChecksumSHA256),
			Binary: asset.Binary, Executable: asset.Executable, SourceRevision: asset.Revision,
			SourceURL: asset.SourceURL, ScanStatus: asset.ScanStatus, ScanProvider: asset.ScanProvider,
			ScanVersion: asset.ScanVersion,
		}
		if !asset.Binary {
			content := []byte(asset.Content)
			entry.SizeBytes = int64(len(content))
			entry.ChecksumSHA256 = checksum(content)
			files = append(files, packageFile{file: entry, content: content})
			continue
		}
		if asset.ObjectRefID == "" || input.ResolveBinary == nil {
			return nil, fmt.Errorf("binary skill asset %q has no resolvable object reference", asset.Path)
		}
		object, err := input.ResolveBinary(ctx, asset.ObjectRefID)
		if err != nil {
			return nil, fmt.Errorf("resolve binary skill asset %q: %w", asset.Path, err)
		}
		content, err := r.Read(ctx, object)
		if err != nil {
			return nil, fmt.Errorf("read binary skill asset %q: %w", asset.Path, err)
		}
		if checksum(content) != entry.ChecksumSHA256 {
			return nil, fmt.Errorf("binary skill asset %q checksum mismatch", asset.Path)
		}
		entry.SizeBytes = int64(len(content))
		files = append(files, packageFile{file: entry, content: content, existing: &object})
	}
	return files, nil
}

func deterministicArchive(files []packageFile) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, item := range files {
		header := &zip.FileHeader{Name: item.file.Path, Method: zip.Deflate}
		header.Modified = deterministicZIPTime
		if item.file.Executable {
			header.SetMode(0o755)
		} else {
			header.SetMode(0o644)
		}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(item.content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func packageChecksum(files []packageFile) string {
	hash := sha256.New()
	for _, item := range files {
		writeDigestPart(hash, []byte(item.file.Path))
		writeDigestPart(hash, item.content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeDigestPart(writer io.Writer, value []byte) {
	_, _ = fmt.Fprintf(writer, "%d:", len(value))
	_, _ = writer.Write(value)
}

func checksum(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func encodeSegment(value string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	var result strings.Builder
	for _, item := range []byte(strings.TrimSpace(value)) {
		if strings.ContainsRune(alphabet, rune(item)) {
			result.WriteByte(item)
			continue
		}
		result.WriteByte('~')
		result.WriteString(strings.ToUpper(hex.EncodeToString([]byte{item})))
	}
	return result.String()
}

func fallback(value string, fallbackValue string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallbackValue
}
