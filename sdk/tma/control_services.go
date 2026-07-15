package tma

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type ObjectRefsService struct{ client *Client }

type OrchestrationService struct{ client *Client }

func (s *OrchestrationService) TaskGroupTemplates(ctx context.Context) (AgentTaskGroupTemplateList, error) {
	var result AgentTaskGroupTemplateList
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/agent/task-group-templates", nil, &result)
	return result, err
}

func (s *OrchestrationService) DiscussionStrategies(ctx context.Context) (AgentDiscussionStrategyList, error) {
	var result AgentDiscussionStrategyList
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/agent/discussion-strategies", nil, &result)
	return result, err
}

func (s *OrchestrationService) ListDeliberations(ctx context.Context, sessionID string) ([]AgentDeliberationResponse, error) {
	var response struct {
		Deliberations []AgentDeliberationResponse `json:"deliberations"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/deliberations", nil, &response)
	return response.Deliberations, err
}

func (s *OrchestrationService) GetDeliberation(ctx context.Context, sessionID string, deliberationID string) (AgentDeliberationResponse, error) {
	var result AgentDeliberationResponse
	err := s.client.DoJSON(ctx, http.MethodGet, deliberationPath(sessionID, deliberationID), nil, &result)
	return result, err
}

func (s *OrchestrationService) CancelDeliberation(ctx context.Context, sessionID string, deliberationID string, request CancelAgentDeliberationRequest) (AgentDeliberationResponse, error) {
	var result AgentDeliberationResponse
	err := s.client.DoJSON(ctx, http.MethodPost, deliberationPath(sessionID, deliberationID)+"/cancel", request, &result)
	return result, err
}

func (s *OrchestrationService) RetryDeliberationParticipant(ctx context.Context, sessionID string, deliberationID string, participantIndex int32, request RetryAgentDeliberationParticipantRequest) (AgentDeliberationResponse, error) {
	path := deliberationPath(sessionID, deliberationID) + "/participants/" + strconv.FormatInt(int64(participantIndex), 10) + "/retry"
	var result AgentDeliberationResponse
	err := s.client.DoJSON(ctx, http.MethodPost, path, request, &result)
	return result, err
}

func (s *OrchestrationService) ListTaskGroups(ctx context.Context, sessionID string) ([]InspectorTaskGroupState, error) {
	var response struct {
		TaskGroups []InspectorTaskGroupState `json:"task_groups"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/task-groups", nil, &response)
	return response.TaskGroups, err
}

func (s *OrchestrationService) TaskGroupTree(ctx context.Context, sessionID string) (SessionTaskGroupTree, error) {
	var result SessionTaskGroupTree
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/task-group-tree", nil, &result)
	return result, err
}

func (s *OrchestrationService) GetTaskGroup(ctx context.Context, sessionID string, groupID string) (InspectorTaskGroupState, error) {
	var result InspectorTaskGroupState
	err := s.client.DoJSON(ctx, http.MethodGet, taskGroupPath(sessionID, groupID), nil, &result)
	return result, err
}

func (s *OrchestrationService) CancelTaskGroup(ctx context.Context, sessionID string, groupID string, request CancelTaskGroupRequest) (AgentTaskGroupResponse, error) {
	var result AgentTaskGroupResponse
	err := s.client.DoJSON(ctx, http.MethodPost, taskGroupPath(sessionID, groupID)+"/cancel", request, &result)
	return result, err
}

func (s *OrchestrationService) RetryTaskGroup(ctx context.Context, sessionID string, groupID string) (AgentTaskGroupResponse, error) {
	var result AgentTaskGroupResponse
	err := s.client.DoJSON(ctx, http.MethodPost, taskGroupPath(sessionID, groupID)+"/retry", struct{}{}, &result)
	return result, err
}

func (s *OrchestrationService) RetryTaskGroupItem(ctx context.Context, sessionID string, groupID string, itemIndex int32) (AgentTaskGroupResponse, error) {
	path := taskGroupPath(sessionID, groupID) + "/items/" + strconv.FormatInt(int64(itemIndex), 10) + "/retry"
	var result AgentTaskGroupResponse
	err := s.client.DoJSON(ctx, http.MethodPost, path, struct{}{}, &result)
	return result, err
}

func (s *OrchestrationService) ReapOrphans(ctx context.Context, request ReapOrphanSubagentsRequest) (ReapOrphanSubagentsResult, error) {
	var result ReapOrphanSubagentsResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/subagents/reap-orphans", request, &result)
	return result, err
}

func deliberationPath(sessionID string, deliberationID string) string {
	return sessionPath(sessionID) + "/deliberations/" + url.PathEscape(deliberationID)
}

func taskGroupPath(sessionID string, groupID string) string {
	return sessionPath(sessionID) + "/task-groups/" + url.PathEscape(groupID)
}

func (s *OrchestrationService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2", path), request, response)
}

func (s *ObjectRefsService) Create(ctx context.Context, request CreateObjectRefRequest) (ObjectRef, error) {
	var object ObjectRef
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/object-refs", request, &object)
	return object, err
}

func (s *ObjectRefsService) Get(ctx context.Context, objectRefID string) (ObjectRef, error) {
	var object ObjectRef
	err := s.client.DoJSON(ctx, http.MethodGet, objectRefPath(objectRefID), nil, &object)
	return object, err
}

func (s *ObjectRefsService) Delete(ctx context.Context, objectRefID string) error {
	return s.client.DoJSON(ctx, http.MethodDelete, objectRefPath(objectRefID), nil, nil)
}

func (s *ObjectRefsService) Download(ctx context.Context, objectRefID string, sessionID string, output io.Writer) error {
	path := objectRefPath(objectRefID) + "/download"
	if strings.TrimSpace(sessionID) != "" {
		path += "?session_id=" + url.QueryEscape(sessionID)
	}
	return s.client.Download(ctx, path, output)
}

func (s *ObjectRefsService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2/object-refs", path), request, response)
}

func objectRefPath(objectRefID string) string {
	return "/v2/object-refs/" + url.PathEscape(objectRefID)
}

type TracesService struct{ client *Client }

func (s *TracesService) GetSession(ctx context.Context, sessionID string, turnID string) (TurnTrace, error) {
	var trace TurnTrace
	err := s.client.DoJSON(ctx, http.MethodGet, tracePath(sessionID, turnID, ""), nil, &trace)
	return trace, err
}

func (s *TracesService) List(ctx context.Context, query TraceListQuery) (TracePage, error) {
	values := url.Values{}
	setTraceQueryValues(values, query.WorkspaceID, query.SessionID, query.TurnID, query.SessionStatus, query.IncludeArchived, query.Limit, query.Cursor)
	var result TracePage
	err := s.client.DoJSON(ctx, http.MethodGet, pathWithQuery("/v2/traces", values), nil, &result)
	return result, err
}

func (s *TracesService) Get(ctx context.Context, traceID string) (TurnTrace, error) {
	var trace TurnTrace
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/traces/"+url.PathEscape(traceID), nil, &trace)
	return trace, err
}

func (s *TracesService) ListSpans(ctx context.Context, query TraceSpanListQuery) (TraceSpanPage, error) {
	values := url.Values{}
	setStringQuery(values, "workspace_id", query.WorkspaceID)
	setStringQuery(values, "trace_id", query.TraceID)
	setStringQuery(values, "session_id", query.SessionID)
	setStringQuery(values, "turn_id", query.TurnID)
	setStringQuery(values, "kind", query.Kind)
	setStringQuery(values, "status", query.Status)
	setStringQuery(values, "q", query.Search)
	if query.Critical != nil {
		values.Set("critical", strconv.FormatBool(*query.Critical))
	}
	setPositiveInt64Query(values, "min_duration_ms", query.MinDurationMillis)
	setPositiveInt64Query(values, "max_duration_ms", query.MaxDurationMillis)
	setPositiveInt64Query(values, "min_self_duration_ms", query.MinSelfDurationMillis)
	if query.IncludeArchived {
		values.Set("include_archived", "true")
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(query.Limit), 10))
	}
	setStringQuery(values, "cursor", query.Cursor)
	var result TraceSpanPage
	err := s.client.DoJSON(ctx, http.MethodGet, pathWithQuery("/v2/spans", values), nil, &result)
	return result, err
}

