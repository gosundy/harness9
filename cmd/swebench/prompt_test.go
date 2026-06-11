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
	// 语言锁定（P2：防止语言漂移）
	if !strings.Contains(prompt, "语言要求") {
		t.Error("prompt should contain language requirement")
	}
	// Python 快速放弃策略（P0：杜绝死循环搜索）
	if !strings.Contains(prompt, "NO_PYTHON") {
		t.Error("prompt should contain NO_PYTHON fast-detection pattern")
	}
	if !strings.Contains(prompt, "立即跳过本步，不再搜索 Python 安装位置") {
		t.Error("prompt should contain instruction to skip python search immediately")
	}
	// 先规划后编辑（P1：减少 edit_file 反复撤回）
	if !strings.Contains(prompt, "在调用任何编辑工具之前") {
		t.Error("prompt should contain plan-before-edit instruction")
	}
	// 验证上限（P2：杜绝过度验证）
	if !strings.Contains(prompt, "验证至多 2 步") {
		t.Error("prompt should contain max 2 verification steps constraint")
	}
	// 并发工具调用引导（P3）
	if !strings.Contains(prompt, "尽量同时发起多个工具调用") {
		t.Error("prompt should encourage concurrent tool calls")
	}
}
