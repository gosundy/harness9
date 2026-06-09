package observability_test

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/harness9/internal/observability"
)

func TestOTELEngineObserver_LifecycleNoPanic(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	ctx := context.Background()
	ctx = obs.OnInteractionStart(ctx, "session-123", "hello")

	ctx = obs.OnTurnStart(ctx, 1)
	obs.OnTurnEnd(ctx, 1, false)

	ctx = obs.OnTurnStart(ctx, 2)
	obs.OnTurnEnd(ctx, 2, true)

	obs.OnInteractionEnd(ctx, 2, nil)
	// noop tracer 不 panic 即视为通过
}

func TestOTELEngineObserver_ErrorPropagation(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	obs, _ := observability.NewOTELEngineObserver(p)

	ctx := obs.OnInteractionStart(context.Background(), "s1", "task")
	obs.OnInteractionEnd(ctx, 0, fmt.Errorf("test error"))
	// noop tracer 对 RecordError 静默，不 panic
}

func TestOTELEngineObserver_NilSpanSafety(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	// 直接调用 End 方法，ctx 中没有存储 Span（模拟未调用 OnInteractionStart 的情况）
	obs.OnInteractionEnd(context.Background(), 0, nil)
	obs.OnTurnEnd(context.Background(), 0, false)
	// 不 panic 即通过
}

func TestOTELEngineObserver_Multipleturns(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	ctx := obs.OnInteractionStart(context.Background(), "session-multi", "multi-turn test")

	const numTurns = 5
	for i := 1; i <= numTurns; i++ {
		turnCtx := obs.OnTurnStart(ctx, i)
		obs.OnTurnEnd(turnCtx, i, i%2 == 0)
	}

	obs.OnInteractionEnd(ctx, numTurns, nil)
}
