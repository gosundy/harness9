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
	b := &swebenchPromptBuilder{instance: inst, workDir: "/tmp/swebench-test"}
	prompt := b.Build()

	// 占位符注入
	if !strings.Contains(prompt, inst.ProblemStatement) {
		t.Error("prompt should contain the problem statement")
	}
	if strings.Contains(prompt, "{{PROBLEM_STATEMENT}}") {
		t.Error("prompt should not contain unreplaced placeholder")
	}
	if !strings.Contains(prompt, "/tmp/swebench-test") {
		t.Error("prompt should contain the injected workDir")
	}
	if strings.Contains(prompt, "{{WORK_DIR}}") {
		t.Error("prompt should not contain unreplaced WORK_DIR placeholder")
	}

	// 结构化工作流
	if !strings.Contains(prompt, "Step 1") || !strings.Contains(prompt, "Step 5") {
		t.Error("prompt should contain structured workflow steps")
	}

	// 约束：不修改测试文件
	if !strings.Contains(prompt, "Never modify test files") {
		t.Error("prompt should contain constraint about not modifying test files")
	}

	// 英文推理 + 单行防漂移
	if !strings.Contains(prompt, "Reason and respond in English") {
		t.Error("prompt should mandate English reasoning with anti-drift line")
	}

	// 相对路径强约束（修复 safePath 路径翻倍的诱因）
	if !strings.Contains(prompt, "relative to the working directory") {
		t.Error("prompt should require relative paths for file tools")
	}
	if !strings.Contains(prompt, "NEVER pass an absolute path") {
		t.Error("prompt should forbid absolute paths to file tools")
	}

	// 行为验证而非语法验证（核心修复：杜绝 plausible-but-wrong 过度修复）
	if !strings.Contains(prompt, "does NOT prove the behavior is correct") {
		t.Error("prompt should reframe verification as behavioral, not syntactic")
	}
	// 禁止内联重抄自测的假验证
	if !strings.Contains(prompt, "re-implementing the class/function inline") {
		t.Error("prompt should forbid fake verification via inline re-implementation")
	}

	// 阅读现有测试（最强行为信号）
	if !strings.Contains(prompt, "never modify — the existing tests") {
		t.Error("prompt should encourage reading existing tests")
	}

	// heredoc 跑复现，禁止在仓库内建临时文件污染 patch
	if !strings.Contains(prompt, "heredoc") {
		t.Error("prompt should instruct running repros via heredoc")
	}
	if !strings.Contains(prompt, "corrupt the final patch") {
		t.Error("prompt should warn that scratch files corrupt the patch")
	}

	// read_file 行号模式 + 并发探索
	if !strings.Contains(prompt, "start_line") {
		t.Error("prompt should mention read_file start_line parameter")
	}
	if !strings.Contains(prompt, "multiple tool calls in parallel") {
		t.Error("prompt should encourage concurrent tool calls")
	}

	// timeout_secs 放宽慢命令
	if !strings.Contains(prompt, "timeout_secs") {
		t.Error("prompt should mention per-call timeout_secs for slow commands")
	}

	// 不应再保留旧的"放弃验证"反模式
	if strings.Contains(prompt, "diff 即为权威确认") || strings.Contains(prompt, "验证至多 1-2 步") {
		t.Error("prompt should no longer contain the discourage-verification anti-pattern")
	}
}
