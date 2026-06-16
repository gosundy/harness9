# AutoDev — harness9 自举开发闭环

## 1. 设计动机

harness9 已具备完整的 eval 体系和 SWE-bench benchmark，下一步目标是**自举**：让框架本身开发新功能，无需人工编写代码。AutoDev 将 harness9 既有的核心能力——Skills 系统、Sub-Agent 委派、Docker Sandbox、git worktree——组合为一条从「需求描述」到「PR 创建」的完整自动化链路。

```
用户描述需求
     │
     ▼
主 Agent 澄清需求 + 生成 Spec
     │
用户确认 Spec
     │
     ▼
Dev Sub-Agent（Docker Sandbox + git worktree）
     ├── 读规范 → 探索代码 → 实现功能 → 编写测试
     ├── go build + go test 迭代（最多 3 次）
     └── gofmt → git commit → git push → gh pr create
     │
     ▼
主 Agent 在 TUI 展示 PR URL
```

---

## 2. 整体架构

### 2.1 组件关系

```
┌─────────────────────────────────────────────────────────────┐
│                     harness9 TUI                             │
│                                                              │
│  用户: /autodev 实现 X 功能                                   │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐  │
│  │              主 Agent（spec 生成阶段）                   │  │
│  │                                                        │  │
│  │  加载 /autodev AgentSkill ──► 指导主 agent 工作流       │  │
│  │  读取 AGENTS.md ──► 了解项目现状                        │  │
│  │  向用户提 2-3 个澄清问题 ──► 确认功能边界               │  │
│  │  生成结构化 Spec ──► 等待用户「确认」                    │  │
│  └──────────────────────────┬─────────────────────────────┘  │
│                             │ 用户: 确认                      │
│  ┌──────────────────────────▼─────────────────────────────┐  │
│  │              主 Agent（委派阶段）                        │  │
│  │                                                        │  │
│  │  bash: git worktree add .autodev/<slug>                │  │
│  │  task("dev", spec + worktreePath)                      │  │
│  └──────────────────────────┬─────────────────────────────┘  │
└─────────────────────────────┼───────────────────────────────┘
                              │
          ┌───────────────────▼──────────────────────┐
          │         Dev Sub-Agent                     │
          │  ┌─────────────────────────────────────┐  │
          │  │  Docker Sandbox（Go 镜像）            │  │
          │  │  bash 命令 → docker exec → 容器内     │  │
          │  │  文件工具 → bind mount → worktree     │  │
          │  └─────────────────────────────────────┘  │
          │                                            │
          │  git worktree（feature/autodev-<slug>）    │
          │  ├── 读 AGENTS.md，了解编码规范            │
          │  ├── 探索相关代码                          │
          │  ├── 实现功能 + 编写 *_test.go             │
          │  ├── go build ./...                       │
          │  └── 测试循环（≤3次）:                    │
          │      go test ./... ──► FAIL → 修复 → 重试 │
          │                    └── PASS               │
          │                         │                  │
          │  gofmt → git commit → git push → gh pr create │
          └───────────────────────────────────────────┘
```

### 2.2 依赖的核心能力

| 能力层 | 组件 | AutoDev 的使用方式 |
|--------|------|-------------------|
| **Skill 系统** | `internal/skills/` + `skills/autodev/SKILL.md` | 注入到主 agent 的 system prompt，定义三阶段工作流 |
| **Sub-Agent 委派** | `internal/subagent/` + `.harness9/agents/dev.md` | 主 agent 通过 `task` 工具委派 dev 子代理执行编码 |
| **Docker Sandbox** | `internal/sandbox/` | Dev sub-agent 在 Go 镜像容器内运行 `go build/test` |
| **文件工具** | `internal/tools/` | Dev sub-agent 在 worktree 内读写代码文件 |
| **bash 工具** | `internal/tools/bash.go` | 透明路由到容器，执行 git、go、gh 等命令 |

---

## 3. 三阶段工作流

### Phase 1 — 需求澄清

