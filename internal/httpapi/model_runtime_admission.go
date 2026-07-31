package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

const (
	modelRuntimeFamilyModel  = "model"
	modelRuntimeFamilySpeech = "speech"
)

type ModelRuntimePolicy struct {
	ModelGlobalConcurrency           int
	ModelWorkspaceConcurrency        int
	ModelIdentityConcurrency         int
	ModelRouteConcurrency            int
	ModelWorkspaceRequestsPerMinute  int
	ModelIdentityRequestsPerMinute   int
	SpeechGlobalConcurrency          int
	SpeechWorkspaceConcurrency       int
	SpeechIdentityConcurrency        int
	SpeechRouteConcurrency           int
	SpeechWorkspaceSessionsPerMinute int
	SpeechIdentitySessionsPerMinute  int
	SpeechMaxSessionDuration         time.Duration
}

func DefaultModelRuntimePolicy() ModelRuntimePolicy {
	return ModelRuntimePolicy{
		ModelGlobalConcurrency:           64,
		ModelWorkspaceConcurrency:        16,
		ModelIdentityConcurrency:         8,
		ModelRouteConcurrency:            32,
		ModelWorkspaceRequestsPerMinute:  600,
		ModelIdentityRequestsPerMinute:   120,
		SpeechGlobalConcurrency:          100,
		SpeechWorkspaceConcurrency:       20,
		SpeechIdentityConcurrency:        10,
		SpeechRouteConcurrency:           50,
		SpeechWorkspaceSessionsPerMinute: 120,
		SpeechIdentitySessionsPerMinute:  30,
		SpeechMaxSessionDuration:         15 * time.Minute,
	}
}

type modelRuntimeAdmissionRequest struct {
	Family            string
	WorkspaceID       string
	PrincipalID       string
	ServiceIdentityID string
	Capability        string
	ProviderID        string
	Model             string
}

type modelRuntimeAdmissionError struct {
	kind       string
	scope      string
	limit      int
	retryAfter time.Duration
}

func (e *modelRuntimeAdmissionError) Error() string {
	return fmt.Sprintf("model runtime %s limit reached for %s", e.kind, e.scope)
}

func (e *modelRuntimeAdmissionError) code(family string) string {
	prefix := "model"
	if family == modelRuntimeFamilySpeech {
		prefix = "speech"
	}
	return prefix + "_" + e.kind + "_exceeded"
}

