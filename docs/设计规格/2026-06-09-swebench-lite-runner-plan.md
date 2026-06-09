# SWE-bench Lite Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `cmd/swebench/` 下实现一个独立的 CLI 工具，加载 SWE-bench Lite 数据集、按 repo 抽样、对每个 instance 用 harness9 引擎生成 patch，输出官方兼容的 `predictions.jsonl` 供 `swebench` Python 包打分。

**Architecture:** 5 个文件各司其职：`dataset.go`（数据加载/采样）、`prompt.go`（专用 system prompt）、`report.go`（输出文件管理）、`runner.go`（单 instance 执行）、`main.go`（CLI 编排）。复用现有 `sandbox.Manager`、`engine.NewAgentEngine`、`tools.NewRegistry` 等基础设施，不修改任何现有包。

**Tech Stack:** Go 标准库 + `golang.org/x/sync/semaphore`（并发控制）；`github.com/harness9/internal/engine`、`sandbox`、`tools`、`hooks`、`provider`。

---

## 文件清单

| 文件 | 职责 |
|------|------|
| `cmd/swebench/main.go` | CLI 入口：flag 解析、preflight check、加载/采样数据集、并发编排、汇总 |
| `cmd/swebench/dataset.go` | `Instance`/`Prediction`/`RunResult` 类型；`loadDataset`；`sampleByRepo` |
| `cmd/swebench/prompt.go` | `swebenchPromptBuilder`（实现 `engine.PromptBuilder`）；`sweBenchPromptTemplate` |
| `cmd/swebench/report.go` | `loadExistingIDs`；`appendPrediction`；`writeSummary` |
| `cmd/swebench/runner.go` | `Config` 结构体；`runInstance`；`newProvider` |
| `cmd/swebench/dataset_test.go` | `loadDataset` + `sampleByRepo` 单元测试 |
| `cmd/swebench/prompt_test.go` | `swebenchPromptBuilder.Build()` 单元测试 |
| `cmd/swebench/report_test.go` | `loadExistingIDs` + `appendPrediction` 单元测试 |

---

## Task 1: dataset.go — 类型定义、JSONL 加载、采样

**Files:**
- Create: `cmd/swebench/dataset_test.go`
- Create: `cmd/swebench/dataset.go`

- [ ] **Step 1: 写失败测试**

```go
// cmd/swebench/dataset_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDataset(t *testing.T) {
	content := `{"instance_id":"django__django-1","repo":"django/django","base_commit":"abc123","problem_statement":"Fix bug A","hints_text":""}
{"instance_id":"astropy__astropy-2","repo":"astropy/astropy","base_commit":"def456","problem_statement":"Fix bug B","hints_text":"hint"}
`
	tmp := filepath.Join(t.TempDir(), "lite.jsonl")
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	instances, err := loadDataset(tmp)
	if err != nil {
		t.Fatalf("loadDataset error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("want 2 instances, got %d", len(instances))
	}
	if instances[0].InstanceID != "django__django-1" {
		t.Errorf("want django__django-1, got %s", instances[0].InstanceID)
	}
	if instances[1].ProblemStatement != "Fix bug B" {
		t.Errorf("want 'Fix bug B', got %s", instances[1].ProblemStatement)
	}
}

func TestLoadDatasetFileNotFound(t *testing.T) {
	_, err := loadDataset("/nonexistent/path.jsonl")
	if err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}

func TestSampleByRepo(t *testing.T) {
	instances := []Instance{
		{InstanceID: "django-1", Repo: "django/django"},
		{InstanceID: "django-2", Repo: "django/django"},
		{InstanceID: "django-3", Repo: "django/django"},
		{InstanceID: "astropy-1", Repo: "astropy/astropy"},
		{InstanceID: "astropy-2", Repo: "astropy/astropy"},
		{InstanceID: "flask-1", Repo: "pallets/flask"},
	}

	sampled := sampleByRepo(instances, 2, 42)

	// 每个 repo 最多取 2 条，共 3 repo → 最多 6 条
	// django 3条取2, astropy 2条取2, flask 1条取1 → 共 5 条
	if len(sampled) != 5 {
		t.Fatalf("want 5 sampled instances, got %d", len(sampled))
	}

	// 验证 django 不超过 2 条
	count := make(map[string]int)
	for _, inst := range sampled {
		count[inst.Repo]++
	}
	if count["django/django"] > 2 {
		t.Errorf("django sample exceeds limit: %d", count["django/django"])
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /path/to/harness9
go test ./cmd/swebench/... -v 2>&1 | head -20
```

