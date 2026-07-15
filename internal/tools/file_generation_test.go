package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/capability"
)

func TestValidateFileMutationCallKeepsSmallWriteAndOrdinaryEdit(t *testing.T) {
	write := Call{APIName: "write_file", Arguments: json.RawMessage(`{"path":"note.txt","content":"small file"}`)}
	if validationError := ValidateFileMutationCall(write); validationError != nil {
		t.Fatalf("small write should remain valid: %#v", validationError)
	}
	edit := Call{APIName: "edit_file", Arguments: json.RawMessage(`{"path":"note.txt","old_string":"small","new_string":"updated"}`)}
	if validationError := ValidateFileMutationCall(edit); validationError != nil {
		t.Fatalf("ordinary edit should remain valid: %#v", validationError)
	}
}

func TestValidateFileMutationCallRejectsOversizedPayloadsBeforeExecution(t *testing.T) {
	large := strings.Repeat("complete semantic section with markup <div>value</div>\n", 1200)
	if estimated := EstimateFileMutationTokens(large); estimated <= MaxFileMutationTokens {
		t.Fatalf("test payload must exceed hard limit, estimated=%d", estimated)
	}
	writeArguments, _ := json.Marshal(map[string]string{"path": "report.html", "content": large})
	writeError := ValidateFileMutationCall(Call{APIName: "write_file", Arguments: writeArguments})
	if writeError == nil || writeError.Type != "file_content_too_large" || !strings.Contains(writeError.Message, "__TMA_PLACEHOLDER_REPORT_001__") {
		t.Fatalf("unexpected oversized write validation: %#v", writeError)
	}
	editArguments, _ := json.Marshal(map[string]string{"path": "report.html", "old_string": "__TMA_PLACEHOLDER_REPORT_001__", "new_string": large})
	editError := ValidateFileMutationCall(Call{APIName: "edit_file", Arguments: editArguments})
	if editError == nil || editError.Type != "edit_replacement_too_large" {
		t.Fatalf("unexpected oversized edit validation: %#v", editError)
	}
}

func TestValidateFileMutationCallRequiresUniqueSegmentPlaceholders(t *testing.T) {
	placeholder := "__TMA_PLACEHOLDER_REPORT_001__"
	arguments, _ := json.Marshal(map[string]string{"path": "report.html", "content": placeholder + "\n" + placeholder})
	validationError := ValidateFileMutationCall(Call{APIName: "write_file", Arguments: arguments})
	if validationError == nil || validationError.Type != "invalid_segmented_file_skeleton" {
		t.Fatalf("expected duplicate placeholder rejection, got %#v", validationError)
	}
}

func TestValidateFileMutationCallRejectsUnnumberedReservedPlaceholders(t *testing.T) {
	writeArguments, _ := json.Marshal(map[string]string{
		"path": "report.html", "content": "<main>__TMA_PLACEHOLDER_NEWS_CONTENT__</main>",
	})
	writeError := ValidateFileMutationCall(Call{APIName: "write_file", Arguments: writeArguments})
	if writeError == nil || writeError.Type != "invalid_segmented_file_skeleton" || !strings.Contains(writeError.Message, "_001__") {
		t.Fatalf("expected unnumbered skeleton placeholder rejection, got %#v", writeError)
	}

	editArguments, _ := json.Marshal(map[string]string{
		"path": "report.html", "old_string": "__TMA_PLACEHOLDER_NEWS_CONTENT__", "new_string": "complete section",
	})
	editError := ValidateFileMutationCall(Call{APIName: "edit_file", Arguments: editArguments})
	if editError == nil || editError.Type != "invalid_segmented_file_edit" || !strings.Contains(editError.Message, "not numbered") {
		t.Fatalf("expected unnumbered edit placeholder rejection, got %#v", editError)
	}
}

func TestValidateFileMutationBatchRejectsMultipleMutations(t *testing.T) {
	calls := []Call{
		{APIName: "write_file", Arguments: json.RawMessage(`{"path":"a","content":"a"}`)},
		{APIName: "default.edit_file", Arguments: json.RawMessage(`{"path":"b","old_string":"b","new_string":"c"}`)},
	}
	validationError := ValidateFileMutationBatch(calls)
	if validationError == nil || validationError.Type != "multiple_file_mutations" {
		t.Fatalf("expected multiple mutation rejection, got %#v", validationError)
	}
}

func TestSegmentedWriteWithEditIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.js")
	executor := NewDefaultExecutor()
	executionContext := ExecutionContext{Provider: capability.LocalSystemProvider{}}
	call := func(id string, apiName string, arguments map[string]any) ExecutionResult {
		t.Helper()
		raw, err := json.Marshal(arguments)
		if err != nil {
			t.Fatalf("marshal arguments: %v", err)
		}
		result, err := executor.Execute(context.Background(), Call{ID: id, APIName: apiName, Arguments: raw}, executionContext)
		if err != nil {
			t.Fatalf("execute %s: %v", apiName, err)
		}
		return result
	}

	skeleton := "__TMA_PLACEHOLDER_REPORT_001__\n\n__TMA_PLACEHOLDER_REPORT_002__\n"
	if result := call("write", "write_file", map[string]any{"path": path, "content": skeleton}); result.Error != nil {
		t.Fatalf("write skeleton: %#v", result.Error)
	}
	first := map[string]any{
		"path": path, "old_string": "__TMA_PLACEHOLDER_REPORT_001__",
		"new_string": "function first() {\n  return 1;\n}", "replace_all": false,
	}
	if result := call("edit-1", "edit_file", first); result.Error != nil {
		t.Fatalf("replace first segment: %#v", result.Error)
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	retryResult := call("edit-1-retry", "edit_file", first)
	if retryResult.Error != nil || !strings.Contains(retryResult.Content, "already applied") {
		t.Fatalf("retry must succeed idempotently without duplicating content: %#v", retryResult)
	}
	afterRetry, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRetry) != string(afterFirst) {
		t.Fatalf("retry changed file content:\n%s", afterRetry)
	}
	if result := call("edit-2", "edit_file", map[string]any{
		"path": path, "old_string": "__TMA_PLACEHOLDER_REPORT_002__",
		"new_string": "class Report {\n  render() { return first(); }\n}", "replace_all": false,
	}); result.Error != nil {
		t.Fatalf("replace second segment: %#v", result.Error)
	}
	completed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if placeholders := SegmentedFilePlaceholders(string(completed)); len(placeholders) != 0 {
		t.Fatalf("completed file retains placeholders: %#v", placeholders)
	}
}

func TestReadFileWarnsWhenSegmentPlaceholdersRemain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("# Report\n\n__TMA_PLACEHOLDER_REPORT_001__\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"path": path})
	result, err := NewDefaultExecutor().Execute(context.Background(), Call{APIName: "read_file", Arguments: raw}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "Segmented file generation is incomplete") {
		t.Fatalf("expected remaining-placeholder warning, got %q", result.Content)
	}
}
