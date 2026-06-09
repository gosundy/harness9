package observability

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/harness9/internal/schema"
)

// maxSpanAttrLen 是写入 OTEL Span 属性的最大字节数。
// 超出部分截断并追加 "…（已截断）"。
const maxSpanAttrLen = 4096

// truncateAttr 净化并截断字符串，确保满足 OTEL/protobuf string 字段的两个约束：
//  1. 必须是合法的 UTF-8（工具输出可能含有 binary 数据）
//  2. 长度不超过 maxSpanAttrLen 字节
func truncateAttr(s string) string {
	// 将非法 UTF-8 字节序列替换为空，满足 OTLP protobuf 序列化要求。
	// 不替换为占位符是为了避免引入意义不明的乱码。
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if len(s) <= maxSpanAttrLen {
		return s
	}
	// 截断时必须在合法 UTF-8 边界处截断，避免截到多字节字符的中间位置。
	cut := maxSpanAttrLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…（已截断）"
}

// msgView 是 schema.Message 的轻量序列化视图，只保留 Langfuse 展示所需字段。
type msgView struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCallView `json:"tool_calls,omitempty"`
}

// toolCallView 是工具调用的序列化视图。
type toolCallView struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// serializeMessages 将消息列表序列化为 JSON 字符串，供 langfuse.input 属性使用。
// 保留 role、content 和 tool_calls，截断超长内容。
func serializeMessages(messages []schema.Message) string {
	views := make([]msgView, 0, len(messages))
	for _, m := range messages {
		v := msgView{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			v.ToolCalls = append(v.ToolCalls, toolCallView{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}
		views = append(views, v)
	}
	b, err := json.Marshal(views)
	if err != nil {
		return ""
	}
	return truncateAttr(string(b))
}

// serializeOutput 将 LLM 响应消息序列化为字符串，供 langfuse.output 属性使用。
// 若响应包含工具调用则序列化工具调用列表；否则返回文本内容。
func serializeOutput(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if len(msg.ToolCalls) > 0 {
		calls := make([]toolCallView, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			calls[i] = toolCallView{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}
		}
		b, _ := json.Marshal(calls)
		return truncateAttr(string(b))
	}
	return truncateAttr(msg.Content)
}
