# Long-Term Memory 系统实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 harness9 新增跨会话长期记忆能力——SQLite + FTS5 结构化存储、MEMORY.md 物化视图注入、显式工具 + 压缩前提取 + turn nudge 三路触发，零新增依赖。

**Architecture:** 新增自包含包 `internal/ltm/`，复用 `Manager` 持有的 `state.db` 连接。SQLite `long_term_memories` 表为唯一事实源；`memories_fts`（standalone FTS5）服务全文检索；MEMORY.md 由 top-N 条目自动渲染并注入 System Prompt。`memory_write`/`memory_search` 工具暴露给 LLM；`SummarizationCompactor` 压缩前调用 `Extractor` 提取持久事实；引擎按 turn 间隔注入 nudge 提示。

**Tech Stack:** Go 1.25.3、`modernc.org/sqlite`（已验证支持 FTS5）、标准库 `testing`、`crypto/sha256`、`encoding/json`。

**规格依据：** `docs/技术调研/2026-06-03-long-term-memory-design.md`

---

## 文件结构

| 文件 | 职责 | 任务 |
|------|------|------|
| `internal/ltm/entry.go` | `Entry` 结构体、`Category`、`Signature`/`normalize`、`Expired` | T1 |
| `internal/ltm/store.go` | `Store`：schema 迁移 + Add/Get/Search/Update/SoftDelete/List/PurgeExpired/StaleCandidates | T2-T5 |
| `internal/ltm/precis.go` | `Precis`：render + Regenerate + Read（MEMORY.md 物化视图） | T6 |
| `internal/ltm/provider.go` | Phase 3 接口：`Provider`/`Embedder`/`Consolidator` + `noopProvider` | T7 |
| `internal/ltm/extractor.go` | `Extractor`：LLM 压缩前事实提取（实现 `memory.MemoryExtractor`） | T9 |
| `internal/memory/summarization.go`（改） | `MemoryExtractor` 接口 + `WithMemoryExtractor` + Compact 集成 | T8 |
| `internal/memory/manager.go`（改） | `DB()` 访问器 | T10 |
| `internal/tools/memory_write.go` | `memory_write` 工具 | T11 |
| `internal/tools/memory_search.go` | `memory_search` 工具 | T12 |
| `internal/context/builder.go`（改） | `WithLongTermMemory` 注入 | T13 |
| `internal/engine/agent_loop.go`（改） | `WithMemoryNudge` 选项 + 循环注入 | T14 |
| `cmd/harness9/main.go`（改） | 主流程接线 | T15 |
| `docs/核心功能/long-term-memory.md` + `AGENTS.md`（改） | 文档同步 | T16 |

每个测试文件与被测文件同目录、同包，命名 `xxx_test.go`。

---

### Task 1: ltm.Entry + 签名

**Files:**
- Create: `internal/ltm/entry.go`
- Test: `internal/ltm/entry_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/ltm/entry_test.go
package ltm

import (
	"testing"
	"time"
)

func TestSignatureNormalizes(t *testing.T) {
	a := Signature("  Hello   World  ")
	b := Signature("hello world")
	if a != b {
		t.Fatalf("规范化后签名应相等: %s != %s", a, b)
	}
	if Signature("hello world") == Signature("goodbye world") {
		t.Fatal("不同内容签名不应相等")
	}
}

func TestEntryExpired(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		ttlDays int
		updated time.Time
		want    bool
	}{
		{"永不过期", 0, now.Add(-100 * 24 * time.Hour), false},
		{"未过期", 30, now.Add(-10 * 24 * time.Hour), false},
		{"已过期", 30, now.Add(-31 * 24 * time.Hour), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Entry{TTLDays: tt.ttlDays, UpdatedAt: tt.updated}
			if got := e.Expired(now); got != tt.want {
				t.Errorf("Expired()=%v want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/`
Expected: 编译失败（`undefined: Signature` / `Entry`）。

- [ ] **Step 3: 写最小实现**

```go
// internal/ltm/entry.go

// Package ltm 实现 harness9 的长期记忆（Long-Term Memory）能力：
// 跨会话持久化的知识/偏好/任务/技能条目。SQLite long_term_memories 表为唯一事实源，
// MEMORY.md 物化视图注入 System Prompt，FTS5 提供按需全文检索。
package ltm

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Category 是长期记忆条目的分类，影响精华渲染与检索语义。
type Category string

const (
	CategoryKnowledge  Category = "knowledge"  // 事实性知识
	CategoryPreference Category = "preference" // 用户偏好
	CategoryTask       Category = "task"       // 跨会话任务/承诺
	CategorySkill      Category = "skill"      // 操作性技能/方法
)

// Entry 是一条长期记忆。TTLDays 为 0 表示永不过期；Disabled 为软删除标志。
type Entry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Category   Category  `json:"category,omitempty"`
	Importance int       `json:"importance"` // 0-10，决定精华排序与陈旧识别
	Signature  string    `json:"-"`          // SHA256(normalize(content))，用于去重
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	UseCount   int       `json:"use_count"`
	TTLDays    int       `json:"ttl_days,omitempty"`
	Disabled   bool      `json:"-"`
	Tags       []string  `json:"tags,omitempty"`
}

// Signature 计算内容的去重指纹：SHA256(normalize(content))。
func Signature(content string) string {
	sum := sha256.Sum256([]byte(normalize(content)))
	return hex.EncodeToString(sum[:])
}

// normalize 折叠空白、小写化、去除首尾空白，得到稳定的去重基准串。
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// Expired 报告条目相对 now 是否已超过 TTL。TTLDays<=0 视为永不过期。
func (e Entry) Expired(now time.Time) bool {
	if e.TTLDays <= 0 {
		return false
	}
	return e.UpdatedAt.Add(time.Duration(e.TTLDays) * 24 * time.Hour).Before(now)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/entry.go internal/ltm/entry_test.go
git commit -m "feat(ltm): Entry 结构体与去重签名"
```

---

### Task 2: ltm.Store — schema 迁移 + Add（去重）+ Get

**Files:**
- Create: `internal/ltm/store.go`
- Test: `internal/ltm/store_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/ltm/store_test.go
package ltm

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStore 创建一个内存 SQLite Store，now 固定为可控时间。
func newTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.now = func() time.Time { return now }
	return s, &now
}

func TestStoreAddAndGet(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	got, err := s.Add(ctx, &Entry{Title: "Go 版本", Content: "项目使用 Go 1.25.3", Category: CategoryKnowledge, Importance: 7})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.ID == "" {
		t.Fatal("Add 应生成非空 ID")
	}
	if got.Signature == "" {
		t.Fatal("Add 应写入签名")
	}
	fetched, err := s.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Content != "项目使用 Go 1.25.3" || fetched.Importance != 7 {
		t.Errorf("Get 内容不符: %+v", fetched)
	}
}

func TestStoreAddDedup(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, _ := s.Add(ctx, &Entry{Title: "x", Content: "重复内容"})
	b, err := s.Add(ctx, &Entry{Title: "y", Content: "重复内容"})
	if err != nil {
		t.Fatalf("第二次 Add: %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("相同内容应去重为同一条目: %s != %s", a.ID, b.ID)
	}
	if b.UseCount != 1 {
		t.Errorf("去重命中应自增 use_count，got %d", b.UseCount)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run TestStore`
Expected: 编译失败（`undefined: NewStore`）。

- [ ] **Step 3: 写实现**

