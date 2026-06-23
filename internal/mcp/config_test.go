package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_FileNotFound 验证文件不存在时返回空 Config（不报错）。
func TestLoadConfig_FileNotFound(t *testing.T) {
	cfg, err := LoadConfig("/tmp/harness9-test-nonexistent-mcp.json")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg.Servers == nil {
		t.Error("Servers should be initialized to empty map, not nil")
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(cfg.Servers))
	}
}

// TestLoadConfig_ValidFile 验证合法 .mcp.json 被正确解析。
func TestLoadConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	content := `{
		"mcpServers": {
			"context7": {
				"type": "stdio",
				"command": "npx",
				"args": ["-y", "@upstash/context7-mcp"]
			},
			"myapi": {
				"type": "http",
				"url": "https://api.example.com/mcp",
				"headers": {"Authorization": "Bearer token123"}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}

	ctx7 := cfg.Servers["context7"]
	if ctx7.Command != "npx" {
		t.Errorf("context7.Command = %q, want %q", ctx7.Command, "npx")
	}
	if len(ctx7.Args) != 2 {
		t.Errorf("context7.Args len = %d, want 2", len(ctx7.Args))
	}

	myapi := cfg.Servers["myapi"]
	if myapi.URL != "https://api.example.com/mcp" {
		t.Errorf("myapi.URL = %q", myapi.URL)
	}
	if myapi.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("myapi.Headers[Authorization] = %q", myapi.Headers["Authorization"])
	}
}

// TestLoadConfig_InvalidJSON 验证非法 JSON 文件时返回解析错误。
func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected parse error for invalid JSON, got nil")
	}
}

// TestLoadConfig_EmptyServersField 验证 mcpServers 为 null/缺省时 Servers 被初始化为空 map。
func TestLoadConfig_EmptyServersField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Servers == nil {
		t.Error("Servers should be non-nil even when field absent from JSON")
	}
}

// TestServerConfig_TransportType 验证 transportType() 的推断逻辑。
func TestServerConfig_TransportType(t *testing.T) {
	cases := []struct {
		cfg  ServerConfig
		want string
	}{
		{ServerConfig{Type: "stdio"}, "stdio"},
		{ServerConfig{Type: "http"}, "http"},
		{ServerConfig{Command: "npx"}, "stdio"},              // 无 Type，有 Command → stdio
		{ServerConfig{URL: "http://x"}, "http"},              // 无 Type，无 Command → http
		{ServerConfig{}, "http"},                             // 全空 → 默认 http
		{ServerConfig{Type: "stdio", Command: "x"}, "stdio"}, // Type 优先于 Command
	}
	for _, tc := range cases {
		got := tc.cfg.transportType()
		if got != tc.want {
			t.Errorf("transportType(%+v) = %q, want %q", tc.cfg, got, tc.want)
		}
	}
}
