package managedagents

import "sync"

type eventHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan struct{}]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subscribers: make(map[string]map[chan struct{}]struct{})}
}

func (h *eventHub) subscribe(sessionID string) (<-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan struct{}, 1)
	if h.subscribers[sessionID] == nil {
		h.subscribers[sessionID] = make(map[chan struct{}]struct{})
	}
	h.subscribers[sessionID][ch] = struct{}{}

	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		if _, ok := h.subscribers[sessionID][ch]; !ok {
			return
		}
		delete(h.subscribers[sessionID], ch)
		if len(h.subscribers[sessionID]) == 0 {
			delete(h.subscribers, sessionID)
		}
		close(ch)
	}

	return ch, cancel
}

func (h *eventHub) publish(event Event) {
	h.notify(event.SessionID)
}

func (h *eventHub) notify(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.subscribers[sessionID] {
		select {
		case ch <- struct{}{}:
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
