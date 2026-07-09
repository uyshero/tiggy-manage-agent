// Package objectstore 提供 TMA 的对象存储抽象层。
//
// 二进制内容（artifact、tool output、workspace snapshot）不进 Postgres，只在这里落盘；
// 数据库只保存 object_refs / session_artifacts 等 metadata。上层 HTTP 通过 TMA API
// 代理下载，不直接暴露 bucket/key 或底层存储地址。
//
// 当前实现：
//   - localfs：本地目录模拟 S3 bucket/key，默认开发后端
//   - noop：占位实现，所有操作返回 ErrNotConfigured
//   - s3：S3 兼容后端，支持 RustFS / MinIO / AWS S3 风格 endpoint
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

var (
	ErrInvalid       = errors.New("invalid object store input")
	ErrNotConfigured = errors.New("object store client not configured")
	ErrNotFound      = errors.New("object not found")
)

const (
	ProviderS3      = "s3"      // S3 兼容后端（RustFS / MinIO / AWS S3）
	ProviderLocalFS = "localfs" // 本地目录后端，默认开发环境
	ProviderNoop    = "noop"    // 显式禁用对象存储
)

// Config 是构造 Client 所需的运行时配置，通常由 serverconfig.ObjectStore 转换而来。
type Config struct {
	Provider     string
	Endpoint     string
	Region       string
	Bucket       string
	RootDir      string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

// Client 是 S3 风格的对象存储契约。所有 provider 共享同一套 bucket/key 语义，
// 便于 artifact upload、tool result 归档和 presigned download 在 localfs / S3 间切换。
type Client interface {
	PutObject(ctx context.Context, input PutObjectInput) (PutObjectResult, error)
	GetObject(ctx context.Context, input GetObjectInput) (GetObjectResult, error)
	DeleteObject(ctx context.Context, input DeleteObjectInput) error
	PresignGetObject(ctx context.Context, input PresignGetObjectInput) (PresignedURL, error)
}

type PutObjectInput struct {
	Bucket         string
	Key            string
	Body           io.Reader
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	Metadata       map[string]string
}

type PutObjectResult struct {
	Bucket         string
	Key            string
	Version        string
	ETag           string
	SizeBytes      int64
	ChecksumSHA256 string
}

type GetObjectInput struct {
	Bucket  string
	Key     string
	Version string
}

type GetObjectResult struct {
	Bucket         string
	Key            string
	Version        string
	Body           io.ReadCloser
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	ETag           string
	Metadata       map[string]string
}

type DeleteObjectInput struct {
	Bucket  string
	Key     string
	Version string
}

type PresignGetObjectInput struct {
	Bucket  string
	Key     string
	Version string
	TTL     time.Duration
}

type PresignedURL struct {
	URL       string
	ExpiresAt time.Time
}

// NoopClient 在未配置真实后端时使用。返回 ErrNotConfigured 而不是静默丢弃，
// 让 HTTP handler 能明确回 503，而不是误以为写入成功。
type NoopClient struct {
	config Config
}

// NewClient 按 provider 构造存储客户端。空 provider 仍回落到 NoopClient，
// 生产环境应显式设置 localfs 或 s3。
func NewClient(config Config) (Client, error) {
	switch normalizeProvider(config.Provider) {
	case "", ProviderNoop:
		return NewNoopClient(config), nil
	case ProviderS3:
		return NewS3Client(config)
	case ProviderLocalFS:
		return NewLocalFSClient(config)
	default:
		return nil, fmt.Errorf("%w: unsupported object storage provider %q", ErrInvalid, config.Provider)
	}
}

func NewNoopClient(config Config) NoopClient {
	return NoopClient{config: config}
}

func (c NoopClient) Config() Config {
	return c.config
}

func (c NoopClient) PutObject(context.Context, PutObjectInput) (PutObjectResult, error) {
	return PutObjectResult{}, ErrNotConfigured
}

func (c NoopClient) GetObject(context.Context, GetObjectInput) (GetObjectResult, error) {
	return GetObjectResult{}, ErrNotConfigured
}

func (c NoopClient) DeleteObject(context.Context, DeleteObjectInput) error {
	return ErrNotConfigured
}

func (c NoopClient) PresignGetObject(context.Context, PresignGetObjectInput) (PresignedURL, error) {
	return PresignedURL{}, ErrNotConfigured
}

// normalizeProvider 统一配置别名，避免 .env 里 local/filesystem/file 写法不一致。
func normalizeProvider(provider string) string {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		return ""
	}
	if provider == "local" || provider == "filesystem" || provider == "file" {
		return ProviderLocalFS
	}
	return provider
}

// ResolveBucket 允许调用方省略 bucket，回落到服务默认值。
func ResolveBucket(bucket string, defaultBucket string) (string, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket != "" {
		return bucket, nil
	}
	defaultBucket = strings.TrimSpace(defaultBucket)
	if defaultBucket == "" {
		return "", fmt.Errorf("%w: bucket is required", ErrInvalid)
	}
	return defaultBucket, nil
}

// ValidateObjectKey 拒绝绝对路径和 ".." 段，防止 key 被解析成 root 外的路径。
func ValidateObjectKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("%w: object key is required", ErrInvalid)
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("%w: object key must be relative", ErrInvalid)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return fmt.Errorf("%w: object key must not contain parent path segments", ErrInvalid)
		}
	}
	if path.Clean("/"+key) == "/.." {
		return fmt.Errorf("%w: object key must be relative", ErrInvalid)
	}
	return nil
}

// ValidateBucketName 把 bucket 限制为单个逻辑名，不能夹带路径分隔符。
func ValidateBucketName(bucket string) error {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return fmt.Errorf("%w: bucket is required", ErrInvalid)
	}
	if strings.Contains(bucket, "/") || strings.Contains(bucket, "\\") {
		return fmt.Errorf("%w: bucket must not contain path separators", ErrInvalid)
	}
	if bucket == "." || bucket == ".." {
		return fmt.Errorf("%w: bucket is invalid", ErrInvalid)
	}
	return nil
}
