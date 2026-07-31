package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	ranalysisproject "tiggy-manage-agent/examples/r-analysis-project"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/workbenchprojects"
	"tiggy-manage-agent/internal/workbenchruntime"
)

const rSurvivalWorkbenchPluginID = "com.tma.r-survival-workbench"

type workbenchProjectTemplate struct {
	files        []workbenchprojects.TemplateFile
	fileMetadata []managedagents.WorkbenchProjectFile
	activeFile   string
}

func loadWorkbenchProjectTemplate(pluginID string) (workbenchProjectTemplate, error) {
	if strings.TrimSpace(pluginID) != rSurvivalWorkbenchPluginID {
		return workbenchProjectTemplate{}, fmt.Errorf("%w: unsupported workbench plugin", managedagents.ErrInvalid)
	}
	templateFiles, err := ranalysisproject.Files()
	if err != nil {
		return workbenchProjectTemplate{}, err
	}
	files := make([]workbenchprojects.TemplateFile, 0, len(templateFiles))
	folders := map[string]struct{}{}
	metadata := make([]managedagents.WorkbenchProjectFile, 0, len(templateFiles)*2)
	for _, file := range templateFiles {
		files = append(files, workbenchprojects.TemplateFile{Path: file.Path, Content: file.Content})
		parts := strings.Split(file.Path, "/")
		for index := 1; index < len(parts); index++ {
			folders[strings.Join(parts[:index], "/")] = struct{}{}
		}
		metadata = append(metadata, managedagents.WorkbenchProjectFile{Path: file.Path, Kind: "file", Status: "clean", Content: file.Content})
	}
	for folder := range folders {
		metadata = append(metadata, managedagents.WorkbenchProjectFile{Path: folder, Kind: "folder"})
	}
	sort.Slice(metadata, func(i, j int) bool {
		if metadata[i].Path == metadata[j].Path {
			return metadata[i].Kind == "folder"
		}
		return metadata[i].Path < metadata[j].Path
	})
	return workbenchProjectTemplate{
		files: files, fileMetadata: metadata, activeFile: "notebooks/survival-analysis.ipynb",
	}, nil
}

func workbenchTemplateFilesForProject(project managedagents.WorkbenchProject, template workbenchProjectTemplate) []workbenchprojects.TemplateFile {
	fallback := make(map[string]string, len(template.files))
	for _, file := range template.files {
		fallback[file.Path] = file.Content
	}
	files := make([]workbenchprojects.TemplateFile, 0, len(project.Files))
	for _, file := range project.Files {
		if file.Kind != "file" {
			continue
		}
		content := file.Content
		if content == "" {
			content = fallback[file.Path]
		}
		files = append(files, workbenchprojects.TemplateFile{Path: file.Path, Content: content})
	}
	if len(files) == 0 {
		return template.files
	}
	return files
}

func workbenchRuntimeFilesForProject(project managedagents.WorkbenchProject, template workbenchProjectTemplate) []workbenchruntime.TemplateFile {
	files := workbenchTemplateFilesForProject(project, template)
	result := make([]workbenchruntime.TemplateFile, 0, len(files))
	for _, file := range files {
		result = append(result, workbenchruntime.TemplateFile{Path: file.Path, Content: file.Content})
	}
	return result
}

func (s *Server) workbenchProjectStore(w http.ResponseWriter) (managedagents.WorkbenchProjectStore, bool) {
	store, ok := s.store.(managedagents.WorkbenchProjectStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "workbench projects are unavailable"})
	}
	return store, ok
}

func (s *Server) listWorkbenchProjects(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	pluginID := r.URL.Query().Get("plugin_id")
	if strings.HasPrefix(r.URL.Path, "/v2/r-survival-projects") {
		pluginID = rSurvivalWorkbenchPluginID
	}
	projects, err := store.ListWorkbenchProjectsContext(
		r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), pluginID,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": nonNilSlice(projects), "gitlab_configured": s.workbenchProjects != nil})
}

