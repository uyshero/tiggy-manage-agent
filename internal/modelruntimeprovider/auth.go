package modelruntimeprovider

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	AuthModeStatic = "static"
	AuthModeSigned = "signed"

	defaultRuntimeTokenIssuer   = "tma-server"
	defaultRuntimeTokenAudience = "tma-model-runtime"
	defaultRuntimeTokenTTL      = time.Minute
	runtimeTokenType            = "TMA-RUNTIME+JWT"
	runtimeTokenUse             = "model_runtime"
	runtimeTokenClockSkew       = 5 * time.Second
)

type AuthConfig struct {
	Mode     string
	Secret   string
	Issuer   string
	Audience string
	TokenTTL time.Duration
	Now      func() time.Time
}

type runtimeTokenClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	JWTID     string `json:"jti"`
	TokenUse  string `json:"token_use"`
	Method    string `json:"method"`
	Path      string `json:"path"`
}

type requestAuthenticator struct {
	config AuthConfig
}

func newRequestAuthenticator(config AuthConfig) (*requestAuthenticator, error) {
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Mode == "" {
		config.Mode = AuthModeStatic
	}
	config.Secret = strings.TrimSpace(config.Secret)
	if config.Secret == "" {
		return nil, errors.New("model runtime auth token or signing secret is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	switch config.Mode {
	case AuthModeStatic:
	case AuthModeSigned:
		if len(config.Secret) < 32 {
			return nil, errors.New("model runtime signed auth secret must be at least 32 bytes")
		}
		if strings.TrimSpace(config.Issuer) == "" {
			config.Issuer = defaultRuntimeTokenIssuer
		}
		if strings.TrimSpace(config.Audience) == "" {
			config.Audience = defaultRuntimeTokenAudience
		}
		if config.TokenTTL == 0 {
			config.TokenTTL = defaultRuntimeTokenTTL
		}
		if config.TokenTTL < time.Second || config.TokenTTL > 5*time.Minute {
			return nil, errors.New("model runtime signed token TTL must be between 1 and 300 seconds")
		}
	default:
		return nil, fmt.Errorf("unsupported model runtime auth mode %q", config.Mode)
	}
	return &requestAuthenticator{config: config}, nil
}

func (a *requestAuthenticator) authorization(method, path string) (string, error) {
	if a.config.Mode == AuthModeStatic {
		return "Bearer " + a.config.Secret, nil
	}
	now := a.config.Now().UTC()
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("generate model runtime token ID: %w", err)
	}
	claims := runtimeTokenClaims{
		Issuer: strings.TrimSpace(a.config.Issuer), Audience: strings.TrimSpace(a.config.Audience),
		ExpiresAt: now.Add(a.config.TokenTTL).Unix(), IssuedAt: now.Unix(),
		NotBefore: now.Add(-runtimeTokenClockSkew).Unix(), JWTID: hex.EncodeToString(jti),
		TokenUse: runtimeTokenUse, Method: strings.ToUpper(strings.TrimSpace(method)), Path: path,
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": runtimeTokenType})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode model runtime token: %w", err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(a.config.Secret))
	_, _ = mac.Write([]byte(unsigned))
	return "Bearer " + unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (a *requestAuthenticator) verifyRequest(r *http.Request) bool {
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if a.config.Mode == AuthModeStatic {
		return len(provided) == len(a.config.Secret) && subtle.ConstantTimeCompare([]byte(provided), []byte(a.config.Secret)) == 1
	}
	claims, err := a.verifySignedToken(provided)
	return err == nil && claims.Method == r.Method && claims.Path == r.URL.Path
}

func (a *requestAuthenticator) verifySignedToken(token string) (runtimeTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "HS256" || header.Type != runtimeTokenType {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token header")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token signature")
	}
	mac := hmac.New(sha256.New, []byte(a.config.Secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token claims")
	}
	var claims runtimeTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return runtimeTokenClaims{}, errors.New("invalid model runtime token claims")
	}
	now := a.config.Now().UTC().Unix()
	maxTTL := int64(a.config.TokenTTL / time.Second)
	if claims.TokenUse != runtimeTokenUse || claims.Issuer != strings.TrimSpace(a.config.Issuer) ||
		claims.Audience != strings.TrimSpace(a.config.Audience) || claims.JWTID == "" ||
		claims.Method == "" || claims.Path == "" || claims.ExpiresAt <= now ||
		claims.NotBefore > now+int64(runtimeTokenClockSkew/time.Second) ||
		claims.IssuedAt > now+int64(runtimeTokenClockSkew/time.Second) ||
		claims.ExpiresAt-claims.IssuedAt < 1 || claims.ExpiresAt-claims.IssuedAt > maxTTL {
		return runtimeTokenClaims{}, errors.New("model runtime token claims rejected")
	}
	return claims, nil
}
