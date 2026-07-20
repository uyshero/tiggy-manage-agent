package runner

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const LiveEventLLMText = "llm.text"

var ErrLiveEventsUnavailable = errors.New("live event stream unavailable")

// LiveEvent is best-effort presentation data. It is intentionally separate from
// managedagents.Event because it has no durable sequence or replay guarantee.
type LiveEvent struct {
	StreamSeq     uint64    `json:"stream_seq"`
	SessionID     string    `json:"session_id"`
	TurnID        string    `json:"turn_id"`
	Type          string    `json:"type"`
	Index         int       `json:"index,omitempty"`
	ToolRound     int       `json:"tool_round,omitempty"`
	Operation     string    `json:"operation"`
	ContentFormat string    `json:"content_format"`
	Text          string    `json:"text"`
	CreatedAt     time.Time `json:"created_at"`
}

type LiveEventSource interface {
	SubscribeLiveEvents(sessionID string) (<-chan LiveEvent, func(), error)
}

// LiveEventBroker keeps a bounded channel per subscriber. Slow consumers may
// miss transient text and must converge on the final durable agent.message.
type LiveEventBroker struct {
	buffer      int
	sequence    atomic.Uint64
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[string]map[uint64]chan LiveEvent
}

func NewLiveEventBroker(buffer int) *LiveEventBroker {
	if buffer <= 0 {
		buffer = 256
	}
	return &LiveEventBroker{buffer: buffer, subscribers: make(map[string]map[uint64]chan LiveEvent)}
}

func (broker *LiveEventBroker) Publish(event LiveEvent) {
	if broker == nil || event.SessionID == "" || event.TurnID == "" || event.Type == "" || event.Text == "" {
		return
	}
	event.StreamSeq = broker.sequence.Add(1)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	broker.mu.RLock()
	defer broker.mu.RUnlock()
	for _, subscriber := range broker.subscribers[event.SessionID] {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (broker *LiveEventBroker) SubscribeLiveEvents(sessionID string) (<-chan LiveEvent, func(), error) {
	if broker == nil {
		return nil, nil, ErrLiveEventsUnavailable
	}
	if sessionID == "" {
		return nil, nil, errors.New("live event session_id is required")
	}

	broker.mu.Lock()
	broker.nextID++
	id := broker.nextID
	channel := make(chan LiveEvent, broker.buffer)
	if broker.subscribers[sessionID] == nil {
		broker.subscribers[sessionID] = make(map[uint64]chan LiveEvent)
	}
	broker.subscribers[sessionID][id] = channel
	broker.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			broker.mu.Lock()
			delete(broker.subscribers[sessionID], id)
			if len(broker.subscribers[sessionID]) == 0 {
				delete(broker.subscribers, sessionID)
			}
			broker.mu.Unlock()
		})
	}
	return channel, cancel, nil
}