```go
// internal/ltm/store.go
package ltm

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const ltmSchema = `
CREATE TABLE IF NOT EXISTS long_term_memories (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,
    category     TEXT,
    importance   INTEGER NOT NULL DEFAULT 0,
    signature    TEXT UNIQUE,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    use_count    INTEGER NOT NULL DEFAULT 0,
    ttl_days     INTEGER,
    disabled     INTEGER NOT NULL DEFAULT 0,
    tags         TEXT
);
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(id UNINDEXED, title, content);
`

// Store 是长期记忆的 SQLite 数据访问层，复用 Manager 持有的 *sql.DB 连接。
// long_term_memories 表为唯一事实源；memories_fts 为手动同步的 standalone FTS5 索引。
type Store struct {
	db  *sql.DB
	now func() time.Time // 可注入，便于测试确定性
}

// NewStore 在给定连接上初始化 LTM schema（幂等），返回 Store。
func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(ltmSchema); err != nil {
		return nil, fmt.Errorf("初始化 ltm schema: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// 全部列，供 scanEntry 复用。
const entryColumns = `id, title, content, category, importance, signature,
	created_at, updated_at, last_used_at, use_count, ttl_days, disabled, tags`

type scanner interface{ Scan(dest ...any) error }

func scanEntry(sc scanner) (*Entry, error) {
	var e Entry
	var category, signature, tags sql.NullString
	var createdAt, updatedAt int64
	var lastUsed, ttlDays sql.NullInt64
	var disabled int
	if err := sc.Scan(&e.ID, &e.Title, &e.Content, &category, &e.Importance, &signature,
		&createdAt, &updatedAt, &lastUsed, &e.UseCount, &ttlDays, &disabled, &tags); err != nil {
		return nil, err
	}
	e.Category = Category(category.String)
	e.Signature = signature.String
	e.CreatedAt = time.Unix(createdAt, 0)
	e.UpdatedAt = time.Unix(updatedAt, 0)
	if lastUsed.Valid {
		e.LastUsedAt = time.Unix(lastUsed.Int64, 0)
	}
	if ttlDays.Valid {
		e.TTLDays = int(ttlDays.Int64)
	}
	e.Disabled = disabled != 0
	if tags.String != "" {
		_ = json.Unmarshal([]byte(tags.String), &e.Tags)
	}
	return &e, nil
}

// nullTTL 将 0 映射为 NULL（永不过期），否则返回天数。
func nullTTL(days int) any {
	if days <= 0 {
		return nil
	}
	return days
}

func marshalTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Add 写入一条新记忆。内容签名已存在（未删除）时视为去重命中：
// 刷新 updated_at + 自增 use_count，返回既有条目，不插入新行。
func (s *Store) Add(ctx context.Context, e *Entry) (*Entry, error) {
	sig := Signature(e.Content)
	now := s.now().Unix()

	var existingID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM long_term_memories WHERE signature = ? AND disabled = 0`, sig).Scan(&existingID)
	if err == nil {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE long_term_memories SET updated_at = ?, use_count = use_count + 1 WHERE id = ?`,
			now, existingID); err != nil {
			return nil, fmt.Errorf("去重刷新: %w", err)
		}
		return s.Get(ctx, existingID)
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("查询签名: %w", err)
	}

	id := newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO long_term_memories
			(id, title, content, category, importance, signature, created_at, updated_at, use_count, ttl_days, disabled, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?)`,
		id, e.Title, e.Content, string(e.Category), e.Importance, sig, now, now, nullTTL(e.TTLDays), marshalTags(e.Tags)); err != nil {
		return nil, fmt.Errorf("插入记忆: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (id, title, content) VALUES (?, ?, ?)`, id, e.Title, e.Content); err != nil {
		return nil, fmt.Errorf("插入 fts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务: %w", err)
	}
	return s.Get(ctx, id)
}

// Get 按 ID 返回条目（含已软删除的，便于审计）。不存在时返回错误。
func (s *Store) Get(ctx context.Context, id string) (*Entry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+entryColumns+` FROM long_term_memories WHERE id = ?`, id)
	e, err := scanEntry(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("记忆不存在: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询记忆: %w", err)
	}
	return e, nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/ -run TestStore`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/store.go internal/ltm/store_test.go
git commit -m "feat(ltm): Store schema 迁移 + Add 去重 + Get"
```

---

### Task 3: ltm.Store — Search（FTS5）+ 强化

**Files:**
- Modify: `internal/ltm/store.go`
- Test: `internal/ltm/store_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/ltm/store_test.go
func TestStoreSearchAndReinforce(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, &Entry{Title: "Go 版本", Content: "项目使用 Go 1.25.3 构建"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, &Entry{Title: "数据库", Content: "使用 SQLite 持久化会话"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(ctx, "SQLite", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "数据库" {
		t.Fatalf("期望命中「数据库」，got %+v", res)
	}
	// 强化：命中后 use_count 自增、last_used_at 写入。
	again, _ := s.Get(ctx, res[0].ID)
	if again.UseCount != 1 {
		t.Errorf("命中应自增 use_count，got %d", again.UseCount)
	}
	if again.LastUsedAt.IsZero() {
		t.Error("命中应写入 last_used_at")
	}
}

func TestStoreSearchEmptyQuery(t *testing.T) {
	s, _ := newTestStore(t)
	res, err := s.Search(context.Background(), "   ", 5)
	if err != nil {
		t.Fatalf("空查询不应报错: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("空查询应返回空结果，got %d", len(res))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run TestStoreSearch`
Expected: 编译失败（`undefined: Search`）。

- [ ] **Step 3: 写实现（追加到 store.go）**

```go
import "strings" // 加入 store.go 的 import 分组

// ftsQuery 把用户查询转换为安全的 FTS5 MATCH 表达式：
// 按空白分词，每个 token 作为双引号短语（内部双引号翻倍转义），以 OR 连接。
// 无有效 token 时返回空串。
func ftsQuery(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " OR ")
}

// Search 用 FTS5 检索未删除、未过期的记忆，按相关度返回至多 limit 条。
// 命中条目执行强化：自增 use_count、更新 last_used_at。
func (s *Store) Search(ctx context.Context, query string, limit int) ([]*Entry, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM memories_fts WHERE memories_fts MATCH ? ORDER BY rank LIMIT ?`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("fts 检索: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("扫描 fts 结果: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := s.now()
	var result []*Entry
	for _, id := range ids {
		e, err := s.Get(ctx, id)
		if err != nil {
			continue // 容忍并发删除
		}
		if e.Disabled || e.Expired(now) {
			continue
		}
		// 强化命中条目。
		if _, err := s.db.ExecContext(ctx,
			`UPDATE long_term_memories SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`,
			now.Unix(), id); err != nil {
			return nil, fmt.Errorf("强化命中: %w", err)
		}
		e.UseCount++
		e.LastUsedAt = now
		result = append(result, e)
	}
	return result, nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/ -run TestStoreSearch`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/store.go internal/ltm/store_test.go
git commit -m "feat(ltm): FTS5 全文检索 + 命中强化"
```

---

### Task 4: ltm.Store — Update + SoftDelete

**Files:**
- Modify: `internal/ltm/store.go`
- Test: `internal/ltm/store_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/ltm/store_test.go
func TestStoreUpdate(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	e, _ := s.Add(ctx, &Entry{Title: "旧标题", Content: "旧内容"})
	e.Title = "新标题"
	e.Content = "新内容"
	e.Importance = 9
	if err := s.Update(ctx, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(ctx, e.ID)
	if got.Title != "新标题" || got.Content != "新内容" || got.Importance != 9 {
		t.Errorf("更新未生效: %+v", got)
	}
	if got.Signature != Signature("新内容") {
		t.Error("更新内容应重算签名")
	}
	// FTS 应可检索到新内容、检索不到旧内容。
	if res, _ := s.Search(ctx, "新内容", 5); len(res) != 1 {
		t.Errorf("更新后 FTS 应命中新内容，got %d", len(res))
	}
	if res, _ := s.Search(ctx, "旧内容", 5); len(res) != 0 {
		t.Errorf("更新后 FTS 不应命中旧内容，got %d", len(res))
	}
}

func TestStoreSoftDelete(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	e, _ := s.Add(ctx, &Entry{Title: "t", Content: "待删除内容"})
	if err := s.SoftDelete(ctx, e.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, _ := s.Get(ctx, e.ID) // 仍可取到（审计）
	if !got.Disabled {
		t.Error("软删除后 disabled 应为 true")
	}
	if res, _ := s.Search(ctx, "待删除内容", 5); len(res) != 0 {
		t.Errorf("软删除后不应被检索到，got %d", len(res))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run "TestStoreUpdate|TestStoreSoftDelete"`
Expected: 编译失败（`undefined: Update` / `SoftDelete`）。

- [ ] **Step 3: 写实现（追加到 store.go）**

```go
// Update 按 e.ID 更新标题/内容/分类/重要度/TTL/标签，重算签名并刷新 updated_at，
// 同步重建该条目的 FTS 索引。条目不存在时返回错误。
func (s *Store) Update(ctx context.Context, e *Entry) error {
	sig := Signature(e.Content)
	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE long_term_memories
		 SET title = ?, content = ?, category = ?, importance = ?, signature = ?, ttl_days = ?, tags = ?, updated_at = ?
		 WHERE id = ?`,
		e.Title, e.Content, string(e.Category), e.Importance, sig, nullTTL(e.TTLDays), marshalTags(e.Tags), now, e.ID)
	if err != nil {
		return fmt.Errorf("更新记忆: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("记忆不存在: %s", e.ID)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories_fts WHERE id = ?`, e.ID); err != nil {
		return fmt.Errorf("清理 fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (id, title, content) VALUES (?, ?, ?)`, e.ID, e.Title, e.Content); err != nil {
		return fmt.Errorf("重建 fts: %w", err)
	}
	return tx.Commit()
}

// SoftDelete 将条目标记为 disabled（不物理删除，保留审计），并移出 FTS 索引。
func (s *Store) SoftDelete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE long_term_memories SET disabled = 1, updated_at = ? WHERE id = ?`, s.now().Unix(), id); err != nil {
		return fmt.Errorf("软删除: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("清理 fts: %w", err)
	}
	return tx.Commit()
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/ -run "TestStoreUpdate|TestStoreSoftDelete"`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/store.go internal/ltm/store_test.go
git commit -m "feat(ltm): Update 重建 FTS + SoftDelete 软删除"
```

---

### Task 5: ltm.Store — List + PurgeExpired + StaleCandidates

**Files:**
- Modify: `internal/ltm/store.go`
- Test: `internal/ltm/store_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/ltm/store_test.go
func TestStoreListOrderAndFilter(t *testing.T) {
	s, now := newTestStore(t)
	ctx := context.Background()
	s.Add(ctx, &Entry{Title: "低", Content: "低优先", Importance: 2})
	s.Add(ctx, &Entry{Title: "高", Content: "高优先", Importance: 9})
	// 一条已过期条目（updated_at 在过去，ttl=1 天）。
	s.now = func() time.Time { return now.Add(-48 * time.Hour) }
	s.Add(ctx, &Entry{Title: "过期", Content: "过期内容", Importance: 10, TTLDays: 1})
	s.now = func() time.Time { return *now }

	list, err := s.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("过期条目应被过滤，期望 2 条，got %d", len(list))
	}
	if list[0].Title != "高" {
		t.Errorf("应按 importance 降序，首条应为「高」，got %q", list[0].Title)
	}
}

