package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

const DefaultMockTurnDelay = 750 * time.Millisecond

// MockRunner 只模拟异步执行生命周期；它是 Runner 层的默认开发实现。
// 后续接入真实执行器时，HTTP 层不需要再理解 mock 细节。
type MockRunner struct {
	store  managedagents.Store
	delay  time.Duration
	logger *slog.Logger

	mu    sync.Mutex
	turns map[turnKey]context.CancelFunc
}

func NewMockRunner(store managedagents.Store, delay time.Duration, logger *slog.Logger) *MockRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &MockRunner{
		store:  store,
		delay:  delay,
		logger: logger,
		turns:  make(map[turnKey]context.CancelFunc),
	}
}

func (r *MockRunner) StartTurn(_ context.Context, request TurnRequest) error {
	// 复制 payload，避免调用方后续复用底层 buffer 影响后台 goroutine。
	payload := append(json.RawMessage(nil), request.UserPayload...)

	// 用 session + turn 唯一标识这一轮执行；InterruptTurn 靠同一个 key 找到 cancel。
	key := turnKey{sessionID: request.SessionID, turnID: request.TurnID}

	// ctx 给后台 goroutine 监听取消；cancel 存入 turns，供 InterruptTurn 调用。
	ctx, cancel := context.WithCancel(context.Background())

	// 登记「进行中的 turn」：同一 key 不能重复启动，否则返回 ErrTurnAlreadyRunning。
	r.mu.Lock()
	if _, exists := r.turns[key]; exists {
		r.mu.Unlock()
		cancel() // 本轮未启动，立刻释放 context，避免泄漏。
		return ErrTurnAlreadyRunning
	}
	r.turns[key] = cancel
	r.mu.Unlock()

	r.logger.Info("mock runner turn scheduled",
		"session_id", request.SessionID,
		"turn_id", request.TurnID,
		"delay_ms", r.delay.Milliseconds(),
	)

	// 启动 goroutine 模拟异步执行；本函数立即 return，HTTP 不必等待 delay。
	go func() {
		// goroutine 退出时（正常完成或被 cancel）从 turns 清掉登记。
		defer r.deleteTurn(key)

		timer := time.NewTimer(r.delay)
		defer timer.Stop()

		// 同时等两件事，谁先发生走谁的分支：
		//   timer.C      → 模拟执行时间到，继续往下 CompleteSessionTurn
		//   ctx.Done()   → InterruptTurn 调了 cancel()，提前 return，不写 agent 回复
		select {
		case <-timer.C:
		case <-ctx.Done():
			r.logger.Info("mock runner turn canceled",
				"session_id", request.SessionID,
				"turn_id", request.TurnID,
			)
			return
		}

		// 写入 agent.message + session.status_idle。
		// Store 会校验 turn 是否仍为 running；若已被 interrupt，返回空 events 而非报错。
		databaseCtx, err := databaseContextForTurn(ctx, request)
		if err != nil {
			r.logger.Error("mock runner completion scope failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", err)
			return
		}
		events, err := managedagents.CompleteSessionTurnWithContext(databaseCtx, r.store, request.SessionID, request.TurnID, mockAgentMessagePayload(payload))
		if err != nil {
			if errors.Is(err, managedagents.ErrNotFound) || errors.Is(err, managedagents.ErrTerminated) {
				r.logger.Info("mock runner completion skipped",
					"session_id", request.SessionID,
					"turn_id", request.TurnID,
					"reason", err.Error(),
				)
				return
			}
			r.logger.Error("mock runner completion failed",
				"session_id", request.SessionID,
				"turn_id", request.TurnID,
				"error", err,
			)
			return
		}
		if len(events) == 0 {
			// turn 已不是 running（例如 interrupt 抢先改了 Store 状态），跳过补消息。
			r.logger.Info("mock runner completion skipped",
				"session_id", request.SessionID,
				"turn_id", request.TurnID,
				"reason", "turn_not_running",
			)
			return
		}
		r.logger.Info("mock runner turn completed",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"events", len(events),
		)
		for _, event := range events {
			r.logger.Info("mock runner event appended",
				"event_id", event.ID,
				"session_id", event.SessionID,
				"turn_id", payloadString(event.Payload, "turn_id"),
				"event_seq", event.Seq,
				"event_type", event.Type,
			)
		}
	}()

	// 只表示「后台任务已调度」，不代表 turn 已完成。
	return nil
}

func (r *MockRunner) InterruptTurn(_ context.Context, request InterruptRequest) error {
	key := turnKey{sessionID: request.SessionID, turnID: request.TurnID}
	cancel := r.takeTurn(key)
	if cancel == nil {
		r.logger.Info("mock runner interrupt ignored",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"reason", "turn_not_active",
		)
		return nil
	}
	cancel()
	r.logger.Info("mock runner interrupt received",
		"session_id", request.SessionID,
		"turn_id", request.TurnID,
	)
	return nil
}

func (r *MockRunner) takeTurn(key turnKey) context.CancelFunc {
	r.mu.Lock()
	defer r.mu.Unlock()

	cancel := r.turns[key]
	delete(r.turns, key)
	return cancel
}

func (r *MockRunner) deleteTurn(key turnKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.turns, key)
}

type turnKey struct {
	sessionID string
	turnID    string
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}

	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}

func mockAgentMessagePayload(userPayload json.RawMessage) json.RawMessage {
	text := "Mock Agent received your message."
	turnID := payloadString(userPayload, "turn_id")

	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(userPayload, &payload); err == nil {
		for _, content := range payload.Content {
			if content.Type == "text" && content.Text != "" {
				text = "Mock Agent received: " + content.Text
				break
			}
		}
	}

	response := map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
	}
	if turnID != "" {
		response["turn_id"] = turnID
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		return json.RawMessage(`{"content":[{"type":"text","text":"Mock Agent received your message."}]}`)
	}
	return encoded
}
