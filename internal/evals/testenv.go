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
//   - 设置 HARNESS9_EVAL_HERMETIC=1（标识当前运行于隔离模式）
//
// 所有 eval 用例必须在函数开头调用此函数，保证 CI 与本地环境行为完全一致。
func SetupHermeticEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) < 1 {
			continue
		}
		k := parts[0]
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
