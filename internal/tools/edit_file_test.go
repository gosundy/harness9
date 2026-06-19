package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestEditFileTool_Name(t *testing.T) {
	tool := NewEditFileTool("/tmp")
	if tool.Name() != "edit_file" {
		t.Errorf("expected 'edit_file', got %q", tool.Name())
	}
}

func TestEditFileTool_Definition(t *testing.T) {
	tool := NewEditFileTool("/tmp")
	def := tool.Definition()
	if def.Name != "edit_file" {
		t.Errorf("definition name mismatch: %q", def.Name)
	}
	if def.Description == "" {
		t.Error("definition should have a description")
	}
	if def.InputSchema == nil {
		t.Error("definition should have an input schema")
	}
}

func TestEditFileTool_Execute_BadJSON(t *testing.T) {
	tool := NewEditFileTool("/tmp")
	_, err := tool.Execute(context.Background(), json.RawMessage(`not_json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON args")
	}
}

func TestEditFileTool_Execute_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../../etc/passwd","source_text":"x","target_text":"y"}`))
	if err == nil {
		t.Fatal("expected sandbox error for path traversal")
	}
	if !strings.Contains(err.Error(), "超出工作区范围") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}

func TestEditFileTool_Execute_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"nonexistent.txt","source_text":"x","target_text":"y"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// L1: 精确匹配替换
func TestFuzzyReplace_L1_ExactMatch(t *testing.T) {
	content := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	source := `	fmt.Println("hello")`
	target := `	fmt.Println("world")`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear in result, got: %s", result)
	}
	if strings.Contains(result, `fmt.Println("hello")`) {
		t.Errorf("source text should not remain, got: %s", result)
	}
}

// L1: 精确匹配多处 → 返回错误
func TestFuzzyReplace_L1_MultipleMatch_Error(t *testing.T) {
	content := `foo
bar
foo`
	source := `foo`
	target := `baz`

	_, err := fuzzyReplace(content, source, target)
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "匹配到了") {
		t.Errorf("error should mention multiple matches, got: %v", err)
	}
}

