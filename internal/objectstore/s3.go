package objectstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	s3Service             = "s3"
	s3ContentTypeFallback = "application/octet-stream"
	s3MaxErrorBodyBytes   = 4096
)

// S3Client 是不依赖 AWS SDK 的最小 S3-compatible 客户端。
// 当前只覆盖 TMA 需要的对象 PUT/GET/DELETE/presign 能力。
type S3Client struct {
	config     Config
	endpoint   *url.URL
	region     string
	httpClient *http.Client
	now        func() time.Time
}

func NewS3Client(config Config) (*S3Client, error) {
	endpointText := strings.TrimSpace(config.Endpoint)
	if endpointText == "" {
		return nil, fmt.Errorf("%w: s3 endpoint is required", ErrInvalid)
	}
	endpoint, err := url.Parse(endpointText)
	if err != nil {
		return nil, fmt.Errorf("%w: parse s3 endpoint: %v", ErrInvalid, err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("%w: s3 endpoint must include scheme and host", ErrInvalid)
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}
	if strings.TrimSpace(config.AccessKey) == "" {
		return nil, fmt.Errorf("%w: s3 access key is required", ErrInvalid)
	}
	if strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("%w: s3 secret key is required", ErrInvalid)
	}
	return &S3Client{
		config:     config,
		endpoint:   endpoint,
		region:     region,
		httpClient: http.DefaultClient,
		now:        time.Now,
	}, nil
}

func (c *S3Client) Config() Config {
	return c.config
}

func (c *S3Client) PutObject(ctx context.Context, input PutObjectInput) (PutObjectResult, error) {
	if err := ValidateBucketName(input.Bucket); err != nil {
		return PutObjectResult{}, err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return PutObjectResult{}, err
	}
	if input.Body == nil {
		return PutObjectResult{}, fmt.Errorf("%w: object body is required", ErrInvalid)
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return PutObjectResult{}, err
	}
	checksumBytes := sha256.Sum256(body)
	checksum := hex.EncodeToString(checksumBytes[:])
	if input.SizeBytes > 0 && int64(len(body)) != input.SizeBytes {
		return PutObjectResult{}, fmt.Errorf("%w: size mismatch, expected %d got %d", ErrInvalid, input.SizeBytes, len(body))
	}
	if input.ChecksumSHA256 != "" && !strings.EqualFold(input.ChecksumSHA256, checksum) {
		return PutObjectResult{}, fmt.Errorf("%w: checksum mismatch", ErrInvalid)
	}

	objectURL := c.objectURL(input.Bucket, input.Key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, objectURL.String(), bytes.NewReader(body))
	if err != nil {
		return PutObjectResult{}, err
	}
	req.ContentLength = int64(len(body))
	if input.ContentType != "" {
		req.Header.Set("Content-Type", input.ContentType)
	}
	for key, value := range input.Metadata {
		key = strings.TrimSpace(strings.ToLower(key))
		if key == "" {
			continue
		}
		req.Header.Set("x-amz-meta-"+key, value)
	}
	c.signRequest(req, checksum, c.now().UTC())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return PutObjectResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return PutObjectResult{}, s3StatusError(resp)
	}

	return PutObjectResult{
		Bucket:         input.Bucket,
		Key:            input.Key,
		Version:        resp.Header.Get("x-amz-version-id"),
		ETag:           trimS3ETag(resp.Header.Get("ETag")),
		SizeBytes:      int64(len(body)),
		ChecksumSHA256: checksum,
	}, nil
}

func (c *S3Client) GetObject(ctx context.Context, input GetObjectInput) (GetObjectResult, error) {
	if err := ValidateBucketName(input.Bucket); err != nil {
		return GetObjectResult{}, err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return GetObjectResult{}, err
	}

	objectURL := c.objectURL(input.Bucket, input.Key)
	query := objectURL.Query()
	if input.Version != "" {
		query.Set("versionId", input.Version)
	}
	objectURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURL.String(), nil)
	if err != nil {
		return GetObjectResult{}, err
	}
	c.signRequest(req, emptyPayloadSHA256, c.now().UTC())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return GetObjectResult{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return GetObjectResult{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return GetObjectResult{}, s3StatusError(resp)
	}

	sizeBytes := resp.ContentLength
	if sizeBytes < 0 {
		sizeBytes = 0
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = s3ContentTypeFallback
	}
	return GetObjectResult{
		Bucket:      input.Bucket,
		Key:         input.Key,
		Version:     firstNonEmpty(resp.Header.Get("x-amz-version-id"), input.Version),
		Body:        resp.Body,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		ETag:        trimS3ETag(resp.Header.Get("ETag")),
		Metadata:    s3MetadataFromHeaders(resp.Header),
	}, nil
}

func (c *S3Client) DeleteObject(ctx context.Context, input DeleteObjectInput) error {
	if err := ValidateBucketName(input.Bucket); err != nil {
		return err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return err
	}

	objectURL := c.objectURL(input.Bucket, input.Key)
	query := objectURL.Query()
	if input.Version != "" {
		query.Set("versionId", input.Version)
	}
	objectURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, objectURL.String(), nil)
	if err != nil {
		return err
	}
	c.signRequest(req, emptyPayloadSHA256, c.now().UTC())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return s3StatusError(resp)
	}
	return nil
}

