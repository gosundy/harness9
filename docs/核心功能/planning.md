# Planning 模块实现原理

harness9 的 Planning 模块为 Agent 提供两阶段的"先规划、再执行"工作流。用户通过 `Shift+Tab` 进入 Plan Mode，Agent 先用 `todo_write` 生成结构化任务清单并停止，用户审查后批准执行，Agent 按清单逐项完成实际工作并自动续跑，直到全部 todo 完成。

---

## 核心组件

```
internal/planning/
├── todo.go       # TodoStore（线程安全任务列表）+ TodoItem / TodoStatus
├── mode.go       # PlanMode 枚举（Default / Plan / AutoEdit）
internal/tools/
├── todo_write.go # todo_write 工具：读写 + 状态转换校验
cmd/harness9/
├── tui_update.go # 执行入口（execPrompt / dispatch）、autoExecuting 自动续跑
├── tui_view.go   # renderTodoLines（格式化任务列表）
├── tui.go        # Plan Mode 色调样式
```

---

## 工作流总览

```
用户 Shift+Tab ──► Plan Mode 激活（琥珀黄色调）
用户输入任务 ──► dispatch(planPrompt)
                  │
                  ▼
         Agent 只读探索（filterReadOnlyTools：仅读写工具被过滤）
         + todo_write 输出实现计划
         + 文字简述后自然停止
                  │
                  ▼
         TUI 弹出审查对话框（planReviewing = true）
         [1] 批准并自动执行
         [2] 批准并逐步确认
         [3] 继续修改计划
         [4] 取消
                  │ 选 1 或 2
                  ▼
         模式切回 Default，dispatch(execPrompt)
         autoExecuting = true
                  │
                  ▼
         Agent 按清单逐项执行（in_progress → 实际工作 → completed）
                  │
            EventDone 检测
            ┌────────────────────────────────────────┐
            │ pending > 0 且 stuck < 3？             │
            │  ├── 有进度：dispatch(execContinuePrompt)│
            │  └── 无进度：stuck++                   │
            │ stuck ≥ 3：放弃，提示手动干预            │
            │ pending == 0：autoExecuting = false     │
            └────────────────────────────────────────┘
```

---

## PlanMode 枚举

```go
const (
    PlanModeDefault  PlanMode = iota // 完整工具访问（默认）
    PlanModePlan                     // 只读模式：过滤写工具，用于规划阶段
    PlanModeAutoEdit                 // 保留：自动确认编辑（未来扩展）
)
```

- `Shift+Tab` 循环切换：Default → Plan → AutoEdit → Default
- 当前模式在 TUI 状态栏展示（`[PLAN]` / `[AUTO]`），Default 模式不展示
- `eng.SetPlanMode(mode)` 线程安全，可从 TUI goroutine 调用
- `engine.runLoop` 启动时快照模式，避免循环中竞态

---

## 工具层权限控制（filterReadOnlyTools）

Plan Mode 下 `write_file`、`edit_file`、`bash`（写）等工具从工具列表中过滤，而不是通过 prompt 声明。

```go
// agent_loop.go
if planMode == planning.PlanModePlan {
    availableTools = filterReadOnlyTools(availableTools)
}
```

**为什么在工具层而非 prompt 层控制？**

Prompt 是软约束，LLM 在上下文压缩后可能遗忘限制，或被历史消息中的角色声明覆盖。在工具层过滤后，LLM 从 schema 中根本看不到写工具，无论 prompt 内容如何，都无法调用。

Plan Mode 下可用工具：`bash`（只读命令限制通过 prompt 补充）、`read_file`、`todo_write`、`use_skill`。

---

## Plan Mode Prompt 注入

工具层无法表达"bash 只用只读命令"这类行为约束，因此在 `runLoop` 启动时注入前缀 prompt：

```go
if planMode == planning.PlanModePlan {
    userPrompt = "分析以下请求，用 todo_write 输出一份可直接执行的实现计划，然后用纯文字简述计划后停止。\n" +
        "todo 项要求：每条对应一个具体的实现动作（例如：创建某文件、实现某函数、运行某命令），\n" +
        "而非高层规划描述（禁止写\"需求澄清\"、\"方案设计\"之类无法直接执行的条目）。\n" +
        "如需了解当前代码库，可使用 read_file 或 bash（只读命令：ls、cat、find、grep）。\n" +
        "不要创建文件、执行 build/install 或做任何实际修改。\n\n" +
        userPrompt
}
```

注入规则：只包含 **行为引导**，不声明权限（"你现在有权限 X"）。权限由工具层决定，prompt 只告诉 LLM"该做什么"，不告诉"能做什么"。

---

## TodoStore

`TodoStore` 是线程安全的内存任务列表，使用全量替换（atomic replace）语义：

```go
type TodoStore struct {
    mu    sync.RWMutex
    items []TodoItem
}

// Write 原子性全量替换任务列表，返回替换后的副本。
func (s *TodoStore) Write(items []TodoItem) []TodoItem

// Read 返回当前任务列表的副本（线程安全）。
func (s *TodoStore) Read() []TodoItem
```

**TodoItem 状态机：**

```
pending ──► in_progress ──► completed
   │              │
   └──► cancelled └──► cancelled
```

**强制约束**：`pending → completed` 和 `new → completed` 的直接跳转被 `todo_write` 工具拒绝：

```go
// todo_write.go — Execute()
for _, item := range input.Todos {
    if item.Status != planning.TodoCompleted { continue }
    prior, exists := prevStatus[item.ID]
    if !exists || (prior != planning.TodoInProgress && prior != planning.TodoCompleted) {
        return "", fmt.Errorf("任务 %q 不能直接标记为 completed（当前状态：%s）；"+
            "请先将其标记为 in_progress，完成实际操作后再标记为 completed", ...)
    }
}
```

