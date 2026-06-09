package observability_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/observability"
	"github.com/harness9/internal/schema"
)

func TestObservabilityHook_BeforeAndAfter(t *testing.T) {
	// 使用 noop provider，不产生真实 span 数据
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	hook, err := observability.NewObservabilityHook(p)
	if err != nil {
		t.Fatalf("NewObservabilityHook: %v", err)
	}

	tc := schema.ToolCall{ID: "tc1", Name: "bash"}
	ctx, dec, err := hook.BeforeExecute(context.Background(), tc)
	if err != nil {
		t.Fatalf("BeforeExecute error: %v", err)
	}
	if dec.Action != hooks.HookActionAllow {
		t.Errorf("expected HookActionAllow, got %v", dec.Action)
	}

	result := schema.ToolResult{ToolCallID: "tc1", Output: "ok", IsError: false}
	out := hook.AfterExecute(ctx, tc, result)
	if out.ToolCallID != "tc1" {
		t.Errorf("AfterExecute changed ToolCallID")
	}
}

func TestObservabilityHook_ErrorTool(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	hook, _ := observability.NewObservabilityHook(p)

	tc := schema.ToolCall{ID: "tc2", Name: "bash"}
	ctx, _, _ := hook.BeforeExecute(context.Background(), tc)

	result := schema.ToolResult{ToolCallID: "tc2", Output: "command failed", IsError: true}
	out := hook.AfterExecute(ctx, tc, result)
	// IsError 应透传不变
	if !out.IsError {
		t.Error("AfterExecute should preserve IsError=true")
	}
}