func TestStorePurgeExpired(t *testing.T) {
	s, now := newTestStore(t)
	ctx := context.Background()
	s.now = func() time.Time { return now.Add(-48 * time.Hour) }
	e, _ := s.Add(ctx, &Entry{Title: "x", Content: "过期", TTLDays: 1})
	s.now = func() time.Time { return *now }

	n, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("应回收 1 条过期记忆，got %d", n)
	}
	got, _ := s.Get(ctx, e.ID)
	if !got.Disabled {
		t.Error("过期回收应置 disabled")
	}
}

func TestStoreStaleCandidates(t *testing.T) {
	s, now := newTestStore(t)
	ctx := context.Background()
	s.now = func() time.Time { return now.Add(-90 * 24 * time.Hour) }
	s.Add(ctx, &Entry{Title: "陈旧", Content: "低价值且久未使用", Importance: 1})
	s.now = func() time.Time { return *now }
	s.Add(ctx, &Entry{Title: "新鲜", Content: "近期高价值", Importance: 8})

	stale, err := s.StaleCandidates(ctx)
	if err != nil {
		t.Fatalf("StaleCandidates: %v", err)
	}
	if len(stale) != 1 || stale[0].Title != "陈旧" {
		t.Errorf("应识别出 1 条陈旧候选，got %+v", stale)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run "TestStoreList|TestStorePurge|TestStoreStale"`
Expected: 编译失败（`undefined: List`）。

- [ ] **Step 3: 写实现（追加到 store.go）**

```go
// queryEntries 是 List/StaleCandidates 共享的多行查询执行器。
func (s *Store) queryEntries(ctx context.Context, where string, args ...any) ([]*Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM long_term_memories WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("查询记忆列表: %w", err)
	}
	defer rows.Close()
	var result []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描记忆: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// List 返回未删除、未过期的记忆，按 importance 降序、updated_at 降序排列，至多 limit 条。
// 供 Precis 渲染精华视图使用。limit<=0 时不限制。
func (s *Store) List(ctx context.Context, limit int) ([]*Entry, error) {
	nowUnix := s.now().Unix()
	where := `disabled = 0 AND (ttl_days IS NULL OR updated_at + ttl_days * 86400 >= ?)
		ORDER BY importance DESC, updated_at DESC`
	if limit > 0 {
		where += fmt.Sprintf(" LIMIT %d", limit)
	}
	return s.queryEntries(ctx, where, nowUnix)
}

// PurgeExpired 将所有已过 TTL 的未删除记忆软删除，返回回收条数。
func (s *Store) PurgeExpired(ctx context.Context) (int, error) {
	nowUnix := s.now().Unix()
	res, err := s.db.ExecContext(ctx,
		`UPDATE long_term_memories SET disabled = 1
		 WHERE disabled = 0 AND ttl_days IS NOT NULL AND updated_at + ttl_days * 86400 < ?`, nowUnix)
	if err != nil {
		return 0, fmt.Errorf("回收过期记忆: %w", err)
	}
	// 同步清理 FTS（被回收条目移出索引）。
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE id IN (SELECT id FROM long_term_memories WHERE disabled = 1)`); err != nil {
		return 0, fmt.Errorf("清理 fts: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// StaleCandidates 识别清理候选：importance<=1 且 use_count=0 且 60 天未更新的未删除条目。
func (s *Store) StaleCandidates(ctx context.Context) ([]*Entry, error) {
	cutoff := s.now().Add(-60 * 24 * time.Hour).Unix()
	return s.queryEntries(ctx,
		`disabled = 0 AND importance <= 1 AND use_count = 0 AND updated_at < ?`, cutoff)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/`
Expected: PASS（全部 Store 测试）。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/store.go internal/ltm/store_test.go
git commit -m "feat(ltm): List 精华查询 + PurgeExpired + StaleCandidates"
```

---

### Task 6: ltm.Precis — MEMORY.md 物化视图

**Files:**
- Create: `internal/ltm/precis.go`
- Test: `internal/ltm/precis_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/ltm/precis_test.go
package ltm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecisRegenerateAndRead(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Add(ctx, &Entry{Title: "高优先", Content: "重要事实", Importance: 9, Category: CategoryKnowledge})
	s.Add(ctx, &Entry{Title: "低优先", Content: "次要事实", Importance: 1})

	path := filepath.Join(t.TempDir(), "memories", "MEMORY.md")
	p := NewPrecis(s, path, 4096)
	if err := p.Regenerate(ctx); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	content, err := p.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(content, "高优先") || !strings.Contains(content, "重要事实") {
		t.Errorf("精华应包含高优先条目: %q", content)
	}
	// 文件确实落盘。
	if _, err := os.Stat(path); err != nil {
		t.Errorf("MEMORY.md 应已写入磁盘: %v", err)
	}
}

func TestPrecisByteCap(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		s.Add(ctx, &Entry{Title: "条目", Content: strings.Repeat("内容", 50), Importance: 5})
	}
	p := NewPrecis(s, filepath.Join(t.TempDir(), "MEMORY.md"), 500)
	if err := p.Regenerate(ctx); err != nil {
		t.Fatal(err)
	}
	content, _ := p.Read()
	if len(content) > 600 { // 500 上限 + 截断标记裕量
		t.Errorf("精华应被截断到约 500 字节，got %d", len(content))
	}
}

func TestPrecisReadMissing(t *testing.T) {
	p := NewPrecis(nil, filepath.Join(t.TempDir(), "nope.md"), 4096)
	content, err := p.Read()
	if err != nil {
		t.Errorf("缺失文件应返回空串而非错误: %v", err)
	}
	if content != "" {
		t.Errorf("缺失文件应返回空串，got %q", content)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run TestPrecis`
Expected: 编译失败（`undefined: NewPrecis`）。

- [ ] **Step 3: 写实现**

```go
// internal/ltm/precis.go
package ltm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// precisMaxEntries 是渲染进精华视图的最大条目数（按 importance 取 top-N）。
const precisMaxEntries = 30

// Precis 维护 MEMORY.md 物化视图：从 Store 的 top-N 高价值条目渲染出有界的
// markdown 文件，供 System Prompt 全量注入。每次写入记忆后调用 Regenerate 重建。
type Precis struct {
	store    *Store
	path     string // MEMORY.md 绝对路径
	maxBytes int    // 注入预算上限（字节）
}

// NewPrecis 创建绑定到指定 Store 与文件路径的 Precis。maxBytes<=0 时默认 5120。
func NewPrecis(store *Store, path string, maxBytes int) *Precis {
	if maxBytes <= 0 {
		maxBytes = 5120
	}
	return &Precis{store: store, path: path, maxBytes: maxBytes}
}

// Regenerate 从 Store 拉取 top-N 条目，渲染为 markdown 并写入 MEMORY.md（含父目录创建）。
func (p *Precis) Regenerate(ctx context.Context) error {
	entries, err := p.store.List(ctx, precisMaxEntries)
	if err != nil {
		return fmt.Errorf("拉取精华条目: %w", err)
	}
	content := renderPrecis(entries, p.maxBytes)
	if err := os.MkdirAll(filepath.Dir(p.path), 0700); err != nil {
		return fmt.Errorf("创建精华目录: %w", err)
	}
	if err := os.WriteFile(p.path, []byte(content), 0600); err != nil {
		return fmt.Errorf("写入精华文件: %w", err)
	}
	return nil
}

// Read 读取 MEMORY.md 内容；文件不存在时返回空串（不报错）。
func (p *Precis) Read() (string, error) {
	data, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取精华文件: %w", err)
	}
	return string(data), nil
}

// renderPrecis 将条目渲染为 markdown，并在 maxBytes 处按 UTF-8 边界截断。
func renderPrecis(entries []*Entry, maxBytes int) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString("## ")
		b.WriteString(e.Title)
		if e.Category != "" {
			b.WriteString(fmt.Sprintf(" `%s`", e.Category))
		}
		b.WriteString("\n")
		b.WriteString(e.Content)
		b.WriteString("\n\n")
	}
	return truncateUTF8(strings.TrimRight(b.String(), "\n"), maxBytes)
}

// truncateUTF8 将 s 截断到不超过 maxBytes 字节，保证不切断多字节 rune，
// 截断时追加省略标记。
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	const marker = "\n…（已截断）"
	budget := maxBytes - len(marker)
	if budget < 0 {
		budget = 0
	}
	cut := budget
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

// utf8RuneStart 报告字节 b 是否是一个 UTF-8 rune 的起始字节。
func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/ -run TestPrecis`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/precis.go internal/ltm/precis_test.go
git commit -m "feat(ltm): Precis MEMORY.md 物化视图（top-N 渲染 + 字节截断）"
```

---

### Task 7: ltm.provider.go — Phase 3 接口（仅接缝）

**Files:**
- Create: `internal/ltm/provider.go`
- Test: `internal/ltm/provider_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/ltm/provider_test.go
package ltm

import (
	"context"
	"testing"

	"github.com/harness9/internal/schema"
)

func TestNoopProviderSatisfiesInterface(t *testing.T) {
	var p Provider = NewNoopProvider()
	ctx := context.Background()
	if got, err := p.Prefetch(ctx, "q"); err != nil || got != nil {
		t.Errorf("noop Prefetch 应返回 (nil,nil)，got (%v,%v)", got, err)
	}
	if err := p.Sync(ctx, "u", "a"); err != nil {
		t.Errorf("noop Sync 应返回 nil，got %v", err)
	}
	if err := p.OnPreCompress(ctx, []schema.Message{}); err != nil {
		t.Errorf("noop OnPreCompress 应返回 nil，got %v", err)
	}
	if err := p.OnSessionEnd(ctx); err != nil {
		t.Errorf("noop OnSessionEnd 应返回 nil，got %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run TestNoopProvider`
Expected: 编译失败（`undefined: Provider`）。

- [ ] **Step 3: 写实现**

```go
// internal/ltm/provider.go
package ltm

import (
	"context"

	"github.com/harness9/internal/schema"
)

// Provider 是外部记忆提供者的扩展接口（Phase 3 接缝，当前仅 noopProvider 实现）。
// 参考 HermesAgent 的提供者插件系统：每个生命周期阶段允许外部存储介入。
//
// 后续可实现接入 Mem0 / Honcho / 向量库等外部记忆后端。
type Provider interface {
	// Prefetch 在每个 turn 前按 query 预取相关记忆（语义检索）。
	Prefetch(ctx context.Context, query string) ([]*Entry, error)
	// Sync 在每个 turn 结束后同步对话数据给提供者。
	Sync(ctx context.Context, userContent, assistantContent string) error
	// OnPreCompress 在上下文压缩前从待压缩消息中提取记忆。
	OnPreCompress(ctx context.Context, msgs []schema.Message) error
	// OnSessionEnd 在会话结束时执行收尾固化。
	OnSessionEnd(ctx context.Context) error
}

// Embedder 是向量嵌入扩展接口（Phase 3 接缝，当前无实现）。
// 后续可接入 Ollama 本地嵌入或 OpenAI Embeddings，为 Store 增加语义检索。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Consolidator 是 Dreaming 巩固扩展接口（Phase 3 接缝，当前无实现）。
// 后续可由 cron 触发，批量筛选短期信号晋升为长期记忆。
type Consolidator interface {
	Consolidate(ctx context.Context) (promoted int, err error)
}

// noopProvider 是 Provider 的空实现，所有钩子均为无操作。
// 作为默认提供者占位，使主流程在未配置外部提供者时仍可正常运行。
type noopProvider struct{}

// NewNoopProvider 返回一个无操作的 Provider。
func NewNoopProvider() Provider { return noopProvider{} }

func (noopProvider) Prefetch(context.Context, string) ([]*Entry, error)   { return nil, nil }
func (noopProvider) Sync(context.Context, string, string) error           { return nil }
func (noopProvider) OnPreCompress(context.Context, []schema.Message) error { return nil }
func (noopProvider) OnSessionEnd(context.Context) error                    { return nil }
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/ -run TestNoopProvider`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/provider.go internal/ltm/provider_test.go
git commit -m "feat(ltm): Phase 3 扩展接口（Provider/Embedder/Consolidator）+ noop"
```

---

### Task 8: memory.MemoryExtractor 接口 + 压缩前提取集成

**Files:**
- Modify: `internal/memory/summarization.go`
- Test: `internal/memory/summarization_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/memory/summarization_test.go
// recordingExtractor 记录 Extract 是否被调用及收到的消息数。
type recordingExtractor struct {
	called bool
	count  int
}

func (r *recordingExtractor) Extract(msgs []schema.Message) {
	r.called = true
	r.count = len(msgs)
}

func TestCompactInvokesExtractorBeforeSummarize(t *testing.T) {
	// 构造一个超出预算、需要压缩的历史。
	msgs := []schema.Message{{Role: schema.RoleSystem, Content: "sys"}}
	for i := 0; i < 20; i++ {
		msgs = append(msgs, schema.Message{Role: schema.RoleUser, Content: strings.Repeat("x", 2000)})
	}
	rec := &recordingExtractor{}
	c := NewSummarizationCompactor(
		newStubSummarizer("摘要内容"), // 复用本包测试已有的桩；见下方说明
		1000,
		WithMemoryExtractor(rec),
	)
	c.Compact(msgs)
	if !rec.called {
		t.Fatal("压缩时应调用 extractor.Extract")
	}
	if rec.count == 0 {
		t.Error("extractor 应收到 head 消息")
	}
}
```

> **桩说明：** `newStubSummarizer` 应返回一个满足 `Summarizer` 接口、`Generate` 固定返回给定文本的桩。若本包测试已有等价桩（检查 `summarization_test.go` 顶部），直接复用；否则在测试文件中补充：
> ```go
> type stubSummarizer struct{ text string }
> func (s stubSummarizer) Generate(ctx context.Context, _ []schema.Message, _ []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
> 	return &schema.Message{Role: schema.RoleAssistant, Content: s.text}, nil, nil
> }
> func newStubSummarizer(text string) stubSummarizer { return stubSummarizer{text: text} }
> ```
> 确保 `summarization_test.go` 顶部 import 含 `"context"`、`"strings"`、`"github.com/harness9/internal/schema"`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/ -run TestCompactInvokesExtractor`
Expected: 编译失败（`undefined: WithMemoryExtractor`）。

- [ ] **Step 3: 写实现（修改 summarization.go）**

在 `SummarizationCompactor` 结构体（`internal/memory/summarization.go:61`）末尾字段后追加：

```go
	// extractor 若非 nil，在压缩摘要前从 head 消息中提取长期记忆（fail-open）。
	extractor MemoryExtractor
```

在 `TodoInjector` 接口定义（约 `summarization.go:23`）附近新增接口：

```go
// MemoryExtractor 由 ltm.Extractor 实现，在上下文压缩前从即将被摘要的消息中
// 提取持久事实写入长期记忆。定义在 memory 包（使用者侧），避免 memory 依赖 ltm。
type MemoryExtractor interface {
	Extract(msgs []schema.Message)
}
```

在 `WithTodoInjector`（`summarization.go:75`）附近新增选项：

```go
// WithMemoryExtractor 注入长期记忆提取器，在每次压缩摘要前从 head 消息提取持久事实。
func WithMemoryExtractor(ex MemoryExtractor) CompactorOption {
	return func(c *SummarizationCompactor) { c.extractor = ex }
}
```

在 `Compact` 方法中，将 `summarize(head)` 调用（`summarization.go:114`）前一行插入提取调用：

```go
	// 压缩前提取：在 head 被摘要抹除前，先提取持久事实到长期记忆（fail-open）。
	if c.extractor != nil {
		c.extractor.Extract(head)
	}

	summary, err := c.summarize(head)
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/memory/`
Expected: PASS（含新测试与既有测试）。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/memory/
git add internal/memory/summarization.go internal/memory/summarization_test.go
git commit -m "feat(memory): MemoryExtractor 接口 + 压缩前提取钩子"
```

---

### Task 9: ltm.Extractor — LLM 压缩前事实提取

**Files:**
- Create: `internal/ltm/extractor.go`
- Test: `internal/ltm/extractor_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/ltm/extractor_test.go
package ltm

import (
	"context"
	"errors"
	"testing"

	"github.com/harness9/internal/schema"
)

// fakeGen 是 Generator 桩：固定返回 text，或返回 err。
type fakeGen struct {
	text string
	err  error
}

func (f fakeGen) Generate(_ context.Context, _ []schema.Message, _ []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return &schema.Message{Role: schema.RoleAssistant, Content: f.text}, nil, nil
}

func TestExtractorUpsertsFacts(t *testing.T) {
	s, _ := newTestStore(t)
	gen := fakeGen{text: "```json\n[{\"title\":\"偏好\",\"content\":\"用户偏好中文回复\",\"category\":\"preference\",\"importance\":8}]\n```"}
	ex := NewExtractor(gen, s)
	ex.Extract([]schema.Message{{Role: schema.RoleUser, Content: "请用中文"}})

	list, _ := s.List(context.Background(), 10)
	if len(list) != 1 || list[0].Content != "用户偏好中文回复" {
		t.Fatalf("提取应写入 1 条记忆，got %+v", list)
	}
	if list[0].Category != CategoryPreference || list[0].Importance != 8 {
		t.Errorf("分类/重要度解析错误: %+v", list[0])
	}
}

func TestExtractorFailOpen(t *testing.T) {
	s, _ := newTestStore(t)
	ex := NewExtractor(fakeGen{err: errors.New("network")}, s)
	// 不应 panic；提取失败静默吞掉。
	ex.Extract([]schema.Message{{Role: schema.RoleUser, Content: "x"}})
	list, _ := s.List(context.Background(), 10)
	if len(list) != 0 {
		t.Errorf("提取失败不应写入记忆，got %d", len(list))
	}
}

func TestExtractorIgnoresEmptyArray(t *testing.T) {
	s, _ := newTestStore(t)
	ex := NewExtractor(fakeGen{text: "[]"}, s)
	ex.Extract([]schema.Message{{Role: schema.RoleUser, Content: "闲聊"}})
	list, _ := s.List(context.Background(), 10)
	if len(list) != 0 {
		t.Errorf("空数组不应写入记忆，got %d", len(list))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/ltm/ -run TestExtractor`
Expected: 编译失败（`undefined: NewExtractor`）。

- [ ] **Step 3: 写实现**

```go
// internal/ltm/extractor.go
package ltm

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/harness9/internal/logfmt"
	"github.com/harness9/internal/schema"
)

// Generator 抽象提取所需的 LLM 调用能力（与 memory.Summarizer 同形）。
type Generator interface {
	Generate(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (*schema.Message, *schema.Usage, error)
}

const extractTimeout = 60 * time.Second

const extractSystemPrompt = `你是长期记忆提取器。从对话中提取值得跨会话长期保留的事实` +
	`（用户偏好、稳定的项目知识、关键决策、可复用技能），忽略一次性的临时上下文。` +
	`仅输出 JSON 数组，每个元素形如 {"title","content","category","importance"}，` +
	`category ∈ {knowledge,preference,task,skill}，importance 为 0-10 整数。` +
	`没有值得保留的内容时输出 []。不要输出任何解释。`

// Extractor 在上下文压缩前用 LLM 从 head 消息提取持久事实并写入 Store。
// 实现 memory.MemoryExtractor 接口（Extract 方法）。所有错误 fail-open。
type Extractor struct {
	gen   Generator
	store *Store
}

// NewExtractor 创建绑定到指定 Generator 与 Store 的提取器。
func NewExtractor(gen Generator, store *Store) *Extractor {
	return &Extractor{gen: gen, store: store}
}

// extractedFact 是 LLM 返回的单条事实的解析结构。
type extractedFact struct {
	Title      string `json:"title"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Importance int    `json:"importance"`
}

// Extract 从 msgs 提取持久事实并 upsert 到 Store。任何环节出错仅记日志，不阻断调用方。
func (e *Extractor) Extract(msgs []schema.Message) {
	if e.gen == nil || e.store == nil || len(msgs) == 0 {
		return
	}
	convo := renderConversation(msgs)
	if strings.TrimSpace(convo) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
	defer cancel()

	resp, _, err := e.gen.Generate(ctx, []schema.Message{
		{Role: schema.RoleSystem, Content: extractSystemPrompt},
		{Role: schema.RoleUser, Content: "对话：\n" + convo},
	}, nil)
	if err != nil || resp == nil {
		log.Print(logfmt.FormatMsg("ltm", "压缩前提取失败（fail-open）"))
		return
	}

	facts, err := parseFacts(resp.Content)
	if err != nil {
		log.Print(logfmt.FormatMsg("ltm", "提取结果解析失败（fail-open）"))
		return
	}
	for _, f := range facts {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		if _, err := e.store.Add(ctx, &Entry{
			Title:      f.Title,
			Content:    f.Content,
			Category:   Category(f.Category),
			Importance: f.Importance,
		}); err != nil {
			log.Print(logfmt.FormatMsg("ltm", "提取条目写入失败（fail-open）"))
		}
	}
}

// renderConversation 将消息扁平化为文本，供提取 prompt 使用。
func renderConversation(msgs []schema.Message) string {
	var lines []string
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		lines = append(lines, string(m.Role)+": "+m.Content)
	}
	return strings.Join(lines, "\n")
}

// parseFacts 解析 LLM 输出的 JSON 数组，容忍 ```json ``` 代码围栏包裹。
func parseFacts(out string) ([]extractedFact, error) {
	s := strings.TrimSpace(out)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var facts []extractedFact
	if err := json.Unmarshal([]byte(s), &facts); err != nil {
		return nil, err
	}
	return facts, nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/ltm/ -run TestExtractor`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/ltm/
git add internal/ltm/extractor.go internal/ltm/extractor_test.go
git commit -m "feat(ltm): Extractor LLM 压缩前事实提取（fail-open）"
```

---

### Task 10: Manager.DB() 访问器

**Files:**
- Modify: `internal/memory/manager.go`
- Test: `internal/memory/manager_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/memory/manager_test.go
func TestManagerDBAccessor(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if mgr.DB() == nil {
		t.Fatal("DB() 应返回非 nil 连接")
	}
	if err := mgr.DB().Ping(); err != nil {
		t.Errorf("DB() 连接应可用: %v", err)
	}
}
```

> 确保 `manager_test.go` import 含 `"path/filepath"`（若已存在则跳过）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/ -run TestManagerDBAccessor`
Expected: 编译失败（`mgr.DB undefined`）。

- [ ] **Step 3: 写实现（追加到 manager.go，`Close` 方法后）**

```go
// DB 返回底层 SQLite 连接，供长期记忆（ltm.Store）等共享同一连接复用。
// 调用方不得关闭该连接——其生命周期由 Manager.Close 统一管理。
func (m *Manager) DB() *sql.DB {
	return m.db
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/memory/ -run TestManagerDBAccessor`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/memory/
git add internal/memory/manager.go internal/memory/manager_test.go
git commit -m "feat(memory): Manager.DB() 访问器供 ltm 共享连接"
```

---

### Task 11: memory_write 工具

**Files:**
- Create: `internal/tools/memory_write.go`
- Test: `internal/tools/memory_write_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/tools/memory_write_test.go
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/harness9/internal/ltm"
	_ "modernc.org/sqlite"
)

func newWriteTool(t *testing.T) (*MemoryWriteTool, *ltm.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := ltm.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	precis := ltm.NewPrecis(store, t.TempDir()+"/MEMORY.md", 4096)
	return NewMemoryWriteTool(store, precis), store
}

func TestMemoryWriteAdd(t *testing.T) {
	tool, store := newWriteTool(t)
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"add","title":"偏好","content":"用户偏好简洁回复","category":"preference","importance":7}`))
	if err != nil {
		t.Fatalf("Execute add: %v", err)
	}
	if !strings.Contains(out, "偏好") {
		t.Errorf("返回应含写入条目: %s", out)
	}
	list, _ := store.List(context.Background(), 10)
	if len(list) != 1 {
		t.Fatalf("应写入 1 条，got %d", len(list))
	}
}

func TestMemoryWriteRemove(t *testing.T) {
	tool, store := newWriteTool(t)
	addOut, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"add","title":"t","content":"待删"}`))
	var added ltm.Entry
	json.Unmarshal([]byte(addOut), &added)

	if _, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"remove","id":"`+added.ID+`"}`)); err != nil {
		t.Fatalf("Execute remove: %v", err)
	}
	list, _ := store.List(context.Background(), 10)
	if len(list) != 0 {
		t.Errorf("软删除后 List 应为空，got %d", len(list))
	}
}

func TestMemoryWriteBadAction(t *testing.T) {
	tool, _ := newWriteTool(t)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"bogus"}`)); err == nil {
		t.Error("未知 action 应返回错误")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/tools/ -run TestMemoryWrite`
Expected: 编译失败（`undefined: NewMemoryWriteTool`）。

- [ ] **Step 3: 写实现**

```go
// internal/tools/memory_write.go

// Package tools — memory_write 工具（长期记忆写入）。
//
// 三种动作：
//   - add：新增一条记忆（内容签名去重）
//   - update：按 id 更新既有记忆
//   - remove：按 id 软删除记忆
// 每次成功写入后重建 MEMORY.md 物化视图（若注入了 Precis）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/harness9/internal/logfmt"
	"github.com/harness9/internal/ltm"
	"github.com/harness9/internal/schema"
)

