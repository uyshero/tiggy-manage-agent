package skillretention

import "sync"

type RunMetric struct {
	Outcome string
	Count   uint64
}

type ObjectMetric struct {
	Outcome string
	Count   uint64
	Bytes   uint64
}

type MetricsSnapshot struct {
	Runs       []RunMetric
	Objects    []ObjectMetric
	Candidates int64
}

var gcMetrics = struct {
	sync.Mutex
	runs       map[string]uint64
	objects    map[string]ObjectMetric
	candidates int64
}{runs: map[string]uint64{}, objects: map[string]ObjectMetric{}}

func recordCandidates(count int) {
	gcMetrics.Lock()
	gcMetrics.candidates = int64(count)
	gcMetrics.Unlock()
}

func recordRun(outcome string) {
	gcMetrics.Lock()
	gcMetrics.runs[outcome]++
	gcMetrics.Unlock()
}

func recordObject(outcome string, sizeBytes int64) {
	gcMetrics.Lock()
	metric := gcMetrics.objects[outcome]
	metric.Outcome = outcome
	metric.Count++
	if sizeBytes > 0 {
		metric.Bytes += uint64(sizeBytes)
	}
	gcMetrics.objects[outcome] = metric
	gcMetrics.Unlock()
}

func SnapshotMetrics() MetricsSnapshot {
	gcMetrics.Lock()
	defer gcMetrics.Unlock()
	snapshot := MetricsSnapshot{Candidates: gcMetrics.candidates}
	for outcome, count := range gcMetrics.runs {
		snapshot.Runs = append(snapshot.Runs, RunMetric{Outcome: outcome, Count: count})
	}
	for _, metric := range gcMetrics.objects {
		snapshot.Objects = append(snapshot.Objects, metric)
	}
	return snapshot
}
