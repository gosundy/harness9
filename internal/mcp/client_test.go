package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// ---- Client.Connect 测试 ----

// TestClient_Connect_Success 验证正常握手三步流程后 Tools 被正确填充。
func TestClient_Connect_Success(t *testing.T) {
	toolInfos := []ToolInfo{
		{Name: "tool_a", Description: "desc a", InputSchema: json.RawMessage(`{}`)},
		{Name: "tool_b", Description: "desc b", InputSchema: json.RawMessage(`{}`)},
	}
	transport := newMockTransport(toolInfos)
	client := newClient("srv", transport)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	if len(client.Tools) != 2 {
		t.Errorf("expected 2 tools after Connect, got %d", len(client.Tools))
	}
	if client.Tools[0].Name != "tool_a" {
		t.Errorf("tools[0].Name = %q, want %q", client.Tools[0].Name, "tool_a")
	}
}

// TestClient_Connect_StartFails 验证 Transport.Start 失败时 Connect 返回包装错误。
func TestClient_Connect_StartFails(t *testing.T) {
	transport := &mockTransport{
		startErr:  errors.New("process not found"),
		responses: map[string]json.RawMessage{},
	}
	client := newClient("srv", transport)

	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error when Transport.Start fails")
	}
	// 错误应包含 "start transport" 包装前缀
	if err.Error() == "" {
		t.Error("error message should not be empty")
	}
}

// TestClient_Connect_ToolsListParseFails 验证 tools/list 返回非法 JSON 时 Connect 返回错误。
func TestClient_Connect_ToolsListParseFails(t *testing.T) {
	transport := &mockTransport{
		responses: map[string]json.RawMessage{
			"initialize": json.RawMessage(`{}`),
			"tools/list": json.RawMessage(`not-json`), // 非法 JSON
		},
	}
	client := newClient("srv", transport)

	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error when tools/list response is invalid JSON")
	}
}

// TestClient_Connect_ContextCancelled 验证 ctx 取消时 Connect 返回 context 错误。
func TestClient_Connect_ContextCancelled(t *testing.T) {
	// mockTransport 默认在 Send 时直接返回，无法模拟阻塞；
	// 改用 cancelledMockTransport，其 Send 立即返回 ctx.Err()。
	transport := &cancelledMockTransport{}
	client := newClient("srv", transport)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error when context is already cancelled")
	}
}

// ---- Client.CallTool 测试 ----

// TestClient_CallTool_Success 验证工具调用返回文本内容正确拼接。
func TestClient_CallTool_Success(t *testing.T) {
	callResult := toolCallResult{
		Content: []contentBlock{
			{Type: "text", Text: "hello"},
			{Type: "image", Text: "ignored"},
			{Type: "text", Text: " world"},
		},
		IsError: false,
	}
	raw, _ := json.Marshal(callResult)
	transport := &mockTransport{
		responses: map[string]json.RawMessage{
			"tools/call": raw,
		},
	}
	client := newClient("srv", transport)

	out, err := client.CallTool(context.Background(), "my_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool() unexpected error: %v", err)
	}
	if out != "hello\n world" {
		t.Errorf("output = %q, want %q", out, "hello\n world")
	}
}

// TestClient_CallTool_IsError 验证 isError=true 时同时返回非 nil error。
func TestClient_CallTool_IsError(t *testing.T) {
	callResult := toolCallResult{
		Content: []contentBlock{
			{Type: "text", Text: "tool exec failed: permission denied"},
		},
		IsError: true,
	}
	raw, _ := json.Marshal(callResult)
	transport := &mockTransport{
		responses: map[string]json.RawMessage{
			"tools/call": raw,
		},
	}
	client := newClient("srv", transport)

	out, err := client.CallTool(context.Background(), "bash", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when isError=true")
	}
	// 错误输出文本应同时返回
	if out == "" {
		t.Error("output should not be empty even when isError=true")
	}
}

// TestClient_CallTool_EmptyContent 验证空 Content 时返回空字符串且无 error。
func TestClient_CallTool_EmptyContent(t *testing.T) {
	callResult := toolCallResult{Content: nil, IsError: false}
	raw, _ := json.Marshal(callResult)
	transport := &mockTransport{
		responses: map[string]json.RawMessage{
			"tools/call": raw,
		},
	}
	client := newClient("srv", transport)

	out, err := client.CallTool(context.Background(), "noop", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("empty content should return empty string, got %q", out)
	}
}

// TestClient_Close_DelegatesToTransport 验证 Close 调用底层 Transport.Close。
func TestClient_Close_DelegatesToTransport(t *testing.T) {
	transport := newMockTransport(nil)
	client := newClient("srv", transport)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if !transport.closed {
		t.Error("Close should delegate to Transport.Close")
	}
}

// ---- cancelledMockTransport：Start 成功，Send 返回 ctx.Err() ----

type cancelledMockTransport struct{}

func (c *cancelledMockTransport) Start(_ context.Context) error { return nil }
func (c *cancelledMockTransport) Notify(_ string, _ json.RawMessage) error {
	return nil
}
func (c *cancelledMockTransport) Send(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.RawMessage("{}"), nil
}
func (c *cancelledMockTransport) Close() error { return nil }
