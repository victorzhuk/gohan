package loadtest

import (
	"context"
	"errors"
	"testing"
)

func TestRunIndexOrderAndWall(t *testing.T) {
	rs := Run(context.Background(), 4, func(_ context.Context, i int) error {
		return nil
	})
	if len(rs) != 4 {
		t.Fatalf("len = %d, want 4", len(rs))
	}
	for i, r := range rs {
		if r.Index != i {
			t.Fatalf("results[%d].Index = %d", i, r.Index)
		}
		if r.Wall <= 0 {
			t.Fatalf("results[%d].Wall = %v, want > 0", i, r.Wall)
		}
		if r.Started.IsZero() || r.Done.Before(r.Started) {
			t.Fatalf("results[%d] time bounds: started=%v done=%v", i, r.Started, r.Done)
		}
	}
}

func TestRunStartBarrier(t *testing.T) {
	const k = 4
	entered := make(chan int, k)
	gate := make(chan struct{})
	// Collector closes the gate only after all k workers entered fn; Run
	// returning at all therefore proves the barrier held.
	go func() {
		for range k {
			<-entered
		}
		close(gate)
	}()
	rs := Run(context.Background(), k, func(_ context.Context, i int) error {
		entered <- i
		<-gate
		return nil
	})
	if len(rs) != k {
		t.Fatalf("len = %d, want %d", len(rs), k)
	}
}

func TestRunErrorPropagation(t *testing.T) {
	sentinel := errors.New("boom")
	rs := Run(context.Background(), 2, func(_ context.Context, i int) error {
		if i == 1 {
			return sentinel
		}
		return nil
	})
	if rs[0].Err != nil {
		t.Fatalf("results[0].Err = %v, want nil", rs[0].Err)
	}
	if !errors.Is(rs[1].Err, sentinel) {
		t.Fatalf("results[1].Err = %v, want sentinel", rs[1].Err)
	}
}

func TestRunFewerThanOne(t *testing.T) {
	if rs := Run(context.Background(), 0, func(context.Context, int) error { return nil }); len(rs) != 0 {
		t.Fatalf("k=0: len = %d, want 0", len(rs))
	}
	if rs := Run(context.Background(), -3, nil); len(rs) != 0 {
		t.Fatalf("k=-3: len = %d, want 0", len(rs))
	}
}

func TestRunNilFunc(t *testing.T) {
	rs := Run(context.Background(), 2, nil)
	for i, r := range rs {
		if r.Err == nil {
			t.Fatalf("results[%d].Err = nil, want error", i)
		}
	}
}