// MemoryWriteTool 实现 BaseTool，向长期记忆 Store 写入条目。
type MemoryWriteTool struct {
	store  *ltm.Store
	precis *ltm.Precis // 可选，nil 时跳过精华重建
}

// NewMemoryWriteTool 创建写入工具。precis 可为 nil。
func NewMemoryWriteTool(store *ltm.Store, precis *ltm.Precis) *MemoryWriteTool {
	return &MemoryWriteTool{store: store, precis: precis}
}

// Name 返回工具标识符 "memory_write"。
func (t *MemoryWriteTool) Name() string { return "memory_write" }

// Definition 返回工具元信息。
func (t *MemoryWriteTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: "memory_write",
		Description: "写入跨会话长期记忆。action=add 新增（相同内容自动去重）；" +
			"action=update 按 id 更新；action=remove 按 id 删除。" +
			"用于记住用户偏好、稳定的项目知识、关键决策或可复用技能。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":  map[string]interface{}{"type": "string", "enum": []string{"add", "update", "remove"}},
				"id":      map[string]interface{}{"type": "string", "description": "update/remove 时必填"},
				"title":   map[string]interface{}{"type": "string"},
				"content": map[string]interface{}{"type": "string"},
				"category": map[string]interface{}{
					"type": "string",
					"enum": []string{"knowledge", "preference", "task", "skill"},
				},
				"importance": map[string]interface{}{"type": "integer", "description": "0-10"},
				"ttl_days":   map[string]interface{}{"type": "integer", "description": "可选，过期天数；省略=永不过期"},
				"tags":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			},
			"required": []string{"action"},
		},
	}
}

