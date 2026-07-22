package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestInterventionPolicyUsesTypedApprovalMetadata(t *testing.T) {
	manifest := Manifest{ApprovalPolicy: ApprovalPolicyAlways, ApprovalReason: "manifest_default"}

	manual := InterventionPolicy{Mode: InterventionModeRequestApproval}.Evaluate(manifest, API{Risk: ToolRiskWrite})
	if manual.Allowed || !manual.Required || manual.ApprovalPolicy != ApprovalPolicyAlways || manual.Reason != "manifest_default" || manual.Risk != ToolRiskWrite {
		t.Fatalf("unexpected manual decision: %#v", manual)
	}

	auto := InterventionPolicy{Mode: InterventionModeApproveForMe}.Evaluate(manifest, API{Risk: ToolRiskWrite})
	if !auto.Allowed || !auto.Required || auto.Reason != "manifest_default" {
		t.Fatalf("unexpected automatic decision: %#v", auto)
	}

	never := InterventionPolicy{Mode: InterventionModeRequestApproval}.Evaluate(manifest, API{
		Risk: ToolRiskWrite, ApprovalPolicy: ApprovalPolicyNever,
	})
	if !never.Allowed || never.Required || never.ApprovalPolicy != ApprovalPolicyNever || never.Reason != "" {
		t.Fatalf("api-level never policy must override manifest default: %#v", never)
	}
}

func TestInterventionPolicyDefaultsReadOnlyAPIToNever(t *testing.T) {
	decision := InterventionPolicy{Mode: InterventionModeRequestApproval}.Evaluate(Manifest{}, API{Risk: ToolRiskRead})
	if !decision.Allowed || decision.Required || decision.ApprovalPolicy != ApprovalPolicyNever || decision.Reason != "" {
		t.Fatalf("unexpected read-only decision: %#v", decision)
	}
}

func TestDefaultManifestDeclaresTypedApprovalMetadata(t *testing.T) {
	registry := DefaultRegistry()
	expected := map[string]string{
		"run_command": ApprovalPolicyAlways, "execute_code": ApprovalPolicyAlways,
		"write_file": ApprovalPolicyConditional, "edit_file": ApprovalPolicyConditional,
	}
	for name, expectedPolicy := range expected {
		_, api, ok := registry.GetAPI(DefaultIdentifier, name)
		if !ok {
			t.Fatalf("missing default api %q", name)
		}
		if api.ApprovalPolicy != expectedPolicy || api.ApprovalReason == "" {
			t.Fatalf("api %q approval metadata = policy %q reason %q", name, api.ApprovalPolicy, api.ApprovalReason)
		}
	}
}

func TestValidateManifestPermissionsRejectsAmbiguousMetadata(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		want     string
	}{
		{
			name: "unknown policy",
			manifest: Manifest{API: []API{{
				Name: "edit", ApprovalPolicy: "sometimes", ApprovalReason: "write",
			}}},
			want: "invalid approval_policy",
		},
		{
			name: "reason without policy",
			manifest: Manifest{API: []API{{
				Name: "edit", ApprovalReason: "write",
			}}},
			want: "requires approval_policy",
		},
		{
			name: "never with reason",
			manifest: Manifest{API: []API{{
				Name: "edit", ApprovalPolicy: ApprovalPolicyNever, ApprovalReason: "write",
			}}},
			want: "cannot declare an approval reason",
		},
		{
			name: "always without reason",
			manifest: Manifest{API: []API{{
				Name: "edit", ApprovalPolicy: ApprovalPolicyAlways,
			}}},
			want: "requires approval_reason",
		},
		{
			name: "write without policy",
			manifest: Manifest{API: []API{{
				Name: "edit", Risk: ToolRiskWrite,
			}}},
			want: "risk write requires approval_policy",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateManifestPermissions(test.manifest)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRegistryRejectsInvalidApprovalMetadata(t *testing.T) {
	registry := Registry{runtimes: map[string]Runtime{}}
	err := registry.RegisterChecked(ManifestRuntime{ManifestData: Manifest{
		Identifier: "invalid_permissions",
		API: []API{{
			Name: "write", ApprovalPolicy: ApprovalPolicyAlways,
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "requires approval_reason") {
		t.Fatalf("unexpected registration error: %v", err)
	}
	var contractError *ToolContractError
	if !errors.As(err, &contractError) || contractError.ErrorCode() != "invalid_tool_registry" {
		t.Fatalf("unexpected registration error type: %T %v", err, err)
	}
	if _, ok := registry.Get("invalid_permissions"); ok {
		t.Fatal("invalid manifest was registered")
	}
}
