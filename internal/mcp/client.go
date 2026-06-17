package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolInfo 描述 MCP Server 提供的单个工具（从 tools/list 返回）。
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// contentBlock 是 tools/call 返回结果中的单个内容块。
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolCallResult 是 tools/call 方法的响应结构体。
type toolCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

// toolsListResult 是 tools/list 方法的响应结构体。
type toolsListResult struct {
	Tools []ToolInfo `json:"tools"`
}

// initializeParams 是 initialize 方法的参数结构体。
type initializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      clientInfo `json:"clientInfo"`
	Capabilities    struct{}   `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Client 封装对单个 MCP Server 的操作：连接握手、工具发现和工具调用。
type Client struct {
	name      string
	transport Transport
	Tools     []ToolInfo // 连接成功后填充
}

// newClient 创建 Client，transport 由 Manager 根据 ServerConfig 构建。
func newClient(name string, transport Transport) *Client {
	return &Client{name: name, transport: transport}
}

// Connect 执行 MCP 握手（initialize → notifications/initialized → tools/list）。
// 超时由调用者通过 ctx 控制（建议 30s）。
func (c *Client) Connect(ctx context.Context) error {
	if err := c.transport.Start(ctx); err != nil {
		return fmt.Errorf("start transport: %w", err)
	}

	// 1. initialize
	params := initializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      clientInfo{Name: "harness9", Version: "1.0.0"},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal initialize params: %w", err)
	}
	if _, err := c.transport.Send(ctx, "initialize", raw); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	// 2. notifications/initialized（通知，无需等待响应）
	if err := c.transport.Notify("notifications/initialized", nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	// 3. tools/list
	result, err := c.transport.Send(ctx, "tools/list", nil)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	var list toolsListResult
	if err := json.Unmarshal(result, &list); err != nil {
		return fmt.Errorf("parse tools/list: %w", err)
	}
	c.Tools = list.Tools
	return nil
}

// CallTool 调用指定工具，返回文本输出。
// args 是 LLM 传入的原始 JSON 参数，直接透传给 MCP Server（无需二次解析）。
func (c *Client) CallTool(ctx context.Context, toolName string, args json.RawMessage) (string, error) {
	type callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	raw, err := json.Marshal(callParams{Name: toolName, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("marshal tool call params: %w", err)
	}

	result, err := c.transport.Send(ctx, "tools/call", raw)
	if err != nil {
		return "", fmt.Errorf("tools/call: %w", err)
	}

	var callResult toolCallResult
	if err := json.Unmarshal(result, &callResult); err != nil {
		return "", fmt.Errorf("parse tools/call result: %w", err)
	}

	// 提取所有 text 类型内容块并拼接
	var parts []string
	for _, block := range callResult.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	output := strings.Join(parts, "\n")

	if callResult.IsError {
		return output, fmt.Errorf("tool %s returned error: %s", toolName, output)
	}
	return output, nil
}

// Close 关闭 Client 的底层传输层。
func (c *Client) Close() error {
	return c.transport.Close()
}
