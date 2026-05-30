package subagent

import "testing"

func TestParseAgentFile(t *testing.T) {
	content := `---
name: code-reviewer
description: 代码审查专家
tools: read_file, bash
disallowed_tools: write_file
model: gpt-4.1
max_turns: 20
skills: api-conventions, error-handling
---
你是一名代码审查专家。
保持简洁。`

	def, err := parseAgentFile(content)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "code-reviewer" {
		t.Errorf("Name=%q", def.Name)
	}
	if def.Description != "代码审查专家" {
		t.Errorf("Description=%q", def.Description)
	}
	if len(def.Tools) != 2 || def.Tools[0] != "read_file" || def.Tools[1] != "bash" {
		t.Errorf("Tools=%v", def.Tools)
	}
	if len(def.DisallowedTools) != 1 || def.DisallowedTools[0] != "write_file" {
		t.Errorf("DisallowedTools=%v", def.DisallowedTools)
	}
	if def.Model != "gpt-4.1" {
		t.Errorf("Model=%q", def.Model)
	}
	if def.MaxTurns != 20 {
		t.Errorf("MaxTurns=%d", def.MaxTurns)
	}
	if len(def.Skills) != 2 {
		t.Errorf("Skills=%v", def.Skills)
	}
	if def.SystemPrompt != "你是一名代码审查专家。\n保持简洁。" {
		t.Errorf("SystemPrompt=%q", def.SystemPrompt)
	}
}

func TestParseAgentFileNoFrontmatter(t *testing.T) {
	if _, err := parseAgentFile("just body text"); err == nil {
		t.Fatal("无 frontmatter 应返回错误")
	}
}

func TestParseAgentFileQuotedAndEmpty(t *testing.T) {
	content := `---
name: "x-agent"
description: 'has: colon'
max_turns: notanumber
---
body`
	def, err := parseAgentFile(content)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "x-agent" {
		t.Errorf("Name=%q", def.Name)
	}
	if def.Description != "has: colon" {
		t.Errorf("Description=%q", def.Description)
	}
	if def.MaxTurns != 0 {
		t.Errorf("非法 max_turns 应解析为 0，得 %d", def.MaxTurns)
	}
}
