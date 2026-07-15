package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"tiggy-manage-agent/sdk/tma"
)

var openAuthURL = launchBrowser

func commandAuth(client *apiClient, args []string) error {
	if len(args) == 0 {
		return errors.New("auth command requires login, status, or logout")
	}
	switch args[0] {
	case "login":
		return commandAuthLogin(client, args[1:])
	case "status":
		if len(args) != 1 {
			return errors.New("auth status does not accept arguments")
		}
		return commandAuthStatus(client)
	case "logout":
		if len(args) != 1 {
			return errors.New("auth logout does not accept arguments")
		}
		return commandAuthLogout(client)
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

func commandAuthLogin(client *apiClient, args []string) error {
	flags := flag.NewFlagSet("auth login", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	loginTimeout := defaultAuthLoginTimeout()
	noBrowser := false
	flags.DurationVar(&loginTimeout, "timeout", loginTimeout, "device authorization timeout")
	flags.BoolVar(&noBrowser, "no-browser", false, "do not open the verification URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("auth login does not accept positional arguments")
	}
	if loginTimeout <= 0 {
		return errors.New("auth login timeout must be positive")
	}
	if strings.TrimSpace(client.authToken) != "" {
		return errors.New("auth login cannot replace --auth-token or TMA_AUTH_TOKEN; unset the explicit token first")
	}
	unauthenticated, err := client.unauthenticatedSDKClient()
	if err != nil {
		return err
	}
	configuration, err := unauthenticated.Auth.Configuration(context.Background())
	if err != nil {
		return fmt.Errorf("discover Server authentication configuration: %w", err)
	}
	if configuration.Mode != "oidc" || configuration.OIDC == nil {
		return fmt.Errorf("Server authentication mode is %q; OIDC device login is unavailable", configuration.Mode)
	}
	if !configuration.OIDC.DeviceAuthorization || strings.TrimSpace(configuration.OIDC.ClientID) == "" {
		return errors.New("Server does not advertise an OIDC Device Authorization client")
	}
	oidcConfiguration := authOIDCConfiguration{
		Issuer: strings.TrimSpace(configuration.OIDC.Issuer), Audience: strings.TrimSpace(configuration.OIDC.Audience),
		ClientID: strings.TrimSpace(configuration.OIDC.ClientID), Scopes: normalizedAuthScopes(configuration.OIDC.Scopes),
	}
	loginContext, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	providerContext := loginContext
	if client.http != nil {
		providerContext = oidc.ClientContext(loginContext, client.http)
	}
	provider, err := oidc.NewProvider(providerContext, oidcConfiguration.Issuer)
	if err != nil {
		return fmt.Errorf("discover OIDC provider: %w", err)
	}
	oauthConfiguration := oauth2.Config{
		ClientID: oidcConfiguration.ClientID,
		Endpoint: provider.Endpoint(),
		Scopes:   oidcConfiguration.Scopes,
	}
	if strings.TrimSpace(oauthConfiguration.Endpoint.DeviceAuthURL) == "" {
		return errors.New("OIDC provider discovery does not advertise device_authorization_endpoint")
	}
	oauthContext := loginContext
	if client.http != nil {
		oauthContext = context.WithValue(loginContext, oauth2.HTTPClient, client.http)
	}
	device, err := oauthConfiguration.DeviceAuth(oauthContext)
	if err != nil {
		return fmt.Errorf("start OIDC device authorization: %w", err)
	}
	verificationURL := strings.TrimSpace(device.VerificationURIComplete)
	if verificationURL == "" {
		verificationURL = strings.TrimSpace(device.VerificationURI)
	}
	fmt.Fprintf(os.Stderr, "Open %s\n", verificationURL)
	fmt.Fprintf(os.Stderr, "Enter code: %s\n", device.UserCode)
	if !noBrowser {
		if err := openAuthURL(verificationURL); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\n", err)
		}
	}
	token, err := oauthConfiguration.DeviceAccessToken(oauthContext, device)
	if err != nil {
		return fmt.Errorf("complete OIDC device authorization: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return errors.New("OIDC provider returned an empty access token")
	}
	state, err := validateAuthToken(client, token.AccessToken)
	if err != nil {
		return fmt.Errorf("Server rejected the OIDC token: %w", err)
	}
	if !state.Authenticated || state.Principal == nil {
		return errors.New("Server did not resolve an authenticated principal")
	}
	store := client.credentials
	if store == nil {
		store = keyringCredentialStore{}
		client.credentials = store
	}
	if err := store.Save(client.baseURL, storedCredentialFromToken(client.baseURL, oidcConfiguration, token)); err != nil {
		return err
	}
	client.tokenSource = (&keyringTokenSource{baseURL: client.baseURL, store: store, httpClient: client.http}).Token
	client.sdk = nil
	return printJSON(map[string]any{
		"authenticated":    true,
		"base_url":         normalizeBaseURL(client.baseURL),
		"issuer":           oidcConfiguration.Issuer,
		"client_id":        oidcConfiguration.ClientID,
		"expires_at":       token.Expiry,
		"principal":        state.Principal,
		"credential_store": "system_keychain",
	})
}

func commandAuthStatus(client *apiClient) error {
	configurationClient, err := client.unauthenticatedSDKClient()
	if err != nil {
		return err
	}
	configuration, err := configurationClient.Auth.Configuration(context.Background())
	if err != nil {
		return err
	}
	state, stateErr := clientAuthState(client)
	result := map[string]any{
		"base_url": normalizeBaseURL(client.baseURL),
		"mode":     configuration.Mode,
	}
	if stateErr == nil {
		result["authenticated"] = state.Authenticated
		if state.Principal != nil {
			result["principal"] = state.Principal
		}
	} else {
		result["authenticated"] = false
		result["error"] = stateErr.Error()
	}
	if client.authToken != "" {
		result["credential_source"] = "explicit_token"
	} else if client.credentials != nil {
		credential, loadErr := client.credentials.Load(client.baseURL)
		if loadErr == nil {
			result["credential_source"] = "system_keychain"
			result["issuer"] = credential.Issuer
			result["client_id"] = credential.ClientID
			result["expires_at"] = credential.Expiry
		} else if !errors.Is(loadErr, errCredentialNotFound) {
			return loadErr
		}
	}
	return printJSON(result)
}

func commandAuthLogout(client *apiClient) error {
	store := client.credentials
	if store == nil {
		store = keyringCredentialStore{}
	}
	warnings := make([]string, 0, 2)
	credential, loadErr := store.Load(client.baseURL)
	if loadErr == nil && strings.TrimSpace(credential.RefreshToken) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		revokeErr := revokeOIDCCredential(ctx, client.http, credential)
		cancel()
		if revokeErr != nil {
			warnings = append(warnings, "remote token revocation failed; local credentials were removed: "+revokeErr.Error())
		}
	} else if loadErr != nil && !errors.Is(loadErr, errCredentialNotFound) {
		warnings = append(warnings, "stored credentials could not be read before local removal: "+loadErr.Error())
	}
	err := store.Delete(client.baseURL)
	if err != nil && !errors.Is(err, errCredentialNotFound) {
		return err
	}
	client.sdk = nil
	result := map[string]any{
		"logged_out": err == nil,
		"base_url":   normalizeBaseURL(client.baseURL),
	}
	if errors.Is(err, errCredentialNotFound) {
		result["logged_out"] = true
		result["credential_found"] = false
	}
	if client.authToken != "" {
		warnings = append(warnings, "explicit --auth-token or TMA_AUTH_TOKEN remains active")
	}
	if len(warnings) != 0 {
		result["warning"] = strings.Join(warnings, "; ")
	}
	return printJSON(result)
}

func revokeOIDCCredential(ctx context.Context, httpClient *http.Client, credential storedCredential) error {
	providerContext := ctx
	if httpClient != nil {
		providerContext = oidc.ClientContext(ctx, httpClient)
	}
	provider, err := oidc.NewProvider(providerContext, credential.Issuer)
	if err != nil {
		return fmt.Errorf("discover OIDC provider: %w", err)
	}
	var metadata struct {
		RevocationEndpoint string `json:"revocation_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return fmt.Errorf("read OIDC provider metadata: %w", err)
	}
	if strings.TrimSpace(metadata.RevocationEndpoint) == "" {
		return errors.New("OIDC provider does not advertise revocation_endpoint")
	}
	form := url.Values{
		"token":           {credential.RefreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {credential.ClientID},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RevocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create revocation request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("revoke refresh token: provider returned %s: %s", response.Status, message)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func clientAuthState(client *apiClient) (tma.AuthState, error) {
	sdk, err := client.sdkClient()
	if err != nil {
		return tma.AuthState{}, err
	}
	return sdk.Auth.Me(context.Background())
}

func validateAuthToken(client *apiClient, accessToken string) (tma.AuthState, error) {
	options := []tma.Option{tma.WithBearerToken(accessToken)}
	if client.http != nil {
		options = append(options, tma.WithHTTPClient(client.http))
	}
	temporary, err := tma.NewClient(client.baseURL, options...)
	if err != nil {
		return tma.AuthState{}, err
	}
	return temporary.Auth.Me(context.Background())
}

func normalizedAuthScopes(scopes []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(scopes)+1)
	for _, scope := range append([]string{oidc.ScopeOpenID}, scopes...) {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result
}

func defaultAuthLoginTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv("TMA_AUTH_LOGIN_TIMEOUT"))
	if value == "" {
		return 5 * time.Minute
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 5 * time.Minute
	}
	return duration
}

func launchBrowser(target string) error {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("authorization URL must be absolute HTTP(S)")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return nil
}
