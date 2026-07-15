package envvars

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	MasterKeyEnvironmentVariable = "TMA_ENV_ENCRYPTION_KEY"
	maxNameLength                = 128
	maxValueBytes                = 64 << 10
)

var (
	ErrNotConfigured = errors.New("environment variable encryption is not configured")
	ErrInvalid       = errors.New("invalid managed environment variable")
	namePattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type EncryptedVariable struct {
	WorkspaceID string
	Name        string
	Ciphertext  []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type VariableMetadata struct {
	Name       string    `json:"name"`
	Configured bool      `json:"configured"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Store interface {
	ListEncryptedEnvironmentVariables(context.Context, string) ([]EncryptedVariable, error)
	UpsertEncryptedEnvironmentVariable(context.Context, EncryptedVariable) (EncryptedVariable, error)
	DeleteEncryptedEnvironmentVariable(context.Context, string, string) error
}

type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(encodedKey string) (*Cipher, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		return nil, ErrNotConfigured
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%w: %s must be a base64-encoded 32-byte key", ErrNotConfigured, MasterKeyEnvironmentVariable)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create environment variable cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create environment variable AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func CipherFromEnvironment() (*Cipher, error) {
	return NewCipher(os.Getenv(MasterKeyEnvironmentVariable))
}

func (c *Cipher) Seal(plaintext []byte, associatedData string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrNotConfigured
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate environment variable nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, []byte(associatedData)), nil
}

func (c *Cipher) Open(ciphertext []byte, associatedData string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrNotConfigured
	}
	if len(ciphertext) < c.aead.NonceSize() {
		return nil, errors.New("managed environment variable ciphertext is invalid")
	}
	nonce, payload := ciphertext[:c.aead.NonceSize()], ciphertext[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, payload, []byte(associatedData))
	if err != nil {
		return nil, errors.New("managed environment variable could not be decrypted")
	}
	return plaintext, nil
}

func (c *Cipher) SealMap(values map[string]string, associatedData string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode managed environment: %w", err)
	}
	sealed, err := c.Seal(encoded, associatedData)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) OpenMap(envelope string, associatedData string) (map[string]string, error) {
	if strings.TrimSpace(envelope) == "" {
		return nil, nil
	}
	sealed, err := base64.RawStdEncoding.DecodeString(envelope)
	if err != nil {
		return nil, errors.New("managed environment envelope is invalid")
	}
	encoded, err := c.Open(sealed, associatedData)
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, errors.New("managed environment envelope payload is invalid")
	}
	return values, nil
}

type Service struct {
	store  Store
	cipher *Cipher
}

func NewService(store Store, cipher *Cipher) (*Service, error) {
	if store == nil {
		return nil, errors.New("managed environment variable store is unavailable")
	}
	if cipher == nil {
		return nil, ErrNotConfigured
	}
	return &Service{store: store, cipher: cipher}, nil
}

func NewServiceFromEnvironment(store Store) (*Service, error) {
	cipher, err := CipherFromEnvironment()
	if err != nil {
		return nil, err
	}
	return NewService(store, cipher)
}

func (s *Service) List(ctx context.Context, workspaceID string) ([]VariableMetadata, error) {
	records, err := s.store.ListEncryptedEnvironmentVariables(ctx, normalizedWorkspace(workspaceID))
	if err != nil {
		return nil, err
	}
	items := make([]VariableMetadata, 0, len(records))
	for _, record := range records {
		items = append(items, VariableMetadata{Name: record.Name, Configured: len(record.Ciphertext) > 0, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *Service) Put(ctx context.Context, workspaceID string, name string, value string) (VariableMetadata, error) {
	workspaceID = normalizedWorkspace(workspaceID)
	name = strings.TrimSpace(name)
	if err := Validate(name, value); err != nil {
		return VariableMetadata{}, err
	}
	ciphertext, err := s.cipher.Seal([]byte(value), variableAssociatedData(workspaceID, name))
	if err != nil {
		return VariableMetadata{}, err
	}
	record, err := s.store.UpsertEncryptedEnvironmentVariable(ctx, EncryptedVariable{WorkspaceID: workspaceID, Name: name, Ciphertext: ciphertext})
	if err != nil {
		return VariableMetadata{}, err
	}
	return VariableMetadata{Name: record.Name, Configured: true, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

func (s *Service) Delete(ctx context.Context, workspaceID string, name string) error {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) || len(name) > maxNameLength {
		return fmt.Errorf("%w: invalid variable name", ErrInvalid)
	}
	return s.store.DeleteEncryptedEnvironmentVariable(ctx, normalizedWorkspace(workspaceID), name)
}

func (s *Service) Resolve(ctx context.Context, workspaceID string) (map[string]string, error) {
	workspaceID = normalizedWorkspace(workspaceID)
	records, err := s.store.ListEncryptedEnvironmentVariables(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(records))
	for _, record := range records {
		plaintext, err := s.cipher.Open(record.Ciphertext, variableAssociatedData(workspaceID, record.Name))
		if err != nil {
			return nil, fmt.Errorf("decrypt managed environment variable %q: %w", record.Name, err)
		}
		values[record.Name] = string(plaintext)
	}
	return values, nil
}

func ResolveWorkspace(ctx context.Context, store any, workspaceID string) (map[string]string, *Cipher, error) {
	environmentStore, ok := store.(Store)
	if !ok {
		return nil, nil, nil
	}
	records, err := environmentStore.ListEncryptedEnvironmentVariables(ctx, normalizedWorkspace(workspaceID))
	if err != nil {
		return nil, nil, err
	}
	if len(records) == 0 {
		return nil, nil, nil
	}
	cipher, err := CipherFromEnvironment()
	if err != nil {
		return nil, nil, err
	}
	service, _ := NewService(environmentStore, cipher)
	values, err := service.Resolve(ctx, workspaceID)
	return values, cipher, err
}

func Validate(name string, value string) error {
	if !namePattern.MatchString(name) || len(name) > maxNameLength {
		return fmt.Errorf("%w: name must match [A-Za-z_][A-Za-z0-9_]* and be at most %d characters", ErrInvalid, maxNameLength)
	}
	if len([]byte(value)) > maxValueBytes {
		return fmt.Errorf("%w: value exceeds %d bytes", ErrInvalid, maxValueBytes)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: value cannot contain NUL", ErrInvalid)
	}
	return nil
}

func EnvelopeAssociatedData(workspaceID string, sessionID string, turnID string) string {
	return "tma:tool-env:v1:" + normalizedWorkspace(workspaceID) + ":" + strings.TrimSpace(sessionID) + ":" + strings.TrimSpace(turnID)
}

func variableAssociatedData(workspaceID string, name string) string {
	return "tma:managed-env:v1:" + workspaceID + ":" + name
}

func normalizedWorkspace(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "wksp_default"
	}
	return workspaceID
}
