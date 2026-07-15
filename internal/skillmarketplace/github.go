package skillmarketplace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	GitHubProvider           = "github"
	defaultGitHubAPIBaseURL  = "https://api.github.com"
	defaultGitHubTimeout     = 15 * time.Second
	maxGitHubResponseBytes   = 2 << 20
	maxRemoteSkillBytes      = 100000
	maxRemoteBinaryFileBytes = 512 << 10
	maxRemoteAssetFiles      = 32
	maxRemoteAssetDepth      = 4
	maxRemoteAssetBytes      = 4 << 20
	maxGitHubAttempts        = 3
	maxGitHubRetryDelay      = 2 * time.Second
)

var (
	githubRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	markdownLinkPattern     = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	inlineCodePattern       = regexp.MustCompile("`([^`\\n]+)`")
	plainDocumentPattern    = regexp.MustCompile(`\b[A-Z][A-Z0-9_-]*\.(?:md|txt)\b`)
)

type Source struct {
	Provider       string `json:"provider"`
	Repository     string `json:"repository,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Path           string `json:"path,omitempty"`
	ArtifactID     string `json:"artifact_id,omitempty"`
	CatalogEntryID string `json:"catalog_entry_id,omitempty"`
	CatalogSkillID string `json:"catalog_skill_id,omitempty"`
}

type DiscoverInput struct {
	Query      string
	Repository string
	Limit      int
}

type Candidate struct {
	Provider            string   `json:"provider"`
	Repository          string   `json:"repository"`
	Path                string   `json:"path,omitempty"`
	Ref                 string   `json:"ref,omitempty"`
	HTMLURL             string   `json:"html_url"`
	CatalogEntryID      string   `json:"catalog_entry_id,omitempty"`
	CatalogSkillID      string   `json:"catalog_skill_id,omitempty"`
	SkillVersion        int      `json:"skill_version,omitempty"`
	Title               string   `json:"title,omitempty"`
	Category            string   `json:"category,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	VersionChecksum     string   `json:"version_checksum_sha256,omitempty"`
	Description         string   `json:"description,omitempty"`
	Stars               int      `json:"stars,omitempty"`
	SuggestedIdentifier string   `json:"suggested_identifier,omitempty"`
	Verified            bool     `json:"verified"`
}

type DiscoverResult struct {
	Provider   string      `json:"provider"`
	SearchMode string      `json:"search_mode"`
	Items      []Candidate `json:"items"`
	Count      int         `json:"count"`
}

type Package struct {
	Source          Source          `json:"source"`
	Name            string          `json:"name,omitempty"`
	Description     string          `json:"description,omitempty"`
	License         string          `json:"license,omitempty"`
	Content         string          `json:"content"`
	Manifest        json.RawMessage `json:"manifest,omitempty"`
	Revision        string          `json:"revision"`
	HTMLURL         string          `json:"html_url"`
	Files           []PackageFile   `json:"files,omitempty"`
	TotalAssetBytes int             `json:"total_asset_bytes,omitempty"`
	Warnings        []string        `json:"warnings,omitempty"`
}

