// report.go — 评估报告生成器，提供 JSON 和 Markdown 两种输出格式。
//
// 典型用法（在 CI 中生成报告）：
//
//	results := suite.Run(ctx)
//	report := evals.BuildReport(results)
//	evals.WriteJSON(report, "eval-results/report.json")
//	evals.WriteMarkdown(report, "eval-results/report.md")
package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SuiteReport 是一次 Suite 运行的聚合报告，包含全局统计和按类别分解的结果。
// 可序列化为 JSON 供 CI 机器解析，也可渲染为 Markdown 供人工阅读。
type SuiteReport struct {
	RunAt      time.Time                 `json:"run_at"`      // 报告生成时间（UTC）
	TotalCases int                       `json:"total_cases"` // 总用例数
	Passed     int                       `json:"passed"`      // 通过用例数
	Failed     int                       `json:"failed"`      // 失败用例数
	PassRate   float64                   `json:"pass_rate"`   // 通过率（0.0~1.0）
	Categories map[string]*CategoryStats `json:"categories"`  // 按 Category 分类的统计
	Results    []ResultSnapshot          `json:"results"`     // 每个 Case 的详细快照
}

// CategoryStats 是单个 Category 的统计信息。
// 对应 SuiteReport.Categories 的 map 值。
type CategoryStats struct {
	Total    int     `json:"total"`     // 该类别总用例数
	Passed   int     `json:"passed"`    // 通过用例数
	PassRate float64 `json:"pass_rate"` // 通过率（0.0~1.0）
}

// ResultSnapshot 是单个 Case 结果的轻量序列化视图（不包含 Case.Provider 等大对象）。
// 用于 JSON 持久化和 Markdown 渲染。
type ResultSnapshot struct {
	ID         string   `json:"id"`                 // Case.ID
	Category   string   `json:"category"`           // Case.Category
	Passed     bool     `json:"passed"`             // 是否通过所有硬断言
	TurnCount  int      `json:"turn_count"`         // 实际执行的 Turn 数
	ToolCalls  []string `json:"tool_calls"`         // 被调用工具名称列表
	Failures   []string `json:"failures,omitempty"` // 硬断言失败消息列表
	Warnings   []string `json:"warnings,omitempty"` // 软断言警告消息列表
	DurationMs int64    `json:"duration_ms"`        // 执行耗时（毫秒）
}

// BuildReport 从 Results 列表聚合生成 SuiteReport。
// 自动计算全局和分类通过率，将 Failure/Warning 序列化为字符串列表。
func BuildReport(results []Result) SuiteReport {
	report := SuiteReport{
		RunAt:      time.Now(),
		TotalCases: len(results),
		Categories: make(map[string]*CategoryStats),
	}

	for _, r := range results {
		if r.Passed {
			report.Passed++
		} else {
			report.Failed++
		}

		cat := r.Case.Category
		if _, ok := report.Categories[cat]; !ok {
			report.Categories[cat] = &CategoryStats{}
		}
		report.Categories[cat].Total++
		if r.Passed {
			report.Categories[cat].Passed++
		}

		snap := ResultSnapshot{
			ID:         r.Case.ID,
			Category:   r.Case.Category,
			Passed:     r.Passed,
			TurnCount:  r.TurnCount,
			ToolCalls:  r.ToolCallsExecuted,
			DurationMs: r.Duration.Milliseconds(),
		}
		for _, f := range r.Failures {
			snap.Failures = append(snap.Failures, f.Error())
		}
		for _, w := range r.Warnings {
			snap.Warnings = append(snap.Warnings, w.Error())
		}
		report.Results = append(report.Results, snap)
	}

	if report.TotalCases > 0 {
		report.PassRate = float64(report.Passed) / float64(report.TotalCases)
	}
	for _, s := range report.Categories {
		if s.Total > 0 {
			s.PassRate = float64(s.Passed) / float64(s.Total)
		}
	}
	return report
}

// WriteJSON 将报告序列化为 JSON，写入 path。
// 自动创建父目录（权限 0755），使用 MarshalIndent 保证可读性。
func WriteJSON(report SuiteReport, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteMarkdown 将报告渲染为 Markdown，写入 path。
// 包含：总计行、按类别统计表格、每个 Case 的详细结果（含失败消息和效率警告）。
// 分类统计按名称字母序排列，确保输出稳定可 diff。
func WriteMarkdown(report SuiteReport, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval Report — %s\n\n", report.RunAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "**总计**: %d cases | **通过**: %d | **失败**: %d | **通过率**: %.1f%%\n\n",
		report.TotalCases, report.Passed, report.Failed, report.PassRate*100)

	// 分类统计（按名称排序）
	cats := make([]string, 0, len(report.Categories))
	for k := range report.Categories {
		cats = append(cats, k)
	}
	sort.Strings(cats)

	fmt.Fprint(&b, "## 分类统计\n\n")
	fmt.Fprintln(&b, "| 类别 | 总数 | 通过 | 通过率 |")
	fmt.Fprintln(&b, "|------|------|------|--------|")
	for _, cat := range cats {
		s := report.Categories[cat]
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f%% |\n", cat, s.Total, s.Passed, s.PassRate*100)
	}
	fmt.Fprintln(&b)

	// 详细结果
	fmt.Fprint(&b, "## 详细结果\n\n")
	for _, r := range report.Results {
		icon := "✅"
		if !r.Passed {
			icon = "❌"
		}
		fmt.Fprintf(&b, "### %s `%s`\n\n", icon, r.ID)
		fmt.Fprintf(&b, "- **轮次**: %d | **工具调用**: %v | **耗时**: %dms\n",
			r.TurnCount, r.ToolCalls, r.DurationMs)
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "- ❌ %s\n", f)
		}
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "- ⚠️ %s\n", w)
		}
		fmt.Fprintln(&b)
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}
