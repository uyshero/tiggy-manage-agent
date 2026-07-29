package biographyvoice

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"tiggy-manage-agent/sdk/tma"
)

type tmaStreamingInterviewBackend interface {
	RunStreaming(context.Context, string, string, func(string) error) (json.RawMessage, error)
}

type tmaLiveEvent struct {
	TurnID    string `json:"turn_id"`
	Type      string `json:"type"`
	Operation string `json:"operation"`
	Text      string `json:"text"`
}

type tmaLiveResult struct {
	result tma.RunResult
	err    error
}

type tmaLiveRead struct {
	event tmaLiveEvent
	err   error
}

func (backend sdkTMABackend) RunStreaming(ctx context.Context, sessionID string, prompt string, onText func(string) error) (json.RawMessage, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	response, err := backend.client.OpenStream(runCtx, "/v2/sessions/"+url.PathEscape(sessionID)+"/live/stream")
	if err != nil {
		return backend.Run(ctx, sessionID, prompt)
	}
	defer response.Body.Close()

	handle, err := backend.client.Runs.Start(runCtx, sessionID, tma.StartRunRequest{
		Input:          tma.TextInput(prompt),
		IdempotencyKey: newDoubaoID("biography-turn"),
	})
	if err != nil {
		return nil, err
	}
	runFinished := false
	defer func() {
		if runFinished {
			return
		}
		cancelCtx, cancelRun := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelRun()
		_, _ = backend.client.Runs.Cancel(cancelCtx, sessionID, handle.Run.ID)
	}()
	results := make(chan tmaLiveResult, 1)
	go func() {
		result, waitErr := handle.Wait(runCtx)
		results <- tmaLiveResult{result: result, err: waitErr}
	}()
	reads := make(chan tmaLiveRead, 1)
	go readTMALiveEvents(runCtx, response.Body, reads)
	var completed *tmaLiveResult
	var drainTimer *time.Timer
	var drain <-chan time.Time
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-drain:
			runFinished = true
			return completed.result.Output, nil
		case read, open := <-reads:
			if !open {
				reads = nil
				continue
			}
			if read.err != nil {
				reads = nil
				continue
			}
			if read.event.TurnID == handle.Run.ID && read.event.Type == "llm.text" && read.event.Operation == "append" && read.event.Text != "" {
				if err := onText(read.event.Text); err != nil {
					return nil, err
				}
			}
		case result := <-results:
			if result.err != nil {
				return nil, result.err
			}
			if result.result.Run.Status != tma.RunStatusCompleted {
				return nil, fmt.Errorf("TMA interview run ended with status %s", result.result.Run.Status)
			}
			completed = &result
			drainTimer = time.NewTimer(120 * time.Millisecond)
			drain = drainTimer.C
			results = nil
		}
	}
}

func readTMALiveEvents(ctx context.Context, reader io.Reader, output chan<- tmaLiveRead) {
	defer close(output)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			var event tmaLiveEvent
			err := json.Unmarshal([]byte(data.String()), &event)
			data.Reset()
			select {
			case output <- tmaLiveRead{event: event, err: err}:
			case <-ctx.Done():
				return
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case output <- tmaLiveRead{err: err}:
		case <-ctx.Done():
		}
	}
}
