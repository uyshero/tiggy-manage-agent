package httpapi

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type testStore struct {
	mu sync.Mutex

	nextAgentID       int64
	nextEnvironmentID int64
	nextSessionID     int64
	nextEventID       int64

	agents       map[string]managedagents.Agent
	environments map[string]managedagents.Environment
	sessions     map[string]managedagents.Session
	events       map[string][]managedagents.Event
	subscribers  map[string]map[chan managedagents.Event]struct{}
}

func newTestStore() *testStore {
	return &testStore{
		agents:       make(map[string]managedagents.Agent),
		environments: make(map[string]managedagents.Environment),
		sessions:     make(map[string]managedagents.Session),
		events:       make(map[string][]managedagents.Event),
		subscribers:  make(map[string]map[chan managedagents.Event]struct{}),
	}
}

func (s *testStore) CreateAgent(input managedagents.CreateAgentInput) (managedagents.Agent, error) {
	if input.Name == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent name is required", managedagents.ErrInvalid)
	}
	if input.Model == "" {
		return managedagents.Agent{}, fmt.Errorf("%w: agent model is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	id := s.nextID("agt", &s.nextAgentID)
	workspaceID := defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID)
	agent := managedagents.Agent{
		ID:             id,
		WorkspaceID:    workspaceID,
		Name:           input.Name,
		CurrentVersion: 1,
		Version: managedagents.AgentVersion{
			Version:   1,
			Model:     input.Model,
			System:    input.System,
			Tools:     cloneRaw(input.Tools),
			Skills:    cloneRaw(input.Skills),
			CreatedAt: now,
		},
		CreatedAt: now,
	}
	s.agents[id] = agent
	return agent, nil
}

func (s *testStore) CreateEnvironment(input managedagents.CreateEnvironmentInput) (managedagents.Environment, error) {
	if input.Name == "" {
		return managedagents.Environment{}, fmt.Errorf("%w: environment name is required", managedagents.ErrInvalid)
	}
	if len(input.Config) == 0 {
		input.Config = json.RawMessage(`{}`)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	id := s.nextID("env", &s.nextEnvironmentID)
	environment := managedagents.Environment{
		ID:          id,
		WorkspaceID: defaultString(input.WorkspaceID, managedagents.DefaultWorkspaceID),
		Name:        input.Name,
		Config:      cloneRaw(input.Config),
		CreatedAt:   now,
	}
	s.environments[id] = environment
	return environment, nil
}

func (s *testStore) CreateSession(input managedagents.CreateSessionInput) (managedagents.Session, error) {
	agentID := input.AgentID
	if agentID == "" {
		agentID = input.Agent
	}
	if agentID == "" {
		return managedagents.Session{}, fmt.Errorf("%w: agent_id is required", managedagents.ErrInvalid)
	}
	if input.EnvironmentID == "" {
		return managedagents.Session{}, fmt.Errorf("%w: environment_id is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[agentID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: agent %s", managedagents.ErrNotFound, agentID)
	}
	environment, ok := s.environments[input.EnvironmentID]
	if !ok {
		return managedagents.Session{}, fmt.Errorf("%w: environment %s", managedagents.ErrNotFound, input.EnvironmentID)
	}

	workspaceID := defaultString(input.WorkspaceID, agent.WorkspaceID)
	if workspaceID != agent.WorkspaceID || workspaceID != environment.WorkspaceID {
		return managedagents.Session{}, fmt.Errorf("%w: workspace mismatch", managedagents.ErrInvalid)
	}

	now := time.Now().UTC()
	id := s.nextID("sesn", &s.nextSessionID)
	session := managedagents.Session{
		ID:            id,
		WorkspaceID:   workspaceID,
		AgentID:       agent.ID,
		AgentVersion:  agent.CurrentVersion,
		EnvironmentID: environment.ID,
		Status:        managedagents.SessionStatusIdle,
		Title:         input.Title,
		CreatedBy:     defaultString(input.CreatedBy, "system"),
		CreatedAt:     now,
	}
	s.sessions[id] = session
	s.appendEventLocked(id, managedagents.EventSessionStatusProvisioning, json.RawMessage(`{"status":"provisioning"}`), now)
	s.appendEventLocked(id, managedagents.EventSessionStatusIdle, json.RawMessage(`{"status":"idle"}`), now)
	return session, nil
}

func (s *testStore) GetSession(id string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	return session, nil
}

func (s *testStore) ArchiveSession(id string) (managedagents.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return managedagents.Session{}, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return session, nil
	}

	now := time.Now().UTC()
	session.Status = managedagents.SessionStatusTerminated
	session.ArchivedAt = &now
	s.sessions[id] = session

	event := s.appendEventLocked(id, managedagents.EventSessionStatusTerminated, json.RawMessage(`{"status":"terminated"}`), now)
	s.publishLocked(event)
	return session, nil
}

