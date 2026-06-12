package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestFetchTool 创建绕过 SSRF 检查的 WebFetchTool（专供测试使用）。
// 生产代码始终使用 NewWebFetchTool()，其中 safetyCheck = isSafeURL。
func newTestFetchTool() *WebFetchTool {
	return &WebFetchTool{
		safetyCheck: func(string) error { return nil },
		client:      &http.Client{},
	}
}

func TestWebFetchSSRFBlock(t *testing.T) {
	tool := NewWebFetchTool()
	args, _ := json.Marshal(map[string]interface{}{"url": "http://169.254.169.254/latest/meta-data"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Error") {
		t.Errorf("expected SSRF error in result, got: %s", result)
	}
}

func TestWebFetchHTMLContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><head><title>Test Page</title></head>
<body><article><h1>Welcome</h1><p>Hello from the test server.</p></article></body></html>`))
	}))
	defer server.Close()

	tool := newTestFetchTool()
	args, _ := json.Marshal(map[string]interface{}{"url": server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result, "Hello from the test server") {
		t.Errorf("result should contain page content\ngot:\n%s", result)
	}
	if !strings.Contains(result, "来源：") {
		t.Errorf("result should contain source header\ngot:\n%s", result)
	}
}

func TestWebFetchPlainTextContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plain text content line 1\nplain text content line 2"))
	}))
	defer server.Close()

	tool := newTestFetchTool()
	args, _ := json.Marshal(map[string]interface{}{"url": server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result, "plain text content line 1") {
		t.Errorf("result should contain plain text\ngot:\n%s", result)
	}
}

func TestWebFetchUnsupportedContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("PDF binary content"))
	}))
	defer server.Close()

	tool := newTestFetchTool()
	args, _ := json.Marshal(map[string]interface{}{"url": server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result, "不支持的内容类型") {
		t.Errorf("expected unsupported content type message\ngot:\n%s", result)
	}
}

func TestWebFetchEmptyURL(t *testing.T) {
	tool := NewWebFetchTool()
	args, _ := json.Marshal(map[string]interface{}{"url": ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Error") {
		t.Errorf("expected error for empty URL, got: %s", result)
	}
}

func TestWebFetchHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tool := newTestFetchTool()
	args, _ := json.Marshal(map[string]interface{}{"url": server.URL})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "404") {
		t.Errorf("expected 404 in result, got: %s", result)
	}
}
