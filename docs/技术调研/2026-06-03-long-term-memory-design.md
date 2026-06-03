# harness9 Long-Term Memory 系统设计

> 设计日期：2026-06-03
> 状态：已通过设计评审，待实现
> 调研依据：`docs/技术调研/long-term-memory-调研.md`
> 范围：Phase 1+2 完整实现 + Phase 3 仅接口

---

## 1. 背景与目标

harness9 目前具备完善的**短期记忆**（`internal/memory/`）：SQLiteSession（WAL + 事务）、SummarizationCompactor（LLM 摘要压缩，80% 阈值触发）、TokenBudgetCompactor（字符截断回退）。但**长期记忆（跨会话持久化）尚未实现**。

本设计为 harness9 新增 Long-Term Memory（LTM）能力，使 Agent 能够跨会话积累知识、记住用户偏好、复用过去经验。设计基于对 6 个主流框架（DeepAgents / OpenHarness / OpenClaw / HermesAgent / Claude Agent SDK 等）的调研结论。

**核心约束**：零新增第三方依赖（复用 `modernc.org/sqlite`，已验证支持 FTS5），遵循 harness9"简洁 / 完备 / 生产可用"理念。

---

## 2. 架构与包边界

新增自包含包 **`internal/ltm/`**，与 `internal/memory/`（短期记忆）保持隔离。LTM **复用已有的 `state.db` 连接**而非另开连接，保证 WAL 单写者语义。

```
internal/ltm/
├── entry.go        # Entry 结构体 + Category 类型 + SHA256 签名计算
├── store.go        # 基于 *sql.DB 的 Store：Upsert/Search/List/SoftDelete/PurgeExpired/StaleCandidates
├── precis.go       # MEMORY.md 物化视图（渲染 top-N 条目 → 有界文件）
├── extractor.go    # 基于 LLM 的压缩前事实提取（复用 Summarizer 接口）
└── provider.go     # 仅 Phase 3 接口：Provider / Embedder / Consolidator（+ noopProvider 空实现）
internal/tools/
├── memory_write.go  # memory_write 工具（add/update/remove）
└── memory_search.go # memory_search 工具（FTS5 查询）
```

**包归属决策**：LTM 独立成包，而非报告建议的 `internal/memory/ltm.go`。理由：`internal/memory/` 在 CLAUDE.md 中被定义为短期记忆，混入长期记忆会模糊模块边界。

**连接共享**：`Manager` 新增 `DB() *sql.DB` 访问器，向 ltm 包暴露底层连接。`ltm.NewStore(db)` 在构造时自行执行 `CREATE TABLE IF NOT EXISTS` 迁移——LTM schema 的所有权留在 ltm 包内，符合"接口/数据归属在使用者侧"的项目惯例。

---

## 3. 核心设计决策：MEMORY.md 与 SQLite 的关系

采用**方案 A —— 物化视图（Materialized View）**：

- **SQLite 是唯一事实源**。所有结构化记忆条目存于 `long_term_memories` 表。
- **MEMORY.md 是 SQLite 的有界物化视图**：由 top-N 条非过期、非禁用、按 `importance` 降序排列的条目自动渲染，**每次写入时重新生成**，硬上限约 5KB（可配置字节预算）。
- **长尾通过 `memory_search`（FTS5，按需 JIT）触达**。

**收益**：单一事实源、无数据漂移、天然规避 OpenClaw 的"token bomb"问题（精华文件有界，不随记忆总量线性膨胀）。

**取舍**：每次写入需重渲精华文件（成本可忽略，文件 ≤5KB）。

被否决的方案 B（报告字面的"独立存储"）：MEMORY.md 自由文本与 SQLite 表分别人工维护——两个事实源会漂移，且 LLM 需同时维护两边，复杂度更高。

---

## 4. 存储 Schema

```sql
CREATE TABLE IF NOT EXISTS long_term_memories (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,
    category     TEXT,                 -- knowledge | preference | task | skill
    importance   INTEGER DEFAULT 0,    -- 0-10，决定精华排序 + 陈旧识别
    signature    TEXT UNIQUE,          -- SHA256(normalize(content)) 去重指纹
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    use_count    INTEGER DEFAULT 0,
    ttl_days     INTEGER,              -- NULL = 永不过期
    disabled     INTEGER DEFAULT 0,    -- 软删除标志
    tags         TEXT                  -- JSON 数组
);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    title, content,
    content=long_term_memories,
    content_rowid=rowid
);

-- AFTER INSERT/UPDATE/DELETE 触发器保持 FTS5 外部内容表同步
```

**Go 数据结构**：

```go
type Category string // "knowledge" | "preference" | "task" | "skill"

type Entry struct {
    ID         string
    Title      string
    Content    string
    Category   Category
    Importance int       // 0-10
    Signature  string    // SHA256(normalize(content))
    CreatedAt  time.Time
    UpdatedAt  time.Time
    LastUsedAt time.Time
    UseCount   int
    TTLDays    int       // 0 = 永不过期
    Disabled   bool
    Tags       []string
}
```

`normalize(content)`：去除首尾空白 + 折叠连续空白 + 小写化，用于稳定的去重签名计算。

---

## 5. 触发时机（报告 5.2 的全部三路）