func (s *testStore) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[id]; !ok {
		return managedagents.ErrNotFound
	}
	delete(s.sessions, id)
	delete(s.events, id)
	s.closeSessionLocked(id)
	return nil
}

func (s *testStore) AppendEvents(sessionID string, inputs []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: at least one event is required", managedagents.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}

	now := time.Now().UTC()
	events := make([]managedagents.Event, 0, len(inputs))
	for _, input := range inputs {
		if input.Type == "" {
			return nil, fmt.Errorf("%w: event type is required", managedagents.ErrInvalid)
		}
		newEvents, err := s.applyEventLocked(&session, input, now)
		if err != nil {
			return nil, err
		}
		events = append(events, newEvents...)
	}

	s.sessions[sessionID] = session
	return events, nil
}

func (s *testStore) CompleteSessionTurn(sessionID string, turnID string, agentPayload json.RawMessage) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	currentTurnID := s.currentTurnIDLocked(sessionID)
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	now := time.Now().UTC()
	agentEvent := s.appendEventLocked(session.ID, managedagents.EventAgentMessage, payloadWithTurnID(agentPayload, turnID), now)
	session.Status = managedagents.SessionStatusIdle
	idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, statusPayload("idle", turnID), now)
	s.sessions[sessionID] = session

	s.publishLocked(agentEvent)
	s.publishLocked(idleEvent)
	return []managedagents.Event{agentEvent, idleEvent}, nil
}

func (s *testStore) AppendRuntimeEvent(sessionID string, turnID string, input managedagents.AppendEventInput) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if input.Type == "" {
		return nil, fmt.Errorf("%w: event type is required", managedagents.ErrInvalid)
	}
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	if turnID == "" || s.currentTurnIDLocked(sessionID) != turnID {
		return nil, nil
	}

	event := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), time.Now().UTC())
	s.publishLocked(event)
	return []managedagents.Event{event}, nil
}

func (s *testStore) FailSessionTurn(sessionID string, turnID string, reason string) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, managedagents.ErrNotFound
	}
	if session.Status == managedagents.SessionStatusTerminated {
		return nil, managedagents.ErrTerminated
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	currentTurnID := s.currentTurnIDLocked(sessionID)
	if turnID == "" || currentTurnID != turnID {
		return nil, nil
	}

	now := time.Now().UTC()
	session.Status = managedagents.SessionStatusIdle
	idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, failedTurnIdlePayload(turnID, reason), now)
	s.sessions[sessionID] = session

	s.publishLocked(idleEvent)
	return []managedagents.Event{idleEvent}, nil
}

func (s *testStore) ListEvents(sessionID string, afterSeq int64) ([]managedagents.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, managedagents.ErrNotFound
	}

	all := s.events[sessionID]
	events := make([]managedagents.Event, 0, len(all))
	for _, event := range all {
		if event.Seq > afterSeq {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *testStore) SubscribeEvents(sessionID string) (<-chan managedagents.Event, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, nil, managedagents.ErrNotFound
	}

	ch := make(chan managedagents.Event, 16)
	if s.subscribers[sessionID] == nil {
		s.subscribers[sessionID] = make(map[chan managedagents.Event]struct{})
	}
	s.subscribers[sessionID][ch] = struct{}{}

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		delete(s.subscribers[sessionID], ch)
		if len(s.subscribers[sessionID]) == 0 {
			delete(s.subscribers, sessionID)
		}
		close(ch)
	}
	return ch, cancel, nil
}

