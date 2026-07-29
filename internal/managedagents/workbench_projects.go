package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	WorkbenchProjectSyncLocal    = "local"
	WorkbenchProjectSyncing      = "syncing"
	WorkbenchProjectSyncSynced   = "synced"
	WorkbenchProjectSyncError    = "error"
	WorkbenchProjectGitLab       = "gitlab"
	WorkbenchRuntimeUnconfigured = "unconfigured"
	WorkbenchRuntimeStarting     = "starting"
	WorkbenchRuntimeRunning      = "running"
	WorkbenchRuntimeStopped      = "stopped"
	WorkbenchRuntimeError        = "error"
	maxWorkbenchProjectFiles     = 500
)

type WorkbenchProjectFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Status  string `json:"status,omitempty"`
	Content string `json:"content,omitempty"`
}

type WorkbenchProject struct {
	ID                 string                 `json:"id"`
	WorkspaceID        string                 `json:"workspace_id"`
	OwnerID            string                 `json:"owner_id"`
	PluginID           string                 `json:"plugin_id"`
	Name               string                 `json:"name"`
	Objective          string                 `json:"objective,omitempty"`
	RepositoryProvider string                 `json:"repository_provider"`
	RepositoryPath     string                 `json:"repository_path"`
	RepositoryID       string                 `json:"repository_id,omitempty"`
	RepositoryURL      string                 `json:"repository_url,omitempty"`
	DefaultBranch      string                 `json:"default_branch"`
	SyncStatus         string                 `json:"sync_status"`
	SyncError          string                 `json:"sync_error,omitempty"`
	NotebookURL        string                 `json:"notebook_url,omitempty"`
	RuntimeID          string                 `json:"runtime_id,omitempty"`
	RuntimeStatus      string                 `json:"runtime_status"`
	RuntimeURL         string                 `json:"runtime_url,omitempty"`
	RuntimeError       string                 `json:"runtime_error,omitempty"`
	RuntimeStartedAt   *time.Time             `json:"runtime_started_at,omitempty"`
	ActiveFile         string                 `json:"active_file,omitempty"`
	NotebookCode       string                 `json:"notebook_code,omitempty"`
	Files              []WorkbenchProjectFile `json:"files"`
	CreatedBy          string                 `json:"created_by"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

type CreateWorkbenchProjectInput struct {
	WorkspaceID        string                 `json:"workspace_id,omitempty"`
	OwnerID            string                 `json:"owner_id,omitempty"`
	PluginID           string                 `json:"plugin_id"`
	Name               string                 `json:"name"`
	Objective          string                 `json:"objective,omitempty"`
	RepositoryProvider string                 `json:"repository_provider,omitempty"`
	RepositoryPath     string                 `json:"repository_path"`
	NotebookURL        string                 `json:"notebook_url,omitempty"`
	ActiveFile         string                 `json:"active_file,omitempty"`
	NotebookCode       string                 `json:"notebook_code,omitempty"`
	Files              []WorkbenchProjectFile `json:"files,omitempty"`
	CreatedBy          string                 `json:"created_by,omitempty"`
}

type UpdateWorkbenchProjectInput struct {
	WorkspaceID  string                  `json:"workspace_id,omitempty"`
	Name         *string                 `json:"name,omitempty"`
	Objective    *string                 `json:"objective,omitempty"`
	NotebookURL  *string                 `json:"notebook_url,omitempty"`
	ActiveFile   *string                 `json:"active_file,omitempty"`
	NotebookCode *string                 `json:"notebook_code,omitempty"`
	Files        *[]WorkbenchProjectFile `json:"files,omitempty"`
}

type UpdateWorkbenchProjectProvisioningInput struct {
	WorkspaceID   string
	RepositoryID  string
	RepositoryURL string
	DefaultBranch string
	SyncStatus    string
	SyncError     string
}

type UpdateWorkbenchProjectRuntimeInput struct {
	WorkspaceID   string
	RuntimeID     string
	RuntimeStatus string
	RuntimeURL    string
	RuntimeError  string
	StartedAt     *time.Time
}

type WorkbenchProjectStore interface {
	CreateWorkbenchProjectContext(context.Context, CreateWorkbenchProjectInput) (WorkbenchProject, error)
	GetWorkbenchProjectContext(context.Context, string, string) (WorkbenchProject, error)
	ListWorkbenchProjectsContext(context.Context, string, string) ([]WorkbenchProject, error)
	UpdateWorkbenchProjectContext(context.Context, string, UpdateWorkbenchProjectInput) (WorkbenchProject, error)
	UpdateWorkbenchProjectProvisioningContext(context.Context, string, UpdateWorkbenchProjectProvisioningInput) (WorkbenchProject, error)
	UpdateWorkbenchProjectRuntimeContext(context.Context, string, UpdateWorkbenchProjectRuntimeInput) (WorkbenchProject, error)
}

func normalizeWorkbenchProjectText(value, field string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalid, field)
	}
	if len(value) > max {
		return "", fmt.Errorf("%w: %s exceeds %d characters", ErrInvalid, field, max)
	}
	return value, nil
}

func normalizeWorkbenchProjectFiles(files []WorkbenchProjectFile) ([]WorkbenchProjectFile, error) {
	if len(files) > maxWorkbenchProjectFiles {
		return nil, fmt.Errorf("%w: workbench project supports at most %d files", ErrInvalid, maxWorkbenchProjectFiles)
	}
	result := make([]WorkbenchProjectFile, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		path := strings.Trim(strings.TrimSpace(file.Path), "/")
		if path == "" || len(path) > 500 || strings.Contains(path, "..") {
			return nil, fmt.Errorf("%w: invalid workbench project file path", ErrInvalid)
		}
		kind := strings.TrimSpace(file.Kind)
		if kind != "file" && kind != "folder" {
			return nil, fmt.Errorf("%w: invalid workbench project file kind", ErrInvalid)
		}
		content := file.Content
		if kind == "folder" {
			content = ""
		}
		if len(content) > 1_000_000 {
			return nil, fmt.Errorf("%w: workbench project file content exceeds 1000000 characters", ErrInvalid)
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, WorkbenchProjectFile{Path: path, Kind: kind, Status: strings.TrimSpace(file.Status), Content: content})
	}
	return result, nil
}

func validateCreateWorkbenchProject(input CreateWorkbenchProjectInput) (CreateWorkbenchProjectInput, error) {
	var err error
	if input.PluginID, err = normalizeWorkbenchProjectText(input.PluginID, "plugin_id", 240, true); err != nil {
		return input, err
	}
	if input.Name, err = normalizeWorkbenchProjectText(input.Name, "name", 120, true); err != nil {
		return input, err
	}
	if input.Objective, err = normalizeWorkbenchProjectText(input.Objective, "objective", 1200, false); err != nil {
		return input, err
	}
	if input.RepositoryPath, err = normalizeWorkbenchProjectText(input.RepositoryPath, "repository_path", 240, true); err != nil {
		return input, err
	}
	if input.NotebookURL, err = normalizeWorkbenchProjectText(input.NotebookURL, "notebook_url", 1000, false); err != nil {
		return input, err
	}
	if input.ActiveFile, err = normalizeWorkbenchProjectText(input.ActiveFile, "active_file", 500, false); err != nil {
		return input, err
	}
	if len(input.NotebookCode) > 1_000_000 {
		return input, fmt.Errorf("%w: notebook_code exceeds 1000000 characters", ErrInvalid)
	}
	if input.Files, err = normalizeWorkbenchProjectFiles(input.Files); err != nil {
		return input, err
	}
	input.RepositoryProvider = strings.TrimSpace(input.RepositoryProvider)
	if input.RepositoryProvider == "" {
		input.RepositoryProvider = WorkbenchProjectGitLab
	}
	if input.RepositoryProvider != WorkbenchProjectGitLab {
		return input, fmt.Errorf("%w: unsupported repository_provider", ErrInvalid)
	}
	return input, nil
}

const workbenchProjectColumns = `
	id, workspace_id, owner_id, plugin_id, name, objective, repository_provider,
	repository_path, repository_id, repository_url, default_branch, sync_status,
	sync_error, notebook_url, runtime_id, runtime_status, runtime_url, runtime_error, runtime_started_at,
	active_file, notebook_code, files_json,
	created_by, created_at, updated_at`

func scanWorkbenchProject(scanner interface{ Scan(...any) error }) (WorkbenchProject, error) {
	var project WorkbenchProject
	var filesJSON []byte
	err := scanner.Scan(
		&project.ID, &project.WorkspaceID, &project.OwnerID, &project.PluginID, &project.Name, &project.Objective,
		&project.RepositoryProvider, &project.RepositoryPath, &project.RepositoryID, &project.RepositoryURL,
		&project.DefaultBranch, &project.SyncStatus, &project.SyncError, &project.NotebookURL, &project.RuntimeID,
		&project.RuntimeStatus, &project.RuntimeURL, &project.RuntimeError, &project.RuntimeStartedAt,
		&project.ActiveFile, &project.NotebookCode, &filesJSON, &project.CreatedBy, &project.CreatedAt, &project.UpdatedAt,
	)
	if err != nil {
		return WorkbenchProject{}, err
	}
	if err := json.Unmarshal(filesJSON, &project.Files); err != nil {
		return WorkbenchProject{}, fmt.Errorf("decode workbench project files: %w", err)
	}
	if project.Files == nil {
		project.Files = []WorkbenchProjectFile{}
	}
	return project, nil
}

func (s *PostgresStore) CreateWorkbenchProjectContext(ctx context.Context, input CreateWorkbenchProjectInput) (WorkbenchProject, error) {
	input, err := validateCreateWorkbenchProject(input)
	if err != nil {
		return WorkbenchProject{}, err
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return WorkbenchProject{}, err
	}
	defer tx.Rollback()
	ownerID := strings.TrimSpace(scope.OwnerID)
	if ownerID == "" {
		ownerID = strings.TrimSpace(input.OwnerID)
	}
	if ownerID == "" {
		ownerID = defaultString(strings.TrimSpace(input.CreatedBy), "system")
	}
	id, err := nextSequenceID(ctx, tx, "wbp", "tma_workbench_project_id_seq")
	if err != nil {
		return WorkbenchProject{}, err
	}
	filesJSON, _ := json.Marshal(input.Files)
	now := time.Now().UTC()
	project, err := scanWorkbenchProject(tx.QueryRowContext(ctx, `
		INSERT INTO workbench_projects (`+workbenchProjectColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'','', 'main','local','',$9,'','unconfigured','','',NULL,$10,$11,$12,$13,$14,$14)
		RETURNING `+workbenchProjectColumns,
		id, scope.WorkspaceID, ownerID, input.PluginID, input.Name, input.Objective, input.RepositoryProvider,
		input.RepositoryPath, input.NotebookURL, input.ActiveFile, input.NotebookCode, filesJSON,
		defaultString(strings.TrimSpace(input.CreatedBy), ownerID), now))
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return WorkbenchProject{}, fmt.Errorf("%w: workbench project repository path already exists", ErrConflict)
		}
		return WorkbenchProject{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkbenchProject{}, err
	}
	return project, nil
}

