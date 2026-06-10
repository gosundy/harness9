# Benchmark — SWE-bench 评估体系

## 1. 技术背景

### 1.1 为什么需要 Benchmark

harness9 的核心价值在于「编排 Agent 解决真实工程问题」，而不是通过静态单元测试就能验证的能力。因此需要一个**客观、可量化、与主流系统可比较**的评估体系，来回答：

> harness9 驱动的 LLM Agent，在真实软件工程任务上的能力边界在哪里？

### 1.2 SWE-bench 概述

[SWE-bench](https://github.com/princeton-nlp/SWE-bench)（Software Engineering Benchmark）由 Princeton NLP 团队构建，是当前 Agent 能力评估的业界权威标准。

**数据来源**：从 GitHub 上 12 个主流 Python 开源项目（Django、Flask、Requests、Sympy 等）中，收集真实的 Issue + 对应的 PR（commit），确保每个 Issue 都有已知的正确修复。

**评估方式**：
- 给 Agent 一个 Issue 描述（`problem_statement`）和一个仓库快照（`base_commit`）
- Agent 自主探索代码、定位 bug、生成修复 patch
- 官方评估器在沙箱中将 patch 应用到仓库，运行原始测试套件，判断是否 **Resolved**

**指标**：`% Resolved` —— 成功修复的 Instance 数 / 总 Instance 数。

### 1.3 数据集规模

| 版本 | Instance 数 | 说明 |
|------|------------|------|
| **SWE-bench** | 2,294 | 完整集，权威但运行成本高 |
| **SWE-bench Lite** | 300 | 精选子集，平衡难度，主流评测首选 |
| **SWE-bench Verified** | 500 | 人工验证子集，信噪比最高 |

harness9 当前针对 **SWE-bench Lite** 运行，并支持按 repo 分类抽样（默认每类 10 条），平衡成本与覆盖面。

### 1.4 主流系统参考分数（SWE-bench Lite）

| 系统 | % Resolved | 备注 |
|------|-----------|------|
| SWE-agent (GPT-4) | ~18% | 经典 ReAct agent |
| Devin | ~14% | 早期 AI 软件工程师 |
| Claude 3.5 Sonnet | ~49% | Anthropic 官方结果 |
| OpenHands (CodeAct) | ~26% | 开源框架 |
| harness9 | TBD | 待跑分 |

---

## 2. Benchmark 核心原理

SWE-bench 的执行分为**两个完全解耦的阶段**：

```
┌─────────────────────────────────────────────────────────────────┐
│                     Phase 1: Inference（推断）                    │
│                                                                  │
│   ┌──────────┐    problem_statement    ┌─────────────────────┐  │
│   │ Dataset  │ ──────────────────────► │   harness9 Runner   │  │
│   │ (JSONL)  │    base_commit          │   (cmd/swebench/)   │  │
│   └──────────┘                        └──────────┬──────────┘  │
│                                                  │              │
│                                            git diff             │
│                                                  │              │
│                                                  ▼              │
│                                        predictions.jsonl        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Phase 2: Evaluation（评估）                   │
│                                                                  │
│  predictions.jsonl ──► swebench Python 包 ──► 官方 Docker 镜像  │
│                                                      │           │
│                                              apply patch         │
│                                              run test suite      │
│                                                      │           │
│                                                      ▼           │
│                                            % Resolved 分数       │
└─────────────────────────────────────────────────────────────────┘
```

**解耦的好处**：
- Inference 和 Evaluation 可以完全独立运行，无需在 Agent 执行期间安装测试套件
- 可先积累所有 `predictions.jsonl`，再批量评估
- 官方 Docker 镜像保证评估环境一致性，避免本地环境差异影响分数

### 2.1 数据集格式

SWE-bench Lite 以 JSONL 格式分发，每行一个 Instance：

```json
{
  "instance_id": "django__django-11179",
  "repo": "django/django",
  "base_commit": "9b224f172a30d38d8b4e7b38a4e2ee47faaf4019",
  "problem_statement": "Autoreloader with StatReloader doesn't work properly...",
  "hints_text": "",
  "test_patch": "diff --git a/tests/utils_tests/test_autoreload.py..."
}
```

| 字段 | 说明 | 是否传给 Agent |
|------|------|:------------:|
| `instance_id` | 唯一标识符（`repo__hash` 格式） | 否 |
| `repo` | GitHub 仓库路径（`owner/name`） | 否（用于 clone）|
| `base_commit` | 问题存在的提交 hash | 否（用于 checkout）|
| `problem_statement` | Issue 描述（Agent 看到的任务输入） | **是** |
| `hints_text` | 可选提示（部分 Instance 有） | 否（不传给 Agent）|
| `test_patch` | 用于验证的测试改动（官方评估器专用） | **绝对不传** |

> `test_patch` 是评估的黄金标准，绝不能泄露给 Agent，否则污染分数。

### 2.2 Prediction 格式

Runner 输出 `predictions.jsonl`，每行一条：

```json
{"instance_id": "django__django-11179", "model_patch": "diff --git a/django/utils/autoreload.py..."}
```

`model_patch` 为空字符串时，评估器将该 Instance 标记为 **Unresolved**（Agent 无改动或失败）。

---

## 3. harness9 的设计

### 3.1 整体架构

```
cmd/swebench/
├── main.go       CLI 入口：flag 解析、preflight、数据集加载、并发编排
├── runner.go     单 instance 执行核心：git → sandbox → engine → patch
├── dataset.go    JSONL 加载、按 repo 分类随机采样
├── prompt.go     SWE-bench 专用 system prompt（结构化流程约束）
└── report.go     predictions.jsonl 追加写、run_summary.md 生成
```

### 3.2 单 Instance 执行流程

```
runInstance(ctx, inst, cfg)
│
├─ 1. os.MkdirTemp → tmpDir（defer RemoveAll 保证清理）
│
├─ 2. git clone https://github.com/<inst.Repo> tmpDir  [5min timeout]
│      git -C tmpDir checkout <inst.BaseCommit>         [30s timeout]
│
├─ 3. sandbox.Manager.Create(tmpDir)                    [60s timeout]
│      → DockerEnvironment（bind mount 共享 tmpDir）
│      → defer DestroyAll（独立清理 context，不受取消影响）
│
├─ 4. tools.Registry 注册四个工具：
│      bash（路由进容器）
│      read_file / write_file / edit_file（bind mount，宿主机侧 IO）
│
├─ 5. engine.NewAgentEngine(llm, hookReg, tmpDir,
│        WithPromptBuilder(&swebenchPromptBuilder{inst}))
│        // MaxTurns > 0 时才附加 WithMaxTurns，否则沿用引擎默认值（500）
│
├─ 6. runWithTrajectory(instanceCtx, eng, prompt, logPath, inst)  [per-instance timeout]
│      → engine.RunStream() 消费所有事件，写入 logs/<instance_id>.log
│      → Benchmark 模式自动批准所有工具审批（无人值守）
│
├─ 7. git diff（独立 context.Background，Ctrl+C 安全）  [10s timeout]
│      → patch string
│
└─ 8. return RunResult{Instance, Patch, Error, Duration}
```

**关键设计决策**：

| 决策 | 理由 |
|------|------|
| git clone 在宿主机执行 | 容器内无 git credential，bind mount 后容器可见相同文件 |
| bash 工具路由进容器 | Agent 执行的 Python 代码在隔离环境中运行，不污染宿主机 |
| git diff 用独立 context | Ctrl+C 取消主 context 后，仍能收集已修改的 patch，避免丢弃有效结果 |
| MaxTurns 默认沿用引擎值（500） | SWE-bench 复杂任务不应被过早截断，使用 `--max-turns N` 显式限制 |
| RunStream 替代 Run | 以事件流方式捕获完整 trajectory，写入 `logs/` 目录供后续分析 |
| predictions.jsonl 追加写 | 每条完成后立即 flush，配合 `--resume` 支持断点续跑 |

### 3.3 采样策略

SWE-bench Lite 覆盖 11 个 Python 仓库，harness9 按 repo 分类后随机采样：

```
allInstances (300条)
    │
    ├─ 按 repo 分组 → 11 个分组（astropy/astropy, django/django, ...）
    │
    ├─ 每组内随机打乱（seed 固定 → 可重复）
    │
    ├─ 每组取前 min(n, groupSize) 条
    │
    └─ 合并后整体打乱 → 均衡分布，并发时不集中于单一 repo
```

这样能在控制总量的同时，保证跨仓库的多样性（不同语言特性、项目规模、bug 类型）。

### 3.4 专用 System Prompt 设计

harness9 为 SWE-bench 设计了专用的 system prompt，策略为：

**结构化流程约束（5 步顺序）+ 每步内自由探索（不限制工具调用方式）**

```
Step 1 — 理解问题
  ↓ 识别 bug 核心、复现步骤、预期行为

Step 2 — 探索仓库
  ↓ find/grep 定位相关文件，阅读源码（不读测试文件）

Step 3 — 复现
  ↓ 写最简复现脚本，用 bash 验证 bug 存在

Step 4 — 修复
  ↓ 最小化改动，绝不修改测试文件，不引入新依赖

Step 5 — 验证
  ↓ 重跑复现脚本，确认 bug 消失
```

**约束的设计原则**：
- "不修改测试文件"是 SWE-bench 的硬约束，违反会导致评估结果无效
- "最小化改动"减少误改不相关代码引入回归的风险
- 结构化步骤确保 Agent 不跳过复现步骤直接猜测修复，提升修复质量

### 3.5 并发控制与韧性

```
主循环（main.go）

sem := semaphore.NewWeighted(N)   ← --parallel N 控制最大并发

for each instance:
    sem.Acquire(ctx, 1)            ← 获取槽位（超出 N 时阻塞）
    go func:
        result = runInstance(...)  ← 每个 goroutine 完全独立
        mu.Lock()
        results = append(...)
        appendPrediction(...)      ← 立即写入，不等全部完成
        mu.Unlock()
        sem.Release(1)             ← 释放槽位

wg.Wait()                          ← 等所有 goroutine 完成
writeSummary(...)
```

**韧性机制**：

| 场景 | 处理方式 |
|------|---------|
| git clone 失败 | 记录 Error，写空 patch，继续下一条 |
| Docker 启动失败 | 同上 |
| LLM API 超时/限流 | 同上；若有部分 patch 则保留 |
| MaxTurns 触发 | 收集当前 git diff，不标记为错误 |
| 整体 Ctrl+C | 等待当前 instance 完成，收集 patch 后退出 |
| `--resume` 重启 | 读取已有 predictions.jsonl，跳过已处理 instance_id |

---

## 4. 完整操作流程

### 4.1 前置条件

**推荐方式**：在项目根目录创建 `.env` 文件（与 harness9 主程序共用同一套配置，runner 启动时自动从当前工作目录加载）：

```bash
# harness9/.env
OPENAI_API_KEY=sk-...
OPENAI_BASE_URL=https://openrouter.ai/api/v1   # 可选，接入 OpenRouter / Azure 等
LLM_MODEL=openai/gpt-4o
SANDBOX_IMAGE=python:3.11-slim                  # 推荐，Python 3.11 更适合 SWE-bench
```

**也可通过系统环境变量提供**（系统变量优先于 `.env`）：

```bash
export OPENAI_API_KEY=sk-...
export LLM_MODEL=openai/gpt-4o
```

确认 Docker 守护进程运行中：

```bash
docker info
```

### 4.2 下载数据集

```bash
pip install datasets

python -c "
from datasets import load_dataset
ds = load_dataset('princeton-nlp/SWE-bench_Lite', split='test')
ds.to_json('swe-bench-lite.jsonl')
print(f'下载完成: {len(ds)} 条 instances')
"
```

> 也可以直接从 Hugging Face Hub 下载 JSONL 文件：
> `https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite`

### 4.3 按类别抽样运行（推荐初次运行）

配置好 `.env` 后直接运行（runner 从当前目录自动加载）：

```bash
cd /path/to/harness9

go run ./cmd/swebench \
  --dataset swe-bench-lite.jsonl \
  --sample 10 \       # 每个 repo 取 10 条，约 110 条总量
  --output ./swebench-results \
  --parallel 2 \      # 同时运行 2 个 instance
  --timeout 15        # 每个 instance 超时 15 分钟（--max-turns 默认 0 = 不限轮数）
```

如果没有 `.env` 文件，也可直接通过环境变量传入：

```bash
OPENAI_API_KEY=sk-... LLM_MODEL=openai/gpt-4o go run ./cmd/swebench \
  --dataset swe-bench-lite.jsonl --sample 10
```

运行期间，stderr 输出进度：

```
数据集加载完成: 300 条 instances
采样完成: 110 条（每 repo 最多 10 条）
[start] django__django-11179
[start] astropy__astropy-12345
[done]  django__django-11179 (4m32s) patch=1842 bytes
[done]  astropy__astropy-12345 (6m10s) patch=0 bytes   ← 空 patch
[error] flask__flask-5678 (15m0s): context deadline exceeded
...
完成！结果已写入 ./swebench-results
总实例: 110，耗时: 3h21m
```

### 4.4 断点续跑

如果运行中途中断（网络故障、Ctrl+C 等），用 `--resume` 跳过已有结果：

```bash
go run ./cmd/swebench \
  --dataset swe-bench-lite.jsonl \
  --output ./swebench-results \
  --resume              # 自动跳过 predictions.jsonl 中已有的 instance_id
```

### 4.5 查看中间结果

```
swebench-results/
├── predictions.jsonl        # 每行一条 {"instance_id":..., "model_patch":...}
├── run_summary.md           # 运行摘要（总数/patch 数/出错数/按 repo 分布）
└── logs/
    ├── django__django-12908.log   # 每个 instance 的完整 trajectory
    ├── astropy__astropy-5678.log
    └── ...
```

**Trajectory 日志格式**（`logs/<instance_id>.log`）：

```
=== SWE-bench Instance: django__django-12908 ===
Repo:        django/django
BaseCommit:  abc123...
StartTime:   2026-06-09 14:30:00

--- Turn 1 ---
Let me start by exploring the repository structure to understand the codebase...

[Tool Call: bash]
{"command":"find . -type f -name \"*.py\" | grep -v __pycache__ | head -40"}

[Tool Result: abc12345 | 350ms | ok]
./django/utils/autoreload.py
./django/core/management/base.py
...

[Tokens: 4821]

--- Turn 2 ---
I can see the issue in autoreload.py. Let me read the relevant section...
```

日志包含：每轮 LLM 输出文本、工具调用参数、工具返回结果（含耗时和状态）、token 用量、上下文压缩事件。

`run_summary.md` 示例：

```markdown
# SWE-bench Lite Run Summary

- 开始时间: 2026-06-09 14:30:00
- 结束时间: 2026-06-09 17:51:00
- 总实例数: 110
- 成功生成 patch: 89 / 110
- 空 patch（agent 无改动）: 14
- 运行出错: 7

## 按 Repo 分布
| Repo              | 实例数 | 有 patch | 空 patch | 出错 |
|-------------------|--------|---------|---------|------|
| astropy/astropy   | 10     | 8       | 2       | 0    |
| django/django     | 10     | 9       | 1       | 0    |
| ...               | ...    | ...     | ...     | ...  |
```

### 4.6 官方评估打分

Runner 完成后，使用官方 `swebench` 工具评估分数：

```bash
pip install swebench

python -m swebench.harness.run_evaluation \
    --dataset_name princeton-nlp/SWE-bench_Lite \
    --predictions_path ./swebench-results/predictions.jsonl \
    --max_workers 4 \           # 并发评估的 instance 数
    --run_id harness9-lite-v1   # 本次运行标识符（影响输出目录名）
```

评估完成后查看结果：

```bash
# 官方工具将结果输出到 logs/run_evaluation/harness9-lite-v1/
cat logs/run_evaluation/harness9-lite-v1/results.json
# {"resolved": 23, "unresolved": 77, "error": 10, "total": 110}
# Resolved Rate: 20.9%
```

> **注意**：官方评估器需要拉取每个 Instance 对应的官方 Docker 镜像（约 1–5 GB/镜像），首次运行会耗费大量带宽和磁盘空间（数百 GB）。建议在有足够磁盘的环境中运行，或使用 `--max_workers 1` 串行评估节省资源。

### 4.7 全量运行（与公榜对比）

运行全部 300 个 instance，与 SWE-agent、Claude 等系统的公开结果直接对比：

```bash
go run ./cmd/swebench \
  --dataset swe-bench-lite.jsonl \
  --sample 300 \        # 不限制，取全量
  --output ./swebench-results-full \
  --max-turns 30 \
  --parallel 3 \
  --timeout 15
```

> **成本估算（GPT-4o）**：
> - 每个 instance 平均约 15–20 轮 LLM 调用，约 $1–3 API 费用
> - 300 个 instance 总计约 $300–900
> - 建议先用 `--sample 10` 验证流程后再跑全量

---

## 5. 参数速查

```bash
go run ./cmd/swebench --help
```

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--dataset` | string | **必填** | SWE-bench Lite JSONL 文件路径 |
| `--sample` | int | 10 | 每个 repo 抽取的 instance 数量（≥1）|
| `--output` | string | `./swebench-results` | 输出目录 |
| `--max-turns` | int | 0 | 每个 instance 最大 Turn 数（0 = 引擎默认 500）|
| `--parallel` | int | 1 | 并发 instance 数（≥1）|
| `--resume` | bool | false | 跳过已有结果（断点续跑）|
| `--timeout` | int | 10 | 单个 instance 超时（分钟）|
| `--model` | string | `""` | LLM 模型（空则读 `LLM_MODEL` 环境变量）|

**环境变量**（可通过 `.env` 文件或系统环境变量提供，系统变量优先）：

| 变量 | 说明 | 推荐值 |
|------|------|--------|
| `OPENAI_API_KEY` | LLM API Key（必填）| — |
| `OPENAI_BASE_URL` | 自定义 API 地址（可选）| `https://openrouter.ai/api/v1` |
| `LLM_MODEL` | 模型名称 | `openai/gpt-4o` |
| `SANDBOX_IMAGE` | Docker 镜像 | `python:3.11-slim` |
| `SANDBOX_ENABLED` | 启用 Docker 隔离 | `true`（默认）|

---

## 6. 代码位置速查

| 文件 | 职责 |
|------|------|
| `cmd/swebench/main.go` | CLI 入口、preflight、并发编排 |
| `cmd/swebench/runner.go` | 单 instance 执行核心（git/sandbox/engine/patch）|
| `cmd/swebench/dataset.go` | 数据集加载、按 repo 采样 |
| `cmd/swebench/prompt.go` | SWE-bench 专用 system prompt |
| `cmd/swebench/report.go` | 输出文件管理（predictions/summary）|
| `cmd/swebench/*_test.go` | 各模块单元测试 |
| `internal/sandbox/` | Docker 沙箱基础设施 |
| `internal/engine/` | ReAct Agent Loop |
| `docs/设计规格/2026-06-09-swebench-lite-runner-design.md` | 完整设计文档 |
