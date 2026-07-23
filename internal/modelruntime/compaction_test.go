package modelruntime

import (
	"context"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tokenestimate"
)

func TestEstimateCoreMessagesCountsDecodedContentInsteadOfJSONEscaping(t *testing.T) {
	text := strings.Repeat(`{"name":"tool","description":"value"}`, 200)
	messages := []coremodel.Message{{
		ID: "system", Role: coremodel.RoleSystem, Visibility: coremodel.VisibilityInternal,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: text}},
	}}

	want := 4 + tokenestimate.Text(text)
	if got := estimateCoreMessages(messages); got != want {
		t.Fatalf("estimateCoreMessages() = %d, want %d", got, want)
	}

	compactor := LLMCompactor{Model: stubCompactionModel{}, ThresholdTokens: want + 1}
	if compactor.NeedsCompaction(agentcore.State{Messages: messages}) {
		t.Fatal("JSON transport escaping must not trigger context compaction")
	}
}

type stubCompactionModel struct{}

func (stubCompactionModel) Generate(_ context.Context, _ coremodel.Request, _ agentcore.DeltaSink) (coremodel.Response, error) {
	return coremodel.Response{}, nil
}
