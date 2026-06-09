// Package evals 提供 harness9 的自动化评估框架。
//
// 核心组件：
//   - ScriptedProvider：确定性 mock LLMProvider，支持脚本化响应序列
//   - EvalHarness：评估运行器，管理 Case 生命周期
//   - Assertion：断言接口，验证 Agent 行为
//   - Suite：批量运行多个 Case 并生成报告
package evals

import (
	"context"
	"sync"

	"github.com/harness9/internal/schema"
)

// ScriptedTurn 代表 ScriptedProvider 在某一轮的预设回复。
// 一个 Turn 可以包含工具调用（ToolCalls 非空）或文本回复（Text 非空），
// 也可以模拟 LLM 调用失败（Err 非 nil）。三者互斥：Err 优先于其他字段。
type ScriptedTurn struct {
	// Text 是 LLM 的文本回复。工具调用时可为空；纯对话 Turn 时应非空。
	Text string
	// ToolCalls 是 LLM 发起的工具调用列表。非空时引擎会执行工具并将结果注入上下文。
	ToolCalls []schema.ToolCall
	// Err 如果非 nil，Generate 直接返回此错误，模拟 LLM 调用失败。
	// 用于测试引擎的错误处理路径（error observation → self-healing）。
	Err error
}

// RecordedCall 记录一次实际发生的 LLM 调用（包含实际传入的消息和工具列表）。
// 用于高级断言：验证 LLM 调用时的上下文内容（如 Tool Discovery 是否正确）。
type RecordedCall struct {
	Messages []schema.Message        // 实际传入 Generate/GenerateStream 的消息列表
	Tools    []schema.ToolDefinition // 实际传入的工具定义列表
}

// ScriptedProvider 是 LLMProvider 的确定性实现，按 Turns 序列返回预设回复。
//
// 设计意图：将"LLM 会做什么"从 eval 测试中抽离，只关注引擎行为（工具调度、Observation 注入、终止条件）。
// 所有 Turn 耗尽后，默认返回「任务完成。」文本回复（模拟 LLM 自然终止，触发 runLoop 退出）。
//
// 线程安全：Generate 调用持有 mu 锁，并发访问无竞争。
type ScriptedProvider struct {
	// Turns 是预设响应序列，外部可直接访问（用于 extractFinalOutput 遍历）。
	Turns []ScriptedTurn
	mu    sync.Mutex
	idx   int            // 下一个待返回的 Turn 索引
	calls []RecordedCall // 记录的 LLM 调用历史
}

// NewScriptedProvider 构造一个预设了响应序列的 ScriptedProvider。
func NewScriptedProvider(turns ...ScriptedTurn) *ScriptedProvider {
	return &ScriptedProvider{Turns: turns}
}

// Generate 按序列返回预设 Turn 的回复；超出序列时返回默认终止回复。
func (p *ScriptedProvider) Generate(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, RecordedCall{Messages: messages, Tools: tools})

	if p.idx >= len(p.Turns) {
		return &schema.Message{Role: schema.RoleAssistant, Content: "任务完成。"}, nil, nil
	}
	turn := p.Turns[p.idx]
	p.idx++

	if turn.Err != nil {
		return nil, nil, turn.Err
	}
	return &schema.Message{
		Role:      schema.RoleAssistant,
		Content:   turn.Text,
		ToolCalls: turn.ToolCalls,
	}, &schema.Usage{InputTokens: 100, OutputTokens: 50}, nil
}

// GenerateStream 委托给 Generate，将结果包装为 channel 返回。
// eval 场景不需要逐 token 流式，因此简化为一次性 Done 事件。
// 错误情况：将错误包装为 StreamChunkError 写入 channel 后关闭，不返回 channel error。
// 这保持了与真实 Provider 流式语义的一致性（调用者始终从 channel 读取）。
func (p *ScriptedProvider) GenerateStream(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (<-chan schema.StreamChunk, error) {
	msg, usage, err := p.Generate(ctx, messages, tools)
	ch := make(chan schema.StreamChunk, 2)
	if err != nil {
		ch <- schema.StreamChunk{Type: schema.StreamChunkError, Error: err.Error()}
		close(ch)
		return ch, nil
	}
	ch <- schema.StreamChunk{Type: schema.StreamChunkDone, Message: msg, Usage: usage}
	close(ch)
	return ch, nil
}

// Calls 返回所有已记录的 LLM 调用列表（线程安全）。
func (p *ScriptedProvider) Calls() []RecordedCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]RecordedCall, len(p.calls))
	copy(result, p.calls)
	return result
}

// TurnIndex 返回当前已消耗的 Turn 数量（线程安全）。
func (p *ScriptedProvider) TurnIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idx
}

// Reset 重置 provider 状态（可复用于多次运行）。
func (p *ScriptedProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idx = 0
	p.calls = nil
}

// MakeToolCall 构造 schema.ToolCall 的辅助函数，简化测试数据准备。
// args 应为合法的 JSON 字符串（如 `{"command":"ls"}`），直接转为 json.RawMessage。
// 不做 JSON 合法性验证（eval 场景传入的 args 均为字面量，省略校验以保持简洁）。
func MakeToolCall(id, name, args string) schema.ToolCall {
	return schema.ToolCall{
		ID:        id,
		Name:      name,
		Arguments: []byte(args),
	}
}
