package subagent

import (
	"strings"
	"testing"
)

func TestPromptBuilderBuild(t *testing.T) {
	pb := newPromptBuilder("你是审查专家。", "/work", nil, nil)
	got := pb.Build()
	if !strings.Contains(got, "你是审查专家。") {
		t.Error("应包含 system prompt 正文")
	}
	if !strings.Contains(got, "/work") {
		t.Error("应包含工作目录")
	}
}

func TestPromptBuilderWithSkills(t *testing.T) {
	loader := func(name string) (string, error) {
		return "技能正文-" + name, nil
	}
	pb := newPromptBuilder("base", "/work", []string{"api-conventions"}, loader)
	got := pb.Build()
	if !strings.Contains(got, "技能正文-api-conventions") {
		t.Errorf("应预加载并注入 skill 正文，得: %s", got)
	}
}

func TestPromptBuilderSkillLoadErrorIgnored(t *testing.T) {
	loader := func(name string) (string, error) {
		return "", errSkillMissing
	}
	pb := newPromptBuilder("base", "/work", []string{"missing"}, loader)
	if got := pb.Build(); !strings.Contains(got, "base") {
		t.Error("skill 加载失败应被忽略，base 仍在")
	}
}
