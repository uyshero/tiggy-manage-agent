package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	mcppkg "tiggy-manage-agent/internal/mcp"
)

const (
	defaultMCPToolDescription = "MCP tool"
	defaultMCPServerTitle     = "MCP Server"

	mcpVirtualListResources         = "__tma_mcp_list_resources"
	mcpVirtualListResourceTemplates = "__tma_mcp_list_resource_templates"
	mcpVirtualReadResource          = "__tma_mcp_read_resource"
	mcpVirtualListPrompts           = "__tma_mcp_list_prompts"
	mcpVirtualGetPrompt             = "__tma_mcp_get_prompt"
)

type MCPRuntime struct {
	Config          mcppkg.ServerConfig
	ManifestData    Manifest
	OriginalByAlias map[string]string
	Capabilities    mcppkg.ServerCapabilities
	OAuthCache      *mcppkg.OAuthTokenCache
	client          mcpRuntimeClient
}

type MCPContextCatalog struct {
	ResourceCount         int
	ResourceTemplateCount int
	PromptCount           int
}

type mcpRuntimeClient interface {
	ListTools(context.Context) (mcppkg.InitializeResult, []mcppkg.ToolDefinition, error)
	ListResources(context.Context) (mcppkg.InitializeResult, []mcppkg.ResourceDefinition, error)
	ListResourceTemplates(context.Context) (mcppkg.InitializeResult, []mcppkg.ResourceTemplate, error)
	ReadResource(context.Context, string) (mcppkg.ResourceReadResult, error)
	ListPrompts(context.Context) (mcppkg.InitializeResult, []mcppkg.PromptDefinition, error)
	GetPrompt(context.Context, string, json.RawMessage) (mcppkg.PromptGetResult, error)
	Complete(context.Context, mcppkg.CompletionReference, mcppkg.CompletionArgument, mcppkg.CompletionContext) (mcppkg.CompletionResult, error)
	CallTool(context.Context, string, json.RawMessage) (mcppkg.ToolCallResult, error)
}

func RegistryWithMCP(ctx context.Context, registry Registry, raw json.RawMessage) (Registry, error) {
	return RegistryWithMCPLookup(ctx, registry, raw, nil)
}

func RegistryWithMCPLookup(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool)) (Registry, error) {
	return RegistryWithMCPLookupHost(ctx, registry, raw, lookup, nil, "")
}

func RegistryWithMCPLookupHost(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool), host *mcppkg.StdioHost, scope string) (Registry, error) {
	return RegistryWithMCPLookupHosts(ctx, registry, raw, lookup, host, nil, scope)
}

func RegistryWithMCPLookupHosts(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string) (Registry, error) {
	return RegistryWithMCPLookupHostsGuard(ctx, registry, raw, lookup, stdioHost, httpHost, scope, nil, "")
}

func RegistryWithMCPLookupHostsGuard(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string, guard *mcppkg.RuntimeGuard, workspaceID string) (Registry, error) {
	config, err := mcppkg.ParseConfig(raw)
	if err != nil {
		return registry, err
	}
	for _, server := range config.Servers {
		runtime, err := loadMCPRuntimeWithLookupHostsGuard(ctx, server, lookup, stdioHost, httpHost, mcpRuntimeHostScope(scope, server.Identifier), nil, guard, workspaceID)
		if err != nil {
			return registry, err
		}
		registry.Register(runtime)
	}
	return registry, nil
}

func RegistryWithMCPWarnings(ctx context.Context, registry Registry, raw json.RawMessage) Registry {
	return RegistryWithMCPWarningsLookup(ctx, registry, raw, nil)
}

func RegistryWithMCPWarningsLookup(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool)) Registry {
	return RegistryWithMCPWarningsLookupHost(ctx, registry, raw, lookup, nil, "")
}

func RegistryWithMCPWarningsLookupHost(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool), host *mcppkg.StdioHost, scope string) Registry {
	return RegistryWithMCPWarningsLookupHosts(ctx, registry, raw, lookup, host, nil, scope)
}

