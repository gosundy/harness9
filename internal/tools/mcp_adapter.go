// mcp_adapter.go — MCPToolAdapter：将 MCP 工具包装为 harness9 原生 BaseTool 实例。
//
// MCP 工具对 Engine 完全透明，自动获得并发执行、超时控制、错误回传等所有现有机制。
// 工具名采用 mcp__{server}__{tool} 双下划线格式，与 Claude Agent SDK / OpenHarness 保持一致，
// 明确区分 MCP 工具与内置工具，避免 Registry 命名冲突。
//
// 调用链：Manager.InjectTools → NewMCPToolAdapter → registry.Register → Engine 透明分发。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/harness9/internal/schema"
)

// MCPCallerFn 是 MCP 工具调用的函数签名。
// 由 mcp.Manager.InjectTools 以闭包形式提供，捕获对应的 Client 和工具名。
type MCPCallerFn func(ctx context.Context, args json.RawMessage) (string, error)

// MCPToolAdapter 将 MCP 工具包装为实现 BaseTool 接口的适配器。
// 工具名格式为 mcp__{serverName}__{toolName}，与 Claude Agent SDK 和 OpenHarness 保持一致，
// 明确区分 MCP 工具与内置工具，避免命名冲突。
type MCPToolAdapter struct {
	adapterName string
	def         schema.ToolDefinition
	caller      MCPCallerFn
}

// NewMCPToolAdapter 构造 MCPToolAdapter。
//
//   - serverName: MCP Server 配置名（如 "context7"）
//   - toolName: MCP 工具原始名称（如 "resolve-library-id"）
//   - description: 工具描述（直接来自 MCP Server，供 LLM 理解用途）
//   - inputSchema: 工具参数 JSON Schema（json.RawMessage，由 MCP Server 提供，直接透传）
//   - caller: 工具调用函数（由 mcp.Manager 提供闭包）
func NewMCPToolAdapter(serverName, toolName, description string, inputSchema json.RawMessage, caller MCPCallerFn) *MCPToolAdapter {
	name := "mcp__" + SanitizeMCPName(serverName) + "__" + SanitizeMCPName(toolName)
	return &MCPToolAdapter{
		adapterName: name,
		def: schema.ToolDefinition{
			Name:        name,
			Description: fmt.Sprintf("[MCP:%s] %s", serverName, description),
			InputSchema: parseInputSchema(inputSchema),
		},
		caller: caller,
	}
}

// Name 返回适配器的工具名（mcp__{server}__{tool} 格式）。
func (a *MCPToolAdapter) Name() string { return a.adapterName }

// Definition 返回工具定义，供 LLM Provider 了解工具的用途和参数格式。
func (a *MCPToolAdapter) Definition() schema.ToolDefinition { return a.def }

// Execute 调用 MCP Server 的对应工具，透传 LLM 提供的 JSON 参数。
func (a *MCPToolAdapter) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return a.caller(ctx, args)
}

// SanitizeMCPName 将工具或服务器名中的非字母数字字符替换为下划线，
// 确保生成的工具名（mcp__{server}__{tool}）在 Registry 中合法且唯一。
// 该函数同时被 mcp.Manager（构建 ToolDetails）复用，两侧保证命名一致。
func SanitizeMCPName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// parseInputSchema 将 json.RawMessage 转换为 map[string]any，供 LLM Provider 使用。
// 若解析失败，返回空 schema，不阻断工具注册。
func parseInputSchema(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return s
}
