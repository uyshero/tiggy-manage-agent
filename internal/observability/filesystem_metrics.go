package observability

import (
	"sort"
	"sync"
)

type FilesystemToolMetricInput struct {
	API            string
	Outcome        string
	ErrorCode      string
	DurationMillis int64
	State          map[string]any
}

type FilesystemToolRuntimeMetric struct {
	API                  string
	CallsByOutcome       map[string]int64
	DurationSumMillis    int64
	DurationBucketCounts []int64
	ScannedFiles         int64
	ScannedBytes         int64
	Results              int64
	ReturnedBytes        int64
	Truncated            int64
	BinaryFiles          int64
	RevisionConflicts    int64
	Errors               map[string]int64
}

var filesystemToolRuntimeMetrics = struct {
	sync.Mutex
	byAPI map[string]*FilesystemToolRuntimeMetric
}{byAPI: map[string]*FilesystemToolRuntimeMetric{}}

func RecordFilesystemToolMetric(input FilesystemToolMetricInput) {
	if !isFilesystemToolAPI(input.API) {
		return
	}
	outcome := input.Outcome
	if outcome != "success" && outcome != "error" && outcome != "pending_intervention" {
		outcome = "unknown"
	}
	filesystemToolRuntimeMetrics.Lock()
	defer filesystemToolRuntimeMetrics.Unlock()
	metric := filesystemToolRuntimeMetrics.byAPI[input.API]
	if metric == nil {
		metric = &FilesystemToolRuntimeMetric{
			API: input.API, CallsByOutcome: map[string]int64{},
			DurationBucketCounts: make([]int64, len(filesystemToolDurationBuckets)+1), Errors: map[string]int64{},
		}
		filesystemToolRuntimeMetrics.byAPI[input.API] = metric
	}
	metric.CallsByOutcome[outcome]++
	metric.DurationSumMillis += input.DurationMillis
	bucketed := false
	for index, upperBound := range filesystemToolDurationBuckets {
		if input.DurationMillis <= upperBound {
			for bucket := index; bucket < len(metric.DurationBucketCounts); bucket++ {
				metric.DurationBucketCounts[bucket]++
			}
			bucketed = true
			break
		}
	}
	if !bucketed {
		metric.DurationBucketCounts[len(metric.DurationBucketCounts)-1]++
	}
	state := input.State
	metric.ScannedFiles += mapInt64(state, "scanned_files")
	metric.ScannedBytes += mapInt64(state, "scanned_bytes")
	metric.ReturnedBytes += mapInt64(state, "returned_bytes")
	if files, ok := state["files"].([]any); ok {
		metric.Results += int64(len(files))
	}
	if matches, ok := state["matches"].([]any); ok {
		metric.Results += int64(len(matches))
	}
	if mapBool(state, "truncated") {
		metric.Truncated++
	}
	metric.BinaryFiles += mapInt64(state, "skipped_binary_files")
	if mapBool(state, "binary") {
		metric.BinaryFiles++
	}
	if input.ErrorCode != "" {
		code := boundedFilesystemErrorCode(input.ErrorCode)
		metric.Errors[code]++
		if code == "stale_file_revision" {
			metric.RevisionConflicts++
		}
	}
}

func FilesystemToolMetricsSnapshot() []FilesystemToolRuntimeMetric {
	filesystemToolRuntimeMetrics.Lock()
	defer filesystemToolRuntimeMetrics.Unlock()
	apis := make([]string, 0, len(filesystemToolRuntimeMetrics.byAPI))
	for api := range filesystemToolRuntimeMetrics.byAPI {
		apis = append(apis, api)
	}
	sort.Strings(apis)
	result := make([]FilesystemToolRuntimeMetric, 0, len(apis))
	for _, api := range apis {
		metric := filesystemToolRuntimeMetrics.byAPI[api]
		cloned := *metric
		cloned.CallsByOutcome = cloneInt64Map(metric.CallsByOutcome)
		cloned.DurationBucketCounts = append([]int64(nil), metric.DurationBucketCounts...)
		cloned.Errors = cloneInt64Map(metric.Errors)
		result = append(result, cloned)
	}
	return result
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func resetFilesystemToolMetricsForTest() {
	filesystemToolRuntimeMetrics.Lock()
	defer filesystemToolRuntimeMetrics.Unlock()
	filesystemToolRuntimeMetrics.byAPI = map[string]*FilesystemToolRuntimeMetric{}
}