func RegistryWithMCPWarningsLookupHosts(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string) Registry {
	return RegistryWithMCPWarningsLookupHostsGuard(ctx, registry, raw, lookup, stdioHost, httpHost, scope, nil, "")
}

func RegistryWithMCPWarningsLookupHostsGuard(ctx context.Context, registry Registry, raw json.RawMessage, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string, guard *mcppkg.RuntimeGuard, workspaceID string) Registry {
	if len(raw) == 0 || string(raw) == "null" {
		return registry
	}
	augmented, err := RegistryWithMCPLookupHostsGuard(ctx, registry, raw, lookup, stdioHost, httpHost, scope, guard, workspaceID)
	if err != nil {
		slog.Default().Warn("mcp registry load failed", "error", err)
		return registry
	}
	return augmented
}

func LoadMCPRuntime(ctx context.Context, config mcppkg.ServerConfig) (MCPRuntime, error) {
	return LoadMCPRuntimeWithLookup(ctx, config, nil)
}

func LoadMCPRuntimeWithLookup(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool)) (MCPRuntime, error) {
	return LoadMCPRuntimeWithLookupHost(ctx, config, lookup, nil, "")
}

func LoadMCPRuntimeWithLookupEgressPolicy(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool), policy *mcppkg.EgressPolicy) (MCPRuntime, error) {
	return loadMCPRuntimeWithLookupHostsAndEgressPolicy(ctx, config, lookup, nil, nil, "", policy)
}

func LoadMCPRuntimeWithLookupHost(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool), host *mcppkg.StdioHost, scope string) (MCPRuntime, error) {
	return LoadMCPRuntimeWithLookupHosts(ctx, config, lookup, host, nil, scope)
}

func LoadMCPRuntimeWithLookupHosts(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string) (MCPRuntime, error) {
	return loadMCPRuntimeWithLookupHostsAndEgressPolicy(ctx, config, lookup, stdioHost, httpHost, scope, nil)
}

func loadMCPRuntimeWithLookupHostsAndEgressPolicy(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string, policy *mcppkg.EgressPolicy) (MCPRuntime, error) {
	return loadMCPRuntimeWithLookupHostsGuard(ctx, config, lookup, stdioHost, httpHost, scope, policy, nil, "")
}

