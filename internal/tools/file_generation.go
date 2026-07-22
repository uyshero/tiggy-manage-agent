package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"tiggy-manage-agent/internal/capability"
)

const (
	RecommendedFileMutationTokens = 6000
	MaxFileMutationTokens         = 8000
)

var (
	segmentedFilePlaceholderPattern     = regexp.MustCompile(`__TMA_PLACEHOLDER_[A-Za-z0-9][A-Za-z0-9_-]*_[0-9]{3,}__`)
	reservedSegmentedPlaceholderPattern = regexp.MustCompile(`__TMA_PLACEHOLDER_[A-Za-z0-9_-]+__`)
)

// ValidateFileMutationCall keeps generated file payloads below provider output
// limits and enforces the idempotent placeholder protocol for segmented writes.
func ValidateFileMutationCall(call Call) *ExecutionError {
	return ValidateFileMutationCallWithLimits(call, FileMutationLimits{})
}

type FileMutationLimits struct {
	RecommendedTokens int
	MaxTokens         int
}

func (limits FileMutationLimits) normalized() FileMutationLimits {
	if limits.MaxTokens <= 0 || limits.MaxTokens > MaxFileMutationTokens {
		limits.MaxTokens = MaxFileMutationTokens
	}
	if limits.RecommendedTokens <= 0 || limits.RecommendedTokens > limits.MaxTokens {
		limits.RecommendedTokens = min(RecommendedFileMutationTokens, limits.MaxTokens)
	}
	return limits
}

func ValidateFileMutationCallWithLimits(call Call, limits FileMutationLimits) *ExecutionError {
	limits = limits.normalized()
	call = NormalizeCall(call)
	switch normalizeAPIName(call.APIName) {
	case "write_file":
		var request capability.WriteFileRequest
		if json.Unmarshal(call.Arguments, &request) != nil {
			return nil
		}
		content := string(request.Content)
		if estimated := EstimateFileMutationTokens(content); estimated > limits.MaxTokens {
			return &ExecutionError{
				Type:    "file_content_too_large",
				Message: segmentedFileGenerationMessage("write_file.content", estimated, limits),
			}
		}
		if duplicate := duplicateSegmentedPlaceholder(content); duplicate != "" {
			return &ExecutionError{
				Type:    "invalid_segmented_file_skeleton",
				Message: fmt.Sprintf("Segmented file placeholder %q occurs more than once. Every placeholder must be unique so retries remain idempotent.", duplicate),
			}
		}
		if invalid := invalidSegmentedPlaceholder(content); invalid != "" {
			return &ExecutionError{
				Type:    "invalid_segmented_file_skeleton",
				Message: fmt.Sprintf("Segmented file placeholder %q is invalid. Use a unique numbered placeholder ending in at least three digits, for example __TMA_PLACEHOLDER_REPORT_001__.", invalid),
			}
		}
	case "edit_file":
		var request capability.EditFileRequest
		if json.Unmarshal(call.Arguments, &request) != nil {
			return nil
		}
		operations := request.Edits
		if len(operations) == 0 {
			operations = []capability.EditOperation{{OldString: request.OldString, NewString: request.NewString, ReplaceAll: request.ReplaceAll}}
		}
		totalEstimated := 0
		segmentedOperations := 0
		for index, operation := range operations {
			if operation.OldString == operation.NewString {
				return &ExecutionError{Type: "invalid_edit_noop", Message: fmt.Sprintf("edit_file operation %d old_string and new_string must be different.", index)}
			}
			totalEstimated += EstimateFileMutationTokens(operation.NewString)
			if duplicate := duplicateSegmentedPlaceholder(operation.NewString); duplicate != "" {
				return &ExecutionError{
					Type:    "invalid_segmented_file_edit",
					Message: fmt.Sprintf("Replacement introduces duplicate placeholder %q. Split at semantic boundaries and use a unique numbered placeholder for each remaining segment.", duplicate),
				}
			}
			if invalid := invalidSegmentedPlaceholder(operation.NewString); invalid != "" {
				return &ExecutionError{
					Type:    "invalid_segmented_file_edit",
					Message: fmt.Sprintf("Replacement introduces invalid segmented placeholder %q. Use a unique numbered placeholder such as __TMA_PLACEHOLDER_REPORT_001__.", invalid),
				}
			}
			placeholder, segmentedPlaceholder := SegmentedFilePlaceholderToken(operation.OldString)
			if reservedSegmentedPlaceholderPattern.MatchString(operation.OldString) && !segmentedPlaceholder {
				return &ExecutionError{
					Type:    "invalid_segmented_file_edit",
					Message: fmt.Sprintf("edit_file old_string %q uses the reserved TMA placeholder prefix but is not numbered in the required format. Pass exactly one placeholder, optionally surrounded by whitespace, such as __TMA_PLACEHOLDER_REPORT_001__.", operation.OldString),
				}
			}
			if segmentedPlaceholder {
				segmentedOperations++
				if operation.ReplaceAll {
					return &ExecutionError{Type: "invalid_segmented_file_edit", Message: "Segment placeholder replacement must use replace_all=false so exactly one unique placeholder is consumed."}
				}
				if strings.Contains(operation.NewString, placeholder) {
					return &ExecutionError{Type: "invalid_segmented_file_edit", Message: "Segment replacement must consume its old placeholder and must not reinsert the same placeholder."}
				}
			}
		}
		if totalEstimated > limits.MaxTokens {
			return &ExecutionError{
				Type:    "edit_replacement_too_large",
				Message: segmentedFileGenerationMessage("edit_file replacement content", totalEstimated, limits),
			}
		}
		if segmentedOperations > 0 && len(operations) != 1 {
			return &ExecutionError{Type: "invalid_segmented_file_edit", Message: "Segmented generation must replace exactly one numbered placeholder per edit_file call."}
		}
	}
	return nil
}