type PackageFile struct {
	Path           string `json:"path"`
	Content        string `json:"content,omitempty"`
	ContentBase64  string `json:"content_base64,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	Size           int    `json:"size"`
	Revision       string `json:"revision"`
	SourceURL      string `json:"source_url"`
	Executable     bool   `json:"executable,omitempty"`
	Binary         bool   `json:"binary,omitempty"`
}

type Client interface {
	Discover(context.Context, DiscoverInput) (DiscoverResult, error)
	Fetch(context.Context, Source) (Package, error)
}

type GitHubClient struct {
	HTTPClient *http.Client
	BaseURL    string
	Token      string
}

func NewGitHubClient(token string) *GitHubClient {
	return &GitHubClient{
		HTTPClient: &http.Client{
			Timeout: defaultGitHubTimeout,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if !strings.EqualFold(request.URL.Hostname(), "api.github.com") {
					return errors.New("github API redirect target is not allowed")
				}
				return nil
			},
		},
		BaseURL: defaultGitHubAPIBaseURL,
		Token:   strings.TrimSpace(token),
	}
}

func (c *GitHubClient) Discover(ctx context.Context, input DiscoverInput) (DiscoverResult, error) {
	input.Query = strings.TrimSpace(input.Query)
	input.Repository = strings.TrimSpace(input.Repository)
	if input.Limit <= 0 {
		input.Limit = 10
	}
	if input.Limit > 20 {
		return DiscoverResult{}, errors.New("github discovery limit cannot exceed 20")
	}
	if input.Repository != "" {
		pkg, err := c.Fetch(ctx, Source{Provider: GitHubProvider, Repository: input.Repository, Path: "SKILL.md"})
		if err != nil {
			return DiscoverResult{}, err
		}
		item := Candidate{
			Provider: GitHubProvider, Repository: pkg.Source.Repository, Path: pkg.Source.Path,
			Ref: pkg.Source.Ref, HTMLURL: pkg.HTMLURL, Description: pkg.Description,
			SuggestedIdentifier: SuggestedIdentifier(pkg.Name, pkg.Source.Repository), Verified: true,
		}
		return DiscoverResult{Provider: GitHubProvider, SearchMode: "repository", Items: []Candidate{item}, Count: 1}, nil
	}
	if input.Query == "" {
		return DiscoverResult{}, errors.New("github discovery query or repository is required")
	}
	if strings.TrimSpace(c.Token) != "" {
		result, err := c.discoverCode(ctx, input.Query, input.Limit)
		if err == nil {
			return result, nil
		}
		var statusErr *githubStatusError
		if !errors.As(err, &statusErr) || (statusErr.StatusCode != http.StatusUnauthorized && statusErr.StatusCode != http.StatusForbidden && statusErr.StatusCode != http.StatusUnprocessableEntity) {
			return DiscoverResult{}, err
		}
	}
	return c.discoverRepositories(ctx, input.Query, input.Limit)
}

func (c *GitHubClient) Fetch(ctx context.Context, source Source) (Package, error) {
	source.Provider = strings.ToLower(strings.TrimSpace(source.Provider))
	if source.Provider == "" {
		source.Provider = GitHubProvider
	}
	if source.Provider != GitHubProvider {
		return Package{}, fmt.Errorf("unsupported skill source provider %q", source.Provider)
	}
	source.Repository = strings.TrimSpace(source.Repository)
	if !githubRepositoryPattern.MatchString(source.Repository) {
		return Package{}, errors.New("github repository must use owner/repo format")
	}
	cleanPath, err := normalizeSkillPath(source.Path)
	if err != nil {
		return Package{}, err
	}
	source.Path = cleanPath
	source.Ref = strings.TrimSpace(source.Ref)

	root, err := c.fetchFile(ctx, source, source.Path)
	if err != nil {
		return Package{}, err
	}
	if len(root.Content) == 0 || len(root.Content) > maxRemoteSkillBytes {
		return Package{}, fmt.Errorf("github skill content must contain 1 to %d bytes", maxRemoteSkillBytes)
	}
	name, description, license := parseFrontMatter([]byte(root.Content))
	manifest, err := parsePackageManifest([]byte(root.Content))
	if err != nil {
		return Package{}, err
	}
	files, totalBytes, warnings, err := c.fetchReferencedFiles(ctx, source, root)
	if err != nil {
		return Package{}, err
	}
	return Package{
		Source: source, Name: name, Description: description, License: license, Content: root.Content, Manifest: manifest,
		Revision: root.Revision, HTMLURL: root.SourceURL, Files: files,
		TotalAssetBytes: totalBytes, Warnings: warnings,
	}, nil
}

type fetchedFile struct {
	RepositoryPath string
	Content        string
	Bytes          []byte
	Revision       string
	SourceURL      string
}

func (c *GitHubClient) fetchFile(ctx context.Context, source Source, repositoryPath string) (fetchedFile, error) {
	cleanPath, err := normalizeRepositoryFilePath(repositoryPath)
	if err != nil {
		return fetchedFile{}, err
	}
	file, err := c.fetchFileExact(ctx, source, cleanPath)
	if err == nil {
		return file, nil
	}
	var statusErr *githubStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		return fetchedFile{}, err
	}
	correctedPath, resolveErr := c.resolveCaseInsensitivePath(ctx, source, cleanPath)
	if resolveErr != nil {
		return fetchedFile{}, err
	}
	return c.fetchFileExact(ctx, source, correctedPath)
}

func (c *GitHubClient) fetchFileExact(ctx context.Context, source Source, cleanPath string) (fetchedFile, error) {
	endpoint := "/repos/" + escapePath(source.Repository) + "/contents/" + escapePath(cleanPath)
	query := url.Values{}
	if source.Ref != "" {
		query.Set("ref", source.Ref)
	}
	var response struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		SHA      string `json:"sha"`
		HTMLURL  string `json:"html_url"`
	}
	if err := c.getJSON(ctx, endpoint, query, &response); err != nil {
		return fetchedFile{}, err
	}
	if response.Type != "file" || response.Encoding != "base64" {
		return fetchedFile{}, errors.New("github skill source must resolve to a base64 encoded file")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil {
		return fetchedFile{}, fmt.Errorf("decode github skill content: %w", err)
	}
	if len(decoded) == 0 || len(decoded) > maxRemoteBinaryFileBytes {
		return fetchedFile{}, fmt.Errorf("github package file %q must contain 1 to %d bytes", cleanPath, maxRemoteBinaryFileBytes)
	}
	return fetchedFile{
		RepositoryPath: cleanPath, Content: string(decoded), Bytes: append([]byte(nil), decoded...), Revision: strings.TrimSpace(response.SHA),
		SourceURL: strings.TrimSpace(response.HTMLURL),
	}, nil
}

func (c *GitHubClient) resolveCaseInsensitivePath(ctx context.Context, source Source, requestedPath string) (string, error) {
	directory := path.Dir(requestedPath)
	if directory == "." {
		directory = ""
	}
	endpoint := "/repos/" + escapePath(source.Repository) + "/contents"
	if directory != "" {
		endpoint += "/" + escapePath(directory)
	}
	query := url.Values{}
	if source.Ref != "" {
		query.Set("ref", source.Ref)
	}
	var entries []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if err := c.getJSON(ctx, endpoint, query, &entries); err != nil {
		return "", err
	}
	requestedName := path.Base(requestedPath)
	matches := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.Type == "file" && strings.EqualFold(entry.Name, requestedName) {
			matches = append(matches, entry.Path)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("github package file %q was not found with a unique case-insensitive match", requestedPath)
	}
	return normalizeRepositoryFilePath(matches[0])
}

func (c *GitHubClient) fetchReferencedFiles(ctx context.Context, source Source, root fetchedFile) ([]PackageFile, int, []string, error) {
	packageRoot := path.Dir(source.Path)
	if packageRoot == "." {
		packageRoot = ""
	}
	type queuedReference struct {
		From string
		Path string
	}
	queue := make([]queuedReference, 0)
	for _, reference := range referencedPaths(root.Content) {
		queue = append(queue, queuedReference{From: root.RepositoryPath, Path: reference})
	}
	visited := map[string]bool{root.RepositoryPath: true}
	files := make([]PackageFile, 0)
	warnings := make([]string, 0)
	totalBytes := 0
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		resolved, warning := resolvePackageReference(packageRoot, next.From, next.Path)
		if warning != "" {
			warnings = appendUnique(warnings, warning)
			continue
		}
		if visited[resolved] {
			continue
		}
		visited[resolved] = true
		if len(files) >= maxRemoteAssetFiles {
			return nil, 0, warnings, fmt.Errorf("github skill package exceeds %d referenced files", maxRemoteAssetFiles)
		}
		fetchPath := resolved
		if path.Base(resolved) != strings.ToLower(path.Base(resolved)) {
			if correctedPath, resolveErr := c.resolveCaseInsensitivePath(ctx, source, resolved); resolveErr == nil {
				fetchPath = correctedPath
			}
		}
		file, err := c.fetchFile(ctx, source, fetchPath)
		if err != nil {
			var statusErr *githubStatusError
			if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
				warnings = appendUnique(warnings, fmt.Sprintf("skipped missing package reference %q", next.Path))
				continue
			}
			return nil, 0, warnings, fmt.Errorf("fetch referenced package file %q: %w", resolved, err)
		}
		visited[file.RepositoryPath] = true
		resolved = file.RepositoryPath
		binaryAsset := isAllowedBinaryAssetExtension(path.Ext(relativePackagePath(packageRoot, resolved)))
		if !binaryAsset && len(file.Bytes) > maxRemoteSkillBytes {
			return nil, 0, warnings, fmt.Errorf("github text package file %q exceeds %d bytes", resolved, maxRemoteSkillBytes)
		}
		if !binaryAsset && !utf8.Valid(file.Bytes) {
			return nil, 0, warnings, fmt.Errorf("github text package file %q is not valid UTF-8", resolved)
		}
		totalBytes += len(file.Bytes)
		if totalBytes > maxRemoteAssetBytes {
			return nil, 0, warnings, fmt.Errorf("github skill package assets exceed %d bytes", maxRemoteAssetBytes)
		}
		relativePath := relativePackagePath(packageRoot, resolved)
		packageFile := PackageFile{
			Path: relativePath, Size: len(file.Bytes), Revision: file.Revision,
			SourceURL: file.SourceURL, Executable: isScriptExtension(path.Ext(relativePath)), Binary: binaryAsset,
		}
		checksum := sha256.Sum256(file.Bytes)
		packageFile.ChecksumSHA256 = hex.EncodeToString(checksum[:])
		if binaryAsset {
			packageFile.ContentBase64 = base64.StdEncoding.EncodeToString(file.Bytes)
			packageFile.ContentType = http.DetectContentType(file.Bytes)
		} else {
			packageFile.Content = file.Content
		}
		files = append(files, packageFile)
		if !binaryAsset {
			for _, reference := range referencedPaths(file.Content) {
				queue = append(queue, queuedReference{From: file.RepositoryPath, Path: reference})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, totalBytes, warnings, nil
}

func (c *GitHubClient) discoverCode(ctx context.Context, query string, limit int) (DiscoverResult, error) {
	values := url.Values{}
	values.Set("q", strings.TrimSpace(query)+" filename:SKILL.md")
	values.Set("per_page", strconv.Itoa(limit))
	var response struct {
		Items []struct {
			Name       string `json:"name"`
			Path       string `json:"path"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName    string `json:"full_name"`
				Description string `json:"description"`
				HTMLURL     string `json:"html_url"`
			} `json:"repository"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/search/code", values, &response); err != nil {
		return DiscoverResult{}, err
	}
	items := make([]Candidate, 0, len(response.Items))
	for _, item := range response.Items {
		items = append(items, Candidate{
			Provider: GitHubProvider, Repository: item.Repository.FullName, Path: item.Path,
			HTMLURL: item.HTMLURL, Description: item.Repository.Description,
			SuggestedIdentifier: SuggestedIdentifier("", item.Repository.FullName), Verified: true,
		})
	}
	return DiscoverResult{Provider: GitHubProvider, SearchMode: "code", Items: items, Count: len(items)}, nil
}

func (c *GitHubClient) discoverRepositories(ctx context.Context, query string, limit int) (DiscoverResult, error) {
	values := url.Values{}
	values.Set("q", strings.TrimSpace(query)+" skill")
	values.Set("sort", "stars")
	values.Set("order", "desc")
	values.Set("per_page", strconv.Itoa(limit))
	var response struct {
		Items []struct {
			FullName      string `json:"full_name"`
			Description   string `json:"description"`
			HTMLURL       string `json:"html_url"`
			DefaultBranch string `json:"default_branch"`
			Stars         int    `json:"stargazers_count"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/search/repositories", values, &response); err != nil {
		return DiscoverResult{}, err
	}
	items := make([]Candidate, 0, len(response.Items))
	for _, item := range response.Items {
		items = append(items, Candidate{
			Provider: GitHubProvider, Repository: item.FullName, Path: "SKILL.md", Ref: item.DefaultBranch,
			HTMLURL: item.HTMLURL, Description: item.Description, Stars: item.Stars,
			SuggestedIdentifier: SuggestedIdentifier("", item.FullName), Verified: false,
		})
	}
	return DiscoverResult{Provider: GitHubProvider, SearchMode: "repository", Items: items, Count: len(items)}, nil
}

