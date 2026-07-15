package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestAgentToolServiceSpawnStartsSubagentTurn(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	response, err := service.Spawn(t.Context(), tools.AgentSpawnRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_000001",
		AgentID:         childAgent.ID,
		Title:           "child research",
		Message:         "inspect auth module",
	})
	if err != nil {
		t.Fatalf("spawn subagent: %v", err)
	}
	if response.Session.ID == "" || response.Session.AgentID != childAgent.ID {
		t.Fatalf("unexpected spawned session: %#v", response.Session)
	}
	if !response.Started || len(runner.starts) != 1 {
		t.Fatalf("expected spawned message to start runner, response=%#v starts=%#v", response, runner.starts)
	}
	if got := runner.starts[0].SessionID; got != response.Session.ID {
		t.Fatalf("expected runner to start child session %s, got %s", response.Session.ID, got)
	}
	if got := response.Session.CreatedBy; got != "agent.spawn:"+parentSession.ID+":turn_000001" {
		t.Fatalf("unexpected created_by: %q", got)
	}
	if response.Session.ParentSessionID != parentSession.ID || response.Session.ParentTurnID != "turn_000001" || response.Session.SpawnDepth != 1 {
		t.Fatalf("expected lineage on spawned session, got %#v", response.Session)
	}
}

func TestAgentToolServiceWaitAndCollectResult(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	childSession := mustCreateSessionForSubagentTest(t, store, childAgent.ID, environment.ID, "child-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	payload, err := json.Marshal(map[string]any{
		"content": []map[string]string{{
			"type": "text",
			"text": "subagent finished",
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	events, err := store.AppendEvents(childSession.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: payload,
	}})
	if err != nil {
		t.Fatalf("append child user message: %v", err)
	}
	turnID := ""
	for _, event := range events {
		if event.Type == managedagents.EventUserMessage {
			turnID = payloadString(event.Payload, "turn_id")
		}
	}
	if turnID == "" {
		t.Fatal("expected child turn id")
	}
	if _, err := store.CompleteSessionTurn(childSession.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err != nil {
		t.Fatalf("complete child turn: %v", err)
	}

	waitResponse, err := service.Wait(t.Context(), tools.AgentWaitRequest{
		ParentSessionID: parentSession.ID,
		SessionID:       childSession.ID,
		TimeoutSeconds:  1,
	})
	if err != nil {
		t.Fatalf("wait child session: %v", err)
	}
	if waitResponse.Status != managedagents.SessionStatusIdle || waitResponse.TimedOut {
		t.Fatalf("unexpected wait response: %#v", waitResponse)
	}

	collectResponse, err := service.CollectResult(t.Context(), tools.AgentCollectResultRequest{
		ParentSessionID: parentSession.ID,
		SessionID:       childSession.ID,
	})
	if err != nil {
		t.Fatalf("collect child result: %v", err)
	}
	if collectResponse.AgentText != "done" {
		t.Fatalf("unexpected collect result text: %#v", collectResponse)
	}
	if collectResponse.EventCount == 0 {
		t.Fatalf("expected collected events, got %#v", collectResponse)
	}
}

func TestAgentToolServiceCreateWaitAndCollectTaskGroup(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect billing"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	if created.Group.ID == "" || created.Status != "running" || len(created.Items) != 2 {
		t.Fatalf("unexpected created group response: %#v", created)
	}
	if len(runner.starts) != 2 {
		t.Fatalf("expected two started child turns, got %#v", runner.starts)
	}

	for index, item := range created.Items {
		if item.Session == nil {
			t.Fatalf("expected session on item %d, got %#v", index, item)
		}
		events, err := store.ListEvents(item.Session.ID, 0)
		if err != nil {
			t.Fatalf("list child %d events: %v", index, err)
		}
		turnID := ""
		for _, event := range events {
			if event.Type == managedagents.EventUserMessage {
				turnID = payloadString(event.Payload, "turn_id")
			}
		}
		if turnID == "" {
			t.Fatalf("expected turn id for child %d events %#v", index, events)
		}
		if _, err := store.CompleteSessionTurn(item.Session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done `+string(rune('A'+index))+`"}]}`)); err != nil {
			t.Fatalf("complete child %d turn: %v", index, err)
		}
	}

	waited, err := service.WaitTaskGroup(t.Context(), tools.AgentTaskGroupWaitRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
		TimeoutSeconds:  1,
	})
	if err != nil {
		t.Fatalf("wait task group: %v", err)
	}
	if waited.TimedOut || !waited.Completed || waited.Status != "completed" || waited.Summary.Completed != 2 {
		t.Fatalf("unexpected waited group response: %#v", waited)
	}

	collected, err := service.CollectTaskGroup(t.Context(), tools.AgentTaskGroupCollectRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
	})
	if err != nil {
		t.Fatalf("collect task group: %v", err)
	}
	if !collected.Completed || collected.Status != "completed" || len(collected.Items) != 2 {
		t.Fatalf("unexpected collected group response: %#v", collected)
	}
	if collected.Items[0].AgentText == "" || collected.Items[1].AgentText == "" {
		t.Fatalf("expected collected agent texts, got %#v", collected.Items)
	}
	if collected.Aggregate.Reducer != managedagents.SubagentTaskGroupReducerConcatText || !strings.Contains(collected.Aggregate.Text, "done A") || !strings.Contains(collected.Aggregate.Text, "done B") {
		t.Fatalf("expected concatenated aggregate text, got %#v", collected.Aggregate)
	}
}

