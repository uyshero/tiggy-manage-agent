package v2_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var routePattern = regexp.MustCompile(`HandleFunc\("(GET|POST|PUT|PATCH|DELETE) /v1([^\"]*)"`)
var parameterPattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)(?:\.\.\.)?\}`)

type openAPISpecification struct {
	Paths map[string]map[string]openAPIOperation `yaml:"paths"`
}

type openAPIOperation struct {
	RequestBody *openAPIRequestBody        `yaml:"requestBody"`
	Responses   map[string]openAPIResponse `yaml:"responses"`
	Parameters  []openAPIParameter         `yaml:"parameters"`
}

type openAPIRequestBody struct {
	Required bool                      `yaml:"required"`
	Content  map[string]openAPIContent `yaml:"content"`
}

type openAPIResponse struct {
	Content map[string]openAPIContent `yaml:"content"`
}

type openAPIContent struct {
	Schema openAPISchema `yaml:"schema"`
}

type openAPISchema struct {
	Ref string `yaml:"$ref"`
}

type openAPIParameter struct {
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
	Style    string `yaml:"style"`
	Explode  bool   `yaml:"explode"`
	Schema   struct {
		Type   string `yaml:"type"`
		Format string `yaml:"format"`
		Items  struct {
			Type string `yaml:"type"`
		} `yaml:"items"`
	} `yaml:"schema"`
}

func TestOpenAPICoversUserAndControlRoutes(t *testing.T) {
	source, err := os.ReadFile("../../internal/httpapi/server.go")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification openAPISpecification
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	for _, match := range routePattern.FindAllStringSubmatch(string(source), -1) {
		path := "/v2" + parameterPattern.ReplaceAllString(match[2], `{$1}`)
		if excludedV2Route(match[1], path) {
			continue
		}
		if !hasOperation(specification.Paths, strings.ToLower(match[1]), path) {
			t.Errorf("OpenAPI is missing %s %s", match[1], path)
		}
	}
	for _, operation := range []struct {
		method string
		path   string
	}{
		{"post", "/v2/sessions/{session_id}/runs"},
		{"get", "/v2/sessions/{session_id}/runs"},
		{"get", "/v2/sessions/{session_id}/runs/{run_id}"},
		{"post", "/v2/sessions/{session_id}/runs/{run_id}/cancel"},
		{"get", "/v2/sessions/{session_id}/runs/{run_id}/events"},
		{"get", "/v2/sessions/{session_id}/runs/{run_id}/events/stream"},
	} {
		if !hasOperation(specification.Paths, operation.method, operation.path) {
			t.Errorf("OpenAPI is missing Run operation %s %s", operation.method, operation.path)
		}
	}
}

func TestCoreOperationsUseExplicitSchemas(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification openAPISpecification
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	tests := []struct {
		method      string
		path        string
		requestRef  string
		status      string
		responseRef string
		contentType string
	}{
		{"post", "/v2/agents", "#/components/schemas/CreateAgentRequest", "201", "#/components/schemas/Agent", "application/json"},
		{"get", "/v2/auth/me", "", "200", "#/components/schemas/AuthState", "application/json"},
		{"get", "/v2/environment-variables", "", "200", "#/components/schemas/EnvironmentVariableList", "application/json"},
		{"put", "/v2/environment-variables/{name}", "#/components/schemas/PutEnvironmentVariableRequest", "200", "#/components/schemas/EnvironmentVariable", "application/json"},
		{"delete", "/v2/environment-variables/{name}", "", "204", "", "application/json"},
		{"get", "/v2/workspaces/{workspace_id}/tool-permissions", "", "200", "#/components/schemas/WorkspaceToolPermissionPolicy", "application/json"},
		{"put", "/v2/workspaces/{workspace_id}/tool-permissions", "#/components/schemas/UpdateWorkspaceToolPermissionPolicyRequest", "200", "#/components/schemas/WorkspaceToolPermissionPolicy", "application/json"},
		{"post", "/v2/workspaces/{workspace_id}/tool-permissions/evaluate", "#/components/schemas/EvaluateWorkspaceToolPermissionRequest", "200", "#/components/schemas/EvaluateWorkspaceToolPermissionResult", "application/json"},
		{"post", "/v2/environments", "#/components/schemas/CreateEnvironmentRequest", "201", "#/components/schemas/Environment", "application/json"},
		{"patch", "/v2/llm-providers/{provider_id}", "#/components/schemas/UpdateLLMProviderRequest", "200", "#/components/schemas/LLMProvider", "application/json"},
		{"post", "/v2/llm-providers/{provider_id}/test", "", "200", "#/components/schemas/LLMDiagnosticResult", "application/json"},
		{"post", "/v2/llm-models", "#/components/schemas/PutLLMModelRequest", "201", "#/components/schemas/LLMModel", "application/json"},
		{"post", "/v2/llm-models/{provider_id}/{model}/test", "", "200", "#/components/schemas/LLMDiagnosticResult", "application/json"},
		{"post", "/v2/sessions", "#/components/schemas/CreateSessionRequest", "201", "#/components/schemas/Session", "application/json"},
		{"post", "/v2/sessions/{session_id}/runs", "#/components/schemas/StartRunRequest", "201", "#/components/schemas/StartRunResponse", "application/json"},
		{"get", "/v2/sessions/{session_id}/runs/{run_id}/events", "", "200", "#/components/schemas/EventList", "application/json"},
		{"get", "/v2/sessions/{session_id}/runs/{run_id}/events/stream", "", "200", "#/components/schemas/EventStream", "text/event-stream"},
		{"get", "/v2/sessions/{session_id}/live/stream", "", "200", "#/components/schemas/LiveEventStream", "text/event-stream"},
		{"post", "/v2/object-refs", "#/components/schemas/CreateObjectRefRequest", "201", "#/components/schemas/ObjectRef", "application/json"},
		{"get", "/v2/mcp-servers", "", "200", "#/components/schemas/MCPServerList", "application/json"},
		{"post", "/v2/mcp-servers", "#/components/schemas/CreateMCPServerRequest", "201", "#/components/schemas/MCPServer", "application/json"},
		{"get", "/v2/mcp-servers/runtime-status", "", "200", "#/components/schemas/MCPRuntimeStatus", "application/json"},
		{"get", "/v2/mcp-servers/{server_id}", "", "200", "#/components/schemas/MCPServer", "application/json"},
		{"patch", "/v2/mcp-servers/{server_id}", "#/components/schemas/UpdateMCPServerRequest", "200", "#/components/schemas/MCPServer", "application/json"},
		{"delete", "/v2/mcp-servers/{server_id}", "", "200", "#/components/schemas/MCPServer", "application/json"},
		{"post", "/v2/mcp-servers/{server_id}/enable", "", "200", "#/components/schemas/MCPServer", "application/json"},
		{"post", "/v2/mcp-servers/{server_id}/disable", "", "200", "#/components/schemas/MCPServer", "application/json"},
		{"post", "/v2/mcp-servers/{server_id}/test", "", "200", "#/components/schemas/MCPServerTestResult", "application/json"},
		{"get", "/v2/mcp-servers/{server_id}/versions", "", "200", "#/components/schemas/MCPServerVersionList", "application/json"},
		{"post", "/v2/mcp-servers/{server_id}/versions/{version}/restore", "", "200", "#/components/schemas/MCPRestoreResult", "application/json"},
		{"get", "/v2/operator-audit", "", "200", "#/components/schemas/OperatorAuditList", "application/json"},
		{"get", "/v2/sessions/{session_id}/operator-audit", "", "200", "#/components/schemas/OperatorAuditList", "application/json"},
		{"get", "/v2/sessions/{session_id}/tool-permission-audit", "", "200", "#/components/schemas/ToolPermissionAuditList", "application/json"},
		{"get", "/v2/observability/security-audit/integrity-keys", "", "200", "#/components/schemas/SecurityAuditIntegrityKeyStatus", "application/json"},
		{"post", "/v2/observability/security-audit/replay", "", "200", "#/components/schemas/SecurityAuditReplayResult", "application/json"},
		{"post", "/v2/skills", "#/components/schemas/CreateSkillRequest", "201", "#/components/schemas/Skill", "application/json"},
		{"get", "/v2/skills", "", "200", "#/components/schemas/SkillList", "application/json"},
		{"post", "/v2/skills/resolve-preview", "#/components/schemas/ResolveSkillsPreviewRequest", "200", "#/components/schemas/ResolveSkillsResult", "application/json"},
		{"get", "/v2/skills/{skill_id}", "", "200", "#/components/schemas/Skill", "application/json"},
		{"post", "/v2/skills/{skill_id}/archive", "", "200", "#/components/schemas/Skill", "application/json"},
		{"post", "/v2/skills/{skill_id}/versions", "#/components/schemas/CreateSkillVersionRequest", "201", "#/components/schemas/SkillVersion", "application/json"},
		{"get", "/v2/skills/{skill_id}/versions", "", "200", "#/components/schemas/SkillVersionList", "application/json"},
		{"get", "/v2/skills/{skill_id}/versions/{version}", "", "200", "#/components/schemas/SkillVersion", "application/json"},
		{"get", "/v2/skills/{skill_id}/versions/{version}/package", "", "200", "#/components/schemas/BinaryContent", "application/zip"},
		{"post", "/v2/skill-packages/backfill", "#/components/schemas/SkillPackageBackfillRequest", "200", "#/components/schemas/SkillPackageBackfillResult", "application/json"},
		{"get", "/v2/sessions/{session_id}/skill-usages", "", "200", "#/components/schemas/SkillUsageList", "application/json"},
		{"get", "/v2/skill-asset-retention/effective", "", "200", "#/components/schemas/EffectiveSkillRetentionPolicy", "application/json"},
		{"post", "/v2/skill-asset-retention/policies", "#/components/schemas/CreateSkillRetentionPolicyRequest", "201", "#/components/schemas/SkillRetentionPolicyResult", "application/json"},
		{"get", "/v2/skill-asset-retention/policies", "", "200", "#/components/schemas/SkillRetentionPolicyList", "application/json"},
		{"get", "/v2/skill-asset-retention/policies/{policy_id}", "", "200", "#/components/schemas/SkillRetentionPolicyResult", "application/json"},
		{"post", "/v2/skill-asset-retention/policies/{policy_id}/versions", "#/components/schemas/PublishSkillRetentionPolicyRequest", "201", "#/components/schemas/SkillRetentionPolicyVersion", "application/json"},
		{"get", "/v2/skill-asset-retention/policies/{policy_id}/versions/{version}", "", "200", "#/components/schemas/SkillRetentionPolicyVersion", "application/json"},
		{"post", "/v2/skill-asset-retention/policies/{policy_id}/archive", "", "200", "#/components/schemas/SkillRetentionPolicy", "application/json"},
		{"post", "/v2/skill-asset-gc/preview", "#/components/schemas/SkillAssetGCRequest", "200", "#/components/schemas/SkillAssetGCPreview", "application/json"},
		{"post", "/v2/skill-asset-gc/run", "#/components/schemas/SkillAssetGCRequest", "200", "#/components/schemas/SkillAssetGCRunResult", "application/json"},
		{"get", "/v2/skill-asset-gc/runs", "", "200", "#/components/schemas/SkillAssetGCRunList", "application/json"},
		{"get", "/v2/skill-asset-gc/runs/{run_id}", "", "200", "#/components/schemas/SkillAssetGCRunResult", "application/json"},
		{"get", "/v2/skill-asset-gc/tombstones", "", "200", "#/components/schemas/SkillAssetGCTombstoneList", "application/json"},
		{"get", "/v2/skills/marketplace/discover", "", "200", "#/components/schemas/MarketplaceDiscoverResult", "application/json"},
		{"post", "/v2/skills/marketplace/preview", "#/components/schemas/MarketplacePreviewRequest", "200", "#/components/schemas/MarketplacePreviewResult", "application/json"},
		{"post", "/v2/skills/marketplace/install", "#/components/schemas/MarketplaceInstallRequest", "201", "#/components/schemas/MarketplaceInstallResult", "application/json"},
		{"get", "/v2/skills/marketplace/internal", "", "200", "#/components/schemas/MarketplaceInternalResult", "application/json"},
		{"post", "/v2/skills/marketplace/internal/preview", "#/components/schemas/MarketplacePreviewRequest", "200", "#/components/schemas/MarketplacePreviewResult", "application/json"},
		{"post", "/v2/skills/marketplace/internal/install", "#/components/schemas/MarketplaceInstallRequest", "201", "#/components/schemas/MarketplaceInstallResult", "application/json"},
		{"post", "/v2/skills/{skill_id}/enable", "#/components/schemas/MarketplaceEnableRequest", "201", "#/components/schemas/MarketplaceEnableResult", "application/json"},
		{"post", "/v2/skills/{skill_id}/disable", "#/components/schemas/MarketplaceDisableRequest", "201", "#/components/schemas/MarketplaceDisableResult", "application/json"},
		{"get", "/v2/skill-marketplace-entries", "", "200", "#/components/schemas/MarketplaceEntryList", "application/json"},
		{"post", "/v2/skill-marketplace-entries", "#/components/schemas/CreateMarketplaceEntryRequest", "201", "#/components/schemas/MarketplaceEntry", "application/json"},
		{"get", "/v2/skill-marketplace-entries/{entry_id}", "", "200", "#/components/schemas/MarketplaceEntry", "application/json"},
		{"patch", "/v2/skill-marketplace-entries/{entry_id}", "#/components/schemas/UpdateMarketplaceEntryRequest", "200", "#/components/schemas/MarketplaceEntry", "application/json"},
		{"post", "/v2/skill-marketplace-entries/{entry_id}/submit", "#/components/schemas/MarketplaceTransitionRequest", "200", "#/components/schemas/MarketplaceEntry", "application/json"},
		{"post", "/v2/skill-marketplace-entries/{entry_id}/publish", "#/components/schemas/MarketplaceTransitionRequest", "200", "#/components/schemas/MarketplaceEntry", "application/json"},
		{"post", "/v2/skill-marketplace-entries/{entry_id}/withdraw", "#/components/schemas/MarketplaceTransitionRequest", "200", "#/components/schemas/MarketplaceEntry", "application/json"},
		{"get", "/v2/skill-marketplace-policies", "", "200", "#/components/schemas/MarketplacePolicyList", "application/json"},
		{"post", "/v2/skill-marketplace-policies", "#/components/schemas/CreateMarketplacePolicyRequest", "201", "#/components/schemas/MarketplacePolicyResult", "application/json"},
		{"get", "/v2/skill-marketplace-policies/{policy_id}", "", "200", "#/components/schemas/MarketplacePolicyResult", "application/json"},
		{"post", "/v2/skill-marketplace-policies/{policy_id}/versions", "#/components/schemas/PublishMarketplacePolicyRequest", "201", "#/components/schemas/MarketplacePolicyVersion", "application/json"},
		{"get", "/v2/skill-marketplace-policies/{policy_id}/versions/{version}", "", "200", "#/components/schemas/MarketplacePolicyVersion", "application/json"},
		{"post", "/v2/skill-marketplace-policies/{policy_id}/archive", "", "200", "#/components/schemas/MarketplacePolicy", "application/json"},
		{"get", "/v2/sessions/{session_id}/trace", "", "200", "#/components/schemas/TraceDocument", "application/json"},
		{"post", "/v2/workers/diagnose", "#/components/schemas/WorkerDiagnoseRequest", "200", "#/components/schemas/WorkerDiagnoseResponse", "application/json"},
		{"post", "/v2/worker-work", "#/components/schemas/EnqueueWorkerWorkRequest", "201", "#/components/schemas/WorkerWork", "application/json"},
		{"get", "/v2/worker-work/{work_id}/diagnose", "", "200", "#/components/schemas/WorkerWorkDiagnosis", "application/json"},
		{"get", "/v2/observability/status", "", "200", "#/components/schemas/ObservabilityStatus", "application/json"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			operation, ok := specification.Paths[test.path][test.method]
			if !ok {
				t.Fatalf("operation is missing")
			}
			if test.requestRef != "" {
				if operation.RequestBody == nil || !operation.RequestBody.Required {
					t.Fatalf("typed request body must be required")
				}
				if got := operation.RequestBody.Content["application/json"].Schema.Ref; got != test.requestRef {
					t.Fatalf("request schema = %q, want %q", got, test.requestRef)
				}
			}
			if got := operation.Responses[test.status].Content[test.contentType].Schema.Ref; got != test.responseRef {
				t.Fatalf("response schema = %q, want %q", got, test.responseRef)
			}
		})
	}
}

func TestEveryPublicOperationUsesExplicitSchemas(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification map[string]any
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths, ok := specification["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI paths are missing")
	}
	for path, rawPathItem := range paths {
		pathItem, _ := rawPathItem.(map[string]any)
		for method, rawOperation := range pathItem {
			if !isHTTPMethod(method) {
				continue
			}
			operation, _ := rawOperation.(map[string]any)
			if rawRequestBody, exists := operation["requestBody"]; exists {
				requestBody, _ := rawRequestBody.(map[string]any)
				assertContentSchemasUseRefs(t, method+" "+path+" request", requestBody["content"])
			}
			responses, _ := operation["responses"].(map[string]any)
			if _, exists := responses["2XX"]; exists {
				t.Errorf("%s %s still uses the generic 2XX fallback", method, path)
			}
			for status, rawResponse := range responses {
				if len(status) != 3 || status[0] != '2' {
					continue
				}
				response, _ := rawResponse.(map[string]any)
				if content, exists := response["content"]; exists {
					assertContentSchemasUseRefs(t, method+" "+path+" response "+status, content)
				}
			}
		}
	}
}

func TestTracePaginationUsesOpaqueCursor(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification map[string]any
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := specification["paths"].(map[string]any)
	for _, path := range []string{"/v2/traces", "/v2/spans"} {
		operation := paths[path].(map[string]any)["get"].(map[string]any)
		parameters := operation["parameters"].([]any)
		names := map[string]bool{}
		for _, raw := range parameters {
			parameter := raw.(map[string]any)
			names[parameter["name"].(string)] = true
		}
		if !names["cursor"] || names["offset"] {
			t.Fatalf("%s parameters must expose cursor and exclude offset: %#v", path, names)
		}
	}
	components := specification["components"].(map[string]any)["schemas"].(map[string]any)
	for _, schemaName := range []string{"TraceCatalog", "TraceSpanCatalog"} {
		schema := components[schemaName].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, name := range []string{"items", "next_cursor", "has_more"} {
			if _, ok := properties[name]; !ok {
				t.Errorf("%s is missing %s", schemaName, name)
			}
		}
		for _, legacy := range []string{"traces", "spans", "offset", "next_offset"} {
			if _, ok := properties[legacy]; ok {
				t.Errorf("%s still exposes legacy property %s", schemaName, legacy)
			}
		}
	}
}

func TestDynamicJSONIsNamedAndDocumented(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification map[string]any
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	components := specification["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	dynamic := schemas["DynamicJSONValue"].(map[string]any)
	if marked, _ := dynamic["x-tma-dynamic-json"].(bool); !marked {
		t.Fatal("DynamicJSONValue must explicitly identify intentional extension JSON")
	}
	event := schemas["Event"].(map[string]any)
	properties := event["properties"].(map[string]any)
	payload := properties["payload"].(map[string]any)
	if got := fmt.Sprint(payload["$ref"]); got != "#/components/schemas/DynamicJSONValue" {
		t.Fatalf("Event.payload schema = %q, want named dynamic JSON", got)
	}
	var inspect func(any, string)
	inspect = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			if allowsAny, _ := typed["additionalProperties"].(bool); allowsAny {
				if marked, _ := typed["x-tma-dynamic-json"].(bool); !marked {
					t.Errorf("%s permits arbitrary JSON without x-tma-dynamic-json", path)
				}
			}
			for key, child := range typed {
				inspect(child, path+"."+key)
			}
		case []any:
			for index, child := range typed {
				inspect(child, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	inspect(specification, "openapi")
}

func assertContentSchemasUseRefs(t *testing.T, location string, rawContent any) {
	t.Helper()
	content, _ := rawContent.(map[string]any)
	if len(content) == 0 {
		t.Errorf("%s has no content schema", location)
		return
	}
	for contentType, rawMedia := range content {
		media, _ := rawMedia.(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		if strings.TrimSpace(fmt.Sprint(schema["$ref"])) == "" {
			t.Errorf("%s %s must reference a named schema", location, contentType)
		}
	}
}

func isHTTPMethod(value string) bool {
	switch value {
	case "get", "post", "put", "patch", "delete":
		return true
	default:
		return false
	}
}

func TestTaskTemplatesAreExcludedFromV2Contract(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification openAPISpecification
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatal(err)
	}
	if _, exists := specification.Paths["/v2/task-templates"]; exists {
		t.Fatal("legacy Workbench task templates must not be part of the v2 contract")
	}
	for _, path := range []string{"/v2/agent/task-group-templates", "/v2/agent/discussion-strategies"} {
		if !hasOperation(specification.Paths, "get", path) {
			t.Fatalf("Orchestration operation GET %s is missing", path)
		}
	}
}

func TestInt64SchemasStayWithinJavaScriptSafeIntegerRange(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification map[string]any
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatal(err)
	}
	var inspect func(any, string)
	inspect = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			if typed["format"] == "int64" && fmt.Sprint(typed["maximum"]) != "9007199254740991" {
				t.Errorf("%s is int64 without JavaScript-safe maximum", path)
			}
			for key, child := range typed {
				inspect(child, path+"."+key)
			}
		case []any:
			for index, child := range typed {
				inspect(child, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	inspect(specification, "openapi")
}

func TestLLMConditionalHeadersArePartOfContract(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification openAPISpecification
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	assertParameter := func(t *testing.T, method string, path string, name string, required bool) {
		t.Helper()
		for _, parameter := range specification.Paths[path][method].Parameters {
			if parameter.In == "header" && parameter.Name == name {
				if parameter.Required != required {
					t.Fatalf("%s %s header %s required=%t, want %t", method, path, name, parameter.Required, required)
				}
				return
			}
		}
		t.Fatalf("%s %s is missing %s header", method, path, name)
	}
	assertParameter(t, "patch", "/v2/llm-providers/{provider_id}", "If-Match", true)
	assertParameter(t, "patch", "/v2/sessions/{session_id}/runtime-settings", "If-Match", true)
	assertParameter(t, "post", "/v2/llm-models", "If-Match", false)
	assertParameter(t, "post", "/v2/llm-models", "If-None-Match", false)
}

func TestSkillVersionPathParametersUseInt32(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification openAPISpecification
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v2/skills/{skill_id}/versions/{version}",
		"/v2/skills/{skill_id}/versions/{version}/package",
		"/v2/skill-asset-retention/policies/{policy_id}/versions/{version}",
		"/v2/skill-marketplace-policies/{policy_id}/versions/{version}",
	} {
		found := false
		for _, parameter := range specification.Paths[path]["get"].Parameters {
			if parameter.Name == "version" && parameter.In == "path" {
				found = true
				if parameter.Schema.Type != "integer" || parameter.Schema.Format != "int32" {
					t.Fatalf("GET %s version schema = %s/%s", path, parameter.Schema.Type, parameter.Schema.Format)
				}
			}
		}
		if !found {
			t.Fatalf("GET %s is missing version path parameter", path)
		}
	}
}

func TestMarketplaceInternalTagsUseRepeatedArrayQuery(t *testing.T) {
	contract, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var specification openAPISpecification
	if err := yaml.Unmarshal(contract, &specification); err != nil {
		t.Fatal(err)
	}
	for _, parameter := range specification.Paths["/v2/skills/marketplace/internal"]["get"].Parameters {
		if parameter.Name != "tag" {
			continue
		}
		if parameter.Schema.Type != "array" || parameter.Schema.Items.Type != "string" || parameter.Style != "form" || !parameter.Explode {
			t.Fatalf("tag query must be a repeated string array, got %+v", parameter)
		}
		return
	}
	t.Fatal("Marketplace internal browse is missing tag query")
}

func hasOperation(paths map[string]map[string]openAPIOperation, method string, path string) bool {
	operations, ok := paths[path]
	if !ok {
		return false
	}
	_, ok = operations[method]
	return ok
}

func excludedV2Route(method string, path string) bool {
	if method == "GET" && path == "/v2/task-templates" {
		return true
	}
	if method == "POST" && path == "/v2/workers" {
		return true
	}
	if method == "POST" && strings.HasPrefix(path, "/v2/workers/") && strings.HasSuffix(path, "/heartbeat") {
		return true
	}
	return strings.Contains(path, "/work/poll") || strings.Contains(path, "/work/{work_id}/ack") ||
		strings.Contains(path, "/work/{work_id}/heartbeat") || strings.Contains(path, "/work/{work_id}/result")
}
