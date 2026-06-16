// Package engine — 手动上下文压缩支持。
//
// Compact 方法允许调用方（如 TUI /compact 命令）在不触发 LLM 推理循环的情况下，
// 对当前 session 的历史消息执行一次强制压缩，跳过常规的 80% 阈值检查。
package engine

import (
	"context"
	"fmt"
	"log"

	"github.com/harness9/internal/logfmt"
	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/schema"
)

// Compact 对当前 session 的历史消息执行一次强制压缩，跳过 80% 阈值检查。
//
// 行为说明：
//   - compactor 为 nil 时：立即返回零值 CompactionData 和 nil error（no-op）
//   - session 为 nil 时：立即返回零值 CompactionData 和 nil error（no-op）
//   - 否则：读取完整历史 → 注入 system prompt（与 runLoop 路径一致）→ 调用 ForceCompact →
//     剥离 system prompt → 写回 session
//
// 注意：system prompt 不持久化到 DB（与 loadHistoryWith 一致），Compact 在调用
// compactor 前注入 system prompt，写回时再剥离，保证 compactor 的 msgs[0].Role==System 前提。
//
// 返回的 CompactionData 包含压缩前后的 token 数和消息条数（不含 system prompt），
// 供 TUI 在对话流中展示压缩通知消息。
//
// 线程安全：通过读锁快照 session 和 compactor，与 TUI goroutine 并发安全。
func (e *AgentEngine) Compact(ctx context.Context) (CompactionData, error) {
	e.mu.RLock()
	sess := e.session
	comp := e.compactor
	e.mu.RUnlock()

	if comp == nil || sess == nil {
		return CompactionData{}, nil
	}

	// 从 DB 加载历史（不含 system prompt）
	msgs, err := sess.GetMessages(ctx, 0)
	if err != nil {
		return CompactionData{}, fmt.Errorf("compact: load history: %w", err)
	}

	if len(msgs) == 0 {
		return CompactionData{}, nil
	}

	tokensBefore := memory.EstimateTokens(msgs)
	msgsBefore := len(msgs)

	// system prompt 不存 DB，在调用 compactor 前动态注入（与 loadHistoryWith 一致），
	// 确保 compactor 的前置条件 msgs[0].Role == RoleSystem 成立。
	// 使用独立分配的切片，避免与 msgs 底层数组产生别名。
	withSystem := make([]schema.Message, 0, len(msgs)+1)
	withSystem = append(withSystem, schema.Message{Role: schema.RoleSystem, Content: e.buildSystemPrompt()})
	withSystem = append(withSystem, msgs...)

	var compactedWithSystem []schema.Message
	if fc, ok := comp.(memory.ForceCompactor); ok {
		compactedWithSystem = fc.CompactForce(withSystem)
	} else {
		compactedWithSystem = comp.Compact(withSystem)
	}

	// 剥离 system prompt（不持久化），得到写回 DB 的消息列表。
	compacted := compactedWithSystem
	if len(compacted) > 0 && compacted[0].Role == schema.RoleSystem {
		compacted = compacted[1:]
	}

	tokensAfter := memory.EstimateTokens(compacted)
	msgsAfter := len(compacted)

	// 将压缩后的历史写回 session：先清空再批量写入。
	if err := sess.Clear(ctx); err != nil {
		return CompactionData{}, fmt.Errorf("compact: clear session: %w", err)
	}
	if err := sess.AddMessages(ctx, compacted); err != nil {
		return CompactionData{}, fmt.Errorf("compact: write compacted messages: %w", err)
	}

	data := CompactionData{
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
		MsgsBefore:   msgsBefore,
		MsgsAfter:    msgsAfter,
	}

	log.Print(logfmt.FormatMsg("engine", fmt.Sprintf(
		"manual compact: %s → %s tokens (%d → %d msgs)",
		memory.FormatTokenCount(data.TokensBefore),
		memory.FormatTokenCount(data.TokensAfter),
		data.MsgsBefore, data.MsgsAfter,
	)))

	return data, nil
}