func TestAgentToolServiceCollectTaskGroupJSONReducer(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_json",
		ResultReducer:   managedagents.SubagentTaskGroupReducerJSONList,
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect billing"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	for index, item := range created.Items {
		events, err := store.ListEvents(item.Session.ID, 0)
		if err != nil {
			t.Fatalf("list child %d events: %v", index, err)
		}
		turnID := ""
		for _, event := range events {
			if event.Type == managedagents.EventUserMessage {
				turnID = payloadString(event.Payload, "turn_id")
			}
		}
		if _, err := store.CompleteSessionTurn(item.Session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"json `+string(rune('A'+index))+`"}],"result_json":{"item":"`+string(rune('A'+index))+`","index":`+string(rune('0'+index))+`}}`)); err != nil {
			t.Fatalf("complete child %d turn: %v", index, err)
		}
	}
	collected, err := service.CollectTaskGroup(t.Context(), tools.AgentTaskGroupCollectRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
	})
	if err != nil {
		t.Fatalf("collect json reducer group: %v", err)
	}
	if collected.Aggregate.Reducer != managedagents.SubagentTaskGroupReducerJSONList || len(collected.Aggregate.JSON) == 0 {
		t.Fatalf("expected json aggregate, got %#v", collected.Aggregate)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(collected.Aggregate.JSON, &decoded); err != nil {
		t.Fatalf("decode aggregate json: %v", err)
	}
	if len(decoded) != 2 || decoded[0]["item_index"] != float64(0) || decoded[1]["item_index"] != float64(1) {
		t.Fatalf("unexpected aggregate json payload: %#v", decoded)
	}
}

func TestAgentToolServiceCreateTaskGroupFromTemplate(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_template",
		TemplateID:      "module_risk_audit",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "audit auth module"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "audit billing module"},
		},
	})
	if err != nil {
		t.Fatalf("create templated task group: %v", err)
	}
	if created.Group.ResultReducer != managedagents.SubagentTaskGroupReducerJSONValues {
		t.Fatalf("expected template reducer, got %#v", created.Group)
	}
	if len(created.Items) != 2 || len(created.Items[0].Item.ExpectedResultSchema) == 0 || len(created.Items[1].Item.ExpectedResultSchema) == 0 {
		t.Fatalf("expected template schema on group items, got %#v", created.Items)
	}

	for index, item := range created.Items {
		events, err := store.ListEvents(item.Session.ID, 0)
		if err != nil {
			t.Fatalf("list child %d events: %v", index, err)
		}
		turnID := ""
		for _, event := range events {
			if event.Type == managedagents.EventUserMessage {
				turnID = payloadString(event.Payload, "turn_id")
			}
		}
		if turnID == "" {
			t.Fatalf("expected turn id for child %d", index)
		}
		result := `{"content":[{"type":"text","text":"done"}],"result_json":{"module":"` + []string{"auth", "billing"}[index] + `","risk_level":"high","summary":"needs review","files":["` + []string{"auth.go", "billing.go"}[index] + `"]}}`
		if _, err := store.CompleteSessionTurn(item.Session.ID, turnID, json.RawMessage(result)); err != nil {
			t.Fatalf("complete child %d turn: %v", index, err)
		}
	}

	collected, err := service.CollectTaskGroup(t.Context(), tools.AgentTaskGroupCollectRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
	})
	if err != nil {
		t.Fatalf("collect templated task group: %v", err)
	}
	if collected.Aggregate.Reducer != managedagents.SubagentTaskGroupReducerJSONValues || len(collected.Aggregate.JSON) == 0 {
		t.Fatalf("expected json_values aggregate, got %#v", collected.Aggregate)
	}
	if !strings.Contains(string(collected.Aggregate.Schema), `"type":"array"`) || !strings.Contains(string(collected.Aggregate.Schema), `"module"`) {
		t.Fatalf("expected aggregate schema derived from template, got %s", string(collected.Aggregate.Schema))
	}
}

func TestAgentToolServiceCreateTaskGroupRejectsUnknownTemplate(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	service := newAgentToolService(store, &recordingRunner{}, nil, defaultSubagentPolicy())

	_, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_bad_template",
		TemplateID:      "does_not_exist",
		Items: []tools.AgentTaskGroupItemRequest{
			{Message: "noop"},
		},
	})
	if err == nil || !errors.Is(err, managedagents.ErrInvalid) {
		t.Fatalf("expected invalid template error, got %v", err)
	}
}

func TestAgentToolServiceCollectTaskGroupSchemaValidationFailure(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_schema",
		Items: []tools.AgentTaskGroupItemRequest{{
			AgentID:              childAgent.ID,
			EnvironmentID:        environment.ID,
			Message:              "inspect auth",
			ExpectedResultSchema: json.RawMessage(`{"type":"object","required":["module"],"properties":{"module":{"type":"string"},"risk":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	item := created.Items[0]
	events, err := store.ListEvents(item.Session.ID, 0)
	if err != nil {
		t.Fatalf("list item events: %v", err)
	}
	turnID := ""
	for _, event := range events {
		if event.Type == managedagents.EventUserMessage {
			turnID = payloadString(event.Payload, "turn_id")
		}
	}
	if _, err := store.CompleteSessionTurn(item.Session.ID, turnID, json.RawMessage(`{"content":[{"type":"text","text":"done"}],"result_json":{"risk":"high"}}`)); err != nil {
		t.Fatalf("complete item turn: %v", err)
	}

	collected, err := service.CollectTaskGroup(t.Context(), tools.AgentTaskGroupCollectRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
	})
	if err != nil {
		t.Fatalf("collect task group: %v", err)
	}
	if collected.Items[0].ResultValid || collected.Items[0].Status != managedagents.TurnStatusFailed || !strings.Contains(collected.Items[0].ResultValidationError, ".module is required") {
		t.Fatalf("expected schema validation failure on item, got %#v", collected.Items[0])
	}
}

func TestBuildTaskGroupAggregateSupportsTextReducers(t *testing.T) {
	items := []tools.AgentTaskGroupItemState{
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 0}, Status: managedagents.TurnStatusCompleted, AgentText: "same"},
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 1}, Status: managedagents.TurnStatusCompleted, AgentText: "same"},
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 2}, Status: managedagents.TurnStatusCompleted, AgentText: "other"},
	}

	first := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerFirstSuccess}, items)
	if first.Text != "same" {
		t.Fatalf("expected first_success text, got %#v", first)
	}
	majority := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerMajorityText}, items)
	if majority.Text != "same" {
		t.Fatalf("expected majority_text text, got %#v", majority)
	}
}