func (c *S3Client) PresignGetObject(ctx context.Context, input PresignGetObjectInput) (PresignedURL, error) {
	_ = ctx
	if err := ValidateBucketName(input.Bucket); err != nil {
		return PresignedURL{}, err
	}
	if err := ValidateObjectKey(input.Key); err != nil {
		return PresignedURL{}, err
	}
	ttl := input.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 7*24*time.Hour {
		return PresignedURL{}, fmt.Errorf("%w: presigned url ttl must be <= 7 days", ErrInvalid)
	}

	now := c.now().UTC()
	objectURL := c.objectURL(input.Bucket, input.Key)
	query := objectURL.Query()
	if input.Version != "" {
		query.Set("versionId", input.Version)
	}
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", c.credentialScope(now))
	query.Set("X-Amz-Date", amzDate(now))
	query.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	query.Set("X-Amz-SignedHeaders", "host")
	objectURL.RawQuery = canonicalQueryString(query)

	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		canonicalURI(objectURL),
		canonicalQueryString(query),
		"host:" + objectURL.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	signature := c.signature(canonicalRequest, now)
	query.Set("X-Amz-Signature", signature)
	objectURL.RawQuery = canonicalQueryString(query)

	return PresignedURL{
		URL:       objectURL.String(),
		ExpiresAt: now.Add(ttl),
	}, nil
}

func (c *S3Client) objectURL(bucket string, key string) url.URL {
	objectURL := *c.endpoint
	basePath := strings.TrimRight(objectURL.Path, "/")
	baseRawPath := strings.TrimRight(objectURL.EscapedPath(), "/")
	escapedKey := escapeS3Key(key)
	if c.config.UsePathStyle {
		objectURL.Path = joinS3Path(basePath, bucket, key)
		objectURL.RawPath = joinS3Path(baseRawPath, url.PathEscape(bucket), escapedKey)
		return objectURL
	}
	objectURL.Host = bucket + "." + objectURL.Host
	objectURL.Path = joinS3Path(basePath, key)
	objectURL.RawPath = joinS3Path(baseRawPath, escapedKey)
	return objectURL
}

func (c *S3Client) signRequest(req *http.Request, payloadSHA256 string, now time.Time) {
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("x-amz-content-sha256", payloadSHA256)
	req.Header.Set("x-amz-date", amzDate(now))

	signedHeaders, canonicalHeaders := canonicalSignedHeaders(req.Header, req.URL.Host)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(*req.URL),
		canonicalQueryString(req.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadSHA256,
	}, "\n")
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s, SignedHeaders=%s, Signature=%s",
		c.credentialScope(now),
		signedHeaders,
		c.signature(canonicalRequest, now),
	))
}

func (c *S3Client) credentialScope(now time.Time) string {
	return fmt.Sprintf("%s/%s/%s/%s/aws4_request", c.config.AccessKey, dateStamp(now), c.region, s3Service)
}

func (c *S3Client) signature(canonicalRequest string, now time.Time) string {
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate(now),
		fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp(now), c.region, s3Service),
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	signingKey := s3SigningKey(c.config.SecretKey, dateStamp(now), c.region)
	return hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
}

const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func s3SigningKey(secret string, date string, region string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), date)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, s3Service)
	return hmacSHA256(serviceKey, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func canonicalSignedHeaders(headers http.Header, host string) (string, string) {
	values := map[string]string{"host": host}
	for name, headerValues := range headers {
		lowerName := strings.ToLower(strings.TrimSpace(name))
		if lowerName == "authorization" || lowerName == "user-agent" || lowerName == "" {
			continue
		}
		trimmed := make([]string, 0, len(headerValues))
		for _, value := range headerValues {
			trimmed = append(trimmed, strings.Join(strings.Fields(value), " "))
		}
		values[lowerName] = strings.Join(trimmed, ",")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteByte(':')
		canonical.WriteString(values[name])
		canonical.WriteByte('\n')
	}
	return strings.Join(names, ";"), canonical.String()
}

func canonicalURI(u url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

func canonicalQueryString(values url.Values) string {
	type pair struct {
		key   string
		value string
	}
	pairs := make([]pair, 0)
	for key, vals := range values {
		for _, value := range vals {
			pairs = append(pairs, pair{key: key, value: value})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	encoded := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		encoded = append(encoded, awsQueryEscape(pair.key)+"="+awsQueryEscape(pair.value))
	}
	return strings.Join(encoded, "&")
}

func awsQueryEscape(value string) string {
	escaped := url.QueryEscape(value)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

func escapeS3Key(key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func joinS3Path(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "/"
	}
	return "/" + strings.Join(clean, "/")
}

func amzDate(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func dateStamp(t time.Time) string {
	return t.UTC().Format("20060102")
}

func trimS3ETag(value string) string {
	return strings.Trim(value, `"`)
}

func s3MetadataFromHeaders(headers http.Header) map[string]string {
	metadata := map[string]string{}
	for key, values := range headers {
		lower := strings.ToLower(key)
		if !strings.HasPrefix(lower, "x-amz-meta-") {
			continue
		}
		metadata[strings.TrimPrefix(lower, "x-amz-meta-")] = strings.Join(values, ",")
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func s3StatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, s3MaxErrorBodyBytes))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("%w: s3 returned %s: %s", ErrInvalid, resp.Status, message)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