func (c *GitHubClient) getJSON(ctx context.Context, endpoint string, query url.Values, target any) error {
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultGitHubAPIBaseURL
	}
	requestURL := baseURL + endpoint
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultGitHubTimeout}
	}
	for attempt := 0; attempt < maxGitHubAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "tiggy-manage-agent/skills")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token := strings.TrimSpace(c.Token); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}

		response, err := client.Do(request)
		if err != nil {
			if attempt == maxGitHubAttempts-1 {
				return err
			}
			if err := waitForGitHubRetry(ctx, githubRetryDelay(attempt, "")); err != nil {
				return err
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxGitHubResponseBytes+1))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if len(body) > maxGitHubResponseBytes {
			return errors.New("github API response exceeds size limit")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			statusErr := newGitHubStatusError(response.StatusCode, body)
			if attempt == maxGitHubAttempts-1 || !isRetryableGitHubStatus(response.StatusCode) {
				return statusErr
			}
			if err := waitForGitHubRetry(ctx, githubRetryDelay(attempt, response.Header.Get("Retry-After"))); err != nil {
				return err
			}
			continue
		}
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("decode github API response: %w", err)
		}
		return nil
	}
	return errors.New("github API request failed after retries")
}

func newGitHubStatusError(statusCode int, body []byte) *githubStatusError {
	message := strings.TrimSpace(string(body))
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Message) != "" {
		message = strings.TrimSpace(payload.Message)
	}
	if len(message) > 300 {
		message = message[:300]
	}
	return &githubStatusError{StatusCode: statusCode, Message: message}
}

func isRetryableGitHubStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func githubRetryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, maxGitHubRetryDelay)
	}
	if retryAt, err := http.ParseTime(strings.TrimSpace(retryAfter)); err == nil {
		return min(max(time.Until(retryAt), 0), maxGitHubRetryDelay)
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}

func waitForGitHubRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type githubStatusError struct {
	StatusCode int
	Message    string
}

func (e *githubStatusError) Error() string {
	return fmt.Sprintf("github API returned %d: %s", e.StatusCode, e.Message)
}

func normalizeSkillPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "SKILL.md"
	}
	cleaned, err := normalizeRepositoryFilePath(value)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(path.Base(cleaned), "SKILL.md") {
		return "", errors.New("github skill path must end with SKILL.md")
	}
	return cleaned, nil
}

func normalizeRepositoryFilePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return "", errors.New("github package path must be a relative slash-separated path")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("github package path cannot escape the repository")
	}
	return cleaned, nil
}

func referencedPaths(content string) []string {
	result := make([]string, 0)
	seen := map[string]bool{}
	appendReference := func(value string) {
		value = strings.Trim(strings.TrimSpace(value), "<>\"'")
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	for _, match := range markdownLinkPattern.FindAllStringSubmatch(content, -1) {
		appendReference(match[1])
	}
	for _, match := range inlineCodePattern.FindAllStringSubmatch(content, -1) {
		for _, token := range strings.Fields(match[1]) {
			candidate := strings.Trim(token, "<>[]{}(),;:\"'")
			extension := strings.ToLower(path.Ext(candidate))
			binaryAsset := isAllowedBinaryAssetExtension(extension)
			if !isAllowedTextAssetExtension(extension) && !binaryAsset {
				continue
			}
			if !binaryAsset && extension != ".md" && extension != ".txt" && !strings.Contains(candidate, "/") {
				continue
			}
			appendReference(candidate)
		}
	}
	for _, match := range plainDocumentPattern.FindAllString(content, -1) {
		appendReference(match)
	}
	return result
}

func resolvePackageReference(packageRoot string, fromPath string, reference string) (string, string) {
	reference = strings.Trim(strings.TrimSpace(reference), "<>\"'")
	lower := strings.ToLower(reference)
	if reference == "" || strings.HasPrefix(reference, "#") || strings.HasPrefix(reference, "/") ||
		strings.Contains(reference, "://") || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "data:") {
		return "", ""
	}
	if index := strings.IndexAny(reference, "#?"); index >= 0 {
		reference = reference[:index]
	}
	decoded, err := url.PathUnescape(reference)
	if err != nil {
		return "", fmt.Sprintf("skipped invalid package reference %q", reference)
	}
	resolved := path.Clean(path.Join(path.Dir(fromPath), decoded))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") ||
		(packageRoot != "" && resolved != packageRoot && !strings.HasPrefix(resolved, packageRoot+"/")) {
		return "", fmt.Sprintf("skipped out-of-package reference %q", reference)
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(resolved, packageRoot), "/")
	if relative == "" {
		return "", ""
	}
	if len(strings.Split(relative, "/")) > maxRemoteAssetDepth {
		return "", fmt.Sprintf("skipped package reference deeper than %d levels: %q", maxRemoteAssetDepth, reference)
	}
	if !isAllowedTextAssetExtension(path.Ext(relative)) && !isAllowedBinaryAssetExtension(path.Ext(relative)) {
		return "", fmt.Sprintf("skipped unsupported package asset %q", reference)
	}
	return resolved, ""
}