func TestBuildTaskGroupAggregateSupportsValueReducers(t *testing.T) {
	items := []tools.AgentTaskGroupItemState{
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 0}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"module":"auth","risk":"high"}`)},
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 1}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"module":"billing","risk":"low"}`)},
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 2}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"module":"auth","risk":"high"}`)},
	}

	values := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerJSONValues}, items)
	var decodedValues []map[string]any
	if err := json.Unmarshal(values.JSON, &decodedValues); err != nil || len(decodedValues) != 3 {
		t.Fatalf("expected json_values aggregate, got %#v err=%v", values, err)
	}

	merged := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerMergeObjects}, items)
	var decodedMerged map[string]any
	if err := json.Unmarshal(merged.JSON, &decodedMerged); err != nil || decodedMerged["module"] != "auth" || decodedMerged["risk"] != "high" {
		t.Fatalf("expected merged object aggregate, got %#v err=%v", merged, err)
	}

	first := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerFirstValue}, items)
	var decodedFirst map[string]any
	if err := json.Unmarshal(first.JSON, &decodedFirst); err != nil || decodedFirst["module"] != "auth" {
		t.Fatalf("expected first value aggregate, got %#v err=%v", first, err)
	}

	majority := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerMajorityValue}, items)
	var decodedMajority map[string]any
	if err := json.Unmarshal(majority.JSON, &decodedMajority); err != nil || decodedMajority["module"] != "auth" || decodedMajority["risk"] != "high" {
		t.Fatalf("expected majority value aggregate, got %#v err=%v", majority, err)
	}
	if len(majority.Schema) == 0 {
		t.Fatalf("expected schema on majority value aggregate, got %#v", majority)
	}
}

func TestBuildTaskGroupAggregateMergeObjectsHonorsSchemaMergeModes(t *testing.T) {
	items := []tools.AgentTaskGroupItemState{
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 0, ExpectedResultSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","x-array-merge":"dedupe"},"risk":{"type":"string","x-conflict-mode":"first_wins"}}}`)}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"files":["a.go","b.go"],"risk":"high"}`)},
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 1, ExpectedResultSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","x-array-merge":"dedupe"},"risk":{"type":"string","x-conflict-mode":"first_wins"}}}`)}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"files":["b.go","c.go"],"risk":"low"}`)},
	}
	aggregate := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerMergeObjects}, items)
	var decoded map[string]any
	if err := json.Unmarshal(aggregate.JSON, &decoded); err != nil {
		t.Fatalf("decode merge_objects aggregate: %v", err)
	}
	files, ok := decoded["files"].([]any)
	if !ok || len(files) != 3 {
		t.Fatalf("expected deduped merged files, got %#v", decoded["files"])
	}
	if decoded["risk"] != "high" {
		t.Fatalf("expected first_wins risk, got %#v", decoded["risk"])
	}
}

func TestBuildTaskGroupAggregateMergeObjectsSupportsReplaceAndLastWins(t *testing.T) {
	items := []tools.AgentTaskGroupItemState{
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 0, ExpectedResultSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","x-array-merge":"replace"},"risk":{"type":"string","x-conflict-mode":"last_wins"}}}`)}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"files":["a.go"],"risk":"low"}`)},
		{Item: managedagents.SubagentTaskGroupItem{ItemIndex: 1, ExpectedResultSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","x-array-merge":"replace"},"risk":{"type":"string","x-conflict-mode":"last_wins"}}}`)}, Status: managedagents.TurnStatusCompleted, ResultJSON: json.RawMessage(`{"files":["z.go"],"risk":"high"}`)},
	}
	aggregate := buildTaskGroupAggregate(managedagents.SubagentTaskGroup{ResultReducer: managedagents.SubagentTaskGroupReducerMergeObjects}, items)
	var decoded map[string]any
	if err := json.Unmarshal(aggregate.JSON, &decoded); err != nil {
		t.Fatalf("decode merge_objects aggregate: %v", err)
	}
	files, ok := decoded["files"].([]any)
	if !ok || len(files) != 1 || files[0] != "z.go" {
		t.Fatalf("expected replace merged files, got %#v", decoded["files"])
	}
	if decoded["risk"] != "high" {
		t.Fatalf("expected last_wins risk, got %#v", decoded["risk"])
	}
}

func TestAgentToolServiceCancelTaskGroup(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_cancel",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect billing"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}

	canceled, err := service.CancelTaskGroup(t.Context(), tools.AgentTaskGroupCancelRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
		Reason:          "no longer needed",
	})
	if err != nil {
		t.Fatalf("cancel task group: %v", err)
	}
	if !canceled.Completed || canceled.Status != "canceled" || canceled.Group.CanceledAt == nil || canceled.Group.CancelReason != "no longer needed" {
		t.Fatalf("unexpected canceled task group response: %#v", canceled)
	}
	for index, item := range canceled.Items {
		if item.Session == nil || item.Session.Status != managedagents.SessionStatusTerminated {
			t.Fatalf("expected terminated session for item %d, got %#v", index, item)
		}
	}
}

func TestAgentToolServiceTaskGroupFailFastAutoCancelsOutstandingItems(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_fail_fast",
		FailFast:        true,
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect billing"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	if len(created.Items) != 2 {
		t.Fatalf("expected 2 items, got %#v", created.Items)
	}

	firstSession := created.Items[0].Session
	secondSession := created.Items[1].Session
	if firstSession == nil || secondSession == nil {
		t.Fatalf("expected started sessions, got %#v", created.Items)
	}

	firstEvents, err := store.ListEvents(firstSession.ID, 0)
	if err != nil {
		t.Fatalf("list first child events: %v", err)
	}
	firstTurnID := ""
	for _, event := range firstEvents {
		if event.Type == managedagents.EventUserMessage {
			firstTurnID = payloadString(event.Payload, "turn_id")
		}
	}
	if firstTurnID == "" {
		t.Fatal("expected first child turn id")
	}
	if _, err := store.FailSessionTurn(firstSession.ID, firstTurnID, "boom"); err != nil {
		t.Fatalf("fail first child turn: %v", err)
	}

	waited, err := service.WaitTaskGroup(t.Context(), tools.AgentTaskGroupWaitRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
		TimeoutSeconds:  1,
	})
	if err != nil {
		t.Fatalf("wait fail_fast task group: %v", err)
	}
	if waited.TimedOut || !waited.Completed || waited.Status != "canceled" || waited.Group.CanceledAt == nil || waited.Group.CancelReason != "fail_fast triggered by task group item failure" {
		t.Fatalf("unexpected fail_fast group response: %#v", waited)
	}

	secondReloaded, err := store.GetSession(secondSession.ID)
	if err != nil {
		t.Fatalf("reload second session: %v", err)
	}
	if secondReloaded.Status != managedagents.SessionStatusTerminated {
		t.Fatalf("expected outstanding second session terminated, got %#v", secondReloaded)
	}
}

