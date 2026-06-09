// Package observability — OTELEngineObserver 实现 engine.EngineObserver，
// 为每次 interaction 和每个 Turn 创建 OTEL Span。
package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/harness9/internal/engine"
)

// 确保编译期接口检查。
var _ engine.EngineObserver = (*OTELEngineObserver)(nil)

// interactionSpanKey 存储 interaction-level Span 的 ctx key。
type interactionSpanKey struct{}

// turnSpanKey 存储 turn-level Span 的 ctx key。
type turnSpanKey struct{}

// OTELEngineObserver 实现 engine.EngineObserver，
// 用 OTEL Span 覆盖每次 interaction（顶层父节点）和每个 Turn。
type OTELEngineObserver struct {
	tracer     trace.Tracer
	turnsTotal metric.Int64Counter
}

// NewOTELEngineObserver 构造 OTELEngineObserver，初始化 turns 计数器。
func NewOTELEngineObserver(p *Providers) (*OTELEngineObserver, error) {
	turns, err := p.Meter.Int64Counter(MetricTurnsTotal,
		metric.WithDescription("Total agent turns executed"))
	if err != nil {
		return nil, err
	}
	return &OTELEngineObserver{tracer: p.Tracer, turnsTotal: turns}, nil
}

// OnInteractionStart 启动顶层 interaction Span，注入 session.id 属性，将 Span 存入 ctx。
func (o *OTELEngineObserver) OnInteractionStart(ctx context.Context, sessionID, prompt string) context.Context {
	ctx, span := o.tracer.Start(ctx, SpanInteraction,
		trace.WithAttributes(attribute.String(AttrSessionID, sessionID)),
	)
	return context.WithValue(ctx, interactionSpanKey{}, span)
}

// OnInteractionEnd 结束 interaction Span，记录总 turns 数。
func (o *OTELEngineObserver) OnInteractionEnd(ctx context.Context, turns int, err error) {
	span, _ := ctx.Value(interactionSpanKey{}).(trace.Span)
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
	}
	span.SetAttributes(attribute.Int(AttrTurnNumber, turns))
	span.End()
}

// OnTurnStart 启动 turn-level Span（interaction Span 的子节点），将其存入 ctx。
func (o *OTELEngineObserver) OnTurnStart(ctx context.Context, turn int) context.Context {
	ctx, span := o.tracer.Start(ctx, SpanTurn,
		trace.WithAttributes(attribute.Int(AttrTurnNumber, turn)),
	)
	return context.WithValue(ctx, turnSpanKey{}, span)
}

// OnTurnEnd 结束 turn Span，增加 turns 计数。
func (o *OTELEngineObserver) OnTurnEnd(ctx context.Context, turn int, hasToolCalls bool) {
	span, _ := ctx.Value(turnSpanKey{}).(trace.Span)
	if span != nil {
		span.SetAttributes(attribute.Bool("turn.has_tool_calls", hasToolCalls))
		span.End()
	}
	o.turnsTotal.Add(ctx, 1)
}
