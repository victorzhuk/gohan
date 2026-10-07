package storetest

import (
	"context"
	"testing"
	"time"
)

// BloatResult reports what one bloat run measured.
type BloatResult struct {
	Cycles     int
	P99Latency time.Duration
	DeadTuples int64
}

// BloatHarness runs cycles of reserve/complete against one journal and
// measures the reserve p99 and the table's dead tuples afterwards.
type BloatHarness func(ctx context.Context, cycles int) (BloatResult, error)

// Bloat runs cycles reserve/complete cycles and holds the run to the p99
// and dead-tuple bounds, so an implementation that bloats its table or
// degrades its reserve path fails here, not in production.
func Bloat(t *testing.T, harness BloatHarness, cycles int, p99Bound time.Duration, deadTupleBound int64) {
	t.Helper()
	if cycles <= 0 {
		t.Fatalf("bloat cycles = %d, want positive", cycles)
	}
	res, err := harness(context.Background(), cycles)
	if err != nil {
		t.Fatalf("bloat run: %v", err)
	}
	if res.Cycles != cycles {
		t.Fatalf("bloat ran %d cycles, want %d", res.Cycles, cycles)
	}
	if res.P99Latency > p99Bound {
		t.Fatalf("reserve p99 = %s, bound %s", res.P99Latency, p99Bound)
	}
	if res.DeadTuples > deadTupleBound {
		t.Fatalf("dead tuples = %d, bound %d", res.DeadTuples, deadTupleBound)
	}
}
