package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/harness9/internal/tools"
)

// mockTransport 是 Transport 接口的确定性测试替身，避免启动真实子进程。
type mockTransport struct {
	responses map[string]json.RawMessage
	startErr  error
	closed    bool
}

func (m *mockTransport) Start(_ context.Context) error {
	return m.startErr
}

func (m *mockTransport) Close() error {
	m.closed = true
	return nil
}

func (m *mockTransport) Notify(_ string, _ json.RawMessage) error {
	return nil
}

func (m *mockTransport) Send(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	if resp, ok := m.responses[method]; ok {
		return resp, nil
	}
	return json.RawMessage("{}"), nil
}

// newMockTransport 创建预填 initialize 和 tools/list 响应的 mockTransport。
func newMockTransport(toolInfos []ToolInfo) *mockTransport {
	list := toolsListResult{Tools: toolInfos}
	listRaw, _ := json.Marshal(list)
	return &mockTransport{
		responses: map[string]json.RawMessage{
			"initialize": json.RawMessage(`{}`),
			"tools/list": listRaw,
		},
	}
}

// TestManager_Start_Success 直接向 manager 注入已连接的 Client，
// 验证 Statuses() 返回正确的连接状态与 ToolsLen。
func TestManager_Start_Success(t *testing.T) {
	mgr := NewManager(Config{})

	toolInfos := []ToolInfo{
		{Name: "tool_a", Description: "Tool A"},
		{Name: "tool_b", Description: "Tool B"},
	}
	transport := newMockTransport(toolInfos)
	client := newClient("test-server", transport)
	client.Tools = toolInfos

	mgr.mu.Lock()
	mgr.clients["test-server"] = client
	mgr.status["test-server"] = ServerStatus{
		Name:     "test-server",
		Status:   StatusConnected,
		ToolsLen: len(toolInfos),
	}
	mgr.mu.Unlock()

	statuses := mgr.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	s := statuses[0]
	if s.Status != StatusConnected {
		t.Errorf("expected status %q, got %q", StatusConnected, s.Status)
	}
	if s.ToolsLen != len(toolInfos) {
		t.Errorf("expected ToolsLen %d, got %d", len(toolInfos), s.ToolsLen)
	}
}

// TestManager_InjectTools 验证 InjectTools 将已连接 server 的工具注入 Registry，
// 返回值等于注入工具数量，且 Registry 中工具数量正确。
func TestManager_InjectTools(t *testing.T) {
	mgr := NewManager(Config{})

	toolInfos := []ToolInfo{
		{Name: "tool_x", Description: "Tool X", InputSchema: json.RawMessage(`{}`)},
		{Name: "tool_y", Description: "Tool Y", InputSchema: json.RawMessage(`{}`)},
	}
	transport := newMockTransport(toolInfos)
	client := newClient("server-a", transport)
	client.Tools = toolInfos

	mgr.mu.Lock()
	mgr.clients["server-a"] = client
	mgr.status["server-a"] = ServerStatus{Name: "server-a", Status: StatusConnected, ToolsLen: len(toolInfos)}
	mgr.mu.Unlock()

	registry := tools.NewRegistry()
	count := mgr.InjectTools(registry)
	if count != 2 {
		t.Errorf("expected InjectTools to return 2, got %d", count)
	}
	defs := registry.GetAvailableTools()
	if len(defs) != 2 {
		t.Errorf("expected 2 tools in registry, got %d", len(defs))
	}
}

// TestManager_InjectTools_DuplicateSkipped 验证对同一个 Registry 二次调用 InjectTools
// 时，已存在的工具被跳过，第二次调用返回 0。
func TestManager_InjectTools_DuplicateSkipped(t *testing.T) {
	mgr := NewManager(Config{})

	toolInfos := []ToolInfo{
		{Name: "tool_dup", Description: "Dup Tool", InputSchema: json.RawMessage(`{}`)},
	}
	transport := newMockTransport(toolInfos)
	client := newClient("server-dup", transport)
	client.Tools = toolInfos

	mgr.mu.Lock()
	mgr.clients["server-dup"] = client
	mgr.status["server-dup"] = ServerStatus{Name: "server-dup", Status: StatusConnected, ToolsLen: len(toolInfos)}
	mgr.mu.Unlock()

	registry := tools.NewRegistry()

	first := mgr.InjectTools(registry)
	if first != 1 {
		t.Errorf("expected first InjectTools to return 1, got %d", first)
	}

	second := mgr.InjectTools(registry)
	if second != 0 {
		t.Errorf("expected second InjectTools to return 0 (duplicates skipped), got %d", second)
	}
}