func (s *Server) createWorkbenchProject(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	var request struct {
		WorkspaceID    string `json:"workspace_id"`
		PluginID       string `json:"plugin_id"`
		Name           string `json:"name"`
		Objective      string `json:"objective"`
		RepositoryPath string `json:"repository_path"`
		NotebookURL    string `json:"notebook_url"`
		NotebookCode   string `json:"notebook_code"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v2/r-survival-projects") {
		request.PluginID = rSurvivalWorkbenchPluginID
	}
	template, err := loadWorkbenchProjectTemplate(request.PluginID)
	if err != nil {
		writeError(w, err)
		return
	}
	project, err := store.CreateWorkbenchProjectContext(r.Context(), managedagents.CreateWorkbenchProjectInput{
		WorkspaceID: requestWorkspaceID(r, request.WorkspaceID), OwnerID: requestOwnerID(r, requestActorID(r, "system")),
		PluginID: request.PluginID, Name: request.Name, Objective: request.Objective,
		RepositoryProvider: managedagents.WorkbenchProjectGitLab, RepositoryPath: request.RepositoryPath,
		NotebookURL: request.NotebookURL, ActiveFile: template.activeFile, NotebookCode: request.NotebookCode,
		Files: template.fileMetadata, CreatedBy: requestActorID(r, "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	if s.workbenchProjects != nil {
		project = s.provisionWorkbenchProject(r, store, project, template)
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) updateWorkbenchProject(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	var input managedagents.UpdateWorkbenchProjectInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, defaultValue(input.WorkspaceID, r.URL.Query().Get("workspace_id")))
	project, err := store.UpdateWorkbenchProjectContext(r.Context(), r.PathValue("project_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) syncWorkbenchProject(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	if s.workbenchProjects == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GitLab project provisioning is not configured"})
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	project, err := store.GetWorkbenchProjectContext(r.Context(), workspaceID, r.PathValue("project_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	template, err := loadWorkbenchProjectTemplate(project.PluginID)
	if err != nil {
		writeError(w, err)
		return
	}
	project = s.provisionWorkbenchProject(r, store, project, template)
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) startWorkbenchProjectRuntime(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	if s.workbenchRuntime == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "R Notebook runtime provisioning is not configured"})
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	project, err := store.GetWorkbenchProjectContext(r.Context(), workspaceID, r.PathValue("project_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if project.RuntimeStatus == managedagents.WorkbenchRuntimeRunning && project.RuntimeURL != "" {
		writeJSON(w, http.StatusOK, project)
		return
	}
	template, err := loadWorkbenchProjectTemplate(project.PluginID)
	if err != nil {
		writeError(w, err)
		return
	}
	starting, err := store.UpdateWorkbenchProjectRuntimeContext(r.Context(), project.ID, managedagents.UpdateWorkbenchProjectRuntimeInput{
		WorkspaceID: project.WorkspaceID, RuntimeID: project.RuntimeID, RuntimeStatus: managedagents.WorkbenchRuntimeStarting,
		RuntimeURL: project.RuntimeURL,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result, provisionErr := s.workbenchRuntime.Start(r.Context(), workbenchruntime.StartInput{ProjectID: starting.ID, Files: workbenchRuntimeFilesForProject(project, template)})
	if provisionErr != nil {
		failed, updateErr := store.UpdateWorkbenchProjectRuntimeContext(r.Context(), starting.ID, managedagents.UpdateWorkbenchProjectRuntimeInput{
			WorkspaceID: starting.WorkspaceID, RuntimeID: result.RuntimeID, RuntimeStatus: managedagents.WorkbenchRuntimeError,
			RuntimeURL: result.URL, RuntimeError: provisionErr.Error(),
		})
		if updateErr == nil {
			writeJSON(w, http.StatusOK, failed)
			return
		}
		writeError(w, updateErr)
		return
	}
	startedAt := time.Now().UTC()
	started, err := store.UpdateWorkbenchProjectRuntimeContext(r.Context(), starting.ID, managedagents.UpdateWorkbenchProjectRuntimeInput{
		WorkspaceID: starting.WorkspaceID, RuntimeID: result.RuntimeID, RuntimeStatus: managedagents.WorkbenchRuntimeRunning,
		RuntimeURL: result.URL, StartedAt: &startedAt,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, started)
}

func (s *Server) stopWorkbenchProjectRuntime(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	if s.workbenchRuntime == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "R Notebook runtime provisioning is not configured"})
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	project, err := store.GetWorkbenchProjectContext(r.Context(), workspaceID, r.PathValue("project_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if project.RuntimeID != "" {
		if err := s.workbenchRuntime.Stop(r.Context(), workbenchruntime.StopInput{RuntimeID: project.RuntimeID}); err != nil {
			failed, updateErr := store.UpdateWorkbenchProjectRuntimeContext(r.Context(), project.ID, managedagents.UpdateWorkbenchProjectRuntimeInput{
				WorkspaceID: project.WorkspaceID, RuntimeID: project.RuntimeID, RuntimeStatus: managedagents.WorkbenchRuntimeError,
				RuntimeURL: project.RuntimeURL, RuntimeError: err.Error(),
			})
			if updateErr == nil {
				writeJSON(w, http.StatusOK, failed)
				return
			}
			writeError(w, updateErr)
			return
		}
	}
	stopped, err := store.UpdateWorkbenchProjectRuntimeContext(r.Context(), project.ID, managedagents.UpdateWorkbenchProjectRuntimeInput{
		WorkspaceID: project.WorkspaceID, RuntimeID: project.RuntimeID, RuntimeStatus: managedagents.WorkbenchRuntimeStopped,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stopped)
}

func (s *Server) runWorkbenchProjectCleaning(w http.ResponseWriter, r *http.Request) {
	store, ok := s.workbenchProjectStore(w)
	if !ok {
		return
	}
	if s.workbenchRuntime == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "R Notebook runtime provisioning is not configured"})
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	project, err := store.GetWorkbenchProjectContext(r.Context(), workspaceID, r.PathValue("project_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if project.RuntimeID == "" || project.RuntimeStatus != managedagents.WorkbenchRuntimeRunning {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "R Runtime must be running before data cleaning can execute"})
		return
	}
	template, err := loadWorkbenchProjectTemplate(project.PluginID)
	if err != nil {
		writeError(w, err)
		return
	}
	result, runErr := s.workbenchRuntime.RunCleaning(r.Context(), workbenchruntime.RunCleaningInput{
		RuntimeID: project.RuntimeID, ProjectID: project.ID, Files: workbenchRuntimeFilesForProject(project, template),
	})
	if runErr != nil {
		writeError(w, runErr)
		return
	}
	report := strings.TrimSpace(result.Stdout)
	if report == "" {
		report = "# 数据清洗执行报告\n\n清洗流程已执行，但没有输出日志。"
	}
	report = strings.TrimSpace(report + fmt.Sprintf("\n\n## 运行元数据\n\n- 执行时间：%s\n- 退出码：%d\n", time.Now().UTC().Format(time.RFC3339), result.ExitCode))
	if strings.TrimSpace(result.Stderr) != "" {
		report = strings.TrimSpace(report + "\n\n## stderr\n\n```text\n" + strings.TrimSpace(result.Stderr) + "\n```")
	}
	files := upsertWorkbenchProjectFile(project.Files, managedagents.WorkbenchProjectFile{
		Path: "reports/data-cleaning-summary.md", Kind: "file", Status: "modified", Content: report,
	})
	for _, file := range result.Files {
		files = upsertWorkbenchProjectFile(files, managedagents.WorkbenchProjectFile{
			Path: file.Path, Kind: "file", Status: "modified", Content: file.Content,
		})
	}
	updated, err := store.UpdateWorkbenchProjectContext(r.Context(), project.ID, managedagents.UpdateWorkbenchProjectInput{
		WorkspaceID: project.WorkspaceID, Files: &files, ActiveFile: stringPtr("reports/data-cleaning-summary.md"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": updated, "result": result})
}

func upsertWorkbenchProjectFile(files []managedagents.WorkbenchProjectFile, next managedagents.WorkbenchProjectFile) []managedagents.WorkbenchProjectFile {
	result := make([]managedagents.WorkbenchProjectFile, 0, len(files)+2)
	seen := map[string]struct{}{}
	for _, folder := range parentWorkbenchProjectFolders(next.Path) {
		if !workbenchProjectFileExists(files, folder) {
			result = append(result, managedagents.WorkbenchProjectFile{Path: folder, Kind: "folder"})
			seen[folder] = struct{}{}
		}
	}
	replaced := false
	for _, file := range files {
		if _, ok := seen[file.Path]; ok {
			continue
		}
		if file.Path == next.Path {
			result = append(result, next)
			replaced = true
			continue
		}
		result = append(result, file)
	}
	if !replaced {
		result = append(result, next)
	}
	return result
}

func workbenchProjectFileExists(files []managedagents.WorkbenchProjectFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func parentWorkbenchProjectFolders(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	folders := []string{}
	for index := 1; index < len(parts); index++ {
		folders = append(folders, strings.Join(parts[:index], "/"))
	}
	return folders
}

func stringPtr(value string) *string {
	return &value
}

func (s *Server) provisionWorkbenchProject(r *http.Request, store managedagents.WorkbenchProjectStore, project managedagents.WorkbenchProject, template workbenchProjectTemplate) managedagents.WorkbenchProject {
	syncing, err := store.UpdateWorkbenchProjectProvisioningContext(r.Context(), project.ID, managedagents.UpdateWorkbenchProjectProvisioningInput{
		WorkspaceID: project.WorkspaceID, RepositoryID: project.RepositoryID, RepositoryURL: project.RepositoryURL,
		DefaultBranch: project.DefaultBranch, SyncStatus: managedagents.WorkbenchProjectSyncing,
	})
	if err != nil {
		s.logger.Warn("mark workbench project syncing failed", "project_id", project.ID, "error", err)
		return project
	}
	result, provisionErr := s.workbenchProjects.Provision(r.Context(), workbenchprojects.ProvisionInput{
		Name: syncing.Name, RepositoryPath: syncing.RepositoryPath, ExistingRepositoryID: syncing.RepositoryID,
		DefaultBranch: syncing.DefaultBranch, Files: workbenchTemplateFilesForProject(syncing, template),
	})
	status := managedagents.WorkbenchProjectSyncSynced
	errorMessage := ""
	if provisionErr != nil {
		status = managedagents.WorkbenchProjectSyncError
		errorMessage = provisionErr.Error()
		s.logger.Warn("workbench GitLab project provisioning failed", "project_id", syncing.ID, "error", provisionErr)
	}
	updated, updateErr := store.UpdateWorkbenchProjectProvisioningContext(r.Context(), syncing.ID, managedagents.UpdateWorkbenchProjectProvisioningInput{
		WorkspaceID:   syncing.WorkspaceID,
		RepositoryID:  defaultValue(result.RepositoryID, syncing.RepositoryID),
		RepositoryURL: defaultValue(result.RepositoryURL, syncing.RepositoryURL),
		DefaultBranch: defaultValue(result.DefaultBranch, syncing.DefaultBranch),
		SyncStatus:    status, SyncError: errorMessage,
	})
	if updateErr != nil {
		s.logger.Warn("persist workbench project provisioning state failed", "project_id", syncing.ID, "error", updateErr)
		return syncing
	}
	return updated
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