func loadMCPRuntimeWithLookupHostsGuard(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool), stdioHost *mcppkg.StdioHost, httpHost *mcppkg.StreamableHTTPHost, scope string, policy *mcppkg.EgressPolicy, guard *mcppkg.RuntimeGuard, workspaceID string) (MCPRuntime, error) {
	if isReservedPluginNamespace(config.Identifier) {
		return MCPRuntime{}, fmt.Errorf("mcp server identifier %q is reserved for built-in tools", config.Identifier)
	}
	resolvedClient, err := mcpClientFromConfigLookupEgressPolicy(config, lookup, policy)
	if err != nil {
		return MCPRuntime{}, err
	}
	var client mcpRuntimeClient = resolvedClient
	switch mcpFirstNonEmptyString(config.Transport, mcppkg.TransportStdio) {
	case mcppkg.TransportStdio:
		if stdioHost != nil {
			client = stdioHost.Client(scope, resolvedClient)
		}
	case mcppkg.TransportStreamableHTTP:
		if httpHost != nil {
			client = httpHost.Client(scope, resolvedClient)
		}
	}
	if guard != nil {
		client = guard.WrapPartition(mcpRuntimeGuardPartition(workspaceID, config), config.Runtime, client)
	}
	initialized, toolsList, err := client.ListTools(ctx)
	if err != nil {
		methodNotFound := mcppkg.IsMethodNotFound(err)
		err = redactMCPClientError(err, resolvedClient)
		if !mcpContextExposureEnabled(config) || !methodNotFound {
			return MCPRuntime{}, fmt.Errorf("load mcp tools for %s: %w", config.Identifier, err)
		}
		initialized, err = initializeMCPContextOnlyRuntime(ctx, client, config)
		if err != nil {
			return MCPRuntime{}, redactMCPClientError(err, resolvedClient)
		}
	}

	manifest := Manifest{
		Identifier: config.Identifier,
		Type:       "mcp_server",
		Meta: Meta{
			Title:       mcpFirstNonEmptyString(config.Title, initialized.ServerInfo.Name, titleFromIdentifier(config.Identifier), defaultMCPServerTitle),
			Description: mcpFirstNonEmptyString(config.Description, initialized.ServerInfo.Name, "MCP server "+config.Identifier),
		},
		Metadata:   mcpManifestMetadata(config, initialized, len(toolsList)),
		SystemRole: "Use " + config.Identifier + ".* tools only when they are the best fit for the user's request.",
	}

	include := stringSet(config.IncludeTools)
	exclude := stringSet(config.ExcludeTools)
	usedAliases := map[string]bool{}
	originalByAlias := map[string]string{}
	for _, toolDef := range toolsList {
		originalName := strings.TrimSpace(toolDef.Name)
		if originalName == "" {
			continue
		}
		if len(include) > 0 && !include[originalName] {
			continue
		}
		if exclude[originalName] {
			continue
		}
		alias := uniqueAlias(usedAliases, mcppkg.NormalizeName(originalName, "tool"))
		usedAliases[alias] = true
		originalByAlias[alias] = originalName
		parameters := toolDef.InputSchema
		if len(parameters) == 0 || !json.Valid(parameters) {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		manifest.API = append(manifest.API, API{
			Name:           alias,
			APIName:        originalName,
			Description:    mcpFirstNonEmptyString(toolDef.Description, toolDef.Title, defaultMCPToolDescription+" "+originalName),
			Parameters:     parameters,
			Risk:           mcpToolRisk(toolDef.Annotations),
			Implementation: ToolImplementationServerBuiltin,
		})
	}
	addMCPContextAPIs(&manifest, usedAliases, originalByAlias, config)
	if len(manifest.API) == 0 {
		return MCPRuntime{}, fmt.Errorf("mcp server %s exposes no tools after filtering", config.Identifier)
	}

	return MCPRuntime{
		Config:          config,
		ManifestData:    manifest,
		OriginalByAlias: originalByAlias,
		Capabilities:    initialized.Capabilities,
		OAuthCache:      resolvedClient.OAuthCache,
		client:          client,
	}, nil
}

func mcpRuntimeGuardPartition(workspaceID string, config mcppkg.ServerConfig) mcppkg.RuntimePartition {
	partition := mcppkg.RuntimePartition{WorkspaceID: strings.TrimSpace(workspaceID), Identifier: config.Identifier}
	if config.Registry != nil {
		partition.ServerID = config.Registry.ServerID
		partition.Version = config.Registry.Version
	}
	return partition
}

func mcpRuntimeHostScope(scope string, identifier string) string {
	scope = strings.Trim(strings.TrimSpace(scope), "/")
	identifier = strings.Trim(strings.TrimSpace(identifier), "/")
	if scope == "" {
		return identifier
	}
	if identifier == "" {
		return scope
	}
	return scope + "/" + identifier
}

func mcpContextExposureEnabled(config mcppkg.ServerConfig) bool {
	return config.Expose.Resources || config.Expose.Prompts
}

func mcpManifestMetadata(config mcppkg.ServerConfig, initialized mcppkg.InitializeResult, toolCount int) map[string]any {
	runtimePolicy := config.Runtime.Effective()
	metadata := map[string]any{
		"mcp_transport":         mcpFirstNonEmptyString(config.Transport, mcppkg.TransportStdio),
		"mcp_protocol_version":  "2025-06-18",
		"mcp_tool_count":        toolCount,
		"mcp_timeout_seconds":   int(runtimePolicy.Timeout / time.Second),
		"mcp_max_concurrency":   runtimePolicy.MaxConcurrency,
		"mcp_failure_threshold": runtimePolicy.FailureThreshold,
		"mcp_cooldown_seconds":  int(runtimePolicy.Cooldown / time.Second),
	}
	if names := initialized.Capabilities.Names(); len(names) > 0 {
		metadata["mcp_capabilities"] = names
	}
	if config.Listen {
		metadata["mcp_listen"] = true
	}
	if config.OAuth != nil {
		metadata["mcp_oauth"] = true
	}
	if config.Expose.Resources {
		metadata["mcp_expose_resources"] = true
	}
	if config.Expose.Prompts {
		metadata["mcp_expose_prompts"] = true
	}
	if config.Logging != nil && config.Logging.Level != "" {
		metadata["mcp_logging_level"] = config.Logging.Level
	}
	return metadata
}

func initializeMCPContextOnlyRuntime(ctx context.Context, client mcpRuntimeClient, config mcppkg.ServerConfig) (mcppkg.InitializeResult, error) {
	if config.Expose.Resources {
		initialized, _, err := client.ListResources(ctx)
		if err != nil {
			return mcppkg.InitializeResult{}, fmt.Errorf("load mcp resources for %s: %w", config.Identifier, err)
		}
		return initialized, nil
	}
	if config.Expose.Prompts {
		initialized, _, err := client.ListPrompts(ctx)
		if err != nil {
			return mcppkg.InitializeResult{}, fmt.Errorf("load mcp prompts for %s: %w", config.Identifier, err)
		}
		return initialized, nil
	}
	return mcppkg.InitializeResult{}, fmt.Errorf("mcp server %s exposes no tools and no context APIs", config.Identifier)
}

func ProbeMCPContextCatalog(ctx context.Context, config mcppkg.ServerConfig) (MCPContextCatalog, error) {
	return ProbeMCPContextCatalogWithLookup(ctx, config, nil)
}

func ProbeMCPContextCatalogWithLookup(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool)) (MCPContextCatalog, error) {
	return ProbeMCPContextCatalogWithLookupEgressPolicy(ctx, config, lookup, nil)
}

