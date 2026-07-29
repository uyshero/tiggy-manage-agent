package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/workbenchruntime"
)

type workbenchProjectHTTPTestStore struct {
	*testStore
	projects map[string]managedagents.WorkbenchProject
}

type workbenchProjectRuntimeTestProvisioner struct{}

func (workbenchProjectRuntimeTestProvisioner) Start(context.Context, workbenchruntime.StartInput) (workbenchruntime.StartResult, error) {
	return workbenchruntime.StartResult{RuntimeID: "tma-r-wbp_000001", Status: managedagents.WorkbenchRuntimeRunning, URL: "http://127.0.0.1:18888/lab"}, nil
}

func (workbenchProjectRuntimeTestProvisioner) Stop(context.Context, workbenchruntime.StopInput) error {
	return nil
}

func (workbenchProjectRuntimeTestProvisioner) RunCleaning(context.Context, workbenchruntime.RunCleaningInput) (workbenchruntime.RunCleaningResult, error) {
	return workbenchruntime.RunCleaningResult{
		ExitCode: 0,
		Stdout:   "清洗完成\nn=12",
		Files:    []workbenchruntime.TemplateFile{{Path: "data/processed/followup_clean.csv", Content: "patient_id,event\nP001,1\n"}},
	}, nil
}

func newWorkbenchProjectHTTPTestStore() *workbenchProjectHTTPTestStore {
	return &workbenchProjectHTTPTestStore{testStore: newTestStore(), projects: map[string]managedagents.WorkbenchProject{}}
}

