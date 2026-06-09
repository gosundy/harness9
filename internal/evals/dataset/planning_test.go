package dataset

import (
	"context"
	"testing"

	"github.com/harness9/internal/evals"
	"github.com/harness9/internal/schema"
)

// TestPlanning 运行 Planning 能力评估（2 个黄金用例）。
func TestPlanning(t *testing.T) {
	evals.SetupHermeticEnv(t)

	cases := []*evals.Case{
		// 用例1：通过 todo_write 生成计划
		{
			ID:       "planning/plan_generated",
			Category: "planning",
			Prompt:   "用 todo_write 创建一个包含 3 个步骤的实现计划。",
			Provider: evals.NewScriptedProvider(
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc1", "todo_write", `{"todos":[
							{"id":"1","content":"步骤一：读取需求","status":"pending"},
							{"id":"2","content":"步骤二：实现功能","status":"pending"},
							{"id":"3","content":"步骤三：编写测试","status":"pending"}
						]}`),
					},
				},
				evals.ScriptedTurn{Text: "已生成包含 3 个步骤的实现计划。"},
			),
			Assertions: []evals.Assertion{
				&evals.ToolCalledAssertion{ToolName: "todo_write"},
				&evals.NoErrorAssertion{},
			},
		},
		// 用例2：Plan Mode 下不应调用 write_file/edit_file
		{
			ID:       "planning/no_write_in_plan_mode",
			Category: "planning",
			Prompt:   "分析代码并制定修改计划，不要直接修改文件。",
			Provider: evals.NewScriptedProvider(
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc1", "read_file", `{"path":"go.mod"}`),
					},
				},
				evals.ScriptedTurn{
					ToolCalls: []schema.ToolCall{
						evals.MakeToolCall("tc2", "todo_write", `{"todos":[
							{"id":"1","content":"修改 go.mod 添加依赖","status":"pending"}
						]}`),
					},
				},
				evals.ScriptedTurn{Text: "分析完成，计划已制定。"},
			),
			Assertions: []evals.Assertion{
				&evals.ToolCalledAssertion{ToolName: "todo_write"},
				&evals.ToolNotCalledAssertion{ToolName: "write_file"},
				&evals.ToolNotCalledAssertion{ToolName: "edit_file"},
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
	t.Logf("Planning 评估: %d/%d 通过", passed, passed+failed)
}
