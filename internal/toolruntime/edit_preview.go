package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

type durableEditPreview struct {
	capability.EditFilePreview
	CallSHA256 string `json:"call_sha256"`
}

func (r ToolRuntime) buildDurableEditPreview(ctx context.Context, call coremodel.ToolCall, receipt persistedFileReceipt) (durableEditPreview, bool) {
	preview, err := r.previewEditCall(ctx, call, receipt)
	if err != nil || !preview.Success {
		return durableEditPreview{}, false
	}
	bound := durableEditPreview{EditFilePreview: preview, CallSHA256: editCallSHA256(call)}
	if validateDurableEditPreview(bound, call, receipt) != nil {
		return durableEditPreview{}, false
	}
	return bound, true
}

func (r ToolRuntime) previewEditCall(ctx context.Context, call coremodel.ToolCall, receipt persistedFileReceipt) (capability.EditFilePreview, error) {
	normalized := tools.NormalizeCall(tools.Call{Name: call.Name, Arguments: call.Arguments})
	if normalized.Identifier != tools.DefaultIdentifier || normalized.APIName != "edit_file" {
		return capability.EditFilePreview{}, errors.New("edit preview requires default.edit_file")
	}
	previewer, ok := r.ExecutionContext.Provider.(capability.EditPreviewProvider)
	if !ok || previewer == nil {
		return capability.EditFilePreview{}, errors.New("capability provider does not support edit preview")
	}
	var request capability.EditFileRequest
	if err := json.Unmarshal(call.Arguments, &request); err != nil {
		return capability.EditFilePreview{}, fmt.Errorf("decode edit preview arguments: %w", err)
	}
	request.ExpectedRevision = receipt.revision
	request.ExpectedContentSHA256 = receipt.contentSHA256
	request.Meta = capability.NewRequestMeta(r.ExecutionContext.SessionID, r.ExecutionContext.TurnID, r.ExecutionContext.Deadline)
	return previewer.PreviewEditFile(ctx, request)
}

func editCallSHA256(call coremodel.ToolCall) string {
	sum := sha256.Sum256(append(append([]byte(call.Name), 0), call.Arguments...))
	return hex.EncodeToString(sum[:])
}

func validateDurableEditPreview(preview durableEditPreview, call coremodel.ToolCall, receipt persistedFileReceipt) error {
	if !preview.Success || strings.TrimSpace(preview.Path) == "" || strings.TrimSpace(preview.UnifiedDiff) == "" {
		return errors.New("edit preview is incomplete")
	}
	if preview.CallSHA256 != editCallSHA256(call) {
		return errors.New("edit preview call hash does not match")
	}
	if preview.BaseRevision != receipt.revision || !strings.EqualFold(preview.BaseContentSHA256, receipt.contentSHA256) {
		return errors.New("edit preview base receipt does not match")
	}
	if preview.PatchSHA256 != toolsSHA256(preview.UnifiedDiff) {
		return errors.New("edit preview patch hash does not match")
	}
	return nil
}

func toolsSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func approvedDurableEditPreview(interactions []agentcore.RequiredInteraction, call coremodel.ToolCall, receipt persistedFileReceipt) (durableEditPreview, bool) {
	for _, interaction := range interactions {
		if interaction.CallID != call.ID || interaction.Kind != managedagents.InterventionKindToolApproval ||
			interaction.Decision == nil || interaction.Decision.Status != managedagents.InterventionStatusApproved {
			continue
		}
		var request struct {
			Preview *durableEditPreview `json:"edit_preview"`
		}
		if json.Unmarshal(interaction.Request, &request) != nil || request.Preview == nil {
			return durableEditPreview{}, false
		}
		if validateDurableEditPreview(*request.Preview, call, receipt) != nil {
			return durableEditPreview{}, false
		}
		return *request.Preview, true
	}
	return durableEditPreview{}, false
}

func sameEditPreview(left durableEditPreview, right capability.EditFilePreview) bool {
	return left.Success && right.Success &&
		left.Path == right.Path && left.BaseRevision == right.BaseRevision &&
		strings.EqualFold(left.BaseContentSHA256, right.BaseContentSHA256) &&
		left.UnifiedDiff == right.UnifiedDiff && left.PatchSHA256 == right.PatchSHA256 &&
		left.LinesAdded == right.LinesAdded && left.LinesDeleted == right.LinesDeleted &&
		left.Replacements == right.Replacements
}

func editPreviewFailureToolResult(call coremodel.ToolCall, preview capability.EditFilePreview, previewErr error) coremodel.ToolResult {
	code := strings.TrimSpace(preview.Code)
	message := strings.TrimSpace(preview.Error)
	if previewErr != nil {
		code = "edit_preview_failed"
		message = previewErr.Error()
	}
	if code == "" {
		code = "edit_preview_failed"
	}
	if message == "" {
		message = "The edit could not be previewed and was not executed."
	}
	state, _ := json.Marshal(map[string]any{
		"status": "failed", "error_type": code, "edit_preview": preview,
	})
	return coremodel.ToolResult{
		CallID: call.ID, Name: call.Name,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: message}},
		State:   state, IsError: true, Retryable: true,
	}
}

func staleEditPreviewToolResult(call coremodel.ToolCall) coremodel.ToolResult {
	message := "The file or approved Edit diff changed after approval. Read the file again and submit a new edit for approval. No content was written."
	state, _ := json.Marshal(map[string]any{"status": "failed", "error_type": "stale_edit_preview"})
	return coremodel.ToolResult{
		CallID: call.ID, Name: call.Name,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: message}},
		State:   state, IsError: true, Retryable: true,
	}
}
