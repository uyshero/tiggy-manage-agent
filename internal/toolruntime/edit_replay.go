package toolruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/capability"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

type fileMutationIdentity struct {
	path        string
	placeholder string
	replacement string
}

func persistedSegmentEditReplay(state agentcore.State, call coremodel.ToolCall) (coremodel.ToolResult, bool) {
	current, ok := segmentedEditIdentity(call)
	if !ok {
		return coremodel.ToolResult{}, false
	}

	succeeded := make(map[string]bool, len(state.ToolJournal))
	for _, entry := range state.ToolJournal {
		succeeded[entry.CallID] = entry.Status == agentcore.ToolCallSucceeded && entry.Result != nil && !entry.Result.IsError
	}
	applied := map[fileMutationIdentity]bool{}
	for _, message := range state.Messages {
		for _, content := range message.Content {
			if content.Type != coremodel.ContentToolCall || content.ToolCall == nil || !succeeded[content.ToolCall.ID] {
				continue
			}
			if path, ok := fileWritePath(*content.ToolCall); ok {
				for identity := range applied {
					if identity.path == path {
						delete(applied, identity)
					}
				}
				continue
			}
			identity, ok := segmentedEditIdentity(*content.ToolCall)
			if !ok {
				continue
			}
			for _, introduced := range editReplacementPlaceholders(*content.ToolCall) {
				for previous := range applied {
					if previous.path == identity.path && previous.placeholder == introduced {
						delete(applied, previous)
					}
				}
			}
			applied[identity] = true
		}
	}
	if !applied[current] {
		return coremodel.ToolResult{}, false
	}

	result := capability.EditFileResult{
		Path: current.path, Replacements: 0, AlreadyApplied: true, Success: true,
	}
	resultState, _ := json.Marshal(result)
	return coremodel.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Content: []coremodel.Content{{
			Type: coremodel.ContentText,
			Text: capability.FormatEditResult(result),
		}},
		State: resultState,
	}, true
}

func segmentedEditIdentity(call coremodel.ToolCall) (fileMutationIdentity, bool) {
	normalized := tools.NormalizeCall(tools.Call{Name: call.Name, Arguments: call.Arguments})
	if normalized.Identifier != tools.DefaultIdentifier || normalized.APIName != "edit_file" {
		return fileMutationIdentity{}, false
	}
	var request capability.EditFileRequest
	if json.Unmarshal(call.Arguments, &request) != nil {
		return fileMutationIdentity{}, false
	}
	operation, ok := singleEditOperation(request)
	if !ok {
		return fileMutationIdentity{}, false
	}
	placeholder, ok := tools.SegmentedFilePlaceholderToken(operation.OldString)
	if !ok {
		return fileMutationIdentity{}, false
	}
	path := normalizedMutationPath(request.Path, request.WorkDir)
	if path == "" {
		return fileMutationIdentity{}, false
	}
	hash := sha256.Sum256([]byte(operation.NewString))
	return fileMutationIdentity{
		path: path, placeholder: placeholder, replacement: hex.EncodeToString(hash[:]),
	}, true
}

func fileWritePath(call coremodel.ToolCall) (string, bool) {
	normalized := tools.NormalizeCall(tools.Call{Name: call.Name, Arguments: call.Arguments})
	if normalized.Identifier != tools.DefaultIdentifier || normalized.APIName != "write_file" {
		return "", false
	}
	var request capability.WriteFileRequest
	if json.Unmarshal(call.Arguments, &request) != nil {
		return "", false
	}
	path := normalizedMutationPath(request.Path, "")
	return path, path != ""
}

func editReplacementPlaceholders(call coremodel.ToolCall) []string {
	var request capability.EditFileRequest
	if json.Unmarshal(call.Arguments, &request) != nil {
		return nil
	}
	operations := request.Edits
	if len(operations) == 0 {
		operations = []capability.EditOperation{{OldString: request.OldString, NewString: request.NewString, ReplaceAll: request.ReplaceAll}}
	}
	var placeholders []string
	for _, operation := range operations {
		placeholders = append(placeholders, tools.SegmentedFilePlaceholders(operation.NewString)...)
	}
	return placeholders
}

func singleEditOperation(request capability.EditFileRequest) (capability.EditOperation, bool) {
	if len(request.Edits) == 1 && request.OldString == "" && request.NewString == "" && !request.ReplaceAll {
		return request.Edits[0], true
	}
	if len(request.Edits) == 0 && request.OldString != "" {
		return capability.EditOperation{OldString: request.OldString, NewString: request.NewString, ReplaceAll: request.ReplaceAll}, true
	}
	return capability.EditOperation{}, false
}

func normalizedMutationPath(path, workDir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) && strings.TrimSpace(workDir) != "" {
		path = filepath.Join(workDir, path)
	}
	return filepath.ToSlash(filepath.Clean(path))
}
