package evals

import (
	"os"
	"strings"
	"testing"
)

// SetupHermeticEnv 配置 hermetic（密封）测试环境，清除所有 API Key 等敏感环境变量。
// 仿 HermesAgent 模式：防止 eval 测试意外调用真实 LLM API，保证 CI 与本地环境完全一致。
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
