package provider

import (
	"testing"
)

func TestExtractReasoningContent_PresentField(t *testing.T) {
	raw := `{"choices":[{"delta":{"reasoning_content":"step one"}}]}`
	got := extractReasoningContent(raw)
	if got != "step one" {
		t.Errorf("got %q, want %q", got, "step one")
	}
}

func TestExtractReasoningContent_AbsentField(t *testing.T) {
	raw := `{"choices":[{"delta":{"content":"hello"}}]}`
	got := extractReasoningContent(raw)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractReasoningContent_EmptyChoices(t *testing.T) {
	raw := `{"choices":[]}`
	got := extractReasoningContent(raw)
	if got != "" {
		t.Errorf("expected empty string for empty choices, got %q", got)
	}
}

func TestExtractReasoningContent_InvalidJSON(t *testing.T) {
	raw := `not-json`
	got := extractReasoningContent(raw)
	if got != "" {
		t.Errorf("expected empty string for invalid JSON, got %q", got)
	}
}
