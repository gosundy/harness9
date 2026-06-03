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
