package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/tools"
)

const segmentedFileGenerationStateVersion = "tma.segmented_file_generation.v1"

type segmentedFileGenerationState struct {
	ProtocolVersion   string                             `json:"protocol_version"`
	Tasks             map[string]*segmentedFileTaskState `json:"tasks,omitempty"`
	OversizedCalls    int                                `json:"oversized_call_count,omitempty"`
	SegmentCount      int                                `json:"segment_count,omitempty"`
	IdempotentReplays int                                `json:"idempotent_replay_count,omitempty"`
}

type segmentedFileTaskState struct {
	Path                 string            `json:"path"`
	Remaining            []string          `json:"remaining_placeholders,omitempty"`
	SegmentHashes        map[string]string `json:"segment_hashes,omitempty"`
	ValidationSucceeded  bool              `json:"validation_succeeded,omitempty"`
	PlanApproved         bool              `json:"plan_approved,omitempty"`
	FinalArtifactCreated bool              `json:"final_artifact_created,omitempty"`
	StartedAtUnixMilli   int64             `json:"started_at_unix_milli"`
}

func newSegmentedFileGenerationState() *segmentedFileGenerationState {
	return &segmentedFileGenerationState{
		ProtocolVersion: segmentedFileGenerationStateVersion,
		Tasks:           map[string]*segmentedFileTaskState{},
	}
}

func segmentedFileGenerationStateFromRaw(raw json.RawMessage) *segmentedFileGenerationState {
	state := newSegmentedFileGenerationState()
	if len(raw) == 0 || json.Unmarshal(raw, state) != nil || state.ProtocolVersion != segmentedFileGenerationStateVersion {
		return newSegmentedFileGenerationState()
	}
	if state.Tasks == nil {
		state.Tasks = map[string]*segmentedFileTaskState{}
	}
	return state
}

func (state *segmentedFileGenerationState) raw() json.RawMessage {
	if state == nil {
		return nil
	}
	encoded, _ := json.Marshal(state)
	return encoded
}

func (state *segmentedFileGenerationState) active() bool {
	return state != nil && len(state.Tasks) > 0
}

func fileMutationPath(call tools.Call) string {
	switch normalizeToolAPIName(call.APIName) {
	case "write_file":
		var request capability.WriteFileRequest
		if json.Unmarshal(call.Arguments, &request) == nil {
			return filepath.Clean(request.Path)
		}
	case "edit_file":
		var request capability.EditFileRequest
		if json.Unmarshal(call.Arguments, &request) == nil {
			path := request.Path
			if path == "" {
				path = request.FilePath
			}
			if request.WorkDir != "" && !filepath.IsAbs(path) {
				path = filepath.Join(request.WorkDir, path)
			}
			return filepath.Clean(path)
		}
	}
	return ""
}

func segmentedSkeletonCall(call tools.Call) bool {
	if normalizeToolAPIName(call.APIName) != "write_file" {
		return false
	}
	var request capability.WriteFileRequest
	return json.Unmarshal(call.Arguments, &request) == nil && len(tools.SegmentedFilePlaceholders(string(request.Content))) > 0
}

func (state *segmentedFileGenerationState) trackedMutation(call tools.Call) bool {
	if state == nil {
		return false
	}
	_, ok := state.Tasks[fileMutationPath(call)]
	return ok
}

func (state *segmentedFileGenerationState) shouldDeferArtifacts(call tools.Call) bool {
	if segmentedSkeletonCall(call) || state.trackedMutation(call) {
		return true
	}
	if !state.active() {
		return false
	}
	switch normalizeToolAPIName(call.APIName) {
	case "run_command", "execute_code":
		return true
	default:
		return false
	}
}

func (state *segmentedFileGenerationState) planApproves(call tools.Call) bool {
	if state == nil || normalizeToolAPIName(call.APIName) != "edit_file" {
		return false
	}
	task := state.Tasks[fileMutationPath(call)]
	if task == nil || !task.PlanApproved {
		return false
	}
	var request capability.EditFileRequest
	if json.Unmarshal(call.Arguments, &request) != nil || !tools.IsSegmentedFilePlaceholder(request.OldString) {
		return false
	}
	return containsString(task.Remaining, request.OldString) || task.SegmentHashes[request.OldString] != ""
}