func ProbeMCPContextCatalogWithLookupEgressPolicy(ctx context.Context, config mcppkg.ServerConfig, lookup func(string) (string, bool), policy *mcppkg.EgressPolicy) (MCPContextCatalog, error) {
	client, err := mcpClientFromConfigLookupEgressPolicy(config, lookup, policy)
	if err != nil {
		return MCPContextCatalog{}, err
	}
	var catalog MCPContextCatalog
	var probeErrors []error
	if _, resources, err := client.ListResources(ctx); err != nil {
		probeErrors = append(probeErrors, fmt.Errorf("resources/list: %w", redactMCPClientError(err, client)))
	} else {
		catalog.ResourceCount = len(resources)
	}
	if _, templates, err := client.ListResourceTemplates(ctx); err != nil {
		probeErrors = append(probeErrors, fmt.Errorf("resources/templates/list: %w", redactMCPClientError(err, client)))
	} else {
		catalog.ResourceTemplateCount = len(templates)
	}
	if _, prompts, err := client.ListPrompts(ctx); err != nil {
		probeErrors = append(probeErrors, fmt.Errorf("prompts/list: %w", redactMCPClientError(err, client)))
	} else {
		catalog.PromptCount = len(prompts)
	}
	return catalog, errors.Join(probeErrors...)
}

func (r MCPRuntime) Manifest() Manifest {
	return r.ManifestData
}

