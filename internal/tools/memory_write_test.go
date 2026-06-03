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

func TestMemoryWriteUpdatePreservesOmittedContent(t *testing.T) {
	tool, store := newWriteTool(t)
	ctx := context.Background()
	addOut, _ := tool.Execute(ctx, json.RawMessage(
		`{"action":"add","title":"原标题","content":"原始内容","importance":3}`))
	var added ltm.Entry
	if err := json.Unmarshal([]byte(addOut), &added); err != nil {
		t.Fatalf("解析 add 输出: %v", err)
	}
	// 仅更新 title 与 importance，省略 content —— content 应保留原值。
	if _, err := tool.Execute(ctx, json.RawMessage(
		`{"action":"update","id":"`+added.ID+`","title":"新标题","importance":9}`)); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "新标题" {
		t.Errorf("title 应更新为「新标题」，got %q", got.Title)
	}
	if got.Content != "原始内容" {
		t.Errorf("省略的 content 应保留原值「原始内容」，got %q", got.Content)
	}
	if got.Importance != 9 {
		t.Errorf("importance 应更新为 9，got %d", got.Importance)
	}
}
