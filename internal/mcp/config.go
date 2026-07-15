package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	TransportStdio            = "stdio"
	TransportStreamableHTTP   = "streamable_http"
	StdioFramingJSONLines     = "json_lines"
	StdioFramingContentLength = "content_length"
)

type Config struct {
	Servers  []ServerConfig  `json:"servers,omitempty"`
	Bindings []ServerBinding `json:"bindings,omitempty"`
}

type ServerBinding struct {
	ServerID   string `json:"server_id"`
	Version    int    `json:"version"`
	Identifier string `json:"identifier,omitempty"`
}

type ServerConfig struct {
	Identifier   string              `json:"identifier"`
	Command      string              `json:"command,omitempty"`
	Args         []string            `json:"args,omitempty"`
	Env          map[string]EnvValue `json:"env,omitempty"`
	Cwd          string              `json:"cwd,omitempty"`
	URL          string              `json:"url,omitempty"`
	Headers      map[string]EnvValue `json:"headers,omitempty"`
	OAuth        *OAuthConfig        `json:"oauth,omitempty"`
	Listen       bool                `json:"listen,omitempty"`
	Roots        []Root              `json:"roots,omitempty"`
	Sampling     *SamplingConfig     `json:"sampling,omitempty"`
	Elicitation  *ElicitationConfig  `json:"elicitation,omitempty"`
	Logging      *LoggingConfig      `json:"logging,omitempty"`
	Runtime      *RuntimePolicy      `json:"runtime,omitempty"`
	Expose       ExposeConfig        `json:"expose,omitempty"`
	Title        string              `json:"title,omitempty"`
	Description  string              `json:"description,omitempty"`
	IncludeTools []string            `json:"include_tools,omitempty"`
	ExcludeTools []string            `json:"exclude_tools,omitempty"`
	Transport    string              `json:"transport,omitempty"`
	StdioFraming string              `json:"stdio_framing,omitempty"`
	Disabled     bool                `json:"disabled,omitempty"`
	Registry     *RegistrySource     `json:"_registry,omitempty"`
}

type serverJSON struct {
	Identifier   string              `json:"identifier,omitempty"`
	ID           string              `json:"id,omitempty"`
	Name         string              `json:"name,omitempty"`
	Command      string              `json:"command,omitempty"`
	Args         []string            `json:"args,omitempty"`
	Env          map[string]EnvValue `json:"env,omitempty"`
	Cwd          string              `json:"cwd,omitempty"`
	URL          string              `json:"url,omitempty"`
	Headers      map[string]EnvValue `json:"headers,omitempty"`
	OAuth        *OAuthConfig        `json:"oauth,omitempty"`
	Listen       bool                `json:"listen,omitempty"`
	Roots        []rootJSON          `json:"roots,omitempty"`
	Sampling     *SamplingConfig     `json:"sampling,omitempty"`
	Elicitation  *ElicitationConfig  `json:"elicitation,omitempty"`
	Logging      *LoggingConfig      `json:"logging,omitempty"`
	Runtime      *RuntimePolicy      `json:"runtime,omitempty"`
	Expose       ExposeConfig        `json:"expose,omitempty"`
	Title        string              `json:"title,omitempty"`
	Description  string              `json:"description,omitempty"`
	IncludeTools []string            `json:"include_tools,omitempty"`
	ExcludeTools []string            `json:"exclude_tools,omitempty"`
	Transport    string              `json:"transport,omitempty"`
	StdioFraming string              `json:"stdio_framing,omitempty"`
	Disabled     bool                `json:"disabled,omitempty"`
	Registry     *RegistrySource     `json:"_registry,omitempty"`
}

type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type SamplingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

type ElicitationConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

type LoggingConfig struct {
	Level string `json:"level,omitempty"`
}

type RuntimePolicy struct {
	TimeoutSeconds   int `json:"timeout_seconds,omitempty"`
	MaxConcurrency   int `json:"max_concurrency,omitempty"`
	FailureThreshold int `json:"failure_threshold,omitempty"`
	CooldownSeconds  int `json:"cooldown_seconds,omitempty"`
}

type RegistrySource struct {
	ServerID string `json:"server_id"`
	Version  int    `json:"version"`
}

type ExposeConfig struct {
	Resources bool `json:"resources,omitempty"`
	Prompts   bool `json:"prompts,omitempty"`
}