主 agent 加载 `/autodev` skill 后，首先读取 `AGENTS.md` 了解项目模块和规范，然后向用户**每次只提一个**关键澄清问题：

1. **功能边界**：实现什么、明确不实现什么
2. **验收标准**：什么样的测试证明功能完成
3. **影响范围**：新增文件为主还是修改已有模块

2-3 轮澄清后直接进入 Spec 生成，不过度追问。

### Phase 2 — Spec 生成与确认

主 agent 生成结构化 Spec 并展示给用户：

```markdown
## Feature Spec: <标题>

### 功能描述
<简洁描述该功能做什么>

### 实现范围
新增文件：
- internal/<pkg>/<file>.go
- internal/<pkg>/<file>_test.go

修改文件：
- cmd/harness9/main.go（如需注册新工具）

### 验收标准
- [ ] go build ./... 通过
- [ ] go test ./... 通过
- [ ] （如涉及 Agent 行为）internal/evals/dataset/ 新增 eval 用例

### 不在范围内
- <明确列出不做的事>
```

**关键设计**：主 agent 展示 Spec 后**不调用任何工具，直接结束本轮回复**，TUI 自然等待用户输入。只有用户明确输入「确认」后才继续。若用户要求修改，重新生成 Spec 再等待确认，直到通过为止。

### Phase 3 — 前置检查 + 委派

用户确认后，主 agent 依次执行：

**3.1 前置检查**（任一失败则停下等待修复）：
```bash
go version          # 确认 Go ≥1.25
gh auth status      # 确认 gh CLI 已登录
git --version       # 确认 git 可用
# 若 SANDBOX_ENABLED=true，提示检查 SANDBOX_IMAGE 是否为 Go 镜像
```

**3.2 创建 git worktree**：
```bash
git worktree add .autodev/<slug> -b feature/autodev-<slug>
readlink -f .autodev/<slug>   # 获取绝对路径传给 sub-agent
```

**3.3 委派 dev sub-agent**：
```
task("dev", "以下是需要实现的 Feature Spec：\n\n<spec 全文>\n\n工作目录（git worktree 绝对路径）：<worktreePath>")
```

**3.4 处理结果**：
- 成功：展示 `✓ PR 已创建：<URL>`，执行 `git worktree remove .autodev/<slug>`
- 失败：保留 worktree 供排查，告知用户路径，提示 `git worktree remove .autodev/<slug> --force` 手动清理

---

## 4. Dev Sub-Agent 设计

Dev sub-agent（`.harness9/agents/dev.md`）是 autodev 的执行核心，接收 Spec 和 worktreePath，完成完整的开发闭环：

### 路径约定

Dev sub-agent 的工作目录（workDir）是 harness9 根目录，但实际工作区是 git worktree：

| 操作 | 路径形式 |
|------|---------|
| bash 命令 | `cd <worktreePath> && go build ./...` |
| file 工具路径 | `.autodev/<slug>/internal/tools/xxx.go`（相对 workDir）|

### 迭代循环

```
go build ./...
    ├── FAIL → 修复编译错误
    └── PASS
         │
         └── go test ./... -timeout 5m
                  ├── FAIL（第 1/2/3 次）→ 分析错误 → 修复代码 → 重试
                  ├── FAIL（第 3 次后）→ 输出 AUTODEV_RESULT: FAILED
                  └── PASS
                       │
                       └── gofmt -w .
                            → git add -A
                            → git commit -m "feat: <描述>"
                            → git push origin HEAD
                            → gh pr create --base master
                            → 输出 AUTODEV_RESULT: SUCCESS + PR URL
```

### 约束

- 不修改已有 `*_test.go` 中的测试用例（可新增测试函数）
- 不引入 `go.mod` 中没有的新依赖
- `git commit` message 必须以 `feat:` 开头
- 3 次迭代后仍失败，诚实报告失败，不伪造 PASS

---

## 5. git worktree 隔离设计

AutoDev 使用 git worktree 而非直接在主仓库改代码，原因：

