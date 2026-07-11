package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
)

const (
	CapabilityOpen     = "browser.open"
	CapabilityRead     = "browser.read"
	CapabilityInteract = "browser.interact"
	CapabilityCapture  = "browser.capture"
	CapabilityTakeover = "browser.takeover"
	CapabilityClose    = "browser.close"
	CapabilityDownload = "browser.download"
	CapabilityUpload   = "browser.upload"
	CapabilityNetwork  = "browser.network"

	DefaultTimeoutMS           = 15000
	DefaultTakeoverWaitSeconds = 300
	MaxTakeoverWaitSeconds     = 3600
)

type Provider interface {
	Open(context.Context, OpenRequest) (PageState, error)
	Read(context.Context, ReadRequest) (PageState, error)
	Click(context.Context, ClickRequest) (PageState, error)
	Type(context.Context, TypeRequest) (PageState, error)
	Screenshot(context.Context, ScreenshotRequest) (PageState, error)
	Takeover(context.Context, TakeoverRequest) (PageState, error)
	Close(context.Context, CloseRequest) (PageState, error)
}

type BaseRequest struct {
	Meta             capability.RequestMeta `json:"meta"`
	BrowserSessionID string                 `json:"browser_session_id,omitempty"`
	URL              string                 `json:"url,omitempty"`
	TimeoutMS        int                    `json:"timeout_ms,omitempty"`
	Viewport         Viewport               `json:"viewport,omitempty"`
	UserAgent        string                 `json:"user_agent,omitempty"`
}

type OpenRequest struct {
	BaseRequest
}

type ReadRequest struct {
	BaseRequest
}

type ClickRequest struct {
	BaseRequest
	Selector string `json:"selector,omitempty"`
	Ref      string `json:"ref,omitempty"`
}