func (s *testStore) applyEventLocked(session *managedagents.Session, input managedagents.AppendEventInput, now time.Time) ([]managedagents.Event, error) {
	switch input.Type {
	case managedagents.EventUserMessage:
		if session.Status != managedagents.SessionStatusIdle {
			return nil, fmt.Errorf("%w: user.message requires idle session", managedagents.ErrInvalid)
		}
		turnID := s.nextTurnIDLocked(session.ID)
		session.Status = managedagents.SessionStatusRunning
		statusEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusRunning, statusPayload("running", turnID), now)
		userEvent := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		s.publishLocked(statusEvent)
		s.publishLocked(userEvent)
		return []managedagents.Event{statusEvent, userEvent}, nil

	case managedagents.EventUserInterrupt:
		if session.Status != managedagents.SessionStatusRunning {
			return nil, fmt.Errorf("%w: user.interrupt requires running session", managedagents.ErrInvalid)
		}
		turnID := s.currentTurnIDLocked(session.ID)
		if turnID == "" {
			return nil, fmt.Errorf("%w: running session has no active turn", managedagents.ErrInvalid)
		}
		userEvent := s.appendEventLocked(session.ID, input.Type, payloadWithTurnID(input.Payload, turnID), now)
		interruptingEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusInterrupting, statusPayload("interrupting", turnID), now)
		session.Status = managedagents.SessionStatusIdle
		idleEvent := s.appendEventLocked(session.ID, managedagents.EventSessionStatusIdle, statusPayload("idle", turnID), now)
		s.publishLocked(userEvent)
		s.publishLocked(interruptingEvent)
		s.publishLocked(idleEvent)
		return []managedagents.Event{userEvent, interruptingEvent, idleEvent}, nil

	default:
		event := s.appendEventLocked(session.ID, input.Type, cloneRaw(input.Payload), now)
		s.publishLocked(event)
		return []managedagents.Event{event}, nil
	}
}

func (s *testStore) appendEventLocked(sessionID, eventType string, payload json.RawMessage, now time.Time) managedagents.Event {
	seq := int64(len(s.events[sessionID]) + 1)
	event := managedagents.Event{
		ID:        s.nextID("evt", &s.nextEventID),
		SessionID: sessionID,
		Seq:       seq,
		Type:      eventType,
		Payload:   cloneRaw(payload),
		CreatedAt: now,
	}
	s.events[sessionID] = append(s.events[sessionID], event)
	return event
}

func (s *testStore) publishLocked(event managedagents.Event) {
	for ch := range s.subscribers[event.SessionID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *testStore) closeSessionLocked(sessionID string) {
	for ch := range s.subscribers[sessionID] {
		close(ch)
	}
	delete(s.subscribers, sessionID)
}

func (s *testStore) nextID(prefix string, counter *int64) string {
	*counter = *counter + 1
	return fmt.Sprintf("%s_%06d", prefix, *counter)
}

func (s *testStore) nextTurnIDLocked(sessionID string) string {
	var count int64
	for _, event := range s.events[sessionID] {
		if event.Type == managedagents.EventUserMessage {
			count++
		}
	}
	return fmt.Sprintf("turn_%06d", count+1)
}

func (s *testStore) currentTurnIDLocked(sessionID string) string {
	events := s.events[sessionID]
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == managedagents.EventUserMessage {
			return payloadString(events[i].Payload, "turn_id")
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func statusPayload(status string, turnID string) json.RawMessage {
	payload := map[string]string{"status": status}
	if turnID != "" {
		payload["turn_id"] = turnID
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"status":"` + status + `"}`)
	}
	return encoded
}

func failedTurnIdlePayload(turnID string, reason string) json.RawMessage {
	payload := map[string]string{
		"status":           "idle",
		"last_turn_status": "failed",
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}
	if reason != "" {
		payload["reason"] = reason
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"status":"idle","last_turn_status":"failed"}`)
	}
	return encoded
}

func payloadWithTurnID(payload json.RawMessage, turnID string) json.RawMessage {
	var object map[string]any
	if len(payload) == 0 || string(payload) == "null" {
		object = make(map[string]any)
	} else if err := json.Unmarshal(payload, &object); err != nil {
		object = make(map[string]any)
	} else if object == nil {
		object = make(map[string]any)
	}

	object["turn_id"] = turnID
	encoded, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return encoded
}
