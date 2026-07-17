package observability

import (
	"sort"
	"strings"
	"sync"

	"tiggy-manage-agent/internal/managedagents"
)

type CompletionValidationMetric struct {
	Outcome   string
	Validator string
	Count     int64
}

var completionValidationCounters = struct {
	sync.Mutex
	counts map[string]int64
}{counts: map[string]int64{}}

func RecordCompletionValidation(eventType string, validator string) {
	outcome := completionMetricOutcome(eventType)
	if outcome == "" {
		return
	}
	validator = completionMetricValidator(validator)
	completionValidationCounters.Lock()
	completionValidationCounters.counts[outcome+"\x00"+validator]++
	completionValidationCounters.Unlock()
}

func CompletionValidationMetricsSnapshot() []CompletionValidationMetric {
	completionValidationCounters.Lock()
	defer completionValidationCounters.Unlock()
	metrics := make([]CompletionValidationMetric, 0, len(completionValidationCounters.counts))
	for key, count := range completionValidationCounters.counts {
		parts := strings.SplitN(key, "\x00", 2)
		metrics = append(metrics, CompletionValidationMetric{Outcome: parts[0], Validator: parts[1], Count: count})
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Outcome+"\x00"+metrics[i].Validator < metrics[j].Outcome+"\x00"+metrics[j].Validator
	})
	return metrics
}

func completionMetricOutcome(eventType string) string {
	switch eventType {
	case managedagents.EventRuntimeCompletionValidated:
		return "pass"
	case managedagents.EventRuntimeCompletionBlocked:
		return "retry"
	case managedagents.EventRuntimeCompletionFailed:
		return "fail"
	default:
		return ""
	}
}