错误信息以 `ToolResult{IsError: true}` 回传 LLM，LLM 被迫按正确步骤重试。

---

## 执行模式 Prompt

批准执行后，TUI 向 Agent 发送：

```go
const execPrompt = "按照 todo 清单逐项执行。规则：\n" +
    "1. 每开始一项前，用 todo_write 将其状态设为 in_progress\n" +
    "2. 用工具完成该项的实际工作——创建文件、写代码、运行命令等；" +
    "仅更新 todo_write 状态而不调用其他工具，不算完成该项\n" +
    "3. 确认实际产出后，用 todo_write 将其状态设为 completed\n" +
    "4. 不要输出进度摘要文字，立即处理下一项\n" +
    "全部完成后，用一句话汇报整体结果。"
```

Agent 自然停止后（`EventDone`），若仍有未完成 todo，TUI 自动追加续跑 prompt：

```go
const execContinuePrompt = "继续处理 todo 清单中下一个 pending 或 in_progress 的任务项。" +
    "先用 todo_write 标记为 in_progress，然后用工具完成实际工作（写文件、执行命令等），" +
    "确认产出后标记为 completed，再处理下一项。" +
    "不要只更新状态而不做实际操作，不要输出进度摘要。"
```

---

## 自动执行循环与停滞检测

`autoExecuting = true` 时，每次 `EventDone` 触发以下逻辑：

```go
if m.autoExecuting && m.todoStore != nil {
    // 统计 pending / done
    if pending > 0 {
        if done > m.autoExecPrevDone {
            m.autoExecStuck = 0      // 有进度，重置
        } else {
            m.autoExecStuck++        // 无进度，计数
        }
        if m.autoExecStuck < 3 {
            m.autoExecPrevDone = done
            return m.dispatch(execContinuePrompt)  // 续跑
        }
        // 连续 3 次无进度 → 放弃，提示手动干预
        m.autoExecuting = false
        m.lines = append(m.lines, dimStyle.Render("  ⚠ 执行停滞，请手动描述下一步"))
    } else {
        m.autoExecuting = false  // 全部完成
    }
}
```

停滞检测的触发条件：连续 3 次 `EventDone` 后 `done` 计数没有增加，说明 Agent 在空转（例如只输出文字但不调用工具）。

---

## TUI 视觉集成

### Plan Mode 色调

Plan Mode 激活时，TUI 从青色（`#81`）切换为琥珀黄色调：

```go
planAccentStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
planStatusBarStyle = lipgloss.NewStyle().
    Background(lipgloss.Color("94")).
    Foreground(lipgloss.Color("220")).
    Padding(0, 1)
```

`accentStyle()` 和 `activeStatusBarStyle()` 方法按当前模式返回对应样式，View 层统一调用，无需散落的 `if planMode` 判断。

### 实时 Todo 快照

每次 `todo_write` 工具完成后，在工具完成行正下方追加最新任务快照（而非在顶部原地替换）：

```
  ✓ todo_write({...}) — 0s
  ☰  Tasks  ·  3/11  ·  1 active
  ──────────────────────────────────
   1.  ✔  创建目录结构
   2.  ✔  初始化 go.mod
   3.  ▶  实现 main.go           ← 进行中
   4.  ○  添加路由注册
   ...
```

### 审查对话框

Plan Mode 完成后，TUI 展示带边框的选项对话框（`renderPlanReviewDialog`），暂停输入等待用户按键（1-4）：

```
╭──────────────────────────────────────────────╮
│  Plan Mode 完成 — 选择下一步操作               │
│                                              │
│  [1]  批准并自动执行                           │
│  [2]  批准并逐步确认编辑                        │
│  [3]  继续修改计划（保持 Plan Mode）             │
│  [4]  取消                                   │
╰──────────────────────────────────────────────╯
```

---

## 跨会话 Todo 持久化

`TodoStore` 内容随 Session 持久化到 SQLite，进程重启后可续跑未完成的任务：

```go
// runLoop 启动时恢复
if sess != nil && todoStore != nil {
    if todos, err := sess.GetTodos(ctx); err == nil {
        todoStore.Write(todos)
    }
}

// runLoop 结束时保存（defer）
defer func() {
    if sess != nil && todoStore != nil {
        sess.SaveTodos(ctx, todoStore.Read())
    }
}()
```

`TodoStore` 在 TUI 层跨 session 切换时直接清空（`/new` 和 `/resume` 命令触发），新会话从空列表开始。

---

## 数据流总结

```
用户 Shift+Tab
    │
    ▼
tuiModel.planMode = PlanModePlan
eng.SetPlanMode(PlanModePlan)
    │
    ▼
用户输入任务 → dispatch(userPrompt)
    │
    ▼
engine.runLoop（快照 planMode）
    ├── filterReadOnlyTools（硬性过滤写工具）
    ├── 注入 Plan Mode 前缀 prompt
    └── 正常 ReAct 循环
            │ todo_write 调用
            ▼
        TodoStore.Write（校验状态转换）
        → TUI EventToolResult → updateTodoBlock（追加快照）
            │ 自然停止（EventDone）
            ▼
        planReviewing = true → 审查对话框
            │ 用户按 1/2
            ▼
        planMode = Default
        eng.SetPlanMode(Default)
        autoExecuting = true
        dispatch(execPrompt)
            │ EventDone 后
            ▼
        pending > 0 → dispatch(execContinuePrompt)（循环）
        pending == 0 → autoExecuting = false，完成
```