type OAuthConfig struct {
	GrantType               string    `json:"grant_type,omitempty"`
	TokenURL                string    `json:"token_url,omitempty"`
	ClientID                *EnvValue `json:"client_id,omitempty"`
	ClientSecret            *EnvValue `json:"client_secret,omitempty"`
	RefreshToken            *EnvValue `json:"refresh_token,omitempty"`
	Scopes                  []string  `json:"scopes,omitempty"`
	Audience                string    `json:"audience,omitempty"`
	Resource                string    `json:"resource,omitempty"`
	TokenEndpointAuthMethod string    `json:"token_endpoint_auth_method,omitempty"`
}

type OAuthClientCredentials struct {
	TokenURL                string
	GrantType               string
	ClientID                string
	ClientSecret            string
	RefreshToken            string
	Scopes                  []string
	Audience                string
	Resource                string
	TokenEndpointAuthMethod string
}

type rootJSON struct {
	URI  string `json:"uri,omitempty"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

type EnvValue struct {
	Literal   bool   `json:"-"`
	Value     string `json:"-"`
	EnvRef    string `json:"-"`
	SecretRef string `json:"-"`
}

func LiteralEnv(value string) EnvValue {
	return EnvValue{Literal: true, Value: value}
}

func EnvRef(name string) EnvValue {
	return EnvValue{EnvRef: strings.TrimSpace(name)}
}

func SecretRef(ref string) EnvValue {
	return EnvValue{SecretRef: strings.TrimSpace(ref)}
}

func (v EnvValue) MarshalJSON() ([]byte, error) {
	switch {
	case v.Literal:
		return json.Marshal(v.Value)
	case strings.TrimSpace(v.EnvRef) != "":
		return json.Marshal(map[string]string{"env_ref": strings.TrimSpace(v.EnvRef)})
	case strings.TrimSpace(v.SecretRef) != "":
		return json.Marshal(map[string]string{"secret_ref": strings.TrimSpace(v.SecretRef)})
	default:
		return json.Marshal("")
	}
}

func (v *EnvValue) UnmarshalJSON(raw []byte) error {
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		*v = LiteralEnv(literal)
		return nil
	}

	var object struct {
		Value     *string `json:"value"`
		EnvRef    string  `json:"env_ref"`
		SecretRef string  `json:"secret_ref"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("mcp env value must be a string or object: %w", err)
	}
	envValue := EnvValue{
		EnvRef:    strings.TrimSpace(object.EnvRef),
		SecretRef: strings.TrimSpace(object.SecretRef),
	}
	if object.Value != nil {
		envValue.Literal = true
		envValue.Value = *object.Value
	}
	if err := validateEnvValue(envValue); err != nil {
		return err
	}
	*v = envValue
	return nil
}

