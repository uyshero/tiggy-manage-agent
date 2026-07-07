package runner

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrTurnAlreadyRunning = errors.New("turn already running")

// TurnRequest 是 HTTP / Store 层提交给 Runner 的一次执行请求。
// HTTP 请求结束不代表 turn 结束，真实 Runner 后续应使用自己的生命周期管理。
type TurnRequest struct {
	SessionID    string
	TurnID       string
	UserEventSeq int64
	UserPayload  json.RawMessage
}

// InterruptRequest 表示对某次 running turn 的中断请求。
// 当前 Store 已经负责写入中断事件，Runner 只负责把中断信号传给执行侧。
type InterruptRequest struct {
	SessionID string
	TurnID    string
}

// Runner 抽象一条用户消息背后的执行生命周期。
// 具体执行策略由 WorkerRunner 搭配 TurnExecutor 承担。
type Runner interface {
	StartTurn(ctx context.Context, request TurnRequest) error
	InterruptTurn(ctx context.Context, request InterruptRequest) error
}