func (state *segmentedFileGenerationState) idempotentReplay(call tools.Call) (tools.ExecutionResult, bool) {
	if state == nil || normalizeToolAPIName(call.APIName) != "edit_file" {
		return tools.ExecutionResult{}, false
	}
	var request capability.EditFileRequest
	if json.Unmarshal(call.Arguments, &request) != nil || !tools.IsSegmentedFilePlaceholder(request.OldString) {
		return tools.ExecutionResult{}, false
	}
	task := state.Tasks[fileMutationPath(call)]
	if task == nil || containsString(task.Remaining, request.OldString) {
		return tools.ExecutionResult{}, false
	}
	hash := sha256.Sum256([]byte(request.NewString))
	hashText := hex.EncodeToString(hash[:])
	if task.SegmentHashes[request.OldString] != hashText {
		return tools.ExecutionResult{}, false
	}
	editResult := capability.EditFileResult{
		Path: fileMutationPath(call), Replacements: 0, AlreadyApplied: true, Success: true,
	}
	stateJSON, _ := json.Marshal(editResult)
	return tools.ExecutionResult{
		ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
		Content: capability.FormatEditResult(editResult), State: stateJSON,
	}, true
}

func (state *segmentedFileGenerationState) observe(call tools.Call, result tools.ExecutionResult, planApproved bool) {
	if state == nil || result.Error != nil {
		return
	}
	switch normalizeToolAPIName(call.APIName) {
	case "write_file":
		var request capability.WriteFileRequest
		if json.Unmarshal(call.Arguments, &request) != nil {
			return
		}
		placeholders := tools.SegmentedFilePlaceholders(string(request.Content))
		if len(placeholders) == 0 {
			return
		}
		path := filepath.Clean(request.Path)
		state.Tasks[path] = &segmentedFileTaskState{
			Path: path, Remaining: placeholders, SegmentHashes: map[string]string{},
			PlanApproved: planApproved, StartedAtUnixMilli: time.Now().UnixMilli(),
		}
	case "edit_file":
		var request capability.EditFileRequest
		if json.Unmarshal(call.Arguments, &request) != nil {
			return
		}
		task := state.Tasks[fileMutationPath(call)]
		if task == nil || !tools.IsSegmentedFilePlaceholder(request.OldString) {
			return
		}
		var editResult capability.EditFileResult
		if json.Unmarshal(result.State, &editResult) != nil || !editResult.Success {
			return
		}
		if task.SegmentHashes == nil {
			task.SegmentHashes = map[string]string{}
		}
		hash := sha256.Sum256([]byte(request.NewString))
		task.SegmentHashes[request.OldString] = hex.EncodeToString(hash[:])
		task.Remaining = removeString(task.Remaining, request.OldString)
		for _, placeholder := range tools.SegmentedFilePlaceholders(request.NewString) {
			if !containsString(task.Remaining, placeholder) && task.SegmentHashes[placeholder] == "" {
				task.Remaining = append(task.Remaining, placeholder)
			}
		}
		task.ValidationSucceeded = false
		task.FinalArtifactCreated = false
		task.PlanApproved = task.PlanApproved || planApproved
		if editResult.AlreadyApplied {
			state.IdempotentReplays++
		} else {
			state.SegmentCount++
		}
	case "run_command", "execute_code":
		var commandResult capability.CommandResult
		if json.Unmarshal(result.State, &commandResult) != nil || commandResult.ExitCode != 0 {
			return
		}
		for _, task := range state.Tasks {
			if len(task.Remaining) == 0 && validationCallCoversTask(call, task) {
				task.ValidationSucceeded = true
			}
		}
	}
}

func validationCallCoversTask(call tools.Call, task *segmentedFileTaskState) bool {
	var commandText string
	var workDir string
	switch normalizeToolAPIName(call.APIName) {
	case "run_command":
		var request capability.RunCommandRequest
		if json.Unmarshal(call.Arguments, &request) != nil {
			return false
		}
		commandText = strings.Join(append([]string{request.Command}, request.Args...), " ")
		workDir = request.WorkDir
	case "execute_code":
		var request capability.ExecuteCodeRequest
		if json.Unmarshal(call.Arguments, &request) != nil {
			return false
		}
		commandText = request.Code
		workDir = request.WorkDir
	default:
		return false
	}
	lower := strings.ToLower(commandText)
	pathReferenced := strings.Contains(commandText, task.Path) || strings.Contains(commandText, filepath.Base(task.Path))
	validationIntent := false
	for _, marker := range []string{"test", "check", "lint", "compile", "py_compile", "pytest", "gofmt", "go test", "tsc", "build"} {
		if strings.Contains(lower, marker) {
			validationIntent = true
			break
		}
	}
	return validationIntent && (pathReferenced || pathWithinWorkDir(task.Path, workDir))
}

