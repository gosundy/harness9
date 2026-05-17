package memory

import (
	"context"
	"time"

	"github.com/harness9/internal/schema"
)

// Session 管理单个会话的消息历史。
// 接口定义在 memory 包（使用者侧），由 MemorySession / SQLiteSession 实现。
type Session interface {
	SessionID() string
	GetMessages(ctx context.Context, limit int) ([]schema.Message, error)
	AddMessages(ctx context.Context, msgs []schema.Message) error
	PopMessage(ctx context.Context) (*schema.Message, error)
	Clear(ctx context.Context) error
}

// SessionInfo 是 Manager.ListSessions 返回的会话元数据。
type SessionInfo struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	MsgCount  int
}