| 场景 | git worktree 的保障 |
|------|-------------------|
| Dev sub-agent 改坏代码 | 不影响主分支，worktree 是独立的文件树 |
| 测试失败、中途终止 | 保留 worktree 供排查，主仓库干净 |
| 并发多个 autodev 任务 | 每个 slug 有独立目录，互不干扰 |
| 成功完成 | `git worktree remove` 清理，PR 已推送到远端 |

Worktree 路径约定：`.autodev/<slug>/`，已加入 `.gitignore`，不会污染主仓库的 `git status`。

---

## 6. Docker Sandbox 集成

当 `SANDBOX_ENABLED=true` 时，dev sub-agent 的 bash 工具自动路由到 Docker 容器：

```
dev sub-agent
    │
    bash("cd <worktreePath> && go test ./...")
    │
    ├── SANDBOX_ENABLED=false → 本地进程执行
    └── SANDBOX_ENABLED=true  → docker exec → Go 镜像容器
                                         ↕ bind mount
                                    worktree 目录（共享文件）
```

**前置要求**：默认 Sandbox 镜像（`ubuntu:22.04`）不含 Go，需在 `.env` 中配置：

```bash
SANDBOX_ENABLED=true
SANDBOX_IMAGE=golang:1.25-bookworm
```

若不使用 Docker（`SANDBOX_ENABLED=false` 或不设置），dev sub-agent 在本地进程中运行 `go test`，只需确保本机已安装 Go 1.25+。

---

## 7. 使用指南

### 7.1 前置条件

| 条件 | 说明 |
|------|------|
| harness9 运行中 | 在项目根目录启动 `go run ./cmd/harness9` |
| `gh` CLI 已登录 | `gh auth status` 显示 Logged in |
| Go 已安装 | `go version` ≥ 1.25 |
| `SANDBOX_IMAGE` 配置（可选） | 需要 Docker 隔离时设置为 `golang:1.25-bookworm` |

### 7.2 使用示例

```
# 在 harness9 TUI 中输入：
/autodev 实现一个 token_count 工具，返回输入文本的 token 估算数量

# 主 agent 开始澄清（示例对话）：
> 这个工具需要支持哪种 tokenizer？只做简单的空格/字符估算，还是接入真实的 tiktoken？

用户: 简单估算，用字符数除以 4 近似

> 好的。这个工具是否需要支持多语言（如中文按字符计数）？

用户: 是的，中文按字符计数，英文按 word 计数

# 主 agent 生成 Spec 并展示，等待确认...

用户: 确认

# 主 agent 创建 worktree，委派 dev sub-agent
# Dev sub-agent 开始工作...（TUI 实时显示进度）

# 完成后：
✓ PR 已创建：https://github.com/ZhangShenao/harness9/pull/42
```

### 7.3 失败时的排查

如果 dev sub-agent 3 次迭代后仍未通过测试：

```bash
# worktree 被保留，进入查看当前状态
cd .autodev/<slug>
go test ./... -v                        # 查看具体失败
git diff HEAD                           # 查看当前改动
git log --oneline -5                    # 查看提交历史

# 修复后手动提交、推送
git add -A && git commit -m "fix: ..."
git push origin HEAD
gh pr create --base master

# 清理 worktree
cd -
git worktree remove .autodev/<slug> --force
```

---

## 8. 文件位置速查

| 文件 | 职责 |
|------|------|
| `skills/autodev/SKILL.md` | `/autodev` AgentSkill：三阶段工作流指令 |
| `.harness9/agents/dev.md` | Dev Sub-Agent 定义：编码→测试→PR 流程（本地配置，不提交 git）|
| `.gitignore` 中 `.autodev/` | 忽略 git worktree 临时目录 |
| `internal/skills/loader.go` | Skills 加载器（`skills/<name>/SKILL.md` → `Index`）|
| `internal/subagent/runner.go` | Sub-Agent Runner（构建隔离子引擎 + Docker Sandbox）|
| `internal/sandbox/` | Docker 容器级隔离基础设施 |
