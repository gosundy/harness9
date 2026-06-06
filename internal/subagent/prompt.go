// Package subagent — promptBuilder：子代理 system prompt 组装器。
// 本文件实现 promptBuilder，组合子代理 system prompt、预加载 skill 正文和工作目录信息。
// 通过结构类型（Structural Typing）隐式满足 engine.PromptBuilder 接口，无需显式 import engine 包，
// 避免循环依赖（subagent 依赖 engine，engine 不反向依赖 subagent）。
package subagent

import (
	"errors"
	"fmt"
	"strings"
)

// errSkillMissing 仅用于测试桩，表示 skill 不存在。
var errSkillMissing = errors.New("skill missing")

// skillLoader 按名称加载 skill 正文。生产中由 *skills.Index.GetFullContent 适配。
type skillLoader func(name string) (string, error)

// promptBuilder 是子代理的静态 PromptBuilder，实现 engine.PromptBuilder 接口（Build() string）。
// 输出 = 子代理 system prompt + 预加载 skills 正文 + 工作目录信息 + Sandbox 环境说明（可选）。
type promptBuilder struct {
	systemPrompt   string
	workDir        string
	skills         []string
	loader         skillLoader
	sandboxContext bool
}

// newPromptBuilder 创建子代理 PromptBuilder。loader 为 nil 时不加载 skills。
func newPromptBuilder(systemPrompt, workDir string, skills []string, loader skillLoader) *promptBuilder {
	return &promptBuilder{systemPrompt: systemPrompt, workDir: workDir, skills: skills, loader: loader}
}

// WithSandboxContext 在子代理 system prompt 末尾注入 Sandbox 执行环境说明。
func (b *promptBuilder) WithSandboxContext(enabled bool) *promptBuilder {
	b.sandboxContext = enabled
	return b
}

// Build 组装子代理的完整 system prompt。
func (b *promptBuilder) Build() string {
	var sb strings.Builder
	sb.WriteString(b.systemPrompt)
	fmt.Fprintf(&sb, "\n\n工作目录：%s", b.workDir)

	if b.loader != nil {
		for _, name := range b.skills {
			body, err := b.loader(name)
			if err != nil || strings.TrimSpace(body) == "" {
				continue // skill 加载失败静默忽略，不阻断子代理启动
			}
			fmt.Fprintf(&sb, "\n\n## 预加载技能：%s\n\n%s", name, body)
		}
	}

	if b.sandboxContext {
		sb.WriteString("\n\n## Sandbox 执行环境\n\n" +
			"你当前在一个隔离的 Docker 容器（Ubuntu 22.04）内执行所有工具调用：\n" +
			"- 这是与宿主机完全隔离的临时环境，容器内的任何操作都不会影响用户的真实系统\n" +
			"- 容器有完整的网络访问权限，可访问公网\n" +
			"- 你拥有容器内的完整权限（root）\n" +
			"- 缺少运行时或工具时（如 Go、Node.js、Python、Rust、gcc 等），" +
			"直接使用 apt-get / wget / curl 安装，无需请示用户\n" +
			"- 安装的软件在本次会话期间持续有效\n" +
			"- 代码验证（编译、运行、测试）是开发任务的必要步骤，不得跳过；" +
			"遇到工具缺失时，先安装后验证")
	}

	return sb.String()
}
