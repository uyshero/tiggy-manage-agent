package biographyvoice

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
)

func TestMockVoiceSessionProtocol(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-1"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我十九岁去了上海"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "我十九岁去了上海")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "我十九岁去了上海")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSStart, Text: "那是第一次离开家吗？"})
	assertTextEvents(t, ctx, connection, ServerProjectUpdated, ServerChapterConfirmation, ServerTTSStarted, ServerTTSFinished)
}

func TestMockVoiceSessionCanDeferInterviewUntilFollowupRequest(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-defer"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我想补充第一段"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "我想补充第一段")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit, DeferInterview: true})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "我想补充第一段")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInterviewFollowup, Text: "我想补充第一段\n这是补充内容"})
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")
}

func TestMockVoiceSessionStoresInterviewOrder(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-order"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInterviewOrderSet, InterviewOrder: InterviewOrderKeyMoments})
	updated := readServerTextMessage(t, ctx, connection)
	if updated.Type != ServerProjectUpdated || updated.Project == nil || updated.Project.InterviewOrder != InterviewOrderKeyMoments {
		t.Fatalf("interview order was not saved: %+v", updated)
	}

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInterviewOrderSet, InterviewOrder: "random"})
	invalid := readServerTextMessage(t, ctx, connection)
	if invalid.Type != ServerError || invalid.Code != "invalid_interview_order" {
		t.Fatalf("invalid interview order was not rejected: %+v", invalid)
	}
}

func TestMockVoiceSessionRejectsUnknownChapterFocus(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-chapter-focus"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInterviewChapterFocus, ChapterID: "not-my-chapter"})
	invalid := readServerTextMessage(t, ctx, connection)
	if invalid.Type != ServerError || invalid.Code != "invalid_chapter" {
		t.Fatalf("unknown chapter focus was not rejected: %+v", invalid)
	}
}

func TestGatewayCORSUsesConfiguredOriginAllowlist(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1:*", "app.example.com"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/v1/auth/config", nil)
	allowedRequest.Header.Set("Origin", "http://127.0.0.1:5175")
	server.Handler().ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK {
		t.Fatalf("expected allowed CORS request, got %d", allowed.Code)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5175" {
		t.Fatalf("unexpected allow origin %q", got)
	}
	if !strings.Contains(allowed.Header().Get("Vary"), "Origin") {
		t.Fatalf("expected Origin vary header, got %q", allowed.Header().Get("Vary"))
	}

	preflight := httptest.NewRecorder()
	preflightRequest := httptest.NewRequest(http.MethodOptions, "/v1/progress", nil)
	preflightRequest.Header.Set("Origin", "https://app.example.com")
	preflightRequest.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflightRequest.Header.Set("Access-Control-Request-Headers", "authorization")
	server.Handler().ServeHTTP(preflight, preflightRequest)
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("expected preflight success, got %d: %s", preflight.Code, preflight.Body.String())
	}
	if got := preflight.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("expected Authorization to be allowed, got %q", got)
	}

	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodOptions, "/v1/progress", nil)
	deniedRequest.Header.Set("Origin", "https://attacker.example")
	server.Handler().ServeHTTP(denied, deniedRequest)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("expected denied preflight, got %d", denied.Code)
	}
	if got := denied.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS allowance for denied origin %q", got)
	}
}