func TestAgentToolServiceRetryTaskGroupItem(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_retry_item",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	firstSession := created.Items[0].Session
	if firstSession == nil {
		t.Fatalf("expected first session, got %#v", created.Items)
	}
	firstEvents, err := store.ListEvents(firstSession.ID, 0)
	if err != nil {
		t.Fatalf("list first session events: %v", err)
	}
	firstTurnID := ""
	for _, event := range firstEvents {
		if event.Type == managedagents.EventUserMessage {
			firstTurnID = payloadString(event.Payload, "turn_id")
		}
	}
	if _, err := store.FailSessionTurn(firstSession.ID, firstTurnID, "boom"); err != nil {
		t.Fatalf("fail first item: %v", err)
	}

	retried, err := service.RetryTaskGroupItem(t.Context(), tools.AgentTaskGroupRetryItemRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
		ItemIndex:       0,
	})
	if err != nil {
		t.Fatalf("retry task group item: %v", err)
	}
	if retried.Items[0].Item.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %#v", retried.Items[0])
	}
	if retried.Items[0].Session == nil || retried.Items[0].Session.ID == firstSession.ID {
		t.Fatalf("expected fresh retried session, got %#v", retried.Items[0])
	}
	if retried.Status != "running" {
		t.Fatalf("expected running group after retry, got %#v", retried)
	}
}

func TestAgentToolServiceRetryTaskGroupReactivatesCanceledGroup(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	created, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_group_retry_all",
		FailFast:        true,
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect billing"},
		},
	})
	if err != nil {
		t.Fatalf("create task group: %v", err)
	}
	firstSession := created.Items[0].Session
	if firstSession == nil {
		t.Fatalf("expected first session, got %#v", created.Items)
	}
	firstEvents, err := store.ListEvents(firstSession.ID, 0)
	if err != nil {
		t.Fatalf("list first session events: %v", err)
	}
	firstTurnID := ""
	for _, event := range firstEvents {
		if event.Type == managedagents.EventUserMessage {
			firstTurnID = payloadString(event.Payload, "turn_id")
		}
	}
	if _, err := store.FailSessionTurn(firstSession.ID, firstTurnID, "boom"); err != nil {
		t.Fatalf("fail first item: %v", err)
	}
	failedFast, err := service.WaitTaskGroup(t.Context(), tools.AgentTaskGroupWaitRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
		TimeoutSeconds:  1,
	})
	if err != nil {
		t.Fatalf("wait fail_fast group: %v", err)
	}
	if failedFast.Status != "canceled" || failedFast.Group.CanceledAt == nil {
		t.Fatalf("expected canceled fail_fast group, got %#v", failedFast)
	}

	retried, err := service.RetryTaskGroup(t.Context(), tools.AgentTaskGroupRetryRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         created.Group.ID,
	})
	if err != nil {
		t.Fatalf("retry task group: %v", err)
	}
	if retried.Group.CanceledAt != nil || retried.Group.CancelReason != "" {
		t.Fatalf("expected reactivated group, got %#v", retried.Group)
	}
	if retried.Status != "running" {
		t.Fatalf("expected running group after retry, got %#v", retried)
	}
	retriedSessions := 0
	for _, item := range retried.Items {
		if item.Item.RetryCount != 1 {
			t.Fatalf("expected retry count on all retried items, got %#v", retried.Items)
		}
		if item.Session != nil {
			retriedSessions++
		}
	}
	if retriedSessions != 2 {
		t.Fatalf("expected both canceled items retried, got %#v", retried.Items)
	}
}

func TestAgentToolServiceGetTaskGroupIncludesNestedGroups(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	root, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_root_group",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create root task group: %v", err)
	}
	childSession := root.Items[0].Session
	if childSession == nil {
		t.Fatalf("expected child session, got %#v", root.Items)
	}

	nested, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: childSession.ID,
		ParentTurnID:    "turn_nested_group",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "deep inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create nested task group: %v", err)
	}
	if nested.Group.ParentGroupID != root.Group.ID || nested.Group.ParentItemIndex != 0 {
		t.Fatalf("expected nested lineage, got %#v", nested.Group)
	}

	loaded, err := service.GetTaskGroup(t.Context(), tools.AgentTaskGroupRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         root.Group.ID,
	})
	if err != nil {
		t.Fatalf("get root task group: %v", err)
	}
	if len(loaded.Items) != 1 || len(loaded.Items[0].NestedGroups) != 1 {
		t.Fatalf("expected nested groups on root item, got %#v", loaded.Items)
	}
	if loaded.Items[0].NestedGroups[0].Group.ID != nested.Group.ID {
		t.Fatalf("expected nested group %q, got %#v", nested.Group.ID, loaded.Items[0].NestedGroups)
	}
}

