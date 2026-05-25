package provider

import (
	"strings"
	"testing"
)

func TestWithIncludeReasoning_SetsField(t *testing.T) {
	p := &OpenAIProvider{}
	WithIncludeReasoning()(p)
	if !p.includeReasoning {
		t.Error("WithIncludeReasoning should set includeReasoning=true")
	}
}

func TestWithIncludeReasoning_AutoDetectOpenRouter(t *testing.T) {
	// OpenRouter base URL 应自动启用 includeReasoning
	p := &OpenAIProvider{includeReasoning: strings.Contains("https://openrouter.ai/api/v1", "openrouter")}
	if !p.includeReasoning {
		t.Error("OpenRouter base URL should auto-enable includeReasoning")
	}
}

func TestExtractReasoningContent_PresentField(t *testing.T) {
	raw := `{"choices":[{"delta":{"reasoning_content":"step one"}}]}`
	got := extractReasoningContent(raw)
	if got != "step one" {
		t.Errorf("got %q, want %q", got, "step one")
	}
}

func TestExtractReasoningContent_ReasoningField(t *testing.T) {
	// OpenRouter 为 OpenAI/gpt-5.x 等模型使用 delta.reasoning（无 _content 后缀）
	raw := `{"choices":[{"delta":{"reasoning":"reasoning step"}}]}`
	got := extractReasoningContent(raw)
	if got != "reasoning step" {
		t.Errorf("got %q, want %q", got, "reasoning step")
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
