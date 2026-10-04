// Package limit provides the local MaxInFlight bulkhead with independent
// per-endpoint wrapping. The per-endpoint ceiling arrives as a constructor
// argument; profile wiring belongs to the route and Build layers.
package limit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/victorzhuk/gohan/core/types"
)

// Endpoint is one endpoint's bulkhead: at most maxInFlight calls in flight.
// Endpoints are independent — one endpoint's saturation never delays another.
type Endpoint struct {
	slots    chan struct{}
	inFlight atomic.Int64
}

// Acquire blocks until a slot is free or ctx is done. The returned release
// function frees the slot; call it exactly once on both the success and the
// error path.
func (e *Endpoint) Acquire(ctx context.Context) (func(), error) {
	select {
	case e.slots <- struct{}{}:
		e.inFlight.Add(1)
		return e.release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TryAcquire takes a slot without waiting. On saturation it reports the
// floor's LimitExceededError.
func (e *Endpoint) TryAcquire() (func(), error) {
	select {
	case e.slots <- struct{}{}:
		e.inFlight.Add(1)
		return e.release, nil
	default:
		return nil, &types.LimitExceededError{Limit: "max_in_flight", Value: float64(cap(e.slots))}
	}
}

// InFlight reports the current number of in-flight calls.
func (e *Endpoint) InFlight() int { return int(e.inFlight.Load()) }

func (e *Endpoint) release() {
	<-e.slots
	e.inFlight.Add(-1)
}

// Limiter vends one bulkhead per endpoint name, each with the same ceiling.
type Limiter struct {
	max  int
	mu   sync.Mutex
	ends map[string]*Endpoint
}

// Option configures a Limiter.
type Option func(*Limiter)

// New returns a Limiter whose every endpoint admits at most maxInFlight
// concurrent calls. A non-positive ceiling is rejected: a bulkhead without a
// ceiling is not a bulkhead.
func New(maxInFlight int, opts ...Option) (*Limiter, error) {
	if maxInFlight <= 0 {
		return nil, fmt.Errorf("gohan/limit: maxInFlight must be positive, got %d", maxInFlight)
	}
	l := &Limiter{max: maxInFlight, ends: make(map[string]*Endpoint)}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// Endpoint returns the bulkhead for name, creating it on first use. Distinct
// names get distinct ceilings; the same name always gets the same bulkhead.
func (l *Limiter) Endpoint(name string) *Endpoint {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.ends[name]
	if !ok {
		e = &Endpoint{slots: make(chan struct{}, l.max)}
		l.ends[name] = e
	}
	return e
}
