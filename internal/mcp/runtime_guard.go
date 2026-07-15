package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRuntimeTimeout          = 30 * time.Second
	DefaultRuntimeMaxConcurrency   = 4
	DefaultRuntimeFailureThreshold = 5
	DefaultRuntimeCooldown         = 30 * time.Second
)

var (
	ErrRuntimeCircuitOpen     = errors.New("mcp runtime circuit is open")
	ErrRuntimeConcurrencyFull = errors.New("mcp runtime concurrency wait canceled")
)

type RuntimeClient interface {
	ListTools(context.Context) (InitializeResult, []ToolDefinition, error)
	ListResources(context.Context) (InitializeResult, []ResourceDefinition, error)
	ListResourceTemplates(context.Context) (InitializeResult, []ResourceTemplate, error)
	ReadResource(context.Context, string) (ResourceReadResult, error)
	ListPrompts(context.Context) (InitializeResult, []PromptDefinition, error)
	GetPrompt(context.Context, string, json.RawMessage) (PromptGetResult, error)
	Complete(context.Context, CompletionReference, CompletionArgument, CompletionContext) (CompletionResult, error)
	CallTool(context.Context, string, json.RawMessage) (ToolCallResult, error)
}

type EffectiveRuntimePolicy struct {
	Timeout          time.Duration
	MaxConcurrency   int
	FailureThreshold int
	Cooldown         time.Duration
}

func (p *RuntimePolicy) Effective() EffectiveRuntimePolicy {
	result := EffectiveRuntimePolicy{
		Timeout:          DefaultRuntimeTimeout,
		MaxConcurrency:   DefaultRuntimeMaxConcurrency,
		FailureThreshold: DefaultRuntimeFailureThreshold,
		Cooldown:         DefaultRuntimeCooldown,
	}
	if p == nil {
		return result
	}
	if p.TimeoutSeconds > 0 {
		result.Timeout = time.Duration(p.TimeoutSeconds) * time.Second
	}
	if p.MaxConcurrency > 0 {
		result.MaxConcurrency = p.MaxConcurrency
	}
	if p.FailureThreshold > 0 {
		result.FailureThreshold = p.FailureThreshold
	}
	if p.CooldownSeconds > 0 {
		result.Cooldown = time.Duration(p.CooldownSeconds) * time.Second
	}
	return result
}

type RuntimeGuardOptions struct {
	Now func() time.Time
}

type RuntimeCallError struct {
	Class string
	Err   error
}

func (e *RuntimeCallError) Error() string {
	if e == nil || e.Err == nil {
		return "mcp runtime call failed"
	}
	return e.Err.Error()
}