type TypeRequest struct {
	BaseRequest
	Selector string `json:"selector,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Text     string `json:"text"`
	Clear    bool   `json:"clear,omitempty"`
}

type ScreenshotRequest struct {
	BaseRequest
	FullPage bool `json:"full_page,omitempty"`
}

type TakeoverRequest struct {
	BaseRequest
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

type CloseRequest struct {
	BaseRequest
}

type Viewport struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type Element struct {
	Ref      string `json:"ref"`
	Role     string `json:"role,omitempty"`
	Text     string `json:"text,omitempty"`
	Selector string `json:"selector,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

type PageState struct {
	BrowserSessionID string    `json:"browser_session_id,omitempty"`
	URL              string    `json:"url,omitempty"`
	Title            string    `json:"title,omitempty"`
	Text             string    `json:"text,omitempty"`
	Elements         []Element `json:"elements,omitempty"`
	ScreenshotPath   string    `json:"screenshot_path,omitempty"`
	Persistent       bool      `json:"persistent,omitempty"`
	Error            string    `json:"error,omitempty"`
}

type CommandProvider struct {
	Runner     capability.Provider
	Command    string
	WorkDir    string
	Env        map[string]string
	Headless   bool
	Persistent bool
	StateRoot  string
}

func NewCommandProvider(runner capability.Provider) CommandProvider {
	return CommandProvider{Runner: runner, Headless: true}
}

func BrowserCapabilities() []string {
	return []string{
		CapabilityOpen,
		CapabilityRead,
		CapabilityInteract,
		CapabilityCapture,
		CapabilityTakeover,
		CapabilityClose,
	}
}

func (p CommandProvider) Open(ctx context.Context, request OpenRequest) (PageState, error) {
	return p.run(ctx, "open", commandRequest{
		BaseRequest: request.BaseRequest,
	})
}

func (p CommandProvider) Read(ctx context.Context, request ReadRequest) (PageState, error) {
	return p.run(ctx, "read", commandRequest{
		BaseRequest: request.BaseRequest,
	})
}

func (p CommandProvider) Click(ctx context.Context, request ClickRequest) (PageState, error) {
	return p.run(ctx, "click", commandRequest{
		BaseRequest: request.BaseRequest,
		Selector:    request.Selector,
		Ref:         request.Ref,
	})
}

func (p CommandProvider) Type(ctx context.Context, request TypeRequest) (PageState, error) {
	return p.run(ctx, "type", commandRequest{
		BaseRequest: request.BaseRequest,
		Selector:    request.Selector,
		Ref:         request.Ref,
		Text:        request.Text,
		Clear:       request.Clear,
	})
}

func (p CommandProvider) Screenshot(ctx context.Context, request ScreenshotRequest) (PageState, error) {
	payload := commandRequest{
		BaseRequest:    request.BaseRequest,
		FullPage:       request.FullPage,
		ScreenshotPath: p.screenshotPath(request.BaseRequest),
	}
	return p.run(ctx, "screenshot", payload)
}

func (p CommandProvider) Takeover(ctx context.Context, request TakeoverRequest) (PageState, error) {
	return p.run(ctx, "takeover", commandRequest{
		BaseRequest: request.BaseRequest,
		WaitSeconds: normalizeTakeoverWaitSeconds(request.WaitSeconds),
	})
}

func (p CommandProvider) Close(ctx context.Context, request CloseRequest) (PageState, error) {
	return p.run(ctx, "close", commandRequest{
		BaseRequest: request.BaseRequest,
	})
}

type commandRequest struct {
	BaseRequest
	Action         string `json:"action"`
	Selector       string `json:"selector,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Text           string `json:"text,omitempty"`
	Clear          bool   `json:"clear,omitempty"`
	FullPage       bool   `json:"full_page,omitempty"`
	StatePath      string `json:"state_path,omitempty"`
	ScreenshotPath string `json:"screenshot_path,omitempty"`
	WaitSeconds    int    `json:"wait_seconds,omitempty"`
	Persistent     bool   `json:"persistent,omitempty"`
}

func (p CommandProvider) run(ctx context.Context, action string, request commandRequest) (PageState, error) {
	runner := p.Runner
	if runner == nil {
		runner = capability.LocalSystemProvider{}
	}
	request.Action = action
	request.BrowserSessionID = defaultSessionID(request.BrowserSessionID, request.Meta.SessionID)
	if request.TimeoutMS <= 0 {
		request.TimeoutMS = DefaultTimeoutMS
	}
	request.StatePath = p.statePath(request.BaseRequest)
	request.Persistent = p.persistent(runner)
	input, err := json.Marshal(request)
	if err != nil {
		return PageState{}, err
	}
	commandResult, err := runner.RunCommand(ctx, capability.RunCommandRequest{
		Meta:    request.Meta,
		Command: p.command(),
		Args:    []string{"-e", playwrightRunnerScript},
		WorkDir: p.WorkDir,
		Env:     p.env(action),
		Stdin:   input,
	})
	if err != nil {
		return PageState{}, err
	}
	if commandResult.ExitCode != 0 {
		return PageState{}, fmt.Errorf("browser runner exited with code %d: %s", commandResult.ExitCode, strings.TrimSpace(commandResult.Stderr))
	}
	var state PageState
	if err := json.Unmarshal([]byte(commandResult.Stdout), &state); err != nil {
		return PageState{}, fmt.Errorf("decode browser runner output: %w; stdout=%q stderr=%q", err, commandResult.Stdout, commandResult.Stderr)
	}
	if state.BrowserSessionID == "" {
		state.BrowserSessionID = request.BrowserSessionID
	}
	if state.Error != "" {
		return state, errors.New(state.Error)
	}
	return state, nil
}

func (p CommandProvider) persistent(runner capability.Provider) bool {
	if p.Persistent {
		return true
	}
	if value, ok := p.Env["TMA_BROWSER_PERSISTENT"]; ok {
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.TrimSpace(value) == "1"
	}
	_, ok := runner.(capability.LocalSystemProvider)
	return ok
}

func (p CommandProvider) command() string {
	if strings.TrimSpace(p.Command) != "" {
		return strings.TrimSpace(p.Command)
	}
	return "node"
}

func (p CommandProvider) env(action string) map[string]string {
	env := map[string]string{
		"TMA_BROWSER_HEADLESS": "true",
	}
	if !p.Headless {
		env["TMA_BROWSER_HEADLESS"] = "false"
	}
	if action == "takeover" {
		env["TMA_BROWSER_HEADLESS"] = "false"
	}
	for key, value := range p.Env {
		env[key] = value
	}
	return env
}

func (p CommandProvider) statePath(request BaseRequest) string {
	root := strings.TrimSpace(p.StateRoot)
	if root == "" {
		root = defaultStateRoot()
	}
	sessionID := safePathPart(defaultSessionID(request.BrowserSessionID, request.Meta.SessionID))
	if strings.HasPrefix(root, "/mnt/data") {
		return filepath.ToSlash(filepath.Join(root, sessionID, "state.json"))
	}
	return filepath.Join(root, sessionID, "state.json")
}

func (p CommandProvider) screenshotPath(request BaseRequest) string {
	root := strings.TrimSpace(p.StateRoot)
	if root == "" {
		root = defaultStateRoot()
	}
	sessionID := safePathPart(defaultSessionID(request.BrowserSessionID, request.Meta.SessionID))
	name := fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano())
	if strings.HasPrefix(root, "/mnt/data") {
		return filepath.ToSlash(filepath.Join(root, sessionID, name))
	}
	return filepath.Join(root, sessionID, name)
}

func defaultStateRoot() string {
	if strings.TrimSpace(os.Getenv("TMA_BROWSER_STATE_ROOT")) != "" {
		return strings.TrimSpace(os.Getenv("TMA_BROWSER_STATE_ROOT"))
	}
	return filepath.Join(os.TempDir(), "tma-browser")
}

func normalizeTakeoverWaitSeconds(value int) int {
	if value <= 0 {
		return DefaultTakeoverWaitSeconds
	}
	if value > MaxTakeoverWaitSeconds {
		return MaxTakeoverWaitSeconds
	}
	return value
}

func defaultSessionID(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "anonymous"
}

func safePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "anonymous"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return replacer.Replace(value)
}
