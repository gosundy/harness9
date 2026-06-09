package observability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Providers 持有已初始化的 OTEL tracer 和 meter，以及关闭函数。
type Providers struct {
	Tracer trace.Tracer
	Meter  metric.Meter
	// Shutdown 关闭所有 OTEL provider，应在进程退出时调用（defer）。
	Shutdown func(context.Context) error
}

// Setup 根据 cfg 初始化 OTEL tracer 和 meter。
// 若 cfg.Enabled=false 或 cfg.Exporter=noop，返回零开销的 noop 实现。
func Setup(ctx context.Context, cfg Config) (*Providers, error) {
	if !cfg.Enabled || cfg.Exporter == ExporterNoop {
		return noopProviders(), nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 otel resource 失败: %w", err)
	}

	// 仅对 http:// 前缀的端点使用非加密传输；https:// 端点（如 Langfuse、Grafana Cloud）
	// 必须保留 TLS，否则握手失败。
	insecure := strings.HasPrefix(cfg.OTLPEndpoint, "http://")

	var spanExporter sdktrace.SpanExporter
	switch cfg.Exporter {
	case ExporterStdout:
		spanExporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	case ExporterOTLP:
		traceOpts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint),
		}
		if insecure {
			traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
		}
		spanExporter, err = otlptracehttp.New(ctx, traceOpts...)
	default:
		return noopProviders(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("创建 trace exporter 失败: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(spanExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var metricExporter sdkmetric.Exporter
	switch cfg.Exporter {
	case ExporterStdout:
		metricExporter, err = stdoutmetric.New()
	case ExporterOTLP:
		metricOpts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint),
		}
		if insecure {
			metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
		}
		metricExporter, err = otlpmetrichttp.New(ctx, metricOpts...)
	}
	if err != nil {
		_ = tp.Shutdown(ctx) // 清理已创建的 tracer provider
		return nil, fmt.Errorf("创建 metric exporter 失败: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
			sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	tracer := otel.Tracer(cfg.ServiceName)
	meter := otel.Meter(cfg.ServiceName)

	shutdown := func(ctx context.Context) error {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		return nil
	}
	return &Providers{Tracer: tracer, Meter: meter, Shutdown: shutdown}, nil
}

// NewNoopProviders 返回零开销的 noop 实现，供测试使用。
func NewNoopProviders() *Providers {
	return noopProviders()
}

// noopProviders 返回零开销的 noop 实现。
func noopProviders() *Providers {
	return &Providers{
		Tracer:   noop.NewTracerProvider().Tracer("harness9"),
		Meter:    otel.GetMeterProvider().Meter("harness9"),
		Shutdown: func(_ context.Context) error { return nil },
	}
}
