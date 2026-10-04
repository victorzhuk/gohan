// Package route routes a model call across endpoints: a circuit breaker per
// endpoint, static and latency-class ordering, and a Fallback middleware that
// fails over before the first chunk. A rate-limited response fails over at
// once; a transient one is retried first by the retry middleware it wraps.
// The successful endpoint is the one whose usage would be charged.
package route

import (
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// Frozen: no spec pins these values.
const (
	defaultOpenThreshold = 5
	defaultWindow        = 30 * time.Second
	defaultHalfOpenDelay = 10 * time.Second
)

// Breaker is one endpoint's circuit breaker. It opens after openThreshold
// consecutive rate-limited or transient failures, stays open for the window
// plus the half-open delay, then admits a single probe. A successful probe
// closes it again.
type Breaker struct {
	openThreshold int
	window        time.Duration
	halfOpenDelay time.Duration

	mu       sync.Mutex
	failures int
	openedAt time.Time
	probing  bool
}

// BreakerOption configures a Breaker.
type BreakerOption func(*Breaker)

// WithOpenThreshold sets how many consecutive failover-eligible failures open
// the breaker.
func WithOpenThreshold(n int) BreakerOption {
	return func(b *Breaker) { b.openThreshold = n }
}

// WithWindow sets how long the breaker stays fully open.
func WithWindow(d time.Duration) BreakerOption {
	return func(b *Breaker) { b.window = d }
}

// WithHalfOpenDelay sets how long after opening the half-open probe waits.
func WithHalfOpenDelay(d time.Duration) BreakerOption {
	return func(b *Breaker) { b.halfOpenDelay = d }
}

// NewBreaker returns a Breaker with the defaults and any overrides.
func NewBreaker(opts ...BreakerOption) *Breaker {
	b := &Breaker{
		openThreshold: defaultOpenThreshold,
		window:        defaultWindow,
		halfOpenDelay: defaultHalfOpenDelay,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Allow reports whether a call may start on this endpoint: the breaker is
// closed, or the half-open delay has passed and the probe is free.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.openThreshold {
		return true
	}
	elapsed := time.Since(b.openedAt)
	if elapsed < b.window+b.halfOpenDelay || b.probing {
		return false
	}
	b.probing = true
	return true
}

// Record counts a failure. A rate-limited or transient error advances the
// consecutive-failure count; any other class resets it. A failed probe
// reopens the breaker.
func (b *Breaker) Record(err error) {
	me, ok := errAsModelError(err)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !ok || !b.failoverEligible(me) {
		b.failures = 0
		b.probing = false
		return
	}
	if b.failures >= b.openThreshold {
		b.openedAt = time.Now()
		b.probing = false
		b.failures = 1
		return
	}
	b.failures++
	if b.failures >= b.openThreshold {
		b.openedAt = time.Now()
	}
}

// Succeeded closes the breaker after a healthy call or a successful probe.
func (b *Breaker) Succeeded() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.probing = false
}

func (b *Breaker) failoverEligible(me *types.ModelError) bool {
	return me.Class == types.ClassRateLimited ||
		me.Class == types.ClassTransient ||
		me.Class == types.ClassVersionDrift
}
