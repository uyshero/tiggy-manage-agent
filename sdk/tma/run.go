package tma

import (
	"context"
	"encoding/json"
)

type RunHandle struct {
	client *Client
	Run    Run
}

func (h *RunHandle) Events(ctx context.Context, afterSeq int64) (*EventStream, error) {
	return h.client.Runs.Events(ctx, h.Run.SessionID, h.Run.ID, afterSeq)
}

func (h *RunHandle) Cancel(ctx context.Context) error {
	run, err := h.client.Runs.Cancel(ctx, h.Run.SessionID, h.Run.ID)
	if err == nil {
		h.Run = run
	}
	return err
}

func (h *RunHandle) Approve(ctx context.Context, callID string, reason string) error {
	_, err := h.client.Interventions.Decide(ctx, h.Run.SessionID, h.Run.ID, callID, "approve", reason)
	return err
}

func (h *RunHandle) Reject(ctx context.Context, callID string, reason string) error {
	_, err := h.client.Interventions.Decide(ctx, h.Run.SessionID, h.Run.ID, callID, "reject", reason)
	return err
}

func (h *RunHandle) Wait(ctx context.Context) (RunResult, error) {
	stream, err := h.Events(ctx, h.Run.UserEventSeq-1)
	if err != nil {
		return RunResult{}, err
	}
	defer stream.Close()
	var lastEvent *Event
	var output json.RawMessage
	for {
		event, err := stream.Next(ctx)
		if err != nil {
			return RunResult{}, err
		}
		if event.EffectiveTurnID() != h.Run.ID {
			continue
		}
		copy := event
		lastEvent = &copy
		if event.Type == "agent.message" {
			output = append(json.RawMessage(nil), event.Payload...)
		}
		if event.Type != "session.status_idle" {
			continue
		}
		run, err := h.client.Runs.Get(ctx, h.Run.SessionID, h.Run.ID)
		if err != nil {
			return RunResult{}, err
		}
		h.Run = run
		if isTerminalRunStatus(run.Status) {
			return RunResult{Run: run, LastEvent: lastEvent, Output: output}, nil
		}
	}
}

func isTerminalRunStatus(status string) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusInterrupted
}
