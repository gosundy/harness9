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
