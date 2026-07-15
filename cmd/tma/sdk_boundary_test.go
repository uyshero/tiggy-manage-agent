package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestSessionCommandsDoNotBypassCoreSDK(t *testing.T) {
	typedFunctions := map[string]bool{
		"commandProvider":            true,
		"commandModel":               true,
		"commandAgent":               true,
		"commandAgentConfig":         true,
		"commandEnvironment":         true,
		"commandUsage":               true,
		"commandObject":              true,
		"commandTrace":               true,
		"commandObservability":       true,
		"commandSession":             true,
		"commandSessionConfig":       true,
		"commandSessionRuntime":      true,
		"commandSessionIntervention": true,
		"commandSessionSummary":      true,
		"commandEvent":               true,
		"commandSessionAttach":       true,
		"streamInteractive":          true,
		"listPendingInterventions":   true,
		"decideSessionIntervention":  true,
		"sendUserMessage":            true,
		"sendUserInterrupt":          true,
		"commandSessionArtifact":     true,
		"printSessionVersionNotice":  true,
		"commandAuth":                true,
		"commandAuthLogin":           true,
		"commandAuthStatus":          true,
		"commandAuthLogout":          true,
		"clientAuthState":            true,
		"validateAuthToken":          true,
	}
	for _, name := range []string{"main.go", "attach.go", "object.go", "skill.go", "marketplace.go", "auth.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			isNewTypedCLI := (name == "skill.go" || name == "marketplace.go" || name == "auth.go") && strings.HasPrefix(function.Name.Name, "command")
			if !typedFunctions[function.Name.Name] && !isNewTypedCLI {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok && (selector.Sel.Name == "do" || selector.Sel.Name == "download" || selector.Sel.Name == "stream") {
					t.Errorf("%s:%s uses generic API helper %s; add or use a typed sdk/tma service method", name, function.Name.Name, selector.Sel.Name)
				}
				return true
			})
		}
	}
}

func TestWorkerCommandsKeepOnlyMachineProtocolOnV1(t *testing.T) {
	for _, check := range []struct {
		file      string
		forbidden []string
	}{
		{"worker.go", []string{"/v1/workers/reap-expired", "/v1/workers/diagnose", "/archive"}},
		{"work.go", []string{"/v1/worker-work"}},
	} {
		content, err := os.ReadFile(check.file)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range check.forbidden {
			if strings.Contains(string(content), forbidden) {
				t.Errorf("%s contains control-plane v1 path %q; use the typed Core SDK service", check.file, forbidden)
			}
		}
	}
}
