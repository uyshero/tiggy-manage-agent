package modelruntimeprovider

import (
	"fmt"
	"sync"
)

type MultimodalCreditSnapshot struct {
	Bytes  int64
	Frames int64
}

// MultimodalCreditWindow applies the same byte/frame credit rules on both ends
// of a realtime connection. It never waits or queues media on behalf of callers.
type MultimodalCreditWindow struct {
	mu           sync.Mutex
	limits       MultimodalFlowLimits
	bytes        int64
	frames       int64
	lastSequence map[string]uint64
}

func NewMultimodalCreditWindow(limits MultimodalFlowLimits, initial MultimodalFlowCredit) (*MultimodalCreditWindow, error) {
	if err := validateMultimodalFlowLimits(limits); err != nil {
		return nil, err
	}
	if initial.Bytes < 0 || initial.Frames < 0 || initial.Bytes > limits.MaxInFlightBytes || initial.Frames > limits.MaxInFlightFrames {
		return nil, fmt.Errorf("%w: initial credit exceeds negotiated limits", ErrMultimodalFlowControl)
	}
	return &MultimodalCreditWindow{
		limits: limits, bytes: initial.Bytes, frames: initial.Frames, lastSequence: make(map[string]uint64),
	}, nil
}

func (w *MultimodalCreditWindow) ReserveFrame(frame MultimodalMediaFrame) error {
	if err := validateMultimodalMediaFrame(frame); err != nil {
		return err
	}
	return w.Reserve(frame.TrackID, frame.Sequence, int64(len(frame.Payload)))
}

func (w *MultimodalCreditWindow) Reserve(trackID string, sequence uint64, sizeBytes int64) error {
	if w == nil {
		return fmt.Errorf("%w: credit window is unavailable", ErrMultimodalFlowControl)
	}
	if !validMultimodalTrackID(trackID) || sequence == 0 || sizeBytes <= 0 || sizeBytes > w.limits.MaxFrameBytes {
		return fmt.Errorf("%w: invalid reservation", ErrMultimodalFlowControl)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if sequence <= w.lastSequence[trackID] {
		return fmt.Errorf("%w: sequence for track %q must increase", ErrMultimodalSequence, trackID)
	}
	if sizeBytes > w.bytes || w.frames < 1 {
		return fmt.Errorf("%w: insufficient byte or frame credit", ErrMultimodalFlowControl)
	}
	w.bytes -= sizeBytes
	w.frames--
	w.lastSequence[trackID] = sequence
	return nil
}

func (w *MultimodalCreditWindow) Grant(credit MultimodalFlowCredit) error {
	if w == nil {
		return fmt.Errorf("%w: credit window is unavailable", ErrMultimodalFlowControl)
	}
	if credit.Bytes < 0 || credit.Frames < 0 || (credit.Bytes == 0 && credit.Frames == 0) {
		return fmt.Errorf("%w: credit grant must be positive", ErrMultimodalFlowControl)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if credit.Bytes > w.limits.MaxInFlightBytes-w.bytes || credit.Frames > w.limits.MaxInFlightFrames-w.frames {
		return fmt.Errorf("%w: credit grant exceeds negotiated limits", ErrMultimodalFlowControl)
	}
	w.bytes += credit.Bytes
	w.frames += credit.Frames
	return nil
}

func (w *MultimodalCreditWindow) Snapshot() MultimodalCreditSnapshot {
	if w == nil {
		return MultimodalCreditSnapshot{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return MultimodalCreditSnapshot{Bytes: w.bytes, Frames: w.frames}
}

func validateMultimodalFlowLimits(limits MultimodalFlowLimits) error {
	if limits.MaxFrameBytes <= 0 || limits.MaxFrameBytes > MultimodalMaxFrameBytes ||
		limits.MaxInFlightBytes < limits.MaxFrameBytes || limits.MaxInFlightBytes > MultimodalMaxInFlightBytes ||
		limits.MaxInFlightFrames <= 0 || limits.MaxInFlightFrames > MultimodalMaxInFlightFrames {
		return fmt.Errorf("%w: invalid negotiated flow limits", ErrMultimodalFlowControl)
	}
	return nil
}