type memoryWriteArgs struct {
	Action     string   `json:"action"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Category   string   `json:"category"`
	Importance int      `json:"importance"`
	TTLDays    int      `json:"ttl_days"`
	Tags       []string `json:"tags"`
}

// Execute 处理 memory_write 调用，返回写入后条目的 JSON（remove 返回状态消息）。
func (t *MemoryWriteTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in memoryWriteArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	var result string
	switch in.Action {
	case "add":
		if in.Content == "" {
			return "", fmt.Errorf("add 需要非空 content")
		}
		e, err := t.store.Add(ctx, &ltm.Entry{
			Title: in.Title, Content: in.Content, Category: ltm.Category(in.Category),
			Importance: in.Importance, TTLDays: in.TTLDays, Tags: in.Tags,
		})
		if err != nil {
			return "", fmt.Errorf("写入记忆失败: %w", err)
		}
		result = mustJSON(e)
	case "update":
		if in.ID == "" {
			return "", fmt.Errorf("update 需要 id")
		}
		if err := t.store.Update(ctx, &ltm.Entry{
			ID: in.ID, Title: in.Title, Content: in.Content, Category: ltm.Category(in.Category),
			Importance: in.Importance, TTLDays: in.TTLDays, Tags: in.Tags,
		}); err != nil {
			return "", fmt.Errorf("更新记忆失败: %w", err)
		}
		e, _ := t.store.Get(ctx, in.ID)
		result = mustJSON(e)
	case "remove":
		if in.ID == "" {
			return "", fmt.Errorf("remove 需要 id")
		}
		if err := t.store.SoftDelete(ctx, in.ID); err != nil {
			return "", fmt.Errorf("删除记忆失败: %w", err)
		}
		result = fmt.Sprintf(`{"status":"removed","id":%q}`, in.ID)
	default:
		return "", fmt.Errorf("未知 action: %q（应为 add/update/remove）", in.Action)
	}

	// 重建 MEMORY.md 物化视图（fail-soft：失败仅记日志）。
	if t.precis != nil {
		if err := t.precis.Regenerate(ctx); err != nil {
			log.Print(logfmt.FormatMsg("memory_write", fmt.Sprintf("重建精华失败: %v", err)))
		}
	}
	return result, nil
}

// mustJSON 将值序列化为 JSON 字符串，失败时返回空对象。
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
```

> **注意：** 若 `internal/tools/` 中已存在 `mustJSON` 同名函数会冲突，先 `grep -rn "func mustJSON" internal/tools/`；如有则删除本文件中的 `mustJSON` 定义复用既有的。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/tools/ -run TestMemoryWrite`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/tools/
git add internal/tools/memory_write.go internal/tools/memory_write_test.go
git commit -m "feat(tools): memory_write 工具（add/update/remove + 精华重建）"
```

---

### Task 12: memory_search 工具

**Files:**
- Create: `internal/tools/memory_search.go`
- Test: `internal/tools/memory_search_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/tools/memory_search_test.go
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/harness9/internal/ltm"
	_ "modernc.org/sqlite"
)

func TestMemorySearchExecute(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	t.Cleanup(func() { db.Close() })
	store, err := ltm.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	store.Add(context.Background(), &ltm.Entry{Title: "数据库", Content: "项目使用 SQLite 持久化"})

	tool := NewMemorySearchTool(store)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"SQLite"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "SQLite") {
		t.Errorf("应检索到含 SQLite 的记忆: %s", out)
	}
}

func TestMemorySearchNoHit(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	t.Cleanup(func() { db.Close() })
	store, _ := ltm.NewStore(db)
	tool := NewMemorySearchTool(store)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"不存在"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "[]" {
		t.Errorf("无命中应返回 \"[]\"，got %q", out)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/tools/ -run TestMemorySearch`
Expected: 编译失败（`undefined: NewMemorySearchTool`）。

- [ ] **Step 3: 写实现**

```go
// internal/tools/memory_search.go

// Package tools — memory_search 工具（长期记忆全文检索）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harness9/internal/ltm"
	"github.com/harness9/internal/schema"
)

// MemorySearchTool 实现 BaseTool，用 FTS5 检索长期记忆。
type MemorySearchTool struct {
	store *ltm.Store
}

// NewMemorySearchTool 创建检索工具。
func NewMemorySearchTool(store *ltm.Store) *MemorySearchTool {
	return &MemorySearchTool{store: store}
}

// Name 返回工具标识符 "memory_search"。
func (t *MemorySearchTool) Name() string { return "memory_search" }

// Definition 返回工具元信息。
func (t *MemorySearchTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: "memory_search",
		Description: "在跨会话长期记忆中按关键词全文检索。" +
			"当前任务可能涉及用户既有偏好、过去的决策或项目背景时调用，召回相关记忆。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "检索关键词"},
				"limit": map[string]interface{}{"type": "integer", "description": "返回上限，默认 5"},
			},
			"required": []string{"query"},
		},
	}
}

type memorySearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// Execute 处理 memory_search 调用，返回命中条目的 JSON 数组（无命中返回 "[]"）。
func (t *MemorySearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in memorySearchArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	entries, err := t.store.Search(ctx, in.Query, in.Limit)
	if err != nil {
		return "", fmt.Errorf("检索记忆失败: %w", err)
	}
	if entries == nil {
		entries = []*ltm.Entry{}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(b), nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/tools/ -run TestMemorySearch`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/tools/
git add internal/tools/memory_search.go internal/tools/memory_search_test.go
git commit -m "feat(tools): memory_search 工具（FTS5 全文检索）"
```

---

### Task 13: PromptBuilder 长期记忆注入

**Files:**
- Modify: `internal/context/builder.go`
- Test: `internal/context/builder_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/context/builder_test.go
func TestBuildInjectsLongTermMemory(t *testing.T) {
	b := NewPromptBuilder(t.TempDir(), nil).WithLongTermMemory("## 偏好\n用户偏好中文")
	out := b.Build()
	if !strings.Contains(out, "用户偏好中文") {
		t.Errorf("system prompt 应注入长期记忆内容: %s", out)
	}
}

func TestBuildSkipsEmptyLongTermMemory(t *testing.T) {
	b := NewPromptBuilder(t.TempDir(), nil).WithLongTermMemory("")
	out := b.Build()
	if strings.Contains(out, "长期记忆") {
		t.Error("空长期记忆不应注入标题段落")
	}
}
```

> 确保 `builder_test.go` import 含 `"strings"`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/ -run TestBuildInjects`
Expected: 编译失败（`WithLongTermMemory undefined`）。

- [ ] **Step 3: 写实现（修改 builder.go）**

在 `DefaultPromptBuilder` 结构体（`builder.go:20`）追加字段：

```go
	longTermMemory string
```

在 `WithOffloadEnabled`（`builder.go:42`）后新增方法：

```go
// WithLongTermMemory 注入长期记忆精华（MEMORY.md 物化视图内容）到 System Prompt。
// content 为空时跳过整段。仅在 LTM 启用时调用。
func (b *DefaultPromptBuilder) WithLongTermMemory(content string) *DefaultPromptBuilder {
	b.longTermMemory = content
	return b
}
```

在 `Build()` 方法的 Offload 段落（`builder.go:97-106`）之后、`return` 之前插入：

```go
	// 6. 长期记忆精华（仅在非空时注入）
	if b.longTermMemory != "" {
		parts = append(parts,
			"## 长期记忆\n\n"+
				"以下是跨会话积累的长期记忆精华。需要更多历史细节时，使用 `memory_search` 工具检索；"+
				"发现值得长期保留的新信息时，使用 `memory_write` 工具记录。\n\n"+
				b.longTermMemory,
		)
	}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/context/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/context/
git add internal/context/builder.go internal/context/builder_test.go
git commit -m "feat(context): PromptBuilder 注入长期记忆精华"
```

---

### Task 14: 引擎 turn nudge

**Files:**
- Modify: `internal/engine/agent_loop.go`
- Test: `internal/engine/agent_loop_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
// 追加到 internal/engine/agent_loop_test.go
func TestMemoryNudgeInjectedEveryNTurns(t *testing.T) {
	var captured [][]schema.Message
	mock := providertest.NewMockWithCallback(func(msgs []schema.Message, _ []schema.ToolDefinition) schema.Message {
		// 拷贝快照，返回无工具调用的终止响应。
		snap := make([]schema.Message, len(msgs))
		copy(snap, msgs)
		captured = append(captured, snap)
		return schema.Message{Role: schema.RoleAssistant, Content: "完成"}
	})
	reg := tools.NewRegistry()
	eng := NewAgentEngine(mock, reg, t.TempDir(),
		WithMemoryNudge(1, "【记忆提示】如有值得长期保留的信息，请调用 memory_write。"),
	)
	if err := eng.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("provider 未被调用")
	}
	found := false
	for _, m := range captured[0] {
		if strings.Contains(m.Content, "【记忆提示】") {
			found = true
		}
	}
	if !found {
		t.Error("turn 1 的历史应注入 nudge 提示")
	}
}

func TestMemoryNudgeNotPersisted(t *testing.T) {
	// nudge 注入到每轮的临时副本，不应污染 contextHistory（此处验证不 panic、正常结束）。
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "done"}
	})
	eng := NewAgentEngine(mock, tools.NewRegistry(), t.TempDir(), WithMemoryNudge(1, "提示"))
	if err := eng.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
```

> 确保 `agent_loop_test.go` import 含 `"strings"`、`"github.com/harness9/internal/provider/providertest"`、`"github.com/harness9/internal/tools"`、`"github.com/harness9/internal/schema"`（多数已存在，按需补充）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/engine/ -run TestMemoryNudge`
Expected: 编译失败（`undefined: WithMemoryNudge`）。

- [ ] **Step 3: 写实现（修改 agent_loop.go）**

在 `AgentEngine` 结构体（`agent_loop.go:102-117`）追加字段：

```go
	nudgeInterval int    // >0 时每隔该轮数注入一次记忆 nudge
	nudgeText     string // nudge 提示文本
```

在 `WithContextWindow`（`agent_loop.go:43`）附近新增选项：

```go
// WithMemoryNudge 配置长期记忆 nudge：每隔 interval 个 turn 在发送给 LLM 的历史中
// 注入一行 text 提示（仅注入到临时副本，不持久化）。interval<=0 时关闭。
func WithMemoryNudge(interval int, text string) Option {
	return func(e *AgentEngine) {
		e.nudgeInterval = interval
		e.nudgeText = text
	}
}
```

在 `runLoop` 中，定位到 `em.tokenUpdate(totalTokens, e.contextWindow)`（`agent_loop.go:274`）之后、`turnStart := time.Now()`（`agent_loop.go:276`）之前，插入：

```go
		// 记忆 nudge：每隔 nudgeInterval 轮，向发送给 LLM 的历史副本追加一行提示。
		// 注入到防御性副本，绝不写入 contextHistory（因此不会被持久化、不会累积）。
		if e.nudgeInterval > 0 && e.nudgeText != "" && turnCount%e.nudgeInterval == 0 {
			withNudge := make([]schema.Message, len(compactedHistory), len(compactedHistory)+1)
			copy(withNudge, compactedHistory)
			compactedHistory = append(withNudge, schema.Message{
				Role:    schema.RoleUser,
				Content: e.nudgeText,
			})
		}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/engine/`
Expected: PASS（含新测试与既有测试）。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/engine/
git add internal/engine/agent_loop.go internal/engine/agent_loop_test.go
git commit -m "feat(engine): WithMemoryNudge 按 turn 间隔注入记忆提示"
```

---

### Task 15: main.go 主流程接线

**Files:**
- Modify: `cmd/harness9/main.go`

- [ ] **Step 1: 接线 LTM Store + Precis（在 Session 创建后、registry 工具注册前）**

在 `sess, err := mgr.NewSession(ctx)` 块（`main.go:146-149`）之后插入：

```go
	// ---- Long-Term Memory 接线 ----
	// 复用 Manager 的 SQLite 连接，初始化长期记忆 Store 与 MEMORY.md 物化视图。
	ltmStore, err := ltm.NewStore(mgr.DB())
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("初始化长期记忆 Store 失败: %v", err)))
	}
	memoryFilePath := filepath.Join(homeDir, ".harness9", "memories", "MEMORY.md")
	ltmPrecis := ltm.NewPrecis(ltmStore, memoryFilePath, 5120)
	// 启动时回收过期记忆并重建一次精华（fail-soft）。
	if _, err := ltmStore.PurgeExpired(ctx); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("回收过期记忆失败: %v", err)))
	}
	if err := ltmPrecis.Regenerate(ctx); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("重建记忆精华失败: %v", err)))
	}
	ltmContent, _ := ltmPrecis.Read()
	promptBuilder = promptBuilder.WithLongTermMemory(ltmContent)
	// ---- Long-Term Memory 接线（续：工具注册见下）----
```

- [ ] **Step 2: 注册 memory_write / memory_search 工具**

在工具注册循环（`main.go:157-168`）的工具切片中追加两行（在 `tools.NewTodoWriteTool(...)` 之后）：

```go
		tools.NewMemoryWriteTool(ltmStore, ltmPrecis),
		tools.NewMemorySearchTool(ltmStore),
```

- [ ] **Step 3: 将 Extractor 注入 Compactor**

将 `compactor := memory.NewSummarizationCompactor(...)`（`main.go:248-250`）改为：

```go
	// SummarizationCompactor 使用同一 LLM 生成摘要，内置 TokenBudgetCompactor 作为错误回退。
	// 注入长期记忆 Extractor：压缩前从 head 消息提取持久事实（fail-open）。
	compactor := memory.NewSummarizationCompactor(llm, modelLimits.ContextTokens,
		memory.WithTodoInjector(todoStore),
		memory.WithMemoryExtractor(ltm.NewExtractor(llm, ltmStore)),
	)
```

- [ ] **Step 4: 给引擎加 turn nudge 选项**

在 `engine.NewAgentEngine(...)` 选项列表（`main.go:252-259`）中追加一行（在 `engine.WithMaxTurns(agentMaxTurns)` 之后）：

```go
		engine.WithMemoryNudge(10, "如果本轮对话中出现了值得跨会话长期保留的信息（用户偏好、稳定的项目知识、关键决策、可复用技能），请调用 memory_write 工具记录；否则忽略此提示。"),
```

- [ ] **Step 5: 补充 import**

确认 `cmd/harness9/main.go` 顶部 import 分组的项目内部包中包含：

```go
	"github.com/harness9/internal/ltm"
```

- [ ] **Step 6: 编译 + 全量测试 + 手动验证**

```bash
gofmt -w cmd/harness9/
go build ./cmd/harness9
go test ./...
```
Expected: build 成功；`go test ./...` 全绿。

手动冒烟（需配置 .env 的 API Key；无 key 时跳过此步，仅依赖单测）：
```bash
go run ./cmd/harness9 <<'EOF'
请记住：我偏好简洁的中文回复。
EOF
ls -la ~/.harness9/memories/MEMORY.md
```
Expected: LLM 调用 `memory_write` 后，`~/.harness9/memories/MEMORY.md` 存在且含「简洁」「中文」相关内容。

- [ ] **Step 7: 提交**

```bash
git add cmd/harness9/main.go
git commit -m "feat: 接线 Long-Term Memory（Store/Precis/工具/Extractor/nudge）"
```

---

### Task 16: 文档同步

**Files:**
- Create: `docs/核心功能/long-term-memory.md`
- Modify: `AGENTS.md`（`CLAUDE.md` 为符号链接，自动同步）

- [ ] **Step 1: 写核心功能文档**

创建 `docs/核心功能/long-term-memory.md`，内容涵盖：架构（ltm 包 + 复用 state.db）、存储 schema（long_term_memories + memories_fts）、MEMORY.md 物化视图、三路触发（memory_write/memory_search 工具、压缩前 Extractor、turn nudge）、冲突/遗忘机制（SHA256 去重、TTL、软删除、强化、陈旧识别）、Phase 3 接缝（Provider/Embedder/Consolidator）。结构参照 `docs/核心功能/context-engineering.md` 的写法。

- [ ] **Step 2: 更新 AGENTS.md 项目结构与模块表**

在 `AGENTS.md` 的「核心设计理念」列表中，于 Sub-Agent 条目后追加一行 Long-Term Memory 说明；在「项目结构」树中 `internal/memory/` 后插入 `internal/ltm/` 子树；在「模块职责」表中新增 `ltm` 行；在 `docs/核心功能/` 列表中追加 `long-term-memory.md`。确保描述与实现一致。

- [ ] **Step 3: 校验与提交**

```bash
test -L CLAUDE.md && echo "CLAUDE.md 是符号链接，随 AGENTS.md 同步"
gofmt -l .   # 应无输出
go test ./...
git add AGENTS.md docs/核心功能/long-term-memory.md
git commit -m "docs: 同步 Long-Term Memory 模块文档与项目结构"
```

---

## 自查结果（Self-Review）

**1. 规格覆盖：**
- 架构与包边界（规格 §2）→ T1-T7、T10
- MEMORY.md 物化视图决策（§3）→ T6、T13、T15
- 存储 Schema（§4）→ T2
- 三路触发（§5）：显式工具 → T11/T12；压缩前提取 → T8/T9；turn nudge → T14
- Context 注入（§6）→ T13、T15
- 冲突/遗忘/强化（§7）：去重 → T2；TTL/PurgeExpired → T5；软删除 → T4；强化 → T3；陈旧识别 → T5
- Phase 3 接口（§8）→ T7
- 主流程接线（§9）→ T15
- 测试策略（§10）→ 各任务 TDD
- 阶段划分（§11）→ T1-T6（Phase1/2 核心）、T7（Phase3 接缝）

**2. 占位符扫描：** 无 TBD/TODO；除 T16 文档任务（按章节描述需手写散文，已给出明确章节清单与参照文件）外，所有代码步骤均含完整代码。

**3. 类型一致性：** `ltm.Store` 方法签名（`Add/Get/Search/Update/SoftDelete/List/PurgeExpired/StaleCandidates`）在 T2-T5 定义后，T6/T9/T11/T12/T15 的调用与之一致；`Entry` 字段在 T1 定义后全程一致；`memory.MemoryExtractor.Extract(msgs)` 与 `ltm.Extractor.Extract(msgs)` 签名匹配（T8/T9）；`WithMemoryExtractor`/`WithLongTermMemory`/`WithMemoryNudge` 选项名在定义与 T15 调用处一致。

**4. 已知风险点：** T11 的 `mustJSON` 可能与 tools 包既有函数重名——已在该任务内加 grep 检查指引。
