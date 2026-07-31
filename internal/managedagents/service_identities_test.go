package managedagents

import (
	"reflect"
	"testing"
)

func TestNormalizeServiceIdentityScopesRejectsUnknownAndSorts(t *testing.T) {
	got, err := NormalizeServiceIdentityScopes([]string{ServiceScopeSpeechRealtime, ServiceScopeModelGenerate, ServiceScopeSpeechRealtime})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ServiceScopeModelGenerate, ServiceScopeSpeechRealtime}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized scopes = %#v, want %#v", got, want)
	}
	if _, err := NormalizeServiceIdentityScopes([]string{"platform:admin"}); err == nil {
		t.Fatal("unknown service identity scope was accepted")
	}
}

func TestNormalizeServiceIdentityInputRejectsAdministratorRole(t *testing.T) {
	if _, _, _, _, _, err := NormalizeServiceIdentityInput(
		ServiceIdentityKindApplication, "knowledge", "", WorkspaceRoleAdmin, []string{ServiceScopeRetrievalRead},
	); err == nil {
		t.Fatal("administrator service identity role was accepted")
	}
}
