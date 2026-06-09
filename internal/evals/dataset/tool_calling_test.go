// Package dataset 包含 harness9 的黄金评估数据集。
// 所有测试使用 ScriptedProvider（确定性）+ SetupHermeticEnv（hermetic 隔离），无 API Key 依赖。
// 运行方式：go test ./internal/evals/dataset/... -v
package dataset

import (
	"context"
	"testing"

	"github.com/harness9/internal/evals"
	"github.com/harness9/internal/schema"
)

// TestToolCalling 运行工具调用准确性评估（4 个黄金用例）。
func TestToolCalling(t *testing.T) {
	evals.SetupHermeticEnv(t)

	cases := []*evals.Case{
		// 用例1：bash 基础调用
		{
			ID:       "tool_calling/bash_basic",
			Category: "tool_calling",
			Prompt:   "运行命令 `echo hello`，告诉我输出结果。",
			Provider: evals.NewScriptedProvider(
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc1", "bash", `{"command":"echo hello"}`),
					},
				},
				evals.ScriptedTurn{Text: "命令输出了 hello。"},
			),
			Assertions: []evals.Assertion{
				&evals.ToolCalledAssertion{ToolName: "bash"},
				&evals.NoErrorAssertion{},
				&evals.MaxTurnsAssertion{Max: 3},
			},
		},
		// 用例2：read_file 调用
		{
			ID:       "tool_calling/read_file",
			Category: "tool_calling",
			Prompt:   "读取 README.md 文件，总结其内容。",
			Provider: evals.NewScriptedProvider(
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc1", "read_file", `{"path":"README.md"}`),
					},
				},
				evals.ScriptedTurn{Text: "README.md 描述了一个 Agent 框架项目。"},
			),
			Assertions: []evals.Assertion{
				&evals.ToolCalledAssertion{ToolName: "read_file"},
				&evals.NoErrorAssertion{},
			},
		},
		// 用例3：write_file 后 read_file（多工具顺序调用）
		{
			ID:       "tool_calling/write_then_read",
			Category: "tool_calling",
			Prompt:   "创建 hello.txt 写入 'Hello World'，然后读取确认内容。",
			Provider: evals.NewScriptedProvider(
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc1", "write_file", `{"path":"hello.txt","content":"Hello World"}`),
					},
				},
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc2", "read_file", `{"path":"hello.txt"}`),
					},
				},
				evals.ScriptedTurn{Text: "已确认文件内容为 Hello World。"},
			),
			Assertions: []evals.Assertion{
				&evals.ToolCalledAssertion{ToolName: "write_file"},
				&evals.ToolCalledAssertion{ToolName: "read_file"},
				&evals.NoErrorAssertion{},
				&evals.MaxTurnsAssertion{Max: 4},
			},
		},
		// 用例4：纯对话，不应调用工具
		{
			ID:       "tool_calling/no_tool_conversation",
			Category: "tool_calling",
			Prompt:   "harness9 是什么？简单介绍一下。",
			Provider: evals.NewScriptedProvider(
				evals.ScriptedTurn{Text: "harness9 是一个轻量级 AI Agent Harness 框架。"},
			),
			Assertions: []evals.Assertion{
				&evals.ToolNotCalledAssertion{ToolName: "bash"},
				&evals.ToolNotCalledAssertion{ToolName: "write_file"},
				&evals.OutputContainsAssertion{Expected: "harness9"},
				&evals.NoErrorAssertion{},
			},
		},
	}

	suite := &evals.Suite{Cases: cases}
	results := suite.Run(context.Background())

	passed, failed := 0, 0
	for _, r := range results {
		if r.Passed {
			passed++
			t.Logf("✅ %s (%d turns, %dms)", r.Case.ID, r.TurnCount, r.Duration.Milliseconds())
		} else {
			failed++
			t.Errorf("❌ %s", r.Case.ID)
			for _, f := range r.Failures {
				t.Errorf("   %s", f.Error())
			}
		}
		for _, w := range r.Warnings {
			t.Logf("   ⚠️ %s: %s", r.Case.ID, w.Error())
		}
	}
	t.Logf("工具调用评估: %d/%d 通过", passed, passed+failed)
}
