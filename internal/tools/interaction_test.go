package tools

import (
	"encoding/json"
	"testing"
)

func TestParseAskUserRequestModes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "select", raw: `{"question":"Deployment?","mode":"select","choices":[{"id":"private","label":"Private"},{"id":"saas","label":"SaaS"}]}`},
		{name: "freeform", raw: `{"question":"What region should be used?","mode":"freeform"}`},
		{name: "form", raw: `{"question":"Provide deployment settings","mode":"form","fields":[{"id":"region","label":"Region","type":"text","required":true}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := ParseAskUserRequest(json.RawMessage(test.raw))
			if err != nil {
				t.Fatalf("parse request: %v", err)
			}
			if request.Question == "" || request.Mode == "" {
				t.Fatalf("unexpected request: %#v", request)
			}
		})
	}
}

func TestParseAskUserRequestRejectsInvalidChoiceSet(t *testing.T) {
	_, err := ParseAskUserRequest(json.RawMessage(`{"question":"Deployment?","mode":"select","choices":[{"id":"private","label":"Private"}]}`))
	if err == nil {
		t.Fatal("expected select mode with one choice to be rejected")
	}
}

func TestInteractionManifestSeparatesClarificationFromApproval(t *testing.T) {
	manifest := (InteractionRuntime{}).Manifest()
	if manifest.Identifier != InteractionIdentifier || len(manifest.API) != 3 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	askAPI := manifest.API[0]
	uploadAPI := manifest.API[1]
	approvalAPI := manifest.API[2]
	if manifest.ApprovalPolicy != ApprovalPolicyNever || askAPI.ApprovalPolicy != "" || uploadAPI.ApprovalPolicy != "" || approvalAPI.ApprovalPolicy != "" {
		t.Fatalf("interaction parking must not use tool approval metadata: %#v", manifest.API)
	}
	if askAPI.Name != InteractionAPIAskUser || !IsAskUserCall(Call{Identifier: InteractionIdentifier, APIName: InteractionAPIAskUser}) {
		t.Fatalf("unexpected ask_user API: %#v", askAPI)
	}
	if uploadAPI.Name != InteractionAPIRequestUpload || !IsUploadRequestCall(Call{Identifier: InteractionIdentifier, APIName: InteractionAPIRequestUpload}) {
		t.Fatalf("unexpected request_upload API: %#v", uploadAPI)
	}
	if approvalAPI.Name != InteractionAPIRequestPlanApproval || !IsPlanApprovalCall(Call{Identifier: InteractionIdentifier, APIName: InteractionAPIRequestPlanApproval}) {
		t.Fatalf("unexpected request_plan_approval API: %#v", approvalAPI)
	}
}

func TestParseUploadRequest(t *testing.T) {
	request, err := ParseUploadRequest(json.RawMessage(`{
		"prompt": "Upload the signed PDF",
		"accept": [".pdf", "application/pdf", ".pdf"],
		"max_files": 2,
		"max_bytes": 1048576,
		"required": true,
		"reason": "Need the signed version",
		"upload_hint": "PDF only"
	}`))
	if err != nil {
		t.Fatalf("parse upload request: %v", err)
	}
	if request.Prompt != "Upload the signed PDF" || request.MaxFiles != 2 || len(request.Accept) != 2 {
		t.Fatalf("unexpected upload request: %#v", request)
	}
	defaulted, err := ParseUploadRequest(json.RawMessage(`{"prompt":"Upload input data"}`))
	if err != nil {
		t.Fatalf("parse default upload request: %v", err)
	}
	if defaulted.MaxFiles != 1 {
		t.Fatalf("expected max_files default 1, got %#v", defaulted)
	}
	if _, err := ParseUploadRequest(json.RawMessage(`{"prompt":"x","max_files":11}`)); err == nil {
		t.Fatal("expected excessive max_files to fail")
	}
}

func TestParsePlanApprovalRequest(t *testing.T) {
	request, err := ParsePlanApprovalRequest(json.RawMessage(`{"plan_id":" plan_1 ","summary":" Review rollout "}`))
	if err != nil {
		t.Fatalf("parse plan approval: %v", err)
	}
	if request.PlanID != "plan_1" || request.Summary != "Review rollout" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if _, err := ParsePlanApprovalRequest(json.RawMessage(`{"summary":`)); err == nil {
		t.Fatal("expected malformed request to fail")
	}
}
