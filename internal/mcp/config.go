// Package mcp 提供 MCP（Model Context Protocol）客户端能力。
// 支持 stdio / HTTP 两种传输，将外部 MCP Server 的工具注入 harness9 工具 Registry。
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// ServerConfig 描述单个 MCP Server 的连接配置。
// Type 为空时根据 Command 字段自动推断：有 Command → "stdio"，有 URL → "http"。
type ServerConfig struct {
	// Type 传输类型："stdio" 或 "http"。
	Type string `json:"type"`
	// Command 是 stdio 服务器的可执行程序路径（如 "npx"）。
	Command string `json:"command"`
	// Args 是 Command 的参数列表（如 ["-y", "@upstash/context7-mcp"]）。
	Args []string `json:"args"`
	// Env 是注入子进程的额外环境变量，格式为 ["KEY=VALUE"]。
	Env []string `json:"env"`
	// URL 是 HTTP 服务器地址（type="http" 时必填）。
	URL string `json:"url"`
	// Headers 是 HTTP 请求头，用于认证（如 Bearer token）。
	Headers map[string]string `json:"headers"`
}

// transportType 推断传输类型。
func (c ServerConfig) transportType() string {
	if c.Type != "" {
		return c.Type
	}
	if c.Command != "" {
		return "stdio"
	}
	return "http"
}

// Config 是 .mcp.json 文件的顶层结构体。
type Config struct {
	// Servers 是 MCP Server 名称到配置的映射。
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig 从指定路径读取 .mcp.json。文件不存在时返回空 Config（不报错）。
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{Servers: make(map[string]ServerConfig)}, nil
		}
		return Config{}, fmt.Errorf("read mcp config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]ServerConfig)
	}
	return cfg, nil
}
