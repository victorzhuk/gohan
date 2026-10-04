package limit

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/victorzhuk/gohan/core/types"
)

func TestEndpointLimits(t *testing.T) {
	t.Run("chains.per-endpoint-limits", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, err := New(1)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			a := l.Endpoint("a")
			b := l.Endpoint("b")
			relA, err := a.Acquire(context.Background())
			if err != nil {
				t.Fatalf("endpoint a Acquire: %v", err)
			}
			defer relA()

			hold := make(chan struct{})
			done := make(chan struct{})
			go func() {
				relB, err := b.Acquire(context.Background())
				if err != nil {
					t.Errorf("endpoint b Acquire: %v", err)
					close(done)
					return
				}
				<-hold
				relB()
				close(done)
			}()
			synctest.Wait()
			select {
			case <-done:
				t.Fatal("endpoint b blocked by endpoint a's saturation")
			default:
			}
			if got := b.InFlight(); got != 1 {
				t.Errorf("endpoint b InFlight = %d, want 1", got)
			}
			close(hold)
			synctest.Wait()
			<-done
		})
	})

	t.Run("admits exactly maxInFlight under contention", func(t *testing.T) {
		const max = 3
		synctest.Test(t, func(t *testing.T) {
			l, err := New(max)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			e := l.Endpoint("shared")
			var rels []func()
			for range max {
				rel, err := e.Acquire(context.Background())
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				rels = append(rels, rel)
			}
			const waiters = 5
			errc := make(chan error, waiters)
			for range waiters {
				go func() {
					rel, err := e.Acquire(context.Background())
					if err == nil {
						rel()
					}
					errc <- err
				}()
			}
			synctest.Wait()
			if got := e.InFlight(); got != max {
				t.Errorf("InFlight under contention = %d, want exactly %d", got, max)
			}
			select {
			case err := <-errc:
				t.Fatalf("waiter admitted past the ceiling: err=%v", err)
			default:
			}
			rels[0]()
			rels[1]()
			rels[2]()
			synctest.Wait()
			for range waiters {
				if err := <-errc; err != nil {
					t.Errorf("waiter Acquire: %v", err)
				}
			}
			if got := e.InFlight(); got != 0 {
				t.Errorf("InFlight after all releases = %d, want 0", got)
			}
		})
	})

	t.Run("release on success path frees the slot", func(t *testing.T) {
		l, err := New(1)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		e := l.Endpoint("a")
		rel, err := e.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		rel()
		if got := e.InFlight(); got != 0 {
			t.Fatalf("InFlight after release = %d, want 0", got)
		}
		rel2, err := e.Acquire(context.Background())
		if err != nil {
			t.Fatalf("second Acquire after release: %v", err)
		}
		rel2()
	})

	t.Run("release on error path frees the slot", func(t *testing.T) {
		l, err := New(1)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		e := l.Endpoint("a")
		ctx, cancel := context.WithCancel(context.Background())
		rel, err := e.Acquire(ctx)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		cancel()
		// Simulate the caller's work failing: the release still runs.
		rel()
		if got := e.InFlight(); got != 0 {
			t.Fatalf("InFlight after release on error path = %d, want 0", got)
		}
		if _, err := e.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire after error-path release: %v", err)
		}
	})

	t.Run("cancelled waiter returns without a slot", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			l, err := New(1)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			e := l.Endpoint("a")
			rel, err := e.Acquire(context.Background())
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			defer rel()

			ctx, cancel := context.WithCancel(context.Background())
			errc := make(chan error, 1)
			go func() {
				_, err := e.Acquire(ctx)
				errc <- err
			}()
			synctest.Wait()
			cancel()
			if err := <-errc; !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled Acquire err = %v, want context.Canceled", err)
			}
			if got := e.InFlight(); got != 1 {
				t.Errorf("InFlight after cancelled wait = %d, want 1 (slot untouched)", got)
			}
		})
	})

	t.Run("TryAcquire reports LimitExceededError when saturated", func(t *testing.T) {
		l, err := New(1)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		e := l.Endpoint("a")
		rel, err := e.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		defer rel()
		_, err = e.TryAcquire()
		var limitErr *types.LimitExceededError
		if !errors.As(err, &limitErr) {
			t.Fatalf("TryAcquire err = %v, want *types.LimitExceededError", err)
		}
		if limitErr.Limit != "max_in_flight" {
			t.Errorf("Limit = %q, want max_in_flight", limitErr.Limit)
		}
	})

	t.Run("New rejects a non-positive ceiling", func(t *testing.T) {
		if _, err := New(0); err == nil {
			t.Error("New(0) err = nil, want error")
		}
	})
}