func TestAgentToolServiceNestedAggregatePropagatesSchema(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	root, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_root_schema_nested",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create root task group: %v", err)
	}
	childSession := root.Items[0].Session
	if childSession == nil {
		t.Fatalf("expected child session, got %#v", root.Items)
	}
	nested, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: childSession.ID,
		ParentTurnID:    "turn_nested_schema",
		ResultReducer:   managedagents.SubagentTaskGroupReducerMergeObjects,
		Items: []tools.AgentTaskGroupItemRequest{{
			AgentID:              childAgent.ID,
			EnvironmentID:        environment.ID,
			Message:              "deep inspect auth",
			ExpectedResultSchema: json.RawMessage(`{"type":"object","properties":{"module":{"type":"string"},"risk":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("create nested task group: %v", err)
	}
	nestedItem := nested.Items[0].Session
	if nestedItem == nil {
		t.Fatalf("expected nested item session, got %#v", nested.Items)
	}
	nestedEvents, err := store.ListEvents(nestedItem.ID, 0)
	if err != nil {
		t.Fatalf("list nested item events: %v", err)
	}
	nestedTurnID := ""
	for _, event := range nestedEvents {
		if event.Type == managedagents.EventUserMessage {
			nestedTurnID = payloadString(event.Payload, "turn_id")
		}
	}
	if _, err := store.CompleteSessionTurn(nestedItem.ID, nestedTurnID, json.RawMessage(`{"content":[{"type":"text","text":"nested done"}],"result_json":{"module":"auth","risk":"high"}}`)); err != nil {
		t.Fatalf("complete nested item turn: %v", err)
	}

	loaded, err := service.GetTaskGroup(t.Context(), tools.AgentTaskGroupRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         root.Group.ID,
	})
	if err != nil {
		t.Fatalf("get root task group: %v", err)
	}
	nestedState := loaded.Items[0].NestedGroups[0]
	if nestedState.Aggregate.Reducer != managedagents.SubagentTaskGroupReducerMergeObjects || len(nestedState.Aggregate.Schema) == 0 || len(nestedState.Aggregate.JSON) == 0 {
		t.Fatalf("expected nested aggregate schema/json, got %#v", nestedState.Aggregate)
	}
	if len(loaded.Items[0].ResultSchema) == 0 {
		t.Fatalf("expected parent item to expose nested result schema, got %#v", loaded.Items[0])
	}
}

func TestAgentToolServiceCancelTaskGroupPropagatesToNestedGroups(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	root, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_root_cancel_nested",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create root task group: %v", err)
	}
	childSession := root.Items[0].Session
	if childSession == nil {
		t.Fatalf("expected child session, got %#v", root.Items)
	}
	nested, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: childSession.ID,
		ParentTurnID:    "turn_nested_cancel",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "deep inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create nested task group: %v", err)
	}

	canceled, err := service.CancelTaskGroup(t.Context(), tools.AgentTaskGroupCancelRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         root.Group.ID,
		Reason:          "cancel tree",
	})
	if err != nil {
		t.Fatalf("cancel root task group: %v", err)
	}
	if canceled.Status != "canceled" || canceled.Group.CanceledAt == nil {
		t.Fatalf("expected canceled root, got %#v", canceled)
	}
	nestedLoaded, err := service.GetTaskGroup(t.Context(), tools.AgentTaskGroupRequest{
		ParentSessionID: childSession.ID,
		GroupID:         nested.Group.ID,
	})
	if err != nil {
		t.Fatalf("load nested task group: %v", err)
	}
	if nestedLoaded.Status != "canceled" || nestedLoaded.Group.CanceledAt == nil || nestedLoaded.Group.CancelReason != "canceled by ancestor task group" {
		t.Fatalf("expected nested group canceled by ancestor, got %#v", nestedLoaded)
	}
}

func TestAgentToolServiceRetryTaskGroupPropagatesToNestedGroups(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	root, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_root_retry_nested",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create root task group: %v", err)
	}
	childSession := root.Items[0].Session
	if childSession == nil {
		t.Fatalf("expected child session, got %#v", root.Items)
	}
	nested, err := service.CreateTaskGroup(t.Context(), tools.AgentTaskGroupCreateRequest{
		ParentSessionID: childSession.ID,
		ParentTurnID:    "turn_nested_retry",
		Items: []tools.AgentTaskGroupItemRequest{
			{AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "deep inspect auth"},
		},
	})
	if err != nil {
		t.Fatalf("create nested task group: %v", err)
	}
	nestedItemSession := nested.Items[0].Session
	if nestedItemSession == nil {
		t.Fatalf("expected nested item session, got %#v", nested.Items)
	}
	nestedEvents, err := store.ListEvents(nestedItemSession.ID, 0)
	if err != nil {
		t.Fatalf("list nested item events: %v", err)
	}
	nestedTurnID := ""
	for _, event := range nestedEvents {
		if event.Type == managedagents.EventUserMessage {
			nestedTurnID = payloadString(event.Payload, "turn_id")
		}
	}
	if _, err := store.FailSessionTurn(nestedItemSession.ID, nestedTurnID, "nested boom"); err != nil {
		t.Fatalf("fail nested item turn: %v", err)
	}

	retried, err := service.RetryTaskGroup(t.Context(), tools.AgentTaskGroupRetryRequest{
		ParentSessionID: parentSession.ID,
		GroupID:         root.Group.ID,
	})
	if err != nil {
		t.Fatalf("retry root task group: %v", err)
	}
	if len(retried.Items) != 1 || len(retried.Items[0].NestedGroups) != 1 {
		t.Fatalf("expected nested groups after retry, got %#v", retried.Items)
	}
	nestedRetried := retried.Items[0].NestedGroups[0]
	if nestedRetried.Items[0].Item.RetryCount != 1 {
		t.Fatalf("expected nested retry count 1, got %#v", nestedRetried.Items)
	}
	if nestedRetried.Items[0].Session == nil || nestedRetried.Items[0].Session.ID == nestedItemSession.ID {
		t.Fatalf("expected fresh nested retried session, got %#v", nestedRetried.Items[0])
	}
}

func TestAgentToolServiceSpawnRejectsDepthBeyondLimit(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	parentSession.SpawnDepth = maxSubagentSpawnDepth
	store.sessions[parentSession.ID] = parentSession
	service := newAgentToolService(store, &recordingRunner{}, nil, defaultSubagentPolicy())

	_, err := service.Spawn(t.Context(), tools.AgentSpawnRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_000001",
		AgentID:         childAgent.ID,
		Message:         "one level too deep",
	})
	if err == nil {
		t.Fatal("expected spawn depth limit error")
	}
}

func TestAgentToolServiceSpawnRejectsPerTurnFanoutLimit(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	service := newAgentToolService(store, &recordingRunner{}, nil, defaultSubagentPolicy())
	parentEvents, err := store.AppendEvents(parentSession.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"delegate work"}]}`),
	}})
	if err != nil {
		t.Fatalf("start parent turn: %v", err)
	}
	parentTurnID := ""
	for _, event := range parentEvents {
		if event.Type == managedagents.EventUserMessage {
			parentTurnID = payloadString(event.Payload, "turn_id")
		}
	}
	if parentTurnID == "" {
		t.Fatal("expected parent turn id")
	}

	for index := 0; index < maxSubagentChildrenPerTurn; index++ {
		_, err := store.CreateSession(managedagents.CreateSessionInput{
			WorkspaceID:     managedagents.DefaultWorkspaceID,
			AgentID:         childAgent.ID,
			EnvironmentID:   environment.ID,
			ParentSessionID: parentSession.ID,
			ParentTurnID:    parentTurnID,
			SpawnDepth:      1,
			Title:           "child",
			CreatedBy:       "seed",
		})
		if err != nil {
			t.Fatalf("seed child session %d: %v", index, err)
		}
	}

	_, err = service.Spawn(t.Context(), tools.AgentSpawnRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    parentTurnID,
		AgentID:         childAgent.ID,
		Message:         "should be rejected",
	})
	if err == nil {
		t.Fatal("expected per-turn fanout limit error")
	}
	var toolErr tools.AgentToolError
	if !errors.As(err, &toolErr) || toolErr.Type != "subagent_turn_fanout_limit" {
		t.Fatalf("expected structured fanout error, got %T: %v", err, err)
	}

	events, err := store.ListEvents(parentSession.ID, 0)
	if err != nil {
		t.Fatalf("list parent events: %v", err)
	}
	var rejection *managedagents.Event
	for index := range events {
		if events[index].Type == managedagents.EventRuntimeSubagentSpawnRejected {
			rejection = &events[index]
		}
	}
	if rejection == nil {
		t.Fatalf("expected subagent spawn rejection event, got %#v", events)
	}
	if got := payloadString(rejection.Payload, "error_type"); got != "subagent_turn_fanout_limit" {
		t.Fatalf("unexpected rejection error type %q", got)
	}
	if got := payloadString(rejection.Payload, "policy"); got != "max_children_per_turn" {
		t.Fatalf("unexpected rejection policy %q", got)
	}
	if got := payloadString(rejection.Payload, "parent_turn_id"); got != parentTurnID {
		t.Fatalf("unexpected rejection parent turn %q", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(rejection.Payload, &payload); err != nil {
		t.Fatalf("decode rejection payload: %v", err)
	}
	if payload["limit"] != float64(maxSubagentChildrenPerTurn) || payload["current_children"] != float64(maxSubagentChildrenPerTurn) {
		t.Fatalf("unexpected rejection counters: %#v", payload)
	}
}

func TestAgentToolServiceSpawnRejectsPerSessionChildLimit(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	service := newAgentToolService(store, &recordingRunner{}, nil, defaultSubagentPolicy())

	for index := 0; index < maxSubagentChildrenPerSession; index++ {
		_, err := store.CreateSession(managedagents.CreateSessionInput{
			WorkspaceID:     managedagents.DefaultWorkspaceID,
			AgentID:         childAgent.ID,
			EnvironmentID:   environment.ID,
			ParentSessionID: parentSession.ID,
			ParentTurnID:    "turn_seed",
			SpawnDepth:      1,
			Title:           "child",
			CreatedBy:       "seed",
		})
		if err != nil {
			t.Fatalf("seed child session %d: %v", index, err)
		}
	}

	_, err := service.Spawn(t.Context(), tools.AgentSpawnRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_new",
		AgentID:         childAgent.ID,
		Message:         "should be rejected",
	})
	if err == nil {
		t.Fatal("expected per-session child limit error")
	}
}

