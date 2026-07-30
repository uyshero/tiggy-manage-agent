package biographyvoice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	biographyAuthModeDisabled = "disabled"
	biographyAuthModeOIDC     = "oidc"
)

type authenticatedUser struct {
	ID          string
	Subject     string
	DisplayName string
	AccessToken string
}

type publicUser struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	DisplayName string `json:"display_name,omitempty"`
}

type authService struct {
	issuer   string
	audience string
	clientID string
	scopes   []string
	verifier *oidc.IDTokenVerifier
	store    biographyStore
}

type authConfigResponse struct {
	Enabled bool              `json:"enabled"`
	Mode    string            `json:"mode"`
	OIDC    *authOIDCResponse `json:"oidc,omitempty"`
}

type authOIDCResponse struct {
	Issuer   string   `json:"issuer"`
	Audience string   `json:"audience"`
	ClientID string   `json:"client_id,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

type oidcTokenClaims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Email             string `json:"email"`
}

func newAuthService(config Config, store biographyStore) (*authService, error) {
	if strings.TrimSpace(config.AuthMode) != biographyAuthModeOIDC {
		return nil, nil
	}
	if store == nil {
		return nil, fmt.Errorf("biography OIDC auth requires a data store")
	}
	timeout := config.AuthOIDCHTTPTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := &http.Client{Timeout: timeout}
	ctx = oidc.ClientContext(ctx, client)
	keySetCtx := oidc.ClientContext(context.Background(), client)
	jwksURL := strings.TrimSpace(config.AuthOIDCJWKSURL)
	if strings.TrimSpace(config.AuthOIDCJWKSURL) == "" {
		provider, err := oidc.NewProvider(ctx, strings.TrimSpace(config.AuthOIDCIssuer))
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery failed: %w", err)
		}
		var metadata struct {
			JWKSURL string `json:"jwks_uri"`
		}
		if err := provider.Claims(&metadata); err != nil || strings.TrimSpace(metadata.JWKSURL) == "" {
			return nil, errors.New("OIDC discovery did not provide jwks_uri")
		}
		jwksURL = strings.TrimSpace(metadata.JWKSURL)
	}
	keySet := oidc.NewRemoteKeySet(keySetCtx, jwksURL)
	return &authService{
		issuer: strings.TrimSpace(config.AuthOIDCIssuer), audience: strings.TrimSpace(config.AuthOIDCAudience),
		clientID: strings.TrimSpace(config.AuthOIDCClientID), scopes: append([]string(nil), config.AuthOIDCScopes...),
		verifier: oidc.NewVerifier(strings.TrimSpace(config.AuthOIDCIssuer), keySet, &oidc.Config{ClientID: strings.TrimSpace(config.AuthOIDCAudience)}),
		store:    store,
	}, nil
}

func (auth *authService) publicConfig() authConfigResponse {
	if auth == nil {
		return authConfigResponse{Enabled: false, Mode: biographyAuthModeDisabled}
	}
	return authConfigResponse{
		Enabled: true, Mode: biographyAuthModeOIDC,
		OIDC: &authOIDCResponse{
			Issuer: auth.issuer, Audience: auth.audience, ClientID: auth.clientID, Scopes: append([]string(nil), auth.scopes...),
		},
	}
}

func (auth *authService) authenticateRequest(r *http.Request) (*authenticatedUser, error) {
	if auth == nil {
		return nil, nil
	}
	token := bearerToken(r)
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("access_token"))
	}
	idToken, err := auth.verifier.Verify(r.Context(), token)
	if err != nil {
		return nil, err
	}
	var claims oidcTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, err
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, errors.New("OIDC token did not contain subject")
	}
	displayName := firstNonEmpty(claims.Name, claims.PreferredUsername, claims.Email, claims.Subject)
	user, err := auth.store.upsertOIDCUser(auth.issuer, claims.Subject, displayName, time.Now())
	if err != nil {
		return nil, err
	}
	return &authenticatedUser{ID: user.ID, Subject: user.OIDCSubject, DisplayName: user.DisplayName, AccessToken: token}, nil
}

type biographyDataStore struct {
	path string
	mu   sync.Mutex
	data biographyData
}

type biographyData struct {
	Users             map[string]storedUser                                   `json:"users"`
	Progress          map[string]BiographyProgress                            `json:"progress"`
	Recordings        map[string]map[string]storedRecording                   `json:"recordings"`
	RecordingSegments map[string]map[string]map[string]storedRecordingSegment `json:"recordingSegments"`
}

type storedUser struct {
	ID          string    `json:"id"`
	OIDCIssuer  string    `json:"oidc_issuer"`
	OIDCSubject string    `json:"oidc_subject"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

type storedRecording struct {
	ID           string                   `json:"id"`
	ProjectID    string                   `json:"projectID"`
	ChapterID    string                   `json:"chapterID"`
	ChapterTitle string                   `json:"chapterTitle"`
	Transcript   string                   `json:"transcript"`
	DurationMS   int64                    `json:"durationMs"`
	Title        string                   `json:"title"`
	CreatedAt    time.Time                `json:"createdAt"`
	UpdatedAt    time.Time                `json:"updatedAt"`
	SizeBytes    int64                    `json:"sizeBytes"`
	ContentType  string                   `json:"contentType"`
	Segments     []storedRecordingSegment `json:"segments,omitempty"`
}

type storedRecordingSegment struct {
	ID                  string    `json:"id"`
	Transcript          string    `json:"transcript"`
	DurationMS          int64     `json:"durationMs"`
	CreatedAt           time.Time `json:"createdAt"`
	SizeBytes           int64     `json:"sizeBytes"`
	ContentType         string    `json:"contentType"`
	TranscriptionStatus string    `json:"transcriptionStatus"`
}

// biographyStore keeps development storage replaceable without allowing the
// production gateway to silently fall back to an unshared local JSON file.
type biographyStore interface {
	upsertOIDCUser(issuer string, subject string, displayName string, now time.Time) (storedUser, error)
	userByID(userID string) (storedUser, bool, error)
	progressForUser(userID string) (BiographyProgress, bool, error)
	saveProgress(userID string, progress BiographyProgress) error
	listRecordings(userID string, projectID string) ([]storedRecording, error)
	recordingForUser(userID string, recordingID string) (storedRecording, bool, error)
	saveRecording(userID string, recording storedRecording) error
	deleteRecording(userID string, recordingID string) error
	writeRecordingAudio(userID string, recordingID string, source io.Reader, maxBytes int64) (int64, error)
	openRecordingAudio(userID string, recordingID string) (io.ReadCloser, error)
	removeRecordingAudio(userID string, recordingID string) error
}

type recordingSegmentStore interface {
	listRecordingSegments(userID string, recordingID string) ([]storedRecordingSegment, error)
	recordingSegmentForUser(userID string, recordingID string, segmentID string) (storedRecordingSegment, bool, error)
	saveRecordingSegment(userID string, recordingID string, segment storedRecordingSegment) error
	deleteRecordingSegment(userID string, recordingID string, segmentID string) error
	writeRecordingSegmentAudio(userID string, recordingID string, segmentID string, source io.Reader, maxBytes int64) (int64, error)
	openRecordingSegmentAudio(userID string, recordingID string, segmentID string) (io.ReadCloser, error)
	removeRecordingSegmentAudio(userID string, recordingID string, segmentID string) error
}

type BiographyProgress struct {
	Project             BiographyProject          `json:"project"`
	LastInterview       *InterviewSessionProgress `json:"lastInterview,omitempty"`
	ActiveChapterTitles []string                  `json:"activeChapterTitles"`
	PendingConfirmation string                    `json:"pendingConfirmation,omitempty"`
	PendingTranscripts  []string                  `json:"pendingTranscripts,omitempty"`
	RecentQuestions     []string                  `json:"recentQuestions,omitempty"`
	UpdatedAt           time.Time                 `json:"updatedAt"`
}

type InterviewSessionProgress struct {
	ID                  string     `json:"id"`
	StartedAt           time.Time  `json:"startedAt"`
	EndedAt             *time.Time `json:"endedAt,omitempty"`
	DurationSeconds     int        `json:"durationSeconds"`
	LastChapterTitle    string     `json:"lastChapterTitle,omitempty"`
	TranscriptCount     int        `json:"transcriptCount"`
	TodayRecordingSaved bool       `json:"todayRecordingSaved"`
}

type activeProgressSession struct {
	ID                  string
	StartedAt           time.Time
	TranscriptCount     int
	TodayRecordingSaved bool
}

func newBiographyDataStore(dataDir string) (*biographyDataStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("biography data directory is required")
	}
	store := &biographyDataStore{path: filepath.Join(dataDir, "biography.json")}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *biographyDataStore) load() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.data = biographyData{Users: map[string]storedUser{}, Progress: map[string]BiographyProgress{}, Recordings: map[string]map[string]storedRecording{}, RecordingSegments: map[string]map[string]map[string]storedRecordingSegment{}}
	payload, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, &store.data); err != nil {
		return fmt.Errorf("decode biography data store: %w", err)
	}
	if store.data.Users == nil {
		store.data.Users = map[string]storedUser{}
	}
	if store.data.Progress == nil {
		store.data.Progress = map[string]BiographyProgress{}
	}
	if store.data.Recordings == nil {
		store.data.Recordings = map[string]map[string]storedRecording{}
	}
	if store.data.RecordingSegments == nil {
		store.data.RecordingSegments = map[string]map[string]map[string]storedRecordingSegment{}
	}
	return nil
}

