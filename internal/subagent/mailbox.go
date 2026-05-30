package subagent

import "sync"

// CompletedTask 是一个已完成后台子代理任务的结果。
type CompletedTask struct {
	TaskID    string
	AgentName string
	FinalText string
	IsError   bool
}

// Mailbox 是后台子代理结果的线程安全信箱。后台 goroutine 通过 Deliver 投递，
// 消费侧（TUI/CLI）在父代理下次 dispatch 前通过 Drain 排空并注入上下文。
type Mailbox struct {
	mu      sync.Mutex
	pending []CompletedTask
	notify  func()
}

// NewMailbox 创建空信箱。
func NewMailbox() *Mailbox {
	return &Mailbox{}
}

// SetNotify 设置完成通知回调（TUI 注入），Deliver 时触发。线程安全。
func (m *Mailbox) SetNotify(fn func()) {
	m.mu.Lock()
	m.notify = fn
	m.mu.Unlock()
}

// Deliver 投递一条已完成任务结果，并触发通知回调（若有）。线程安全。
func (m *Mailbox) Deliver(t CompletedTask) {
	m.mu.Lock()
	m.pending = append(m.pending, t)
	notify := m.notify
	m.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// Drain 取出并清空所有待处理结果。线程安全。
func (m *Mailbox) Drain() []CompletedTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return nil
	}
	out := m.pending
	m.pending = nil
	return out
}

// Pending 返回待处理结果数量。线程安全。
func (m *Mailbox) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}