func relativePackagePath(packageRoot string, repositoryPath string) string {
	return strings.TrimPrefix(strings.TrimPrefix(repositoryPath, packageRoot), "/")
}

func isAllowedTextAssetExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".csv", ".py", ".js", ".ts", ".sh", ".go", ".html", ".css", ".sql":
		return true
	default:
		return false
	}
}

func isAllowedBinaryAssetExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".docx", ".xlsx", ".pptx":
		return true
	default:
		return false
	}
}

func isScriptExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".py", ".js", ".ts", ".sh", ".go", ".sql":
		return true
	default:
		return false
	}
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func escapePath(value string) string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func parseFrontMatter(content []byte) (string, string, string) {
	frontMatter, ok := frontMatterBlock(content)
	if !ok {
		return "", "", ""
	}
	var name, description, license string
	for _, line := range strings.Split(frontMatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name", "title":
			if name == "" {
				name = value
			}
		case "description":
			description = value
		case "license":
			license = value
		}
	}
	return name, description, license
}

func parsePackageManifest(content []byte) (json.RawMessage, error) {
	frontMatter, ok := frontMatterBlock(content)
	if !ok {
		return json.RawMessage(`{}`), nil
	}
	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(frontMatter), &metadata); err != nil {
		return nil, fmt.Errorf("parse SKILL.md front matter: %w", err)
	}
	inputsSchema, ok := metadata["inputs_schema"]
	if !ok || inputsSchema == nil {
		return json.RawMessage(`{}`), nil
	}
	manifest, err := json.Marshal(map[string]any{"inputs_schema": inputsSchema})
	if err != nil {
		return nil, fmt.Errorf("encode SKILL.md inputs_schema: %w", err)
	}
	return manifest, nil
}

func frontMatterBlock(content []byte) (string, bool) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", false
	}
	return text[4 : 4+end], true
}

func SuggestedIdentifier(name string, repository string) string {
	value := strings.TrimSpace(name)
	if value == "" {
		parts := strings.Split(repository, "/")
		value = parts[len(parts)-1]
	}
	value = strings.ToLower(value)
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '.' || char == '_'
		if valid {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-._")
}