func (e *RuntimeCallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func RuntimeErrorClass(err error) string {
	var runtimeErr *RuntimeCallError
	if errors.As(err, &runtimeErr) {
		return runtimeErr.Class
	}
	return ""
}

type RuntimeGuardStats struct {
	TrackedServers       int              `json:"tracked_servers"`
	InFlight             int              `json:"in_flight"`
	OpenCircuits         int              `json:"open_circuits"`
	CallsTotal           int64            `json:"calls_total"`
	SuccessesTotal       int64            `json:"successes_total"`
	FailuresTotal        int64            `json:"failures_total"`
	CircuitRejectedTotal int64            `json:"circuit_rejected_total"`
	WaitCanceledTotal    int64            `json:"wait_canceled_total"`
	FailuresByClass      map[string]int64 `json:"failures_by_class,omitempty"`
}

type RuntimeServerState struct {
	Key                 string    `json:"key"`
	State               string    `json:"state"`
	InFlight            int       `json:"in_flight"`
	MaxConcurrency      int       `json:"max_concurrency"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	OpenUntil           time.Time `json:"open_until,omitempty"`
}

type RuntimePartition struct {
	WorkspaceID string
	ServerID    string
	Version     int
	Identifier  string
}

type RegistryRuntimeState struct {
	ServerID            string     `json:"server_id"`
	Version             int        `json:"version"`
	State               string     `json:"state"`
	InFlight            int        `json:"in_flight"`
	MaxConcurrency      int        `json:"max_concurrency"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	FailureThreshold    int        `json:"failure_threshold"`
	LastFailureClass    string     `json:"last_failure_class,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
	OpenUntil           *time.Time `json:"open_until,omitempty"`
	CooldownRemaining   int64      `json:"cooldown_remaining_seconds,omitempty"`
}

type RuntimeGuard struct {
	mu                   sync.Mutex
	entries              map[string]*runtimeGuardEntry
	now                  func() time.Time
	callsTotal           int64
	successesTotal       int64
	failuresTotal        int64
	circuitRejectedTotal int64
	waitCanceledTotal    int64
	failuresByClass      map[string]int64
}

type runtimeGuardEntry struct {
	partition           RuntimePartition
	policy              EffectiveRuntimePolicy
	slots               chan struct{}
	inFlight            int
	consecutiveFailures int
	openUntil           time.Time
	halfOpen            bool
	lastFailureClass    string
	lastFailureAt       time.Time
	failureDomain       string
}

type guardedRuntimeClient struct {
	guard     *RuntimeGuard
	key       string
	partition RuntimePartition
	policy    EffectiveRuntimePolicy
	client    RuntimeClient
}

type runtimePermit struct {
	guard *RuntimeGuard
	entry *runtimeGuardEntry
	probe bool
	once  sync.Once
}

func NewRuntimeGuard(options RuntimeGuardOptions) *RuntimeGuard {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &RuntimeGuard{
		entries:         map[string]*runtimeGuardEntry{},
		now:             now,
		failuresByClass: map[string]int64{},
	}
}

func (g *RuntimeGuard) Wrap(key string, policy *RuntimePolicy, client RuntimeClient) RuntimeClient {
	if g == nil || client == nil {
		return client
	}
	key = strings.Trim(strings.TrimSpace(key), "/")
	if key == "" {
		key = "mcp"
	}
	return guardedRuntimeClient{guard: g, key: key, policy: policy.Effective(), client: client}
}

func (g *RuntimeGuard) WrapPartition(partition RuntimePartition, policy *RuntimePolicy, client RuntimeClient) RuntimeClient {
	if g == nil || client == nil {
		return client
	}
	partition.WorkspaceID = strings.TrimSpace(partition.WorkspaceID)
	partition.ServerID = strings.TrimSpace(partition.ServerID)
	partition.Identifier = strings.TrimSpace(partition.Identifier)
	keyParts := []string{partition.WorkspaceID}
	if partition.ServerID != "" {
		keyParts = append(keyParts, partition.ServerID, fmt.Sprintf("v%d", partition.Version))
	} else {
		keyParts = append(keyParts, partition.Identifier, "embedded")
	}
	return guardedRuntimeClient{
		guard: g, key: strings.Trim(strings.Join(keyParts, "/"), "/"),
		partition: partition, policy: policy.Effective(), client: client,
	}
}

func (g *RuntimeGuard) Stats() RuntimeGuardStats {
	if g == nil {
		return RuntimeGuardStats{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	stats := RuntimeGuardStats{
		TrackedServers:       len(g.entries),
		CallsTotal:           g.callsTotal,
		SuccessesTotal:       g.successesTotal,
		FailuresTotal:        g.failuresTotal,
		CircuitRejectedTotal: g.circuitRejectedTotal,
		WaitCanceledTotal:    g.waitCanceledTotal,
		FailuresByClass:      cloneInt64Map(g.failuresByClass),
	}
	now := g.now()
	for _, entry := range g.entries {
		stats.InFlight += entry.inFlight
		state := runtimeGuardState(entry, now)
		if state == "open" || state == "half_open" {
			stats.OpenCircuits++
		}
	}
	return stats
}

func (g *RuntimeGuard) States() []RuntimeServerState {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	states := make([]RuntimeServerState, 0, len(g.entries))
	for key, entry := range g.entries {
		state := runtimeGuardState(entry, now)
		states = append(states, RuntimeServerState{
			Key: key, State: state, InFlight: entry.inFlight,
			MaxConcurrency:      entry.policy.MaxConcurrency,
			ConsecutiveFailures: entry.consecutiveFailures,
			OpenUntil:           entry.openUntil,
		})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Key < states[j].Key })
	return states
}

func (g *RuntimeGuard) RegistryStates(workspaceID string) []RegistryRuntimeState {
	if g == nil {
		return nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	states := make([]RegistryRuntimeState, 0, len(g.entries))
	for _, entry := range g.entries {
		if entry.partition.WorkspaceID != workspaceID || entry.partition.ServerID == "" {
			continue
		}
		state := runtimeGuardState(entry, now)
		remaining := int64(0)
		var lastFailureAt *time.Time
		if !entry.lastFailureAt.IsZero() {
			value := entry.lastFailureAt
			lastFailureAt = &value
		}
		var openUntil *time.Time
		if entry.openUntil.After(now) {
			remaining = int64((entry.openUntil.Sub(now) + time.Second - 1) / time.Second)
			value := entry.openUntil
			openUntil = &value
		}
		states = append(states, RegistryRuntimeState{
			ServerID: entry.partition.ServerID, Version: entry.partition.Version,
			State: state, InFlight: entry.inFlight, MaxConcurrency: entry.policy.MaxConcurrency,
			ConsecutiveFailures: entry.consecutiveFailures, FailureThreshold: entry.policy.FailureThreshold,
			LastFailureClass: entry.lastFailureClass, LastFailureAt: lastFailureAt,
			OpenUntil: openUntil, CooldownRemaining: remaining,
		})
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].ServerID == states[j].ServerID {
			return states[i].Version > states[j].Version
		}
		return states[i].ServerID < states[j].ServerID
	})
	return states
}

func runtimeGuardState(entry *runtimeGuardEntry, now time.Time) string {
	switch {
	case entry.openUntil.After(now):
		return "open"
	case entry.halfOpen, !entry.openUntil.IsZero() && entry.consecutiveFailures >= entry.policy.FailureThreshold:
		return "half_open"
	case entry.inFlight >= entry.policy.MaxConcurrency:
		return "saturated"
	default:
		return "closed"
	}
}

func (g *RuntimeGuard) acquire(ctx context.Context, key string, policy EffectiveRuntimePolicy) (*runtimePermit, error) {
	return g.acquirePartition(ctx, key, RuntimePartition{}, policy)
}

func (g *RuntimeGuard) acquirePartition(ctx context.Context, key string, partition RuntimePartition, policy EffectiveRuntimePolicy) (*runtimePermit, error) {
	g.mu.Lock()
	entry := g.entries[key]
	if entry == nil {
		entry = &runtimeGuardEntry{partition: partition, policy: policy, slots: make(chan struct{}, policy.MaxConcurrency)}
		g.entries[key] = entry
	}
	now := g.now()
	if entry.openUntil.After(now) || (entry.halfOpen && !entry.openUntil.After(now)) {
		g.circuitRejectedTotal++
		g.mu.Unlock()
		return nil, ErrRuntimeCircuitOpen
	}
	probe := false
	if !entry.openUntil.IsZero() && !entry.openUntil.After(now) && entry.consecutiveFailures >= entry.policy.FailureThreshold {
		entry.halfOpen = true
		probe = true
	}
	g.mu.Unlock()

	select {
	case entry.slots <- struct{}{}:
	case <-ctx.Done():
		g.mu.Lock()
		if probe {
			entry.halfOpen = false
		}
		g.waitCanceledTotal++
		g.mu.Unlock()
		return nil, fmt.Errorf("%w: %w", ErrRuntimeConcurrencyFull, ctx.Err())
	}

	g.mu.Lock()
	now = g.now()
	if entry.openUntil.After(now) && !probe {
		g.circuitRejectedTotal++
		g.mu.Unlock()
		<-entry.slots
		return nil, ErrRuntimeCircuitOpen
	}
	entry.inFlight++
	g.callsTotal++
	g.mu.Unlock()
	return &runtimePermit{guard: g, entry: entry, probe: probe}, nil
}

func (p *runtimePermit) finish(err error) {
	p.finishDomain(err, "generic")
}

func (p *runtimePermit) finishDomain(err error, domain string) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		<-p.entry.slots
		class, countsFailure := ClassifyRuntimeError(err)
		p.guard.mu.Lock()
		defer p.guard.mu.Unlock()
		p.entry.inFlight--
		if err == nil {
			p.guard.successesTotal++
			if p.probe || p.entry.failureDomain == "" || p.entry.failureDomain == domain {
				p.entry.consecutiveFailures = 0
				p.entry.failureDomain = ""
				p.entry.openUntil = time.Time{}
				p.entry.halfOpen = false
			}
			return
		}
		p.guard.failuresTotal++
		p.guard.failuresByClass[class]++
		p.entry.lastFailureClass = class
		p.entry.lastFailureAt = p.guard.now().UTC()
		if !countsFailure {
			if p.probe {
				p.entry.halfOpen = false
			}
			return
		}
		if !p.probe && p.entry.failureDomain != "" && p.entry.failureDomain != domain {
			p.entry.consecutiveFailures = 0
		}
		p.entry.failureDomain = domain
		p.entry.consecutiveFailures++
		if p.entry.consecutiveFailures >= p.entry.policy.FailureThreshold {
			p.entry.openUntil = p.guard.now().Add(p.entry.policy.Cooldown)
		}
		p.entry.halfOpen = false
	})
}

func ClassifyRuntimeError(err error) (string, bool) {
	if err == nil {
		return "none", false
	}
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled", false
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(text, "deadline exceeded"), strings.Contains(text, "timeout"):
		return "timeout", true
	case strings.Contains(text, "401"), strings.Contains(text, "403"), strings.Contains(text, "unauthorized"), strings.Contains(text, "forbidden"):
		return "authentication", true
	case strings.Contains(text, "429"), strings.Contains(text, "rate limit"):
		return "rate_limited", true
	case strings.Contains(text, "connection refused"), strings.Contains(text, "connection reset"), strings.Contains(text, "broken pipe"), strings.Contains(text, "eof"), strings.Contains(text, "no such host"):
		return "transport", true
	case strings.Contains(text, "502"), strings.Contains(text, "503"), strings.Contains(text, "504"), strings.Contains(text, "unavailable"):
		return "unavailable", true
	case strings.Contains(text, "json-rpc"), strings.Contains(text, "invalid response"), strings.Contains(text, "protocol"):
		return "protocol", true
	default:
		return "unknown", true
	}
}

func (c guardedRuntimeClient) run(ctx context.Context, domain string, call func(context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, c.policy.Timeout)
	defer cancel()
	permit, err := c.guard.acquirePartition(callCtx, c.key, c.partition, c.policy)
	if err != nil {
		class := "concurrency_wait"
		if errors.Is(err, ErrRuntimeCircuitOpen) {
			class = "circuit_open"
		}
		return &RuntimeCallError{Class: class, Err: err}
	}
	err = call(callCtx)
	if err == nil && callCtx.Err() != nil {
		err = callCtx.Err()
	}
	permit.finishDomain(err, domain)
	if err == nil {
		return nil
	}
	class, _ := ClassifyRuntimeError(err)
	return &RuntimeCallError{Class: class, Err: err}
}

func (c guardedRuntimeClient) ListTools(ctx context.Context) (result InitializeResult, tools []ToolDefinition, err error) {
	err = c.run(ctx, "catalog", func(callCtx context.Context) error { result, tools, err = c.client.ListTools(callCtx); return err })
	return
}

func (c guardedRuntimeClient) ListResources(ctx context.Context) (result InitializeResult, resources []ResourceDefinition, err error) {
	err = c.run(ctx, "catalog", func(callCtx context.Context) error {
		result, resources, err = c.client.ListResources(callCtx)
		return err
	})
	return
}

func (c guardedRuntimeClient) ListResourceTemplates(ctx context.Context) (result InitializeResult, templates []ResourceTemplate, err error) {
	err = c.run(ctx, "catalog", func(callCtx context.Context) error {
		result, templates, err = c.client.ListResourceTemplates(callCtx)
		return err
	})
	return
}

func (c guardedRuntimeClient) ReadResource(ctx context.Context, uri string) (result ResourceReadResult, err error) {
	err = c.run(ctx, "operation", func(callCtx context.Context) error { result, err = c.client.ReadResource(callCtx, uri); return err })
	return
}

func (c guardedRuntimeClient) ListPrompts(ctx context.Context) (result InitializeResult, prompts []PromptDefinition, err error) {
	err = c.run(ctx, "catalog", func(callCtx context.Context) error { result, prompts, err = c.client.ListPrompts(callCtx); return err })
	return
}

func (c guardedRuntimeClient) GetPrompt(ctx context.Context, name string, arguments json.RawMessage) (result PromptGetResult, err error) {
	err = c.run(ctx, "operation", func(callCtx context.Context) error {
		result, err = c.client.GetPrompt(callCtx, name, arguments)
		return err
	})
	return
}

func (c guardedRuntimeClient) Complete(ctx context.Context, reference CompletionReference, argument CompletionArgument, completionContext CompletionContext) (result CompletionResult, err error) {
	err = c.run(ctx, "operation", func(callCtx context.Context) error {
		result, err = c.client.Complete(callCtx, reference, argument, completionContext)
		return err
	})
	return
}

func (c guardedRuntimeClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (result ToolCallResult, err error) {
	err = c.run(ctx, "operation", func(callCtx context.Context) error {
		result, err = c.client.CallTool(callCtx, name, arguments)
		return err
	})
	return
}
