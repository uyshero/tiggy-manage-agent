package modelruntimeprovider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	streamProtocolNDJSON    = "ndjson"
	streamProtocolWebSocket = "websocket"

	streamDirectionClientToRuntime = "client_to_runtime"
	streamDirectionRuntimeToClient = "runtime_to_client"

	defaultBackpressureThreshold = 100 * time.Millisecond
)

type streamCounters struct {
	active             atomic.Int64
	events             atomic.Int64
	backpressureEvents atomic.Int64
	backpressureNanos  atomic.Int64
}

type RuntimeMetrics struct {
	backpressureThreshold time.Duration
	authAccepted          atomic.Int64
	authRejected          atomic.Int64
	ndjson                streamCounters
	websocketInbound      streamCounters
	websocketOutbound     streamCounters
}

type runtimeMetricsContextKey struct{}

func withRuntimeMetrics(ctx context.Context, metrics *RuntimeMetrics) context.Context {
	return context.WithValue(ctx, runtimeMetricsContextKey{}, metrics)
}

func runtimeMetricsFromContext(ctx context.Context) *RuntimeMetrics {
	metrics, _ := ctx.Value(runtimeMetricsContextKey{}).(*RuntimeMetrics)
	return metrics
}

func NewRuntimeMetrics(backpressureThreshold time.Duration) *RuntimeMetrics {
	if backpressureThreshold <= 0 {
		backpressureThreshold = defaultBackpressureThreshold
	}
	return &RuntimeMetrics{backpressureThreshold: backpressureThreshold}
}

func (m *RuntimeMetrics) observeAuthentication(accepted bool) {
	if accepted {
		m.authAccepted.Add(1)
		return
	}
	m.authRejected.Add(1)
}

func (m *RuntimeMetrics) streamStarted(protocol string) {
	switch protocol {
	case streamProtocolNDJSON:
		m.ndjson.active.Add(1)
	case streamProtocolWebSocket:
		m.websocketInbound.active.Add(1)
		m.websocketOutbound.active.Add(1)
	}
}

func (m *RuntimeMetrics) streamFinished(protocol string) {
	switch protocol {
	case streamProtocolNDJSON:
		m.ndjson.active.Add(-1)
	case streamProtocolWebSocket:
		m.websocketInbound.active.Add(-1)
		m.websocketOutbound.active.Add(-1)
	}
}

func (m *RuntimeMetrics) observeStreamEvent(protocol, direction string, duration time.Duration) {
	counters := m.counters(protocol, direction)
	if counters == nil {
		return
	}
	counters.events.Add(1)
	if duration >= m.backpressureThreshold {
		counters.backpressureEvents.Add(1)
		counters.backpressureNanos.Add(duration.Nanoseconds())
	}
}

func (m *RuntimeMetrics) counters(protocol, direction string) *streamCounters {
	switch protocol {
	case streamProtocolNDJSON:
		return &m.ndjson
	case streamProtocolWebSocket:
		if direction == streamDirectionClientToRuntime {
			return &m.websocketInbound
		}
		if direction == streamDirectionRuntimeToClient {
			return &m.websocketOutbound
		}
	}
	return nil
}

func (m *RuntimeMetrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var builder strings.Builder
	builder.WriteString("# HELP tma_model_runtime_authentication_total Internal model runtime authentication decisions.\n")
	builder.WriteString("# TYPE tma_model_runtime_authentication_total counter\n")
	fmt.Fprintf(&builder, "tma_model_runtime_authentication_total{outcome=\"accepted\"} %d\n", m.authAccepted.Load())
	fmt.Fprintf(&builder, "tma_model_runtime_authentication_total{outcome=\"rejected\"} %d\n", m.authRejected.Load())
	builder.WriteString("# HELP tma_model_runtime_streams_active Active internal model runtime streams.\n")
	builder.WriteString("# TYPE tma_model_runtime_streams_active gauge\n")
	fmt.Fprintf(&builder, "tma_model_runtime_streams_active{protocol=\"ndjson\"} %d\n", m.ndjson.active.Load())
	fmt.Fprintf(&builder, "tma_model_runtime_streams_active{protocol=\"websocket\"} %d\n", m.websocketInbound.active.Load())
	builder.WriteString("# HELP tma_model_runtime_stream_events_total Stream events or frames forwarded by bounded protocol and direction.\n")
	builder.WriteString("# TYPE tma_model_runtime_stream_events_total counter\n")
	writeRuntimeStreamMetric(&builder, "tma_model_runtime_stream_events_total", streamProtocolNDJSON, streamDirectionRuntimeToClient, m.ndjson.events.Load())
	writeRuntimeStreamMetric(&builder, "tma_model_runtime_stream_events_total", streamProtocolWebSocket, streamDirectionClientToRuntime, m.websocketInbound.events.Load())
	writeRuntimeStreamMetric(&builder, "tma_model_runtime_stream_events_total", streamProtocolWebSocket, streamDirectionRuntimeToClient, m.websocketOutbound.events.Load())
	builder.WriteString("# HELP tma_model_runtime_stream_backpressure_events_total Stream forwarding operations that exceeded the configured write threshold.\n")
	builder.WriteString("# TYPE tma_model_runtime_stream_backpressure_events_total counter\n")
	writeRuntimeStreamMetric(&builder, "tma_model_runtime_stream_backpressure_events_total", streamProtocolNDJSON, streamDirectionRuntimeToClient, m.ndjson.backpressureEvents.Load())
	writeRuntimeStreamMetric(&builder, "tma_model_runtime_stream_backpressure_events_total", streamProtocolWebSocket, streamDirectionClientToRuntime, m.websocketInbound.backpressureEvents.Load())
	writeRuntimeStreamMetric(&builder, "tma_model_runtime_stream_backpressure_events_total", streamProtocolWebSocket, streamDirectionRuntimeToClient, m.websocketOutbound.backpressureEvents.Load())
	builder.WriteString("# HELP tma_model_runtime_stream_backpressure_seconds_total Cumulative duration of stream forwarding operations that exceeded the configured threshold.\n")
	builder.WriteString("# TYPE tma_model_runtime_stream_backpressure_seconds_total counter\n")
	writeRuntimeStreamFloatMetric(&builder, "tma_model_runtime_stream_backpressure_seconds_total", streamProtocolNDJSON, streamDirectionRuntimeToClient, m.ndjson.backpressureNanos.Load())
	writeRuntimeStreamFloatMetric(&builder, "tma_model_runtime_stream_backpressure_seconds_total", streamProtocolWebSocket, streamDirectionClientToRuntime, m.websocketInbound.backpressureNanos.Load())
	writeRuntimeStreamFloatMetric(&builder, "tma_model_runtime_stream_backpressure_seconds_total", streamProtocolWebSocket, streamDirectionRuntimeToClient, m.websocketOutbound.backpressureNanos.Load())
	_, _ = w.Write([]byte(builder.String()))
}

func writeRuntimeStreamMetric(builder *strings.Builder, name, protocol, direction string, value int64) {
	fmt.Fprintf(builder, "%s{direction=\"%s\",protocol=\"%s\"} %d\n", name, direction, protocol, value)
}

func writeRuntimeStreamFloatMetric(builder *strings.Builder, name, protocol, direction string, nanoseconds int64) {
	fmt.Fprintf(builder, "%s{direction=\"%s\",protocol=\"%s\"} %.6f\n", name, direction, protocol, float64(nanoseconds)/float64(time.Second))
}
