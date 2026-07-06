package managedagents

import "sync"

type eventHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan Event]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subscribers: make(map[string]map[chan Event]struct{})}
}

func (h *eventHub) subscribe(sessionID string) (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan Event, 16)
	if h.subscribers[sessionID] == nil {
		h.subscribers[sessionID] = make(map[chan Event]struct{})
	}
	h.subscribers[sessionID][ch] = struct{}{}

	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		delete(h.subscribers[sessionID], ch)
		if len(h.subscribers[sessionID]) == 0 {
			delete(h.subscribers, sessionID)
		}
		close(ch)
	}

	return ch, cancel
}

func (h *eventHub) publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.subscribers[event.SessionID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (h *eventHub) closeSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.subscribers[sessionID] {
		close(ch)
	}
	delete(h.subscribers, sessionID)
}
