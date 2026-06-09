package evals

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/schema"
	"github.com/harness9/internal/tools"
)

// Suite 是多个 Case 的集合。
type Suite struct {
	Cases []*Case
}

// Run 按序运行所有 Case，返回结果列表。
func (s *Suite) Run(ctx context.Context) []Result {
	results := make([]Result, len(s.Cases))
	for i, c := range s.Cases {
		results[i] = RunCase(ctx, c)
	}
	return results
}

// RunCase 运行单个评估 Case，返回 Result。
// 内部构造最小化 AgentEngine：使用 Case.Provider（确定性 ScriptedProvider），
// 注册基础工具，通过 recordingHook 记录工具调用。
// 不绑定 Session 或 Compactor（eval 需要确定性，不使用压缩）。
func RunCase(ctx context.Context, c *Case) Result {
	start := time.Now()

	workDir := c.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.MkdirTemp("", "harness9-eval-*")
		if err != nil {
			return Result{
				Case:     c,
				Passed:   false,
				RunError: fmt.Errorf("创建临时目录失败: %w", err),
			}
		}
	}

	maxTurns := c.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}

	// 记录被执行工具的名称
	var toolNames []string
	recorder := &recordingHook{names: &toolNames}

	// 注册基础工具
	registry := tools.NewRegistry()
	for _, t := range []tools.BaseTool{
		tools.NewReadFileTool(workDir),
		tools.NewWriteFileTool(workDir),
		tools.NewBashTool(workDir),
		tools.NewEditFileTool(workDir),
	} {
		_ = registry.Register(t)
	}
	hookReg := hooks.NewHookRegistry(registry, recorder)

	eng := engine.NewAgentEngine(c.Provider, hookReg, workDir,
		engine.WithMaxTurns(maxTurns),
	)

	runErr := eng.Run(ctx, c.Prompt)
	finalOutput := extractFinalOutput(c.Provider)

	result := Result{
		Case:              c,
		TurnCount:         c.Provider.TurnIndex(),
		ToolCallsExecuted: toolNames,
		FinalOutput:       finalOutput,
		RunError:          runErr,
		Duration:          time.Since(start),
	}

	// 运行所有断言
	passed := true
	for _, a := range c.Assertions {
		if f := a.Check(&result); f != nil {
			if f.IsSoft {
				result.Warnings = append(result.Warnings, f)
			} else {
				result.Failures = append(result.Failures, f)
				passed = false
			}
		}
	}
	result.Passed = passed
	return result
}

// recordingHook 实现 hooks.ToolHook，记录所有被调用工具的名称。
type recordingHook struct {
	names *[]string
}

func (h *recordingHook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, hooks.HookDecision, error) {
	*h.names = append(*h.names, tc.Name)
	return ctx, hooks.Allow(), nil
}

func (h *recordingHook) AfterExecute(ctx context.Context, tc schema.ToolCall, result schema.ToolResult) schema.ToolResult {
	return result
}

// extractFinalOutput 从 ScriptedProvider 的脚本序列中提取最终文本输出。
// 返回最后一个有文本内容（无工具调用）的 Turn 的 Text。
func extractFinalOutput(p *ScriptedProvider) string {
	for i := len(p.Turns) - 1; i >= 0; i-- {
		if len(p.Turns[i].ToolCalls) == 0 && p.Turns[i].Text != "" {
			return p.Turns[i].Text
		}
	}
	return "任务完成。"
}