预期：`no Go files in ... cmd/swebench` 或 `cannot find package`（文件尚不存在）

- [ ] **Step 3: 实现 dataset.go**

```go
// cmd/swebench/dataset.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"
)

// Instance 是 SWE-bench Lite 数据集的一条记录（JSONL 格式）。
type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	HintsText        string `json:"hints_text"`
}

// Prediction 是写入 predictions.jsonl 的一条记录（官方兼容格式）。
type Prediction struct {
	InstanceID string `json:"instance_id"`
	ModelPatch string `json:"model_patch"`
}

// RunResult 记录单个 instance 的运行结果，供汇总使用。
type RunResult struct {
	Instance   Instance
	Patch      string
	Error      error
	Duration   time.Duration
}

// loadDataset 从 JSONL 文件加载所有 instance。
func loadDataset(path string) ([]Instance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开数据集失败: %w", err)
	}
	defer f.Close()

	var instances []Instance
	scanner := bufio.NewScanner(f)
	// SWE-bench 行可能较长，扩大 buffer 到 10MB
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var inst Instance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			return nil, fmt.Errorf("解析 JSONL 行失败: %w", err)
		}
		instances = append(instances, inst)
	}
	return instances, scanner.Err()
}

// sampleByRepo 按 repo 分组，每组随机取最多 n 条，打乱后返回。
// seed 用于可重复抽样（测试时传固定值，生产时传 time.Now().UnixNano()）。
func sampleByRepo(instances []Instance, n int, seed int64) []Instance {
	byRepo := make(map[string][]Instance)
	for _, inst := range instances {
		byRepo[inst.Repo] = append(byRepo[inst.Repo], inst)
	}

	rng := rand.New(rand.NewSource(seed))

	var sampled []Instance
	for _, group := range byRepo {
		rng.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
		if len(group) > n {
			group = group[:n]
		}
		sampled = append(sampled, group...)
	}
	// 打乱最终列表，避免并发时 repo 集中
	rng.Shuffle(len(sampled), func(i, j int) { sampled[i], sampled[j] = sampled[j], sampled[i] })
	return sampled
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./cmd/swebench/... -run TestLoadDataset -v
go test ./cmd/swebench/... -run TestSampleByRepo -v
```

预期：`PASS`

- [ ] **Step 5: 提交**

```bash
git add cmd/swebench/dataset.go cmd/swebench/dataset_test.go
git commit -m "feat(swebench): dataset.go — Instance 类型 + loadDataset + sampleByRepo"
```

---

## Task 2: prompt.go — SWE-bench 专用 System Prompt

**Files:**
- Create: `cmd/swebench/prompt_test.go`
- Create: `cmd/swebench/prompt.go`

- [ ] **Step 1: 写失败测试**

```go
// cmd/swebench/prompt_test.go
package main

import (
	"strings"
	"testing"
)

func TestSwebenchPromptBuilder(t *testing.T) {
	inst := Instance{
		InstanceID:       "django__django-99",
		ProblemStatement: "There is a bug in QuerySet.filter() when using complex Q objects.",
	}
	b := &swebenchPromptBuilder{instance: inst}
	prompt := b.Build()

	if !strings.Contains(prompt, inst.ProblemStatement) {
		t.Error("prompt should contain the problem statement")
	}
	if strings.Contains(prompt, "{{PROBLEM_STATEMENT}}") {
		t.Error("prompt should not contain unreplaced placeholder")
	}
	if !strings.Contains(prompt, "Step 1") {
		t.Error("prompt should contain structured workflow steps")
	}
	if !strings.Contains(prompt, "不修改测试文件") {
		t.Error("prompt should contain constraint about not modifying test files")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./cmd/swebench/... -run TestSwebenchPromptBuilder -v
```

