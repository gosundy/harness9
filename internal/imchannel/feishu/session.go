package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/harness9/internal/schema"
)

// maxThinkingRunes 是进度消息中 Thinking 文本的最大 Unicode 字符数。
// 超出部分截断并附加省略号，避免飞书消息过长。
const maxThinkingRunes = 400

// Session 是 imchannel.Session 接口的飞书实现。
//
// 进度展示策略：
//  1. NotifyThinking       → 发送文本占位消息"🤔 思考中..."，记录 msgID
//  2. UpdateThinkingContent → PatchMessage 将思考内容展示在进度消息顶部
//  3. NotifyToolStart      → PatchMessage 追加工具调用行
//  4. NotifyToolDone       → PatchMessage 更新对应行为完成状态
//  5. SendReply            → 发送最终回复（进度消息保留不删除）
//
// 进度消息渲染格式：
//
//	💭 [thinking 文本，最多 400 字]
//	🔧 调用工具：bash
//	✅ bash（123ms）
//
// Session 实例由单个 goroutine 驱动（Server.handleMessage），无并发写入，不需要互斥锁。
type Session struct {
	client *lark.Client
	chatID string

	// msgID 是进度占位消息的飞书消息 ID，由 NotifyThinking 写入，后续方法只读。
	msgID string

	// thinkingContent 保存 Phase 1（Thinking）阶段的推理文本。
	// 空时进度消息显示"🤔 思考中..."；非空时显示"💭 <text>"。
	thinkingContent string

	// lines 记录工具调用行，按追加顺序排列。
	lines []string

	// lineIndex 将工具调用 ID 映射到 lines 中的行索引，用于 NotifyToolDone 时精确更新对应行。
	lineIndex map[string]int
}

// NotifyThinking 发送"思考中"占位消息并记录其 ID。
func (s *Session) NotifyThinking(ctx context.Context) error {
	content := buildTextContent("🤔 思考中...")
	resp, err := s.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeText).
				ReceiveId(s.chatID).
				Content(content).
				Build()).
			Build())
	if err != nil {
		return fmt.Errorf("feishu NotifyThinking: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu NotifyThinking: code=%d msg=%s", resp.Code, resp.Msg)
	}
	s.msgID = *resp.Data.MessageId
	s.thinkingContent = ""
	s.lines = []string{}
	return nil
}

// UpdateThinkingContent 将 Thinking 阶段的推理文本写入进度消息。
func (s *Session) UpdateThinkingContent(ctx context.Context, text string) error {
	s.thinkingContent = text
	return s.patchProgress(ctx)
}

// NotifyToolStart 在进度消息中追加工具调用开始行，并更新飞书消息。
func (s *Session) NotifyToolStart(ctx context.Context, tc schema.ToolCall) error {
	idx := len(s.lines)
	s.lines = append(s.lines, fmt.Sprintf("🔧 调用工具：%s", tc.Name))
	s.lineIndex[tc.ID] = idx
	return s.patchProgress(ctx)
}

// NotifyToolDone 将进度消息中对应工具行更新为完成状态（成功或失败），并更新飞书消息。
func (s *Session) NotifyToolDone(ctx context.Context, tc schema.ToolCall, result schema.ToolResult, d time.Duration) error {
	icon := "✅"
	if result.IsError {
		icon = "❌"
	}
	if idx, ok := s.lineIndex[tc.ID]; ok {
		s.lines[idx] = fmt.Sprintf("%s %s（%dms）", icon, tc.Name, d.Milliseconds())
	}
	return s.patchProgress(ctx)
}

// SendReply 发送 Agent 最终回复。进度占位消息保留不删除。
func (s *Session) SendReply(ctx context.Context, text string) error {
	if text == "" {
		text = "✅ 任务完成"
	}
	content := buildTextContent(text)
	resp, err := s.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeText).
				ReceiveId(s.chatID).
				Content(content).
				Build()).
			Build())
	if err != nil {
		return fmt.Errorf("feishu SendReply: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu SendReply: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// patchProgress 将当前 thinkingContent + lines 渲染为进度文本并更新飞书占位消息。
// 若 msgID 为空（NotifyThinking 失败），静默跳过。
func (s *Session) patchProgress(ctx context.Context) error {
	if s.msgID == "" {
		return nil
	}

	var sb strings.Builder
	if s.thinkingContent != "" {
		sb.WriteString("💭 ")
		sb.WriteString(truncateRunes(s.thinkingContent, maxThinkingRunes))
	} else {
		sb.WriteString("🤔 思考中...")
	}

	if len(s.lines) > 0 {
		sb.WriteByte('\n')
		sb.WriteString(strings.Join(s.lines, "\n"))
	}

	content := buildTextContent(sb.String())
	resp, err := s.client.Im.Message.Patch(ctx,
		larkim.NewPatchMessageReqBuilder().
			MessageId(s.msgID).
			Body(larkim.NewPatchMessageReqBodyBuilder().
				Content(content).
				Build()).
			Build())
	if err != nil {
		return fmt.Errorf("feishu patchProgress: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu patchProgress: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// buildTextContent 将纯文本序列化为飞书文本消息的 Content JSON（{"text":"..."}）。
func buildTextContent(text string) string {
	b, _ := json.Marshal(map[string]string{"text": text})
	return string(b)
}

// truncateRunes 按 Unicode 字符数截断字符串，超出时追加省略号。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