func (e *modelRuntimeAdmissionError) retryAfterSeconds() int {
	seconds := int((e.retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

type modelRuntimeRateWindow struct {
	startedAt time.Time
	count     int
}

type modelRuntimeAdmission struct {
	policy  ModelRuntimePolicy
	mu      sync.Mutex
	active  map[string]int
	windows map[string]modelRuntimeRateWindow
}

func newModelRuntimeAdmission(policy ModelRuntimePolicy) *modelRuntimeAdmission {
	return &modelRuntimeAdmission{policy: policy, active: map[string]int{}, windows: map[string]modelRuntimeRateWindow{}}
}

func (a *modelRuntimeAdmission) acquireCapacity(request modelRuntimeAdmissionRequest) (func(), error) {
	if a == nil {
		return func() {}, nil
	}
	identity := request.ServiceIdentityID
	if identity == "" {
		identity = request.PrincipalID
	}
	type capacityCheck struct {
		scope string
		key   string
		limit int
	}
	checks := []capacityCheck{
		{scope: "global", key: strings.Join([]string{request.Family, "global"}, "\x00"), limit: a.globalConcurrency(request.Family)},
		{scope: "workspace", key: strings.Join([]string{request.Family, "workspace", request.WorkspaceID}, "\x00"), limit: a.workspaceConcurrency(request.Family)},
		{scope: "identity", key: strings.Join([]string{request.Family, "identity", request.WorkspaceID, identity}, "\x00"), limit: a.identityConcurrency(request.Family)},
		{scope: "route", key: strings.Join([]string{request.Family, "route", request.ProviderID, request.Model}, "\x00"), limit: a.routeConcurrency(request.Family)},
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, check := range checks {
		if check.limit > 0 && a.active[check.key] >= check.limit {
			return nil, &modelRuntimeAdmissionError{kind: "capacity", scope: check.scope, limit: check.limit, retryAfter: time.Second}
		}
	}
	for _, check := range checks {
		if check.limit > 0 {
			a.active[check.key]++
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			for _, check := range checks {
				if check.limit <= 0 {
					continue
				}
				a.active[check.key]--
				if a.active[check.key] <= 0 {
					delete(a.active, check.key)
				}
			}
		})
	}, nil
}

func (a *modelRuntimeAdmission) reserveLocalQuota(request modelRuntimeAdmissionRequest, now time.Time) error {
	if a == nil {
		return nil
	}
	workspaceLimit, identityLimit := a.quotaLimits(request.Family)
	if workspaceLimit == 0 && identityLimit == 0 {
		return nil
	}
	identity := request.ServiceIdentityID
	if identity == "" {
		identity = request.PrincipalID
	}
	windowStart := now.UTC().Truncate(time.Minute)
	type quotaCheck struct {
		scope string
		key   string
		limit int
	}
	checks := []quotaCheck{
		{scope: "workspace", key: strings.Join([]string{request.Family, "workspace", request.WorkspaceID, request.Capability, request.ProviderID, request.Model}, "\x00"), limit: workspaceLimit},
		{scope: "identity", key: strings.Join([]string{request.Family, "identity", request.WorkspaceID, identity, request.Capability, request.ProviderID, request.Model}, "\x00"), limit: identityLimit},
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, check := range checks {
		if check.limit == 0 {
			continue
		}
		window := a.windows[check.key]
		if window.startedAt.Before(windowStart) {
			window = modelRuntimeRateWindow{startedAt: windowStart}
		}
		if window.count >= check.limit {
			return &modelRuntimeAdmissionError{kind: "quota", scope: check.scope, limit: check.limit, retryAfter: windowStart.Add(time.Minute).Sub(now)}
		}
	}
	for _, check := range checks {
		if check.limit == 0 {
			continue
		}
		window := a.windows[check.key]
		if window.startedAt.Before(windowStart) {
			window = modelRuntimeRateWindow{startedAt: windowStart}
		}
		window.count++
		a.windows[check.key] = window
	}
	return nil
}

func (a *modelRuntimeAdmission) globalConcurrency(family string) int {
	if family == modelRuntimeFamilySpeech {
		return a.policy.SpeechGlobalConcurrency
	}
	return a.policy.ModelGlobalConcurrency
}

func (a *modelRuntimeAdmission) workspaceConcurrency(family string) int {
	if family == modelRuntimeFamilySpeech {
		return a.policy.SpeechWorkspaceConcurrency
	}
	return a.policy.ModelWorkspaceConcurrency
}

func (a *modelRuntimeAdmission) identityConcurrency(family string) int {
	if family == modelRuntimeFamilySpeech {
		return a.policy.SpeechIdentityConcurrency
	}
	return a.policy.ModelIdentityConcurrency
}

func (a *modelRuntimeAdmission) routeConcurrency(family string) int {
	if family == modelRuntimeFamilySpeech {
		return a.policy.SpeechRouteConcurrency
	}
	return a.policy.ModelRouteConcurrency
}

func (a *modelRuntimeAdmission) quotaLimits(family string) (int, int) {
	if family == modelRuntimeFamilySpeech {
		return a.policy.SpeechWorkspaceSessionsPerMinute, a.policy.SpeechIdentitySessionsPerMinute
	}
	return a.policy.ModelWorkspaceRequestsPerMinute, a.policy.ModelIdentityRequestsPerMinute
}

func (s *Server) admitModelRuntime(ctx context.Context, request modelRuntimeAdmissionRequest) (func(), error) {
	if s.modelRuntimeAdmission == nil {
		return func() {}, nil
	}
	release, err := s.modelRuntimeAdmission.acquireCapacity(request)
	if err != nil {
		return nil, err
	}
	workspaceLimit, identityLimit := s.modelRuntimeAdmission.quotaLimits(request.Family)
	if workspaceLimit == 0 && identityLimit == 0 {
		return release, nil
	}
	if store, ok := s.store.(managedagents.ModelInvocationQuotaStore); ok {
		reservation, reserveErr := store.ReserveModelInvocationQuotaContext(ctx, managedagents.ReserveModelInvocationQuotaInput{
			WorkspaceID: request.WorkspaceID, PrincipalID: request.PrincipalID, ServiceIdentityID: request.ServiceIdentityID,
			Capability: request.Capability, ProviderID: request.ProviderID, Model: request.Model,
			WindowStartedAt: time.Now().UTC(), WorkspaceLimit: workspaceLimit, IdentityLimit: identityLimit,
		})
		if reserveErr != nil {
			release()
			return nil, fmt.Errorf("reserve model runtime quota: %w", reserveErr)
		}
		if !reservation.Allowed {
			release()
			retryAfter := time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute))
			return nil, &modelRuntimeAdmissionError{kind: "quota", scope: reservation.ExceededScope, limit: reservation.Limit, retryAfter: retryAfter}
		}
		return release, nil
	}
	if err := s.modelRuntimeAdmission.reserveLocalQuota(request, time.Now()); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func modelRuntimeAdmissionRequestFromInvocation(family string, invocation managedagents.RecordModelInvocationInput) modelRuntimeAdmissionRequest {
	return modelRuntimeAdmissionRequest{
		Family: family, WorkspaceID: invocation.WorkspaceID, PrincipalID: invocation.PrincipalID,
		ServiceIdentityID: invocation.ServiceIdentityID, Capability: invocation.Capability,
		ProviderID: invocation.ProviderID, Model: invocation.Model,
	}
}

func (s *Server) writeModelRuntimeAdmissionError(w http.ResponseWriter, r *http.Request, family string, err error) {
	var admissionError *modelRuntimeAdmissionError
	if errors.As(err, &admissionError) {
		w.Header().Set("Retry-After", strconv.Itoa(admissionError.retryAfterSeconds()))
		writeV2Error(w, requestIDFromRequest(r), http.StatusTooManyRequests, admissionError.code(family), admissionError.Error(), true,
			map[string]any{"scope": admissionError.scope, "limit": admissionError.limit})
		return
	}
	s.logger.Error("model runtime admission failed", "family", family, "error", err)
	writeV2Error(w, requestIDFromRequest(r), http.StatusServiceUnavailable, "model_runtime_admission_unavailable", "model runtime admission is unavailable", true, nil)
}

func (s *Server) admitModelInvocationHTTP(w http.ResponseWriter, r *http.Request, invocation *managedagents.RecordModelInvocationInput) (func(), bool) {
	release, err := s.admitModelRuntime(r.Context(), modelRuntimeAdmissionRequestFromInvocation(modelRuntimeFamilyModel, *invocation))
	if err == nil {
		return release, true
	}
	completeModelInvocation(invocation, managedagents.ModelInvocationStatusFailed, modelRuntimeAdmissionErrorCode(modelRuntimeFamilyModel, err))
	s.recordModelInvocation(r, *invocation)
	s.writeModelRuntimeAdmissionError(w, r, modelRuntimeFamilyModel, err)
	return nil, false
}

func modelRuntimeAdmissionErrorCode(family string, err error) string {
	var admissionError *modelRuntimeAdmissionError
	if errors.As(err, &admissionError) {
		return admissionError.code(family)
	}
	return "model_runtime_admission_unavailable"
}
