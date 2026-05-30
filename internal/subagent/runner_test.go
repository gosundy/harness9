package subagent

import (
	"context"
	"encoding/json"
	"strings"
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
