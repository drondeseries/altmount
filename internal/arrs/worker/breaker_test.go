package worker

import (
	"context"
	"testing"

	"github.com/kipsilabs/altmount/internal/config"
)

func newTestWorker() *Worker {
	return NewWorker(nil, nil, nil, nil, nil)
}

func TestApplyBreaker_DisabledOrNoKey(t *testing.T) {
	w := newTestWorker()
	ctx := context.Background()

	// Disabled (maxFailures == 0): base action returned, no unmonitor, no counting.
	item := stuckItem{BreakerKey: "k", Unmonitor: func(context.Context) error { return nil }}
	for i := 0; i < 5; i++ {
		action, unmon := w.applyBreaker(ctx, item, config.StuckActionBlocklistSearch, 0, "inst")
		if action != config.StuckActionBlocklistSearch || unmon != nil {
			t.Fatalf("disabled breaker should pass through base action with no unmonitor, got %q unmon=%v", action, unmon != nil)
		}
	}

	// No breaker key: never escalates even with a positive threshold.
	noKey := stuckItem{Unmonitor: func(context.Context) error { return nil }}
	for i := 0; i < 5; i++ {
		action, unmon := w.applyBreaker(ctx, noKey, config.StuckActionRemove, 2, "inst")
		if action != config.StuckActionRemove || unmon != nil {
			t.Fatalf("keyless item should never trip the breaker, got %q unmon=%v", action, unmon != nil)
		}
	}
}

func TestApplyBreaker_EscalatesAtThreshold(t *testing.T) {
	w := newTestWorker()
	ctx := context.Background()
	called := false
	item := stuckItem{
		BreakerKey: "sonarr|inst|ep:42",
		Unmonitor:  func(context.Context) error { called = true; return nil },
	}

	// Threshold 2: first act uses the base action, second gives up.
	if action, unmon := w.applyBreaker(ctx, item, config.StuckActionBlocklistSearch, 2, "inst"); action != config.StuckActionBlocklistSearch || unmon != nil {
		t.Fatalf("first failure should keep base action, got %q unmon=%v", action, unmon != nil)
	}

	action, unmon := w.applyBreaker(ctx, item, config.StuckActionBlocklistSearch, 2, "inst")
	if action != config.StuckActionBlocklist {
		t.Fatalf("at threshold action should downgrade to blocklist (no re-search), got %q", action)
	}
	if unmon == nil {
		t.Fatal("at threshold the unmonitor closure should be returned")
	}
	if err := unmon(ctx); err != nil || !called {
		t.Fatalf("returned unmonitor closure should invoke the item's unmonitor, called=%v err=%v", called, err)
	}
}

func TestResetBreaker_ClearsCount(t *testing.T) {
	w := newTestWorker()
	ctx := context.Background()
	item := stuckItem{BreakerKey: "radarr|inst|movie:7"}

	// One failure recorded, then reset (as happens on a healthy import).
	_, _ = w.applyBreaker(ctx, item, config.StuckActionRemove, 2, "inst")
	w.resetBreaker(item.BreakerKey)

	// After reset, the next failure is the "first" again and must not escalate.
	if action, unmon := w.applyBreaker(ctx, item, config.StuckActionRemove, 2, "inst"); action != config.StuckActionRemove || unmon != nil {
		t.Fatalf("after reset the breaker should start over, got %q unmon=%v", action, unmon != nil)
	}
}

func TestApplyBreaker_MultiKeySelectiveUnmonitor(t *testing.T) {
	w := newTestWorker()
	ctx := context.Background()

	// ep:10 already had 1 failure
	w.bumpBreaker("sonarr|inst|ep:10")

	var unmonitored []string
	item := stuckItem{
		BreakerKeys: []string{"sonarr|inst|ep:10", "sonarr|inst|ep:11"},
		UnmonitorTarget: func(ctx context.Context, trippedKeys []string) error {
			unmonitored = append(unmonitored, trippedKeys...)
			return nil
		},
	}

	// Threshold 2: ep:10 hits 2 and trips; ep:11 hits 1 and does NOT trip
	action, unmon := w.applyBreaker(ctx, item, config.StuckActionBlocklistSearch, 2, "inst")
	if action != config.StuckActionBlocklist {
		t.Fatalf("expected action %q, got %q", config.StuckActionBlocklist, action)
	}
	if unmon == nil {
		t.Fatal("expected unmonitor closure")
	}
	if err := unmon(ctx); err != nil {
		t.Fatalf("unmonitor error: %v", err)
	}
	if len(unmonitored) != 1 || unmonitored[0] != "sonarr|inst|ep:10" {
		t.Fatalf("expected only ep:10 to be unmonitored, got %v", unmonitored)
	}
}
