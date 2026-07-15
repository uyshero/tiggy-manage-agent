package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalFSClient 用本地目录模拟 S3 bucket/key 布局，便于开发和小规模自托管。
//
// 磁盘结构：
//
//	{RootDir}/{bucket}/{key}           对象正文
//	{RootDir}/{bucket}/{key}.meta.json  sidecar 元数据（content-type、checksum、version）
type LocalFSClient struct {
	config Config
	root   string
}

// localFSMetadata 与对象正文分文件存储。正文保持原始字节流，便于大文件流式读写；
// 元数据单独 JSON 化，GetObject 时即使 meta 缺失也能返回 body。
type localFSMetadata struct {
	Bucket         string            `json:"bucket"`
	Key            string            `json:"key"`
	ContentType    string            `json:"content_type"`
	SizeBytes      int64             `json:"size_bytes"`
	ChecksumSHA256 string            `json:"checksum_sha256"`
	ETag           string            `json:"etag"`
	Version        string            `json:"version"`
	CreatedAt      time.Time         `json:"created_at"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// NewLocalFSClient 创建本地后端。RootDir 为空时落到系统临时目录；
// 相对路径会先转为绝对路径，避免进程 cwd 变化导致对象漂移。
func NewLocalFSClient(config Config) (*LocalFSClient, error) {
	root := strings.TrimSpace(config.RootDir)
	if root == "" {
		root = filepath.Join(os.TempDir(), "tma-object-store")
	}
	if !filepath.IsAbs(root) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve localfs root: %w", err)
		}
		root = absRoot
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("prepare localfs root: %w", err)
	}
	return &LocalFSClient{config: config, root: root}, nil
}

func (c *LocalFSClient) Config() Config {
	return c.config
}

// PutObject 先写临时文件再 rename，保证 crash 时不会留下半写入对象。
// checksum 在写入时计算；若调用方提供了 SizeBytes / ChecksumSHA256，会在此校验。
func (c *LocalFSClient) PutObject(ctx context.Context, input PutObjectInput) (PutObjectResult, error) {
	_ = ctx
	if err := ValidateBucketName(input.Bucket); err != nil {
		return PutObjectResult{}, err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return PutObjectResult{}, err
	}
	if input.Body == nil {
		return PutObjectResult{}, fmt.Errorf("%w: object body is required", ErrInvalid)
	}

	objectPath, metaPath, err := c.paths(input.Bucket, input.Key)
	if err != nil {
		return PutObjectResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o755); err != nil {
		return PutObjectResult{}, err
	}

	tmpObject, err := os.CreateTemp(filepath.Dir(objectPath), ".object-*")
	if err != nil {
		return PutObjectResult{}, err
	}
	tmpObjectName := tmpObject.Name()
	defer os.Remove(tmpObjectName)

	// 正文与 SHA256 一次 pass 完成，避免大对象二次读盘。
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmpObject, hash), input.Body)
	if closeErr := tmpObject.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return PutObjectResult{}, copyErr
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	if input.SizeBytes > 0 && written != input.SizeBytes {
		return PutObjectResult{}, fmt.Errorf("%w: size mismatch, expected %d got %d", ErrInvalid, input.SizeBytes, written)
	}
	if input.ChecksumSHA256 != "" && !strings.EqualFold(input.ChecksumSHA256, checksum) {
		return PutObjectResult{}, fmt.Errorf("%w: checksum mismatch", ErrInvalid)
	}
	if err := os.Rename(tmpObjectName, objectPath); err != nil {
		return PutObjectResult{}, err
	}

	metadata := localFSMetadata{
		Bucket:         input.Bucket,
		Key:            input.Key,
		ContentType:    input.ContentType,
		SizeBytes:      written,
		ChecksumSHA256: checksum,
		ETag:           checksum,
		Version:        fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
		CreatedAt:      time.Now().UTC(),
		Metadata:       cloneStringMap(input.Metadata),
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return PutObjectResult{}, err
	}
	// meta 写入失败时回滚已落盘的对象，避免 orphan body。
	tmpMeta, err := os.CreateTemp(filepath.Dir(metaPath), ".meta-*")
	if err != nil {
		return PutObjectResult{}, err
	}
	tmpMetaName := tmpMeta.Name()
	defer os.Remove(tmpMetaName)
	if _, err := tmpMeta.Write(encoded); err != nil {
		_ = tmpMeta.Close()
		_ = os.Remove(objectPath)
		return PutObjectResult{}, err
	}
	if err := tmpMeta.Close(); err != nil {
		_ = os.Remove(objectPath)
		return PutObjectResult{}, err
	}
	if err := os.Rename(tmpMetaName, metaPath); err != nil {
		_ = os.Remove(objectPath)
		return PutObjectResult{}, err
	}

	return PutObjectResult{
		Bucket:         input.Bucket,
		Key:            input.Key,
		Version:        metadata.Version,
		ETag:           metadata.ETag,
		SizeBytes:      written,
		ChecksumSHA256: checksum,
	}, nil
}

// GetObject 优先读 sidecar 元数据；meta 缺失时仍返回正文，content-type 回落为 octet-stream。
func (c *LocalFSClient) GetObject(ctx context.Context, input GetObjectInput) (GetObjectResult, error) {
	_ = ctx
	if err := ValidateBucketName(input.Bucket); err != nil {
		return GetObjectResult{}, err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return GetObjectResult{}, err
	}

	objectPath, metaPath, err := c.paths(input.Bucket, input.Key)
	if err != nil {
		return GetObjectResult{}, err
	}
	body, err := os.Open(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return GetObjectResult{}, ErrNotFound
		}
		return GetObjectResult{}, err
	}

	result := GetObjectResult{
		Bucket: input.Bucket,
		Key:    input.Key,
		Body:   body,
	}
	metadata, err := readLocalFSMetadata(metaPath)
	if err == nil {
		result.Version = metadata.Version
		result.ContentType = metadata.ContentType
		result.SizeBytes = metadata.SizeBytes
		result.ChecksumSHA256 = metadata.ChecksumSHA256
		result.ETag = metadata.ETag
		result.Metadata = cloneStringMap(metadata.Metadata)
	} else if !os.IsNotExist(err) {
		_ = body.Close()
		return GetObjectResult{}, err
	}
	if result.ContentType == "" {
		result.ContentType = "application/octet-stream"
	}
	return result, nil
}

func (c *LocalFSClient) DeleteObject(ctx context.Context, input DeleteObjectInput) error {
	_ = ctx
	if err := ValidateBucketName(input.Bucket); err != nil {
		return err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return err
	}

	objectPath, metaPath, err := c.paths(input.Bucket, input.Key)
	if err != nil {
		return err
	}
	removed := false
	if err := os.Remove(objectPath); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(metaPath); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if !removed {
		return ErrNotFound
	}
	return nil
}

// PresignGetObject 在 localfs 下返回 file:// 绝对路径，仅用于开发/本机调试。
// 生产 S3 实现应换成带签名的 HTTPS URL；ExpiresAt 在此只是占位，localfs 不做鉴权。
func (c *LocalFSClient) PresignGetObject(ctx context.Context, input PresignGetObjectInput) (PresignedURL, error) {
	_ = ctx
	if err := ValidateBucketName(input.Bucket); err != nil {
		return PresignedURL{}, err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return PresignedURL{}, err
	}
	if input.TTL <= 0 {
		input.TTL = 15 * time.Minute
	}
	objectPath, _, err := c.paths(input.Bucket, input.Key)
	if err != nil {
		return PresignedURL{}, err
	}
	absPath, err := filepath.Abs(objectPath)
	if err != nil {
		return PresignedURL{}, err
	}
	return PresignedURL{
		URL:       "file://" + filepath.ToSlash(absPath),
		ExpiresAt: time.Now().UTC().Add(input.TTL),
	}, nil
}

// paths 把 S3 风格 bucket/key 映射到本地路径，并用 filepath.Rel 二次校验，
// 防止 Clean/Join 后的路径逃逸 bucket 根目录。
func (c *LocalFSClient) paths(bucket string, key string) (string, string, error) {
	if err := ValidateBucketName(bucket); err != nil {
		return "", "", err
	}
	if err := ValidateObjectKey(key); err != nil {
		return "", "", err
	}
	bucketRoot := filepath.Join(c.root, bucket)
	objectPath := filepath.Join(bucketRoot, filepath.FromSlash(key))
	rel, err := filepath.Rel(bucketRoot, objectPath)
	if err != nil {
		return "", "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: object path escapes root", ErrInvalid)
	}
	return objectPath, objectPath + ".meta.json", nil
}

func readLocalFSMetadata(path string) (localFSMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return localFSMetadata{}, err
	}
	var metadata localFSMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return localFSMetadata{}, err
	}
	return metadata, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	clone := make(map[string]string, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}