func (s *PostgresStore) GetWorkbenchProjectContext(ctx context.Context, workspaceID, id string) (WorkbenchProject, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return WorkbenchProject{}, err
	}
	defer tx.Rollback()
	project, err := scanWorkbenchProject(tx.QueryRowContext(ctx, `SELECT `+workbenchProjectColumns+` FROM workbench_projects WHERE id = $1 AND workspace_id = $2`, strings.TrimSpace(id), scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return WorkbenchProject{}, ErrNotFound
	}
	if err != nil {
		return WorkbenchProject{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkbenchProject{}, err
	}
	return project, nil
}

func (s *PostgresStore) ListWorkbenchProjectsContext(ctx context.Context, workspaceID, pluginID string) ([]WorkbenchProject, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT `+workbenchProjectColumns+` FROM workbench_projects
		WHERE workspace_id = $1 AND ($2 = '' OR plugin_id = $2)
		ORDER BY updated_at DESC, id DESC`, scope.WorkspaceID, strings.TrimSpace(pluginID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []WorkbenchProject{}
	for rows.Next() {
		project, scanErr := scanWorkbenchProject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return projects, nil
}

func (s *PostgresStore) UpdateWorkbenchProjectContext(ctx context.Context, id string, input UpdateWorkbenchProjectInput) (WorkbenchProject, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return WorkbenchProject{}, err
	}
	defer tx.Rollback()
	current, err := scanWorkbenchProject(tx.QueryRowContext(ctx, `SELECT `+workbenchProjectColumns+` FROM workbench_projects WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, strings.TrimSpace(id), scope.WorkspaceID))
	if err == sql.ErrNoRows {
		return WorkbenchProject{}, ErrNotFound
	}
	if err != nil {
		return WorkbenchProject{}, err
	}
	if input.Name != nil {
		current.Name, err = normalizeWorkbenchProjectText(*input.Name, "name", 120, true)
	}
	if err == nil && input.Objective != nil {
		current.Objective, err = normalizeWorkbenchProjectText(*input.Objective, "objective", 1200, false)
	}
	if err == nil && input.NotebookURL != nil {
		current.NotebookURL, err = normalizeWorkbenchProjectText(*input.NotebookURL, "notebook_url", 1000, false)
	}
	if err == nil && input.ActiveFile != nil {
		current.ActiveFile, err = normalizeWorkbenchProjectText(*input.ActiveFile, "active_file", 500, false)
	}
	if err == nil && input.NotebookCode != nil {
		if len(*input.NotebookCode) > 1_000_000 {
			err = fmt.Errorf("%w: notebook_code exceeds 1000000 characters", ErrInvalid)
		} else {
			current.NotebookCode = *input.NotebookCode
		}
	}
	if err == nil && input.Files != nil {
		current.Files, err = normalizeWorkbenchProjectFiles(*input.Files)
	}
	if err != nil {
		return WorkbenchProject{}, err
	}
	filesJSON, _ := json.Marshal(current.Files)
	project, err := scanWorkbenchProject(tx.QueryRowContext(ctx, `
		UPDATE workbench_projects SET name=$3, objective=$4, notebook_url=$5, active_file=$6, notebook_code=$7, files_json=$8, updated_at=$9
		WHERE id=$1 AND workspace_id=$2 RETURNING `+workbenchProjectColumns,
		id, scope.WorkspaceID, current.Name, current.Objective, current.NotebookURL, current.ActiveFile, current.NotebookCode, filesJSON, time.Now().UTC()))
	if err != nil {
		return WorkbenchProject{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkbenchProject{}, err
	}
	return project, nil
}