func pathWithinWorkDir(path, workDir string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(workDir) == "" {
		return false
	}
	absolutePath, pathErr := filepath.Abs(path)
	absoluteWorkDir, workDirErr := filepath.Abs(workDir)
	if pathErr != nil || workDirErr != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteWorkDir, absolutePath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (state *segmentedFileGenerationState) completionBlock(ctx context.Context, executionContext tools.ExecutionContext) (string, error) {
	if !state.active() {
		return "", nil
	}
	provider := executionContext.Provider
	if provider == nil {
		return "Runtime cannot verify segmented files because the filesystem provider is unavailable.", nil
	}
	paths := make([]string, 0, len(state.Tasks))
	for path := range state.Tasks {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var blockers []string
	for _, path := range paths {
		task := state.Tasks[path]
		remaining, err := remainingSegmentPlaceholders(ctx, provider, executionContext, path)
		if err != nil {
			blockers = append(blockers, fmt.Sprintf("%s could not be searched for final verification: %v", path, err))
			continue
		}
		task.Remaining = remaining
		if len(remaining) > 0 {
			task.ValidationSucceeded = false
			blockers = append(blockers, fmt.Sprintf("%s still contains %d placeholder(s): %s", path, len(remaining), strings.Join(remaining, ", ")))
			continue
		}
		if !task.ValidationSucceeded {
			blockers = append(blockers, fmt.Sprintf("%s has no placeholders but still requires a successful syntax check or test after the final segment", path))
		}
	}
	if len(blockers) == 0 {
		return "", nil
	}
	return "Runtime completion gate blocked the final response:\n- " + strings.Join(blockers, "\n- ") + "\nContinue with exactly one file mutation per response, then run the appropriate validation command. Do not claim completion yet.", nil
}

func remainingSegmentPlaceholders(ctx context.Context, provider capability.Provider, executionContext tools.ExecutionContext, path string) ([]string, error) {
	meta := capability.NewRequestMeta(executionContext.SessionID, executionContext.TurnID, executionContext.Deadline)
	if searcher, ok := provider.(capability.FileSearchProvider); ok {
		result, err := searcher.SearchFile(ctx, capability.SearchFileRequest{
			Meta: meta, Path: path, Query: "__TMA_PLACEHOLDER_", MaxResults: 100,
		})
		if err != nil {
			return nil, err
		}
		if result.Binary {
			return nil, fmt.Errorf("generated file is binary")
		}
		var matchedLines strings.Builder
		for _, match := range result.Matches {
			matchedLines.WriteString(match.Line)
			matchedLines.WriteByte('\n')
		}
		remaining := tools.SegmentedFilePlaceholders(matchedLines.String())
		if len(remaining) == 0 && len(result.Matches) > 0 {
			remaining = []string{"__TMA_PLACEHOLDER_..."}
		}
		return remaining, nil
	}
	result, err := provider.ReadFile(ctx, capability.ReadFileRequest{Meta: meta, Path: path})
	if err != nil {
		return nil, err
	}
	if result.Truncated || (!result.EOF && result.SizeBytes > int64(len(result.Content))) {
		return nil, fmt.Errorf("provider lacks search_file and the file does not fit in one bounded read")
	}
	return tools.SegmentedFilePlaceholders(string(result.Content)), nil
}

func (state *segmentedFileGenerationState) publishFinalArtifacts(ctx context.Context, executionContext tools.ExecutionContext) error {
	if state == nil || executionContext.ArtifactRecorder == nil {
		return nil
	}
	for _, task := range state.Tasks {
		if task.FinalArtifactCreated || len(task.Remaining) > 0 || !task.ValidationSucceeded {
			continue
		}
		call := tools.Call{ID: "segmented-file-final", Identifier: tools.DefaultIdentifier, APIName: "segmented_file_generation"}
		result := tools.ExecutionResult{
			ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
			ExportedFiles: []tools.ArtifactExport{{Path: task.Path, Name: filepath.Base(task.Path), Description: "Validated segmented file", ArtifactType: "file"}},
		}
		if _, err := executionContext.ArtifactRecorder.RecordToolArtifact(ctx, call, executionContext, result); err != nil {
			return err
		}
		task.FinalArtifactCreated = true
	}
	return nil
}

func (state *segmentedFileGenerationState) remainingCount() int {
	count := 0
	for _, task := range state.Tasks {
		count += len(task.Remaining)
	}
	return count
}

func (state *segmentedFileGenerationState) durationMillis() int64 {
	if state == nil {
		return 0
	}
	var earliest int64
	for _, task := range state.Tasks {
		if task.StartedAtUnixMilli > 0 && (earliest == 0 || task.StartedAtUnixMilli < earliest) {
			earliest = task.StartedAtUnixMilli
		}
	}
	if earliest == 0 {
		return 0
	}
	return max(time.Now().UnixMilli()-earliest, 0)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