func (s *workbenchProjectHTTPTestStore) CreateWorkbenchProjectContext(_ context.Context, input managedagents.CreateWorkbenchProjectInput) (managedagents.WorkbenchProject, error) {
	now := time.Now().UTC()
	project := managedagents.WorkbenchProject{
		ID: "wbp_000001", WorkspaceID: input.WorkspaceID, OwnerID: input.OwnerID, PluginID: input.PluginID,
		Name: input.Name, Objective: input.Objective, RepositoryProvider: input.RepositoryProvider,
		RepositoryPath: input.RepositoryPath, DefaultBranch: "main", SyncStatus: managedagents.WorkbenchProjectSyncLocal,
		NotebookURL: input.NotebookURL, ActiveFile: input.ActiveFile, NotebookCode: input.NotebookCode,
		Files: input.Files, CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	s.projects[project.ID] = project
	return project, nil
}

func (s *workbenchProjectHTTPTestStore) GetWorkbenchProjectContext(_ context.Context, _, id string) (managedagents.WorkbenchProject, error) {
	project, ok := s.projects[id]
	if !ok {
		return managedagents.WorkbenchProject{}, managedagents.ErrNotFound
	}
	return project, nil
}

func (s *workbenchProjectHTTPTestStore) ListWorkbenchProjectsContext(_ context.Context, _, pluginID string) ([]managedagents.WorkbenchProject, error) {
	projects := []managedagents.WorkbenchProject{}
	for _, project := range s.projects {
		if pluginID == "" || project.PluginID == pluginID {
			projects = append(projects, project)
		}
	}
	return projects, nil
}

func (s *workbenchProjectHTTPTestStore) UpdateWorkbenchProjectContext(_ context.Context, id string, input managedagents.UpdateWorkbenchProjectInput) (managedagents.WorkbenchProject, error) {
	project, ok := s.projects[id]
	if !ok {
		return managedagents.WorkbenchProject{}, managedagents.ErrNotFound
	}
	if input.NotebookURL != nil {
		project.NotebookURL = *input.NotebookURL
	}
	if input.ActiveFile != nil {
		project.ActiveFile = *input.ActiveFile
	}
	if input.NotebookCode != nil {
		project.NotebookCode = *input.NotebookCode
	}
	if input.Files != nil {
		project.Files = *input.Files
	}
	project.UpdatedAt = time.Now().UTC()
	s.projects[id] = project
	return project, nil
}

func (s *workbenchProjectHTTPTestStore) UpdateWorkbenchProjectProvisioningContext(_ context.Context, id string, input managedagents.UpdateWorkbenchProjectProvisioningInput) (managedagents.WorkbenchProject, error) {
	project, ok := s.projects[id]
	if !ok {
		return managedagents.WorkbenchProject{}, managedagents.ErrNotFound
	}
	project.RepositoryID = input.RepositoryID
	project.RepositoryURL = input.RepositoryURL
	project.DefaultBranch = input.DefaultBranch
	project.SyncStatus = input.SyncStatus
	project.SyncError = input.SyncError
	s.projects[id] = project
	return project, nil
}

func (s *workbenchProjectHTTPTestStore) UpdateWorkbenchProjectRuntimeContext(_ context.Context, id string, input managedagents.UpdateWorkbenchProjectRuntimeInput) (managedagents.WorkbenchProject, error) {
	project, ok := s.projects[id]
	if !ok {
		return managedagents.WorkbenchProject{}, managedagents.ErrNotFound
	}
	project.RuntimeID = input.RuntimeID
	project.RuntimeStatus = input.RuntimeStatus
	project.RuntimeURL = input.RuntimeURL
	project.RuntimeError = input.RuntimeError
	project.RuntimeStartedAt = input.StartedAt
	s.projects[id] = project
	return project, nil
}

func TestWorkbenchProjectCreateAndList(t *testing.T) {
	t.Setenv("TMA_GITLAB_TOKEN", "")
	store := newWorkbenchProjectHTTPTestStore()
	server := NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil)
	request := httptest.NewRequest(http.MethodPost, "/v2/workbench-projects", strings.NewReader(`{
		"plugin_id":"com.tma.r-survival-workbench",
		"name":"肿瘤患者生存分析",
		"objective":"验证后端项目闭环",
		"repository_path":"survival-demo",
		"notebook_code":"fit <- survfit(...)"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	created := decodeResponse[managedagents.WorkbenchProject](t, response)
	if created.ID != "wbp_000001" || created.SyncStatus != managedagents.WorkbenchProjectSyncLocal {
		t.Fatalf("unexpected project: %#v", created)
	}
	if created.ActiveFile != "notebooks/survival-analysis.ipynb" || len(created.Files) < 6 {
		t.Fatalf("R project template was not attached: %#v", created.Files)
	}
	var foundSkill bool
	for _, file := range created.Files {
		if file.Path == "skills/r-survival-data-cleaning/SKILL.md" && strings.Contains(file.Content, "不把字段名") {
			foundSkill = true
		}
	}
	if !foundSkill {
		t.Fatalf("R survival data-cleaning skill was not attached: %#v", created.Files)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/v2/workbench-projects?plugin_id=com.tma.r-survival-workbench", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listResponse.Code, listResponse.Body.String())
	}
	listed := decodeResponse[struct {
		Projects         []managedagents.WorkbenchProject `json:"projects"`
		GitLabConfigured bool                             `json:"gitlab_configured"`
	}](t, listResponse)
	if len(listed.Projects) != 1 || listed.GitLabConfigured {
		t.Fatalf("unexpected list: %#v", listed)
	}

	patchRequest := httptest.NewRequest(http.MethodPatch, "/v2/workbench-projects/wbp_000001?workspace_id=wksp_default", strings.NewReader(`{
		"active_file":"R/clean-data.R"
	}`))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	server.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patchResponse.Code, patchResponse.Body.String())
	}
	patched := decodeResponse[managedagents.WorkbenchProject](t, patchResponse)
	if patched.ActiveFile != "R/clean-data.R" {
		t.Fatalf("active file was not updated: %#v", patched)
	}

	filePatchRequest := httptest.NewRequest(http.MethodPatch, "/v2/workbench-projects/wbp_000001?workspace_id=wksp_default", strings.NewReader(`{
		"active_file":"R/clean-data.R",
		"files":[
			{"path":"R","kind":"folder"},
			{"path":"R/clean-data.R","kind":"file","status":"modified","content":"followup <- raw_followup"}
		]
	}`))
	filePatchRequest.Header.Set("Content-Type", "application/json")
	filePatchResponse := httptest.NewRecorder()
	server.ServeHTTP(filePatchResponse, filePatchRequest)
	if filePatchResponse.Code != http.StatusOK {
		t.Fatalf("file patch status = %d: %s", filePatchResponse.Code, filePatchResponse.Body.String())
	}
	filePatched := decodeResponse[managedagents.WorkbenchProject](t, filePatchResponse)
	if len(filePatched.Files) != 2 || filePatched.Files[1].Content != "followup <- raw_followup" {
		t.Fatalf("file content was not updated: %#v", filePatched.Files)
	}
}

func TestWorkbenchProjectRunCleaningPersistsReport(t *testing.T) {
	store := newWorkbenchProjectHTTPTestStore()
	server := &Server{store: store, workbenchRuntime: workbenchProjectRuntimeTestProvisioner{}}
	created, err := store.CreateWorkbenchProjectContext(context.Background(), managedagents.CreateWorkbenchProjectInput{
		WorkspaceID: "wksp_default", OwnerID: "user_01", PluginID: "com.tma.r-survival-workbench",
		Name: "肿瘤患者生存分析", RepositoryProvider: managedagents.WorkbenchProjectGitLab,
		RepositoryPath: "survival-demo", ActiveFile: "R/clean-data.R",
		Files: []managedagents.WorkbenchProjectFile{{Path: "R/clean-data.R", Kind: "file", Content: "summary(followup)"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	created.RuntimeID = "tma-r-wbp_000001"
	created.RuntimeStatus = managedagents.WorkbenchRuntimeRunning
	store.projects[created.ID] = created

	request := httptest.NewRequest(http.MethodPost, "/v2/workbench-projects/wbp_000001/runtime/run-cleaning?workspace_id=wksp_default", nil)
	request.SetPathValue("project_id", "wbp_000001")
	response := httptest.NewRecorder()
	server.runWorkbenchProjectCleaning(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("run cleaning status = %d: %s", response.Code, response.Body.String())
	}
	decoded := decodeResponse[struct {
		Project managedagents.WorkbenchProject     `json:"project"`
		Result  workbenchruntime.RunCleaningResult `json:"result"`
	}](t, response)
	if decoded.Result.ExitCode != 0 || decoded.Project.ActiveFile != "reports/data-cleaning-summary.md" {
		t.Fatalf("unexpected run response: %#v", decoded)
	}
	var foundReport bool
	var foundProcessed bool
	for _, file := range decoded.Project.Files {
		if file.Path == "reports/data-cleaning-summary.md" && strings.Contains(file.Content, "清洗完成") {
			foundReport = true
		}
		if file.Path == "data/processed/followup_clean.csv" && strings.Contains(file.Content, "P001") {
			foundProcessed = true
		}
	}
	if !foundReport || !foundProcessed {
		t.Fatalf("cleaning report was not persisted: %#v", decoded.Project.Files)
	}
}

func decodeResponse[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return value
}