// L2: 换行符归一化匹配（CRLF → LF）
func TestFuzzyReplace_L2_CRLFNormalization(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	source := "line2"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CRLF 应被保留
	if !strings.Contains(result, "\r\n") {
		t.Error("CRLF line endings should be preserved")
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L2: CRLF 文件中 source_text 也带 CRLF
func TestFuzzyReplace_L2_CRLFInSourceText(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	source := "line2\r\nline3"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L2: CRLF 文件中 target_text 含 CRLF
func TestFuzzyReplace_L2_CRLFInTargetText(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	source := "line2"
	target := "modified\r\nnewline"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L3: 整体首尾去空匹配
func TestFuzzyReplace_L3_TrimmedMatch(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
}
`
	// source_text 带多余的缩进空白
	source := `        func main() {
    fmt.Println("hello")
}`
	target := `func main() {
    fmt.Println("world")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L4: 逐行去缩进匹配
func TestFuzzyReplace_L4_LineByLineMatch(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
    fmt.Println("world")
}
`
	source := `func main() {
    fmt.Println("hello")
    fmt.Println("world")
}`
	target := `func main() {
    fmt.Println("hello universe")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello universe") {
		t.Errorf("target text should appear, got: %s", result)
	}
	if strings.Contains(result, "fmt.Println(\"world\")") {
		t.Errorf("old code should be replaced, got: %s", result)
	}
}

// L4: 缩进不一致时的模糊匹配
func TestFuzzyReplace_L4_IndentAgnostic(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
}
`
	// source_text 使用 tab 缩进而文件中是空格
	source := "func main() {\n\tfmt.Println(\"hello\")\n}"
	target := "func main() {\n    fmt.Println(\"world\")\n}"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L4: 逐行去缩进多处匹配 → 返回错误
func TestFuzzyReplace_L4_MultipleMatch_Error(t *testing.T) {
	content := `x := 1
y := 2
x := 3
`
	source := `x :=`
	target := `z :=`

	_, err := fuzzyReplace(content, source, target)
	// L1 就会匹配到两处
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
}

// L4: 无匹配 → 返回错误
func TestFuzzyReplace_NoMatch(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
}
`
	source := `func foo() {
    return 42
}`
	target := `func bar() {
    return 0
}`

	_, err := fuzzyReplace(content, source, target)
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

// 完整的 Execute 链路测试
func TestEditFileTool_Execute_Success(t *testing.T) {
	dir := t.TempDir()
	content := `package main

import "fmt"

func main() {
    fmt.Println("hello")
}
`
	if err := os.WriteFile(dir+"/main.go", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"main.go","source_text":"fmt.Println(\"hello\")","target_text":"fmt.Println(\"world\")"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "成功修改文件") {
		t.Errorf("success message should contain success text, got: %q", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("success message should mention file path, got: %q", out)
	}
	// 验证改动上下文已嵌入输出（Agent 无需额外 read_file 确认）
	if !strings.Contains(out, "---") {
		t.Errorf("output should contain diff context section, got: %q", out)
	}
	// 检查 - / + 前缀存在，不检查具体缩进（缩进随文件内容变化）
	if !strings.Contains(out, `- `) || !strings.Contains(out, `Println("hello")`) {
		t.Errorf("output should show removed line, got: %q", out)
	}
	if !strings.Contains(out, `+ `) || !strings.Contains(out, `Println("world")`) {
		t.Errorf("output should show added line, got: %q", out)
	}

	data, err := os.ReadFile(dir + "/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `fmt.Println("world")`) {
		t.Errorf("file should contain target text after edit, got: %s", string(data))
	}
}

// L4: 包含空行的代码块匹配
func TestFuzzyReplace_L4_WithBlankLines(t *testing.T) {
	content := `func process() {
    step1()

    step2()
}`
	source := `func process() {
    step1()

    step2()
}`
	target := `func process() {
    step1()
    step2()
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 应该只有 4 行（去掉空行）
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 lines, got %d: %s", len(lines), result)
	}
}

// L4: 单行匹配
func TestFuzzyReplace_L4_SingleLine(t *testing.T) {
	content := `func main() {
    fmt.Println("hello")
}`
	source := `fmt.Println("hello")`
	target := `fmt.Println("world")`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target should appear, got: %s", result)
	}
}

// L3: 空 source_text 不应进入 L3
func TestFuzzyReplace_L3_EmptySourceTrimmed(t *testing.T) {
	content := `just some content`
	source := `   ` // 全空格
	target := `replacement`

	_, err := fuzzyReplace(content, source, target)
	if err == nil {
		t.Fatal("expected error for whitespace-only source")
	}
}

// L1: 原始文本精确匹配，保留原始格式（包括 \r\n）
func TestFuzzyReplace_L1_PreserveOriginalFormatting(t *testing.T) {
	content := "line1\nline2\nline3"
	source := "line2"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "line1\nmodified\nline3" {
		t.Errorf("unexpected result: %q", result)
	}
}

// 验证写入文件后内容正确性
func TestEditFileTool_Execute_L1_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/test.txt", []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"test.txt","source_text":"world","target_text":"harness9"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(dir + "/test.txt")
	if string(data) != "hello harness9" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

// L2: 真正测试换行符归一化 — CRLF 文件中使用纯 LF 的多行 source_text
func TestFuzzyReplace_L2_TrueLineEndingNormalization(t *testing.T) {
	content := "line1\r\nline2\r\nline3\r\nline4"
	// source 使用 LF（\n），与文件的 CRLF（\r\n）不同
	source := "line2\nline3"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
	// CRLF 应被保留
	if !strings.Contains(result, "\r\n") {
		t.Error("CRLF line endings should be preserved")
	}
}

// L3: 测试 source_text 带有额外首尾空白的情况
func TestFuzzyReplace_L3_LeadingTrailingWhitespace(t *testing.T) {
	content := `func main() {
    fmt.Println("hello")
}`
	// source_text 带有额外的首尾空白行和缩进
	source := `
    func main() {
    fmt.Println("hello")
}    
`
	target := `func newFunc() {
    fmt.Println("world")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear, got: %s", result)
	}
	if strings.Contains(result, `fmt.Println("hello")`) {
		t.Errorf("old text should not remain, got: %s", result)
	}
}

// L4: 真正测试逐行去缩进 — 每行缩进级别不同，L1/L2/L3 均无法匹配
func TestFuzzyReplace_L4_LineByLineIndentDifference(t *testing.T) {
	content := `func foo() {
        x := 1
        y := 2
}`
	// source_text 使用 3 空格缩进，而文件中是 8 空格
	source := `func foo() {
   x := 1
   y := 2
}`
	target := `func foo() {
    x := 999
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "x := 999") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// buildEditSummary：单行替换，验证 - / + 行和上下文行
func TestBuildEditSummary_SingleLineChange(t *testing.T) {
	orig := "line1\nline2_old\nline3\n"
	next := "line1\nline2_new\nline3\n"
	out := buildEditSummary("foo.py", orig, next, true)

	if !strings.Contains(out, "- line2_old") {
		t.Errorf("should show removed line, got: %s", out)
	}
	if !strings.Contains(out, "+ line2_new") {
		t.Errorf("should show added line, got: %s", out)
	}
	if !strings.Contains(out, "  line1") {
		t.Errorf("should show context before change, got: %s", out)
	}
	if !strings.Contains(out, "  line3") {
		t.Errorf("should show context after change, got: %s", out)
	}
	// diff 末尾的可信度声明必须存在，让 Agent 无需额外 grep/read_file 确认
	if !strings.Contains(out, "✓") {
		t.Errorf("should contain authoritative confirmation mark, got: %s", out)
	}
}

// buildEditSummary：纯插入（只新增行，无删除）
func TestBuildEditSummary_InsertionOnly(t *testing.T) {
	orig := "line1\nline3\n"
	next := "line1\nline2_inserted\nline3\n"
	out := buildEditSummary("foo.py", orig, next, true)

	if !strings.Contains(out, "+ line2_inserted") {
		t.Errorf("should show inserted line, got: %s", out)
	}
	// 没有 - 行
	if strings.Contains(out, "- line") {
		t.Errorf("should not show removed line for pure insertion, got: %s", out)
	}
}

// buildEditSummary：纯删除（只删除行，无新增）
func TestBuildEditSummary_DeletionOnly(t *testing.T) {
	orig := "line1\nline2_removed\nline3\n"
	next := "line1\nline3\n"
	out := buildEditSummary("foo.py", orig, next, true)

	if !strings.Contains(out, "- line2_removed") {
		t.Errorf("should show removed line, got: %s", out)
	}
	// 没有 + 行
	if strings.Contains(out, "+ line") {
		t.Errorf("should not show added line for pure deletion, got: %s", out)
	}
}

// buildEditSummary：变更超过 20 行时只报数字，不展开 diff
func TestBuildEditSummary_LargeChange(t *testing.T) {
	var origLines, nextLines []string
	for i := 0; i < 25; i++ {
		origLines = append(origLines, fmt.Sprintf("old line %d", i))
		nextLines = append(nextLines, fmt.Sprintf("new line %d", i))
	}
	orig := strings.Join(origLines, "\n")
	next := strings.Join(nextLines, "\n")
	out := buildEditSummary("big.py", orig, next, true)

	if !strings.Contains(out, "删除") || !strings.Contains(out, "新增") {
		t.Errorf("large diff should report line counts, got: %s", out)
	}
	// 不应展开完整 diff
	if strings.Contains(out, "--- 改动上下文") {
		t.Errorf("large diff should not show full context section, got: %s", out)
	}
}

// TestFuzzyReplace_L4_PreservesBlockIndentation 验证 L4 缩进无关匹配命中后，
// 模型提供的 target 会被重新缩进到被替换块的缩进层级（防止 Python IndentationError）。
func TestFuzzyReplace_L4_PreservesBlockIndentation(t *testing.T) {
	// 文件中方法体缩进 4/8 空格；source 用 0/4 空格触发 L4（逐行去缩进）匹配。
	content := "class A:\n    def old(self):\n        return 1\n"
	source := "def old(self):\n    return 1" // 缩进与文件不同 → 走 L4
	// 模型给出的 target 以 0 缩进书写，依赖框架对齐到块基准缩进。
	target := "def new(self):\n    return 2"

	result, level, err := fuzzyReplaceWithLevel(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != matchLineByLine {
		t.Fatalf("expected L4 match, got level %d (result=%q)", level, result)
	}
	// 被替换块首行原缩进为 4 空格 → def new 应为 4 空格，return 2 应为 8 空格。
	if !strings.Contains(result, "\n    def new(self):\n") {
		t.Errorf("def new 应重缩进到 4 空格基准，got:\n%s", result)
	}
	if !strings.Contains(result, "\n        return 2\n") {
		t.Errorf("return 2 应重缩进到 8 空格（保留相对结构），got:\n%s", result)
	}
}

// TestEditFile_FuzzyMatch_CautionBanner 验证模糊匹配（L3/L4）的成功提示
// 不再声称"无需确认"，而是建议复核（避免在最该验证时抑制自愈）。
func TestEditFile_FuzzyMatch_CautionBanner(t *testing.T) {
	dir := t.TempDir()
	// 文件缩进与 source 不同，强制走 L3/L4 模糊匹配。
	content := "class A:\n    def m(self):\n        return 1\n"
	if err := os.WriteFile(dir+"/a.py", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFileTool(dir)
	// source 缩进与文件不同
	args := json.RawMessage(`{"path":"a.py","source_text":"def m(self):\n    return 1","target_text":"def m(self):\n    return 2"}`)
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "无需额外") {
		t.Errorf("模糊匹配不应声称无需确认，got: %q", out)
	}
	if !strings.Contains(out, "模糊匹配") || !strings.Contains(out, "复核") {
		t.Errorf("模糊匹配应提示复核，got: %q", out)
	}
}

// TestEditFile_ExactMatch_AuthoritativeBanner 验证精确匹配仍声明权威无需复核。
func TestEditFile_ExactMatch_AuthoritativeBanner(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/a.txt", []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFileTool(dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.txt","source_text":"world","target_text":"harness9"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "✓") || !strings.Contains(out, "精确匹配") {
		t.Errorf("精确匹配应声明权威确认，got: %q", out)
	}
}

// buildEditSummary：CRLF 文件归一化后正常展示
func TestBuildEditSummary_CRLFContent(t *testing.T) {
	orig := "line1\r\nline2_old\r\nline3\r\n"
	next := "line1\r\nline2_new\r\nline3\r\n"
	out := buildEditSummary("foo.py", orig, next, true)

	if !strings.Contains(out, "- line2_old") {
		t.Errorf("CRLF: should show removed line, got: %s", out)
	}
	if !strings.Contains(out, "+ line2_new") {
		t.Errorf("CRLF: should show added line, got: %s", out)
	}
}

// 空文件编辑测试
func TestEditFileTool_Execute_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/empty.txt", []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	// 在空文件中搜索任何文本都应失败
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"empty.txt","source_text":"anything","target_text":"replacement"}`))
	if err == nil {
		t.Fatal("expected error when editing empty file")
	}
}

// L4: 验证 lineByLineReplace 对 CRLF 文件的保留
func TestFuzzyReplace_L4_CRLFPreservation(t *testing.T) {
	content := "func main() {\r\n    fmt.Println(\"hello\")\r\n}"
	source := `func main() {
    fmt.Println("hello")
}`
	target := `func main() {
    fmt.Println("world")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "\r\n") {
		t.Error("CRLF should be preserved after L4 match")
	}
	if strings.Contains(result, "hello") {
		t.Error("old text should not remain")
	}
}
