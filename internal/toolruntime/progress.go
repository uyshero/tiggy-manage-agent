package toolruntime

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

const maxToolProgressMessageRunes = 2000

func scopedToolProgressSink(upstream tools.ToolProgressSink, call coremodel.ToolCall, toolRound int, environment map[string]string) tools.ToolProgressSink {
	if upstream == nil {
		return nil
	}
	var sequence atomic.Int64
	return func(ctx context.Context, progress tools.ToolProgress) {
		progress.CallID = call.ID
		progress.Tool = call.Name
		progress.Index = int(sequence.Add(1))
		progress.ToolRound = toolRound
		progress.Stage = strings.TrimSpace(progress.Stage)
		if progress.Stage == "" {
			progress.Stage = "running"
		}
		progress.Message = tools.RedactEnvironmentText(strings.TrimSpace(progress.Message), environment)
		progress.Message = truncateToolProgressMessage(progress.Message)
		if progress.Message == "" {
			return
		}
		if progress.Percent < 0 {
			progress.Percent = 0
		} else if progress.Percent > 100 {
			progress.Percent = 100
		}
		if len(progress.Data) > 0 {
			progress.Data = tools.RedactEnvironmentJSON(append(json.RawMessage(nil), progress.Data...), environment)
		}
		upstream(ctx, progress)
	}
}

func truncateToolProgressMessage(value string) string {
	if utf8.RuneCountInString(value) <= maxToolProgressMessageRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxToolProgressMessageRunes])
}
