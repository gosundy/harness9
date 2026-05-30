package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/provider/providertest"
	"github.com/harness9/internal/schema"
	"github.com/harness9/internal/tools"
)

// fakeTool 是一个名称可配置的最小工具，Execute 返回固定文本。
type fakeTool struct{ name string }

func (f *fakeTool) Name() string { return f.name }
func (f *fakeTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: f.name, Description: "fake",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (f *fakeTool) Execute(context.Context, json.RawMessage) (string, error) { return "fake-out", nil }

// newTestRunner 构造一个使用 mock provider、最小工具集的 Runner。
func newTestRunner(t *testing.T, baseTools []tools.BaseTool, p provider.LLMProvider) *Runner {
	t.Helper()
	return &Runner{
		baseTools:       baseTools,
		sharedHooks:     nil,
		settingsPath:    "",
		workDir:         t.TempDir(),
		defaultMaxTurns: 5,
		providerFor: func(model string) (provider.LLMProvider, int, error) {
			return p, 128_000, nil
		},
		compactorFor: func(provider.LLMProvider, int) memory.Compactor { return nil },
		baseCtx:      context.Background(),
	}
}

func TestRunnerToolIsolation(t *testing.T) {
	var offered []string
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, tls []schema.ToolDefinition) schema.Message {
		for _, tl := range tls {
			offered = append(offered, tl.Name)
		}
		return schema.Message{Role: schema.RoleAssistant, Content: "done"}
	})
	base := []tools.BaseTool{&fakeTool{"read_file"}, &fakeTool{"bash"}, &fakeTool{"write_file"}}
	r := newTestRunner(t, base, mock)

	def := SubAgentDefinition{Name: "ro", Description: "d", SystemPrompt: "p", Tools: []string{"read_file"}}
	_, err := r.Run(context.Background(), def, "task", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range offered {
		if name != "read_file" {
			t.Fatalf("子代理不应看到 %q，仅应有 read_file", name)
		}
	}
	if len(offered) == 0 {
		t.Fatal("子代理应至少看到 read_file")
	}
}

func TestRunnerContextIsolation(t *testing.T) {
	var sawHistory []schema.Message
	mock := providertest.NewMockWithCallback(func(msgs []schema.Message, _ []schema.ToolDefinition) schema.Message {
		sawHistory = msgs
		return schema.Message{Role: schema.RoleAssistant, Content: "ok"}
	})
	r := newTestRunner(t, nil, mock)
	def := SubAgentDefinition{Name: "x", Description: "d", SystemPrompt: "SUBAGENT-SYS-PROMPT"}
	_, err := r.Run(context.Background(), def, "USER-TASK", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sawHistory) != 2 {
		t.Fatalf("子代理初始历史应为 2 条，得 %d: %+v", len(sawHistory), sawHistory)
	}
	if sawHistory[0].Role != schema.RoleSystem || !strings.Contains(sawHistory[0].Content, "SUBAGENT-SYS-PROMPT") {
		t.Errorf("第 0 条应为子代理 system prompt: %+v", sawHistory[0])
	}
	if sawHistory[1].Role != schema.RoleUser || sawHistory[1].Content != "USER-TASK" {
		t.Errorf("第 1 条应为任务 prompt: %+v", sawHistory[1])
	}
}

func TestRunnerReturnsFinalText(t *testing.T) {
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "FINAL-ANSWER"}
	})
	r := newTestRunner(t, nil, mock)
	def := SubAgentDefinition{Name: "x", Description: "d", SystemPrompt: "p"}
	res, err := r.Run(context.Background(), def, "go", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "FINAL-ANSWER" {
		t.Fatalf("FinalText=%q, want FINAL-ANSWER", res.FinalText)
	}
}

func TestRunnerForwardsProgress(t *testing.T) {
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "hello"}
	})
	r := newTestRunner(t, nil, mock)

	var updates []schema.SubAgentUpdate
	ctx := hooks.WithSubAgentProgress(context.Background(), func(u schema.SubAgentUpdate) {
		updates = append(updates, u)
	})
	def := SubAgentDefinition{Name: "reviewer", Description: "d", SystemPrompt: "p"}
	if _, err := r.Run(ctx, def, "go", false); err != nil {
		t.Fatal(err)
	}
	var sawStart, sawDone bool
	for _, u := range updates {
		if u.AgentName != "reviewer" {
			t.Fatalf("更新 AgentName=%q", u.AgentName)
		}
		if u.Kind == schema.SubAgentStart {
			sawStart = true
		}
		if u.Kind == schema.SubAgentDone {
			sawDone = true
		}
	}
	if !sawStart || !sawDone {
		t.Fatalf("应收到 start 与 done 更新: start=%v done=%v", sawStart, sawDone)
	}
}

