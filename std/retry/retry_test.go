package retry

import (
	"context"
	"errors"
	"iter"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func transientErr(after time.Duration) error {
	return &types.ModelError{Class: types.ClassTransient, Provider: "test", RetryAfter: after}
}

func classErr(class types.ErrorClass) error {
	return &types.ModelError{Class: class, Provider: "test"}
}

// recording captures what the consumer sees and counts upstream attempts.
type recording struct {
	chunks  []types.ModelChunk
	err     error
	attempt func()
}

func (r *recording) model(errs []error, chunksPerAttempt int) types.ModelFunc {
	n := 0
	return func(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			r.attempt()
			if n >= len(errs) {
				for range chunksPerAttempt {
					if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil) {
						return
					}
				}
				return
			}
			err := errs[n]
			n++
			for range chunksPerAttempt {
				if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: "partial"}, nil) {
					return
				}
			}
			yield(types.ModelChunk{}, err)
		}
	}
}

func consume(next types.ModelFunc) (recording, error) {
	rec := recording{}
	next(context.Background(), types.ModelRequest{})(func(c types.ModelChunk, err error) bool {
		if err != nil {
			rec.err = err
			return false
		}
		rec.chunks = append(rec.chunks, c)
		return true
	})
	return rec, rec.err
}

func TestRetryPolicy(t *testing.T) {
	t.Run("model.5xx-retries-then-fails-over", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var attempts int
			rec := recording{attempt: func() { attempts++ }}
			next := Exponential(
				WithMaxRetries(2),
				WithBackoff(time.Millisecond),
			)(rec.model([]error{transientErr(0), transientErr(0), transientErr(0)}, 0))
			_, err := consume(next)
			if attempts != 3 {
				t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", attempts)
			}
			me, _ := errors.AsType[*types.ModelError](err)
			if me == nil || me.Class != types.ClassTransient {
				t.Fatalf("err = %v, want ClassTransient ModelError", err)
			}
		})
	})

	t.Run("chains.no-retry-after-first-chunk", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var attempts int
			rec := recording{attempt: func() { attempts++ }}
			next := Exponential(WithMaxRetries(5))(rec.model([]error{transientErr(0)}, 2))
			got, err := consume(next)
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1 after first chunk", attempts)
			}
			if len(got.chunks) != 2 {
				t.Fatalf("chunks = %d, want 2 (one per attempt, buffered then surfaced)", len(got.chunks))
			}
			me, _ := errors.AsType[*types.ModelError](err)
			if me == nil || me.Class != types.ClassTransient {
				t.Fatalf("err = %v, want transient surfaced", err)
			}
		})
	})

	t.Run("retry-after-honours-provider-delay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var attempts int
			rec := recording{attempt: func() { attempts++ }}
			start := time.Now()
			next := RetryAfter(WithMaxRetries(1))(rec.model([]error{transientErr(5 * time.Second), nil}, 0))
			if _, err := consume(next); err != nil {
				t.Fatalf("consume: %v", err)
			}
			if waited := time.Since(start); waited != 5*time.Second {
				t.Fatalf("waited %v, want 5s from RetryAfter", waited)
			}
			if attempts != 2 {
				t.Fatalf("attempts = %d, want 2", attempts)
			}
		})
	})

	t.Run("retry-after-falls-back-to-base-backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := recording{attempt: func() {}}
			start := time.Now()
			next := RetryAfter(WithMaxRetries(1))(rec.model([]error{transientErr(0), nil}, 0))
			if _, err := consume(next); err != nil {
				t.Fatalf("consume: %v", err)
			}
			if waited := time.Since(start); waited != DefaultBackoff {
				t.Fatalf("waited %v, want default backoff %v", waited, DefaultBackoff)
			}
		})
	})

	t.Run("class-table", func(t *testing.T) {
		for _, tc := range []struct {
			name         string
			class        types.ErrorClass
			wantAttempts int
		}{
			{"transient retried", types.ClassTransient, 3},
			{"permanent not retried", types.ClassPermanent, 1},
			{"auth not retried", types.ClassAuth, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var attempts int
					rec := recording{attempt: func() { attempts++ }}
					next := Exponential(WithMaxRetries(2), WithBackoff(time.Millisecond))(
						rec.model([]error{classErr(tc.class), classErr(tc.class), classErr(tc.class)}, 0))
					_, err := consume(next)
					if attempts != tc.wantAttempts {
						t.Fatalf("class %d: attempts = %d, want %d", tc.class, attempts, tc.wantAttempts)
					}
					me, _ := errors.AsType[*types.ModelError](err)
					if me == nil || me.Class != tc.class {
						t.Fatalf("class %d: err = %v, want surfaced unchanged", tc.class, err)
					}
				})
			})
		}
	})

	t.Run("rate-limited-never-retried", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var attempts int
			rec := recording{attempt: func() { attempts++ }}
			next := Exponential(WithMaxRetries(3))(rec.model([]error{classErr(types.ClassRateLimited)}, 0))
			_, err := consume(next)
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1 (rate limited never retried)", attempts)
			}
			me, _ := errors.AsType[*types.ModelError](err)
			if me == nil || me.Class != types.ClassRateLimited {
				t.Fatalf("err = %v, want ClassRateLimited surfaced", err)
			}
		})
	})

	t.Run("context-cancel-cancels-wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var attempts int
			rec := recording{attempt: func() { attempts++ }}
			next := Exponential(WithMaxRetries(2), WithBackoff(time.Hour))(rec.model([]error{transientErr(0)}, 0))
			time.AfterFunc(50*time.Millisecond, cancel)
			done := make(chan error, 1)
			go func() {
				_, err := consumeCtx(ctx, next)
				done <- err
			}()
			err := <-done
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1 (cancelled during wait)", attempts)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	})
}

func consumeCtx(ctx context.Context, next types.ModelFunc) ([]types.ModelChunk, error) {
	var chunks []types.ModelChunk
	var err error
	next(ctx, types.ModelRequest{})(func(c types.ModelChunk, e error) bool {
		if e != nil {
			err = e
			return false
		}
		chunks = append(chunks, c)
		return true
	})
	return chunks, err
}
