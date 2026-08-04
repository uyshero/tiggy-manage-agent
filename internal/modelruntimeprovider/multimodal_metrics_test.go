package modelruntimeprovider

import "testing"

func TestMultimodalMetricsAggregateMediaWithoutContent(t *testing.T) {
	metrics := newMultimodalMetrics([]MultimodalTrack{
		{ID: "microphone", Kind: MultimodalMediaAudio, Codec: "pcm_s16le", SampleRateHz: 16000, Channels: 1},
		{ID: "camera", Kind: MultimodalMediaVideo, Delivery: MultimodalDeliveryLatest},
	})
	metrics.setOutputTracks([]MultimodalTrack{
		{ID: "speaker", Kind: MultimodalMediaAudio, Codec: "pcm_s16le", SampleRateHz: 24000, Channels: 1},
		{ID: "preview", Kind: MultimodalMediaVideo, Delivery: MultimodalDeliveryReliable},
	})

	metrics.observeInputFrame(MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "microphone", Sequence: 1, Payload: make([]byte, 320)})
	metrics.observeInputFrame(MultimodalMediaFrame{Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: 1, TimestampMicros: 1000, Payload: []byte("v1")})
	metrics.observeInputFrame(MultimodalMediaFrame{Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: 4, TimestampMicros: 34000, Payload: []byte("v2")})
	metrics.observeOutputFrame(MultimodalMediaFrame{Kind: MultimodalMediaAudio, TrackID: "speaker", Sequence: 1, Payload: make([]byte, 480)})
	metrics.observeOutputFrame(MultimodalMediaFrame{Kind: MultimodalMediaVideo, TrackID: "preview", Sequence: 1, TimestampMicros: 2000, Payload: []byte("o1")})
	metrics.observeOutputFrame(MultimodalMediaFrame{Kind: MultimodalMediaVideo, TrackID: "preview", Sequence: 3, TimestampMicros: 52000, Payload: []byte("o2")})

	if metrics.InputItems != 3 || metrics.InputBytes != 324 || metrics.InputAudioMillis != 10 ||
		metrics.InputVideoFrames != 2 || metrics.InputVideoDropped != 2 || metrics.InputVideoMillis != 33 {
		t.Fatalf("unexpected input media metrics: %+v", metrics)
	}
	if metrics.OutputItems != 3 || metrics.OutputBytes != 484 || metrics.OutputAudioMillis != 10 ||
		metrics.OutputVideoFrames != 2 || metrics.OutputVideoDropped != 0 || metrics.OutputVideoMillis != 50 {
		t.Fatalf("unexpected output media metrics: %+v", metrics)
	}
}

func TestMultimodalMetricsSaturatesUntrustedSequenceGap(t *testing.T) {
	metrics := newMultimodalMetrics([]MultimodalTrack{{ID: "camera", Kind: MultimodalMediaVideo, Delivery: MultimodalDeliveryLatest}})
	metrics.observeInputFrame(MultimodalMediaFrame{Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: 1, Payload: []byte("v1")})
	metrics.observeInputFrame(MultimodalMediaFrame{Kind: MultimodalMediaVideo, TrackID: "camera", Sequence: ^uint64(0), TimestampMicros: 1, Payload: []byte("v2")})
	if metrics.InputVideoDropped != int64(^uint64(0)>>1) || metrics.InputVideoDropped < 0 {
		t.Fatalf("unexpected saturated dropped frame count: %d", metrics.InputVideoDropped)
	}
}