func (r MCPRuntime) Execute(ctx context.Context, call Call, _ ExecutionContext) (ExecutionResult, error) {
	call = NormalizeCall(call)
	toolName := strings.TrimSpace(r.OriginalByAlias[call.APIName])
	if toolName == "" {
		if originalName, ok := r.lookupOriginalName(call.APIName); ok {
			toolName = originalName
		} else {
			return ExecutionResult{}, fmt.Errorf("unsupported mcp api %q", call.APIName)
		}
	}
	client, err := r.runtimeClient()
	if err != nil {
		return ExecutionResult{}, err
	}
	if isMCPVirtualTool(toolName) {
		result, executeErr := r.executeVirtualTool(ctx, client, toolName, call)
		if failure, ok := mcpRuntimeFailureResult(r.Config.Identifier, call, executeErr); ok {
			return failure, nil
		}
		return result, executeErr
	}
	result, err := client.CallTool(ctx, toolName, call.Arguments)
	if err != nil {
		if failure, ok := mcpRuntimeFailureResult(r.Config.Identifier, call, err); ok {
			return failure, nil
		}
		return ExecutionResult{}, fmt.Errorf("mcp tool %s.%s failed: %w", r.Config.Identifier, toolName, err)
	}
	content := summarizeMCPContent(result.Content)
	state, stateErr := encodeMCPResultState(toolName, result)
	if stateErr != nil {
		return ExecutionResult{}, stateErr
	}
	executionResult := ExecutionResult{
		ID:         call.ID,
		Identifier: r.Config.Identifier,
		APIName:    call.APIName,
		Content:    content,
		State:      state,
	}
	if result.IsError {
		executionResult.Error = &ExecutionError{
			Type:    "mcp_tool_error",
			Message: mcpFirstNonEmptyString(content, "MCP tool returned isError=true"),
		}
	}
	return executionResult, nil
}

func mcpRuntimeFailureResult(identifier string, call Call, err error) (ExecutionResult, bool) {
	class := mcppkg.RuntimeErrorClass(err)
	if class == "" {
		return ExecutionResult{}, false
	}
	messages := map[string]string{
		"timeout":          "MCP call timed out.",
		"canceled":         "MCP call was canceled.",
		"authentication":   "MCP authentication failed.",
		"rate_limited":     "MCP server rate limit was reached.",
		"transport":        "MCP transport failed.",
		"protocol":         "MCP protocol validation failed.",
		"unavailable":      "MCP server is unavailable.",
		"circuit_open":     "MCP server circuit is temporarily open.",
		"concurrency_wait": "MCP concurrency wait exceeded the call deadline.",
		"unknown":          "MCP call failed.",
	}
	message := messages[class]
	if message == "" {
		message = messages["unknown"]
	}
	return ExecutionResult{
		ID: call.ID, Identifier: identifier, APIName: call.APIName, Content: message,
		Error: &ExecutionError{Type: "mcp_" + class, Message: message},
	}, true
}

func (r MCPRuntime) runtimeClient() (mcpRuntimeClient, error) {
	if r.client != nil {
		return r.client, nil
	}
	client, err := mcpClientFromConfig(r.Config)
	if err != nil {
		return nil, err
	}
	client.OAuthCache = r.OAuthCache
	return client, nil
}