func TestAgentToolServiceConcurrentSpawnDoesNotExceedPerTurnLimit(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	policy := defaultSubagentPolicy()
	policy.MaxChildrenPerTurn = 5
	policy.MaxChildrenPerSession = 100
	policy.WorkspaceActiveLimit = 0
	policy.UserActiveLimit = 0
	service := newAgentToolService(store, &recordingRunner{}, nil, policy)

	const attempts = 40
	start := make(chan struct{})
	errorsByAttempt := make(chan error, attempts)
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := service.Spawn(t.Context(), tools.AgentSpawnRequest{
				ParentSessionID: parentSession.ID,
				ParentTurnID:    "turn_concurrent",
				AgentID:         childAgent.ID,
				Title:           "concurrent child",
			})
			if err == nil {
				successes.Add(1)
				return
			}
			errorsByAttempt <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsByAttempt)

	if got := successes.Load(); got != int64(policy.MaxChildrenPerTurn) {
		t.Fatalf("expected %d successful spawns, got %d", policy.MaxChildrenPerTurn, got)
	}
	for err := range errorsByAttempt {
		var toolErr tools.AgentToolError
		if !errors.As(err, &toolErr) || toolErr.Type != "subagent_turn_fanout_limit" {
			t.Fatalf("expected structured fanout rejection, got %T: %v", err, err)
		}
	}
	children, err := store.ListSessions(managedagents.ListSessionsInput{
		WorkspaceID:     parentSession.WorkspaceID,
		ParentSessionID: parentSession.ID,
		ParentTurnID:    "turn_concurrent",
		IncludeArchived: true,
		Limit:           attempts,
	})
	if err != nil {
		t.Fatalf("list concurrent children: %v", err)
	}
	if len(children) != policy.MaxChildrenPerTurn {
		t.Fatalf("expected exactly %d persisted children, got %d", policy.MaxChildrenPerTurn, len(children))
	}
}

