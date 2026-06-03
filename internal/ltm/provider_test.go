// internal/ltm/provider_test.go
package ltm

import (
	"context"
	"testing"

	"github.com/harness9/internal/schema"
)

func TestNoopProviderSatisfiesInterface(t *testing.T) {
	var p Provider = NewNoopProvider()
	ctx := context.Background()
	if got, err := p.Prefetch(ctx, "q"); err != nil || got != nil {
		t.Errorf("noop Prefetch 应返回 (nil,nil)，got (%v,%v)", got, err)
	}
	if err := p.Sync(ctx, "u", "a"); err != nil {
		t.Errorf("noop Sync 应返回 nil，got %v", err)
	}
	if err := p.OnPreCompress(ctx, []schema.Message{}); err != nil {
		t.Errorf("noop OnPreCompress 应返回 nil，got %v", err)
	}
	if err := p.OnSessionEnd(ctx); err != nil {
		t.Errorf("noop OnSessionEnd 应返回 nil，got %v", err)
	}
}
