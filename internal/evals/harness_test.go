package evals_test

import (
	"context"
	"testing"

	"github.com/harness9/internal/evals"
	"github.com/harness9/internal/schema"
)

func TestRunCase_SimpleBash(t *testing.T) {
	evals.SetupHermeticEnv(t)

	tc := evals.MakeToolCall("id1", "bash", `{"command":"echo hello"}`)
	c := &evals.Case{
		ID:       "simple/bash",
		Category: "tool_calling",
		Prompt:   "运行 echo hello",
		Provider: evals.NewScriptedProvider(
			evals.ScriptedTurn{ToolCalls: []schema.ToolCall{tc}},
			evals.ScriptedTurn{Text: "命令已执行。"},
		),
		Assertions: []evals.Assertion{
			&evals.ToolCalledAssertion{ToolName: "bash"},
			&evals.NoErrorAssertion{},
		},
	}

	result := evals.RunCase(context.Background(), c)
	if !result.Passed {
		for _, f := range result.Failures {
			t.Errorf("断言失败: %s", f.Error())
		}
		t.FailNow()
	}
	t.Logf("✅ %s (%d turns, %dms)", c.ID, result.TurnCount, result.Duration.Milliseconds())
}

func TestRunCase_NoTool(t *testing.T) {
	evals.SetupHermeticEnv(t)

	c := &evals.Case{
		ID:       "simple/no_tool",
		Category: "conversation",
		Prompt:   "你好",
		Provider: evals.NewScriptedProvider(
			evals.ScriptedTurn{Text: "你好！"},
		),
		Assertions: []evals.Assertion{
			&evals.ToolNotCalledAssertion{ToolName: "bash"},
			&evals.OutputContainsAssertion{Expected: "你好"},
			&evals.NoErrorAssertion{},
		},
	}

	result := evals.RunCase(context.Background(), c)
	if !result.Passed {
		for _, f := range result.Failures {
			t.Errorf("断言失败: %s", f.Error())
		}
	}
}

func TestSuite_Run(t *testing.T) {
	evals.SetupHermeticEnv(t)

	suite := &evals.Suite{
		Cases: []*evals.Case{
			{
				ID:       "suite/case1",
				Category: "test",
				Prompt:   "hello",
				Provider: evals.NewScriptedProvider(evals.ScriptedTurn{Text: "world"}),
				Assertions: []evals.Assertion{
					&evals.NoErrorAssertion{},
					&evals.OutputContainsAssertion{Expected: "world"},
				},
			},
		},
	}

	results := suite.Run(context.Background())
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Errorf("expected case to pass")
	}
}