func TestRecordingBackupIsPrivateToOIDCUser(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC test key: %v", err)
	}
	provider := newBiographyOIDCTestProvider(t)
	provider.setKeys(biographyOIDCTestPublicJWK("bio-rsa", "RS256", &key.PublicKey))
	tokenA := signedBiographyOIDCTestToken(t, key, "bio-rsa", "RS256", map[string]any{
		"iss": provider.server.URL, "sub": "recording-user-a", "aud": "biography-mobile", "exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenB := signedBiographyOIDCTestToken(t, key, "bio-rsa", "RS256", map[string]any{
		"iss": provider.server.URL, "sub": "recording-user-b", "aud": "biography-mobile", "exp": time.Now().Add(time.Hour).Unix(),
	})
	server, err := NewServer(Config{
		HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"},
		AuthMode: biographyAuthModeOIDC, AuthOIDCIssuer: provider.server.URL, AuthOIDCAudience: "biography-mobile",
		AuthOIDCHTTPTimeout: 2 * time.Second, DataDir: t.TempDir(), RecordingMaxBytes: 1024,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	metadata, err := json.Marshal(recordingUploadMetadata{
		ProjectID: "book-a", ChapterID: "chapter-a", ChapterTitle: "童年", Transcript: "我记得院子里的梧桐树。",
		DurationMS: 4_500, Title: "童年 · 第 1 次采访", CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("metadata", string(metadata)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("audio", "recording.wav")
	if err != nil {
		t.Fatal(err)
	}
	audio := []byte("RIFF-recording-private-audio")
	if _, err := part.Write(audio); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	upload := httptest.NewRecorder()
	uploadRequest := httptest.NewRequest(http.MethodPut, "/v1/recordings/recording-private-a/audio", &uploadBody)
	uploadRequest.Header.Set("Authorization", "Bearer "+tokenA)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	server.Handler().ServeHTTP(upload, uploadRequest)
	if upload.Code != http.StatusCreated {
		t.Fatalf("expected recording upload to succeed, got %d: %s", upload.Code, upload.Body.String())
	}

	listA := httptest.NewRecorder()
	listARequest := httptest.NewRequest(http.MethodGet, "/v1/recordings?project_id=book-a", nil)
	listARequest.Header.Set("Authorization", "Bearer "+tokenA)
	server.Handler().ServeHTTP(listA, listARequest)
	if listA.Code != http.StatusOK {
		t.Fatalf("expected owner list to succeed, got %d: %s", listA.Code, listA.Body.String())
	}
	var listPayload struct {
		Recordings []storedRecording `json:"recordings"`
	}
	if err := json.NewDecoder(listA.Body).Decode(&listPayload); err != nil {
		t.Fatal(err)
	}
	if len(listPayload.Recordings) != 1 || listPayload.Recordings[0].Transcript != "我记得院子里的梧桐树。" {
		t.Fatalf("unexpected owner recording list: %+v", listPayload.Recordings)
	}

	foreignList := httptest.NewRecorder()
	foreignListRequest := httptest.NewRequest(http.MethodGet, "/v1/recordings?project_id=book-a", nil)
	foreignListRequest.Header.Set("Authorization", "Bearer "+tokenB)
	server.Handler().ServeHTTP(foreignList, foreignListRequest)
	if foreignList.Code != http.StatusOK || strings.Contains(foreignList.Body.String(), "梧桐树") {
		t.Fatalf("foreign user must not see owner recordings: %d %s", foreignList.Code, foreignList.Body.String())
	}

	foreignDownload := httptest.NewRecorder()
	foreignDownloadRequest := httptest.NewRequest(http.MethodGet, "/v1/recordings/recording-private-a/audio", nil)
	foreignDownloadRequest.Header.Set("Authorization", "Bearer "+tokenB)
	server.Handler().ServeHTTP(foreignDownload, foreignDownloadRequest)
	if foreignDownload.Code != http.StatusNotFound {
		t.Fatalf("expected cross-user audio download to be hidden, got %d", foreignDownload.Code)
	}

	ownerDownload := httptest.NewRecorder()
	ownerDownloadRequest := httptest.NewRequest(http.MethodGet, "/v1/recordings/recording-private-a/audio", nil)
	ownerDownloadRequest.Header.Set("Authorization", "Bearer "+tokenA)
	server.Handler().ServeHTTP(ownerDownload, ownerDownloadRequest)
	if ownerDownload.Code != http.StatusOK || !bytes.Equal(ownerDownload.Body.Bytes(), audio) {
		t.Fatalf("expected owner audio download to match upload, got %d %q", ownerDownload.Code, ownerDownload.Body.Bytes())
	}

	rename := httptest.NewRecorder()
	renameRequest := httptest.NewRequest(http.MethodPatch, "/v1/recordings/recording-private-a", strings.NewReader(`{"title":"院子里的梧桐树"}`))
	renameRequest.Header.Set("Authorization", "Bearer "+tokenA)
	renameRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(rename, renameRequest)
	if rename.Code != http.StatusOK || !strings.Contains(rename.Body.String(), "院子里的梧桐树") {
		t.Fatalf("expected recording rename to succeed, got %d: %s", rename.Code, rename.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/recordings/recording-private-a", nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+tokenA)
	deleteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected recording deletion to succeed, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestBiographyAuthFlowProtectsVoiceAndProgress(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC test key: %v", err)
	}
	provider := newBiographyOIDCTestProvider(t)
	provider.setKeys(biographyOIDCTestPublicJWK("bio-rsa", "RS256", &key.PublicKey))
	token := signedBiographyOIDCTestToken(t, key, "bio-rsa", "RS256", map[string]any{
		"iss":  provider.server.URL,
		"sub":  "oidc-user-1",
		"aud":  "biography-mobile",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"name": "王老师",
	})
	server, err := NewServer(Config{
		HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"},
		AuthMode: biographyAuthModeOIDC, AuthOIDCIssuer: provider.server.URL, AuthOIDCAudience: "biography-mobile",
		AuthOIDCHTTPTimeout: 2 * time.Second, DataDir: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized websocket, response=%v err=%v", response, err)
	}

	meRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	meRequest.Header.Set("Authorization", "Bearer "+token)
	meResponse, err := http.DefaultClient.Do(meRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer meResponse.Body.Close()
	if meResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected me status: %s", meResponse.Status)
	}
	var me struct {
		Authenticated bool       `json:"authenticated"`
		User          publicUser `json:"user"`
	}
	if err := json.NewDecoder(meResponse.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if !me.Authenticated || me.User.ID == "" || me.User.Subject != "oidc-user-1" || me.User.DisplayName != "王老师" {
		t.Fatalf("unexpected OIDC me response: %+v", me)
	}

	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session?access_token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-auth"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我想留给孩子看"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "我想留给孩子看")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "我想留给孩子看")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionFinish})
	assertServerMessageAllowing(t, ctx, connection, ServerSessionFinished, "", ServerProjectUpdated)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/v1/progress", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	progressResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer progressResponse.Body.Close()
	if progressResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected progress status: %s", progressResponse.Status)
	}
	var progress BiographyProgress
	if err := json.NewDecoder(progressResponse.Body).Decode(&progress); err != nil {
		t.Fatal(err)
	}
	if progress.LastInterview == nil || progress.LastInterview.TranscriptCount == 0 || progress.Project.ID == "" {
		t.Fatalf("progress was not isolated and persisted: %+v", progress)
	}
}

func TestBiographyOIDCProgressIsIsolatedBetweenUsers(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC test key: %v", err)
	}
	provider := newBiographyOIDCTestProvider(t)
	provider.setKeys(biographyOIDCTestPublicJWK("bio-rsa", "RS256", &key.PublicKey))
	tokenA := signedBiographyOIDCTestToken(t, key, "bio-rsa", "RS256", map[string]any{
		"iss": provider.server.URL, "sub": "user-a", "aud": "biography-mobile", "exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenB := signedBiographyOIDCTestToken(t, key, "bio-rsa", "RS256", map[string]any{
		"iss": provider.server.URL, "sub": "user-b", "aud": "biography-mobile", "exp": time.Now().Add(time.Hour).Unix(),
	})
	server, err := NewServer(Config{
		HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"},
		AuthMode: biographyAuthModeOIDC, AuthOIDCIssuer: provider.server.URL, AuthOIDCAudience: "biography-mobile",
		AuthOIDCHTTPTimeout: 2 * time.Second, DataDir: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session?access_token="+tokenA, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-user-a"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "这是 A 用户的私密内容"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "这是 A 用户的私密内容")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "这是 A 用户的私密内容")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionFinish})
	assertServerMessageAllowing(t, ctx, connection, ServerSessionFinished, "", ServerProjectUpdated)
	connection.CloseNow()

	progressA := getProgressWithToken(t, ctx, httpServer.URL, tokenA)
	if progressA.LastInterview == nil || progressA.LastInterview.TranscriptCount != 1 {
		t.Fatalf("expected user A progress to be saved: %+v", progressA)
	}
	progressB := getProgressWithToken(t, ctx, httpServer.URL, tokenB)
	if progressB.LastInterview != nil || progressB.Project.OverallProgress != 0 || progressB.Project.ID != "biography_new" {
		t.Fatalf("user B must not see user A progress: %+v", progressB)
	}
}

