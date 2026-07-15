package tma

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type EventStream struct {
	client *Client
	path   string
	cursor int64

	ctx      context.Context
	cancel   context.CancelFunc
	response *http.Response
	scanner  *bufio.Scanner
	backoff  time.Duration
}

func newEventStream(parent context.Context, client *Client, path string, afterSeq int64) *EventStream {
	ctx, cancel := context.WithCancel(parent)
	return &EventStream{client: client, path: path, cursor: afterSeq, ctx: ctx, cancel: cancel, backoff: 250 * time.Millisecond}
}

func (s *EventStream) Close() error {
	if s == nil {
		return nil
	}
	s.cancel()
	if s.response != nil {
		return s.response.Body.Close()
	}
	return nil
}

func (s *EventStream) Next(ctx context.Context) (Event, error) {
	if s == nil || s.client == nil {
		return Event{}, errors.New("tma: event stream is not initialized")
	}
	for {
		if err := s.ctx.Err(); err != nil {
			return Event{}, err
		}
		if err := ctx.Err(); err != nil {
			return Event{}, err
		}
		if s.scanner == nil {
			if err := s.open(); err != nil {
				if !retryableStreamError(err) {
					return Event{}, err
				}
				if err := s.waitRetry(ctx); err != nil {
					return Event{}, err
				}
				continue
			}
		}
		event, err := s.readEvent()
		if err == nil {
			if event.Seq <= s.cursor {
				continue
			}
			s.cursor = event.Seq
			s.backoff = 250 * time.Millisecond
			return event, nil
		}
		s.closeResponse()
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !retryableStreamError(err) {
			return Event{}, err
		}
		if err := s.waitRetry(ctx); err != nil {
			return Event{}, err
		}
	}
}

func (s *EventStream) open() error {
	path, err := url.Parse(s.path)
	if err != nil {
		return fmt.Errorf("tma: invalid stream path: %w", err)
	}
	query := path.Query()
	query.Set("after_seq", strconv.FormatInt(s.cursor, 10))
	path.RawQuery = query.Encode()
	response, err := s.client.OpenStream(s.ctx, path.String())
	if err != nil {
		return err
	}
	s.response = response
	s.scanner = bufio.NewScanner(response.Body)
	s.scanner.Buffer(make([]byte, 64*1024), 2<<20)
	return nil
}

func (s *EventStream) readEvent() (Event, error) {
	var data strings.Builder
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			var event Event
			if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
				return Event{}, fmt.Errorf("tma: decode SSE event: %w", err)
			}
			if event.Seq <= 0 || event.Type == "" {
				return Event{}, errors.New("tma: invalid SSE event envelope")
			}
			return event, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(value, " "))
		}
	}
	if err := s.scanner.Err(); err != nil {
		return Event{}, fmt.Errorf("tma: read SSE stream: %w", err)
	}
	return Event{}, io.EOF
}

func (s *EventStream) closeResponse() {
	if s.response != nil {
		_ = s.response.Body.Close()
	}
	s.response = nil
	s.scanner = nil
}

func (s *EventStream) waitRetry(ctx context.Context) error {
	delay := s.backoff
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	if s.backoff < 5*time.Second {
		s.backoff *= 2
		if s.backoff > 5*time.Second {
			s.backoff = 5 * time.Second
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryableStreamError(err error) bool {
	var apiError *APIError
	if errors.As(err, &apiError) {
		return apiError.StatusCode >= http.StatusInternalServerError
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}
