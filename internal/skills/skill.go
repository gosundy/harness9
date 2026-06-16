// Package skills 实现 harness9 的 Agent Skills 解析与加载系统。
//
// Skills 遵循 Progressive Disclosure 原则：System Prompt 只注入技能索引摘要，
// LLM 通过调用 use_skill 工具按需加载特定技能的全文内容，避免上下文窗口膨胀。
package skills

import "strings"

// Skill 表示一个已解析的 skill。frontmatter 字段在加载时解析，
// 全文内容（body）通过 filePath 懒加载，不在启动时读入内存。
//
// filePath 未导出，调用方通过 Index.GetFullContent 访问全文内容，
// 确保懒加载路径不绕过 Index 的统一访问控制。
type Skill struct {
	Name        string
	Description string
	// Trigger 是触发该 skill 的关键词（如 "/autodev"），目前仅用于文档和 Tab 补全提示，
	// 尚未接入自动触发机制（自动触发依赖 LLM 对触发词的主动识别）。
	Trigger  string
	filePath string // SKILL.md 的绝对路径，用于懒加载全文内容
}

// parseFrontmatter 解析 Markdown 文件开头的 YAML frontmatter 块。
// 返回 name、description、trigger 和 frontmatter 之后的 body。
// 文件不以 "---\n" 开头或缺少闭合分隔符时，视为无 frontmatter，body 为全文。
func parseFrontmatter(content string) (name, description, trigger, body string) {
	const delim = "---\n"
	if !strings.HasPrefix(content, delim) {
		return "", "", "", content
	}
	rest := content[len(delim):]
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		return "", "", "", content
	}
	fm := rest[:idx]
	body = strings.TrimPrefix(rest[idx+len("\n---\n"):], "\n")

	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		switch k {
		case "name":
			name = v
		case "description":
			description = v
		case "trigger":
			trigger = v
		}
	}
	return
}
