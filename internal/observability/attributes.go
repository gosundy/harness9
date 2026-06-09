// Package observability — attributes.go 定义 OTEL Span/Metric 使用的所有属性键常量。
//
// 常量分为三类：
//  1. Span 名称（SpanXxx）：标识 harness9 中各层次的追踪单元
//  2. 通用 Span/Metric 属性键（AttrXxx / MetricXxx）：内部分析与监控维度
//  3. Langfuse 专用属性键（AttrLangfuseXxx / AttrGenAIXxx）：确保 Langfuse v4 UI 正确展示
//
// 维护原则：每次升级 Langfuse 或 OTEL 语义约定版本时，需同步更新本文件的属性键。
package observability

// Span 名称常量（参考 Claude Agent SDK 命名规范）。
// 这些名称对应 Langfuse 中的 Observation/Trace 层级，命名格式为 "harness9.<层级>"。
const (
	SpanInteraction   = "harness9.interaction" // 一次完整 Agent 运行（顶层 Trace，对应用户的一次 Run/RunStream 调用）
	SpanTurn          = "harness9.turn"        // 单个 ReAct Turn（interaction 的子节点，每轮 LLM 调用 + 工具执行）
	SpanLLMRequest    = "harness9.llm_request" // 单次 LLM API 调用（turn 的子节点，由 TracingProvider 创建）
	SpanToolExecution = "harness9.tool"        // 工具执行（turn 的子节点，由 ObservabilityHook 创建）
)

// Span / Metric 属性键常量（harness9 内部属性，不依赖特定 OTEL 语义约定版本）。
// 这些属性用于 harness9 内部监控维度，也作为 Metrics 的标签维度（如按工具名分组计数）。
const (
	AttrSessionID    = "session.id"        // 会话唯一标识符（UUID），关联同一 session 的所有 span
	AttrModel        = "llm.model"         // LLM 模型名称（如 "gpt-4o"、"claude-3-7-sonnet"）
	AttrInputTokens  = "llm.tokens.input"  // LLM 输入 token 数（harness9 内部属性）
	AttrOutputTokens = "llm.tokens.output" // LLM 输出 token 数（harness9 内部属性）
	AttrTurnNumber   = "agent.turn"        // 当前 ReAct Turn 编号（从 1 开始）
	AttrToolName     = "tool.name"         // 工具名称（如 "bash"、"read_file"）
	AttrToolSuccess  = "tool.success"      // 工具执行是否成功（与 ToolResult.IsError 反义）
	AttrAgentType    = "agent.type"        // 代理类型："main"（主代理）或 "sub"（子代理）
	AttrErrorMsg     = "error.message"     // 错误消息文本（当 LLM 调用或工具执行失败时设置）
)

// Metric 名称常量（OTEL Metrics 仪器名称，符合 OpenTelemetry 语义规范的命名格式）。
// 格式：{项目前缀}.{子系统}.{指标名称}，单位附在描述中。
const (
	MetricLLMDuration  = "harness9.llm.request.duration"    // histogram：LLM 单次请求耗时（单位：秒）
	MetricTokensInput  = "harness9.llm.tokens.input"        // counter：LLM 输入 token 累计消耗量
	MetricTokensOutput = "harness9.llm.tokens.output"       // counter：LLM 输出 token 累计生成量
	MetricToolCalls    = "harness9.tool.calls.total"        // counter：工具调用次数（按 tool.name + tool.status 分维度）
	MetricToolDuration = "harness9.tool.execution.duration" // histogram：工具执行耗时（单位：秒）
	MetricTurnsTotal   = "harness9.agent.turns.total"       // counter：Agent 总 Turn 数（全局累计，跨 session）
)

// Langfuse OTEL 属性——Langfuse v4 ingestion 使用以下两组属性映射到 UI 的 Input / Output 字段。
//
// Trace 级别（根 span，即 harness9.interaction）：
//
//	langfuse.trace.input / langfuse.trace.output
//
// Observation 级别（子 span，即 llm_request、tool、turn）：
//
//	langfuse.observation.input / langfuse.observation.output
//
// 旧式的 langfuse.input / langfuse.output 被 Langfuse 存入 attributes 元数据，
// 不会被映射到 Input/Output 展示字段。
const (
	AttrLangfuseTraceInput  = "langfuse.trace.input"
	AttrLangfuseTraceOutput = "langfuse.trace.output"
	AttrLangfuseObsInput    = "langfuse.observation.input"
	AttrLangfuseObsOutput   = "langfuse.observation.output"
)

// GenAI 语义约定属性（OTEL 标准）——Langfuse 以这些属性识别 LLM Generation 并展示 Token 用量与模型信息。
const (
	AttrGenAISystem       = "gen_ai.system"              // LLM 提供商（openai / anthropic 等）
	AttrGenAIRequestModel = "gen_ai.request.model"       // 请求使用的模型名称
	AttrGenAIInputTokens  = "gen_ai.usage.input_tokens"  // 输入 token 数（Langfuse 用于费用估算）
	AttrGenAIOutputTokens = "gen_ai.usage.output_tokens" // 输出 token 数
)
