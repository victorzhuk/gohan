package route

import (
	"context"
	"errors"
	"iter"

	"github.com/victorzhuk/gohan/core/types"
)

// ChainFunc builds one endpoint's inner pipeline — retry, then limiter, then
// the adapter — as a ModelFunc bound to that endpoint.
type ChainFunc func(types.Model) types.ModelFunc

// FallbackConfig holds the callbacks a Fallback reports to.
type FallbackConfig struct {
	// Served is called once, with the endpoint whose call completed. It is
	// the charging point: run charging (RunLimits) reads the usage off this
	// endpoint's call.
	Served func(types.Model)
}

// FallbackOption configures a Fallback.
type FallbackOption func(*FallbackConfig)

// WithServed sets the callback invoked with the endpoint that served the
// call; its usage is the usage to charge.
func WithServed(fn func(types.Model)) FallbackOption {
	return func(c *FallbackConfig) { c.Served = fn }
}

// Fallback fails a model call over to the next endpoint of the router's
// strategy order, running it through the chain each candidate endpoint gets.
// Frozen: no spec declares the shape.
//
// Fallback wraps the retry middleware in each endpoint's chain: a transient
// error is retried on the same endpoint first (the retry policy), and only
// an exhausted retry surfaces here. A rate-limited error never retries on
// the same endpoint, so it fails over at once. ClassAuth, ClassPermanent and
// ClassContentPolicy never fail over; ClassVersionDrift does.
//
// Failover happens before the first chunk only: once any endpoint has
// emitted a chunk, an error surfaces and no further endpoint is tried, at
// most once per chunk stream.
func Fallback(r *Router, chain ChainFunc, opts ...FallbackOption) types.ModelFunc {
	var cfg FallbackConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			candidates := r.Candidates()
			if len(candidates) == 0 {
				yield(types.ModelChunk{}, &types.ModelError{
					Class: types.ClassTransient,
					Code:  "all_endpoints_open",
				})
				return
			}
			var lastErr error
			for _, m := range candidates {
				done, err := tryEndpoint(ctx, chain(m), req, m, yield, r, &cfg)
				if done {
					return
				}
				lastErr = err
			}
			yield(types.ModelChunk{}, lastErr)
		}
	}
}

// tryEndpoint streams one endpoint. It reports done=true when the fallback
// must stop (the endpoint served, or the error surfaced to the caller), and
// err=nil when another endpoint should be tried.
func tryEndpoint(
	ctx context.Context,
	next types.ModelFunc,
	req types.ModelRequest,
	m types.Model,
	yield func(types.ModelChunk, error) bool,
	r *Router,
	cfg *FallbackConfig,
) (done bool, err error) {
	name := m.Profile().Name
	chunks := 0
	for chunk, streamErr := range next(ctx, req) {
		if streamErr == nil {
			chunks++
			if !yield(chunk, nil) {
				served(r, cfg, m)
				return true, nil
			}
			continue
		}
		r.RecordFailure(name, streamErr)
		if chunks > 0 || !failoverEligible(streamErr) {
			yield(types.ModelChunk{}, streamErr)
			return true, streamErr
		}
		return false, streamErr
	}
	served(r, cfg, m)
	return true, nil
}

func served(r *Router, cfg *FallbackConfig, m types.Model) {
	r.RecordSuccess(m.Profile().Name)
	if cfg.Served != nil {
		cfg.Served(m)
	}
}

// failoverEligible holds the class table: rate-limited and transient errors
// fail over, version drift fails over immediately, and everything else
// surfaces to the caller.
func failoverEligible(err error) bool {
	me, ok := errAsModelError(err)
	if !ok {
		return false
	}
	return me.Class == types.ClassRateLimited ||
		me.Class == types.ClassTransient ||
		me.Class == types.ClassVersionDrift
}

func errAsModelError(err error) (*types.ModelError, bool) {
	me, ok := errors.AsType[*types.ModelError](err)
	return me, ok
}