func addMCPContextAPIs(manifest *Manifest, usedAliases map[string]bool, originalByAlias map[string]string, config mcppkg.ServerConfig) {
	if config.Expose.Resources {
		addMCPVirtualAPI(manifest, usedAliases, originalByAlias, "mcp_list_resources", mcpVirtualListResources, "List MCP resource metadata exposed by this server.", json.RawMessage(`{"type":"object","properties":{}}`))
		addMCPVirtualAPI(manifest, usedAliases, originalByAlias, "mcp_list_resource_templates", mcpVirtualListResourceTemplates, "List MCP resource URI templates exposed by this server.", json.RawMessage(`{"type":"object","properties":{}}`))
		addMCPVirtualAPI(manifest, usedAliases, originalByAlias, "mcp_read_resource", mcpVirtualReadResource, "Read a specific MCP resource by URI.", json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string","description":"The MCP resource URI to read."}},"required":["uri"]}`))
	}
	if config.Expose.Prompts {
		addMCPVirtualAPI(manifest, usedAliases, originalByAlias, "mcp_list_prompts", mcpVirtualListPrompts, "List MCP prompt metadata exposed by this server.", json.RawMessage(`{"type":"object","properties":{}}`))
		addMCPVirtualAPI(manifest, usedAliases, originalByAlias, "mcp_get_prompt", mcpVirtualGetPrompt, "Get a specific MCP prompt by name and optional arguments.", json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"The MCP prompt name to get."},"arguments":{"type":"object","description":"Optional prompt arguments."}},"required":["name"]}`))
	}
}

func addMCPVirtualAPI(manifest *Manifest, usedAliases map[string]bool, originalByAlias map[string]string, baseAlias string, virtualName string, description string, parameters json.RawMessage) {
	alias := uniqueAlias(usedAliases, baseAlias)
	usedAliases[alias] = true
	originalByAlias[alias] = virtualName
	manifest.API = append(manifest.API, API{
		Name:           alias,
		APIName:        virtualName,
		Description:    description,
		Parameters:     parameters,
		Risk:           ToolRiskRead,
		Implementation: ToolImplementationServerBuiltin,
	})
}

func isMCPVirtualTool(toolName string) bool {
	switch toolName {
	case mcpVirtualListResources, mcpVirtualListResourceTemplates, mcpVirtualReadResource, mcpVirtualListPrompts, mcpVirtualGetPrompt:
		return true
	default:
		return false
	}
}

func (r MCPRuntime) executeVirtualTool(ctx context.Context, client mcpRuntimeClient, toolName string, call Call) (ExecutionResult, error) {
	switch toolName {
	case mcpVirtualListResources:
		_, resources, err := client.ListResources(ctx)
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("mcp resources/list for %s failed: %w", r.Config.Identifier, err)
		}
		return mcpVirtualExecutionResult(call, toolName, summarizeMCPResources(resources), map[string]any{"resources": resources})
	case mcpVirtualListResourceTemplates:
		_, templates, err := client.ListResourceTemplates(ctx)
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("mcp resources/templates/list for %s failed: %w", r.Config.Identifier, err)
		}
		return mcpVirtualExecutionResult(call, toolName, summarizeMCPResourceTemplates(templates), map[string]any{"resource_templates": templates})
	case mcpVirtualReadResource:
		uri := stringArgument(call.Arguments, "uri")
		result, err := client.ReadResource(ctx, uri)
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("mcp resources/read for %s failed: %w", r.Config.Identifier, err)
		}
		return mcpVirtualExecutionResult(call, toolName, summarizeMCPResourceContents(result.Contents), map[string]any{"uri": uri, "contents": result.Contents})
	case mcpVirtualListPrompts:
		_, prompts, err := client.ListPrompts(ctx)
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("mcp prompts/list for %s failed: %w", r.Config.Identifier, err)
		}
		return mcpVirtualExecutionResult(call, toolName, summarizeMCPPrompts(prompts), map[string]any{"prompts": prompts})
	case mcpVirtualGetPrompt:
		name := stringArgument(call.Arguments, "name")
		arguments := rawObjectArgument(call.Arguments, "arguments")
		result, err := client.GetPrompt(ctx, name, arguments)
		if err != nil {
			return ExecutionResult{}, fmt.Errorf("mcp prompts/get for %s failed: %w", r.Config.Identifier, err)
		}
		return mcpVirtualExecutionResult(call, toolName, summarizeMCPPromptMessages(result.Messages), map[string]any{"name": name, "prompt": result})
	default:
		return ExecutionResult{}, fmt.Errorf("unsupported mcp virtual api %q", toolName)
	}
}

func mcpVirtualExecutionResult(call Call, toolName string, content string, payload map[string]any) (ExecutionResult, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["protocol_version"] = "tma.mcp_context_result.v1"
	payload["tool_name"] = toolName
	state, err := json.Marshal(payload)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("encode mcp context result state: %w", err)
	}
	return ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Content:    content,
		State:      json.RawMessage(state),
	}, nil
}

func summarizeMCPResources(resources []mcppkg.ResourceDefinition) string {
	if len(resources) == 0 {
		return "No MCP resources are available."
	}
	lines := make([]string, 0, len(resources)+1)
	lines = append(lines, "MCP resources:")
	for _, resource := range resources {
		label := mcpFirstNonEmptyString(resource.Title, resource.Name, resource.URI)
		lines = append(lines, fmt.Sprintf("- %s (%s)", label, resource.URI))
	}
	return strings.Join(lines, "\n")
}

func summarizeMCPResourceTemplates(templates []mcppkg.ResourceTemplate) string {
	if len(templates) == 0 {
		return "No MCP resource templates are available."
	}
	lines := make([]string, 0, len(templates)+1)
	lines = append(lines, fmt.Sprintf("MCP resource templates: %d", len(templates)))
	for _, template := range templates {
		name := strings.TrimSpace(template.Title)
		if name == "" {
			name = strings.TrimSpace(template.Name)
		}
		line := strings.TrimSpace(template.URITemplate)
		if name != "" {
			line = name + ": " + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func summarizeMCPResourceContents(contents []mcppkg.ResourceContent) string {
	if len(contents) == 0 {
		return "MCP resource is empty."
	}
	parts := make([]string, 0, len(contents))
	for _, content := range contents {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, content.Text)
			continue
		}
		if strings.TrimSpace(content.Blob) != "" {
			label := mcpFirstNonEmptyString(content.MimeType, "binary")
			parts = append(parts, fmt.Sprintf("MCP resource returned %s blob (%d base64 chars).", label, len(content.Blob)))
		}
	}
	if len(parts) == 0 {
		return "MCP resource returned non-text content."
	}
	return strings.Join(parts, "\n\n")
}

func summarizeMCPPrompts(prompts []mcppkg.PromptDefinition) string {
	if len(prompts) == 0 {
		return "No MCP prompts are available."
	}
	lines := make([]string, 0, len(prompts)+1)
	lines = append(lines, "MCP prompts:")
	for _, prompt := range prompts {
		label := mcpFirstNonEmptyString(prompt.Title, prompt.Name)
		if len(prompt.Arguments) > 0 {
			lines = append(lines, fmt.Sprintf("- %s (%s, %d argument(s))", label, prompt.Name, len(prompt.Arguments)))
		} else {
			lines = append(lines, fmt.Sprintf("- %s (%s)", label, prompt.Name))
		}
	}
	return strings.Join(lines, "\n")
}

func summarizeMCPPromptMessages(messages []mcppkg.PromptMessage) string {
	if len(messages) == 0 {
		return "MCP prompt returned no messages."
	}
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		content := message.Content
		text := strings.TrimSpace(content.Text)
		if text == "" {
			text = fmt.Sprintf("[%s content]", mcpFirstNonEmptyString(content.Type, "non-text"))
		}
		role := mcpFirstNonEmptyString(message.Role, "message")
		lines = append(lines, role+": "+text)
	}
	return strings.Join(lines, "\n\n")
}

func stringArgument(raw json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	value, _ := object[key].(string)
	return strings.TrimSpace(value)
}

func rawObjectArgument(raw json.RawMessage, key string) json.RawMessage {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return json.RawMessage(`{}`)
	}
	value := object[key]
	if len(value) == 0 || strings.TrimSpace(string(value)) == "null" {
		return json.RawMessage(`{}`)
	}
	return value
}

func mcpClientFromConfig(config mcppkg.ServerConfig) (mcppkg.Client, error) {
	return mcpClientFromConfigLookup(config, nil)
}

func mcpClientFromConfigLookup(config mcppkg.ServerConfig, lookup func(string) (string, bool)) (mcppkg.Client, error) {
	return mcpClientFromConfigLookupEgressPolicy(config, lookup, nil)
}

func mcpClientFromConfigLookupEgressPolicy(config mcppkg.ServerConfig, lookup func(string) (string, bool), policy *mcppkg.EgressPolicy) (mcppkg.Client, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	env, err := mcppkg.ResolveEnvWithLookup(config, lookup)
	if err != nil {
		return mcppkg.Client{}, err
	}
	headers, err := mcppkg.ResolveHeadersWithLookup(config, lookup)
	if err != nil {
		return mcppkg.Client{}, err
	}
	oauth, err := mcppkg.ResolveOAuthClientCredentialsWithLookup(config, lookup)
	if err != nil {
		return mcppkg.Client{}, err
	}
	var oauthCache *mcppkg.OAuthTokenCache
	if oauth != nil {
		oauthCache = mcppkg.NewOAuthTokenCache()
	}
	return mcppkg.Client{
		Transport:    config.Transport,
		StdioFraming: config.StdioFraming,
		Command:      config.Command,
		Args:         append([]string(nil), config.Args...),
		Env:          env,
		Cwd:          config.Cwd,
		URL:          config.URL,
		Headers:      headers,
		OAuth:        oauth,
		OAuthCache:   oauthCache,
		Listen:       config.Listen,
		Roots:        append([]mcppkg.Root(nil), config.Roots...),
		Sampling:     config.Sampling,
		Elicitation:  config.Elicitation,
		LoggingLevel: mcpLoggingLevel(config.Logging),
		EgressPolicy: policy,
	}, nil
}

func mcpLoggingLevel(config *mcppkg.LoggingConfig) string {
	if config == nil {
		return ""
	}
	return config.Level
}

func redactMCPClientError(err error, client mcppkg.Client) error {
	if err == nil {
		return nil
	}
	secrets := make(map[string]string, len(client.Env)+len(client.Headers)+2)
	for key, value := range client.Env {
		secrets["mcp_env_"+key] = value
	}
	for key, value := range client.Headers {
		secrets["mcp_header_"+key] = value
	}
	if client.OAuth != nil {
		secrets["mcp_oauth_client_id"] = client.OAuth.ClientID
		secrets["mcp_oauth_client_secret"] = client.OAuth.ClientSecret
	}
	return errors.New(redactEnvironmentText(err.Error(), secrets))
}

func (r MCPRuntime) lookupOriginalName(apiName string) (string, bool) {
	for _, api := range r.ManifestData.API {
		if api.Name == apiName || api.APIName == apiName {
			return api.APIName, true
		}
	}
	return "", false
}

func mcpToolRisk(annotations mcppkg.ToolAnnotations) string {
	if annotations.ReadOnlyHint && !annotations.DestructiveHint {
		return ToolRiskRead
	}
	return ToolRiskWrite
}

func summarizeMCPContent(content []mcppkg.ContentItem) string {
	textParts := make([]string, 0, len(content))
	var otherCount int
	for _, item := range content {
		switch strings.TrimSpace(strings.ToLower(item.Type)) {
		case "text":
			if strings.TrimSpace(item.Text) != "" {
				textParts = append(textParts, item.Text)
			}
		default:
			otherCount++
		}
	}
	if len(textParts) > 0 {
		return strings.Join(textParts, "\n\n")
	}
	if otherCount > 0 {
		return fmt.Sprintf("MCP tool returned %d non-text content item(s).", otherCount)
	}
	return "MCP tool completed."
}

func encodeMCPResultState(toolName string, result mcppkg.ToolCallResult) (json.RawMessage, error) {
	content := make([]map[string]any, 0, len(result.Content))
	for _, item := range result.Content {
		entry := map[string]any{"type": item.Type}
		switch strings.TrimSpace(strings.ToLower(item.Type)) {
		case "text":
			entry["text"] = item.Text
		case "image":
			entry["mime_type"] = item.MimeType
			entry["data_bytes"] = len(item.Data)
		case "resource":
			entry["resource"] = rawJSONValue(item.Resource)
		default:
			if item.Text != "" {
				entry["text"] = item.Text
			}
			if item.MimeType != "" {
				entry["mime_type"] = item.MimeType
			}
		}
		content = append(content, entry)
	}
	payload := map[string]any{
		"protocol_version": "tma.mcp_result.v1",
		"tool_name":        toolName,
		"is_error":         result.IsError,
		"content":          content,
	}
	if len(result.StructuredContent) > 0 {
		payload["structured_content"] = rawJSONValue(result.StructuredContent)
	}
	if len(result.Meta) > 0 {
		payload["meta"] = result.Meta
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode mcp result state: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func uniqueAlias(used map[string]bool, base string) string {
	if base == "" {
		base = "tool"
	}
	if !used[base] {
		return base
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s_%d", base, index)
		if !used[candidate] {
			return candidate
		}
	}
}

func titleFromIdentifier(identifier string) string {
	parts := strings.Fields(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(identifier))
	if len(parts) == 0 {
		return defaultMCPServerTitle
	}
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func mcpFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