func TestBiographyOIDCRejectsConcurrentInterviewsForSameUser(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC test key: %v", err)
	}
	provider := newBiographyOIDCTestProvider(t)
	provider.setKeys(biographyOIDCTestPublicJWK("bio-rsa", "RS256", &key.PublicKey))
	token := signedBiographyOIDCTestToken(t, key, "bio-rsa", "RS256", map[string]any{
		"iss": provider.server.URL, "sub": "same-user", "aud": "biography-mobile", "exp": time.Now().Add(time.Hour).Unix(),
	})
	server, err := NewServer(Config{
		HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"},
		AuthMode: biographyAuthModeOIDC, AuthOIDCIssuer: provider.server.URL, AuthOIDCAudience: "biography-mobile",
		AuthOIDCHTTPTimeout: 2 * time.Second, DataDir: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/voice/session?access_token=" + token

	first, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, first, ClientMessage{Type: ClientSessionStart, SessionID: "voice-first"})
	assertServerMessage(t, ctx, first, ServerSessionReady, "")
	assertServerMessage(t, ctx, first, ServerInterviewProject, "")

	second, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, second, ClientMessage{Type: ClientSessionStart, SessionID: "voice-second"})
	busy := readServerTextMessage(t, ctx, second)
	if busy.Type != ServerError || busy.Code != "interview_busy" {
		t.Fatalf("expected same-user interview lease rejection, got %+v", busy)
	}
	second.CloseNow()

	writeClientMessage(t, ctx, first, ClientMessage{Type: ClientSessionFinish})
	assertServerMessage(t, ctx, first, ServerSessionFinished, "")
	first.CloseNow()

	user := &authenticatedUser{ID: stableUserID(provider.server.URL, "same-user")}
	deadline := time.Now().Add(time.Second)
	for !server.acquireInterviewLease(user, "voice-third") {
		if time.Now().After(deadline) {
			t.Fatal("interview lease was not released after ending the first session")
		}
		time.Sleep(5 * time.Millisecond)
	}
	server.releaseInterviewLease(user, "voice-third")
}