func (s *TracesService) GetSpan(ctx context.Context, traceID string, spanID string) (TraceSpanDetail, error) {
	path := "/v2/traces/" + url.PathEscape(traceID) + "/spans/" + url.PathEscape(spanID)
	var result TraceSpanDetail
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (s *TracesService) Export(ctx context.Context, sessionID string, turnID string, format string) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.client.DoJSON(ctx, http.MethodGet, tracePath(sessionID, turnID, format), nil, &result)
	return result, err
}

func tracePath(sessionID string, turnID string, format string) string {
	values := url.Values{}
	if strings.TrimSpace(turnID) != "" {
		values.Set("turn_id", turnID)
	}
	if strings.TrimSpace(format) != "" {
		values.Set("format", format)
	}
	path := sessionPath(sessionID) + "/trace"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return path
}

func setTraceQueryValues(values url.Values, workspaceID string, sessionID string, turnID string, sessionStatus string, includeArchived bool, limit int32, cursor string) {
	setStringQuery(values, "workspace_id", workspaceID)
	setStringQuery(values, "session_id", sessionID)
	setStringQuery(values, "turn_id", turnID)
	setStringQuery(values, "session_status", sessionStatus)
	if includeArchived {
		values.Set("include_archived", "true")
	}
	if limit > 0 {
		values.Set("limit", strconv.FormatInt(int64(limit), 10))
	}
	setStringQuery(values, "cursor", cursor)
}

