package subagent

import (
	"sync"
	"testing"
)

func TestMailboxDeliverAndDrain(t *testing.T) {
	m := NewMailbox()
	if m.Pending() != 0 {
		t.Fatal("初始应为空")
	}
	m.Deliver(CompletedTask{TaskID: "1", AgentName: "a", FinalText: "x"})
	m.Deliver(CompletedTask{TaskID: "2", AgentName: "b", FinalText: "y"})
	if m.Pending() != 2 {
		t.Fatalf("Pending=%d, want 2", m.Pending())
	}
	got := m.Drain()
	if len(got) != 2 {
		t.Fatalf("Drain 返回 %d 条, want 2", len(got))
	}
	if m.Pending() != 0 {
		t.Fatal("Drain 后应清空")
	}
	if len(m.Drain()) != 0 {
		t.Fatal("二次 Drain 应为空")
	}
}

func TestMailboxNotifyCallback(t *testing.T) {
	m := NewMailbox()
	var notified int
	m.SetNotify(func() { notified++ })
	m.Deliver(CompletedTask{TaskID: "1"})
	if notified != 1 {
		t.Fatalf("通知回调应被调用 1 次, 得 %d", notified)
	}
}

func TestMailboxConcurrentDeliver(t *testing.T) {
	m := NewMailbox()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Deliver(CompletedTask{TaskID: "x"})
		}()
	}
	wg.Wait()
	if m.Pending() != 50 {
		t.Fatalf("并发投递后 Pending=%d, want 50", m.Pending())
	}
}
