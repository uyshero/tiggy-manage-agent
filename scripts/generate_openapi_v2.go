//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var routePattern = regexp.MustCompile(`HandleFunc\("(GET|POST|PUT|PATCH|DELETE) /v1([^\"]*)"`)
var parameterPattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)(?:\.\.\.)?\}`)

type route struct {
	Method string
	Path   string
}

type routeContract struct {
	RequestSchema         string
	RequestRequired       bool
	RequestContentType    string
	ResponseSchema        string
	SuccessStatuses       []string
	ContentType           string
	Parameters            []contractParameter
	IntegerPathParameters map[string]bool
}

type contractParameter struct {
	Name        string
	In          string
	Required    bool
	Description string
	Type        string
	Format      string
	Array       bool
}

var ifMatchParameter = contractParameter{Name: "If-Match", In: "header", Required: true, Description: "Quoted positive resource revision returned by the previous read."}
var ifNoneMatchParameter = contractParameter{Name: "If-None-Match", In: "header", Description: "Use * to require creation of a model that does not already exist."}
var optionalIfMatchParameter = contractParameter{Name: "If-Match", In: "header", Description: "Quoted current model revision; required when updating an existing model."}

func traceCatalogParameters() []contractParameter {
	return []contractParameter{
		{Name: "workspace_id", In: "query"},
		{Name: "session_id", In: "query"},
		{Name: "turn_id", In: "query"},
		{Name: "session_status", In: "query"},
		{Name: "include_archived", In: "query", Type: "boolean"},
		{Name: "limit", In: "query", Type: "integer", Format: "int32"},
		{Name: "cursor", In: "query", Description: "Opaque cursor returned by the previous page."},
	}
}

func traceSpanCatalogParameters() []contractParameter {
	return []contractParameter{
		{Name: "workspace_id", In: "query"},
		{Name: "trace_id", In: "query"},
		{Name: "session_id", In: "query"},
		{Name: "turn_id", In: "query"},
		{Name: "kind", In: "query"},
		{Name: "status", In: "query"},
		{Name: "q", In: "query"},
		{Name: "critical", In: "query", Type: "boolean"},
		{Name: "min_duration_ms", In: "query", Type: "integer", Format: "int64"},
		{Name: "max_duration_ms", In: "query", Type: "integer", Format: "int64"},
		{Name: "min_self_duration_ms", In: "query", Type: "integer", Format: "int64"},
		{Name: "include_archived", In: "query", Type: "boolean"},
		{Name: "limit", In: "query", Type: "integer", Format: "int32"},
		{Name: "cursor", In: "query", Description: "Opaque cursor returned by the previous page."},
	}
}

var coreContracts = map[string]routeContract{
	"get /v2/agent/discussion-strategies":                                      {ResponseSchema: "AgentDiscussionStrategyList"},
	"get /v2/agent/task-group-templates":                                       {ResponseSchema: "AgentTaskGroupTemplateList"},
	"get /v2/agents/default":                                                   {ResponseSchema: "Agent"},
	"post /v2/agents/import":                                                   {RequestSchema: "AgentImportRequest", RequestRequired: true, ResponseSchema: "Agent", SuccessStatuses: []string{"201"}},
	"post /v2/agents":                                                          {RequestSchema: "CreateAgentRequest", RequestRequired: true, ResponseSchema: "Agent", SuccessStatuses: []string{"201"}},
	"get /v2/agents":                                                           {ResponseSchema: "AgentList"},
	"get /v2/agents/{agent_id}":                                                {ResponseSchema: "Agent"},
	"patch /v2/agents/{agent_id}":                                              {RequestSchema: "UpdateAgentRequest", RequestRequired: true, ResponseSchema: "Agent"},
	"get /v2/agents/{agent_id}/config-versions":                                {ResponseSchema: "AgentConfigVersionList"},
	"post /v2/agents/{agent_id}/config-versions":                               {RequestSchema: "CreateAgentConfigVersionRequest", RequestRequired: true, ResponseSchema: "Agent", SuccessStatuses: []string{"201"}},
	"post /v2/agents/{agent_id}/config-versions/{version}/rollback":            {ResponseSchema: "AgentConfigRollbackResponse", SuccessStatuses: []string{"201"}, IntegerPathParameters: map[string]bool{"version": true}},
	"get /v2/agents/{agent_id}/export":                                         {ResponseSchema: "AgentExportDocument"},
	"post /v2/agents/{agent_id}/tooling-health":                                {RequestSchema: "ToolingHealthRequest", ResponseSchema: "ToolingHealthResponse"},
	"get /v2/auth/config":                                                      {ResponseSchema: "AuthClientConfiguration"},
	"get /v2/auth/me":                                                          {ResponseSchema: "AuthState"},
	"get /v2/environment-variables":                                            {ResponseSchema: "EnvironmentVariableList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"put /v2/environment-variables/{name}":                                     {RequestSchema: "PutEnvironmentVariableRequest", RequestRequired: true, ResponseSchema: "EnvironmentVariable", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"delete /v2/environment-variables/{name}":                                  {SuccessStatuses: []string{"204"}, Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"post /v2/environments":                                                    {RequestSchema: "CreateEnvironmentRequest", RequestRequired: true, ResponseSchema: "Environment", SuccessStatuses: []string{"201"}},
	"get /v2/llm-providers":                                                    {ResponseSchema: "LLMProviderList"},
	"post /v2/llm-providers":                                                   {RequestSchema: "CreateLLMProviderRequest", RequestRequired: true, ResponseSchema: "LLMProvider", SuccessStatuses: []string{"201"}},
	"get /v2/llm-providers/{provider_id}":                                      {ResponseSchema: "LLMProvider"},
	"patch /v2/llm-providers/{provider_id}":                                    {RequestSchema: "UpdateLLMProviderRequest", RequestRequired: true, ResponseSchema: "LLMProvider", Parameters: []contractParameter{ifMatchParameter}},
	"post /v2/llm-providers/{provider_id}/enable":                              {ResponseSchema: "LLMProvider", Parameters: []contractParameter{ifMatchParameter}},
	"post /v2/llm-providers/{provider_id}/disable":                             {ResponseSchema: "LLMProvider", Parameters: []contractParameter{ifMatchParameter}},
	"post /v2/llm-providers/{provider_id}/test":                                {ResponseSchema: "LLMDiagnosticResult"},
	"delete /v2/llm-providers/{provider_id}":                                   {SuccessStatuses: []string{"204"}, Parameters: []contractParameter{ifMatchParameter}},
	"get /v2/llm-models":                                                       {ResponseSchema: "LLMModelList", Parameters: []contractParameter{{Name: "provider_id", In: "query"}}},
	"post /v2/llm-models":                                                      {RequestSchema: "PutLLMModelRequest", RequestRequired: true, ResponseSchema: "LLMModel", SuccessStatuses: []string{"200", "201"}, Parameters: []contractParameter{optionalIfMatchParameter, ifNoneMatchParameter}},
	"post /v2/llm-models/{provider_id}/{model}/test":                           {ResponseSchema: "LLMDiagnosticResult"},
	"delete /v2/llm-models/{provider_id}/{model}":                              {SuccessStatuses: []string{"204"}, Parameters: []contractParameter{ifMatchParameter}},
	"get /v2/llm-usage":                                                        {ResponseSchema: "LLMUsageAggregateReport"},
	"get /v2/mcp-servers":                                                      {ResponseSchema: "MCPServerList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"post /v2/mcp-servers":                                                     {RequestSchema: "CreateMCPServerRequest", RequestRequired: true, ResponseSchema: "MCPServer", SuccessStatuses: []string{"201"}},
	"get /v2/mcp-servers/runtime-status":                                       {ResponseSchema: "MCPRuntimeStatus", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"get /v2/mcp-servers/{server_id}":                                          {ResponseSchema: "MCPServer"},
	"patch /v2/mcp-servers/{server_id}":                                        {RequestSchema: "UpdateMCPServerRequest", RequestRequired: true, ResponseSchema: "MCPServer"},
	"delete /v2/mcp-servers/{server_id}":                                       {ResponseSchema: "MCPServer"},
	"post /v2/mcp-servers/{server_id}/enable":                                  {ResponseSchema: "MCPServer"},
	"post /v2/mcp-servers/{server_id}/disable":                                 {ResponseSchema: "MCPServer"},
	"post /v2/mcp-servers/{server_id}/test":                                    {ResponseSchema: "MCPServerTestResult"},
	"get /v2/mcp-servers/{server_id}/versions":                                 {ResponseSchema: "MCPServerVersionList"},
	"post /v2/mcp-servers/{server_id}/versions/{version}/restore":              {ResponseSchema: "MCPRestoreResult", IntegerPathParameters: map[string]bool{"version": true}},
	"post /v2/object-refs":                                                     {RequestSchema: "CreateObjectRefRequest", RequestRequired: true, ResponseSchema: "ObjectRef", SuccessStatuses: []string{"201"}},
	"get /v2/object-refs/{object_ref_id}":                                      {ResponseSchema: "ObjectRef"},
	"delete /v2/object-refs/{object_ref_id}":                                   {SuccessStatuses: []string{"204"}},
	"get /v2/object-refs/{object_ref_id}/download":                             {ResponseSchema: "BinaryContent", ContentType: "application/octet-stream", Parameters: []contractParameter{{Name: "session_id", In: "query"}}},
	"get /v2/observability/status":                                             {ResponseSchema: "ObservabilityStatus"},
	"post /v2/observability/retry":                                             {ResponseSchema: "ObservabilityRetryResult"},
	"get /v2/observability/security-audit/integrity-keys":                      {ResponseSchema: "SecurityAuditIntegrityKeyStatus"},
	"post /v2/observability/security-audit/replay":                             {ResponseSchema: "SecurityAuditReplayResult", Parameters: []contractParameter{{Name: "limit", In: "query", Type: "integer", Format: "int32"}}},
	"get /v2/operator-audit":                                                   {ResponseSchema: "OperatorAuditList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}, {Name: "session_id", In: "query"}, {Name: "principal_id", In: "query"}, {Name: "action", In: "query"}, {Name: "limit", In: "query", Type: "integer", Format: "int32"}}},
	"post /v2/skills":                                                          {RequestSchema: "CreateSkillRequest", RequestRequired: true, ResponseSchema: "Skill", SuccessStatuses: []string{"201"}},
	"get /v2/skills":                                                           {ResponseSchema: "SkillList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}, {Name: "include_archived", In: "query", Type: "boolean"}}},
	"get /v2/skills/marketplace/discover":                                      {ResponseSchema: "MarketplaceDiscoverResult", Parameters: []contractParameter{{Name: "session_id", In: "query", Required: true}, {Name: "query", In: "query"}, {Name: "repository", In: "query"}, {Name: "limit", In: "query", Type: "integer", Format: "int32"}}},
	"post /v2/skills/marketplace/preview":                                      {RequestSchema: "MarketplacePreviewRequest", RequestRequired: true, ResponseSchema: "MarketplacePreviewResult"},
	"post /v2/skills/marketplace/install":                                      {RequestSchema: "MarketplaceInstallRequest", RequestRequired: true, ResponseSchema: "MarketplaceInstallResult", SuccessStatuses: []string{"201"}},
	"get /v2/skills/marketplace/internal":                                      {ResponseSchema: "MarketplaceInternalResult", Parameters: []contractParameter{{Name: "session_id", In: "query", Required: true}, {Name: "query", In: "query"}, {Name: "category", In: "query"}, {Name: "tag", In: "query", Array: true, Description: "Repeat tag to filter by multiple catalog tags."}, {Name: "limit", In: "query", Type: "integer", Format: "int32"}}},
	"post /v2/skills/marketplace/internal/preview":                             {RequestSchema: "MarketplacePreviewRequest", RequestRequired: true, ResponseSchema: "MarketplacePreviewResult"},
	"post /v2/skills/marketplace/internal/install":                             {RequestSchema: "MarketplaceInstallRequest", RequestRequired: true, ResponseSchema: "MarketplaceInstallResult", SuccessStatuses: []string{"201"}},
	"post /v2/skills/resolve-preview":                                          {RequestSchema: "ResolveSkillsPreviewRequest", RequestRequired: true, ResponseSchema: "ResolveSkillsResult"},
	"get /v2/skills/{skill_id}":                                                {ResponseSchema: "Skill"},
	"post /v2/skills/{skill_id}/archive":                                       {ResponseSchema: "Skill"},
	"post /v2/skills/{skill_id}/enable":                                        {RequestSchema: "MarketplaceEnableRequest", RequestRequired: true, ResponseSchema: "MarketplaceEnableResult", SuccessStatuses: []string{"200", "201"}},
	"post /v2/skills/{skill_id}/disable":                                       {RequestSchema: "MarketplaceDisableRequest", RequestRequired: true, ResponseSchema: "MarketplaceDisableResult", SuccessStatuses: []string{"200", "201"}},
	"post /v2/skills/{skill_id}/versions":                                      {RequestSchema: "CreateSkillVersionRequest", RequestRequired: true, ResponseSchema: "SkillVersion", SuccessStatuses: []string{"201"}},
	"get /v2/skills/{skill_id}/draft":                                          {ResponseSchema: "SkillDraft"},
	"put /v2/skills/{skill_id}/draft":                                          {RequestSchema: "PutSkillDraftRequest", RequestRequired: true, ResponseSchema: "SkillDraft"},
	"post /v2/skills/{skill_id}/draft/publish":                                 {RequestSchema: "PublishSkillDraftRequest", RequestRequired: true, ResponseSchema: "SkillVersion", SuccessStatuses: []string{"201"}},
	"post /v2/skills/{skill_id}/fork":                                          {RequestSchema: "ForkSkillRequest", RequestRequired: true, ResponseSchema: "Skill", SuccessStatuses: []string{"201"}},
	"get /v2/skills/{skill_id}/versions":                                       {ResponseSchema: "SkillVersionList"},
	"get /v2/skills/{skill_id}/versions/{version}":                             {ResponseSchema: "SkillVersion", IntegerPathParameters: map[string]bool{"version": true}},
	"get /v2/skills/{skill_id}/versions/{version}/package":                     {ResponseSchema: "BinaryContent", ContentType: "application/zip", IntegerPathParameters: map[string]bool{"version": true}},
	"post /v2/skill-packages/backfill":                                         {RequestSchema: "SkillPackageBackfillRequest", RequestRequired: true, ResponseSchema: "SkillPackageBackfillResult"},
	"get /v2/skill-asset-retention/effective":                                  {ResponseSchema: "EffectiveSkillRetentionPolicy", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"post /v2/skill-asset-retention/policies":                                  {RequestSchema: "CreateSkillRetentionPolicyRequest", RequestRequired: true, ResponseSchema: "SkillRetentionPolicyResult", SuccessStatuses: []string{"201"}},
	"get /v2/skill-asset-retention/policies":                                   {ResponseSchema: "SkillRetentionPolicyList", Parameters: []contractParameter{{Name: "organization_id", In: "query"}, {Name: "workspace_id", In: "query"}, {Name: "include_archived", In: "query", Type: "boolean"}}},
	"get /v2/skill-asset-retention/policies/{policy_id}":                       {ResponseSchema: "SkillRetentionPolicyResult"},
	"post /v2/skill-asset-retention/policies/{policy_id}/versions":             {RequestSchema: "PublishSkillRetentionPolicyRequest", RequestRequired: true, ResponseSchema: "SkillRetentionPolicyVersion", SuccessStatuses: []string{"201"}},
	"get /v2/skill-asset-retention/policies/{policy_id}/versions/{version}":    {ResponseSchema: "SkillRetentionPolicyVersion", IntegerPathParameters: map[string]bool{"version": true}},
	"post /v2/skill-asset-retention/policies/{policy_id}/archive":              {ResponseSchema: "SkillRetentionPolicy"},
	"post /v2/skill-asset-gc/preview":                                          {RequestSchema: "SkillAssetGCRequest", RequestRequired: true, ResponseSchema: "SkillAssetGCPreview"},
	"post /v2/skill-asset-gc/run":                                              {RequestSchema: "SkillAssetGCRequest", RequestRequired: true, ResponseSchema: "SkillAssetGCRunResult"},
	"get /v2/skill-asset-gc/runs":                                              {ResponseSchema: "SkillAssetGCRunList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}, {Name: "limit", In: "query", Type: "integer", Format: "int32"}}},
	"get /v2/skill-asset-gc/runs/{run_id}":                                     {ResponseSchema: "SkillAssetGCRunResult"},
	"get /v2/skill-asset-gc/tombstones":                                        {ResponseSchema: "SkillAssetGCTombstoneList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}, {Name: "limit", In: "query", Type: "integer", Format: "int32"}}},
	"get /v2/skill-marketplace-entries":                                        {ResponseSchema: "MarketplaceEntryList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}, {Name: "status", In: "query"}, {Name: "include_withdrawn", In: "query", Type: "boolean"}}},
	"post /v2/skill-marketplace-entries":                                       {RequestSchema: "CreateMarketplaceEntryRequest", RequestRequired: true, ResponseSchema: "MarketplaceEntry", SuccessStatuses: []string{"201"}},
	"get /v2/skill-marketplace-entries/{entry_id}":                             {ResponseSchema: "MarketplaceEntry", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}}},
	"patch /v2/skill-marketplace-entries/{entry_id}":                           {RequestSchema: "UpdateMarketplaceEntryRequest", RequestRequired: true, ResponseSchema: "MarketplaceEntry"},
	"post /v2/skill-marketplace-entries/{entry_id}/submit":                     {RequestSchema: "MarketplaceTransitionRequest", RequestRequired: true, ResponseSchema: "MarketplaceEntry"},
	"post /v2/skill-marketplace-entries/{entry_id}/publish":                    {RequestSchema: "MarketplaceTransitionRequest", RequestRequired: true, ResponseSchema: "MarketplaceEntry"},
	"post /v2/skill-marketplace-entries/{entry_id}/withdraw":                   {RequestSchema: "MarketplaceTransitionRequest", RequestRequired: true, ResponseSchema: "MarketplaceEntry"},
	"get /v2/skill-marketplace-policies":                                       {ResponseSchema: "MarketplacePolicyList", Parameters: []contractParameter{{Name: "organization_id", In: "query"}, {Name: "workspace_id", In: "query"}, {Name: "include_archived", In: "query", Type: "boolean"}}},
	"post /v2/skill-marketplace-policies":                                      {RequestSchema: "CreateMarketplacePolicyRequest", RequestRequired: true, ResponseSchema: "MarketplacePolicyResult", SuccessStatuses: []string{"201"}},
	"get /v2/skill-marketplace-policies/{policy_id}":                           {ResponseSchema: "MarketplacePolicyResult"},
	"post /v2/skill-marketplace-policies/{policy_id}/archive":                  {ResponseSchema: "MarketplacePolicy"},
	"post /v2/skill-marketplace-policies/{policy_id}/versions":                 {RequestSchema: "PublishMarketplacePolicyRequest", RequestRequired: true, ResponseSchema: "MarketplacePolicyVersion", SuccessStatuses: []string{"201"}},
	"get /v2/skill-marketplace-policies/{policy_id}/versions/{version}":        {ResponseSchema: "MarketplacePolicyVersion", IntegerPathParameters: map[string]bool{"version": true}},
	"post /v2/sessions":                                                        {RequestSchema: "CreateSessionRequest", RequestRequired: true, ResponseSchema: "Session", SuccessStatuses: []string{"201"}},
	"get /v2/sessions":                                                         {ResponseSchema: "SessionList"},
	"get /v2/session-comparisons":                                              {ResponseSchema: "SessionComparison", Parameters: []contractParameter{{Name: "left_session_id", In: "query", Required: true}, {Name: "right_session_id", In: "query", Required: true}}},
	"get /v2/sessions/{session_id}":                                            {ResponseSchema: "Session"},
	"patch /v2/sessions/{session_id}":                                          {RequestSchema: "UpdateSessionMetadataRequest", RequestRequired: true, ResponseSchema: "Session"},
	"delete /v2/sessions/{session_id}":                                         {SuccessStatuses: []string{"204"}},
	"post /v2/sessions/{session_id}/archive":                                   {ResponseSchema: "Session"},
	"post /v2/sessions/{session_id}/restore":                                   {ResponseSchema: "Session"},
	"post /v2/sessions/{session_id}/rerun":                                     {RequestSchema: "RerunSessionRequest", ResponseSchema: "RerunSessionResponse", SuccessStatuses: []string{"201"}},
	"patch /v2/sessions/{session_id}/runtime-settings":                         {RequestSchema: "UpdateSessionRuntimeSettingsRequest", RequestRequired: true, ResponseSchema: "Session"},
	"get /v2/sessions/{session_id}/runtime-config":                             {ResponseSchema: "AgentRuntimeConfig"},
	"get /v2/sessions/{session_id}/runtime-capabilities":                       {ResponseSchema: "SessionRuntimeCapabilities"},
	"post /v2/sessions/{session_id}/config/upgrade":                            {RequestSchema: "UpgradeSessionConfigRequest", RequestRequired: true, ResponseSchema: "UpgradeSessionConfigResult"},
	"get /v2/sessions/{session_id}/summary":                                    {ResponseSchema: "SessionSummary"},
	"put /v2/sessions/{session_id}/summary":                                    {RequestSchema: "UpsertSessionSummaryRequest", RequestRequired: true, ResponseSchema: "SessionSummary"},
	"get /v2/sessions/{session_id}/task-plan":                                  {ResponseSchema: "SessionTaskPlanCurrent"},
	"get /v2/sessions/{session_id}/task-plans":                                 {ResponseSchema: "SessionTaskPlanList"},
	"get /v2/sessions/{session_id}/usage":                                      {ResponseSchema: "SessionUsage"},
	"get /v2/sessions/{session_id}/trace":                                      {ResponseSchema: "TraceDocument", Parameters: []contractParameter{{Name: "turn_id", In: "query"}, {Name: "format", In: "query"}}},
	"get /v2/sessions/{session_id}/operator-audit":                             {ResponseSchema: "OperatorAuditList"},
	"get /v2/sessions/{session_id}/skill-usages":                               {ResponseSchema: "SkillUsageList", Parameters: []contractParameter{{Name: "turn_id", In: "query"}}},
	"get /v2/sessions/{session_id}/interventions":                              {ResponseSchema: "InterventionList", Parameters: []contractParameter{{Name: "status", In: "query"}}},
	"post /v2/sessions/{session_id}/interventions/{turn_id}/{call_id}/approve": {RequestSchema: "InterventionDecisionRequest", RequestRequired: true, ResponseSchema: "InterventionDecision"},
	"post /v2/sessions/{session_id}/interventions/{turn_id}/{call_id}/reject":  {RequestSchema: "InterventionDecisionRequest", RequestRequired: true, ResponseSchema: "InterventionDecision"},
	"post /v2/sessions/{session_id}/interventions/{turn_id}/{call_id}/respond": {RequestSchema: "InterventionDecisionRequest", RequestRequired: true, ResponseSchema: "InterventionDecision"},
	"post /v2/sessions/{session_id}/interventions/{turn_id}/{call_id}/skip":    {RequestSchema: "InterventionDecisionRequest", RequestRequired: true, ResponseSchema: "InterventionDecision"},
	"post /v2/sessions/{session_id}/interventions/{turn_id}/{call_id}/cancel":  {RequestSchema: "InterventionDecisionRequest", RequestRequired: true, ResponseSchema: "InterventionDecision"},
	"post /v2/sessions/{session_id}/events":                                    {RequestSchema: "AppendEventsRequest", RequestRequired: true, ResponseSchema: "AppendEventsResult", SuccessStatuses: []string{"201", "202"}},
	"get /v2/sessions/{session_id}/events":                                     {ResponseSchema: "EventList", Parameters: []contractParameter{{Name: "after_seq", In: "query"}}},
	"get /v2/sessions/{session_id}/events/stream":                              {ResponseSchema: "EventStream", ContentType: "text/event-stream", Parameters: []contractParameter{{Name: "after_seq", In: "query"}}},
	"post /v2/sessions/{session_id}/artifacts":                                 {RequestSchema: "CreateArtifactRequest", RequestRequired: true, ResponseSchema: "Artifact", SuccessStatuses: []string{"201"}},
	"get /v2/sessions/{session_id}/artifacts":                                  {ResponseSchema: "ArtifactList"},
	"post /v2/sessions/{session_id}/artifacts/upload":                          {RequestSchema: "ArtifactUploadRequest", RequestRequired: true, RequestContentType: "multipart/form-data", ResponseSchema: "ArtifactUpload", SuccessStatuses: []string{"201"}},
	"get /v2/sessions/{session_id}/artifacts/{artifact_id}/download":           {ResponseSchema: "BinaryContent", ContentType: "application/octet-stream"},
	"delete /v2/sessions/{session_id}/artifacts/{artifact_id}":                 {SuccessStatuses: []string{"204"}},
	"get /v2/sessions/{session_id}/deliberations":                              {ResponseSchema: "AgentDeliberationList"},
	"get /v2/sessions/{session_id}/deliberations/{deliberation_id}":            {ResponseSchema: "AgentDeliberationResponse"},
	"post /v2/sessions/{session_id}/deliberations/{deliberation_id}/cancel":    {RequestSchema: "CancelAgentDeliberationRequest", RequestRequired: true, ResponseSchema: "AgentDeliberationResponse"},
	"post /v2/sessions/{session_id}/deliberations/{deliberation_id}/participants/{participant_index}/retry": {RequestSchema: "RetryAgentDeliberationParticipantRequest", RequestRequired: true, ResponseSchema: "AgentDeliberationResponse", IntegerPathParameters: map[string]bool{"participant_index": true}},
	"post /v2/sessions/{session_id}/runs":                                            {RequestSchema: "StartRunRequest", RequestRequired: true, ResponseSchema: "StartRunResponse", SuccessStatuses: []string{"200", "201"}},
	"get /v2/sessions/{session_id}/runs":                                             {ResponseSchema: "RunList"},
	"get /v2/sessions/{session_id}/runs/{run_id}":                                    {ResponseSchema: "Run"},
	"post /v2/sessions/{session_id}/runs/{run_id}/cancel":                            {ResponseSchema: "Run"},
	"get /v2/sessions/{session_id}/runs/{run_id}/events":                             {ResponseSchema: "EventList", Parameters: []contractParameter{{Name: "after_seq", In: "query"}}},
	"get /v2/sessions/{session_id}/runs/{run_id}/events/stream":                      {ResponseSchema: "EventStream", ContentType: "text/event-stream", Parameters: []contractParameter{{Name: "after_seq", In: "query"}}},
	"get /v2/sessions/{session_id}/task-groups":                                      {ResponseSchema: "SessionTaskGroupList"},
	"get /v2/sessions/{session_id}/task-group-tree":                                  {ResponseSchema: "SessionTaskGroupTree"},
	"get /v2/sessions/{session_id}/task-groups/{group_id}":                           {ResponseSchema: "InspectorTaskGroupState"},
	"post /v2/sessions/{session_id}/task-groups/{group_id}/cancel":                   {RequestSchema: "CancelTaskGroupRequest", RequestRequired: true, ResponseSchema: "AgentTaskGroupResponse"},
	"post /v2/sessions/{session_id}/task-groups/{group_id}/retry":                    {ResponseSchema: "AgentTaskGroupResponse"},
	"post /v2/sessions/{session_id}/task-groups/{group_id}/items/{item_index}/retry": {ResponseSchema: "AgentTaskGroupResponse", IntegerPathParameters: map[string]bool{"item_index": true}},
	"post /v2/subagents/reap-orphans":                                                {RequestSchema: "ReapOrphanSubagentsRequest", RequestRequired: true, ResponseSchema: "ReapOrphanSubagentsResult"},
	"get /v2/traces":                                                                 {ResponseSchema: "TraceCatalog", Parameters: traceCatalogParameters()},
	"get /v2/traces/{trace_id}":                                                      {ResponseSchema: "TraceDocument", Parameters: []contractParameter{{Name: "search_limit", In: "query", Type: "integer", Format: "int32"}, {Name: "format", In: "query"}}},
	"get /v2/traces/{trace_id}/spans/{span_id}":                                      {ResponseSchema: "TraceSpanDetail", Parameters: []contractParameter{{Name: "search_limit", In: "query", Type: "integer", Format: "int32"}}},
	"get /v2/spans":                          {ResponseSchema: "TraceSpanCatalog", Parameters: traceSpanCatalogParameters()},
	"get /v2/workers":                        {ResponseSchema: "WorkerList", Parameters: []contractParameter{{Name: "workspace_id", In: "query"}, {Name: "status", In: "query"}}},
	"post /v2/workers/diagnose":              {RequestSchema: "WorkerDiagnoseRequest", RequestRequired: true, ResponseSchema: "WorkerDiagnoseResponse"},
	"post /v2/workers/reap-expired":          {RequestSchema: "ReapExpiredWorkersRequest", RequestRequired: true, ResponseSchema: "ReapExpiredWorkersResult"},
	"get /v2/workers/{worker_id}":            {ResponseSchema: "Worker"},
	"post /v2/workers/{worker_id}/archive":   {ResponseSchema: "Worker"},
	"post /v2/worker-work":                   {RequestSchema: "EnqueueWorkerWorkRequest", RequestRequired: true, ResponseSchema: "WorkerWork", SuccessStatuses: []string{"201"}},
	"post /v2/worker-work/reap-expired":      {RequestSchema: "ReapExpiredWorkerWorkRequest", RequestRequired: true, ResponseSchema: "ReapExpiredWorkerWorkResult"},
	"get /v2/worker-work/{work_id}":          {ResponseSchema: "WorkerWork"},
	"get /v2/worker-work/{work_id}/diagnose": {ResponseSchema: "WorkerWorkDiagnosis"},
	"post /v2/worker-work/{work_id}/cancel":  {RequestSchema: "CancelWorkerWorkRequest", RequestRequired: true, ResponseSchema: "WorkerWork"},
	"post /v2/worker-work/{work_id}/requeue": {RequestSchema: "RequeueWorkerWorkRequest", RequestRequired: true, ResponseSchema: "WorkerWork", SuccessStatuses: []string{"201"}},
}

func main() {
	source, err := os.ReadFile("internal/httpapi/server.go")
	if err != nil {
		panic(err)
	}
	routes := []route{}
	for _, match := range routePattern.FindAllStringSubmatch(string(source), -1) {
		path := "/v2" + parameterPattern.ReplaceAllString(match[2], `{$1}`)
		if excludedV2Route(match[1], path) {
			continue
		}
		routes = append(routes, route{Method: strings.ToLower(match[1]), Path: path})
	}
	routes = append(routes,
		route{Method: "post", Path: "/v2/sessions/{session_id}/runs"},
		route{Method: "get", Path: "/v2/sessions/{session_id}/runs"},
		route{Method: "get", Path: "/v2/sessions/{session_id}/runs/{run_id}"},
		route{Method: "post", Path: "/v2/sessions/{session_id}/runs/{run_id}/cancel"},
		route{Method: "get", Path: "/v2/sessions/{session_id}/runs/{run_id}/events"},
		route{Method: "get", Path: "/v2/sessions/{session_id}/runs/{run_id}/events/stream"},
	)
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})

	var output bytes.Buffer
	w := &output
	fmt.Fprint(w, `openapi: 3.0.3
