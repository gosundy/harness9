// Package observability 提供基于 OpenTelemetry 的可观测性支持，
// 包括 Traces、Metrics 和资源语义约定配置。
//
// 本包作为 harness9 可观测性系统的入口，后续将实现：
//   - Config：从环境变量读取 OTEL 配置
//   - Setup：初始化 TracerProvider 和 MeterProvider
//   - Attributes：公共 span/metric 属性定义
package observability

import (
	_ "go.opentelemetry.io/otel"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	_ "go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	_ "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	_ "go.opentelemetry.io/otel/metric"
	_ "go.opentelemetry.io/otel/sdk"
	_ "go.opentelemetry.io/otel/sdk/metric"
	_ "go.opentelemetry.io/otel/semconv/v1.26.0"
	_ "go.opentelemetry.io/otel/trace"
)
