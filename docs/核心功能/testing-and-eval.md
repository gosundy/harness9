# 测试与评估（Testing & Eval）

harness9 内置完整的 eval 框架，支持确定性单元测试（ScriptedProvider）、黄金数据集评估，以及 CI Quality Gate。

## 框架架构

```
internal/evals/
├── provider.go       ScriptedProvider（确定性 mock LLM）
├── assertions.go     Assertion 接口 + Hard/Soft 断言实现 + Result/Case 类型
├── harness.go        EvalHarness（Suite/RunCase/recordingHook）
├── testenv.go        SetupHermeticEnv（CI 隔离）
├── report.go         JSON + Markdown 报告生成
└── dataset/
    ├── tool_calling_test.go  工具调用准确性（4 用例）
    ├── planning_test.go      Planning 完成率（2 用例）
    └── memory_test.go        Memory 持久化（2 用例）
```

## 快速开始

### 运行所有 eval

```bash
go test ./internal/evals/... ./internal/evals/dataset/... -v
```

### 运行特定类别

```bash
go test ./internal/evals/dataset/... -v -run TestToolCalling
go test ./internal/evals/dataset/... -v -run TestPlanning
go test ./internal/evals/dataset/... -v -run TestMemory
```

## 编写 eval 用例

```go
func TestMyFeature(t *testing.T) {
    evals.SetupHermeticEnv(t)  // 必须：隔离 API Key

    c := &evals.Case{
        ID:       "my_feature/basic",
        Category: "tool_calling",
        Prompt:   "帮我运行 ls 命令",
        Provider: evals.NewScriptedProvider(
            evals.ScriptedTurn{
                ToolCalls: []schema.ToolCall{
                    evals.MakeToolCall("tc1", "bash", `{"command":"ls"}`),
                },
            },
            evals.ScriptedTurn{Text: "命令已执行。"},
        ),
        Assertions: []evals.Assertion{
            &evals.ToolCalledAssertion{ToolName: "bash"},
            &evals.NoErrorAssertion{},
            &evals.MaxTurnsAssertion{Max: 3},  // soft，仅警告
        },
    }

    result := evals.RunCase(context.Background(), c)
    if !result.Passed {
        for _, f := range result.Failures {
            t.Errorf("断言失败: %s", f.Error())
        }
    }
}
```

## Assertion 参考

### Hard Assertions（失败则 Case 不通过）

| 断言 | 说明 |
|------|------|
| `ToolCalledAssertion{ToolName, MinTimes}` | 工具被调用 >= MinTimes 次 |
| `ToolNotCalledAssertion{ToolName}` | 工具一次都没有被调用 |
| `OutputContainsAssertion{Expected}` | 最终输出包含期望字符串 |
| `OutputExcludesAssertion{Forbidden}` | 最终输出不包含禁止字符串 |
| `NoErrorAssertion{}` | 执行没有发生 RunError |
| `ErrorAssertion{}` | 执行发生了 RunError（测试错误路径） |

### Soft Assertions（失败仅记警告）

| 断言 | 说明 |
|------|------|
| `MaxTurnsAssertion{Max}` | Turn 数不超过 Max（效率指标） |
| `MaxToolCallsAssertion{Max}` | 工具调用次数不超过 Max |

## CI Quality Gate

PR 触发 eval CI（`.github/workflows/eval.yml`），所有黄金数据集用例必须通过才能合并。
Hermetic 模式（`HARNESS9_EVAL_HERMETIC=1`）下，清除所有 API Key，确保无真实 LLM 调用。