func ParseConfig(raw json.RawMessage) (Config, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Config{}, nil
	}

	var envelope struct {
		Servers    json.RawMessage `json:"servers"`
		MCPServers json.RawMessage `json:"mcpServers"`
		Bindings   json.RawMessage `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		hasEnvelopeFields := len(envelope.Servers) > 0 || len(envelope.MCPServers) > 0 || len(envelope.Bindings) > 0
		bindings, bindingErr := parseBindings(envelope.Bindings)
		if bindingErr != nil {
			return Config{}, bindingErr
		}
		var servers []ServerConfig
		switch {
		case len(envelope.Servers) > 0 && string(envelope.Servers) != "null":
			var err error
			servers, _, err = parseServerCollection(envelope.Servers)
			if err != nil {
				return Config{}, err
			}
		case len(envelope.MCPServers) > 0 && string(envelope.MCPServers) != "null":
			var err error
			servers, _, err = parseServerCollection(envelope.MCPServers)
			if err != nil {
				return Config{}, err
			}
		}
		if hasEnvelopeFields {
			return Config{Servers: servers, Bindings: bindings}, nil
		}
	}

	if servers, ok, err := parseServerCollection(raw); ok || err != nil {
		return Config{Servers: servers}, err
	}
	return Config{}, fmt.Errorf("mcp config must be an array or an object with servers or mcpServers")
}

func CanonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	config, err := ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	if len(config.Servers) == 0 && len(config.Bindings) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode mcp config: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func parseBindings(raw json.RawMessage) ([]ServerBinding, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var bindings []ServerBinding
	if err := json.Unmarshal(raw, &bindings); err != nil {
		return nil, fmt.Errorf("mcp bindings must be an array: %w", err)
	}
	seen := map[string]bool{}
	for index := range bindings {
		binding := &bindings[index]
		binding.ServerID = strings.TrimSpace(binding.ServerID)
		if binding.ServerID == "" {
			return nil, fmt.Errorf("mcp binding server_id is required")
		}
		if binding.Version < 0 {
			return nil, fmt.Errorf("mcp binding %q version must be positive", binding.ServerID)
		}
		if binding.Identifier != "" {
			binding.Identifier = NormalizeName(binding.Identifier, "mcp")
		}
		key := binding.ServerID + "/" + binding.Identifier
		if seen[key] {
			return nil, fmt.Errorf("duplicate mcp binding %q", binding.ServerID)
		}
		seen[key] = true
	}
	return bindings, nil
}

func parseServerCollection(raw json.RawMessage) ([]ServerConfig, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var array []serverJSON
		if err := json.Unmarshal(raw, &array); err != nil {
			return nil, true, err
		}
		servers, normalizeErr := normalizeServers(array, nil)
		return servers, true, normalizeErr
	}

	if strings.HasPrefix(trimmed, "{") {
		var mapping map[string]serverJSON
		if err := json.Unmarshal(raw, &mapping); err != nil {
			return nil, true, err
		}
		keys := make([]string, 0, len(mapping))
		for key := range mapping {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make([]serverJSON, 0, len(keys))
		defaults := make([]string, 0, len(keys))
		for _, key := range keys {
			ordered = append(ordered, mapping[key])
			defaults = append(defaults, key)
		}
		servers, normalizeErr := normalizeServers(ordered, defaults)
		return servers, true, normalizeErr
	}

	return nil, false, nil
}

func normalizeServers(raw []serverJSON, defaultIdentifiers []string) ([]ServerConfig, error) {
	servers := make([]ServerConfig, 0, len(raw))
	seen := map[string]bool{}
	for index, server := range raw {
		if server.Disabled {
			continue
		}
		defaultIdentifier := ""
		if index < len(defaultIdentifiers) {
			defaultIdentifier = defaultIdentifiers[index]
		}
		identifier := NormalizeName(firstNonEmpty(server.Identifier, server.ID, server.Name, defaultIdentifier), "mcp")
		if seen[identifier] {
			return nil, fmt.Errorf("duplicate mcp server identifier %q", identifier)
		}
		seen[identifier] = true

		transport := strings.TrimSpace(strings.ToLower(server.Transport))
		if transport == "" {
			transport = TransportStdio
		}
		if transport != TransportStdio && transport != TransportStreamableHTTP {
			return nil, fmt.Errorf("mcp server %q transport %q is not supported", identifier, transport)
		}
		command := strings.TrimSpace(server.Command)
		serverURL := strings.TrimSpace(server.URL)
		stdioFraming := strings.TrimSpace(strings.ToLower(server.StdioFraming))
		switch transport {
		case TransportStdio:
			if command == "" {
				return nil, fmt.Errorf("mcp server %q command is required", identifier)
			}
			if stdioFraming == "" {
				stdioFraming = StdioFramingContentLength
			}
			if stdioFraming != StdioFramingJSONLines && stdioFraming != StdioFramingContentLength {
				return nil, fmt.Errorf("mcp server %q stdio_framing %q is not supported", identifier, stdioFraming)
			}
		case TransportStreamableHTTP:
			if stdioFraming != "" {
				return nil, fmt.Errorf("mcp server %q stdio_framing is only supported for stdio", identifier)
			}
			if serverURL == "" {
				return nil, fmt.Errorf("mcp server %q url is required", identifier)
			}
			if err := validateHTTPURL(serverURL); err != nil {
				return nil, fmt.Errorf("mcp server %q url: %w", identifier, err)
			}
		}

		env, err := cleanEnv(server.Env)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", identifier, err)
		}
		headers, err := cleanReferenceMap("header", server.Headers)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", identifier, err)
		}
		oauth, err := cleanOAuth(server.OAuth)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", identifier, err)
		}
		if oauth != nil {
			if transport != TransportStreamableHTTP {
				return nil, fmt.Errorf("mcp server %q oauth is only supported for streamable_http", identifier)
			}
			if hasAuthorizationHeader(headers) {
				return nil, fmt.Errorf("mcp server %q cannot set both oauth and Authorization header", identifier)
			}
		}
		roots, err := cleanRoots(server.Roots)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", identifier, err)
		}
		logging, err := cleanLogging(server.Logging)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", identifier, err)
		}
		runtimePolicy, err := cleanRuntimePolicy(server.Runtime)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", identifier, err)
		}
		normalized := ServerConfig{
			Identifier:   identifier,
			Command:      command,
			Args:         cleanStrings(server.Args),
			Env:          env,
			Cwd:          strings.TrimSpace(server.Cwd),
			URL:          serverURL,
			Headers:      headers,
			OAuth:        oauth,
			Listen:       server.Listen,
			Roots:        roots,
			Sampling:     cleanSampling(server.Sampling),
			Elicitation:  cleanElicitation(server.Elicitation),
			Logging:      logging,
			Runtime:      runtimePolicy,
			Expose:       server.Expose,
			Title:        strings.TrimSpace(server.Title),
			Description:  strings.TrimSpace(server.Description),
			IncludeTools: cleanStrings(server.IncludeTools),
			ExcludeTools: cleanStrings(server.ExcludeTools),
			Transport:    transport,
			StdioFraming: stdioFraming,
			Registry:     server.Registry,
		}
		servers = append(servers, normalized)
	}
	return servers, nil
}

func cleanRuntimePolicy(value *RuntimePolicy) (*RuntimePolicy, error) {
	if value == nil {
		return nil, nil
	}
	checks := []struct {
		name  string
		value int
		max   int
	}{
		{name: "runtime.timeout_seconds", value: value.TimeoutSeconds, max: 600},
		{name: "runtime.max_concurrency", value: value.MaxConcurrency, max: 64},
		{name: "runtime.failure_threshold", value: value.FailureThreshold, max: 100},
		{name: "runtime.cooldown_seconds", value: value.CooldownSeconds, max: 3600},
	}
	for _, check := range checks {
		if check.value < 0 || check.value > check.max {
			return nil, fmt.Errorf("%s must be between 1 and %d when configured", check.name, check.max)
		}
	}
	if value.TimeoutSeconds == 0 && value.MaxConcurrency == 0 && value.FailureThreshold == 0 && value.CooldownSeconds == 0 {
		return nil, nil
	}
	copy := *value
	return &copy, nil
}

func cleanLogging(value *LoggingConfig) (*LoggingConfig, error) {
	if value == nil {
		return nil, nil
	}
	rawLevel := strings.TrimSpace(value.Level)
	if rawLevel == "" {
		return nil, nil
	}
	level := NormalizeLoggingLevel(rawLevel)
	if level == "" {
		return nil, fmt.Errorf("logging level %q is not supported", rawLevel)
	}
	return &LoggingConfig{Level: level}, nil
}

func NormalizeLoggingLevel(value string) string {
	level := strings.ToLower(strings.TrimSpace(value))
	switch level {
	case "debug", "info", "notice", "warning", "error", "critical", "alert", "emergency":
		return level
	default:
		return ""
	}
}

func validateHTTPURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("host is required")
	}
	return nil
}

func NormalizeName(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	var builder strings.Builder
	lastUnderscore := false
	for index, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			if index > 0 && !lastUnderscore {
				builder.WriteByte('_')
			}
			builder.WriteByte(byte(r + ('a' - 'A')))
			lastUnderscore = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if builder.Len() == 0 || lastUnderscore {
				continue
			}
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	normalized := strings.Trim(builder.String(), "_")
	if normalized == "" {
		normalized = strings.TrimSpace(fallback)
	}
	if normalized == "" {
		normalized = "item"
	}
	if normalized[0] >= '0' && normalized[0] <= '9' {
		normalized = "_" + normalized
	}
	return normalized
}

func cleanEnv(env map[string]EnvValue) (map[string]EnvValue, error) {
	return cleanReferenceMap("env", env)
}

func cleanReferenceMap(label string, env map[string]EnvValue) (map[string]EnvValue, error) {
	if len(env) == 0 {
		return nil, nil
	}
	normalized := make(map[string]EnvValue, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if err := validateEnvValue(value); err != nil {
			return nil, fmt.Errorf("%s %q: %w", label, key, err)
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func validateEnvValue(value EnvValue) error {
	sources := 0
	if value.Literal {
		sources++
	}
	if strings.TrimSpace(value.EnvRef) != "" {
		sources++
	}
	if strings.TrimSpace(value.SecretRef) != "" {
		sources++
	}
	if sources == 0 {
		return fmt.Errorf("must set one of string, value, env_ref, or secret_ref")
	}
	if sources > 1 {
		return fmt.Errorf("must set only one of value, env_ref, or secret_ref")
	}
	if strings.TrimSpace(value.SecretRef) != "" && !strings.HasPrefix(strings.TrimSpace(value.SecretRef), "env:") {
		return fmt.Errorf("secret_ref %q is not supported; use env:NAME", strings.TrimSpace(value.SecretRef))
	}
	if strings.TrimSpace(value.SecretRef) == "env:" {
		return fmt.Errorf("secret_ref env name is required")
	}
	return nil
}

func cleanRoots(values []rootJSON) ([]Root, error) {
	if len(values) == 0 {
		return nil, nil
	}
	roots := make([]Root, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		uri := strings.TrimSpace(value.URI)
		path := strings.TrimSpace(value.Path)
		if uri == "" && path != "" {
			nextURI, err := fileURI(path)
			if err != nil {
				return nil, err
			}
			uri = nextURI
		}
		if uri == "" {
			return nil, fmt.Errorf("root uri or path is required")
		}
		parsed, err := url.Parse(uri)
		if err != nil {
			return nil, fmt.Errorf("root uri %q is invalid: %w", uri, err)
		}
		if parsed.Scheme == "" {
			return nil, fmt.Errorf("root uri %q must be absolute", uri)
		}
		if seen[uri] {
			continue
		}
		seen[uri] = true
		roots = append(roots, Root{
			URI:  uri,
			Name: strings.TrimSpace(value.Name),
		})
	}
	if len(roots) == 0 {
		return nil, nil
	}
	return roots, nil
}

func cleanSampling(value *SamplingConfig) *SamplingConfig {
	if value == nil {
		return nil
	}
	return &SamplingConfig{Enabled: value.Enabled}
}

func cleanElicitation(value *ElicitationConfig) *ElicitationConfig {
	if value == nil {
		return nil
	}
	return &ElicitationConfig{Enabled: value.Enabled}
}

func cleanOAuth(value *OAuthConfig) (*OAuthConfig, error) {
	if value == nil {
		return nil, nil
	}
	grantType := strings.TrimSpace(strings.ToLower(value.GrantType))
	if grantType == "" {
		grantType = "client_credentials"
	}
	if grantType != "client_credentials" && grantType != "refresh_token" {
		return nil, fmt.Errorf("oauth grant_type %q is not supported", value.GrantType)
	}
	tokenURL := strings.TrimSpace(value.TokenURL)
	if tokenURL == "" {
		return nil, fmt.Errorf("oauth token_url is required")
	}
	if err := validateHTTPURL(tokenURL); err != nil {
		return nil, fmt.Errorf("oauth token_url: %w", err)
	}
	if value.ClientID == nil {
		return nil, fmt.Errorf("oauth client_id is required")
	}
	if err := validateEnvValue(*value.ClientID); err != nil {
		return nil, fmt.Errorf("oauth client_id: %w", err)
	}
	if value.ClientSecret == nil {
		return nil, fmt.Errorf("oauth client_secret is required")
	}
	if err := validateEnvValue(*value.ClientSecret); err != nil {
		return nil, fmt.Errorf("oauth client_secret: %w", err)
	}
	if value.ClientSecret.Literal {
		return nil, fmt.Errorf("oauth client_secret must use env_ref or secret_ref")
	}
	var refreshToken *EnvValue
	if grantType == "refresh_token" {
		if value.RefreshToken == nil {
			return nil, fmt.Errorf("oauth refresh_token is required")
		}
		if err := validateEnvValue(*value.RefreshToken); err != nil {
			return nil, fmt.Errorf("oauth refresh_token: %w", err)
		}
		if value.RefreshToken.Literal {
			return nil, fmt.Errorf("oauth refresh_token must use env_ref or secret_ref")
		}
		refreshToken = value.RefreshToken
	}
	authMethod := strings.TrimSpace(strings.ToLower(value.TokenEndpointAuthMethod))
	if authMethod == "" {
		authMethod = "client_secret_post"
	}
	if authMethod != "client_secret_post" && authMethod != "client_secret_basic" {
		return nil, fmt.Errorf("oauth token_endpoint_auth_method %q is not supported", value.TokenEndpointAuthMethod)
	}
	clientID := *value.ClientID
	clientSecret := *value.ClientSecret
	return &OAuthConfig{
		GrantType:               grantType,
		TokenURL:                tokenURL,
		ClientID:                &clientID,
		ClientSecret:            &clientSecret,
		RefreshToken:            refreshToken,
		Scopes:                  cleanStrings(value.Scopes),
		Audience:                strings.TrimSpace(value.Audience),
		Resource:                strings.TrimSpace(value.Resource),
		TokenEndpointAuthMethod: authMethod,
	}, nil
}

func hasAuthorizationHeader(headers map[string]EnvValue) bool {
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), "Authorization") {
			return true
		}
	}
	return false
}

func fileURI(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("root path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("root path %q is invalid: %w", path, err)
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String(), nil
}

func ResolveEnv(server ServerConfig) (map[string]string, error) {
	return ResolveEnvWithLookup(server, os.LookupEnv)
}

func ResolveEnvWithLookup(server ServerConfig, lookup func(string) (string, bool)) (map[string]string, error) {
	return resolveReferenceMap(server.Identifier, "env", server.Env, lookup)
}

func ResolveHeaders(server ServerConfig) (map[string]string, error) {
	return ResolveHeadersWithLookup(server, os.LookupEnv)
}

func ResolveHeadersWithLookup(server ServerConfig, lookup func(string) (string, bool)) (map[string]string, error) {
	return resolveReferenceMap(server.Identifier, "header", server.Headers, lookup)
}

func ResolveOAuthClientCredentials(server ServerConfig) (*OAuthClientCredentials, error) {
	return ResolveOAuthClientCredentialsWithLookup(server, os.LookupEnv)
}

func ResolveOAuthClientCredentialsWithLookup(server ServerConfig, lookup func(string) (string, bool)) (*OAuthClientCredentials, error) {
	if server.OAuth == nil {
		return nil, nil
	}
	if server.OAuth.ClientID == nil {
		return nil, fmt.Errorf("mcp server %q oauth client_id is required", server.Identifier)
	}
	if server.OAuth.ClientSecret == nil {
		return nil, fmt.Errorf("mcp server %q oauth client_secret is required", server.Identifier)
	}
	clientID, err := resolveReferenceValue(server.Identifier, "oauth client_id", *server.OAuth.ClientID, lookup)
	if err != nil {
		return nil, err
	}
	clientSecret, err := resolveReferenceValue(server.Identifier, "oauth client_secret", *server.OAuth.ClientSecret, lookup)
	if err != nil {
		return nil, err
	}
	var refreshToken string
	if server.OAuth.RefreshToken != nil {
		refreshToken, err = resolveReferenceValue(server.Identifier, "oauth refresh_token", *server.OAuth.RefreshToken, lookup)
		if err != nil {
			return nil, err
		}
	}
	return &OAuthClientCredentials{
		TokenURL:                server.OAuth.TokenURL,
		GrantType:               strings.TrimSpace(strings.ToLower(server.OAuth.GrantType)),
		ClientID:                clientID,
		ClientSecret:            clientSecret,
		RefreshToken:            refreshToken,
		Scopes:                  append([]string(nil), server.OAuth.Scopes...),
		Audience:                server.OAuth.Audience,
		Resource:                server.OAuth.Resource,
		TokenEndpointAuthMethod: server.OAuth.TokenEndpointAuthMethod,
	}, nil
}

func resolveReferenceMap(serverIdentifier string, label string, values map[string]EnvValue, lookup func(string) (string, bool)) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(values))
	for key, value := range values {
		resolvedValue, err := resolveReferenceValue(serverIdentifier, fmt.Sprintf("%s %q", label, key), value, lookup)
		if err != nil {
			return nil, err
		}
		resolved[key] = resolvedValue
	}
	return resolved, nil
}

func resolveReferenceValue(serverIdentifier string, label string, value EnvValue, lookup func(string) (string, bool)) (string, error) {
	switch {
	case value.Literal:
		return value.Value, nil
	case strings.TrimSpace(value.EnvRef) != "":
		envName := strings.TrimSpace(value.EnvRef)
		secret, ok := lookup(envName)
		if !ok {
			return "", fmt.Errorf("mcp server %q %s references unset environment variable %q", serverIdentifier, label, envName)
		}
		return secret, nil
	case strings.TrimSpace(value.SecretRef) != "":
		secretRef := strings.TrimSpace(value.SecretRef)
		envName := strings.TrimPrefix(secretRef, "env:")
		secret, ok := lookup(envName)
		if !ok {
			return "", fmt.Errorf("mcp server %q %s references unset environment variable %q", serverIdentifier, label, envName)
		}
		return secret, nil
	default:
		return "", fmt.Errorf("mcp server %q %s has no value source", serverIdentifier, label)
	}
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