func (s *PostgresStore) UpdateWorkbenchProjectProvisioningContext(ctx context.Context, id string, input UpdateWorkbenchProjectProvisioningInput) (WorkbenchProject, error) {
	status := strings.TrimSpace(input.SyncStatus)
	if status != WorkbenchProjectSyncLocal && status != WorkbenchProjectSyncing && status != WorkbenchProjectSyncSynced && status != WorkbenchProjectSyncError {
		return WorkbenchProject{}, fmt.Errorf("%w: invalid sync_status", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return WorkbenchProject{}, err
	}
	defer tx.Rollback()
	project, err := scanWorkbenchProject(tx.QueryRowContext(ctx, `
		UPDATE workbench_projects SET repository_id=$3, repository_url=$4, default_branch=$5, sync_status=$6, sync_error=$7, updated_at=$8
		WHERE id=$1 AND workspace_id=$2 RETURNING `+workbenchProjectColumns,
		strings.TrimSpace(id), scope.WorkspaceID, strings.TrimSpace(input.RepositoryID), strings.TrimSpace(input.RepositoryURL),
		defaultString(strings.TrimSpace(input.DefaultBranch), "main"), status, strings.TrimSpace(input.SyncError), time.Now().UTC()))
	if err == sql.ErrNoRows {
		return WorkbenchProject{}, ErrNotFound
	}
	if err != nil {
		return WorkbenchProject{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkbenchProject{}, err
	}
	return project, nil
}

func (s *PostgresStore) UpdateWorkbenchProjectRuntimeContext(ctx context.Context, id string, input UpdateWorkbenchProjectRuntimeInput) (WorkbenchProject, error) {
	status := strings.TrimSpace(input.RuntimeStatus)
	switch status {
	case WorkbenchRuntimeUnconfigured, WorkbenchRuntimeStarting, WorkbenchRuntimeRunning, WorkbenchRuntimeStopped, WorkbenchRuntimeError:
	default:
		return WorkbenchProject{}, fmt.Errorf("%w: invalid runtime_status", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return WorkbenchProject{}, err
	}
	defer tx.Rollback()
	project, err := scanWorkbenchProject(tx.QueryRowContext(ctx, `
		UPDATE workbench_projects SET runtime_id=$3, runtime_status=$4, runtime_url=$5, runtime_error=$6, runtime_started_at=$7, updated_at=$8
		WHERE id=$1 AND workspace_id=$2 RETURNING `+workbenchProjectColumns,
		strings.TrimSpace(id), scope.WorkspaceID, strings.TrimSpace(input.RuntimeID), status, strings.TrimSpace(input.RuntimeURL),
		strings.TrimSpace(input.RuntimeError), input.StartedAt, time.Now().UTC()))
	if err == sql.ErrNoRows {
		return WorkbenchProject{}, ErrNotFound
	}
	if err != nil {
		return WorkbenchProject{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkbenchProject{}, err
	}
	return project, nil
}
