package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/internal/envvars"
	mcppkg "tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

const toolingHealthTimeout = 8 * time.Second

type toolingHealthRequest struct {
	Kind       string `json:"kind,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

type toolingHealthItem struct {
	Identifier            string   `json:"identifier"`
	Kind                  string   `json:"kind"`
	Status                string   `json:"status"`
	Detail                string   `json:"detail,omitempty"`
	LatencyMS             int64    `json:"latency_ms,omitempty"`
	ToolCount             int      `json:"tool_count,omitempty"`
	Version               int      `json:"version,omitempty"`
	ServerName            string   `json:"server_name,omitempty"`
	Transport             string   `json:"transport,omitempty"`
	TokenEstimate         int      `json:"estimated_tokens,omitempty"`
	Capabilities          []string `json:"capabilities,omitempty"`
	ResourceCount         int      `json:"resource_count,omitempty"`
	ResourceTemplateCount int      `json:"resource_template_count,omitempty"`
	PromptCount           int      `json:"prompt_count,omitempty"`
}

type toolingHealthResponse struct {
	AgentID         string                          `json:"agent_id"`
	CheckedAt       time.Time                       `json:"checked_at"`
	MCP             []toolingHealthItem             `json:"mcp"`
	Skills          []toolingHealthItem             `json:"skills"`
	MCPHost         *mcppkg.StdioHostStats          `json:"mcp_host,omitempty"`
	MCPHTTPHost     *mcppkg.StreamableHTTPHostStats `json:"mcp_http_host,omitempty"`
	MCPRuntimeGuard *mcppkg.RuntimeGuardStats       `json:"mcp_runtime_guard,omitempty"`
}

func (s *Server) checkAgentToolingHealth(w http.ResponseWriter, r *http.Request) {
	agent, err := s.getAgentForRequest(r, r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	request := toolingHealthRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	request.Kind = strings.TrimSpace(strings.ToLower(request.Kind))
	request.Identifier = strings.TrimSpace(request.Identifier)
	response := toolingHealthResponse{AgentID: agent.ID, CheckedAt: time.Now().UTC(), MCP: []toolingHealthItem{}, Skills: []toolingHealthItem{}}
	if request.Kind == "" || request.Kind == "mcp" {
		mcpConfig := agent.ConfigVersion.MCP
		var resolveErr error
		if registry, ok := s.store.(mcpregistry.Store); ok {
			_, mcpConfig, resolveErr = mcpregistry.PinAndResolve(r.Context(), registry, agent.WorkspaceID, mcpConfig)
		}
		managedEnvironment := map[string]string{}
		if resolveErr == nil {
			managedEnvironment, _, resolveErr = envvars.ResolveWorkspace(r.Context(), s.store, agent.WorkspaceID)
		}
		if resolveErr != nil {
			response.MCP = []toolingHealthItem{{Kind: "mcp", Identifier: request.Identifier, Status: "configuration_error", Detail: safeHealthError(resolveErr)}}
		} else {
			lookup := func(key string) (string, bool) {
				if value, ok := managedEnvironment[key]; ok {
					return value, true
				}
				return os.LookupEnv(key)
			}
			response.MCP = checkMCPHealthWithLookupEgressPolicy(r.Context(), mcpConfig, request.Identifier, lookup, s.mcpHTTPEgressPolicy())
		}
	}
	if request.Kind == "" || request.Kind == "skill" || request.Kind == "skills" {
		response.Skills = s.checkSkillsHealth(r.Context(), agent.WorkspaceID, agent.ConfigVersion.Skills, request.Identifier)
	}
	if stats, ok := s.mcpHostStats(); ok {
		response.MCPHost = &stats
	}
	if stats, ok := s.mcpHTTPHostStats(); ok {
		response.MCPHTTPHost = &stats
	}
	if stats, ok := s.mcpRuntimeGuardStats(); ok {
		response.MCPRuntimeGuard = &stats
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) mcpHostStats() (mcppkg.StdioHostStats, bool) {
	provider, ok := s.runner.(runner.MCPHostStatsProvider)
	if !ok {
		return mcppkg.StdioHostStats{}, false
	}
	return provider.MCPHostStats(), true
}

func (s *Server) mcpHTTPHostStats() (mcppkg.StreamableHTTPHostStats, bool) {
	provider, ok := s.runner.(runner.MCPHTTPHostStatsProvider)
	if !ok {
		return mcppkg.StreamableHTTPHostStats{}, false
	}
	return provider.MCPHTTPHostStats(), true
}

func (s *Server) mcpHTTPEgressPolicy() *mcppkg.EgressPolicy {
	provider, ok := s.runner.(runner.MCPHTTPEgressPolicyProvider)
	if !ok {
		return nil
	}
	return provider.MCPHTTPEgressPolicy()
}

func (s *Server) mcpRuntimeGuardStats() (mcppkg.RuntimeGuardStats, bool) {
	provider, ok := s.runner.(runner.MCPRuntimeGuardStatsProvider)
	if !ok {
		return mcppkg.RuntimeGuardStats{}, false
	}
	return provider.MCPRuntimeGuardStats(), true
}

func checkMCPHealth(ctx context.Context, raw json.RawMessage, identifier string) []toolingHealthItem {
	return checkMCPHealthWithLookup(ctx, raw, identifier, nil)
}

func checkMCPHealthWithLookup(ctx context.Context, raw json.RawMessage, identifier string, lookup func(string) (string, bool)) []toolingHealthItem {
	return checkMCPHealthWithLookupEgressPolicy(ctx, raw, identifier, lookup, nil)
}

func checkMCPHealthWithLookupEgressPolicy(ctx context.Context, raw json.RawMessage, identifier string, lookup func(string) (string, bool), policy *mcppkg.EgressPolicy) []toolingHealthItem {
	config, err := mcppkg.ParseConfig(raw)
	if err != nil {
		return []toolingHealthItem{{Kind: "mcp", Identifier: identifier, Status: "configuration_error", Detail: safeHealthError(err)}}
	}
	items := make([]toolingHealthItem, 0, len(config.Servers))
	for _, server := range config.Servers {
		if identifier != "" && server.Identifier != identifier {
			continue
		}
		started := time.Now()
		checkCtx, cancel := context.WithTimeout(ctx, toolingHealthTimeout)
		runtime, loadErr := tools.LoadMCPRuntimeWithLookupEgressPolicy(checkCtx, server, lookup, policy)
		item := toolingHealthItem{
			Identifier: server.Identifier,
			Kind:       "mcp",
			LatencyMS:  time.Since(started).Milliseconds(),
			Status:     "online",
			Transport:  fallbackString(server.Transport, mcppkg.TransportStdio),
		}
		if loadErr != nil {
			item.Status = toolingHealthErrorStatus(loadErr)
			item.Detail = safeHealthError(loadErr)
		} else {
			item.ServerName = runtime.ManifestData.Meta.Title
			item.ToolCount = len(runtime.ManifestData.API)
			item.Capabilities = runtime.Capabilities.Names()
			item.Detail = "连接成功，可用工具已加载。"
			catalog, probeErr := tools.ProbeMCPContextCatalogWithLookupEgressPolicy(checkCtx, runtime.Config, lookup, policy)
			item.ResourceCount = catalog.ResourceCount
			item.ResourceTemplateCount = catalog.ResourceTemplateCount
			item.PromptCount = catalog.PromptCount
			if probeErr != nil {
				item.Detail += " resources/templates/prompts 探测失败：" + safeHealthError(probeErr)
			}
		}
		cancel()
		items = append(items, item)
	}
	return items
}

func (s *Server) checkSkillsHealth(ctx context.Context, workspaceID string, raw json.RawMessage, identifier string) []toolingHealthItem {
	config, err := skills.ValidateConfig(raw)
	if err != nil {
		return []toolingHealthItem{{Kind: "skill", Identifier: identifier, Status: "configuration_error", Detail: safeHealthError(err)}}
	}
	registry, err := s.skillRegistry()
	if err != nil {
		return []toolingHealthItem{{Kind: "skill", Identifier: identifier, Status: "offline", Detail: "技能注册表不可用。"}}
	}
	items := make([]toolingHealthItem, 0, len(config.Enabled))
	for _, binding := range config.Enabled {
		if identifier != "" && binding.Skill != identifier {
			continue
		}
		bindingRaw, _ := json.Marshal(skills.Config{Enabled: []skills.EnabledSkill{binding}})
		started := time.Now()
		resolved, resolveErr := skills.ResolveRegistry(ctx, registry, workspaceID, bindingRaw, 0)
		item := toolingHealthItem{Identifier: binding.Skill, Kind: "skill", LatencyMS: time.Since(started).Milliseconds(), Status: "online"}
		if resolveErr != nil {
			item.Status = toolingHealthErrorStatus(resolveErr)
			item.Detail = safeHealthError(resolveErr)
		} else if len(resolved.Skills) == 1 {
			item.Version = resolved.Skills[0].Version.Version
			item.TokenEstimate = resolved.Skills[0].EstimatedTokens
			item.Detail = "版本与内容解析正常。"
		}
		items = append(items, item)
	}
	return items
}

func toolingHealthErrorStatus(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(text, "deadline exceeded"), strings.Contains(text, "connection refused"), strings.Contains(text, "no such file or directory"), strings.Contains(text, "executable file not found"):
		return "offline"
	case errors.Is(err, os.ErrPermission), strings.Contains(text, "permission denied"), strings.Contains(text, "environment variable"):
		return "permission_required"
	default:
		return "configuration_error"
	}
}

func safeHealthError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > 220 {
		text = text[:217] + "..."
	}
	return text
}
