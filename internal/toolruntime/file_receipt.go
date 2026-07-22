package toolruntime

import (
	"encoding/json"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/capability"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

type persistedFileReceipt struct {
	revision      string
	contentSHA256 string
}

func persistedFileReceiptForEdit(state agentcore.State, call coremodel.ToolCall) (persistedFileReceipt, bool) {
	path, ok := editCallPath(call)
	if !ok {
		return persistedFileReceipt{}, false
	}

	journal := make(map[string]agentcore.ToolCallJournalEntry, len(state.ToolJournal))
	for _, entry := range state.ToolJournal {
		if entry.Status == agentcore.ToolCallSucceeded && entry.Result != nil && !entry.Result.IsError {
			journal[entry.CallID] = entry
		}
	}
	receipts := map[string]persistedFileReceipt{}
	for _, message := range state.Messages {
		for _, content := range message.Content {
			if content.Type != coremodel.ContentToolCall || content.ToolCall == nil {
				continue
			}
			entry, succeeded := journal[content.ToolCall.ID]
			if !succeeded {
				continue
			}
			recordPersistedFileReceipt(receipts, *content.ToolCall, entry)
		}
	}
	receipt, ok := receipts[path]
	return receipt, ok && receipt.revision != ""
}

func editCallPath(call coremodel.ToolCall) (string, bool) {
	normalized := tools.NormalizeCall(tools.Call{Name: call.Name, Arguments: call.Arguments})
	if normalized.Identifier != tools.DefaultIdentifier || normalized.APIName != "edit_file" {
		return "", false
	}
	var request capability.EditFileRequest
	if json.Unmarshal(call.Arguments, &request) != nil {
		return "", false
	}
	path := normalizedMutationPath(request.Path, request.WorkDir)
	return path, path != ""
}

func recordPersistedFileReceipt(receipts map[string]persistedFileReceipt, call coremodel.ToolCall, entry agentcore.ToolCallJournalEntry) {
	normalized := tools.NormalizeCall(tools.Call{Name: call.Name, Arguments: call.Arguments})
	if normalized.Identifier != tools.DefaultIdentifier || entry.Result == nil {
		return
	}
	switch normalized.APIName {
	case "read_file":
		var request capability.ReadFileRequest
		var result capability.FileResult
		if json.Unmarshal(call.Arguments, &request) != nil || json.Unmarshal(entry.Result.State, &result) != nil || result.Binary {
			return
		}
		recordReceiptPaths(receipts, persistedFileReceipt{revision: result.FileRevision, contentSHA256: result.ContentSHA256}, request.Path, result.Path)
	case "write_file":
		var request capability.WriteFileRequest
		var result capability.FileResult
		if json.Unmarshal(call.Arguments, &request) != nil || json.Unmarshal(entry.Result.State, &result) != nil {
			return
		}
		recordReceiptPaths(receipts, persistedFileReceipt{revision: result.FileRevision, contentSHA256: result.ContentSHA256}, request.Path, result.Path)
	case "edit_file":
		var request capability.EditFileRequest
		var result capability.EditFileResult
		if json.Unmarshal(call.Arguments, &request) != nil || json.Unmarshal(entry.Result.State, &result) != nil || !result.Success {
			return
		}
		recordReceiptPaths(receipts, persistedFileReceipt{revision: result.FileRevision, contentSHA256: result.ContentSHA256}, normalizedMutationPath(request.Path, request.WorkDir), result.Path)
	}
}

func recordReceiptPaths(receipts map[string]persistedFileReceipt, receipt persistedFileReceipt, paths ...string) {
	receipt.revision = strings.TrimSpace(receipt.revision)
	receipt.contentSHA256 = strings.ToLower(strings.TrimSpace(receipt.contentSHA256))
	if receipt.revision == "" {
		return
	}
	for _, path := range paths {
		path = normalizedMutationPath(path, "")
		if path != "" {
			receipts[path] = receipt
		}
	}
}

func fileReadRequiredToolResult(call coremodel.ToolCall) coremodel.ToolResult {
	message := "edit_file requires a successful read_file of the same text file in this tool loop. Read the target path first, then retry the edit using text copied from that revision. A preceding write_file or successful edit also establishes the required revision receipt."
	state, _ := json.Marshal(map[string]any{"status": "failed", "error_type": "file_read_required"})
	return coremodel.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Content: []coremodel.Content{{
			Type: coremodel.ContentText,
			Text: message,
		}},
		State: state, IsError: true, Retryable: true,
	}
}