func TestAgentToolServiceSendMessageQueuesAtActiveQuotaAndAuditsParentTurn(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	parentEvents, err := store.AppendEvents(parentSession.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"delegate"}]}`),
	}})
	if err != nil {
		t.Fatalf("start parent turn: %v", err)
	}
	parentTurnID := payloadString(parentEvents[1].Payload, "turn_id")
	children := make([]managedagents.Session, 0, 3)
	for index := 0; index < 3; index++ {
		child, err := store.CreateSubagentSession(managedagents.CreateSubagentSessionInput{
			Session: managedagents.CreateSessionInput{
				AgentID:         childAgent.ID,
				EnvironmentID:   environment.ID,
				ParentSessionID: parentSession.ID,
				ParentTurnID:    parentTurnID,
			},
			Limits: managedagents.SubagentLimits{MaxDepth: 3, MaxChildrenPerTurn: 5, MaxChildrenPerSession: 20},
		})
		if err != nil {
			t.Fatalf("create child %d: %v", index, err)
		}
		children = append(children, child)
	}
	policy := defaultSubagentPolicy()
	policy.WorkspaceActiveLimit = 1
	policy.UserActiveLimit = 10
	policy.WorkspaceQueuedLimit = 1
	policy.UserQueuedLimit = 10
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, policy)

	if _, err := service.SendMessage(t.Context(), tools.AgentSendMessageRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    parentTurnID,
		SessionID:       children[0].ID,
		Message:         "start first child",
	}); err != nil {
		t.Fatalf("start first child: %v", err)
	}
	queued, err := service.SendMessage(t.Context(), tools.AgentSendMessageRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    parentTurnID,
		SessionID:       children[1].ID,
		Message:         "start second child",
	})
	if err != nil {
		t.Fatalf("queue second child: %v", err)
	}
	if !queued.Queued || queued.Started || queued.QueueRequest == nil || queued.QueueRequest.SessionID != children[1].ID || queued.QueueRequest.Status != "pending" {
		t.Fatalf("unexpected queued response: %#v", queued)
	}
	queuedSession, err := service.GetSession(t.Context(), tools.AgentSessionRequest{ParentSessionID: parentSession.ID, SessionID: children[1].ID})
	if err != nil || queuedSession.Status != "queued" || queuedSession.QueueRequest == nil {
		t.Fatalf("expected queued derived session state, response=%#v err=%v", queuedSession, err)
	}
	_, err = service.SendMessage(t.Context(), tools.AgentSendMessageRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    parentTurnID,
		SessionID:       children[2].ID,
		Message:         "queue is full",
	})
	var toolErr tools.AgentToolError
	if !errors.As(err, &toolErr) || toolErr.Type != "subagent_workspace_queue_limit" {
		t.Fatalf("expected structured queue quota error, got %T: %v", err, err)
	}
	canceled, err := service.CancelStart(t.Context(), tools.AgentCancelStartRequest{
		ParentSessionID: parentSession.ID, SessionID: children[1].ID, Reason: "no longer needed",
	})
	if err != nil || canceled.QueueRequest.Status != "canceled" || canceled.QueueRequest.CancelReason != "no longer needed" {
		t.Fatalf("cancel queued start: response=%#v err=%v", canceled, err)
	}
	afterCancel, err := service.GetSession(t.Context(), tools.AgentSessionRequest{ParentSessionID: parentSession.ID, SessionID: children[1].ID})
	if err != nil || afterCancel.Status != managedagents.SessionStatusIdle || afterCancel.QueueRequest != nil {
		t.Fatalf("expected idle state after queue cancellation, response=%#v err=%v", afterCancel, err)
	}
	childEvents, err := store.ListEvents(children[1].ID, 0)
	if err != nil {
		t.Fatalf("list canceled child events: %v", err)
	}
	foundCanceled := false
	for _, event := range childEvents {
		if event.Type == managedagents.EventRuntimeSubagentStartCanceled && payloadString(event.Payload, "reason") == "no longer needed" {
			foundCanceled = true
		}
	}
	if !foundCanceled {
		t.Fatalf("expected canceled event on child, got %#v", childEvents)
	}
	if len(runner.starts) != 1 || runner.starts[0].SessionID != children[0].ID {
		t.Fatalf("expected only first child to start, got %#v", runner.starts)
	}
	events, err := store.ListEvents(parentSession.ID, 0)
	if err != nil {
		t.Fatalf("list parent events: %v", err)
	}
	queuedAudited := false
	rejectedAudited := false
	for _, event := range events {
		if event.Type == managedagents.EventRuntimeSubagentStartQueued && payloadString(event.Payload, "subagent_session_id") == children[1].ID {
			queuedAudited = true
		}
		if event.Type == managedagents.EventRuntimeSubagentStartRejected && payloadString(event.Payload, "subagent_session_id") == children[2].ID {
			rejectedAudited = true
		}
	}
	if !queuedAudited || !rejectedAudited {
		t.Fatalf("expected queued and rejected audit events, got %#v", events)
	}
}

func TestSubagentActiveAdmissionConcurrentStartsDoNotExceedWorkspaceLimit(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	const attempts = 30
	children := make([]managedagents.Session, 0, attempts)
	for index := 0; index < attempts; index++ {
		child, err := store.CreateSubagentSession(managedagents.CreateSubagentSessionInput{
			Session: managedagents.CreateSessionInput{AgentID: childAgent.ID, EnvironmentID: environment.ID, ParentSessionID: parentSession.ID, ParentTurnID: "turn_active"},
			Limits:  managedagents.SubagentLimits{MaxDepth: 3, MaxChildrenPerTurn: attempts, MaxChildrenPerSession: attempts},
		})
		if err != nil {
			t.Fatalf("create child %d: %v", index, err)
		}
		children = append(children, child)
	}
	limits := managedagents.SubagentLimits{WorkspaceActiveLimit: 5, UserActiveLimit: attempts}
	start := make(chan struct{})
	errorsByAttempt := make(chan error, attempts)
	var successes atomic.Int64
	var waitGroup sync.WaitGroup
	for index := range children {
		waitGroup.Add(1)
		go func(child managedagents.Session) {
			defer waitGroup.Done()
			<-start
			_, err := store.StartSubagentTurn(managedagents.StartSubagentTurnInput{
				SessionID: child.ID,
				Payload:   json.RawMessage(`{"content":[{"type":"text","text":"run"}]}`),
				Limits:    limits,
			})
			if err == nil {
				successes.Add(1)
				return
			}
			errorsByAttempt <- err
		}(children[index])
	}
	close(start)
	waitGroup.Wait()
	close(errorsByAttempt)
	if got := successes.Load(); got != int64(limits.WorkspaceActiveLimit) {
		t.Fatalf("expected %d active children, got %d", limits.WorkspaceActiveLimit, got)
	}
	for err := range errorsByAttempt {
		var violation managedagents.SubagentQuotaViolation
		if !errors.As(err, &violation) || violation.Type != "subagent_workspace_active_limit" {
			t.Fatalf("expected workspace active violation, got %T: %v", err, err)
		}
	}
	active, err := store.ListSessions(managedagents.ListSessionsInput{WorkspaceID: parentSession.WorkspaceID, ParentedOnly: true, Status: managedagents.SessionStatusRunning, Limit: attempts})
	if err != nil {
		t.Fatalf("list active children: %v", err)
	}
	if len(active) != limits.WorkspaceActiveLimit {
		t.Fatalf("expected exactly %d persisted running children, got %d", limits.WorkspaceActiveLimit, len(active))
	}
}

func TestAgentToolServiceApproveToolResumesChildTurn(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	childSession := mustCreateSessionForSubagentTest(t, store, childAgent.ID, environment.ID, "child-session")
	runner := &recordingRunner{}
	service := newAgentToolService(store, runner, nil, defaultSubagentPolicy())

	store.sessions[childSession.ID] = managedagents.Session{
		ID:                 childSession.ID,
		WorkspaceID:        childSession.WorkspaceID,
		OwnerID:            childSession.OwnerID,
		AgentID:            childSession.AgentID,
		AgentConfigVersion: childSession.AgentConfigVersion,
		EnvironmentID:      childSession.EnvironmentID,
		Status:             managedagents.SessionStatusRunning,
		Title:              childSession.Title,
		RuntimeSettings:    childSession.RuntimeSettings,
		CreatedBy:          childSession.CreatedBy,
		CreatedAt:          childSession.CreatedAt,
	}
	intervention, err := store.SaveSessionIntervention(childSession.ID, managedagents.SaveSessionInterventionInput{
		TurnID:           "turn_000001",
		CallID:           "call_approve",
		ToolIdentifier:   "default",
		APIName:          "run_command",
		InterventionMode: "request_approval",
		Continuation:     json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"please run"}]}]`),
	})
	if err != nil {
		t.Fatalf("save intervention: %v", err)
	}

	response, err := service.ApproveTool(t.Context(), tools.AgentInterventionDecisionRequest{
		ParentSessionID: parentSession.ID,
		SessionID:       childSession.ID,
		TurnID:          intervention.TurnID,
		CallID:          intervention.CallID,
		Reason:          "looks safe",
	})
	if err != nil {
		t.Fatalf("approve intervention: %v", err)
	}
	if !response.Resumed || len(runner.starts) != 1 {
		t.Fatalf("expected approve to resume runner, response=%#v starts=%#v", response, runner.starts)
	}
	if runner.starts[0].ResumeIntervention == nil || runner.starts[0].ResumeIntervention.CallID != intervention.CallID {
		t.Fatalf("expected resumed intervention on runner start, got %#v", runner.starts[0])
	}
}

