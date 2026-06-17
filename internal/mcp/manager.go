package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/harness9/internal/logfmt"
	"github.com/harness9/internal/tools"
)

// Status 表示 MCP Server 的连接状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusConnected Status = "connected"
	StatusFailed    Status = "failed"
)

// ServerStatus 是 MCP Server 状态快照，供 TUI 展示。
type ServerStatus struct {
	Name     string
	Status   Status
	ToolsLen int    // 已加载的工具数量（连接成功后填充）
	ErrMsg   string // 连接失败时的错误信息
}

// Manager 管理多个 MCP Server 的连接生命周期。
// 设计原则：连接失败时 fail-soft（记录状态，不阻断启动），工具注入后不动态更新 Registry。
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client
	status  map[string]ServerStatus
	config  Config
	notify  func([]ServerStatus) // TUI 状态更新回调（nil = 不通知）
}

// NewManager 创建 Manager，config 来自 LoadConfig。
func NewManager(config Config) *Manager {
	return &Manager{
		clients: make(map[string]*Client),
		status:  make(map[string]ServerStatus),
		config:  config,
	}
}

// WithNotify 注册状态变更回调（用于 TUI 实时更新）。调用线程不限。
func (m *Manager) WithNotify(fn func([]ServerStatus)) {
	m.notify = fn
}

// Start 并发连接所有已配置的 MCP Server，超时时间为每个 server 30 秒。
// 所有连接结果（成功或失败）均异步返回，本方法会等待全部连接尝试完成后返回。
func (m *Manager) Start(ctx context.Context) error {
	if len(m.config.Servers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	for name, cfg := range m.config.Servers {
		wg.Add(1)
		go func(name string, cfg ServerConfig) {
			defer wg.Done()

			m.mu.Lock()
			m.status[name] = ServerStatus{Name: name, Status: StatusPending}
			m.mu.Unlock()
			m.sendNotify()

			connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			client, err := m.connect(connCtx, name, cfg)
			if err != nil {
				log.Print(logfmt.FormatMsg("mcp", fmt.Sprintf("server %q connect failed: %v", name, err)))
				m.mu.Lock()
				m.status[name] = ServerStatus{Name: name, Status: StatusFailed, ErrMsg: err.Error()}
				m.mu.Unlock()
				m.sendNotify()
				return
			}

			m.mu.Lock()
			m.clients[name] = client
			m.status[name] = ServerStatus{Name: name, Status: StatusConnected, ToolsLen: len(client.Tools)}
			m.mu.Unlock()
			m.sendNotify()
			log.Print(logfmt.FormatMsg("mcp", fmt.Sprintf("server %q connected, %d tools", name, len(client.Tools))))
		}(name, cfg)
	}
	wg.Wait()
	return nil
}

// connect 根据 ServerConfig 构建 Transport 并执行连接握手。
func (m *Manager) connect(ctx context.Context, name string, cfg ServerConfig) (*Client, error) {
	transport, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}
	client := newClient(name, transport)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// newTransport 根据 ServerConfig 创建对应的 Transport 实例。
func newTransport(cfg ServerConfig) (Transport, error) {
	switch cfg.transportType() {
	case "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio transport requires command field")
		}
		return NewStdioTransport(cfg.Command, cfg.Args, cfg.Env), nil
	case "http":
		if cfg.URL == "" {
			return nil, fmt.Errorf("http transport requires url field")
		}
		return NewHTTPTransport(cfg.URL, cfg.Headers), nil
	default:
		return nil, fmt.Errorf("unsupported transport type: %q", cfg.transportType())
	}
}

// InjectTools 将所有已连接 Server 的工具注入 Registry，命名格式为 mcp__{server}__{tool}。
// 工具冲突时记录警告并跳过，不中断注入流程。返回成功注入的工具总数。
func (m *Manager) InjectTools(registry tools.Registry) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for serverName, client := range m.clients {
		for _, toolInfo := range client.Tools {
			// 避免闭包捕获循环变量
			capturedServer := serverName
			capturedTool := toolInfo
			capturedClient := client

			adapter := tools.NewMCPToolAdapter(
				capturedServer,
				capturedTool.Name,
				capturedTool.Description,
				capturedTool.InputSchema,
				func(ctx context.Context, args json.RawMessage) (string, error) {
					return capturedClient.CallTool(ctx, capturedTool.Name, args)
				},
			)
			if err := registry.Register(adapter); err != nil {
				log.Print(logfmt.FormatMsg("mcp", fmt.Sprintf("skip tool %s: %v", adapter.Name(), err)))
				continue
			}
			count++
		}
	}
	return count
}

// Stop 关闭所有活跃的 MCP Server 连接。
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			log.Print(logfmt.FormatMsg("mcp", fmt.Sprintf("close %q: %v", name, err)))
		}
	}
	m.clients = make(map[string]*Client)
}

// Statuses 返回所有 Server 的状态快照（线程安全）。
func (m *Manager) Statuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ServerStatus, 0, len(m.status))
	for _, s := range m.status {
		result = append(result, s)
	}
	return result
}

// sendNotify 在持有锁之外调用通知回调，避免死锁。
func (m *Manager) sendNotify() {
	if m.notify == nil {
		return
	}
	statuses := m.Statuses()
	m.notify(statuses)
}
