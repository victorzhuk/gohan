// Package retry retries transient model failures before the first chunk.
// Once a chunk has been emitted a failure is never retried: it surfaces to
// the router's fallback.
package retry

import (
	"context"
	"errors"
	"iter"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Policy is frozen: no spec pins these values.
const (
	DefaultMaxRetries = 2
	DefaultBackoff    = 100 * time.Millisecond
	DefaultMaxBackoff = 2 * time.Second
)

type policy struct {
	maxRetries int
	backoff    time.Duration
	maxBackoff time.Duration
}

type Option func(*policy)

// WithMaxRetries sets how many times a retryable failure is re-attempted.
func WithMaxRetries(n int) Option {
	return func(p *policy) { p.maxRetries = n }
}

// WithBackoff sets the base delay of the exponential backoff.
func WithBackoff(d time.Duration) Option {
	return func(p *policy) { p.backoff = d }
}

// WithMaxBackoff caps the exponential backoff delay.
func WithMaxBackoff(d time.Duration) Option {
	return func(p *policy) { p.maxBackoff = d }
}

// Exponential retries transient failures with exponential backoff.
func Exponential(opts ...Option) types.ModelMiddleware {
	return newMiddleware(exponentialDelay, opts)
}

// RetryAfter retries transient failures after the delay the provider
// supplied on the error; without one it waits the base backoff.
func RetryAfter(opts ...Option) types.ModelMiddleware {
	return newMiddleware(retryAfterDelay, opts)
}

func newMiddleware(delay func(*types.ModelError, int, policy) time.Duration, opts []Option) types.ModelMiddleware {
	p := policy{maxRetries: DefaultMaxRetries, backoff: DefaultBackoff, maxBackoff: DefaultMaxBackoff}
	for _, opt := range opts {
		opt(&p)
	}
	return func(next types.ModelFunc) types.ModelFunc {
		return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				for attempt := 0; ; attempt++ {
					var buf []types.ModelChunk
					var attemptErr error
					next(ctx, req)(func(c types.ModelChunk, err error) bool {
						if err != nil {
							attemptErr = err
							return false
						}
						buf = append(buf, c)
						return true
					})
					if attemptErr == nil {
						for _, c := range buf {
							if !yield(c, nil) {
								return
							}
						}
						return
					}
					if !retryableBeforeFirstChunk(attemptErr, len(buf)) || attempt >= p.maxRetries {
						for _, c := range buf {
							if !yield(c, nil) {
								return
							}
						}
						yield(types.ModelChunk{}, attemptErr)
						return
					}
					me, ok := errors.AsType[*types.ModelError](attemptErr)
					if !ok {
						me = &types.ModelError{}
					}
					if !sleep(ctx, delay(me, attempt, p)) {
						yield(types.ModelChunk{}, context.Cause(ctx))
						return
					}
				}
			}
		}
	}
}

// retryableBeforeFirstChunk holds the class table: only ClassTransient is
// retried, and only when no chunk was emitted. ClassRateLimited never
// retries on the same endpoint, so it surfaces at once.
func retryableBeforeFirstChunk(err error, chunks int) bool {
	if chunks > 0 {
		return false
	}
	me, ok := errors.AsType[*types.ModelError](err)
	return ok && me.Class == types.ClassTransient
}

func exponentialDelay(_ *types.ModelError, attempt int, p policy) time.Duration {
	d := p.backoff << attempt
	if d > p.maxBackoff || d <= 0 {
		return p.maxBackoff
	}
	return d
}

func retryAfterDelay(me *types.ModelError, _ int, p policy) time.Duration {
	if me.RetryAfter > 0 {
		return me.RetryAfter
	}
	return p.backoff
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
