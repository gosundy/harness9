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
