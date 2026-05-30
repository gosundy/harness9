// Package hooks — 子代理进度回调（与 ApprovalFunc 对称的 ctx 注入机制）。
package hooks

import (
	"context"

	"github.com/harness9/internal/schema"
)

// SubAgentProgressFunc 是子代理进度透传回调，由引擎（RunStream）注入 context，
// 子代理 Runner 从 context 提取并在消费子引擎事件流时调用，实现父 TUI 实时渲染。
type SubAgentProgressFunc func(schema.SubAgentUpdate)

type subAgentProgressKey struct{}

// WithSubAgentProgress 将 SubAgentProgressFunc 注入 context。
func WithSubAgentProgress(ctx context.Context, fn SubAgentProgressFunc) context.Context {
	return context.WithValue(ctx, subAgentProgressKey{}, fn)
}

// SubAgentProgressFromContext 从 context 提取 SubAgentProgressFunc，未设置时返回 nil。
func SubAgentProgressFromContext(ctx context.Context) SubAgentProgressFunc {
	fn, _ := ctx.Value(subAgentProgressKey{}).(SubAgentProgressFunc)
	return fn
}
