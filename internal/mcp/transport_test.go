package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// ---- StdioTransport 边界条件测试（不启动真实子进程，通过 mockTransport 替代）----

// TestStdioTransport_Send_CtxCancel 验证 ctx 取消时 Send 立即返回错误（不永久阻塞）。
// 使用 cancelledMockTransport（定义在 client_test.go）模拟 ctx 已取消的场景。
func TestStdioTransport_Send_CtxCancel(t *testing.T) {
	transport := &cancelledMockTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := transport.Send(ctx, "tools/list", nil)
	if err == nil {
		t.Fatal("Send with cancelled context should return error")
	}
}

// ---- HTTPTransport 单元测试 ----

// TestHTTPTransport_StartIsNoop 验证 HTTPTransport.Start 始终返回 nil（HTTP 是无状态的）。
func TestHTTPTransport_StartIsNoop(t *testing.T) {
	tr := NewHTTPTransport("http://localhost:9999", nil)
	if err := tr.Start(context.Background()); err != nil {
		t.Errorf("HTTPTransport.Start should be noop, got error: %v", err)
	}
}

// TestHTTPTransport_CloseIsNoop 验证 HTTPTransport.Close 始终返回 nil。
func TestHTTPTransport_CloseIsNoop(t *testing.T) {
	tr := NewHTTPTransport("http://localhost:9999", nil)
	if err := tr.Close(); err != nil {
		t.Errorf("HTTPTransport.Close should be noop, got error: %v", err)
	}
}

// TestHTTPTransport_Send_NetworkError 验证向不可达地址发送请求时 Send 返回 HTTP 错误。
// 使用本地不存在的端口（9998），确保立即返回连接错误而不超时。
func TestHTTPTransport_Send_NetworkError(t *testing.T) {
	tr := NewHTTPTransport("http://127.0.0.1:9998/mcp", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := tr.Send(ctx, "tools/list", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Send to unreachable server should return error")
	}
}

// ---- newTransport 工厂函数测试 ----

// TestNewTransport_Stdio 验证 stdio 类型创建 StdioTransport。
func TestNewTransport_Stdio(t *testing.T) {
	cfg := ServerConfig{Type: "stdio", Command: "echo"}
	tr, err := newTransport(cfg)
	if err != nil {
		t.Fatalf("newTransport(stdio) error: %v", err)
	}
	if _, ok := tr.(*StdioTransport); !ok {
		t.Errorf("expected *StdioTransport, got %T", tr)
	}
}

// TestNewTransport_StdioMissingCommand 验证 stdio 类型缺少 Command 字段时返回错误。
func TestNewTransport_StdioMissingCommand(t *testing.T) {
	cfg := ServerConfig{Type: "stdio"} // Command 为空
	_, err := newTransport(cfg)
	if err == nil {
		t.Fatal("stdio transport with empty command should return error")
	}
}

// TestNewTransport_HTTP 验证 http 类型创建 HTTPTransport。
func TestNewTransport_HTTP(t *testing.T) {
	cfg := ServerConfig{Type: "http", URL: "http://example.com/mcp"}
	tr, err := newTransport(cfg)
	if err != nil {
		t.Fatalf("newTransport(http) error: %v", err)
	}
	if _, ok := tr.(*HTTPTransport); !ok {
		t.Errorf("expected *HTTPTransport, got %T", tr)
	}
}

// TestNewTransport_HTTPMissingURL 验证 http 类型缺少 URL 字段时返回错误。
func TestNewTransport_HTTPMissingURL(t *testing.T) {
	cfg := ServerConfig{Type: "http"} // URL 为空
	_, err := newTransport(cfg)
	if err == nil {
		t.Fatal("http transport with empty url should return error")
	}
}

// TestNewTransport_Unknown 验证未知 Type 时返回错误。
func TestNewTransport_Unknown(t *testing.T) {
	cfg := ServerConfig{Type: "grpc"}
	_, err := newTransport(cfg)
	if err == nil {
		t.Fatal("unknown transport type should return error")
	}
}

// TestNewTransport_AutoInferStdio 验证无 Type 但有 Command 时自动推断为 stdio。
func TestNewTransport_AutoInferStdio(t *testing.T) {
	cfg := ServerConfig{Command: "node", Args: []string{"server.js"}}
	tr, err := newTransport(cfg)
	if err != nil {
		t.Fatalf("auto-infer stdio transport error: %v", err)
	}
	if _, ok := tr.(*StdioTransport); !ok {
		t.Errorf("expected *StdioTransport for command-only config, got %T", tr)
	}
}
