// Package observability 提供 harness9 的可观测能力：OpenTelemetry Tracing 与 Metrics。
//
// 设计原则：所有接入点均为非侵入式——ObservabilityHook 复用 ToolHook 接口，
// TracingProvider 包装 LLMProvider 接口，OTELEngineObserver 通过 WithEngineObserver 注入，
// 不修改任何核心引擎逻辑。默认使用 noop 实现，零开销。
package observability

import (
	"os"
	"strings"
)

// ExporterType 枚举 OTEL 导出器类型。
type ExporterType string

const (
	ExporterNoop   ExporterType = "noop"   // 默认，零开销
	ExporterStdout ExporterType = "stdout" // 开发/调试用
	ExporterOTLP   ExporterType = "otlp"   // 生产（Langfuse / Grafana / Jaeger 等）
)

// Config 保存可观测性配置，全部字段可通过环境变量驱动。
type Config struct {
	Enabled      bool              // OTEL_ENABLED=true 才激活
	ServiceName  string            // OTEL_SERVICE_NAME，默认 "harness9"
	Exporter     ExporterType      // OTEL_EXPORTER_TYPE=noop|stdout|otlp
	OTLPEndpoint string            // OTEL_EXPORTER_OTLP_ENDPOINT，base URL（不含 /v1/traces）
	OTLPHeaders  map[string]string // OTEL_EXPORTER_OTLP_HEADERS，解析后的 HTTP header map
}

// ConfigFromEnv 从环境变量读取 OTEL 配置，返回 Config 实例。
//
// 环境变量：
//   - OTEL_ENABLED                = "true" 启用（其他值视为关闭）
//   - OTEL_SERVICE_NAME           = "harness9"（默认）
//   - OTEL_EXPORTER_TYPE          = "noop" | "stdout" | "otlp"（默认 noop）
//   - OTEL_EXPORTER_OTLP_ENDPOINT = base URL，如 "https://us.cloud.langfuse.com/api/public/otel"
//   - OTEL_EXPORTER_OTLP_HEADERS  = "key1=val1,key2=val2"（如 Langfuse Authorization header）
func ConfigFromEnv() Config {
	enabled := os.Getenv("OTEL_ENABLED") == "true"
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "harness9"
	}
	exporterType := ExporterType(os.Getenv("OTEL_EXPORTER_TYPE"))
	switch exporterType {
	case ExporterStdout, ExporterOTLP:
	default:
		exporterType = ExporterNoop
	}
	return Config{
		Enabled:      enabled,
		ServiceName:  serviceName,
		Exporter:     exporterType,
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OTLPHeaders:  parseOTLPHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
	}
}

// parseOTLPHeaders 解析 "key1=val1,key2=val2" 格式的 header 字符串。
// 按 OTEL 规范：每对以 "," 分隔，key/value 以第一个 "=" 分隔（value 中可含 "="）。
func parseOTLPHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	if raw == "" {
		return headers
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		idx := strings.Index(pair, "=")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(pair[:idx])
		v := strings.TrimSpace(pair[idx+1:])
		if k != "" {
			headers[k] = v
		}
	}
	return headers
}