func TestVoiceSessionRequiresConfiguredClientToken(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, ClientToken: "required-token", AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized handshake, response=%v err=%v", response, err)
	}
}

func TestMockVoiceSessionCanCancelTTS(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-cancel"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSStart, Text: "这段话不应该播放完"})
	assertServerMessage(t, ctx, connection, ServerTTSStarted, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSCancel})
	assertServerMessage(t, ctx, connection, ServerTTSCanceled, "")
}

type blockingInterviewEngine struct {
	started  chan struct{}
	canceled chan struct{}
}

func (engine *blockingInterviewEngine) Continue(ctx context.Context, _ *interviewConversation, _ string) (InterviewReply, error) {
	select {
	case <-engine.started:
	default:
		close(engine.started)
	}
	<-ctx.Done()
	select {
	case <-engine.canceled:
	default:
		close(engine.canceled)
	}
	return InterviewReply{}, ctx.Err()
}

func (engine *blockingInterviewEngine) Organize(_ context.Context, conversation *interviewConversation, _ string) (BiographyProject, error) {
	return conversation.projectSnapshot(), nil
}

func (*blockingInterviewEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestInterviewTurnDoesNotBlockHeartbeatAndCanBeInterrupted(t *testing.T) {
	engine := &blockingInterviewEngine{started: make(chan struct{}), canceled: make(chan struct{})}
	server := &Server{
		config: Config{Provider: ProviderMock, InterviewFirstResponseTimeout: time.Second, InterviewTimeout: 2 * time.Second},
		logger: slog.Default(), interview: engine,
	}
	httpServer := httptest.NewServer(http.HandlerFunc(server.voiceSession))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-interrupt"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "这是一段会被打断的话"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "这是一段会被打断的话")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "这是一段会被打断的话")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	select {
	case <-engine.started:
	case <-ctx.Done():
		t.Fatal("interview turn did not start")
	}

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionPing})
	assertServerMessage(t, ctx, connection, ServerSessionPong, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientTTSCancel})
	assertTextEvents(t, ctx, connection, ServerInterviewCanceled, ServerTTSCanceled)
	select {
	case <-engine.canceled:
	case <-ctx.Done():
		t.Fatal("interview model context was not canceled")
	}
}

func TestInterviewFirstResponseTimeoutReturnsSpokenFallback(t *testing.T) {
	engine := &blockingInterviewEngine{started: make(chan struct{}), canceled: make(chan struct{})}
	server := &Server{
		config: Config{Provider: ProviderMock, InterviewFirstResponseTimeout: 100 * time.Millisecond, InterviewTimeout: time.Second},
		logger: slog.Default(), interview: engine,
	}
	httpServer := httptest.NewServer(http.HandlerFunc(server.voiceSession))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-timeout"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "模型故意不返回"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "模型故意不返回")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "模型故意不返回")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	select {
	case <-engine.started:
	case <-ctx.Done():
		t.Fatal("interview turn did not start")
	}
	assertServerMessageAllowing(t, ctx, connection, ServerInterviewReply, "我先接着问一个简单的。回到刚才那段经历里，您现在最清楚记得的一个画面是什么？", ServerProjectUpdated)
}

func TestPendingTranscriptPersistsUntilOrganizerConfirmsIt(t *testing.T) {
	store, err := newBiographyDataStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, logger: slog.Default()}
	conversation := &interviewConversation{UserID: "user-1", Project: newBiographyProject()}
	transcript := "我年轻时第一次离开家去上海学手艺。"
	conversation.addPendingTranscript(transcript)
	server.saveConversationProgress(conversation, activeProgressSession{ID: "voice-1", StartedAt: time.Now()}, nil)

	saved, found, err := store.progressForUser("user-1")
	if err != nil || !found || len(saved.PendingTranscripts) != 1 || saved.PendingTranscripts[0] != transcript {
		t.Fatalf("transcript was not durably saved before organization: found=%t progress=%+v err=%v", found, saved, err)
	}

	conversation.markTranscriptOrganized(transcript)
	server.saveConversationProgress(conversation, activeProgressSession{ID: "voice-1", StartedAt: time.Now()}, nil)
	saved, found, err = store.progressForUser("user-1")
	if err != nil || !found || len(saved.PendingTranscripts) != 0 {
		t.Fatalf("organized transcript should no longer be pending: found=%t progress=%+v err=%v", found, saved, err)
	}
}

