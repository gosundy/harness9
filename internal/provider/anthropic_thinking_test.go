package provider

import (
	"testing"
)

func TestWithThinkingBudget_SetsField(t *testing.T) {
	p := &AnthropicProvider{}
	WithThinkingBudget(8000)(p)
	if p.thinkingBudget != 8000 {
		t.Errorf("thinkingBudget = %d, want 8000", p.thinkingBudget)
	}
}

func TestWithThinkingBudget_ZeroDisables(t *testing.T) {
	p := &AnthropicProvider{thinkingBudget: 5000}
	WithThinkingBudget(0)(p)
	if p.thinkingBudget != 0 {
		t.Errorf("thinkingBudget should be 0 after WithThinkingBudget(0), got %d", p.thinkingBudget)
	}
}
