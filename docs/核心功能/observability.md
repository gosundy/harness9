# 可观测性（Observability）

harness9 通过 OpenTelemetry 标准提供完整的链路追踪与指标能力，支持接入 Langfuse、Grafana、Jaeger 等主流平台。

## 架构设计

三条非侵入式接入路径，零核心引擎修改：

| 组件 | 接入点 | 覆盖范围 |
|------|--------|----------|
| `OTELEngineObserver` | `engine.EngineObserver` 接口 | Interaction Span / Turn Span |
| `TracingProvider` | `provider.LLMProvider` 包装 | LLM Request Span / Token Metrics |
| `ObservabilityHook` | `hooks.ToolHook` 接口 | Tool Execution Span / Tool Metrics |

## Span 层次结构

```
harness9.interaction          ← 一次完整 Agent 运行（含 sessionID）
  └── harness9.turn           ← 单个 ReAct Turn
        ├── harness9.llm_request  ← LLM API 调用（含 token 数）
        └── harness9.tool         ← 工具执行（含工具名、成功/失败）
```

Sub-Agent 调用会以独立 `harness9.interaction` 出现（子 Agent 隔离 context）。

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `OTEL_ENABLED` | `false` | `true` 启用 OTEL |
| `OTEL_SERVICE_NAME` | `harness9` | 服务名 |
| `OTEL_EXPORTER_TYPE` | `noop` | `noop` / `stdout` / `otlp` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP 端点（如 `http://localhost:4318`） |

## 接入 Langfuse

```bash
export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
harness9
```

## 接入 Grafana / Jaeger（本地开发）

```bash
# 启动 Jaeger all-in-one
docker run -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one

export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
harness9
# 打开 http://localhost:16686 查看 Trace
```

## 本地调试（stdout 导出器）

```bash
export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=stdout
harness9
```

## 关键 Metrics

| 指标 | 类型 | 说明 |
|------|------|------|
| `harness9.llm.request.duration` | Histogram | LLM 请求延迟（秒） |
| `harness9.llm.tokens.input` | Counter | 输入 Token 总数 |
| `harness9.llm.tokens.output` | Counter | 输出 Token 总数 |
| `harness9.tool.calls.total` | Counter | 工具调用次数（by name + status） |
| `harness9.tool.execution.duration` | Histogram | 工具执行耗时（秒） |
| `harness9.agent.turns.total` | Counter | Agent Turn 总数 |