预期：`undefined: swebenchPromptBuilder`

- [ ] **Step 3: 实现 prompt.go**

```go
// cmd/swebench/prompt.go
package main

import "strings"

// sweBenchPromptTemplate 是 SWE-bench 专用的 agent 指令模板。
// 策略：结构化流程约束（5步顺序执行）+ 每步内自由探索（不限制工具调用方式）。
const sweBenchPromptTemplate = `你是一名资深软件工程师，正在处理一个真实的 GitHub Issue。
你的目标是在当前代码仓库中找到并修复这个问题，生成一个干净、最小化的 patch。

工作目录已设置为仓库根目录（base_commit 状态）。

## 工作流程

按以下步骤顺序执行：

### Step 1 — 理解问题
仔细阅读 Issue 描述，识别：
- 核心 bug 或缺失行为是什么
- 复现步骤（如有）
- 预期行为 vs 实际行为

### Step 2 — 探索仓库
用工具充分了解相关代码：
- ` + "`find . -type f -name \"*.py\" | grep -v __pycache__ | head -60`" + ` 了解项目结构
- ` + "`grep -r \"<关键词>\" --include=\"*.py\" -l`" + ` 定位相关文件
- 阅读最相关的源文件（不是测试文件）

### Step 3 — 复现
如果 Issue 提供了复现步骤，用 bash 写一个最简单的复现脚本验证问题存在。

### Step 4 — 修复
实现修复：
- **最小化改动**：只修改导致 bug 的代码，不做无关重构或风格修改
- **不修改测试文件**：绝不改动 test_*.py / *_test.py 文件
- **不引入新依赖**：不修改 requirements.txt / setup.py / pyproject.toml

### Step 5 — 验证
重新运行 Step 3 的复现脚本，确认 bug 已修复，输出符合预期。

## 完成条件
确认修复有效后立即停止。不要做额外的清理、注释或重构。

---

## Issue

{{PROBLEM_STATEMENT}}`

// swebenchPromptBuilder 实现 engine.PromptBuilder 接口，
// 将当前 instance 的 problem statement 注入 system prompt 模板。
type swebenchPromptBuilder struct {
	instance Instance
}

// Build 返回注入了 problem statement 的完整 system prompt。
func (b *swebenchPromptBuilder) Build() string {
	return strings.ReplaceAll(sweBenchPromptTemplate, "{{PROBLEM_STATEMENT}}", b.instance.ProblemStatement)
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./cmd/swebench/... -run TestSwebenchPromptBuilder -v
```

预期：`PASS`

- [ ] **Step 5: 提交**

```bash
git add cmd/swebench/prompt.go cmd/swebench/prompt_test.go
git commit -m "feat(swebench): prompt.go — SWE-bench 专用 system prompt"
```

---

## Task 3: report.go — 输出文件管理

**Files:**
- Create: `cmd/swebench/report_test.go`
- Create: `cmd/swebench/report.go`

- [ ] **Step 1: 写失败测试**

```go
// cmd/swebench/report_test.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExistingIDsEmpty(t *testing.T) {
	ids, err := loadExistingIDs("/nonexistent/predictions.jsonl")
	if err != nil {
		t.Fatalf("want nil error for missing file, got %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("want empty map, got %v", ids)
	}
}

func TestLoadExistingIDs(t *testing.T) {
	content := `{"instance_id":"django__django-1","model_patch":"diff ..."}
{"instance_id":"astropy__astropy-2","model_patch":""}
`
	tmp := filepath.Join(t.TempDir(), "predictions.jsonl")
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	ids, err := loadExistingIDs(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ids["django__django-1"] {
		t.Error("want django__django-1 in existing IDs")
	}
	if !ids["astropy__astropy-2"] {
		t.Error("want astropy__astropy-2 in existing IDs")
	}
	if ids["unknown"] {
		t.Error("want unknown NOT in existing IDs")
	}
}

func TestAppendPrediction(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "predictions.jsonl")

	p1 := Prediction{InstanceID: "django__django-1", ModelPatch: "diff line 1"}
	p2 := Prediction{InstanceID: "astropy__astropy-2", ModelPatch: ""}

	if err := appendPrediction(tmp, p1); err != nil {
		t.Fatalf("append p1 error: %v", err)
	}
	if err := appendPrediction(tmp, p2); err != nil {
		t.Fatalf("append p2 error: %v", err)
	}

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}

	var got Prediction
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal line 1: %v", err)
	}
	if got.InstanceID != "django__django-1" {
		t.Errorf("want django__django-1, got %s", got.InstanceID)
	}
}