### 5.1 显式工具调用
- `memory_write`：动作 `add`/`update`/`remove`，由 LLM 在任意 turn 主动调用。
- `memory_search`：FTS5 全文检索，按需召回历史记忆。

### 5.2 压缩前提取（Pre-Compaction Extraction）
`SummarizationCompactor` 新增可选 `Extractor` 字段。由于 `Compact(msgs) []schema.Message` 签名不含 `ctx`/`error`，提取器与现有 `summarize()` 一致，使用自己的 `context.Background()` + 超时。

执行时机：在 `head` 消息被摘要抹除*之前*，用 LLM 从中提取持久事实并 upsert 到 Store。

**Fail-open 原则**：提取失败仅记日志，绝不阻断压缩流程。

### 5.3 Turn 粒度 nudge
引擎统计 turn 数，每 N 轮（可配置，默认关闭；建议值 10）在上下文中注入一行提示，提醒 LLM 持久化值得记住的内容。在 agent loop 中接入。

---

## 6. 注入 Context

**混合注入策略**：

1. **MEMORY.md 全量注入**：`DefaultPromptBuilder` 新增 `WithLongTermMemory(content string)` 选项（或读取回调），将精华内容追加到 System Prompt 固定位置。Anthropic provider 自动附加 `cache_control: ephemeral` breakpoint 降低重复 token 成本。
2. **按需 FTS 检索**：详细记忆通过 `memory_search` 工具 JIT 加载，结果以工具返回值注入当前 turn。

精华快照在 `PromptBuilder.Build()` 时固定（frozen snapshot at session init），与 HermesAgent 模式一致。

---

## 7. 冲突 / 遗忘 / 强化机制

| 机制 | 设计 |
|------|------|
| **去重** | `SHA256(normalize(content))` 签名；内容相同 → 刷新 `updated_at` + 自增 `use_count`，不插入新条目 |
| **TTL** | `ttl_days` 字段；过期条目读取时过滤（`updated_at + ttl_days*86400 < now`），`PurgeExpired()` 回收 |
| **软删除** | `disabled=1` 标记，绝不物理删除（保留审计历史） |
| **强化** | `memory_search` 命中即自增 `use_count`/`last_used_at`，反哺 importance 权重与陈旧识别 |
| **陈旧识别** | `StaleCandidates()` → `importance<=1 AND use_count=0 AND 60 天未更新` |
| **矛盾冲突** | 由 LLM 通过 `memory_write update`/`remove`（意图驱动）解决，不做自动仲裁 |

---

## 8. Phase 3 —— 仅接口（不实现）

`provider.go` 定义以下接口（带文档注释，除 `noopProvider` 外无真实实现），为后续扩展留接缝：

- `Provider`：生命周期钩子 `Prefetch` / `Sync` / `OnPreCompress` / `OnSessionEnd`（参考 HermesAgent 提供者插件系统）
- `Embedder`：向量嵌入钩子（后续可接 Ollama / OpenAI Embeddings）
- `Consolidator`：Dreaming 巩固钩子（后续可接 cron 批量晋升）

这些接口能编译、以 no-op 形式被测试覆盖。

---

## 9. 主流程接线（main.go）

1. 从 `Manager.DB()` 创建 `ltm.Store`。
2. 注册 `memory_write` / `memory_search` 工具到 Registry。
3. 将 `Extractor` 注入 `SummarizationCompactor`（`WithMemoryExtractor` 选项）。
4. 将精华内容注入 `DefaultPromptBuilder`（`WithLongTermMemory`）。
5. 将 turn nudge 计数器接入 engine agent loop。

---

## 10. 测试策略

遵循 CLAUDE.md：stdlib `testing`、表驱动、无第三方断言库、内存 `:memory:` SQLite。

| 测试对象 | 覆盖点 |
|---------|-------|
| `ltm.Store` | Upsert/去重（相同签名刷新）/FTS5 搜索/TTL 过滤/陈旧识别/软删除/强化计数 |
| `ltm.precis` | top-N 渲染、importance 排序、字节上限截断、过期/禁用过滤 |
| `ltm.Extractor` | 用 mock `Summarizer` 测提取与 fail-open |
| `ltm.provider` | noopProvider 无操作行为 |
| `memory_write` 工具 | add/update/remove 三动作 + 参数校验 + 错误回传 |
| `memory_search` 工具 | 查询/空结果/强化副作用 |
| `DefaultPromptBuilder` | 精华注入位置与空内容跳过 |
| `SummarizationCompactor` | extractor 接入后压缩行为不变 + 提取被调用 |

---

## 11. 实现阶段划分

- **Phase 1**：`entry.go` + `store.go`（表 + FTS5 + Upsert/Search 基础）→ `memory_write`/`memory_search` 工具 → 主流程注册。
- **Phase 2**：`precis.go` 物化视图 → PromptBuilder 注入 → `extractor.go` 压缩前提取 → turn nudge → TTL/陈旧/软删除完善。
- **Phase 3**：`provider.go` 接口定义 + noop + 测试（仅接缝）。

---

## 12. 非目标（YAGNI）

- 向量嵌入与语义检索的真实实现（仅留 `Embedder` 接口）。
- Dreaming consolidation 后台 cron（仅留 `Consolidator` 接口）。
- 外部记忆提供者的真实实现（仅留 `Provider` 接口）。
- 多用户 / 团队范围隔离（harness9 当前为单用户本地工具）。