func TestFinishedFallbackReturnsTranscriptWithReply(t *testing.T) {
	controller := newInterviewTurnController(t.Context(), &Server{}, &interviewConversation{Project: newBiographyProject()})
	controller.activeID = 1
	message := ServerMessage{Type: ServerInterviewReply, Text: "刚才连接有些慢。您刚才这段话已先保存，之后会继续整理。您可以接着讲。"}
	transcript, reply := controller.handle(interviewTurnEvent{
		id: 1, message: &message, transcript: "这段话必须先保存", done: true, accepted: true, failed: true,
	})
	if transcript != "这段话必须先保存" || reply == nil || reply.Text != message.Text {
		t.Fatalf("fallback must retain the transcript and reply together: transcript=%q reply=%+v", transcript, reply)
	}
}

func TestServerAcceptsConfiguredDoubaoProvider(t *testing.T) {
	server, err := NewServer(Config{
		HTTPAddr: ":0", Provider: ProviderDoubao, AllowedOrigins: []string{"127.0.0.1"},
		DoubaoAPIKey: "secret", DoubaoASRURL: "wss://speech.example/asr", DoubaoASRResourceID: "asr",
		DoubaoTTSURL: "wss://speech.example/tts", DoubaoTTSResourceID: "tts",
		DoubaoTTSSpeaker: "zh_female_example",
	}, nil)
	if err != nil || server == nil {
		t.Fatalf("expected configured provider to start, server=%v err=%v", server, err)
	}
}

func TestDoubaoVoiceSessionForwardsASRAndTTSAudio(t *testing.T) {
	asrConnection := newFakeDoubaoConnection()
	finalASR := mustDoubaoFrame(t, doubaoFrame{
		MessageType: doubaoMessageFullServer, Flags: doubaoFlagLastWithSequence,
		Serialization: doubaoSerializationJSON, HasSequence: true, Sequence: 2,
		Payload: []byte(`{"code":20000000,"result":{"text":"那年我十九岁","utterances":[{"definite":true}]}}`),
	})
	asrConnection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err == nil && frame.MessageType == doubaoMessageAudioClient && frame.Flags == doubaoFlagLastNoSequence {
			asrConnection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: finalASR}
		}
	}

	ttsConnection := newFakeDoubaoConnection()
	ttsConnection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err != nil {
			return
		}
		var responses []doubaoFrame
		switch frame.Event {
		case doubaoEventStartConnection:
			responses = append(responses, doubaoFrame{MessageType: doubaoMessageFullServer, HasEvent: true, Event: doubaoEventConnectionStarted, EventID: "connection-1", Payload: []byte(`{}`)})
		case doubaoEventStartSession:
			responses = append(responses, doubaoFrame{MessageType: doubaoMessageFullServer, HasEvent: true, Event: doubaoEventSessionStarted, EventID: frame.EventID, Payload: []byte(`{}`)})
		case doubaoEventFinishSession:
			responses = append(responses,
				doubaoFrame{MessageType: doubaoMessageAudioServer, HasEvent: true, Event: doubaoEventTTSResponse, EventID: frame.EventID, Payload: []byte{10, 20, 30}},
				doubaoFrame{MessageType: doubaoMessageFullServer, HasEvent: true, Event: doubaoEventSessionFinished, EventID: frame.EventID, Payload: []byte(`{}`)},
			)
		}
		for _, response := range responses {
			encoded, encodeErr := buildDoubaoFrame(response)
			if encodeErr == nil {
				ttsConnection.reads <- fakeDoubaoRead{messageType: websocket.MessageBinary, payload: encoded}
			}
		}
	}

	config := testDoubaoConfig()
	dialer := func(_ context.Context, target string, _ http.Header) (doubaoConnection, error) {
		if target == config.DoubaoASRURL {
			return asrConnection, nil
		}
		return ttsConnection, nil
	}
	server, err := newServer(config, nil, dialer)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-doubao"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	if err := connection.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "那年我十九岁")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	reply := readServerTextMessage(t, ctx, connection)
	if reply.Type != ServerInterviewReply || !reply.SpeechStarted {
		t.Fatalf("interview reply should start gateway TTS without a client round trip: %+v", reply)
	}
	assertDoubaoTurnEvents(t, ctx, connection, []byte{10, 20, 30})
}

