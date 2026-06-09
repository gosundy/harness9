// testenv.go — 标准 Hermetic（密封隔离）测试环境配置工具。
//
// 所有 eval 测试文件的首行必须调用 SetupHermeticEnv(t)，
// 防止开发者本地 .env 文件中的 API Key 被意外触发，导致：
//   - 产生不必要的费用
//   - 测试结果依赖网络状态而非代码逻辑
//   - CI 与本地行为不一致
package evals

import (
	"os"
	"strings"
	"testing"
)

// SetupHermeticEnv 配置标准 Hermetic（密封隔离）测试环境。
//
// Hermetic testing 是软件工程中的标准实践：将测试与外部依赖完全隔断，确保测试结果
// 只取决于代码变更，不受网络状态、API 配额或密钥有效性影响。
//
// 具体操作：
//   - 清除所有 _API_KEY / _TOKEN / _SECRET 后缀的环境变量（防止意外调用付费 LLM API）
//   - 设置 HARNESS9_EVAL_HERMETIC=1（标识当前运行于隔离模式，可供其他代码感知）
//
// 使用 t.Setenv 而非 os.Setenv：测试结束后自动恢复原始环境，不污染同进程中的其他测试。
// 所有 eval 用例必须在函数开头调用此函数，保证 CI 与本地环境行为完全一致。
func SetupHermeticEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) < 1 {
			continue
		}
		k := parts[0]
		// 清除所有可能触发外部 API 调用的凭据环境变量。
		// 覆盖常见命名模式：OPENAI_API_KEY、ANTHROPIC_API_KEY、GH_TOKEN、AWS_SECRET 等。
		if strings.HasSuffix(k, "_API_KEY") ||
			strings.HasSuffix(k, "_TOKEN") ||
			strings.HasSuffix(k, "_SECRET") {
			t.Setenv(k, "")
		}
	}
	if os.Getenv("HARNESS9_EVAL_HERMETIC") == "" {
		t.Setenv("HARNESS9_EVAL_HERMETIC", "1")
	}
}