info:
  title: TMA Server API
  version: 2.0.0-alpha.1
  description: User and control-plane contract for the TMA Go Core SDK.
servers:
  - url: /
paths:
`)
	lastPath := ""
	for _, item := range routes {
		if item.Path != lastPath {
			fmt.Fprintf(w, "  %s:\n", item.Path)
			lastPath = item.Path
		}
		fmt.Fprintf(w, "    %s:\n      operationId: %s\n", item.Method, operationID(item.Method, item.Path))
		contract, typed := coreContracts[item.Method+" "+item.Path]
		if !typed {
			panic(fmt.Sprintf("public v2 route %s %s has no explicit OpenAPI contract", item.Method, item.Path))
		}
		parameters := pathParameters(item.Path)
		if len(parameters) > 0 || len(contract.Parameters) > 0 {
			fmt.Fprint(w, "      parameters:\n")
			for _, parameter := range parameters {
				if contract.IntegerPathParameters[parameter] {
					fmt.Fprintf(w, "        - name: %s\n          in: path\n          required: true\n          schema: {type: integer, format: int32}\n", parameter)
				} else {
					fmt.Fprintf(w, "        - name: %s\n          in: path\n          required: true\n          schema: {type: string}\n", parameter)
				}
			}
			for _, parameter := range contract.Parameters {
				parameterType := parameter.Type
				if parameterType == "" {
					parameterType = "string"
				}
				if parameter.Array {
					fmt.Fprintf(w, "        - name: %s\n          in: %s\n          required: %t\n          style: form\n          explode: true\n          schema:\n            type: array\n            items: {type: %s}\n", parameter.Name, parameter.In, parameter.Required, parameterType)
				} else if parameter.Format == "int64" {
					fmt.Fprintf(w, "        - name: %s\n          in: %s\n          required: %t\n          schema: {type: %s, format: int64, maximum: 9007199254740991}\n", parameter.Name, parameter.In, parameter.Required, parameterType)
				} else if parameter.Format != "" {
					fmt.Fprintf(w, "        - name: %s\n          in: %s\n          required: %t\n          schema: {type: %s, format: %s}\n", parameter.Name, parameter.In, parameter.Required, parameterType, parameter.Format)
				} else {
					fmt.Fprintf(w, "        - name: %s\n          in: %s\n          required: %t\n          schema: {type: %s}\n", parameter.Name, parameter.In, parameter.Required, parameterType)
				}
				if parameter.Description != "" {
					fmt.Fprintf(w, "          description: %q\n", parameter.Description)
				}
			}
		}
		if contract.RequestSchema != "" {
			requestContentType := contract.RequestContentType
			if requestContentType == "" {
				requestContentType = "application/json"
			}
			fmt.Fprintf(w, "      requestBody:\n        required: %t\n        content:\n          %s:\n            schema:\n              $ref: \"#/components/schemas/%s\"\n", contract.RequestRequired, requestContentType, contract.RequestSchema)
		}
		writeTypedResponses(w, contract)
	}
	fmt.Fprint(w, `components:
  schemas:
    ErrorEnvelope:
      type: object
      required: [error]
      properties:
        error:
          $ref: "#/components/schemas/APIError"
    APIError:
      type: object
      required: [code, message, request_id, retryable]
      properties:
        code: {type: string, pattern: "^[a-z][a-z0-9_]*$"}
        message: {type: string}
        request_id: {type: string}
        retryable: {type: boolean}
        details:
          type: object
          additionalProperties: true
          x-tma-dynamic-json: true
          description: Stable machine-readable error details whose fields depend on error.code.
    DynamicJSONValue:
      description: Extension JSON whose shape is selected by the surrounding event, schema, provider, or runtime contract.
      x-tma-dynamic-json: true
    Event:
      type: object
      required: [id, session_id, seq, type, created_at]
      properties:
        id: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        seq: {type: integer, format: int64, maximum: 9007199254740991}
        type: {type: string}
        payload: {$ref: "#/components/schemas/DynamicJSONValue"}
        created_at: {type: string, format: date-time}
    AgentTaskGroupItemTemplate:
      type: object
      required: [message]
      properties:
        agent_id: {type: string}
        agent: {type: string}
        environment_id: {type: string}
        title: {type: string}
        message: {type: string}
        priority: {type: integer, format: int32}
        expected_result_schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    AgentTaskGroupTemplate:
      type: object
      required: [id, title, description, strategy, result_reducer]
      properties:
        id: {type: string}
        title: {type: string}
        description: {type: string}
        strategy: {type: string}
        result_reducer: {type: string}
        quorum: {type: integer, format: int32}
        fail_fast: {type: boolean}
        items_required: {type: boolean}
        default_items: {type: array, items: {$ref: "#/components/schemas/AgentTaskGroupItemTemplate"}}
        item_expected_result_schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    AgentTaskGroupTemplateList:
      type: object
      required: [templates]
      properties:
        templates: {type: array, items: {$ref: "#/components/schemas/AgentTaskGroupTemplate"}}
    AgentDiscussionStrategy:
      type: object
      required: [id, title, description]
      properties:
        id: {type: string}
        title: {type: string}
        description: {type: string}
    AgentDiscussionStrategyList:
      type: object
      required: [strategies, team_plan_schema]
      properties:
        strategies: {type: array, items: {$ref: "#/components/schemas/AgentDiscussionStrategy"}}
        team_plan_schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    EventList:
      type: object
      required: [events]
      properties:
        events:
          type: array
          items: {$ref: "#/components/schemas/Event"}
    EventStream:
      type: string
      description: Server-sent event stream whose data fields contain Event JSON.
    AgentConfigVersion:
      type: object
      required: [version, llm_provider, llm_model, system, created_at]
      properties:
        version: {type: integer, format: int32}
        llm_provider: {type: string}
        llm_model: {type: string}
        system: {type: string}
        tools: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        mcp: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        skills: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        created_at: {type: string, format: date-time}
    Agent:
      type: object
      required: [id, workspace_id, owner_type, owner_id, visibility, agent_kind, name, current_config_version, config_version, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        owner_type: {type: string, enum: [user, workspace]}
        owner_id: {type: string}
        visibility: {type: string, enum: [private, workspace]}
        agent_kind: {type: string, enum: [general, custom]}
        name: {type: string}
        current_config_version: {type: integer, format: int32}
        config_version: {$ref: "#/components/schemas/AgentConfigVersion"}
        archived_at: {type: string, format: date-time, nullable: true}
        created_at: {type: string, format: date-time}
    PortableAgentConfig:
      type: object
      required: [name, llm_provider, llm_model, system]
      properties:
        name: {type: string}
        llm_provider: {type: string}
        llm_model: {type: string}
        system: {type: string}
        tools: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        mcp: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        skills: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    AgentImportRequest:
      type: object
      required: [format, schema_version, agent]
      properties:
        format: {type: string, enum: [tma.agent]}
        schema_version: {type: integer, format: int32, enum: [1]}
        agent: {$ref: "#/components/schemas/PortableAgentConfig"}
        name: {type: string}
        workspace_id: {type: string}
    AgentExportDocument:
      type: object
      required: [format, schema_version, exported_at, agent]
      properties:
        format: {type: string, enum: [tma.agent]}
        schema_version: {type: integer, format: int32, enum: [1]}
        exported_at: {type: string, format: date-time}
        source_agent_id: {type: string}
        source_config_version: {type: integer, format: int32}
        agent: {$ref: "#/components/schemas/PortableAgentConfig"}
        workspace_id: {type: string}
    AgentConfigRollbackResponse:
      type: object
      required: [agent, previous_version, source_version, new_version]
      properties:
        agent: {$ref: "#/components/schemas/Agent"}
        previous_version: {type: integer, format: int32}
        source_version: {type: integer, format: int32}
        new_version: {type: integer, format: int32}
    ToolingHealthRequest:
      type: object
      properties:
        kind: {type: string}
        identifier: {type: string}
    ToolingHealthItem:
      type: object
      required: [identifier, kind, status]
      properties:
        identifier: {type: string}
        kind: {type: string}
        status: {type: string}
        detail: {type: string}
        latency_ms: {type: integer, format: int64, maximum: 9007199254740991}
        tool_count: {type: integer, format: int32}
        version: {type: integer, format: int32}
        server_name: {type: string}
        transport: {type: string}
        estimated_tokens: {type: integer, format: int32}
        capabilities: {type: array, items: {type: string}}
        resource_count: {type: integer, format: int32}
        resource_template_count: {type: integer, format: int32}
        prompt_count: {type: integer, format: int32}
    MCPHostStats:
      type: object
      properties:
        sessions: {type: integer, format: int32}
        in_use_sessions: {type: integer, format: int32}
        max_sessions: {type: integer, format: int32}
        idle_timeout_seconds: {type: integer, format: int64, maximum: 9007199254740991}
        sweep_interval_seconds: {type: integer, format: int64, maximum: 9007199254740991}
        starts_total: {type: integer, format: int64, maximum: 9007199254740991}
        stops_total: {type: integer, format: int64, maximum: 9007199254740991}
        discards_total: {type: integer, format: int64, maximum: 9007199254740991}
        reaped_total: {type: integer, format: int64, maximum: 9007199254740991}
        evictions_total: {type: integer, format: int64, maximum: 9007199254740991}
        rejections_total: {type: integer, format: int64, maximum: 9007199254740991}
        log_messages_by_level: {type: object, additionalProperties: {type: integer, format: int64, maximum: 9007199254740991}}
    MCPHTTPHostStats:
      allOf:
        - {$ref: "#/components/schemas/MCPHostStats"}
        - type: object
          properties:
            delete_errors_total: {type: integer, format: int64, maximum: 9007199254740991}
            egress_policy_enabled: {type: boolean}
            egress_allow_http: {type: boolean}
            egress_allow_private_networks: {type: boolean}
            egress_allowed_host_count: {type: integer, format: int32}
            egress_allowed_cidr_count: {type: integer, format: int32}
            egress_blocked_total: {type: integer, format: int64, maximum: 9007199254740991}
    MCPRuntimeGuardStats:
      type: object
      properties:
        tracked_servers: {type: integer, format: int32}
        in_flight: {type: integer, format: int32}
        open_circuits: {type: integer, format: int32}
        calls_total: {type: integer, format: int64, maximum: 9007199254740991}
        successes_total: {type: integer, format: int64, maximum: 9007199254740991}
        failures_total: {type: integer, format: int64, maximum: 9007199254740991}
        circuit_rejected_total: {type: integer, format: int64, maximum: 9007199254740991}
        wait_canceled_total: {type: integer, format: int64, maximum: 9007199254740991}
        failures_by_class: {type: object, additionalProperties: {type: integer, format: int64, maximum: 9007199254740991}}
    ToolingHealthResponse:
      type: object
      required: [agent_id, checked_at, mcp, skills]
      properties:
        agent_id: {type: string}
        checked_at: {type: string, format: date-time}
        mcp: {type: array, items: {$ref: "#/components/schemas/ToolingHealthItem"}}
        skills: {type: array, items: {$ref: "#/components/schemas/ToolingHealthItem"}}
        mcp_host: {$ref: "#/components/schemas/MCPHostStats"}
        mcp_http_host: {$ref: "#/components/schemas/MCPHTTPHostStats"}
        mcp_runtime_guard: {$ref: "#/components/schemas/MCPRuntimeGuardStats"}
    AgentList:
      type: object
      required: [agents]
      properties:
        agents: {type: array, items: {$ref: "#/components/schemas/Agent"}}
    AgentConfigVersionList:
      type: object
      required: [config_versions]
      properties:
        config_versions: {type: array, items: {$ref: "#/components/schemas/AgentConfigVersion"}}
    CreateAgentRequest:
      type: object
      required: [name, system]
      properties:
        workspace_id: {type: string}
        owner_type: {type: string, enum: [user, workspace]}
        owner_id: {type: string}
        visibility: {type: string, enum: [private, workspace]}
        agent_kind: {type: string, enum: [general, custom]}
        name: {type: string}
        llm_provider: {type: string}
        llm_model: {type: string}
        model: {type: string, deprecated: true}
        system: {type: string}
        tools: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        mcp: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        skills: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    UpdateAgentRequest:
      type: object
      properties:
        name: {type: string}
        llm_provider: {type: string}
        llm_model: {type: string}
        model: {type: string, deprecated: true}
        system: {type: string}
        tools: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        mcp: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        skills: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    CreateAgentConfigVersionRequest:
      allOf:
        - {$ref: "#/components/schemas/UpdateAgentRequest"}
    Environment:
      type: object
      required: [id, workspace_id, name, config, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        name: {type: string}
        config: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        archived_at: {type: string, format: date-time, nullable: true}
        created_at: {type: string, format: date-time}
    CreateEnvironmentRequest:
      type: object
      required: [name, config]
      properties:
        workspace_id: {type: string}
        name: {type: string}
        config: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    LLMProvider:
      type: object
      required: [id, provider_type, enabled, revision, created_at, updated_at]
      properties:
        id: {type: string}
        provider_type: {type: string}
        base_url: {type: string}
        api_key_env: {type: string}
        enabled: {type: boolean}
        revision: {type: integer, format: int64, maximum: 9007199254740991}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
    LLMProviderList:
      type: object
      required: [providers]
      properties:
        providers: {type: array, items: {$ref: "#/components/schemas/LLMProvider"}}
    CreateLLMProviderRequest:
      type: object
      required: [id, provider_type]
      properties:
        id: {type: string}
        provider_type: {type: string}
        base_url: {type: string}
        api_key_env: {type: string}
        enabled: {type: boolean}
    UpdateLLMProviderRequest:
      type: object
      properties:
        provider_type: {type: string}
        base_url: {type: string}
        api_key_env: {type: string}
        enabled: {type: boolean}
    LLMModel:
      type: object
      required: [provider_id, model, context_window_tokens, capability_type, capabilities, is_default_vision, is_default_embedding, is_default_reranker, revision, created_at, updated_at]
      properties:
        provider_id: {type: string}
        model: {type: string}
        context_window_tokens: {type: integer, format: int32}
        capability_type:
          type: string
          enum: [text, text_image, image_generation, video_generation, embedding, reranker]
        capabilities: {$ref: "#/components/schemas/LLMModelCapabilities"}
        is_default_vision: {type: boolean}
        is_default_embedding: {type: boolean}
        is_default_reranker: {type: boolean}
        revision: {type: integer, format: int64, maximum: 9007199254740991}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
    LLMModelCapabilities:
      type: object
      properties:
        dimensions: {type: integer, format: int32, minimum: 1, maximum: 65535}
        distance_metric:
          type: string
          enum: [cosine, l2, inner_product]
        normalized: {type: boolean}
        max_batch_size: {type: integer, format: int32, minimum: 1, maximum: 4096}
        max_candidates: {type: integer, format: int32, minimum: 1, maximum: 1000}
        protocol: {type: string}
    LLMModelList:
      type: object
      required: [models]
      properties:
        models: {type: array, items: {$ref: "#/components/schemas/LLMModel"}}
    PutLLMModelRequest:
      type: object
      required: [provider_id, model]
      properties:
        provider_id: {type: string}
        model: {type: string}
        context_window_tokens: {type: integer, format: int32}
        capability_type:
          type: string
          enum: [text, text_image, image_generation, video_generation, embedding, reranker]
        capabilities: {$ref: "#/components/schemas/LLMModelCapabilities"}
        is_default_vision: {type: boolean}
        is_default_embedding: {type: boolean}
        is_default_reranker: {type: boolean}
    LLMDiagnosticResult:
      type: object
      required: [status, latency_ms, authenticated, message, retryable, checked_at]
      properties:
        status: {type: string, enum: [succeeded, failed]}
        capability_type: {type: string}
        protocol: {type: string}
        latency_ms: {type: integer, format: int64, maximum: 9007199254740991}
        dimensions: {type: integer, format: int32, minimum: 1}
        candidate_count: {type: integer, format: int32, minimum: 1}
        authenticated: {type: boolean}
        error_type:
          type: string
          enum: [configuration, authentication, rate_limit, timeout, network, invalid_request, invalid_response, dimension_mismatch, unsupported, upstream]
        message: {type: string}
        retryable: {type: boolean}
        checked_at: {type: string, format: date-time}
    LLMUsageSummary:
      type: object
      properties:
        record_count: {type: integer, format: int32}
        input_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        output_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        total_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        cached_input_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        reasoning_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        latency_ms: {type: integer, format: int64, maximum: 9007199254740991}
    LLMUsageRecord:
      type: object
      required: [id, workspace_id, agent_id, agent_config_version, session_id, turn_id, provider_id, model, input_tokens, output_tokens, total_tokens, cached_input_tokens, reasoning_tokens, latency_ms, status, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        agent_id: {type: string}
        agent_config_version: {type: integer, format: int32}
        session_id: {type: string}
        turn_id: {type: string}
        provider_id: {type: string}
        provider_type: {type: string}
        model: {type: string}
        input_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        output_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        total_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        cached_input_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        reasoning_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        latency_ms: {type: integer, format: int64, maximum: 9007199254740991}
        status: {type: string}
        error_message: {type: string}
        created_at: {type: string, format: date-time}
    LLMUsageAggregateReport:
      type: object
      required: [group_by, filters, summary, groups]
      properties:
        group_by: {type: string}
        filters: {$ref: "#/components/schemas/LLMUsageFilters"}
        summary: {$ref: "#/components/schemas/LLMUsageSummary"}
        groups:
          type: array
          items:
            type: object
            properties:
              provider_id: {type: string}
              model: {type: string}
              summary: {$ref: "#/components/schemas/LLMUsageSummary"}
    LLMUsageFilters:
      type: object
      properties:
        workspace_id: {type: string}
        provider_id: {type: string}
        model: {type: string}
        status: {type: string}
        group_by: {type: string}
        from: {type: string, format: date-time}
        to: {type: string, format: date-time}
    Session:
      type: object
      required: [id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, status, tags, created_by, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        agent_id: {type: string}
        agent_config_version: {type: integer, format: int32}
        environment_id: {type: string}
        parent_session_id: {type: string}
        parent_turn_id: {type: string}
        spawn_depth: {type: integer, format: int32}
        status: {type: string}
        title: {type: string}
        sandbox_id: {type: string}
        runtime_settings: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        pinned_at: {type: string, format: date-time, nullable: true}
        tags: {type: array, items: {type: string}}
        summary_text: {type: string}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
        archived_at: {type: string, format: date-time, nullable: true}
    SessionList:
      type: object
      required: [sessions]
      properties:
        sessions: {type: array, items: {$ref: "#/components/schemas/Session"}}
    CreateSessionRequest:
      type: object
      required: [environment_id]
      properties:
        workspace_id: {type: string}
        owner_id: {type: string}
        agent_id: {type: string}
        environment_id: {type: string}
        title: {type: string}
        created_by: {type: string}
        parent_session_id: {type: string}
        parent_turn_id: {type: string}
    UpdateSessionMetadataRequest:
      type: object
      properties:
        pinned: {type: boolean}
        tags: {type: array, items: {type: string}}
    UpdateSessionRuntimeSettingsRequest:
      type: object
      properties:
        llm_provider: {type: string}
        llm_model: {type: string}
        intervention_mode: {type: string}
        tool_runtime: {type: string}
        cloud_sandbox_root: {type: string}
        cloud_sandbox_image: {type: string}
        cloud_sandbox_allow_network: {type: boolean}
        agent_config_update_policy: {type: string, enum: [follow_latest, pinned], description: Defaults to follow_latest.}
        human_interaction: {$ref: "#/components/schemas/HumanInteractionRuntimeSettings"}
        completion_gate: {$ref: "#/components/schemas/CompletionGateRuntimeSettings"}
    HumanInteractionRuntimeSettings:
      type: object
      properties:
        enabled: {type: boolean}
        modes:
          type: array
          items: {type: string, enum: [select, multiselect, form, freeform]}
        supports_upload: {type: boolean}
        fallback: {type: string, enum: [assistant_message, fail]}
    CompletionGateRuntimeSettings:
      type: object
      properties:
        max_retries: {type: integer, format: int32, minimum: 1, maximum: 10, description: Defaults to 3 when omitted.}
    AgentRuntimeConfig:
      type: object
      required: [session_id, workspace_id, owner_id, agent_id, agent_config_version, environment_id, llm_provider, llm_model, context_window_tokens, llm_capability_type, system]
      properties:
        session_id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        agent_id: {type: string}
        agent_config_version: {type: integer, format: int32}
        environment_id: {type: string}
        llm_provider: {type: string}
        llm_provider_type: {type: string}
        llm_model: {type: string}
        llm_base_url: {type: string}
        llm_api_key_env: {type: string}
        context_window_tokens: {type: integer, format: int32}
        llm_capability_type: {type: string}
        vision_llm_provider: {type: string}
        vision_llm_provider_type: {type: string}
        vision_llm_model: {type: string}
        vision_llm_base_url: {type: string}
        vision_llm_api_key_env: {type: string}
        summary_text: {type: string}
        summary_source_until_seq: {type: integer, format: int64, maximum: 9007199254740991}
        system: {type: string}
        runtime_settings: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        tools: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        mcp: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        skills: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    SessionRuntimeCapabilities:
      type: object
      required: [default_runtime, available_runtimes]
      properties:
        default_runtime: {type: string}
        available_runtimes: {type: array, items: {type: string}}
        human_interaction: {$ref: "#/components/schemas/HumanInteractionRuntimeCapabilities"}
    HumanInteractionRuntimeCapabilities:
      type: object
      required: [enabled, modes, supports_upload, fallback]
      properties:
        enabled: {type: boolean}
        modes: {type: array, items: {type: string}}
        supports_upload: {type: boolean}
        fallback: {type: string}
    RerunSessionRequest:
      allOf:
        - {$ref: "#/components/schemas/UpdateSessionRuntimeSettingsRequest"}
        - type: object
          properties:
            title: {type: string}
            message_seq: {type: integer, format: int64, maximum: 9007199254740991}
    RerunSessionResponse:
      type: object
      required: [source_session_id, source_event_seq, session, events]
      properties:
        source_session_id: {type: string}
        source_event_seq: {type: integer, format: int64, maximum: 9007199254740991}
        session: {$ref: "#/components/schemas/Session"}
        events: {type: array, items: {$ref: "#/components/schemas/Event"}}
    UpgradeSessionConfigRequest:
      type: object
      properties:
        to_current: {type: boolean}
        to_version: {type: integer, format: int32}
        updated_by: {type: string}
    UpgradeSessionConfigResult:
      type: object
      required: [changed, old_agent_config_version, new_agent_config_version]
      properties:
        session: {$ref: "#/components/schemas/Session"}
        event: {$ref: "#/components/schemas/Event"}
        old_agent_config_version: {type: integer, format: int32}
        new_agent_config_version: {type: integer, format: int32}
        latest_agent_config_version: {type: integer, format: int32}
        changed: {type: boolean}
    Run:
      type: object
      required: [id, session_id, agent_id, agent_config_version, status, attempt, started_at]
      properties:
        id: {type: string}
        session_id: {type: string}
        agent_id: {type: string}
        agent_config_version: {type: integer, format: int32}
        status: {type: string, enum: [running, waiting_approval, waiting_human, completed, failed, interrupted]}
        user_event_id: {type: string}
        user_event_seq: {type: integer, format: int64, maximum: 9007199254740991}
        attempt: {type: integer, format: int32}
        started_at: {type: string, format: date-time}
        ended_at: {type: string, format: date-time, nullable: true}
        interrupt_requested_at: {type: string, format: date-time, nullable: true}
        error_message: {type: string}
        idempotency_key: {type: string}
    RunList:
      type: object
      required: [runs]
      properties:
        runs: {type: array, items: {$ref: "#/components/schemas/Run"}}
    StartRunRequest:
      type: object
      required: [input]
      properties:
        input: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        idempotency_key: {type: string}
    StartRunResponse:
      type: object
      required: [run, created]
      properties:
        run: {$ref: "#/components/schemas/Run"}
        events: {type: array, items: {$ref: "#/components/schemas/Event"}}
        created: {type: boolean}
    AppendEvent:
      type: object
      required: [type]
      properties:
        type: {type: string}
        payload: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    AppendEventsRequest:
      type: object
      required: [events]
      properties:
        events: {type: array, items: {$ref: "#/components/schemas/AppendEvent"}}
        prefer_latest: {type: boolean}
    AppendEventsResult:
      type: object
      properties:
        events: {type: array, items: {$ref: "#/components/schemas/Event"}}
        queued: {type: boolean}
        queue_request: {$ref: "#/components/schemas/SubagentStartRequest"}
    Intervention:
      type: object
      required: [session_id, turn_id, call_id, status]
      properties:
        session_id: {type: string}
        turn_id: {type: string}
        call_id: {type: string}
        tool_identifier: {type: string}
        api_name: {type: string}
        arguments: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        kind: {type: string, enum: [tool_approval, clarification, plan_approval, upload_request]}
        request: {x-tma-dynamic-json: true}
        response: {x-tma-dynamic-json: true}
        intervention_mode: {type: string}
        reason: {type: string}
        status: {type: string, enum: [pending, approved, rejected, answered, skipped, canceled, expired]}
        decision_reason: {type: string}
        requested_at: {type: string, format: date-time}
        decided_at: {type: string, format: date-time, nullable: true}
        responded_at: {type: string, format: date-time, nullable: true}
        expires_at: {type: string, format: date-time, nullable: true}
    InterventionList:
      type: object
      required: [interventions]
      properties:
        interventions: {type: array, items: {$ref: "#/components/schemas/Intervention"}}
    InterventionDecisionRequest:
      type: object
      properties:
        reason: {type: string}
        response: {x-tma-dynamic-json: true}
    InterventionDecision:
      type: object
      required: [intervention, events]
      properties:
        intervention: {$ref: "#/components/schemas/Intervention"}
        events: {type: array, items: {$ref: "#/components/schemas/Event"}}
    SessionSummary:
      type: object
      required: [session_id, summary_text, source_until_seq, created_at, updated_at]
      properties:
        session_id: {type: string}
        summary_text: {type: string}
        source_until_seq: {type: integer, format: int64, maximum: 9007199254740991}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
    SessionTaskItem:
      type: object
      required: [id, plan_id, index, description, status, evidence_refs, created_at, updated_at]
      properties:
        id: {type: string}
        plan_id: {type: string}
        index: {type: integer, format: int32, minimum: 0}
        description: {type: string}
        status: {type: string, enum: [pending, in_progress, completed, blocked]}
        evidence: {type: string}
        evidence_refs: {type: array, items: {$ref: "#/components/schemas/TaskEvidenceRef"}}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
        completed_at: {type: string, format: date-time, nullable: true}
    TaskEvidenceRef:
      type: object
      required: [kind, turn_id, tool_call_id, tool]
      properties:
        kind: {type: string, enum: [tool_result]}
        turn_id: {type: string}
        tool_call_id: {type: string}
        tool: {type: string}
        artifact_ids: {type: array, items: {type: string}}
    SessionTaskPlan:
      type: object
      required: [id, workspace_id, owner_id, session_id, goal, handling_mode, status, items, created_at, updated_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        session_id: {type: string}
        created_turn_id: {type: string}
        updated_turn_id: {type: string}
        title: {type: string}
        goal: {type: string}
        handling_mode: {type: string, enum: [tracked, planned]}
        status: {type: string, enum: [active, completed, canceled, superseded]}
        items: {type: array, items: {$ref: "#/components/schemas/SessionTaskItem"}}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
        completed_at: {type: string, format: date-time, nullable: true}
    SessionTaskPlanCurrent:
      type: object
      required: [plan]
      properties:
        plan: {$ref: "#/components/schemas/SessionTaskPlan"}
    SessionTaskPlanList:
      type: object
      required: [plans]
      properties:
        plans: {type: array, items: {$ref: "#/components/schemas/SessionTaskPlan"}}
    UpsertSessionSummaryRequest:
      type: object
      required: [summary_text, source_until_seq]
      properties:
        summary_text: {type: string}
        source_until_seq: {type: integer, format: int64, maximum: 9007199254740991}
    SessionUsage:
      type: object
      required: [session_id, summary, records]
      properties:
        session_id: {type: string}
        summary: {$ref: "#/components/schemas/LLMUsageSummary"}
        records: {type: array, items: {$ref: "#/components/schemas/LLMUsageRecord"}}
    Artifact:
      type: object
      required: [id, workspace_id, session_id, object_ref_id, name, artifact_type, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        session_id: {type: string}
        environment_id: {type: string}
        object_ref_id: {type: string}
        turn_id: {type: string}
        tool_call_id: {type: string}
        name: {type: string}
        description: {type: string}
        artifact_type: {type: string}
        content_type: {type: string}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
    ArtifactList:
      type: object
      required: [artifacts]
      properties:
        artifacts: {type: array, items: {$ref: "#/components/schemas/Artifact"}}
    SessionComparisonSide:
      type: object
      required: [session, llm_provider, llm_model, prompt, result, duration_ms, usage, artifacts]
      properties:
        session: {$ref: "#/components/schemas/Session"}
        llm_provider: {type: string}
        llm_model: {type: string}
        prompt: {type: string}
        result: {type: string}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        usage:
          type: object
          required: [session_id, summary, records]
          properties:
            session_id: {type: string}
            summary: {$ref: "#/components/schemas/LLMUsageSummary"}
            records: {type: array, items: {$ref: "#/components/schemas/LLMUsageRecord"}}
        artifacts: {type: array, items: {$ref: "#/components/schemas/Artifact"}}
    SessionComparison:
      type: object
      required: [left, right]
      properties:
        left: {$ref: "#/components/schemas/SessionComparisonSide"}
        right: {$ref: "#/components/schemas/SessionComparisonSide"}
    CreateArtifactRequest:
      type: object
      required: [object_ref_id]
      properties:
        environment_id: {type: string}
        object_ref_id: {type: string}
        turn_id: {type: string}
        tool_call_id: {type: string}
        name: {type: string}
        description: {type: string}
        artifact_type: {type: string}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        created_by: {type: string}
    ArtifactUploadRequest:
      type: object
      required: [file]
      properties:
        file: {type: string, format: binary}
        bucket: {type: string}
        object_key: {type: string}
        content_type: {type: string}
        visibility: {type: string}
        environment_id: {type: string}
        turn_id: {type: string}
        tool_call_id: {type: string}
        name: {type: string}
        description: {type: string}
        artifact_type: {type: string}
        metadata: {type: string, description: JSON object encoded as a multipart text field.}
        created_by: {type: string}
    ArtifactUpload:
      type: object
      required: [object_ref, artifact]
      properties:
        object_ref: {$ref: "#/components/schemas/ObjectRef"}
        artifact: {$ref: "#/components/schemas/Artifact"}
        workspace_path: {type: string}
    SubagentTaskGroup:
      type: object
      required: [id, workspace_id, owner_id, parent_session_id, strategy, result_reducer, planned_count, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        parent_session_id: {type: string}
        parent_turn_id: {type: string}
        parent_group_id: {type: string}
        parent_item_index: {type: integer, format: int32}
        strategy: {type: string}
        result_reducer: {type: string}
        quorum: {type: integer, format: int32}
        fail_fast: {type: boolean}
        planned_count: {type: integer, format: int32}
        created_at: {type: string, format: date-time}
        canceled_at: {type: string, format: date-time, nullable: true}
        cancel_reason: {type: string}
    SubagentTaskGroupItem:
      type: object
      required: [group_id, item_index, agent_id, environment_id, initial_state, created_at]
      properties:
        group_id: {type: string}
        item_index: {type: integer, format: int32}
        agent_id: {type: string}
        environment_id: {type: string}
        session_id: {type: string}
        title: {type: string}
        message: {type: string}
        priority: {type: integer, format: int32}
        initial_state: {type: string}
        error_type: {type: string}
        error_message: {type: string}
        expected_result_schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        retry_count: {type: integer, format: int32}
        created_at: {type: string, format: date-time}
    SubagentStartRequest:
      type: object
      required: [id, workspace_id, owner_id, session_id, parent_session_id, status, priority, queued_at, expires_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        session_id: {type: string}
        parent_session_id: {type: string}
        parent_turn_id: {type: string}
        payload: {$ref: "#/components/schemas/DynamicJSONValue"}
        status: {type: string}
        priority: {type: integer, format: int32}
        queued_at: {type: string, format: date-time}
        expires_at: {type: string, format: date-time}
        started_at: {type: string, format: date-time, nullable: true}
        turn_id: {type: string}
        canceled_at: {type: string, format: date-time, nullable: true}
        cancel_reason: {type: string}
        wait_seconds: {type: integer, format: int64, maximum: 9007199254740991}
    AgentTaskGroupSummary:
      type: object
      required: [total, completed, failed, canceled, terminated, rejected, queued, running, waiting, terminal, status]
      properties:
        total: {type: integer, format: int32}
        completed: {type: integer, format: int32}
        failed: {type: integer, format: int32}
        canceled: {type: integer, format: int32}
        terminated: {type: integer, format: int32}
        rejected: {type: integer, format: int32}
        queued: {type: integer, format: int32}
        running: {type: integer, format: int32}
        waiting: {type: integer, format: int32}
        terminal: {type: integer, format: int32}
        status: {type: string}
    AgentTaskGroupAggregate:
      type: object
      required: [reducer]
      properties:
        reducer: {type: string}
        text: {type: string}
        json: {$ref: "#/components/schemas/DynamicJSONValue"}
        schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        completed_item_indexes: {type: array, items: {type: integer, format: int32}}
        failed_item_indexes: {type: array, items: {type: integer, format: int32}}
        canceled_item_indexes: {type: array, items: {type: integer, format: int32}}
    AgentTaskGroupItemState:
      type: object
      required: [item, status, result_valid]
      properties:
        item: {$ref: "#/components/schemas/SubagentTaskGroupItem"}
        session: {$ref: "#/components/schemas/Session"}
        status: {type: string}
        last_turn_status: {type: string}
        reason: {type: string}
        queue_request: {$ref: "#/components/schemas/SubagentStartRequest"}
        pending_approvals: {type: array, items: {$ref: "#/components/schemas/Intervention"}}
        agent_text: {type: string}
        result_json: {$ref: "#/components/schemas/DynamicJSONValue"}
        result_schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        result_valid: {type: boolean}
        result_validation_error: {type: string}
        event_count: {type: integer, format: int32}
        nested_groups: {type: array, items: {$ref: "#/components/schemas/AgentTaskGroupNestedState"}}
    AgentTaskGroupNestedState:
      type: object
      required: [group, status, summary, aggregate, items]
      properties:
        group: {$ref: "#/components/schemas/SubagentTaskGroup"}
        status: {type: string}
        completed: {type: boolean}
        summary: {$ref: "#/components/schemas/AgentTaskGroupSummary"}
        aggregate: {$ref: "#/components/schemas/AgentTaskGroupAggregate"}
        items: {type: array, items: {$ref: "#/components/schemas/AgentTaskGroupItemState"}}
    AgentTaskGroupResponse:
      allOf:
        - {$ref: "#/components/schemas/AgentTaskGroupNestedState"}
    InspectorTaskGroupState:
      type: object
      required: [state]
      properties:
        template_id: {type: string}
        template_title: {type: string}
        state: {$ref: "#/components/schemas/AgentTaskGroupResponse"}
    SessionTaskGroupList:
      type: object
      required: [task_groups]
      properties:
        task_groups: {type: array, items: {$ref: "#/components/schemas/InspectorTaskGroupState"}}
    SessionTaskGroupTreeSummary:
      type: object
      required: [sessions, groups, items, queued, running, rejected, waiting, max_wait_seconds]
      properties:
        sessions: {type: integer, format: int32}
        groups: {type: integer, format: int32}
        items: {type: integer, format: int32}
        queued: {type: integer, format: int32}
        running: {type: integer, format: int32}
        rejected: {type: integer, format: int32}
        waiting: {type: integer, format: int32}
        max_wait_seconds: {type: integer, format: int64, maximum: 9007199254740991}
    SessionTaskGroupTreeNode:
      type: object
      required: [session, task_groups, children]
      properties:
        session: {$ref: "#/components/schemas/Session"}
        task_groups: {type: array, items: {$ref: "#/components/schemas/InspectorTaskGroupState"}}
        children: {type: array, items: {$ref: "#/components/schemas/SessionTaskGroupTreeNode"}}
    SessionTaskGroupTree:
      type: object
      required: [root, summary]
      properties:
        root: {$ref: "#/components/schemas/SessionTaskGroupTreeNode"}
        summary: {$ref: "#/components/schemas/SessionTaskGroupTreeSummary"}
    CancelTaskGroupRequest:
      type: object
      properties:
        reason: {type: string}
    ReapOrphanSubagentsRequest:
      type: object
      properties:
        workspace_id: {type: string}
        limit: {type: integer, format: int32}
    ReapedSubagent:
      type: object
      required: [session, reason]
      properties:
        session: {$ref: "#/components/schemas/Session"}
        parent_session_id: {type: string}
        reason: {type: string}
    ReapOrphanSubagentsResult:
      type: object
      required: [reaped, count]
      properties:
        reaped: {type: array, items: {$ref: "#/components/schemas/ReapedSubagent"}}
        count: {type: integer, format: int32}
    AgentDeliberation:
      type: object
      required: [id, workspace_id, owner_id, parent_session_id, objective, strategy, status, phase, max_participants, max_rounds, moderator_agent_id, moderator_environment_id, plan, created_at, updated_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        parent_session_id: {type: string}
        parent_turn_id: {type: string}
        idempotency_key: {type: string}
        objective: {type: string}
        strategy: {type: string}
        status: {type: string}
        phase: {type: string}
        max_participants: {type: integer, format: int32}
        max_rounds: {type: integer, format: int32}
        max_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        max_seconds: {type: integer, format: int32}
        moderator_agent_id: {type: string}
        moderator_environment_id: {type: string}
        plan: {$ref: "#/components/schemas/DynamicJSONValue"}
        final_group_id: {type: string}
        final_result: {$ref: "#/components/schemas/DynamicJSONValue"}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
        canceled_at: {type: string, format: date-time, nullable: true}
        cancel_reason: {type: string}
    AgentDeliberationParticipant:
      type: object
      required: [deliberation_id, participant_index, role_id, role_title, goal, agent_id, environment_id, created_at]
      properties:
        deliberation_id: {type: string}
        participant_index: {type: integer, format: int32}
        role_id: {type: string}
        role_title: {type: string}
        goal: {type: string}
        agent_id: {type: string}
        environment_id: {type: string}
        created_at: {type: string, format: date-time}
    AgentDeliberationRound:
      type: object
      required: [deliberation_id, round_number, round_type, status, task_group_id, created_at]
      properties:
        deliberation_id: {type: string}
        round_number: {type: integer, format: int32}
        round_type: {type: string}
        status: {type: string}
        task_group_id: {type: string}
        moderator_group_id: {type: string}
        summary: {$ref: "#/components/schemas/DynamicJSONValue"}
        questions: {$ref: "#/components/schemas/DynamicJSONValue"}
        created_at: {type: string, format: date-time}
        completed_at: {type: string, format: date-time, nullable: true}
    AgentDeliberationContribution:
      type: object
      required: [deliberation_id, round_number, participant_index, task_group_id, item_index, status, created_at, updated_at]
      properties:
        deliberation_id: {type: string}
        round_number: {type: integer, format: int32}
        participant_index: {type: integer, format: int32}
        task_group_id: {type: string}
        item_index: {type: integer, format: int32}
        session_id: {type: string}
        status: {type: string}
        contribution_text: {type: string}
        contribution_json: {$ref: "#/components/schemas/DynamicJSONValue"}
        retry_count: {type: integer, format: int32}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
    AgentDeliberationRoundState:
      type: object
      required: [round, contributions]
      properties:
        round: {$ref: "#/components/schemas/AgentDeliberationRound"}
        contributions: {type: array, items: {$ref: "#/components/schemas/AgentDeliberationContribution"}}
    AgentDeliberationResponse:
      type: object
      required: [deliberation, participants, rounds]
      properties:
        deliberation: {$ref: "#/components/schemas/AgentDeliberation"}
        participants: {type: array, items: {$ref: "#/components/schemas/AgentDeliberationParticipant"}}
        rounds: {type: array, items: {$ref: "#/components/schemas/AgentDeliberationRoundState"}}
        completed: {type: boolean}
    AgentDeliberationList:
      type: object
      required: [deliberations]
      properties:
        deliberations: {type: array, items: {$ref: "#/components/schemas/AgentDeliberationResponse"}}
    CancelAgentDeliberationRequest:
      type: object
      properties:
        reason: {type: string}
    RetryAgentDeliberationParticipantRequest:
      type: object
      required: [round_number]
      properties:
        round_number: {type: integer, format: int32}
    BinaryContent:
      type: string
      format: binary
    ObjectRef:
      type: object
      required: [id, workspace_id, storage_provider, bucket, object_key, size_bytes, visibility, created_by, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        storage_provider: {type: string}
        bucket: {type: string}
        object_key: {type: string}
        object_version: {type: string}
        content_type: {type: string}
        size_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        etag: {type: string}
        visibility: {type: string}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
    CreateObjectRefRequest:
      type: object
      required: [bucket, object_key, size_bytes]
      properties:
        workspace_id: {type: string}
        storage_provider: {type: string}
        bucket: {type: string}
        object_key: {type: string}
        object_version: {type: string}
        content_type: {type: string}
        size_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        etag: {type: string}
        visibility: {type: string}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        created_by: {type: string}
    TurnTrace:
      type: object
      required: [session_id, turn_id, steps]
      properties:
        session_id: {type: string}
        turn_id: {type: string}
        trace_id: {type: string}
        status: {type: string}
        summary: {type: string}
        stats: {$ref: "#/components/schemas/TurnTraceStats"}
        turns: {type: array, items: {$ref: "#/components/schemas/TraceTurnInfo"}}
        graph: {$ref: "#/components/schemas/TraceGraph"}
        steps: {type: array, items: {$ref: "#/components/schemas/TraceStep"}}
        spans: {type: array, items: {$ref: "#/components/schemas/TraceSpan"}}
    TurnTraceStats:
      type: object
      required: [duration_ms, step_count, span_count, llm_requests, tool_calls, approval_waits, pending_approvals, errors, artifact_count]
      properties:
        start_time: {type: string, format: date-time}
        end_time: {type: string, format: date-time}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        step_count: {type: integer, format: int32}
        span_count: {type: integer, format: int32}
        llm_requests: {type: integer, format: int32}
        tool_calls: {type: integer, format: int32}
        approval_waits: {type: integer, format: int32}
        pending_approvals: {type: integer, format: int32}
        errors: {type: integer, format: int32}
        artifact_count: {type: integer, format: int32}
    TraceTurnInfo:
      type: object
      required: [turn_id, duration_ms, step_count, span_count, tool_calls, errors]
      properties:
        turn_id: {type: string}
        status: {type: string}
        summary: {type: string}
        started_at: {type: string, format: date-time}
        ended_at: {type: string, format: date-time}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        step_count: {type: integer, format: int32}
        span_count: {type: integer, format: int32}
        tool_calls: {type: integer, format: int32}
        errors: {type: integer, format: int32}
    TraceGraph:
      type: object
      properties:
        root_span_ids: {type: array, items: {type: string}}
        edges: {type: array, items: {$ref: "#/components/schemas/TraceSpanEdge"}}
        critical_span_ids: {type: array, items: {type: string}}
        critical_path_duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        max_depth: {type: integer, format: int32}
    TraceSpanEdge:
      type: object
      required: [parent_span_id, child_span_id]
      properties:
        parent_span_id: {type: string}
        child_span_id: {type: string}
    TraceArtifact:
      type: object
      properties:
        artifact_id: {type: string}
        object_ref_id: {type: string}
        name: {type: string}
        artifact_type: {type: string}
        download_path: {type: string}
    TraceStep:
      type: object
      required: [seq, type, created_at]
      properties:
        seq: {type: integer, format: int64, maximum: 9007199254740991}
        type: {type: string}
        created_at: {type: string, format: date-time}
        trace_id: {type: string}
        span_id: {type: string}
        parent_span_id: {type: string}
        span_name: {type: string}
        span_kind: {type: string}
        span_status: {type: string}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        message: {type: string}
        summary: {type: string}
        call_id: {type: string}
        identifier: {type: string}
        api_name: {type: string}
        outcome: {type: string}
        approval_source: {type: string}
        decision_reason: {type: string}
        artifact_error: {type: string}
        artifacts: {type: array, items: {$ref: "#/components/schemas/TraceArtifact"}}
        content_truncated: {type: boolean}
        state_truncated: {type: boolean}
        original_content_chars: {type: integer, format: int64, maximum: 9007199254740991}
        visible_content_chars: {type: integer, format: int64, maximum: 9007199254740991}
        original_state_bytes: {type: integer, format: int64, maximum: 9007199254740991}
    TraceSpanEvent:
      type: object
      required: [seq, type, name, time]
      properties:
        seq: {type: integer, format: int64, maximum: 9007199254740991}
        type: {type: string}
        name: {type: string}
        time: {type: string, format: date-time}
        message: {type: string}
        summary: {type: string}
        attributes: {type: object, additionalProperties: {type: string}}
    TraceSpan:
      type: object
      required: [trace_id, span_id, name, kind, start_time, end_time, duration_ms]
      properties:
        trace_id: {type: string}
        span_id: {type: string}
        parent_span_id: {type: string}
        child_span_ids: {type: array, items: {type: string}}
        name: {type: string}
        kind: {type: string}
        status: {type: string}
        start_seq: {type: integer, format: int64, maximum: 9007199254740991}
        end_seq: {type: integer, format: int64, maximum: 9007199254740991}
        depth: {type: integer, format: int32}
        start_offset_ms: {type: integer, format: int64, maximum: 9007199254740991}
        start_time: {type: string, format: date-time}
        end_time: {type: string, format: date-time}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        self_duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        critical: {type: boolean}
        attributes: {type: object, additionalProperties: {type: string}}
        events: {type: array, items: {$ref: "#/components/schemas/TraceSpanEvent"}}
    TraceCatalogEntry:
      type: object
      required: [trace_id, session_id, turn_id, duration_ms, step_count, span_count, tool_calls, errors]
      properties:
        trace_id: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        session_title: {type: string}
        session_status: {type: string}
        turn_status: {type: string}
        summary: {type: string}
        started_at: {type: string, format: date-time}
        ended_at: {type: string, format: date-time}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        step_count: {type: integer, format: int32}
        span_count: {type: integer, format: int32}
        tool_calls: {type: integer, format: int32}
        errors: {type: integer, format: int32}
    TraceCatalog:
      type: object
      required: [items, next_cursor, has_more]
      properties:
        items: {type: array, items: {$ref: "#/components/schemas/TraceCatalogEntry"}}
        next_cursor: {type: string}
        has_more: {type: boolean}
    TraceSpanCatalogEntry:
      type: object
      required: [trace_id, session_id, turn_id, span_id, name, kind, start_time, duration_ms, event_count]
      properties:
        trace_id: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        session_title: {type: string}
        span_id: {type: string}
        parent_span_id: {type: string}
        name: {type: string}
        kind: {type: string}
        status: {type: string}
        depth: {type: integer, format: int32}
        start_time: {type: string, format: date-time}
        start_offset_ms: {type: integer, format: int64, maximum: 9007199254740991}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        self_duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
        critical: {type: boolean}
        event_count: {type: integer, format: int32}
        attributes: {type: object, additionalProperties: {type: string}}
    TraceSpanCatalog:
      type: object
      required: [items, next_cursor, has_more]
      properties:
        items: {type: array, items: {$ref: "#/components/schemas/TraceSpanCatalogEntry"}}
        next_cursor: {type: string}
        has_more: {type: boolean}
    TraceSpanDetail:
      type: object
      required: [session_id, turn_id, trace_id, span]
      properties:
        session_id: {type: string}
        turn_id: {type: string}
        trace_id: {type: string}
        session_title: {type: string}
        span: {$ref: "#/components/schemas/TraceSpan"}
        trace_stats: {$ref: "#/components/schemas/TurnTraceStats"}
    TraceDocument:
      oneOf:
        - {$ref: "#/components/schemas/TurnTrace"}
        - type: object
          description: Perfetto or OTLP JSON selected by the format query parameter.
          additionalProperties: true
          x-tma-dynamic-json: true
    Worker:
      type: object
      required: [id, workspace_id, name, worker_type, status, registered_by, registered_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        name: {type: string}
        worker_type: {type: string}
        status: {type: string}
        capabilities: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        registered_by: {type: string}
        registered_at: {type: string, format: date-time}
        last_seen_at: {type: string, format: date-time, nullable: true}
        lease_expires_at: {type: string, format: date-time, nullable: true}
        archived_at: {type: string, format: date-time, nullable: true}
    WorkerList:
      type: object
      required: [workers]
      properties:
        workers: {type: array, items: {$ref: "#/components/schemas/Worker"}}
    ReapExpiredWorkersRequest:
      type: object
      properties:
        workspace_id: {type: string}
        limit: {type: integer, format: int32}
    ReapExpiredWorkersResult:
      type: object
      required: [count, expired]
      properties:
        count: {type: integer, format: int32}
        expired: {type: array, items: {$ref: "#/components/schemas/Worker"}}
    WorkInvocation:
      type: object
      required: [protocol_version, namespace, api]
      properties:
        protocol_version: {type: string}
        namespace: {type: string}
        api: {type: string}
        capabilities: {type: array, items: {type: string}}
        risk: {type: string}
        runtime: {type: string}
        input: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    WorkerDiagnoseRequest:
      type: object
      required: [namespace, api]
      properties:
        workspace_id: {type: string}
        protocol_version: {type: string}
        namespace: {type: string}
        api: {type: string}
        capabilities: {type: array, items: {type: string}}
        risk: {type: string}
        runtime: {type: string}
        input: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    WorkerDiagnosisResult:
      type: object
      required: [worker_id, workspace_id, name, worker_type, status, match]
      properties:
        worker_id: {type: string}
        workspace_id: {type: string}
        name: {type: string}
        worker_type: {type: string}
        status: {type: string}
        match: {type: boolean}
        reasons: {type: array, items: {type: string}}
        runtimes: {type: array, items: {type: string}}
        apis: {type: array, items: {type: string}}
        capabilities: {type: array, items: {type: string}}
        lease_expires_at: {type: string, format: date-time, nullable: true}
        last_seen_at: {type: string, format: date-time, nullable: true}
        registered_by: {type: string}
    WorkerDiagnoseResponse:
      type: object
      required: [invocation, matches, diagnostics]
      properties:
        invocation: {$ref: "#/components/schemas/WorkInvocation"}
        matches: {type: integer, format: int32}
        diagnostics: {type: array, items: {$ref: "#/components/schemas/WorkerDiagnosisResult"}}
    WorkerWork:
      type: object
      required: [id, workspace_id, work_type, status, created_at, updated_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        worker_id: {type: string}
        environment_id: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        work_type: {type: string}
        status: {type: string}
        payload: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        result: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        error_message: {type: string}
        lease_expires_at: {type: string, format: date-time, nullable: true}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
        started_at: {type: string, format: date-time, nullable: true}
        completed_at: {type: string, format: date-time, nullable: true}
    EnqueueWorkerWorkRequest:
      type: object
      properties:
        workspace_id: {type: string}
        worker_id: {type: string}
        environment_id: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        work_type: {type: string}
        payload: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    CancelWorkerWorkRequest:
      type: object
      properties:
        reason: {type: string}
    RequeueWorkerWorkRequest:
      type: object
      properties:
        worker_id: {type: string}
        clear_worker: {type: boolean}
    ReapExpiredWorkerWorkRequest:
      type: object
      properties:
        workspace_id: {type: string}
        limit: {type: integer, format: int32}
    ReapExpiredWorkerWorkResult:
      type: object
      required: [count, expired]
      properties:
        count: {type: integer, format: int32}
        expired: {type: array, items: {$ref: "#/components/schemas/WorkerWork"}}
    WorkerSummary:
      type: object
      required: [id, workspace_id, name, worker_type, status]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        name: {type: string}
        worker_type: {type: string}
        status: {type: string}
        lease_expires_at: {type: string, format: date-time, nullable: true}
        last_seen_at: {type: string, format: date-time, nullable: true}
    WorkerWorkDiagnosis:
      type: object
      required: [work]
      properties:
        work: {$ref: "#/components/schemas/WorkerWork"}
        worker: {$ref: "#/components/schemas/WorkerSummary"}
        reasons: {type: array, items: {type: string}}
        actions: {type: array, items: {type: string}}
    Skill:
      type: object
      required: [id, workspace_id, identifier, title, owner_type, owner_id, visibility, source_type, status, created_by, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        identifier: {type: string}
        title: {type: string}
        description: {type: string}
        owner_type: {type: string, enum: [user, builtin, workspace, plugin]}
        owner_id: {type: string}
        visibility: {type: string, enum: [private, workspace]}
        forked_from_skill_id: {type: string}
        forked_from_version: {type: integer, format: int32}
        source_plugin_id: {type: string}
        source_type: {type: string, enum: [inline, github, artifact, catalog, plugin, builtin]}
        source_locator: {type: string}
        source_path: {type: string}
        status: {type: string, enum: [active, archived]}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
        archived_at: {type: string, format: date-time, nullable: true}
    SkillList:
      type: object
      required: [skills]
      properties:
        skills: {type: array, items: {$ref: "#/components/schemas/Skill"}}
    CreateSkillRequest:
      type: object
      required: [identifier, title]
      properties:
        workspace_id: {type: string}
        identifier: {type: string}
        title: {type: string}
        description: {type: string}
        owner_type: {type: string, enum: [user, builtin, workspace, plugin]}
        owner_id: {type: string}
        visibility: {type: string, enum: [private, workspace]}
        source_plugin_id: {type: string}
        source_type: {type: string, enum: [inline, github, artifact, catalog, plugin, builtin]}
        source_locator: {type: string}
        source_path: {type: string}
    SkillManifestBlock:
      type: object
      required: [type]
      properties:
        type: {type: string}
        title: {type: string}
        content: {type: string}
        items: {type: array, items: {type: string}}
    SkillManifest:
      type: object
      properties:
        system_role: {type: string}
        blocks: {type: array, items: {$ref: "#/components/schemas/SkillManifestBlock"}}
        inputs_schema: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    SkillAssetSBOMComponent:
      type: object
      required: [path, kind, size, checksum_sha256]
      properties:
        path: {type: string}
        kind: {type: string}
        content_type: {type: string}
        size: {type: integer, format: int32}
        checksum_sha256: {type: string}
        revision: {type: string}
        source_url: {type: string}
        object_ref_id: {type: string}
    SkillAssetSBOM:
      type: object
      required: [format, package_digest_sha256, components]
      properties:
        format: {type: string}
        package_digest_sha256: {type: string}
        components: {type: array, items: {$ref: "#/components/schemas/SkillAssetSBOMComponent"}}
    SkillAssetFile:
      type: object
      required: [path, size]
      properties:
        path: {type: string}
        content: {type: string}
        content_base64: {type: string, format: byte}
        content_type: {type: string}
        checksum_sha256: {type: string}
        object_ref_id: {type: string}
        scan_status: {type: string}
        scan_provider: {type: string}
        scan_version: {type: string}
        size: {type: integer, format: int32}
        revision: {type: string}
        source_url: {type: string}
        executable: {type: boolean}
        binary: {type: boolean}
    SkillAssetBundle:
      type: object
      required: [files, total_bytes]
      properties:
        files: {type: array, maxItems: 32, items: {$ref: "#/components/schemas/SkillAssetFile"}}
        total_bytes: {type: integer, format: int32}
        warnings: {type: array, items: {type: string}}
        sbom: {$ref: "#/components/schemas/SkillAssetSBOM"}
    SkillAssets:
      oneOf:
        - {$ref: "#/components/schemas/SkillAssetBundle"}
        - type: array
          items: {$ref: "#/components/schemas/SkillAssetFile"}
    SkillPackageFile:
      type: object
      required: [path, role, content_type, size_bytes, checksum_sha256]
      properties:
        path: {type: string}
        role: {type: string}
        content_type: {type: string}
        size_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        object_ref_id: {type: string}
        object_key: {type: string}
        binary: {type: boolean}
        executable: {type: boolean}
        source_revision: {type: string}
        source_url: {type: string}
        scan_status: {type: string}
        scan_provider: {type: string}
        scan_version: {type: string}
    SkillPackageManifest:
      type: object
      required: [format, root, package_checksum_sha256, files]
      properties:
        format: {type: string}
        root: {type: string}
        package_checksum_sha256: {type: string}
        files: {type: array, items: {$ref: "#/components/schemas/SkillPackageFile"}}
    SkillVersion:
      type: object
      required: [id, skill_id, version, content_format, manifest, content_text, checksum_sha256, package_format, created_by, created_at]
      properties:
        id: {type: string}
        skill_id: {type: string}
        version: {type: integer, format: int32}
        content_format: {type: string}
        manifest: {$ref: "#/components/schemas/SkillManifest"}
        content_text: {type: string}
        assets: {$ref: "#/components/schemas/SkillAssets"}
        checksum_sha256: {type: string}
        source_ref: {type: string}
        source_revision: {type: string}
        source_url: {type: string}
        package_format: {type: string}
        package_root: {type: string}
        package_checksum_sha256: {type: string}
        package_object_ref_id: {type: string}
        skill_md_object_ref_id: {type: string}
        package_manifest: {$ref: "#/components/schemas/SkillPackageManifest"}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
    SkillVersionList:
      type: object
      required: [versions]
      properties:
        versions: {type: array, items: {$ref: "#/components/schemas/SkillVersion"}}
    CreateSkillVersionRequest:
      type: object
      properties:
        content_format: {type: string}
        manifest: {$ref: "#/components/schemas/SkillManifest"}
        content_text: {type: string}
        assets: {$ref: "#/components/schemas/SkillAssets"}
        source_ref: {type: string}
        source_revision: {type: string}
        source_url: {type: string}
    SkillDraft:
      type: object
      required: [skill_id, revision, content_format, manifest, content_text, assets, updated_by, updated_at]
      properties:
        skill_id: {type: string}
        revision: {type: integer, format: int64, maximum: 9007199254740991}
        content_format: {type: string}
        manifest: {$ref: "#/components/schemas/SkillManifest"}
        content_text: {type: string}
        assets: {$ref: "#/components/schemas/SkillAssets"}
        updated_by: {type: string}
        updated_at: {type: string, format: date-time}
    PutSkillDraftRequest:
      type: object
      properties:
        expected_revision: {type: integer, format: int64, maximum: 9007199254740991}
        content_format: {type: string}
        manifest: {$ref: "#/components/schemas/SkillManifest"}
        content_text: {type: string}
        assets: {$ref: "#/components/schemas/SkillAssets"}
    PublishSkillDraftRequest:
      type: object
      properties:
        expected_revision: {type: integer, format: int64, maximum: 9007199254740991}
    ForkSkillRequest:
      type: object
      required: [version, identifier, title]
      properties:
        version: {type: integer, format: int32, minimum: 1}
        identifier: {type: string}
        title: {type: string}
        description: {type: string}
    EnabledSkill:
      type: object
      required: [skill]
      properties:
        skill_id: {type: string}
        skill: {type: string}
        version: {type: integer, format: int32}
        mode: {type: string, enum: [full, summary, examples_only]}
        priority: {type: integer, format: int32}
        inputs: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    SkillConfig:
      type: object
      required: [enabled]
      properties:
        enabled: {type: array, items: {$ref: "#/components/schemas/EnabledSkill"}}
    SkillConfigInput:
      type: object
      properties:
        enabled: {type: array, items: {$ref: "#/components/schemas/EnabledSkill"}}
    ResolveSkillsPreviewRequest:
      type: object
      properties:
        workspace_id: {type: string}
        skills: {$ref: "#/components/schemas/SkillConfigInput"}
        max_tokens: {type: integer, format: int32}
    ResolvedSkill:
      type: object
      required: [skill, version, requested_mode, priority, estimated_tokens, status]
      properties:
        skill: {$ref: "#/components/schemas/Skill"}
        version: {$ref: "#/components/schemas/SkillVersion"}
        requested_mode: {type: string}
        rendered_mode: {type: string}
        priority: {type: integer, format: int32}
        inputs: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        rendered: {type: string}
        estimated_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        status: {type: string, enum: [resolved, degraded, skipped, failed]}
        failure_reason: {type: string}
    ResolveSkillsResult:
      type: object
      required: [config, estimated_tokens, truncated]
      properties:
        config: {$ref: "#/components/schemas/SkillConfig"}
        rendered: {$ref: "#/components/schemas/RenderedSkillsContext"}
        skills: {type: array, items: {$ref: "#/components/schemas/ResolvedSkill"}}
        legacy_passthrough: {type: boolean}
        estimated_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        truncated: {type: boolean}
    RenderedSkillsContext:
      type: object
      required: [format, content]
      properties:
        format: {type: string, enum: [tma.skills.context.v1]}
        content: {type: string}
    SkillUsage:
      type: object
      required: [workspace_id, session_id, turn_id, agent_id, agent_config_version, skill_id, skill_identifier, skill_version, requested_mode, priority, estimated_tokens, status]
      properties:
        workspace_id: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        agent_id: {type: string}
        agent_config_version: {type: integer, format: int32}
        skill_id: {type: string}
        skill_identifier: {type: string}
        skill_version: {type: integer, format: int32}
        requested_mode: {type: string}
        rendered_mode: {type: string}
        priority: {type: integer, format: int32}
        estimated_tokens: {type: integer, format: int64, maximum: 9007199254740991}
        status: {type: string, enum: [resolved, degraded, skipped, failed]}
        failure_reason: {type: string}
        created_at: {type: string, format: date-time}
    SkillUsageList:
      type: object
      required: [skill_usages]
      properties:
        skill_usages: {type: array, items: {$ref: "#/components/schemas/SkillUsage"}}
    SkillPackageBackfillRequest:
      type: object
      properties:
        workspace_id: {type: string}
        limit: {type: integer, format: int32}
    SkillPackageBackfillResult:
      type: object
      required: [scanned, migrated]
      properties:
        workspace_id: {type: string}
        scanned: {type: integer, format: int32}
        migrated: {type: integer, format: int32}
    SkillRetentionPolicyConfig:
      type: object
      required: [enabled, retention_days, delete_limit]
      properties:
        enabled: {type: boolean}
        retention_days: {type: integer, format: int32}
        delete_limit: {type: integer, format: int32}
    SkillRetentionPolicyConfigInput:
      type: object
      properties:
        enabled: {type: boolean}
        retention_days: {type: integer, format: int32}
        delete_limit: {type: integer, format: int32}
    SkillRetentionPolicy:
      type: object
      required: [id, scope_type, status, current_version, created_by, created_at]
      properties:
        id: {type: string}
        scope_type: {type: string, enum: [organization, workspace]}
        organization_id: {type: string}
        workspace_id: {type: string}
        status: {type: string, enum: [active, archived]}
        current_version: {type: integer, format: int32}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
        archived_at: {type: string, format: date-time, nullable: true}
    SkillRetentionPolicyVersion:
      type: object
      required: [id, policy_id, version, config, checksum_sha256, created_by, created_at]
      properties:
        id: {type: string}
        policy_id: {type: string}
        version: {type: integer, format: int32}
        config: {$ref: "#/components/schemas/SkillRetentionPolicyConfig"}
        checksum_sha256: {type: string}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
    SkillRetentionPolicyResult:
      type: object
      required: [policy, version]
      properties:
        policy: {$ref: "#/components/schemas/SkillRetentionPolicy"}
        version: {$ref: "#/components/schemas/SkillRetentionPolicyVersion"}
    SkillRetentionPolicyList:
      type: object
      required: [policies]
      properties:
        policies: {type: array, items: {$ref: "#/components/schemas/SkillRetentionPolicy"}}
    CreateSkillRetentionPolicyRequest:
      type: object
      required: [scope_type]
      properties:
        scope_type: {type: string, enum: [organization, workspace]}
        organization_id: {type: string}
        workspace_id: {type: string}
        config: {$ref: "#/components/schemas/SkillRetentionPolicyConfigInput"}
    PublishSkillRetentionPolicyRequest:
      type: object
      properties:
        config: {$ref: "#/components/schemas/SkillRetentionPolicyConfigInput"}
    EffectiveSkillRetentionPolicy:
      type: object
      required: [source, config, revision]
      properties:
        source: {type: string, enum: [server, organization, workspace]}
        policy: {$ref: "#/components/schemas/SkillRetentionPolicy"}
        version: {$ref: "#/components/schemas/SkillRetentionPolicyVersion"}
        config: {$ref: "#/components/schemas/SkillRetentionPolicyConfig"}
        revision: {type: string}
    SkillAssetGCRequest:
      type: object
      properties:
        workspace_id: {type: string}
        limit: {type: integer, format: int32}
        confirm: {type: string, enum: [DELETE]}
    SkillAssetCandidate:
      type: object
      required: [workspace_id, object_ref_id, storage_provider, bucket, object_key, size_bytes, reason, eligible_at, object_created_at]
      properties:
        workspace_id: {type: string}
        skill_id: {type: string}
        skill_identifier: {type: string}
        skill_version_id: {type: string}
        skill_version: {type: integer, format: int32}
        asset_path: {type: string}
        object_ref_id: {type: string}
        storage_provider: {type: string}
        bucket: {type: string}
        object_key: {type: string}
        object_version: {type: string}
        content_type: {type: string}
        size_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        scan_provider: {type: string}
        scan_version: {type: string}
        reason: {type: string, enum: [archived_retention_expired, orphaned_skill_asset]}
        eligible_at: {type: string, format: date-time}
        object_created_at: {type: string, format: date-time}
    SkillAssetGCPreview:
      type: object
      required: [workspace_id, effective_policy, cutoff, candidate_count, candidate_bytes, candidates]
      properties:
        workspace_id: {type: string}
        effective_policy: {$ref: "#/components/schemas/EffectiveSkillRetentionPolicy"}
        cutoff: {type: string, format: date-time}
        candidate_count: {type: integer, format: int32}
        candidate_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        candidates: {type: array, items: {$ref: "#/components/schemas/SkillAssetCandidate"}}
    SkillAssetGCRun:
      type: object
      required: [id, workspace_id, policy_source, policy_revision, retention_days, delete_limit, status, candidate_count, deleted_count, skipped_count, failed_count, bytes_deleted, requested_by, started_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        policy_source: {type: string, enum: [server, organization, workspace]}
        policy_id: {type: string}
        policy_version: {type: integer, format: int32}
        policy_revision: {type: string}
        retention_days: {type: integer, format: int32}
        delete_limit: {type: integer, format: int32}
        status: {type: string, enum: [running, succeeded, partial, failed]}
        candidate_count: {type: integer, format: int32}
        deleted_count: {type: integer, format: int32}
        skipped_count: {type: integer, format: int32}
        failed_count: {type: integer, format: int32}
        bytes_deleted: {type: integer, format: int64, maximum: 9007199254740991}
        requested_by: {type: string}
        started_at: {type: string, format: date-time}
        finished_at: {type: string, format: date-time, nullable: true}
    SkillAssetGCItem:
      type: object
      required: [id, run_id, candidate, status, reason, attempts, object_was_missing, created_at, updated_at]
      properties:
        id: {type: string}
        run_id: {type: string}
        candidate: {$ref: "#/components/schemas/SkillAssetCandidate"}
        status: {type: string, enum: [candidate, deleting, deleted, skipped, failed]}
        reason: {type: string}
        attempts: {type: integer, format: int32}
        object_was_missing: {type: boolean}
        error_message: {type: string}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
        deleted_at: {type: string, format: date-time, nullable: true}
    SkillAssetGCRunResult:
      type: object
      required: [run, items]
      properties:
        run: {$ref: "#/components/schemas/SkillAssetGCRun"}
        items: {type: array, items: {$ref: "#/components/schemas/SkillAssetGCItem"}}
    SkillAssetGCRunList:
      type: object
      required: [runs]
      properties:
        runs: {type: array, items: {$ref: "#/components/schemas/SkillAssetGCRun"}}
    SkillAssetGCTombstone:
      type: object
      required: [id, run_id, workspace_id, object_ref_id, storage_provider, bucket, object_key, size_bytes, reason, object_was_missing, deleted_by, deleted_at]
      properties:
        id: {type: string}
        run_id: {type: string}
        workspace_id: {type: string}
        skill_id: {type: string}
        skill_version_id: {type: string}
        asset_path: {type: string}
        object_ref_id: {type: string}
        storage_provider: {type: string}
        bucket: {type: string}
        object_key: {type: string}
        object_version: {type: string}
        content_type: {type: string}
        size_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        metadata: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        scan_provider: {type: string}
        scan_version: {type: string}
        reason: {type: string}
        object_was_missing: {type: boolean}
        deleted_by: {type: string}
        deleted_at: {type: string, format: date-time}
    SkillAssetGCTombstoneList:
      type: object
      required: [tombstones]
      properties:
        tombstones: {type: array, items: {$ref: "#/components/schemas/SkillAssetGCTombstone"}}
    MarketplaceSource:
      type: object
      required: [provider]
      properties:
        provider: {type: string, enum: [github, artifact, catalog]}
        repository: {type: string}
        ref: {type: string}
        path: {type: string}
        artifact_id: {type: string}
        catalog_entry_id: {type: string}
        catalog_skill_id: {type: string}
    MarketplaceCandidate:
      type: object
      required: [provider, repository, html_url, verified]
      properties:
        provider: {type: string}
        repository: {type: string}
        path: {type: string}
        ref: {type: string}
        html_url: {type: string}
        catalog_entry_id: {type: string}
        catalog_skill_id: {type: string}
        skill_version: {type: integer, format: int32}
        title: {type: string}
        category: {type: string}
        tags: {type: array, items: {type: string}}
        version_checksum_sha256: {type: string}
        description: {type: string}
        stars: {type: integer, format: int32}
        suggested_identifier: {type: string}
        verified: {type: boolean}
    MarketplaceDiscoverResult:
      type: object
      required: [provider, search_mode, items, count]
      properties:
        provider: {type: string}
        search_mode: {type: string}
        items: {type: array, items: {$ref: "#/components/schemas/MarketplaceCandidate"}}
        count: {type: integer, format: int32}
    MarketplacePreviewRequest:
      type: object
      required: [session_id, source]
      properties:
        session_id: {type: string}
        identifier: {type: string}
        source: {$ref: "#/components/schemas/MarketplaceSource"}
    MarketplaceInstallRequest:
      type: object
      required: [session_id, source]
      properties:
        session_id: {type: string}
        identifier: {type: string}
        source: {$ref: "#/components/schemas/MarketplaceSource"}
        policy_id: {type: string}
        policy_version: {type: integer, format: int32}
        policy_revision: {type: string}
        upgrade_existing: {type: boolean}
    MarketplaceAssetIndexFile:
      type: object
      required: [path, size]
      properties:
        path: {type: string}
        size: {type: integer, format: int64, maximum: 9007199254740991}
        revision: {type: string}
        source_url: {type: string}
        executable: {type: boolean}
        binary: {type: boolean}
        content_type: {type: string}
        checksum_sha256: {type: string}
        object_ref_id: {type: string}
        scan_status: {type: string}
        scan_provider: {type: string}
        scan_version: {type: string}
    MarketplaceAssetIndex:
      type: object
      required: [files, total_bytes]
      properties:
        files: {type: array, items: {$ref: "#/components/schemas/MarketplaceAssetIndexFile"}}
        total_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        warnings: {type: array, items: {type: string}}
        sbom: {$ref: "#/components/schemas/SkillAssetSBOM"}
    MarketplaceExistingSkill:
      type: object
      required: [skill_id, status, source_type]
      properties:
        skill_id: {type: string}
        version: {type: integer, format: int32}
        status: {type: string}
        source_type: {type: string}
        source_locator: {type: string}
        source_path: {type: string}
        source_ref: {type: string}
        source_revision: {type: string}
    MarketplacePreviewChanges:
      type: object
      required: [content_changed, added_files, removed_files, changed_files]
      properties:
        content_changed: {type: boolean}
        added_files: {type: array, items: {type: string}}
        removed_files: {type: array, items: {type: string}}
        changed_files: {type: array, items: {type: string}}
    MarketplacePolicyCheck:
      type: object
      required: [name, enforced, passed, message]
      properties:
        name: {type: string}
        enforced: {type: boolean}
        passed: {type: boolean}
        message: {type: string}
    MarketplacePolicyDecision:
      type: object
      required: [allowed, checks]
      properties:
        allowed: {type: boolean}
        policy_source: {type: string}
        policy_id: {type: string}
        policy_version: {type: integer, format: int32}
        policy_revision: {type: string}
        checks: {type: array, items: {$ref: "#/components/schemas/MarketplacePolicyCheck"}}
        violations: {type: array, items: {type: string}}
    MarketplaceAttestationResult:
      type: object
      required: [status, digest_sha256, message]
      properties:
        status: {type: string, enum: [missing, verified, invalid, untrusted]}
        path: {type: string}
        key_id: {type: string}
        algorithm: {type: string}
        digest_sha256: {type: string}
        message: {type: string}
    MarketplaceSecurityFinding:
      type: object
      required: [rule_id, severity, path, line, message]
      properties:
        rule_id: {type: string}
        severity: {type: string, enum: [medium, high, critical]}
        path: {type: string}
        line: {type: integer, format: int32}
        message: {type: string}
    MarketplaceExternalBinaryScanResult:
      type: object
      required: [provider, status, attempts, duration_ms]
      properties:
        provider: {type: string}
        status: {type: string}
        scanner: {type: string}
        scan_id: {type: string}
        signature: {type: string}
        message: {type: string}
        attempts: {type: integer, format: int32}
        duration_ms: {type: integer, format: int64, maximum: 9007199254740991}
    MarketplaceBinaryScanResult:
      type: object
      required: [path, status, scanner, content_type, size, checksum_sha256, findings]
      properties:
        path: {type: string}
        status: {type: string, enum: [passed, blocked]}
        scanner: {type: string}
        external_scan: {$ref: "#/components/schemas/MarketplaceExternalBinaryScanResult"}
        content_type: {type: string}
        size: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        findings: {type: array, items: {$ref: "#/components/schemas/MarketplaceSecurityFinding"}}
    MarketplaceSBOMComponent:
      type: object
      required: [path, kind, size, checksum_sha256]
      properties:
        path: {type: string}
        kind: {type: string}
        content_type: {type: string}
        size: {type: integer, format: int64, maximum: 9007199254740991}
        checksum_sha256: {type: string}
        revision: {type: string}
        source_url: {type: string}
    MarketplacePackageSBOM:
      type: object
      required: [format, package_digest_sha256, components]
      properties:
        format: {type: string}
        package_digest_sha256: {type: string}
        components: {type: array, items: {$ref: "#/components/schemas/MarketplaceSBOMComponent"}}
    MarketplaceSecurityReport:
      type: object
      required: [digest_sha256, attestation, findings, scanned_files, binary_files, sbom]
      properties:
        digest_sha256: {type: string}
        attestation: {$ref: "#/components/schemas/MarketplaceAttestationResult"}
        findings: {type: array, items: {$ref: "#/components/schemas/MarketplaceSecurityFinding"}}
        highest_severity: {type: string}
        scanned_files: {type: integer, format: int32}
        findings_limited: {type: boolean}
        binary_files: {type: array, items: {$ref: "#/components/schemas/MarketplaceBinaryScanResult"}}
        sbom: {$ref: "#/components/schemas/MarketplacePackageSBOM"}
    MarketplacePreviewResult:
      type: object
      required: [identifier, source, assets, policy, security, install_state, changes]
      properties:
        identifier: {type: string}
        title: {type: string}
        description: {type: string}
        license: {type: string}
        source: {$ref: "#/components/schemas/MarketplaceSource"}
        revision: {type: string}
        source_url: {type: string}
        content_bytes: {type: integer, format: int64, maximum: 9007199254740991}
        assets: {$ref: "#/components/schemas/MarketplaceAssetIndex"}
        policy: {$ref: "#/components/schemas/MarketplacePolicyDecision"}
        security: {$ref: "#/components/schemas/MarketplaceSecurityReport"}
        install_state: {type: string, enum: [new_install, upgrade, unchanged, blocked]}
        block_reason: {type: string}
        existing: {$ref: "#/components/schemas/MarketplaceExistingSkill"}
        changes: {$ref: "#/components/schemas/MarketplacePreviewChanges"}
    MarketplaceInstallResult:
      type: object
      required: [skill, version]
      properties:
        skill: {$ref: "#/components/schemas/Skill"}
        version: {$ref: "#/components/schemas/SkillVersion"}
        upgraded: {type: boolean}
        policy: {$ref: "#/components/schemas/MarketplacePolicyDecision"}
        security: {$ref: "#/components/schemas/MarketplaceSecurityReport"}
    MarketplaceEnableRequest:
      type: object
      required: [session_id]
      properties:
        session_id: {type: string}
        version: {type: integer, format: int32}
        mode: {type: string, enum: [full, summary, examples_only]}
        priority: {type: integer, format: int32}
        inputs: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
    MarketplaceDisableRequest:
      type: object
      required: [session_id]
      properties:
        session_id: {type: string}
    MarketplaceEnableResult:
      type: object
      required: [agent_id, previous_config_version, new_config_version, current_session_version, binding, changed, requires_session_upgrade]
      properties:
        agent_id: {type: string}
        previous_config_version: {type: integer, format: int32}
        new_config_version: {type: integer, format: int32}
        current_session_version: {type: integer, format: int32}
        binding: {$ref: "#/components/schemas/EnabledSkill"}
        changed: {type: boolean}
        requires_session_upgrade: {type: boolean}
    MarketplaceDisableResult:
      type: object
      required: [agent_id, previous_config_version, new_config_version, current_session_version, binding, removed, requires_session_upgrade]
      properties:
        agent_id: {type: string}
        previous_config_version: {type: integer, format: int32}
        new_config_version: {type: integer, format: int32}
        current_session_version: {type: integer, format: int32}
        binding: {$ref: "#/components/schemas/EnabledSkill"}
        removed: {type: boolean}
        requires_session_upgrade: {type: boolean}
    MarketplaceEntry:
      type: object
      required: [id, workspace_id, skill_id, skill_version, skill_identifier, skill_title, skill_status, version_checksum_sha256, package_format, tags, status, created_by, created_at, updated_by, updated_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        skill_id: {type: string}
        skill_version: {type: integer, format: int32}
        skill_identifier: {type: string}
        skill_title: {type: string}
        skill_description: {type: string}
        skill_status: {type: string}
        version_checksum_sha256: {type: string}
        package_format: {type: string}
        summary: {type: string}
        category: {type: string}
        tags: {type: array, items: {type: string}}
        status: {type: string, enum: [draft, pending_review, published, withdrawn]}
        submitted_by: {type: string}
        submitted_at: {type: string, format: date-time, nullable: true}
        published_by: {type: string}
        published_at: {type: string, format: date-time, nullable: true}
        withdrawn_by: {type: string}
        withdrawn_at: {type: string, format: date-time, nullable: true}
        review_note: {type: string}
        withdrawal_reason: {type: string}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
        updated_by: {type: string}
        updated_at: {type: string, format: date-time}
    MarketplaceEntryList:
      type: object
      required: [entries]
      properties:
        entries: {type: array, items: {$ref: "#/components/schemas/MarketplaceEntry"}}
    CreateMarketplaceEntryRequest:
      type: object
      required: [skill_id, skill_version]
      properties:
        workspace_id: {type: string}
        skill_id: {type: string}
        skill_version: {type: integer, format: int32}
        summary: {type: string, maxLength: 2000}
        category: {type: string, maxLength: 80}
        tags: {type: array, maxItems: 12, items: {type: string, maxLength: 40}}
    UpdateMarketplaceEntryRequest:
      type: object
      properties:
        workspace_id: {type: string}
        summary: {type: string, maxLength: 2000}
        category: {type: string, maxLength: 80}
        tags: {type: array, maxItems: 12, items: {type: string, maxLength: 40}}
    MarketplaceTransitionRequest:
      type: object
      properties:
        workspace_id: {type: string}
        note: {type: string}
    MarketplaceInternalCandidate:
      allOf:
        - {$ref: "#/components/schemas/MarketplaceEntry"}
        - type: object
          required: [provider, suggested_identifier, install_state]
          properties:
            provider: {type: string, enum: [catalog]}
            suggested_identifier: {type: string}
            install_state: {type: string, enum: [new_install, upgrade, unchanged, blocked]}
            existing: {$ref: "#/components/schemas/MarketplaceExistingSkill"}
    MarketplaceInternalResult:
      type: object
      required: [provider, items, count]
      properties:
        provider: {type: string, enum: [catalog]}
        items: {type: array, items: {$ref: "#/components/schemas/MarketplaceInternalCandidate"}}
        count: {type: integer, format: int32}
    MarketplacePolicyConfig:
      type: object
      properties:
        allowed_owners: {type: array, maxItems: 100, items: {type: string}}
        allowed_repositories: {type: array, maxItems: 100, items: {type: string}}
        require_commit_sha: {type: boolean}
        allowed_licenses: {type: array, maxItems: 100, items: {type: string}}
        denied_licenses: {type: array, maxItems: 100, items: {type: string}}
        require_license: {type: boolean}
        require_attestation: {type: boolean}
        trusted_attestation_keys: {type: object, additionalProperties: {type: string}}
        static_scan_block_severity: {type: string, enum: [medium, high, critical]}
    MarketplacePolicy:
      type: object
      required: [id, scope_type, status, current_version, created_by, created_at]
      properties:
        id: {type: string}
        scope_type: {type: string, enum: [organization, workspace]}
        organization_id: {type: string}
        workspace_id: {type: string}
        status: {type: string, enum: [active, archived]}
        current_version: {type: integer, format: int32}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
        archived_at: {type: string, format: date-time, nullable: true}
    MarketplacePolicyVersion:
      type: object
      required: [id, policy_id, version, config, checksum_sha256, created_by, created_at]
      properties:
        id: {type: string}
        policy_id: {type: string}
        version: {type: integer, format: int32}
        config: {$ref: "#/components/schemas/MarketplacePolicyConfig"}
        checksum_sha256: {type: string}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
    MarketplacePolicyResult:
      type: object
      required: [policy, version]
      properties:
        policy: {$ref: "#/components/schemas/MarketplacePolicy"}
        version: {$ref: "#/components/schemas/MarketplacePolicyVersion"}
    MarketplacePolicyList:
      type: object
      required: [policies]
      properties:
        policies: {type: array, items: {$ref: "#/components/schemas/MarketplacePolicy"}}
    CreateMarketplacePolicyRequest:
      type: object
      required: [scope_type, config]
      properties:
        scope_type: {type: string, enum: [organization, workspace]}
        organization_id: {type: string}
        workspace_id: {type: string}
        config: {$ref: "#/components/schemas/MarketplacePolicyConfig"}
    PublishMarketplacePolicyRequest:
      type: object
      required: [config]
      properties:
        config: {$ref: "#/components/schemas/MarketplacePolicyConfig"}
    Principal:
      type: object
      required: [subject, workspace_id, owner_id, roles, auth_type]
      properties:
        subject: {type: string}
        organization_id: {type: string}
        workspace_id: {type: string}
        owner_id: {type: string}
        roles:
          type: array
          items: {type: string, enum: [viewer, member, operator, admin]}
        auth_type: {type: string, enum: [disabled, jwt, oidc, gateway]}
    AuthState:
      type: object
      required: [authenticated]
      properties:
        authenticated: {type: boolean}
        principal: {$ref: "#/components/schemas/Principal"}
    AuthOIDCClientConfiguration:
      type: object
      required: [issuer, audience, client_id, scopes, device_authorization]
      properties:
        issuer: {type: string, format: uri}
        audience: {type: string}
        client_id: {type: string}
        scopes: {type: array, items: {type: string}}
        device_authorization: {type: boolean}
    AuthClientConfiguration:
      type: object
      required: [mode]
      properties:
        mode: {type: string, enum: [disabled, jwt, oidc, gateway]}
        oidc: {$ref: "#/components/schemas/AuthOIDCClientConfiguration"}
    EnvironmentVariable:
      type: object
      required: [name, configured, created_at, updated_at]
      properties:
        name: {type: string, pattern: "^[A-Za-z_][A-Za-z0-9_]*$", maxLength: 128}
        configured: {type: boolean}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
    EnvironmentVariableList:
      type: object
      required: [variables]
      properties:
        variables: {type: array, items: {$ref: "#/components/schemas/EnvironmentVariable"}}
    PutEnvironmentVariableRequest:
      type: object
      required: [value]
      properties:
        value: {type: string, writeOnly: true}
    MCPConfigValue:
      oneOf:
        - {type: string}
        - type: object
          minProperties: 1
          maxProperties: 1
          additionalProperties: false
          properties:
            value: {type: string, writeOnly: true}
            env_ref: {type: string}
            secret_ref: {type: string}
    MCPRoot:
      type: object
      required: [uri]
      properties:
        uri: {type: string}
        name: {type: string}
    MCPRuntimePolicy:
      type: object
      properties:
        timeout_seconds: {type: integer, format: int32}
        max_concurrency: {type: integer, format: int32}
        failure_threshold: {type: integer, format: int32}
        cooldown_seconds: {type: integer, format: int32}
    MCPRegistrySource:
      type: object
      required: [server_id, version]
      properties:
        server_id: {type: string}
        version: {type: integer, format: int32}
    MCPExposeConfig:
      type: object
      properties:
        resources: {type: boolean}
        prompts: {type: boolean}
    MCPOAuthConfig:
      type: object
      properties:
        grant_type: {type: string}
        token_url: {type: string, format: uri}
        client_id: {$ref: "#/components/schemas/MCPConfigValue"}
        client_secret: {$ref: "#/components/schemas/MCPConfigValue"}
        refresh_token: {$ref: "#/components/schemas/MCPConfigValue"}
        scopes: {type: array, items: {type: string}}
        audience: {type: string}
        resource: {type: string}
        token_endpoint_auth_method: {type: string, enum: [client_secret_post, client_secret_basic]}
    MCPServerConfig:
      type: object
      required: [identifier]
      additionalProperties: false
      properties:
        identifier: {type: string}
        command: {type: string}
        args: {type: array, items: {type: string}}
        env:
          type: object
          additionalProperties: {$ref: "#/components/schemas/MCPConfigValue"}
        cwd: {type: string}
        url: {type: string, format: uri}
        headers:
          type: object
          additionalProperties: {$ref: "#/components/schemas/MCPConfigValue"}
        oauth: {$ref: "#/components/schemas/MCPOAuthConfig"}
        listen: {type: boolean}
        roots: {type: array, items: {$ref: "#/components/schemas/MCPRoot"}}
        sampling:
          type: object
          properties: {enabled: {type: boolean}}
        elicitation:
          type: object
          properties: {enabled: {type: boolean}}
        logging:
          type: object
          properties: {level: {type: string}}
        runtime: {$ref: "#/components/schemas/MCPRuntimePolicy"}
        expose: {$ref: "#/components/schemas/MCPExposeConfig"}
        title: {type: string}
        description: {type: string}
        include_tools: {type: array, items: {type: string}}
        exclude_tools: {type: array, items: {type: string}}
        transport: {type: string, enum: [stdio, streamable_http]}
        stdio_framing: {type: string, enum: [json_lines, content_length]}
        disabled: {type: boolean}
        _registry: {$ref: "#/components/schemas/MCPRegistrySource"}
    MCPServer:
      type: object
      required: [id, workspace_id, identifier, name, status, current_version, config, usage_count, created_at, updated_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        identifier: {type: string}
        name: {type: string}
        description: {type: string}
        status: {type: string, enum: [active, disabled, archived]}
        current_version: {type: integer, format: int32}
        config: {$ref: "#/components/schemas/MCPServerConfig"}
        usage_count: {type: integer, format: int32}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
    MCPServerList:
      type: object
      required: [servers]
      properties:
        servers: {type: array, items: {$ref: "#/components/schemas/MCPServer"}}
    CreateMCPServerRequest:
      type: object
      required: [identifier, name, config]
      properties:
        workspace_id: {type: string}
        identifier: {type: string}
        name: {type: string}
        description: {type: string}
        config: {$ref: "#/components/schemas/MCPServerConfig"}
    UpdateMCPServerRequest:
      type: object
      properties:
        name: {type: string}
        description: {type: string}
        config: {$ref: "#/components/schemas/MCPServerConfig"}
    MCPServerVersion:
      type: object
      required: [id, server_id, version, config, checksum_sha256, created_at]
      properties:
        id: {type: string}
        server_id: {type: string}
        version: {type: integer, format: int32}
        config: {$ref: "#/components/schemas/MCPServerConfig"}
        checksum_sha256: {type: string, pattern: "^[0-9a-f]{64}$"}
        created_by: {type: string}
        created_at: {type: string, format: date-time}
    MCPServerVersionList:
      type: object
      required: [versions]
      properties:
        versions: {type: array, items: {$ref: "#/components/schemas/MCPServerVersion"}}
    MCPRestoreResult:
      type: object
      required: [server, source_version, previous_version, new_version]
      properties:
        server: {$ref: "#/components/schemas/MCPServer"}
        source_version: {type: integer, format: int32}
        previous_version: {type: integer, format: int32}
        new_version: {type: integer, format: int32}
    MCPRuntimeState:
      type: object
      required: [server_id, version, state, in_flight, max_concurrency, consecutive_failures, failure_threshold]
      properties:
        server_id: {type: string}
        version: {type: integer, format: int32}
        state: {type: string, enum: [closed, open, half_open]}
        in_flight: {type: integer, format: int32}
        max_concurrency: {type: integer, format: int32}
        consecutive_failures: {type: integer, format: int32}
        failure_threshold: {type: integer, format: int32}
        last_failure_class: {type: string}
        last_failure_at: {type: string, format: date-time, nullable: true}
        open_until: {type: string, format: date-time, nullable: true}
        cooldown_remaining_seconds: {type: integer, format: int64, maximum: 9007199254740991}
    MCPRuntimeStatus:
      type: object
      required: [checked_at, states]
      properties:
        checked_at: {type: string, format: date-time}
        states: {type: array, items: {$ref: "#/components/schemas/MCPRuntimeState"}}
    MCPHealthItem:
      type: object
      required: [identifier, kind, status]
      properties:
        identifier: {type: string}
        kind: {type: string}
        status: {type: string}
        detail: {type: string}
        latency_ms: {type: integer, format: int64, maximum: 9007199254740991}
        tool_count: {type: integer, format: int32}
        version: {type: integer, format: int32}
        server_name: {type: string}
        transport: {type: string, enum: [stdio, streamable_http]}
        estimated_tokens: {type: integer, format: int32}
        capabilities: {type: array, items: {type: string}}
        resource_count: {type: integer, format: int32}
        resource_template_count: {type: integer, format: int32}
        prompt_count: {type: integer, format: int32}
    MCPServerTestResult:
      type: object
      required: [server_id, version, result]
      properties:
        server_id: {type: string}
        version: {type: integer, format: int32}
        result: {$ref: "#/components/schemas/MCPHealthItem"}
    OperatorAuditRecord:
      type: object
      required: [id, principal_id, role, action, resource_type, outcome, created_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        session_id: {type: string}
        principal_id: {type: string}
        operator_label: {type: string}
        role: {type: string}
        action: {type: string}
        resource_type: {type: string}
        resource_id: {type: string}
        outcome: {type: string, enum: [succeeded, failed]}
        error_message: {type: string}
        details: {type: object, additionalProperties: true, x-tma-dynamic-json: true}
        created_at: {type: string, format: date-time}
    OperatorAuditList:
      type: object
      required: [audit_records]
      properties:
        audit_records: {type: array, items: {$ref: "#/components/schemas/OperatorAuditRecord"}}
    SecurityAuditReplayResult:
      type: object
      required: [replayed]
      properties:
        replayed: {type: integer, format: int32}
    ObservabilityRetryResult:
      type: object
      required: [attempted, succeeded, failed, skipped]
      properties:
        attempted: {type: integer, format: int32}
        succeeded: {type: integer, format: int32}
        failed: {type: integer, format: int32}
        skipped: {type: integer, format: int32}
    SecurityAuditIntegrityKeyState:
      type: object
      required: [key_id, configured, active, pending, delivering, delivered, dead_letter, blocking, safe_to_remove]
      properties:
        key_id: {type: string}
        configured: {type: boolean}
        active: {type: boolean}
        pending: {type: integer, format: int64, maximum: 9007199254740991}
        delivering: {type: integer, format: int64, maximum: 9007199254740991}
        delivered: {type: integer, format: int64, maximum: 9007199254740991}
        dead_letter: {type: integer, format: int64, maximum: 9007199254740991}
        blocking: {type: integer, format: int64, maximum: 9007199254740991}
        safe_to_remove: {type: boolean}
    SecurityAuditIntegrityKeyStatus:
      type: object
      required: [historical_unidentified_blocking, keys]
      properties:
        active_key_id: {type: string}
        historical_unidentified_blocking: {type: integer, format: int64, maximum: 9007199254740991}
        keys: {type: array, items: {$ref: "#/components/schemas/SecurityAuditIntegrityKeyState"}}
    ObservabilityExporterHealth:
      type: object
      required: [at]
      properties:
        at: {type: string, format: date-time}
        session_id: {type: string}
        turn_id: {type: string}
        trace_id: {type: string}
        message: {type: string}
    ObservabilityExporterStatus:
      type: object
      required: [enabled, configured]
      properties:
        enabled: {type: boolean}
        configured: {type: boolean}
        destination: {type: string}
        token_provided: {type: boolean}
        last_success: {$ref: "#/components/schemas/ObservabilityExporterHealth"}
        last_failure: {$ref: "#/components/schemas/ObservabilityExporterHealth"}
        last_attempt: {$ref: "#/components/schemas/ObservabilityExporterHealth"}
    SecurityAuditOutboxStats:
      type: object
      required: [pending, delivering, delivered, dead_letter, oldest_pending_seconds]
      properties:
        pending: {type: integer, format: int64, maximum: 9007199254740991}
        delivering: {type: integer, format: int64, maximum: 9007199254740991}
        delivered: {type: integer, format: int64, maximum: 9007199254740991}
        dead_letter: {type: integer, format: int64, maximum: 9007199254740991}
        oldest_pending_at: {type: string, format: date-time, nullable: true}
        oldest_pending_seconds: {type: integer, format: int64, maximum: 9007199254740991}
    ObservabilitySamplingStatus:
      type: object
      required: [enabled, sample_rate, configured]
      properties:
        enabled: {type: boolean}
        sample_rate: {type: number, format: double, minimum: 0, maximum: 1}
        configured: {type: boolean}
    ObservabilityRetryStatus:
      type: object
      required: [enabled, max_attempts, initial_delay_ms, max_delay_ms, pending_recent_retries]
      properties:
        enabled: {type: boolean}
        max_attempts: {type: integer, format: int32}
        initial_delay_ms: {type: integer, format: int64, maximum: 9007199254740991}
        max_delay_ms: {type: integer, format: int64, maximum: 9007199254740991}
        pending_recent_retries: {type: integer, format: int32}
    ObservabilityExporterRun:
      type: object
      required: [id, workspace_id, exporter, status, session_id, turn_id, attempt_count, started_at, finished_at]
      properties:
        id: {type: string}
        workspace_id: {type: string}
        exporter: {type: string}
        status: {type: string}
        session_id: {type: string}
        turn_id: {type: string}
        trace_id: {type: string}
        destination: {type: string}
        message: {type: string}
        attempt_count: {type: integer, format: int32}
        next_retry_at: {type: string, format: date-time, nullable: true}
        started_at: {type: string, format: date-time}
        finished_at: {type: string, format: date-time}
    ObservabilityStatus:
      type: object
      required: [perfetto, otlp, sampling, retry]
      properties:
        perfetto: {$ref: "#/components/schemas/ObservabilityExporterStatus"}
        otlp: {$ref: "#/components/schemas/ObservabilityExporterStatus"}
        security_audit_outbox: {$ref: "#/components/schemas/SecurityAuditOutboxStats"}
        security_audit_integrity_keys: {$ref: "#/components/schemas/SecurityAuditIntegrityKeyStatus"}
        sampling: {$ref: "#/components/schemas/ObservabilitySamplingStatus"}
        retry: {$ref: "#/components/schemas/ObservabilityRetryStatus"}
        recent_runs: {type: array, items: {$ref: "#/components/schemas/ObservabilityExporterRun"}}
`)
	if err := writeFileAtomically("api/v2/openapi.yaml", output.Bytes()); err != nil {
		panic(err)
	}
}

func writeTypedResponses(w *bytes.Buffer, contract routeContract) {
	statuses := contract.SuccessStatuses
	if len(statuses) == 0 {
		statuses = []string{"200"}
	}
	contentType := contract.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	fmt.Fprint(w, "      responses:\n")
	for _, status := range statuses {
		fmt.Fprintf(w, "        \"%s\":\n          description: Successful response\n", status)
		if contract.ResponseSchema != "" {
			fmt.Fprintf(w, "          content:\n            %s:\n              schema:\n                $ref: \"#/components/schemas/%s\"\n", contentType, contract.ResponseSchema)
		}
	}
	fmt.Fprint(w, `        default:
          description: API error
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ErrorEnvelope"
`)
}

func writeFileAtomically(path string, content []byte) error {
	temporary, err := os.CreateTemp("api/v2", "openapi.yaml.tmp.*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
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

func pathParameters(path string) []string {
	matches := parameterPattern.FindAllStringSubmatch(path, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match[1])
	}
	return result
}

func operationID(method string, path string) string {
	value := strings.Trim(path, "/")
	value = parameterPattern.ReplaceAllString(value, "by_$1")
	replacer := strings.NewReplacer("/", "_", "-", "_", ".", "_")
	return strings.ToLower(method) + "_" + replacer.Replace(value)
}
