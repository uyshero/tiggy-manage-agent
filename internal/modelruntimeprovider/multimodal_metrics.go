package modelruntimeprovider

import (
	"math"
	"strings"
)

type multimodalMediaMetrics struct {
	tracks          map[string]MultimodalTrack
	usage           map[string]*multimodalTrackMetrics
	audioMillis     int64
	videoFrames     int64
	videoDropped    int64
	videoSpanMicros int64
}

type multimodalTrackMetrics struct {
	audioBytes           int64
	lastSequence         uint64
	firstTimestampMicros int64
	lastTimestampMicros  int64
	seen                 bool
}

func newMultimodalMetrics(inputTracks []MultimodalTrack) MultimodalMetrics {
	return MultimodalMetrics{inputMedia: newMultimodalMediaMetrics(inputTracks)}
}

func newMultimodalMediaMetrics(tracks []MultimodalTrack) *multimodalMediaMetrics {
	return &multimodalMediaMetrics{tracks: multimodalTrackMap(tracks), usage: make(map[string]*multimodalTrackMetrics)}
}

func (metrics *MultimodalMetrics) setOutputTracks(tracks []MultimodalTrack) {
	if metrics != nil {
		metrics.outputMedia = newMultimodalMediaMetrics(tracks)
	}
}

func (metrics *MultimodalMetrics) observeInputFrame(frame MultimodalMediaFrame) {
	if metrics == nil {
		return
	}
	metrics.InputItems++
	metrics.InputBytes += int64(len(frame.Payload))
	audioMillis, videoFrames, videoDropped, videoMillis := metrics.inputMedia.observeFrame(frame)
	metrics.InputAudioMillis = audioMillis
	metrics.InputVideoFrames = videoFrames
	metrics.InputVideoDropped = videoDropped
	metrics.InputVideoMillis = videoMillis
}

func (metrics *MultimodalMetrics) observeOutputFrame(frame MultimodalMediaFrame) {
	if metrics == nil {
		return
	}
	metrics.OutputItems++
	metrics.OutputBytes += int64(len(frame.Payload))
	audioMillis, videoFrames, videoDropped, videoMillis := metrics.outputMedia.observeFrame(frame)
	metrics.OutputAudioMillis = audioMillis
	metrics.OutputVideoFrames = videoFrames
	metrics.OutputVideoDropped = videoDropped
	metrics.OutputVideoMillis = videoMillis
}

func (metrics *multimodalMediaMetrics) observeFrame(frame MultimodalMediaFrame) (audioMillis, videoFrames, videoDropped, videoMillis int64) {
	if metrics == nil {
		return 0, 0, 0, 0
	}
	track, ok := metrics.tracks[frame.TrackID]
	if !ok || track.Kind != frame.Kind {
		return metrics.audioMillis, metrics.videoFrames, metrics.videoDropped, metrics.videoSpanMicros / 1000
	}
	usage := metrics.usage[frame.TrackID]
	if usage == nil {
		usage = &multimodalTrackMetrics{}
		metrics.usage[frame.TrackID] = usage
	}
	switch track.Kind {
	case MultimodalMediaAudio:
		bytesPerSecond := multimodalAudioBytesPerSecond(track)
		if bytesPerSecond > 0 {
			previousMillis := multimodalAudioMillis(usage.audioBytes, bytesPerSecond)
			usage.audioBytes = saturatingAddInt64(usage.audioBytes, int64(len(frame.Payload)))
			metrics.audioMillis = saturatingAddInt64(metrics.audioMillis, multimodalAudioMillis(usage.audioBytes, bytesPerSecond)-previousMillis)
		}
	case MultimodalMediaVideo:
		metrics.videoFrames = saturatingAddInt64(metrics.videoFrames, 1)
		if usage.seen && track.Delivery == MultimodalDeliveryLatest && frame.Sequence > usage.lastSequence && frame.Sequence-usage.lastSequence > 1 {
			gap := frame.Sequence - usage.lastSequence - 1
			if gap > math.MaxInt64 {
				metrics.videoDropped = math.MaxInt64
			} else {
				metrics.videoDropped = saturatingAddInt64(metrics.videoDropped, int64(gap))
			}
		}
		previousSpan := int64(0)
		if usage.seen {
			previousSpan = usage.lastTimestampMicros - usage.firstTimestampMicros
			if frame.TimestampMicros < usage.firstTimestampMicros {
				usage.firstTimestampMicros = frame.TimestampMicros
			}
			if frame.TimestampMicros > usage.lastTimestampMicros {
				usage.lastTimestampMicros = frame.TimestampMicros
			}
		} else {
			usage.firstTimestampMicros = frame.TimestampMicros
			usage.lastTimestampMicros = frame.TimestampMicros
		}
		metrics.videoSpanMicros = saturatingAddInt64(metrics.videoSpanMicros, usage.lastTimestampMicros-usage.firstTimestampMicros-previousSpan)
	}
	usage.lastSequence = frame.Sequence
	usage.seen = true
	return metrics.audioMillis, metrics.videoFrames, metrics.videoDropped, metrics.videoSpanMicros / 1000
}

func multimodalAudioBytesPerSecond(track MultimodalTrack) int64 {
	if !strings.EqualFold(strings.TrimSpace(track.Codec), "pcm_s16le") || track.SampleRateHz < 1 || track.Channels < 1 {
		return 0
	}
	return int64(track.SampleRateHz) * int64(track.Channels) * 2
}

func multimodalAudioMillis(sizeBytes, bytesPerSecond int64) int64 {
	if sizeBytes <= 0 || bytesPerSecond <= 0 {
		return 0
	}
	seconds := sizeBytes / bytesPerSecond
	if seconds > math.MaxInt64/1000 {
		return math.MaxInt64
	}
	return saturatingAddInt64(seconds*1000, sizeBytes%bytesPerSecond*1000/bytesPerSecond)
}

func saturatingAddInt64(left, right int64) int64 {
	if right > 0 && left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