func TestWriteSummary(t *testing.T) {
	outDir := t.TempDir()
	results := []RunResult{
		{Instance: Instance{InstanceID: "a", Repo: "django/django"}, Patch: "diff ...", Duration: time.Second},
		{Instance: Instance{InstanceID: "b", Repo: "django/django"}, Patch: "", Duration: time.Second},
		{Instance: Instance{InstanceID: "c", Repo: "astropy/astropy"}, Error: fmt.Errorf("clone failed"), Duration: time.Second},
	}
	start := time.Now().Add(-5 * time.Minute)
	end := time.Now()
	if err := writeSummary(outDir, results, start, end); err != nil {
		t.Fatalf("writeSummary error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "run_summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "总实例数: 3") {
		t.Errorf("summary should contain total count 3, got:\n%s", content)
	}
	if !strings.Contains(content, "django/django") {
		t.Error("summary should contain django/django repo")
	}
}
```

注意：`report_test.go` 用了 `fmt.Errorf`，需要在文件顶部加 `"fmt"` 导入。

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./cmd/swebench/... -run "TestLoadExisting|TestAppend|TestWriteSummary" -v
```

预期：`undefined: loadExistingIDs` 等编译错误

- [ ] **Step 3: 实现 report.go**

```go
// cmd/swebench/report.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

// loadExistingIDs 读取已有的 predictions.jsonl，返回已处理的 instance_id 集合。
// 文件不存在时返回空 map（不报错），支持首次运行。
func loadExistingIDs(path string) (map[string]bool, error) {
	ids := make(map[string]bool)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return ids, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var p Prediction
		if err := json.Unmarshal([]byte(scanner.Text()), &p); err == nil && p.InstanceID != "" {
			ids[p.InstanceID] = true
		}
	}
	return ids, scanner.Err()
}

// appendPrediction 将单条 Prediction 追加写入 predictions.jsonl（立即 flush）。
func appendPrediction(path string, p Prediction) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("打开 predictions 文件失败: %w", err)
	}
	defer f.Close()
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

const summaryTmpl = `# SWE-bench Lite Run Summary

- 开始时间: {{.StartTime}}
- 结束时间: {{.EndTime}}
- 总实例数: {{.Total}}
- 成功生成 patch: {{.WithPatch}} / {{.Total}}
- 空 patch（agent 无改动）: {{.EmptyPatch}}
- 运行出错: {{.Errors}}

## 按 Repo 分布

| Repo | 实例数 | 有 patch | 空 patch | 出错 |
|------|--------|---------|---------|------|
{{- range .Repos}}
| {{.Name}} | {{.Total}} | {{.WithPatch}} | {{.EmptyPatch}} | {{.Errors}} |
{{- end}}

## 评估命令

` + "```" + `bash
pip install swebench
python -m swebench.harness.run_evaluation \
    --dataset_name princeton-nlp/SWE-bench_Lite \
    --predictions_path ./swebench-results/predictions.jsonl \
    --max_workers 4 \
    --run_id harness9-lite-v1
` + "```" + `
`

type repoStats struct {
	Name       string
	Total      int
	WithPatch  int
	EmptyPatch int
	Errors     int
}

type summaryData struct {
	StartTime  string
	EndTime    string
	Total      int
	WithPatch  int
	EmptyPatch int
	Errors     int
	Repos      []repoStats
}

// writeSummary 将运行摘要写入 outputDir/run_summary.md。
func writeSummary(outputDir string, results []RunResult, start, end time.Time) error {
	byRepo := make(map[string]*repoStats)
	sd := summaryData{
		StartTime: start.Format("2006-01-02 15:04:05"),
		EndTime:   end.Format("2006-01-02 15:04:05"),
		Total:     len(results),
	}
	for _, r := range results {
		if byRepo[r.Instance.Repo] == nil {
			byRepo[r.Instance.Repo] = &repoStats{Name: r.Instance.Repo}
		}
		rs := byRepo[r.Instance.Repo]
		rs.Total++
		switch {
		case r.Error != nil:
			sd.Errors++
			rs.Errors++
		case r.Patch == "":
			sd.EmptyPatch++
			rs.EmptyPatch++
		default:
			sd.WithPatch++
			rs.WithPatch++
		}
	}
	for _, rs := range byRepo {
		sd.Repos = append(sd.Repos, *rs)
	}
	sort.Slice(sd.Repos, func(i, j int) bool { return sd.Repos[i].Name < sd.Repos[j].Name })

	tmpl, err := template.New("summary").Parse(summaryTmpl)
	if err != nil {
		return err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, sd); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, "run_summary.md"), []byte(sb.String()), 0644)
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./cmd/swebench/... -run "TestLoadExisting|TestAppend|TestWriteSummary" -v
```

预期：`PASS`

- [ ] **Step 5: 提交**

```bash
git add cmd/swebench/report.go cmd/swebench/report_test.go
git commit -m "feat(swebench): report.go — predictions.jsonl 追加写 + run_summary.md 生成"
```

---

## Task 4: runner.go — 单 Instance 执行

**Files:**
- Create: `cmd/swebench/runner.go`

（runner.go 依赖 Docker + git，不写单元测试；集成测试在 Task 6 通过 `go build` + 真实运行验证）

- [ ] **Step 1: 实现 runner.go**

```go
// cmd/swebench/runner.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/sandbox"
	"github.com/harness9/internal/tools"
)

// Config 存储从 CLI flags 解析的运行配置。
type Config struct {
	DatasetPath string
	OutputDir   string
	SampleN     int
	MaxTurns    int
	Parallel    int
	Resume      bool
	TimeoutMins int
	Model       string
}

// newProvider 根据模型名创建 LLM provider。
// 优先级：cfg.Model flag > LLM_MODEL 环境变量 > 默认值 openai/gpt-4o-mini。
func newProvider(model string) (provider.LLMProvider, error) {
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	if model == "" {
		model = "openai/gpt-4o-mini"
	}
	return provider.NewOpenAIProvider(model)
}

// runInstance 对单个 SWE-bench instance 执行完整的 clone → sandbox → engine → patch 流程。
// 任何环境错误都返回 RunResult.Error，不 panic。
func runInstance(ctx context.Context, inst Instance, cfg Config) RunResult {
	start := time.Now()

	// 1. 创建隔离临时目录（defer 保证清理）
	tmpDir, err := os.MkdirTemp("", "swebench-"+inst.InstanceID+"-*")
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("创建临时目录失败: %w", err), Duration: time.Since(start)}
	}
	defer os.RemoveAll(tmpDir)

	// 2. git clone + checkout base_commit（宿主机执行，bind mount 共享给容器）
	repoURL := "https://github.com/" + inst.Repo
	cloneCtx, cloneCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cloneCancel()

	cloneOut, err := exec.CommandContext(cloneCtx, "git", "clone", repoURL, tmpDir).CombinedOutput()
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("git clone 失败: %w\n%s", err, cloneOut), Duration: time.Since(start)}
	}
	checkoutOut, err := exec.CommandContext(cloneCtx, "git", "-C", tmpDir, "checkout", inst.BaseCommit).CombinedOutput()
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("git checkout 失败: %w\n%s", err, checkoutOut), Duration: time.Since(start)}
	}

	// 3. 创建 Docker Sandbox 环境
	sandboxCfg := sandbox.DefaultConfig()
	mgr := sandbox.NewManager(sandboxCfg)
	sandboxCtx, sandboxCancel := context.WithTimeout(ctx, 60*time.Second)
	defer sandboxCancel()
	env, err := mgr.Create(sandboxCtx, tmpDir)
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("sandbox 创建失败: %w", err), Duration: time.Since(start)}
	}
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		mgr.DestroyAll(cleanCtx)
	}()

	// 4. 注册工具（bash 路由进容器，文件工具通过 bind mount 操作宿主机文件系统）
	registry := tools.NewRegistry()
	for _, t := range []tools.BaseTool{
		tools.NewBashTool(tmpDir, tools.WithEnvironment(env)),
		tools.NewReadFileTool(tmpDir),
		tools.NewWriteFileTool(tmpDir),
		tools.NewEditFileTool(tmpDir),
	} {
		if err := registry.Register(t); err != nil {
			return RunResult{Instance: inst, Error: fmt.Errorf("注册工具失败: %w", err), Duration: time.Since(start)}
		}
	}
	hookReg := hooks.NewHookRegistry(registry)

	// 5. 构造 provider 和 engine
	llm, err := newProvider(cfg.Model)
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("创建 LLM provider 失败: %w", err), Duration: time.Since(start)}
	}
	eng := engine.NewAgentEngine(llm, hookReg, tmpDir,
		engine.WithMaxTurns(cfg.MaxTurns),
		engine.WithPromptBuilder(&swebenchPromptBuilder{instance: inst}),
	)

	// 6. 执行 agent loop（带 per-instance 超时）
	instanceCtx, instanceCancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMins)*time.Minute)
	defer instanceCancel()
	runErr := eng.Run(instanceCtx, "请修复上述 Issue。")

	// 7. 收集 patch（无论 runErr 如何都尝试，MaxTurns 触发时可能有部分 patch）
	patchOut, _ := exec.CommandContext(ctx, "git", "-C", tmpDir, "diff").CombinedOutput()
	patch := strings.TrimSpace(string(patchOut))

	if runErr != nil && patch == "" {
		return RunResult{Instance: inst, Error: runErr, Duration: time.Since(start)}
	}
	return RunResult{Instance: inst, Patch: patch, Duration: time.Since(start)}
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./cmd/swebench/...
```

预期：出现 `undefined: main` 错误（main.go 尚未创建），但 runner.go 本身的类型/导入应无误。如果有导入错误，修复后再继续。

- [ ] **Step 3: 提交**

```bash
git add cmd/swebench/runner.go
git commit -m "feat(swebench): runner.go — 单 instance clone+sandbox+engine+patch 执行"
```

---

## Task 5: main.go — CLI 入口与编排

**Files:**
- Create: `cmd/swebench/main.go`

- [ ] **Step 1: 实现 main.go**

```go
// cmd/swebench 是 SWE-bench Lite benchmark runner。
//
// 用法:
//
//	go run ./cmd/swebench --dataset swe-bench-lite.jsonl --sample 10 --output ./results
//
// 环境变量:
//
//	OPENAI_API_KEY   LLM API Key（必填）
//	LLM_MODEL        模型名称（默认: openai/gpt-4o-mini）
//	SANDBOX_IMAGE    Docker 镜像（推荐: python:3.11-slim，默认: ubuntu:22.04）
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/harness9/internal/env"
)

func main() {
	// 加载 .env 文件（系统环境变量优先）
	_ = env.Load(".env")

	cfg := Config{}
	flag.StringVar(&cfg.DatasetPath, "dataset", "", "SWE-bench Lite JSONL 文件路径（必填）")
	flag.IntVar(&cfg.SampleN, "sample", 10, "每个 repo 抽取的 instance 数量")
	flag.StringVar(&cfg.OutputDir, "output", "./swebench-results", "输出目录")
	flag.IntVar(&cfg.MaxTurns, "max-turns", 30, "每个 instance 最大 LLM Turn 数")
	flag.IntVar(&cfg.Parallel, "parallel", 1, "并发 instance 数")
	flag.BoolVar(&cfg.Resume, "resume", false, "跳过已有结果的 instance（断点续跑）")
	flag.IntVar(&cfg.TimeoutMins, "timeout", 10, "单个 instance 超时（分钟）")
	flag.StringVar(&cfg.Model, "model", "", "LLM 模型名称（默认使用 LLM_MODEL 环境变量）")
	flag.Parse()

	if cfg.DatasetPath == "" {
		fmt.Fprintln(os.Stderr, "错误: --dataset 必填")
		flag.Usage()
		os.Exit(1)
	}

	// Preflight checks
	if err := preflight(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "启动检查失败: %v\n", err)
		os.Exit(1)
	}

	// 加载数据集
	allInstances, err := loadDataset(cfg.DatasetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载数据集失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "数据集加载完成: %d 条 instances\n", len(allInstances))

	// 按 repo 采样
	instances := sampleByRepo(allInstances, cfg.SampleN, time.Now().UnixNano())
	fmt.Fprintf(os.Stderr, "采样完成: %d 条（每 repo 最多 %d 条）\n", len(instances), cfg.SampleN)

	// 加载已有结果（--resume 模式）
	predictionsPath := filepath.Join(cfg.OutputDir, "predictions.jsonl")
	skipIDs := make(map[string]bool)
	if cfg.Resume {
		skipIDs, err = loadExistingIDs(predictionsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取已有结果失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "断点续跑: 跳过 %d 个已有结果\n", len(skipIDs))
	}

	// 创建输出目录
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}

	// 信号处理（Ctrl+C 优雅退出）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n收到终止信号，等待当前 instance 完成...")
		cancel()
	}()

	// 并发执行
	sem := semaphore.NewWeighted(int64(cfg.Parallel))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []RunResult
	start := time.Now()

	for _, inst := range instances {
		if skipIDs[inst.InstanceID] {
			fmt.Fprintf(os.Stderr, "[skip] %s\n", inst.InstanceID)
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}
		wg.Add(1)
		go func(inst Instance) {
			defer sem.Release(1)
			defer wg.Done()

			fmt.Fprintf(os.Stderr, "[start] %s\n", inst.InstanceID)
			result := runInstance(ctx, inst, cfg)

			mu.Lock()
			results = append(results, result)
			if appendErr := appendPrediction(predictionsPath, Prediction{
				InstanceID: inst.InstanceID,
				ModelPatch: result.Patch,
			}); appendErr != nil {
				fmt.Fprintf(os.Stderr, "[error] 写入 predictions 失败 (%s): %v\n", inst.InstanceID, appendErr)
			}
			mu.Unlock()

			if result.Error != nil {
				fmt.Fprintf(os.Stderr, "[error] %s (%s): %v\n", inst.InstanceID, result.Duration.Round(time.Second), result.Error)
			} else {
				fmt.Fprintf(os.Stderr, "[done]  %s (%s) patch=%d bytes\n", inst.InstanceID, result.Duration.Round(time.Second), len(result.Patch))
			}
		}(inst)
	}
	wg.Wait()

	// 写汇总
	end := time.Now()
	if err := writeSummary(cfg.OutputDir, results, start, end); err != nil {
		fmt.Fprintf(os.Stderr, "写入摘要失败: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "\n完成！结果已写入 %s\n", cfg.OutputDir)
	fmt.Fprintf(os.Stderr, "总实例: %d，耗时: %s\n", len(results), end.Sub(start).Round(time.Second))
}

// preflight 在启动前验证必要条件，任一失败则终止程序。
func preflight(cfg Config) error {
	// API Key
	if os.Getenv("OPENAI_API_KEY") == "" {
		return fmt.Errorf("OPENAI_API_KEY 未配置")
	}
	// dataset 文件
	if _, err := os.Stat(cfg.DatasetPath); err != nil {
		return fmt.Errorf("dataset 文件不可读: %w", err)
	}
	// git 命令
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git 命令不可用: %w", err)
	}
	// Docker daemon（通过 docker info 探测）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		return fmt.Errorf("Docker daemon 不可达: %w\n%s", err, out)
	}
	// output 目录（不存在时自动创建，此处只验证父目录可写）
	parent := filepath.Dir(cfg.OutputDir)
	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("输出目录的父路径不存在: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: 更新依赖并验证编译通过**

`golang.org/x/sync` 目前是间接依赖，直接引用后需要 `go mod tidy`：

```bash
go mod tidy
go build ./cmd/swebench/...
```

预期：`go mod tidy` 将 `golang.org/x/sync` 移至直接依赖；`go build` 无错误。

- [ ] **Step 3: 运行全量测试**

```bash
go test ./cmd/swebench/... -v
```

预期：`TestLoadDataset`、`TestSampleByRepo`、`TestSwebenchPromptBuilder`、`TestLoadExistingIDs`、`TestAppendPrediction`、`TestWriteSummary` 全部 `PASS`。

- [ ] **Step 4: 提交**

```bash
git add cmd/swebench/main.go
git commit -m "feat(swebench): main.go — CLI 入口、preflight、并发编排、汇总"
```

---

## Task 6: 端到端验证与文档

**Files:**
- Modify: `README.md`（添加 SWE-bench 运行说明）

- [ ] **Step 1: 验证 help 输出正常**

```bash
go run ./cmd/swebench --help
```

预期输出包含：`--dataset`、`--sample`、`--output`、`--max-turns`、`--parallel`、`--resume`、`--timeout`、`--model` 各 flag 说明。

- [ ] **Step 2: 验证 preflight 错误提示**

```bash
go run ./cmd/swebench --dataset /nonexistent.jsonl 2>&1
```

预期：`启动检查失败: dataset 文件不可读: ...`（因为 OPENAI_API_KEY 可能未配置，错误信息可能是 API Key 提示，也可接受）

- [ ] **Step 3: 运行 go vet**

```bash
go vet ./cmd/swebench/...
```

预期：无输出（无警告）

- [ ] **Step 4: 在 README.md 中添加 SWE-bench 使用说明**

在 README.md 末尾添加以下内容（找到合适位置插入）：

```markdown
## SWE-bench Benchmark

评估 harness9 在 [SWE-bench Lite](https://github.com/princeton-nlp/SWE-bench) 上的 Agent 能力。

### 前置条件

- Docker daemon 运行中（用于 Sandbox 隔离）
- `git` 命令可用
- `OPENAI_API_KEY` 已配置

### 运行步骤

**1. 下载数据集**

从 HuggingFace 下载 SWE-bench Lite JSONL 文件：
```bash
# 需要 Python + datasets 库
pip install datasets
python -c "
from datasets import load_dataset
ds = load_dataset('princeton-nlp/SWE-bench_Lite', split='test')
ds.to_json('swe-bench-lite.jsonl')
"
```

**2. 运行 benchmark（推荐使用 python:3.11-slim 镜像）**

```bash
SANDBOX_IMAGE=python:3.11-slim \
OPENAI_API_KEY=your_key \
LLM_MODEL=openai/gpt-4o \
go run ./cmd/swebench \
  --dataset swe-bench-lite.jsonl \
  --sample 10 \
  --output ./swebench-results \
  --max-turns 30 \
  --parallel 2
```

**3. 评估结果**

```bash
pip install swebench
python -m swebench.harness.run_evaluation \
    --dataset_name princeton-nlp/SWE-bench_Lite \
    --predictions_path ./swebench-results/predictions.jsonl \
    --max_workers 4 \
    --run_id harness9-lite-v1
```

### 参数说明

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--dataset` | JSONL 文件路径 | 必填 |
| `--sample` | 每 repo 抽取条数 | 10 |
| `--output` | 输出目录 | ./swebench-results |
| `--max-turns` | 每 instance 最大 Turn 数 | 30 |
| `--parallel` | 并发数 | 1 |
| `--resume` | 断点续跑 | false |
| `--timeout` | 单 instance 超时（分钟） | 10 |
```

- [ ] **Step 5: 最终提交**

```bash
git add README.md
git commit -m "docs: 添加 SWE-bench benchmark 运行指南"
```

---

## 实现完成后的自检清单

- [ ] `go build ./cmd/swebench/...` 无错误
- [ ] `go test ./cmd/swebench/... -v` 全部 PASS
- [ ] `go vet ./cmd/swebench/...` 无警告
- [ ] `go run ./cmd/swebench --help` 正常输出
- [ ] `predictions.jsonl` 格式与官方 swebench 兼容（每行一个 JSON 对象，含 `instance_id` + `model_patch`）
- [ ] `--resume` flag 能跳过已处理的 instance

---

## 设计参考

完整设计文档: `docs/设计规格/2026-06-09-swebench-lite-runner-design.md`