func setStringQuery(values url.Values, key string, value string) {
	if strings.TrimSpace(value) != "" {
		values.Set(key, value)
	}
}

func setPositiveInt64Query(values url.Values, key string, value int64) {
	if value > 0 {
		values.Set(key, strconv.FormatInt(value, 10))
	}
}

func pathWithQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

// WorkersService contains control-plane operations only. Worker registration
// and heartbeat remain part of the Worker machine protocol.
type WorkersService struct{ client *Client }

func (s *WorkersService) List(ctx context.Context, query WorkerListQuery) ([]Worker, error) {
	values := url.Values{}
	if strings.TrimSpace(query.WorkspaceID) != "" {
		values.Set("workspace_id", query.WorkspaceID)
	}
	if strings.TrimSpace(query.Status) != "" {
		values.Set("status", query.Status)
	}
	path := "/v2/workers"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	var response struct {
		Workers []Worker `json:"workers"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Workers, err
}

func (s *WorkersService) Get(ctx context.Context, workerID string) (Worker, error) {
	var worker Worker
	err := s.client.DoJSON(ctx, http.MethodGet, workerPath(workerID), nil, &worker)
	return worker, err
}

func (s *WorkersService) Archive(ctx context.Context, workerID string) (Worker, error) {
	var worker Worker
	err := s.client.DoJSON(ctx, http.MethodPost, workerPath(workerID)+"/archive", struct{}{}, &worker)
	return worker, err
}

func (s *WorkersService) ReapExpired(ctx context.Context, request ReapExpiredWorkersRequest) (ReapExpiredWorkersResult, error) {
	var result ReapExpiredWorkersResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/workers/reap-expired", request, &result)
	return result, err
}

func (s *WorkersService) Diagnose(ctx context.Context, request WorkerDiagnoseRequest) (WorkerDiagnoseResponse, error) {
	var result WorkerDiagnoseResponse
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/workers/diagnose", request, &result)
	return result, err
}

func (s *WorkersService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2/workers", path), request, response)
}

func workerPath(workerID string) string { return "/v2/workers/" + url.PathEscape(workerID) }

// WorkerWorkService contains operator/control-plane operations only. Poll,
// ack, heartbeat, and result are intentionally excluded.
type WorkerWorkService struct{ client *Client }

func (s *WorkerWorkService) Enqueue(ctx context.Context, request EnqueueWorkerWorkRequest) (WorkerWork, error) {
	work, _, err := s.EnqueueWithDiagnostics(ctx, request)
	return work, err
}

func (s *WorkerWorkService) EnqueueWithDiagnostics(ctx context.Context, request EnqueueWorkerWorkRequest) (WorkerWork, *WorkerWorkConflict, error) {
	var response workerWorkEnqueueResponse
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/worker-work", request, &response)
	if err == nil {
		return response.WorkerWork, nil, nil
	}
	if response.Error != "" || len(response.Diagnostics) > 0 {
		conflict := WorkerWorkConflict{Error: response.Error, WorkerDiagnoseResponse: response.WorkerDiagnoseResponse}
		return WorkerWork{}, &conflict, err
	}
	var apiError *APIError
	if errors.As(err, &apiError) && len(apiError.Details) > 0 {
		encoded, encodeErr := json.Marshal(apiError.Details)
		if encodeErr == nil {
			var conflict WorkerWorkConflict
			if json.Unmarshal(encoded, &conflict) == nil && len(conflict.Diagnostics) > 0 {
				conflict.Error = apiError.Message
				return WorkerWork{}, &conflict, err
			}
		}
	}
	return WorkerWork{}, nil, err
}

type workerWorkEnqueueResponse struct {
	WorkerWork
	Error string `json:"error"`
	WorkerDiagnoseResponse
}

func (s *WorkerWorkService) Get(ctx context.Context, workID string) (WorkerWork, error) {
	var work WorkerWork
	err := s.client.DoJSON(ctx, http.MethodGet, workerWorkPath(workID), nil, &work)
	return work, err
}

func (s *WorkerWorkService) ReapExpired(ctx context.Context, request ReapExpiredWorkerWorkRequest) (ReapExpiredWorkerWorkResult, error) {
	var result ReapExpiredWorkerWorkResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/worker-work/reap-expired", request, &result)
	return result, err
}

func (s *WorkerWorkService) Cancel(ctx context.Context, workID string, request CancelWorkerWorkRequest) (WorkerWork, error) {
	var work WorkerWork
	err := s.client.DoJSON(ctx, http.MethodPost, workerWorkPath(workID)+"/cancel", request, &work)
	return work, err
}

func (s *WorkerWorkService) Requeue(ctx context.Context, workID string, request RequeueWorkerWorkRequest) (WorkerWork, error) {
	var work WorkerWork
	err := s.client.DoJSON(ctx, http.MethodPost, workerWorkPath(workID)+"/requeue", request, &work)
	return work, err
}

func (s *WorkerWorkService) Diagnose(ctx context.Context, workID string) (WorkerWorkDiagnosis, error) {
	var result WorkerWorkDiagnosis
	err := s.client.DoJSON(ctx, http.MethodGet, workerWorkPath(workID)+"/diagnose", nil, &result)
	return result, err
}

func (s *WorkerWorkService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2/worker-work", path), request, response)
}

func workerWorkPath(workID string) string { return "/v2/worker-work/" + url.PathEscape(workID) }

type ObservabilityService struct{ client *Client }

func (s *ObservabilityService) Status(ctx context.Context) (ObservabilityStatus, error) {
	var status ObservabilityStatus
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/observability/status", nil, &status)
	return status, err
}

func (s *ObservabilityService) Retry(ctx context.Context) (ObservabilityRetryResult, error) {
	var result ObservabilityRetryResult
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/observability/retry", nil, &result)
	return result, err
}

func (s *ObservabilityService) IntegrityKeys(ctx context.Context) (SecurityAuditIntegrityKeyStatus, error) {
	var status SecurityAuditIntegrityKeyStatus
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/observability/security-audit/integrity-keys", nil, &status)
	return status, err
}

func (s *ObservabilityService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2/observability", path), request, response)
}