func TestAgentToolServiceStreamEventsWaitsForNewEvent(t *testing.T) {
	store := newTestStore()
	parentAgent := mustCreateAgentForSubagentTest(t, store, "Parent Agent")
	childAgent := mustCreateAgentForSubagentTest(t, store, "Child Agent")
	environment := mustCreateEnvironmentForSubagentTest(t, store)
	parentSession := mustCreateSessionForSubagentTest(t, store, parentAgent.ID, environment.ID, "parent-session")
	childSession := mustCreateSessionForSubagentTest(t, store, childAgent.ID, environment.ID, "child-session")
	service := newAgentToolService(store, &recordingRunner{}, nil, defaultSubagentPolicy())

	done := make(chan tools.AgentStreamEventsResponse, 1)
	errs := make(chan error, 1)
	go func() {
		response, err := service.StreamEvents(context.Background(), tools.AgentStreamEventsRequest{
			ParentSessionID: parentSession.ID,
			SessionID:       childSession.ID,
			AfterSeq:        2,
			WaitSeconds:     1,
			Limit:           1,
		})
		if err != nil {
			errs <- err
			return
		}
		done <- response
	}()

	time.Sleep(100 * time.Millisecond)
	payload, err := json.Marshal(map[string]any{
		"content": []map[string]string{{
			"type": "text",
			"text": "hello child",
		}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := store.AppendEvents(childSession.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: payload,
	}}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	select {
	case err := <-errs:
		t.Fatalf("stream events failed: %v", err)
	case response := <-done:
		if response.TimedOut || len(response.Events) != 1 {
			t.Fatalf("unexpected streamed response: %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed response")
	}
}

func mustCreateAgentForSubagentTest(t *testing.T, store *testStore, name string) managedagents.Agent {
	t.Helper()
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: managedagents.DefaultWorkspaceID,
		Name:        name,
		LLMProvider: "fake",
		LLMModel:    "fake-demo",
		System:      "You are helpful.",
	})
	if err != nil {
		t.Fatalf("create agent %s: %v", name, err)
	}
	return agent
}

func mustCreateEnvironmentForSubagentTest(t *testing.T, store *testStore) managedagents.Environment {
	t.Helper()
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{
		WorkspaceID: managedagents.DefaultWorkspaceID,
		Name:        "test-env",
		Config:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	return environment
}

func mustCreateSessionForSubagentTest(t *testing.T, store *testStore, agentID string, environmentID string, title string) managedagents.Session {
	t.Helper()
	session, err := store.CreateSession(managedagents.CreateSessionInput{
		WorkspaceID:   managedagents.DefaultWorkspaceID,
		AgentID:       agentID,
		EnvironmentID: environmentID,
		Title:         title,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session
}
