package biographyvoice

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const resumeTokenVersion = 1

type resumeTokenClaims struct {
	Version          int               `json:"v"`
	TMASessionID     string            `json:"sid"`
	ClientInstanceID string            `json:"cid"`
	ExpiresAt        int64             `json:"exp"`
	Project          *BiographyProject `json:"project,omitempty"`
}

type resumeTokenCodec struct {
	aead cipher.AEAD
	ttl  time.Duration
	now  func() time.Time
}

func newResumeTokenCodec(secret string, ttl time.Duration) (*resumeTokenCodec, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("biography resume signing key must be at least 32 bytes")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("biography resume TTL must be positive")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &resumeTokenCodec{aead: aead, ttl: ttl, now: time.Now}, nil
}

func (codec *resumeTokenCodec) Encode(tmaSessionID string, clientInstanceID string) (string, error) {
	return codec.EncodeState(tmaSessionID, clientInstanceID, nil)
}

func (codec *resumeTokenCodec) EncodeState(tmaSessionID string, clientInstanceID string, project *BiographyProject) (string, error) {
	if codec == nil || strings.TrimSpace(tmaSessionID) == "" || strings.TrimSpace(clientInstanceID) == "" {
		return "", fmt.Errorf("resume token requires TMA session and client instance IDs")
	}
	claims, err := json.Marshal(resumeTokenClaims{
		Version: resumeTokenVersion, TMASessionID: strings.TrimSpace(tmaSessionID),
		ClientInstanceID: strings.TrimSpace(clientInstanceID), ExpiresAt: codec.now().Add(codec.ttl).Unix(), Project: project,
	})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create resume token nonce: %w", err)
	}
	encrypted := codec.aead.Seal(nil, nonce, claims, []byte("tma-biography-resume-v1"))
	token := append(nonce, encrypted...)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func (codec *resumeTokenCodec) Decode(token string, clientInstanceID string) (resumeTokenClaims, error) {
	if codec == nil {
		return resumeTokenClaims{}, fmt.Errorf("resume tokens are not configured")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) <= codec.aead.NonceSize() {
		return resumeTokenClaims{}, fmt.Errorf("invalid biography resume token")
	}
	nonce, encrypted := raw[:codec.aead.NonceSize()], raw[codec.aead.NonceSize():]
	plaintext, err := codec.aead.Open(nil, nonce, encrypted, []byte("tma-biography-resume-v1"))
	if err != nil {
		return resumeTokenClaims{}, fmt.Errorf("invalid biography resume token")
	}
	var claims resumeTokenClaims
	if err := json.Unmarshal(plaintext, &claims); err != nil {
		return resumeTokenClaims{}, fmt.Errorf("invalid biography resume token")
	}
	if claims.Version != resumeTokenVersion || claims.TMASessionID == "" || claims.ClientInstanceID != strings.TrimSpace(clientInstanceID) {
		return resumeTokenClaims{}, fmt.Errorf("invalid biography resume token")
	}
	if codec.now().Unix() >= claims.ExpiresAt {
		return resumeTokenClaims{}, fmt.Errorf("biography resume token expired")
	}
	return claims, nil
}