func TestDoubaoVoiceSessionReturnsNoSpeechWithoutProviderFailure(t *testing.T) {
	asrConnection := newFakeDoubaoConnection()
	asrConnection.onWrite = func(payload []byte) {
		frame, err := parseDoubaoFrame(payload)
		if err == nil && frame.MessageType == doubaoMessageAudioClient && frame.Flags == doubaoFlagLastNoSequence {
			asrConnection.reads <- fakeDoubaoRead{err: errDoubaoASRNoTranscript}
		}
	}
	config := testDoubaoConfig()
	server, err := newServer(config, nil, func(context.Context, string, http.Header) (doubaoConnection, error) {
		return asrConnection, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-no-speech"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	if err := connection.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})

	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message ServerMessage
	if messageType != websocket.MessageText || json.Unmarshal(payload, &message) != nil {
		t.Fatalf("unexpected no-speech response: type=%v payload=%q", messageType, payload)
	}
	if message.Type != ServerError || message.Code != "no_speech" || !message.Retryable {
		t.Fatalf("unexpected no-speech message: %+v", message)
	}
}

type recordingInterviewEngine struct {
	resumedSessionID string
}

func (engine *recordingInterviewEngine) Continue(context.Context, *interviewConversation, string) (InterviewReply, error) {
	return InterviewReply{}, nil
}

func (engine *recordingInterviewEngine) Organize(_ context.Context, conversation *interviewConversation, _ string) (BiographyProject, error) {
	return conversation.projectSnapshot(), nil
}

func (engine *recordingInterviewEngine) Resume(_ context.Context, conversation *interviewConversation, sessionID string) error {
	engine.resumedSessionID = sessionID
	conversation.TMASessionID = sessionID
	conversation.Project = sampleBiographyProject()
	return nil
}

func TestServerPreparesEncryptedInterviewResume(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingInterviewEngine{}
	server := &Server{
		config:    Config{InterviewProvider: ProviderTMA, InterviewTimeout: time.Second},
		interview: engine, resumeTokens: codec,
	}
	token, err := codec.Encode("session-resume", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &interviewConversation{Project: newBiographyProject()}
	err = server.prepareInterviewConversation(t.Context(), conversation, nil, ClientMessage{
		ClientInstanceID: "device-1", ResumeToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.resumedSessionID != "session-resume" || conversation.ClientInstanceID != "device-1" || conversation.Project.OverallProgress != 32 {
		t.Fatalf("unexpected resumed conversation: engine=%+v conversation=%+v", engine, conversation)
	}
	if err := server.prepareInterviewConversation(t.Context(), &interviewConversation{}, nil, ClientMessage{
		ClientInstanceID: "device-2", ResumeToken: token,
	}); err == nil {
		t.Fatal("expected cross-device resume rejection")
	}
}

func TestServerRestoresProjectFromAsyncUpdateToken(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	asyncProject := sampleBiographyProject()
	asyncProject.OverallProgress = 77
	token, err := codec.EncodeState("session-resume", "device-1", "user-1", &asyncProject)
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingInterviewEngine{}
	server := &Server{
		config:    Config{InterviewProvider: ProviderTMA, InterviewTimeout: time.Second},
		interview: engine, resumeTokens: codec,
	}
	conversation := &interviewConversation{Project: newBiographyProject()}
	if err := server.prepareInterviewConversation(t.Context(), conversation, &authenticatedUser{ID: "user-1"}, ClientMessage{
		ClientInstanceID: "device-1", ResumeToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if conversation.projectSnapshot().OverallProgress != 77 {
		t.Fatalf("async project snapshot was not restored: %+v", conversation.projectSnapshot())
	}
	if err := server.prepareInterviewConversation(t.Context(), &interviewConversation{Project: newBiographyProject()}, &authenticatedUser{ID: "user-2"}, ClientMessage{
		ClientInstanceID: "device-1", ResumeToken: token,
	}); err == nil {
		t.Fatal("expected cross-user resume rejection")
	}
}

type blockingOrganizerEngine struct {
	organizeStarted chan struct{}
	releaseOrganize chan struct{}
}

func (engine *blockingOrganizerEngine) Continue(_ context.Context, conversation *interviewConversation, _ string) (InterviewReply, error) {
	project := conversation.projectSnapshot()
	return InterviewReply{Text: "您第一次到上海时，最先看到的是什么？", Expression: "温和", Project: project}, nil
}

func (engine *blockingOrganizerEngine) Organize(ctx context.Context, conversation *interviewConversation, _ string) (BiographyProject, error) {
	close(engine.organizeStarted)
	select {
	case <-engine.releaseOrganize:
		project := conversation.projectSnapshot()
		project.OverallProgress++
		conversation.replaceProject(project)
		return project, nil
	case <-ctx.Done():
		return BiographyProject{}, ctx.Err()
	}
}

func (*blockingOrganizerEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestVoiceSessionSendsLiveReplyBeforeOrganizerFinishes(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := &blockingOrganizerEngine{organizeStarted: make(chan struct{}), releaseOrganize: make(chan struct{})}
	server.interview = engine
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-async"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "我十九岁去了上海"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	assertServerMessage(t, ctx, connection, ServerInterviewReply, "")

	select {
	case <-engine.organizeStarted:
	case <-ctx.Done():
		t.Fatal("organizer did not start")
	}
	close(engine.releaseOrganize)
	assertServerMessage(t, ctx, connection, ServerProjectUpdated, "")
}

type retryInterviewEngine struct {
	organizeCalls chan string
}

func (*retryInterviewEngine) Continue(_ context.Context, conversation *interviewConversation, _ string) (InterviewReply, error) {
	return InterviewReply{
		Text:       "刚才有几处我没有完全听清。请尽量用普通话，稍微慢一点，可以分两三句再说一遍。",
		Expression: "温和、耐心，语速稍慢",
		NeedsRetry: true,
		Project:    conversation.projectSnapshot(),
	}, nil
}

func (engine *retryInterviewEngine) Organize(_ context.Context, _ *interviewConversation, transcript string) (BiographyProject, error) {
	select {
	case engine.organizeCalls <- transcript:
	default:
	}
	return newBiographyProject(), nil
}

func (*retryInterviewEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestVoiceSessionDoesNotOrganizeTranscriptWhenInterviewRequestsRetry(t *testing.T) {
	server, err := NewServer(Config{HTTPAddr: ":0", Provider: ProviderMock, AllowedOrigins: []string{"127.0.0.1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := &retryInterviewEngine{organizeCalls: make(chan string, 1)}
	server.interview = engine
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/voice/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientSessionStart, SessionID: "voice-retry"})
	assertServerMessage(t, ctx, connection, ServerSessionReady, "")
	assertServerMessage(t, ctx, connection, ServerInterviewProject, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientASRDebugText, Text: "字序不清楚的转写"})
	assertServerMessage(t, ctx, connection, ServerASRPartial, "")
	writeClientMessage(t, ctx, connection, ClientMessage{Type: ClientInputCommit})
	assertServerMessage(t, ctx, connection, ServerASRFinal, "")
	assertServerMessage(t, ctx, connection, ServerInterviewDelta, "我听到了，正在想接下来问什么。")
	reply := readServerTextMessage(t, ctx, connection)
	if reply.Type != ServerInterviewReply || !reply.NeedsRetry {
		t.Fatalf("expected a retry reply, got %+v", reply)
	}

	select {
	case transcript := <-engine.organizeCalls:
		t.Fatalf("retry transcript must not reach the organizer: %q", transcript)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestChapterConfirmationRequiresExplicitVoiceReply(t *testing.T) {
	project := sampleBiographyProject()
	project.Chapters[0].Status = "confirm"
	project.Chapters[0].StatusLabel = "待您确认"
	project.Chapters[0].Progress = 88
	project.PendingConfirmation = "这一段我整理成这样：第一次离开家去上海。这样对吗？"
	project.PendingConfirmationChapterID = "shanghai"
	conversation := &interviewConversation{Project: project}
	server := &Server{}

	message, handled := server.applyChapterConfirmation(conversation, "对")
	if !handled || message == nil || message.Project == nil || message.Project.Chapters[0].Status != "completed" || message.Project.CompletedChapterCount != 1 || message.Project.PendingConfirmation != "" {
		t.Fatalf("confirmation did not complete the chapter: handled=%t message=%+v", handled, message)
	}

	project = sampleBiographyProject()
	project.Chapters[0].Status = "confirm"
	project.PendingConfirmation = "这一段我整理成这样：第一次离开家去上海。这样对吗？"
	project.PendingConfirmationChapterID = "shanghai"
	conversation = &interviewConversation{Project: project}
	message, handled = server.applyChapterConfirmation(conversation, "改一下")
	if !handled || message == nil || message.Project == nil || message.Project.Chapters[0].Status != "collecting" || conversation.FocusedChapterID != "shanghai" {
		t.Fatalf("revision did not reopen the chapter: handled=%t message=%+v focus=%q", handled, message, conversation.FocusedChapterID)
	}
}

type orderedOrganizerEngine struct {
	mu        sync.Mutex
	started   []string
	active    int
	maxActive int
	release   chan struct{}
}

func (*orderedOrganizerEngine) Continue(context.Context, *interviewConversation, string) (InterviewReply, error) {
	return InterviewReply{}, nil
}

func (engine *orderedOrganizerEngine) Organize(ctx context.Context, conversation *interviewConversation, transcript string) (BiographyProject, error) {
	engine.mu.Lock()
	engine.started = append(engine.started, transcript)
	engine.active++
	if engine.active > engine.maxActive {
		engine.maxActive = engine.active
	}
	call := len(engine.started)
	engine.mu.Unlock()
	if call == 1 {
		select {
		case <-engine.release:
		case <-ctx.Done():
			return BiographyProject{}, ctx.Err()
		}
	}
	engine.mu.Lock()
	engine.active--
	engine.mu.Unlock()
	project := conversation.projectSnapshot()
	project.OverallProgress = call
	conversation.replaceProject(project)
	return project, nil
}

func (*orderedOrganizerEngine) Resume(context.Context, *interviewConversation, string) error {
	return nil
}

func TestProjectUpdateWorkerRunsTasksInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine := &orderedOrganizerEngine{release: make(chan struct{})}
	server := &Server{config: Config{InterviewTimeout: time.Second}, interview: engine}
	tasks, results := server.startProjectUpdateWorker(ctx, &interviewConversation{Project: sampleBiographyProject()})
	if err := enqueueProjectUpdate(ctx, tasks, "第一段"); err != nil {
		t.Fatal(err)
	}
	if err := enqueueProjectUpdate(ctx, tasks, "第二段"); err != nil {
		t.Fatal(err)
	}
	close(engine.release)
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatal(result.err)
			}
		case <-ctx.Done():
			t.Fatal("project update worker did not finish")
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if strings.Join(engine.started, ",") != "第一段,第二段" || engine.maxActive != 1 {
		t.Fatalf("organizer was not sequential: started=%v max_active=%d", engine.started, engine.maxActive)
	}
}

func writeClientMessage(t *testing.T, ctx context.Context, connection *websocket.Conn, message ClientMessage) {
	t.Helper()
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func assertServerMessage(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedType string, expectedText string) {
	t.Helper()
	message := readServerTextMessage(t, ctx, connection)
	if message.Type != expectedType || (expectedText != "" && message.Text != expectedText) {
		t.Fatalf("unexpected server message: %+v", message)
	}
}

func assertServerMessageAllowing(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedType string, expectedText string, allowedTypes ...string) {
	t.Helper()
	allowed := make(map[string]bool, len(allowedTypes))
	for _, allowedType := range allowedTypes {
		allowed[allowedType] = true
	}
	for {
		message := readServerTextMessage(t, ctx, connection)
		if message.Type == expectedType && (expectedText == "" || message.Text == expectedText) {
			return
		}
		if !allowed[message.Type] {
			t.Fatalf("unexpected server message: %+v", message)
		}
	}
}

func readServerTextMessage(t *testing.T, ctx context.Context, connection *websocket.Conn) ServerMessage {
	t.Helper()
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("expected text message, got %v", messageType)
	}
	var message ServerMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func assertTextEvents(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedTypes ...string) {
	t.Helper()
	pending := make(map[string]bool, len(expectedTypes))
	for _, expectedType := range expectedTypes {
		pending[expectedType] = true
	}
	for len(pending) > 0 {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if messageType != websocket.MessageText {
			t.Fatalf("expected text message, got %v", messageType)
		}
		var message ServerMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if !pending[message.Type] {
			t.Fatalf("unexpected server message while waiting for %v: %+v", pending, message)
		}
		delete(pending, message.Type)
	}
}

func assertDoubaoTurnEvents(t *testing.T, ctx context.Context, connection *websocket.Conn, expectedAudio []byte) {
	t.Helper()
	pending := map[string]bool{ServerProjectUpdated: true, ServerChapterConfirmation: true, ServerTTSStarted: true, ServerTTSFinished: true}
	audioReceived := false
	for len(pending) > 0 || !audioReceived {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if messageType == websocket.MessageBinary {
			if string(payload) != string(expectedAudio) || audioReceived {
				t.Fatalf("unexpected TTS audio frame: %v", payload)
			}
			audioReceived = true
			continue
		}
		var message ServerMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Fatal(err)
		}
		if !pending[message.Type] {
			t.Fatalf("unexpected server message while waiting for %v: %+v", pending, message)
		}
		delete(pending, message.Type)
	}
}

func getProgressWithToken(t *testing.T, ctx context.Context, baseURL string, token string) BiographyProgress {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/progress", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected progress status: %s", response.Status)
	}
	var progress BiographyProgress
	if err := json.NewDecoder(response.Body).Decode(&progress); err != nil {
		t.Fatal(err)
	}
	return progress
}

type biographyOIDCTestProvider struct {
	server *httptest.Server

	mu   sync.RWMutex
	keys []jose.JSONWebKey
}

func newBiographyOIDCTestProvider(t *testing.T) *biographyOIDCTestProvider {
	t.Helper()
	provider := &biographyOIDCTestProvider{}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, http.StatusOK, map[string]any{
				"issuer":                 provider.server.URL,
				"jwks_uri":               provider.server.URL + "/jwks",
				"authorization_endpoint": provider.server.URL + "/authorize",
				"token_endpoint":         provider.server.URL + "/token",
			})
		case "/jwks":
			provider.mu.RLock()
			keys := append([]jose.JSONWebKey(nil), provider.keys...)
			provider.mu.RUnlock()
			writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *biographyOIDCTestProvider) setKeys(keys ...jose.JSONWebKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append([]jose.JSONWebKey(nil), keys...)
}

func biographyOIDCTestPublicJWK(kid string, algorithm string, key any) jose.JSONWebKey {
	return jose.JSONWebKey{Key: key, KeyID: kid, Algorithm: algorithm, Use: "sig"}
}

func signedBiographyOIDCTestToken(t *testing.T, key any, kid string, algorithm string, claims map[string]any) string {
	t.Helper()
	options := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.SignatureAlgorithm(algorithm), Key: key}, options)
	if err != nil {
		t.Fatalf("create %s signer: %v", algorithm, err)
	}
	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign %s token: %v", algorithm, err)
	}
	return token
}
