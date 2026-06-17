# MCP 工具集成技术方案

harness9 的 MCP 集成让 Agent 可以无缝调用任意遵循 [Model Context Protocol](https://modelcontextprotocol.io) 规范的外部工具服务器。MCP 工具以 `mcp__{server}__{tool}` 格式注入统一工具注册表，对 Engine 完全透明——并发执行、超时控制、错误回传等所有现有机制自动生效，不需要对核心循环改动一行。

---

## 架构总览

```
.mcp.json
    ↓ LoadConfig
Config（Servers map）
    ↓ Manager.Start（并发 goroutine，30s timeout/server，fail-soft）
    ↓ newTransport（stdio / http）
    ↓ Client.Connect（initialize → notifications/initialized → tools/list）
ServerStatus + ToolDetails
    ↓ sendNotify → mcpNotifyCh → TUI MCPBar / /mcp 面板
    ↓ Manager.InjectTools（MCPToolAdapter per tool）
tools.Registry
    ↓ Engine.runLoop（LLM 调用携带完整工具列表）
LLM → ToolCall（mcp__context7__resolve_library_id）
    ↓ Registry.Execute → MCPToolAdapter.Execute
    ↓ Client.CallTool（tools/call JSON-RPC）
MCP Server → 工具结果 → Observation 注入上下文
```

---

## 配置（`.mcp.json`）

harness9 在每次启动时从项目根目录的 `.mcp.json` 加载 MCP 配置，文件不存在时静默忽略，不影响启动。

### 格式

```json
{
  "mcpServers": {
    "context7": {
      "command": "npx",
      "args": ["-y", "@upstash/context7-mcp"]
    },
    "remote-api": {
      "type": "http",
      "url": "https://api.example.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}
```

### ServerConfig 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | string | 传输类型：`"stdio"` 或 `"http"`；省略时根据 `command` / `url` 自动推断 |
| `command` | string | stdio 服务器的可执行程序（如 `"npx"`, `"python"`）|
| `args` | []string | 命令行参数（如 `["-y", "@upstash/context7-mcp"]`） |
| `env` | []string | 注入子进程的额外环境变量（`"KEY=VALUE"` 格式） |
| `url` | string | HTTP 服务器地址（type=http 时必填） |
| `headers` | map | HTTP 请求头（如认证 Bearer Token） |

### 加载逻辑

```go
// internal/mcp/config.go
func LoadConfig(path string) (Config, error) {
    data, err := os.ReadFile(path)
    if os.IsNotExist(err) {
        return Config{Servers: make(map[string]ServerConfig)}, nil  // 静默返回空配置
    }
    // ...
}
```

文件不存在时返回空 `Config`，不报错——无 MCP 配置时 harness9 行为与引入前完全一致。

---

## 传输层（`internal/mcp/transport.go`）

### Transport 接口

```go
type Transport interface {
    Start(ctx context.Context) error
    Send(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
    Notify(method string, params json.RawMessage) error
    Close() error
}
```

所有传输实现都满足此接口，Client 层对具体传输完全透明。

### StdioTransport（主力实现）

stdio 传输通过子进程的 stdin/stdout 实现 JSON-RPC 2.0 协议。每条消息是一行完整的 JSON（NDJSON），通过数字 ID 关联请求与响应。

**核心数据结构：**

```go
type StdioTransport struct {
    command string
    args    []string
    env     []string

    cmd  *exec.Cmd
    enc  *json.Encoder  // 写入 stdin（由 writeMu 保护）
    scan *bufio.Scanner // 读取 stdout

    writeMu sync.Mutex          // 保护 enc（多 goroutine 并发写 stdin）
    nextID  atomic.Int64        // 单调递增请求 ID
    mu      sync.Mutex          // 保护 pending map
    pending map[int64]chan rpcResponse // ID → 等待 channel
    done    chan struct{}        // readLoop 退出信号
}
```

**并发安全设计：**

- `writeMu`：保护对 stdin 的写操作——多个工具调用在同一 Turn 内并发执行时，各自的 `Send` 调用需要序列化写入
- `mu`：保护 `pending` map——`Send` 注册 channel、`readLoop` 路由响应，两侧并发操作
- `done` channel：`readLoop` goroutine 退出时关闭，`Send` 的三路 select 通过它检测传输层关闭

**readLoop goroutine：**

```go
func (t *StdioTransport) readLoop() {
    defer close(t.done)
    for t.scan.Scan() {
        line := t.scan.Bytes()
        var resp rpcResponse
        if err := json.Unmarshal(line, &resp); err != nil {
            continue // 忽略非 JSON 行（如 MCP Server 的启动日志）
        }
        if resp.ID == nil {
            continue // 服务器主动推送的通知，当前版本忽略
        }
        t.mu.Lock()
        ch, ok := t.pending[*resp.ID]
        if ok {
            delete(t.pending, *resp.ID)
        }
        t.mu.Unlock()
        if ok {
            ch <- resp
        }
    }
    // 传输关闭：通知所有挂起请求失败，避免调用方永久阻塞
    t.mu.Lock()
    defer t.mu.Unlock()
    for id, ch := range t.pending {
        ch <- rpcResponse{Error: &rpcError{Message: "transport closed"}}
        delete(t.pending, id)
    }
}
```

**Send 三路 select：**

```go
select {
case resp := <-ch:        // 正常响应
    if resp.Error != nil { return nil, fmt.Errorf("rpc error %d: %s", ...) }
    return resp.Result, nil
case <-ctx.Done():        // 调用方超时/取消
    t.mu.Lock(); delete(t.pending, id); t.mu.Unlock()
    return nil, ctx.Err()
case <-t.done:            // 传输层意外关闭
    return nil, fmt.Errorf("transport closed")
}
```

三路 select 保证：工具调用超时（`WithToolTimeout` 配置，默认 60s）、context 取消（用户 Ctrl+C）、MCP Server 进程崩溃，三种场景都能干净退出，不泄漏 goroutine 或 channel。

**Scanner 缓冲区：**

`scan.Buffer(make([]byte, 1024*1024), 1024*1024)` 设置 1MB 读缓冲，适应 `tools/list` 返回大量工具定义的场景（如某些 MCP Server 提供上百个工具时，单次 JSON 响应可能超过默认 64KB 限制）。

### HTTPTransport（无状态实现）

HTTP 传输将每次 `Send` 封装为一个独立的 `POST` 请求，无持久连接状态。适用于提供 Streamable HTTP 端点的 MCP Server。

```go
// Start 是 no-op：HTTP 是无状态的，无需预建连接
func (t *HTTPTransport) Start(_ context.Context) error { return nil }

// Send：POST JSON-RPC 请求体，解析响应
func (t *HTTPTransport) Send(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
    id := t.nextID.Add(1)
    req := rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
    // ... POST to t.url with t.headers ...
}
```

---

## 客户端（`internal/mcp/client.go`）

Client 封装 MCP 协议握手和工具调用逻辑。

### 握手流程（Connect）

MCP 规范要求客户端在开始使用工具前完成三步握手：

```
1. Client → Server: {"jsonrpc":"2.0","id":1,"method":"initialize",
                      "params":{"protocolVersion":"2024-11-05",
                                "clientInfo":{"name":"harness9","version":"1.0.0"},
                                "capabilities":{}}}
   Server → Client: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},...}}

2. Client → Server: {"jsonrpc":"2.0","method":"notifications/initialized"}
   （通知，无 ID，Server 不返回响应）

3. Client → Server: {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
   Server → Client: {"jsonrpc":"2.0","id":2,"result":{"tools":[
     {"name":"resolve-library-id","description":"...","inputSchema":{...}},
     ...
   ]}}
```

握手完成后，`client.Tools` 填充为工具列表，供 Manager 构建 ToolDetails 和注入 Registry。

### 工具调用（CallTool）

```go
func (c *Client) CallTool(ctx context.Context, toolName string, args json.RawMessage) (string, error) {
    // 构造 tools/call 请求
    raw, _ := json.Marshal(struct {
        Name      string          `json:"name"`
        Arguments json.RawMessage `json:"arguments"`
    }{Name: toolName, Arguments: args})

    result, err := c.transport.Send(ctx, "tools/call", raw)
    // ...

    // 提取 text 类型内容块
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
```

MCP `tools/call` 返回的 `content` 是一个数组，每个元素可以是 `text`、`image` 或 `resource` 类型。harness9 当前提取所有 `text` 块拼接为字符串，这覆盖了绝大多数实际 MCP Server 的返回格式。

---

## Manager（`internal/mcp/manager.go`）

Manager 是多 Server 生命周期的单一协调者，持有所有活跃 Client 实例，并向 TUI 通知状态变化。

### 并发启动（Start）

```go
func (m *Manager) Start(ctx context.Context) error {
    var wg sync.WaitGroup
    for name, cfg := range m.config.Servers {
        wg.Add(1)
        go func(name string, cfg ServerConfig) {  // 每个 server 独立 goroutine
            defer wg.Done()

            // 先发出 pending 状态通知，TUI 立即显示"连接中"
            m.mu.Lock()
            m.status[name] = ServerStatus{Name: name, Status: StatusPending}
            m.mu.Unlock()
            m.sendNotify()

            connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
            defer cancel()

            client, err := m.connect(connCtx, name, cfg)
            if err != nil {
                // fail-soft：记录失败状态，不阻断其他 server 的连接
                m.status[name] = ServerStatus{..., Status: StatusFailed, ErrMsg: err.Error()}
                m.sendNotify()
                return
            }

            // 成功：构建 ToolDetails 并通知 TUI
            toolDetails := buildToolDetails(name, client.Tools)
            m.status[name] = ServerStatus{..., Status: StatusConnected, ToolDetails: toolDetails}
            m.sendNotify()
        }(name, cfg)
    }
    wg.Wait()
    return nil
}
```

**设计决策：fail-soft**

单个 Server 连接失败（如 npx 未安装、网络不通）不返回 error，不阻断其他 Server 的初始化，也不阻止 harness9 启动。失败状态会持续展示在 TUI MCPBar 中，用户可感知但不受阻。

**设计决策：异步调用**

在 `main.go` 中，Manager.Start 在独立 goroutine 中运行：

```go
go func() {
    if err := mcpMgr.Start(ctx); err != nil { ... }
    injected := mcpMgr.InjectTools(registry)
}()
```

这避免了 `npx` 类 Server 的冷启动（下载依赖可能需要数十秒）阻塞 TUI 渲染。用户打开 TUI 即可使用内置工具，MCP 工具在后台静默接入，MCPBar 状态实时更新。

### InjectTools

```go
func (m *Manager) InjectTools(registry tools.Registry) int {
    m.mu.RLock()
    defer m.mu.RUnlock()

    count := 0
    for serverName, client := range m.clients {
        for _, toolInfo := range client.Tools {
            // 关键：局部变量捕获，避免闭包捕获循环变量的经典 Go bug
            capturedServer := serverName
            capturedTool   := toolInfo
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
                // 重名冲突时跳过，不中断注入（同名内置工具优先）
                continue
            }
            count++
        }
    }
    return count
}
```

### sendNotify（无锁通知）

```go
func (m *Manager) sendNotify() {
    if m.notify == nil { return }
    statuses := m.Statuses() // Statuses 内部取 RLock，在 mu 之外调用
    m.notify(statuses)       // 不持有 mu，避免回调中可能产生的死锁
}
```

`sendNotify` 设计为在释放锁之后调用。如果在持有锁时调用 `notify`，而 `notify` 回调（如向 channel 发送）恰好阻塞并触发另一个需要 `m.mu` 的操作，将形成死锁。

---

## MCPToolAdapter（`internal/tools/mcp_adapter.go`）

MCPToolAdapter 是 MCP 工具进入 harness9 工具系统的唯一入口，实现 `tools.BaseTool` 接口：

```go
type MCPCallerFn func(ctx context.Context, args json.RawMessage) (string, error)

type MCPToolAdapter struct {
    adapterName string
    def         schema.ToolDefinition
    caller      MCPCallerFn
}

func (a *MCPToolAdapter) Name() string                        { return a.adapterName }
func (a *MCPToolAdapter) Definition() schema.ToolDefinition   { return a.def }
func (a *MCPToolAdapter) Execute(ctx context.Context, args json.RawMessage) (string, error) {
    return a.caller(ctx, args)
}
```

### 命名规范：`mcp__{server}__{tool}`

工具名采用双下划线前缀格式，与 Claude Agent SDK 和 OpenHarness 保持一致：

```
mcp__context7__resolve_library_id
mcp__context7__get_library_docs
mcp__github__list_issues
```

**为什么双下划线**：单下划线（OpenCode 方案：`context7_resolve_library_id`）存在与普通工具名歧义的风险，双下划线在视觉和解析上都能清晰区分 MCP 工具与内置工具。

**sanitizeMCPName**：工具名或服务器名中的非字母数字字符（如 `-`、`.`、空格）统一替换为 `_`：

```go
func sanitizeMCPName(name string) string {
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
```

例：`resolve-library-id` → `resolve_library_id`，`my server` → `my_server`。

### 对 Engine 的透明性

MCPToolAdapter 满足 `tools.BaseTool` 接口后，Engine 主循环对其与 `BashTool`、`ReadFileTool` 等内置工具完全一视同仁：

- **并发执行**：同一 Turn 内多个 MCP 工具并发执行，每个调用独立占用工具 goroutine
- **超时控制**：`WithToolTimeout`（默认 60s）通过 ctx 传递到 `CallTool` → `Transport.Send` 的三路 select，超时自动取消
- **错误回传**：工具执行失败时，`Registry.Execute` 包装为 `ToolResult{IsError: true}`，错误信息原样回传给 LLM 触发自愈重试

---

## TUI 集成

### MCPBar（状态栏）

MCPBar 显示在 SandboxBar 下方，仅在有配置的 MCP Server 时渲染：

```
[MCP] context7 ● 2 tools │ remote-api ✗ failed
```

状态颜色编码：

| 状态 | 颜色 | 含义 |
|------|------|------|
| 绿色 `●` | Color("10") | 已连接，工具可用 |
| 黄色 `○` | Color("11") | 连接中（pending） |
| 红色 `✗` | Color("9")  | 连接失败 |

### channel 驱动的实时更新

状态更新通过 Go channel 从 Manager 推送到 TUI，复用 SandboxBar 的相同模式：

```
Manager.sendNotify()
    → mcpNotifyCh <- statuses（main.go 中的 8-slot 带缓冲 channel）
    → waitMCPUpdate(mcpCh) tea.Cmd（阻塞读 channel）
    → mcpUpdateMsg{statuses}
    → Update() case mcpUpdateMsg: m.mcpServers = msg.statuses
    → View() renderMCPBar() / renderMCPPanel()
```

`mcpNotifyCh` 使用 8 个槽位的带缓冲 channel，`select { default: }` 非阻塞发送——Manager goroutine 不会因 TUI 背压而阻塞，最多丢弃一次中间状态（下次状态变化会刷新）。

### `/mcp` 工具面板

输入 `/mcp`（支持 Tab 补全）打开模态工具面板，展示完整工具列表：

```
MCP 工具管理

● context7  2 工具
  · mcp__context7__resolve_library_id
    Resolves a library or package name to a Context7-compatible library ID
  · mcp__context7__get_library_docs
    Fetches up-to-date documentation for a library

e 编辑配置  ↑↓/jk 滚动  Esc 关闭
```

**快捷键：**

| 键 | 动作 |
|----|------|
| `e` / `E` | 暂停 TUI（`tea.ExecProcess`），打开 `.mcp.json` 配置文件 |
| `↑` / `k` | 向上滚动工具列表 |
| `↓` / `j` | 向下滚动工具列表 |
| `Esc` | 关闭面板 |
| `Ctrl+C` | 退出程序 |

**编辑器打开逻辑：**

```go
func openMCPConfigCmd(path string) tea.Cmd {
    editor := os.Getenv("EDITOR")
    if editor != "" {
        // $EDITOR 设置时（vim/nano/etc）：暂停 TUI，编辑器完全接管终端，退出后恢复 TUI
        return tea.ExecProcess(exec.Command(editor, path), func(err error) tea.Msg {
            return mcpEditorDoneMsg{err: err}
        })
    }
    // 未设置 $EDITOR：macOS 用 open，Linux 用 xdg-open（后台打开，TUI 立即恢复）
    openCmd := "open"
    if runtime.GOOS == "linux" { openCmd = "xdg-open" }
    return tea.ExecProcess(exec.Command(openCmd, path), func(err error) tea.Msg {
        return mcpEditorDoneMsg{err: err}
    })
}
```

`tea.ExecProcess` 是 Bubbletea 专为外部程序集成设计的 API：调用时 TUI 主动释放终端控制权（`p.ReleaseTerminal()`），外部程序结束后重新接管（`p.RestoreTerminal()`）。这使得终端编辑器（vim/nano）可以完整接管屏幕，用户编辑完成后 TUI 无缝恢复。

---

## 与主流框架的对比

harness9 MCP 实现参考了 6 个主流 Agent Harness 框架的设计，核心决策如下：

| 维度 | harness9 选择 | 对标参考 |
|------|--------------|---------|
| 工具集成模式 | **Adapter 注入 Registry**（MCPToolAdapter） | OpenHarness（McpToolAdapter） |
| 命名规范 | `mcp__{server}__{tool}` **双下划线** | Claude Agent SDK、OpenHarness |
| Server 生命周期 | **Session 级**（main.go 启动时连接，退出时 Stop） | OpenHarness（connect_all/close） |
| 连接策略 | **并发 fail-soft**（每 Server 独立 goroutine，失败不阻断） | OpenHarness（connect_all 并发） |
| 传输层 | **stdio + HTTP**（接口抽象，两种实现） | OpenCode（stdio + HTTP + SSE） |
| 大工具集 | 当前无特殊优化 | Claude Agent SDK（Tool Search，≤10,000 工具） |

**选择 Adapter 而非 lazy merge 的原因**：harness9 的 Registry 是 Engine 调用路径上的单一入口，Adapter 模式让 MCP 工具在注册阶段就"成为" harness9 工具，无需在 LLM 调用时做任何 MCP 特殊处理。对比 OpenCode 的 `MCP.tools()` lazy merge 方案，Adapter 模式的好处是：工具注册失败（如命名冲突）在启动时就能发现，而非在 LLM 调用时才暴露。

---

## 调试与常见问题

**Server 连接失败（MCPBar 显示红色 `✗`）**

查看启动日志中的 `[mcp]` 前缀行：
```bash
go run ./cmd/harness9 2>&1 | grep '\[mcp\]'
# [mcp] server "context7" connect failed: start process npx: ...
```

常见原因：
- `npx` 未安装或不在 PATH 中
- 网络问题（HTTP Server 不可达）
- MCP Server 包名错误（查看 args）

**工具注入但 LLM 不调用**

MCP 工具的描述直接来自 MCP Server 的 `tools/list` 响应，质量参差不齐。如果 LLM 不调用某工具，可以通过 `/mcp` 面板查看工具描述，确认描述是否清晰。

**编辑配置后立即生效**

当前版本修改 `.mcp.json` 后需要重启 harness9 生效（Server 连接在启动时建立）。热重载（运行时重新连接）是未来可扩展方向，接入点在 `Manager.Reconnect` 方法（当前未暴露给 TUI）。