// readFileCallMock 返回一个 mock：第 1 个 turn 发起一次 read_file 工具调用，
// 之后的 turn 返回最终文本。用于驱动子代理实际执行一次工具，触发权限审批链。
func readFileCallMock(finalText string) provider.LLMProvider {
	var mu sync.Mutex
	turn := 0
	return providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		mu.Lock()
		turn++
		n := turn
		mu.Unlock()
		if n == 1 {
			return schema.Message{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{}`)},
				},
			}
		}
		return schema.Message{Role: schema.RoleAssistant, Content: finalText}
	})
}

// TestRunnerForegroundApprovalBridge 验证：前台模式下，子代理触发的工具审批
// 会桥接到父级 ApprovalFunc（人类在环），而非自动拒绝。
func TestRunnerForegroundApprovalBridge(t *testing.T) {
	mock := readFileCallMock("ok")
	base := []tools.BaseTool{&fakeTool{"read_file"}}
	r := newTestRunner(t, base, mock)

	var mu sync.Mutex
	var calls int
	var sawTool string
	ctx := hooks.WithApprovalFn(context.Background(),
		func(_ context.Context, tc schema.ToolCall, _, _ string) hooks.ApprovalResponse {
			mu.Lock()
			calls++
			sawTool = tc.Name
			mu.Unlock()
			return hooks.ApprovalResponse{Approved: true}
		})

	def := SubAgentDefinition{Name: "fg", Description: "d", SystemPrompt: "p", Tools: []string{"read_file"}}
	if _, err := r.Run(ctx, def, "go", false); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("前台应恰好桥接一次审批到父级, 得 %d", calls)
	}
	if sawTool != "read_file" {
		t.Fatalf("审批工具名应为 read_file, 得 %q", sawTool)
	}
}

// TestRunnerBackgroundFailClosed 验证：后台模式下，子代理的工具审批永不
// 升级到人类（fail-closed），父级 ApprovalFunc 绝不会被调用。
func TestRunnerBackgroundFailClosed(t *testing.T) {
	mock := readFileCallMock("ok")
	base := []tools.BaseTool{&fakeTool{"read_file"}}
	r := newTestRunner(t, base, mock)

	var mu sync.Mutex
	var calls int
	ctx := hooks.WithApprovalFn(context.Background(),
		func(_ context.Context, _ schema.ToolCall, _, _ string) hooks.ApprovalResponse {
			mu.Lock()
			calls++
			mu.Unlock()
			return hooks.ApprovalResponse{Approved: true}
		})

	def := SubAgentDefinition{Name: "bg", Description: "d", SystemPrompt: "p", Tools: []string{"read_file"}}
	if _, err := r.Run(ctx, def, "go", true); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("后台模式绝不应升级审批到父级, 但被调用 %d 次", calls)
	}
}

// TestRunnerErrorPropagation 验证：当子代理始终发起工具调用、永不终止时，
// 引擎触及 max-turns 上限返回错误，Run 返回非 nil error 且发出 SubAgentError 进度。
func TestRunnerErrorPropagation(t *testing.T) {
	// mock 始终发起 read_file 工具调用，永不返回最终文本 → 触发 max-turns。
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{}`)},
			},
		}
	})
	base := []tools.BaseTool{&fakeTool{"read_file"}}
	r := newTestRunner(t, base, mock)
	r.defaultMaxTurns = 2

	var mu sync.Mutex
	var updates []schema.SubAgentUpdate
	ctx := hooks.WithSubAgentProgress(context.Background(), func(u schema.SubAgentUpdate) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	})
	ctx = hooks.WithApprovalFn(ctx,
		func(_ context.Context, _ schema.ToolCall, _, _ string) hooks.ApprovalResponse {
			return hooks.ApprovalResponse{Approved: true}
		})

	def := SubAgentDefinition{Name: "loop", Description: "d", SystemPrompt: "p", Tools: []string{"read_file"}}
	_, err := r.Run(ctx, def, "go", false)
	if err == nil {
		t.Fatal("子代理触及 max-turns 应返回非 nil error")
	}

	mu.Lock()
	defer mu.Unlock()
	var sawError bool
	for _, u := range updates {
		if u.Kind == schema.SubAgentError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("应至少发出一条 SubAgentError 进度更新")
	}
}