func (store *biographyDataStore) upsertOIDCUser(issuer string, subject string, displayName string, now time.Time) (storedUser, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	id := stableUserID(issuer, subject)
	user := store.data.Users[id]
	if user.ID == "" {
		user = storedUser{ID: id, OIDCIssuer: issuer, OIDCSubject: subject, CreatedAt: now}
	}
	user.DisplayName = strings.TrimSpace(displayName)
	user.LastLoginAt = now
	store.data.Users[user.ID] = user
	return user, store.saveLocked()
}

func (store *biographyDataStore) userByID(userID string) (storedUser, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	user, ok := store.data.Users[userID]
	return user, ok, nil
}

func (store *biographyDataStore) progressForUser(userID string) (BiographyProgress, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	progress, ok := store.data.Progress[userID]
	return progress, ok, nil
}

func (store *biographyDataStore) saveProgress(userID string, progress BiographyProgress) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.data.Progress[userID] = progress
	return store.saveLocked()
}

func (store *biographyDataStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(store.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := store.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, store.path)
}

func buildBiographyProgress(project BiographyProject, recentQuestions []string, pendingTranscripts []string, session activeProgressSession, endedAt *time.Time, now time.Time) BiographyProgress {
	lastChapterTitle := activeChapterTitle(project)
	duration := 0
	if !session.StartedAt.IsZero() {
		until := now
		if endedAt != nil {
			until = *endedAt
		}
		duration = max(0, int(until.Sub(session.StartedAt).Seconds()))
	}
	var last *InterviewSessionProgress
	if session.ID != "" {
		last = &InterviewSessionProgress{
			ID: session.ID, StartedAt: session.StartedAt, EndedAt: endedAt, DurationSeconds: duration,
			LastChapterTitle: lastChapterTitle, TranscriptCount: session.TranscriptCount,
			TodayRecordingSaved: session.TodayRecordingSaved,
		}
	}
	return BiographyProgress{
		Project: cloneBiographyProject(project), LastInterview: last, ActiveChapterTitles: activeChapterTitles(project),
		PendingConfirmation: strings.TrimSpace(project.PendingConfirmation),
		PendingTranscripts:  append(make([]string, 0, len(pendingTranscripts)), pendingTranscripts...),
		RecentQuestions:     append(make([]string, 0, len(recentQuestions)), recentQuestions...), UpdatedAt: now,
	}
}

func activeChapterTitles(project BiographyProject) []string {
	titles := make([]string, 0)
	for _, chapter := range project.Chapters {
		if chapter.Status == "collecting" || chapter.Status == "confirm" {
			titles = append(titles, chapter.Title)
		}
	}
	return titles
}

func activeChapterTitle(project BiographyProject) string {
	titles := activeChapterTitles(project)
	if len(titles) == 0 {
		return ""
	}
	return titles[0]
}

func completedChapterCount(project BiographyProject) int {
	count := 0
	for _, chapter := range project.Chapters {
		if chapter.Status == "completed" {
			count++
		}
	}
	return count
}

func publicUserFromStored(user storedUser) publicUser {
	return publicUser{ID: user.ID, Subject: user.OIDCSubject, DisplayName: user.DisplayName}
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func subtleStringCompare(left string, right string) bool {
	leftSum := sha256.Sum256([]byte(left))
	rightSum := sha256.Sum256([]byte(right))
	return leftSum == rightSum
}

func stableUserID(issuer string, subject string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(issuer) + "\x00" + strings.TrimSpace(subject)))
	return "usr_" + hex.EncodeToString(sum[:12])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func randomID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw), nil
}