func ValidateFileMutationBatch(calls []Call) *ExecutionError {
	count := 0
	for _, call := range calls {
		switch normalizeAPIName(NormalizeCall(call).APIName) {
		case "write_file", "edit_file":
			count++
		}
	}
	if count <= 1 {
		return nil
	}
	return &ExecutionError{
		Type:    "multiple_file_mutations",
		Message: "A single model response may contain only one write_file or edit_file call. Regenerate the response with exactly one semantic file mutation; do not retry the same multi-call payload.",
	}
}

func IsRecoverableFileGenerationError(errorType string) bool {
	switch errorType {
	case "file_content_too_large", "edit_replacement_too_large", "invalid_segmented_file_skeleton", "invalid_segmented_file_edit", "invalid_edit_noop", "file_read_required", "multiple_file_mutations":
		return true
	default:
		return false
	}
}

func SegmentedFilePlaceholders(value string) []string {
	matches := segmentedFilePlaceholderPattern.FindAllString(value, -1)
	if len(matches) == 0 {
		return nil
	}
	result := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		if !seen[match] {
			seen[match] = true
			result = append(result, match)
		}
	}
	return result
}

func IsSegmentedFilePlaceholder(value string) bool {
	_, ok := SegmentedFilePlaceholderToken(value)
	return ok
}

// SegmentedFilePlaceholderToken accepts indentation or line breaks around one
// placeholder while returning the canonical token used by runtime state.
func SegmentedFilePlaceholderToken(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	return trimmed, segmentedFilePlaceholderPattern.MatchString(trimmed) && segmentedFilePlaceholderPattern.FindString(trimmed) == trimmed
}

// EstimateFileMutationTokens estimates the serialized JSON string rather than
// the plain text because escaping also consumes model output tokens.
func EstimateFileMutationTokens(value string) int {
	encoded, _ := json.Marshal(value)
	return EstimateSerializedFileMutationTokens(string(encoded))
}

// EstimateSerializedFileMutationTokens handles an in-progress JSON argument
// stream that is already serialized and must not be escaped a second time.
func EstimateSerializedFileMutationTokens(value string) int {
	estimated := estimateSerializedTextTokens(value)
	byteEstimate := (len(value) + 2) / 3
	if byteEstimate > estimated {
		return byteEstimate
	}
	return estimated
}

func estimateSerializedTextTokens(value string) int {
	tokens := 0
	wordRun := 0
	spaceRun := 0
	flush := func() {
		if wordRun > 0 {
			tokens += (wordRun + 3) / 4
			wordRun = 0
		}
		if spaceRun > 0 {
			tokens += (spaceRun + 3) / 4
			spaceRun = 0
		}
	}
	for _, runeValue := range value {
		switch {
		case runeValue <= unicode.MaxASCII && (unicode.IsLetter(runeValue) || unicode.IsDigit(runeValue) || runeValue == '_'):
			if spaceRun > 0 {
				flush()
			}
			wordRun++
		case unicode.IsSpace(runeValue):
			if wordRun > 0 {
				flush()
			}
			spaceRun++
		default:
			flush()
			tokens++
		}
	}
	flush()
	return tokens
}

func contentWithPlaceholderWarning(content string) string {
	placeholders := SegmentedFilePlaceholders(content)
	if len(placeholders) == 0 {
		return content
	}
	return content + fmt.Sprintf(
		"\n\n[Segmented file generation is incomplete: %d placeholder(s) remain. Replace every unique placeholder with edit_file, then read the file again and run the appropriate syntax check or test before finishing.]",
		len(placeholders),
	)
}

func duplicateSegmentedPlaceholder(value string) string {
	seen := map[string]bool{}
	for _, placeholder := range segmentedFilePlaceholderPattern.FindAllString(value, -1) {
		if seen[placeholder] {
			return placeholder
		}
		seen[placeholder] = true
	}
	return ""
}

func invalidSegmentedPlaceholder(value string) string {
	for _, placeholder := range reservedSegmentedPlaceholderPattern.FindAllString(value, -1) {
		if !IsSegmentedFilePlaceholder(placeholder) {
			return placeholder
		}
	}
	return ""
}

func segmentedFileGenerationMessage(field string, estimatedTokens int, limits FileMutationLimits) string {
	return fmt.Sprintf(
		"%s is estimated at %d tokens and exceeds the hard limit of %d. Do not retry the same payload. Use write_file once to create a small file skeleton with unique numbered placeholders such as __TMA_PLACEHOLDER_REPORT_001__, then use edit_file with old_string set to one placeholder and new_string set to one complete semantic segment. Keep each segment at or below %d tokens when possible and always below %d. Split only at function, class, module, chapter, or complete data-structure boundaries. Before finishing, use read_file to confirm no __TMA_PLACEHOLDER_...__ markers remain and run the appropriate syntax check or test.",
		field, estimatedTokens, limits.MaxTokens, limits.RecommendedTokens, limits.MaxTokens,
	)
}
