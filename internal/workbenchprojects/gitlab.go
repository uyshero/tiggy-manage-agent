package workbenchprojects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultGitLabBranch = "main"

type GitLabConfig struct {
	APIURL      string
	Token       string
	NamespaceID string
	Visibility  string
}

type GitLabProvisioner struct {
	config GitLabConfig
	client *http.Client
}

func GitLabProvisionerFromEnv(client *http.Client) (*GitLabProvisioner, error) {
	token := strings.TrimSpace(os.Getenv("TMA_GITLAB_TOKEN"))
	if token == "" {
		return nil, nil
	}
	apiURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TMA_GITLAB_API_URL")), "/")
	if apiURL == "" {
		apiURL = "https://gitlab.com/api/v4"
	}
	return NewGitLabProvisioner(GitLabConfig{
		APIURL: apiURL, Token: token,
		NamespaceID: strings.TrimSpace(os.Getenv("TMA_GITLAB_NAMESPACE_ID")),
		Visibility:  strings.TrimSpace(os.Getenv("TMA_GITLAB_PROJECT_VISIBILITY")),
	}, client)
}

func NewGitLabProvisioner(config GitLabConfig, client *http.Client) (*GitLabProvisioner, error) {
	config.APIURL = strings.TrimRight(strings.TrimSpace(config.APIURL), "/")
	config.Token = strings.TrimSpace(config.Token)
	config.NamespaceID = strings.TrimSpace(config.NamespaceID)
	config.Visibility = strings.TrimSpace(config.Visibility)
	if config.APIURL == "" || config.Token == "" {
		return nil, errors.New("GitLab API URL and token are required")
	}
	parsed, err := url.Parse(config.APIURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("GitLab API URL must be an absolute URL")
	}
	if config.Visibility == "" {
		config.Visibility = "private"
	}
	if config.Visibility != "private" && config.Visibility != "internal" {
		return nil, errors.New("GitLab project visibility must be private or internal")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitLabProvisioner{config: config, client: client}, nil
}

func (p *GitLabProvisioner) Provision(ctx context.Context, input ProvisionInput) (ProvisionResult, error) {
	branch := strings.TrimSpace(input.DefaultBranch)
	if branch == "" {
		branch = defaultGitLabBranch
	}
	result := ProvisionResult{RepositoryID: strings.TrimSpace(input.ExistingRepositoryID), DefaultBranch: branch}
	if result.RepositoryID == "" {
		created, err := p.createProject(ctx, input.Name, input.RepositoryPath)
		if err != nil {
			return result, err
		}
		result = created
		if result.DefaultBranch == "" {
			result.DefaultBranch = branch
		}
	}
	initialized, err := p.repositoryInitialized(ctx, result.RepositoryID)
	if err != nil {
		return result, err
	}
	if initialized {
		return result, nil
	}
	if len(input.Files) == 0 {
		return result, errors.New("GitLab project template is empty")
	}
	if err := p.commitTemplate(ctx, result.RepositoryID, result.DefaultBranch, input.Files); err != nil {
		return result, err
	}
	return result, nil
}

func (p *GitLabProvisioner) createProject(ctx context.Context, name, path string) (ProvisionResult, error) {
	payload := map[string]any{
		"name": name, "path": path, "visibility": p.config.Visibility,
		"initialize_with_readme": false,
	}
	if p.config.NamespaceID != "" {
		if namespaceID, err := strconv.ParseInt(p.config.NamespaceID, 10, 64); err == nil {
			payload["namespace_id"] = namespaceID
		} else {
			return ProvisionResult{}, errors.New("GitLab namespace ID must be numeric")
		}
	}
	var response struct {
		ID            int64  `json:"id"`
		WebURL        string `json:"web_url"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := p.requestJSON(ctx, http.MethodPost, "/projects", payload, &response, http.StatusCreated); err != nil {
		return ProvisionResult{}, fmt.Errorf("create GitLab project: %w", err)
	}
	if response.ID <= 0 || strings.TrimSpace(response.WebURL) == "" {
		return ProvisionResult{}, errors.New("create GitLab project: response is missing project identity")
	}
	return ProvisionResult{
		RepositoryID: strconv.FormatInt(response.ID, 10), RepositoryURL: strings.TrimSpace(response.WebURL),
		DefaultBranch: defaultString(strings.TrimSpace(response.DefaultBranch), defaultGitLabBranch),
	}, nil
}

func (p *GitLabProvisioner) repositoryInitialized(ctx context.Context, projectID string) (bool, error) {
	endpoint := "/projects/" + url.PathEscape(projectID) + "/repository/tree?per_page=1"
	request, err := p.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("inspect GitLab repository: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, gitLabResponseError(response)
	}
	var entries []json.RawMessage
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&entries); err != nil {
		return false, fmt.Errorf("decode GitLab repository tree: %w", err)
	}
	return len(entries) > 0, nil
}

func (p *GitLabProvisioner) commitTemplate(ctx context.Context, projectID, branch string, files []TemplateFile) error {
	actions := make([]map[string]string, 0, len(files))
	for _, file := range files {
		path := strings.Trim(strings.TrimSpace(file.Path), "/")
		if path == "" {
			return errors.New("GitLab template contains an empty path")
		}
		actions = append(actions, map[string]string{"action": "create", "file_path": path, "content": file.Content})
	}
	payload := map[string]any{
		"branch": branch, "commit_message": "Initialize R survival analysis workspace", "actions": actions,
	}
	endpoint := "/projects/" + url.PathEscape(projectID) + "/repository/commits"
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, payload, nil, http.StatusCreated); err != nil {
		return fmt.Errorf("commit GitLab project template: %w", err)
	}
	return nil
}

func (p *GitLabProvisioner) requestJSON(ctx context.Context, method, path string, payload any, output any, expectedStatus int) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := p.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return gitLabResponseError(response)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode GitLab response: %w", err)
	}
	return nil
}

func (p *GitLabProvisioner) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, p.config.APIURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("PRIVATE-TOKEN", p.config.Token)
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func gitLabResponseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("GitLab API returned %d: %s", response.StatusCode, message)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
