package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/harness9/internal/schema"
)

// Session 是 imchannel.Session 接口的飞书实现。
//
// 进度展示策略：
//  1. NotifyThinking → 发送文本占位消息"🤔 思考中..."，记录 msgID
//  2. NotifyToolStart → PatchMessage 追加工具调用行
//  3. NotifyToolDone  → PatchMessage 更新对应行为完成状态
//  4. SendReply       → 发送最终回复，然后删除占位进度消息
//
// Session 实例由单个 goroutine 驱动（Server.handleMessage），无并发写入，不需要互斥锁。
type Session struct {
	client *lark.Client
	chatID string

	// msgID 是进度占位消息的飞书消息 ID，由 NotifyThinking 写入，后续方法只读。
	msgID string

	// lines 记录进度消息的每一行文本，按追加顺序排列。
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
	s.lines = []string{"🤔 思考中..."}
	return nil
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

// SendReply 发送 Agent 最终回复，然后删除进度占位消息。
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

	// 删除进度占位消息，清理会话消息流
	if s.msgID != "" {
		if _, delErr := s.client.Im.Message.Delete(ctx,
			larkim.NewDeleteMessageReqBuilder().
				MessageId(s.msgID).
				Build()); delErr != nil {
			log.Printf("[feishu] 删除进度消息失败 (msgID=%s): %v", s.msgID, delErr)
		}
	}
	return nil
}

// patchProgress 将当前 lines 合并为多行文本并更新飞书进度占位消息。
// 若 msgID 为空（NotifyThinking 失败），静默跳过。
func (s *Session) patchProgress(ctx context.Context) error {
	if s.msgID == "" {
		return nil
	}
	text := strings.Join(s.lines, "\n")
	content := buildTextContent(text)
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
