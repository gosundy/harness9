package memory_test

import (
	"context"
	"testing"

	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/schema"
)

func TestMemorySession_AddAndGet(t *testing.T) {
	ctx := context.Background()
	s := memory.NewMemorySession("test-id")

	if s.SessionID() != "test-id" {
		t.Fatalf("want test-id, got %q", s.SessionID())
	}

	err := s.AddMessages(ctx, []schema.Message{
		{Role: schema.RoleUser, Content: "hello"},
		{Role: schema.RoleAssistant, Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}

	all, err := s.GetMessages(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
	if all[0].Content != "hello" {
		t.Errorf("want hello, got %q", all[0].Content)
	}
}

func TestMemorySession_GetWithLimit(t *testing.T) {
	ctx := context.Background()
	s := memory.NewMemorySession("lim")
	_ = s.AddMessages(ctx, []schema.Message{
		{Role: schema.RoleUser, Content: "a"},
		{Role: schema.RoleUser, Content: "b"},
		{Role: schema.RoleUser, Content: "c"},
	})

	got, err := s.GetMessages(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Content != "b" || got[1].Content != "c" {
		t.Errorf("want last 2: b,c; got %q,%q", got[0].Content, got[1].Content)
	}
}

func TestMemorySession_PopMessage(t *testing.T) {
	ctx := context.Background()
	s := memory.NewMemorySession("pop")
	_ = s.AddMessages(ctx, []schema.Message{
		{Role: schema.RoleUser, Content: "first"},
		{Role: schema.RoleAssistant, Content: "second"},
	})

	popped, err := s.PopMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if popped == nil || popped.Content != "second" {
		t.Errorf("want second, got %v", popped)
	}

	all, _ := s.GetMessages(ctx, 0)
	if len(all) != 1 {
		t.Fatalf("want 1 after pop, got %d", len(all))
	}
}

func TestMemorySession_PopEmpty(t *testing.T) {
	ctx := context.Background()
	s := memory.NewMemorySession("empty")
	popped, err := s.PopMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if popped != nil {
		t.Errorf("want nil, got %v", popped)
	}
}

func TestMemorySession_Clear(t *testing.T) {
	ctx := context.Background()
	s := memory.NewMemorySession("clr")
	_ = s.AddMessages(ctx, []schema.Message{{Role: schema.RoleUser, Content: "x"}})
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := s.GetMessages(ctx, 0)
	if len(all) != 0 {
		t.Errorf("want 0 after clear, got %d", len(all))
	}
}
