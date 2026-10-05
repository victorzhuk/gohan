package limit

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

// LimitsState is one run's limit ledger. The invocation factory creates one
// per run and carries it in the context, so two independent runs never share
// counters or elapsed start; a preset never captures one.
type LimitsState struct {
	mu       sync.Mutex
	cost     float64
	start    time.Time
	turns    int
	toolUses int
	warned   bool
	warnings []types.LimitWarning
}

// NewLimitsState returns the ledger one run shares across its chain steps.
func NewLimitsState() *LimitsState { return &LimitsState{} }

// Cost reports the cost charged so far.
func (s *LimitsState) Cost() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cost
}

// Warnings reports the LimitWarning values the limit steps emitted.
func (s *LimitsState) Warnings() []types.LimitWarning {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.LimitWarning(nil), s.warnings...)
}

// Snapshot is a copy of one ledger's counters, free of its lock: what a
// driver reads to decide a Done(StopLimit) at its effect boundary.
type Snapshot struct {
	Cost     float64
	Start    time.Time
	Turns    int
	ToolUses int
	Warnings []types.LimitWarning
}

// Snapshot reports the ledger's counters without mutating them.
func (s *LimitsState) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Cost:     s.cost,
		Start:    s.start,
		Turns:    s.turns,
		ToolUses: s.toolUses,
		Warnings: append([]types.LimitWarning(nil), s.warnings...),
	}
}

type limitsStateKey struct{}

// WithLimitsState returns a context that carries one run's ledger. The
// invocation factory sets it once per run; a preset never captures one.
func WithLimitsState(ctx context.Context, st *LimitsState) context.Context {
	return context.WithValue(ctx, limitsStateKey{}, st)
}

// LimitsStateFrom reports the ledger the context carries, if any.
func LimitsStateFrom(ctx context.Context) (*LimitsState, bool) {
	st, ok := ctx.Value(limitsStateKey{}).(*LimitsState)
	if !ok || st == nil {
		return nil, false
	}
	return st, true
}

func (s *LimitsState) charge(u types.Usage, p types.Pricing) float64 {
	writePrice := p.CacheWrite
	if p.SocializeCacheWrites {
		writePrice = p.CachedInput
	}
	spent := float64(u.InputTokens)*p.Input +
		float64(u.CachedInputTokens)*p.CachedInput +
		float64(u.CacheWriteTokens)*writePrice +
		float64(u.OutputTokens)*p.Output +
		u.SandboxSeconds*p.SandboxSecond
	for name, n := range u.ProviderToolCalls {
		spent += float64(n) * p.ProviderCall[name]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cost += spent
	return s.cost
}

func (s *LimitsState) warn(w types.LimitWarning) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.warned {
		return
	}
	s.warned = true
	s.warnings = append(s.warnings, w)
}

// preCall charges one model turn against the ledger and returns the wall
// clock the call has left. Turns are budgeting counters, not refusals:
// passing MaxTurns only records itself, and the driver ends the run with
// Done(StopLimit) at its effect boundary. Only a hard cost overrun or an
// expired wall clock aborts before the call.
func (s *LimitsState) preCall(l types.RunLimits, now time.Time) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	if l.MaxCost > 0 && s.cost > l.MaxCost {
		return 0, &types.LimitExceededError{Limit: "MaxCost", Value: s.cost}
	}
	if l.MaxTurns > 0 {
		s.turns++
	}
	remaining := l.MaxWallClock - now.Sub(s.start)
	if remaining <= 0 {
		return 0, &types.LimitExceededError{Limit: "MaxWallClock", Value: now.Sub(s.start).Seconds()}
	}
	return remaining, nil
}

// afterCharge applies the post-spend checks: the soft-ratio warning once,
// then the hard cost abort.
func (s *LimitsState) afterCharge(l types.RunLimits) error {
	if l.MaxCost <= 0 {
		return nil
	}
	cost := s.Cost()
	if cost >= l.MaxCost*l.SoftRatio {
		s.warn(types.LimitWarning{Limit: "MaxCost", Ratio: cost / l.MaxCost})
	}
	if cost > l.MaxCost {
		return &types.LimitExceededError{Limit: "MaxCost", Value: cost}
	}
	return nil
}

// Limits returns the model-chain limit step. It charges the message's usage
// at message end through the profile's pricing, warns once at the soft ratio
// and aborts on a hard cost overrun or an expired wall clock. Fallback and
// retry run inside it, so a chunk is charged once no matter how many
// endpoints it took to serve.
func Limits(l types.RunLimits, p types.Pricing, st *LimitsState) types.ModelMiddleware {
	return func(next types.ModelFunc) types.ModelFunc {
		return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				s := st
				if fromCtx, ok := LimitsStateFrom(ctx); ok {
					s = fromCtx
				}
				remaining, err := s.preCall(l, time.Now())
				if err != nil {
					yield(types.ModelChunk{}, err)
					return
				}
				callCtx, cancel := context.WithTimeout(ctx, remaining)
				defer cancel()
				var usage types.Usage
				for chunk, cerr := range next(callCtx, req) {
					if cerr != nil {
						yield(chunk, cerr)
						return
					}
					if chunk.Usage != nil {
						usage = *chunk.Usage
					}
					if !yield(chunk, nil) {
						return
					}
					if callCtx.Err() != nil {
						yield(types.ModelChunk{}, &types.LimitExceededError{Limit: "MaxWallClock", Value: l.MaxWallClock.Seconds()})
						return
					}
				}
				if callCtx.Err() != nil {
					yield(types.ModelChunk{}, &types.LimitExceededError{Limit: "MaxWallClock", Value: l.MaxWallClock.Seconds()})
					return
				}
				s.charge(usage, p)
				if err := s.afterCharge(l); err != nil {
					yield(types.ModelChunk{}, err)
				}
			}
		}
	}
}

// preToolCall charges one tool call against the tool total and returns the
// wall clock the call has left. Tool calls are budgeting counters, not
// refusals: passing MaxToolCalls only records itself.
func (s *LimitsState) preToolCall(l types.RunLimits, now time.Time) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	if l.MaxToolCalls > 0 {
		s.toolUses++
	}
	remaining := l.MaxWallClock - now.Sub(s.start)
	if remaining <= 0 {
		return 0, &types.LimitExceededError{Limit: "MaxWallClock", Value: now.Sub(s.start).Seconds()}
	}
	return remaining, nil
}

// ToolLimits returns the tool-chain limit step. It charges each call against
// the tool total — consuming a reserved batch slot when the call runs inside
// one — and bounds the call by the wall clock the run has left, so a tool's
// context is cancelled when the run overruns. It never refuses a call for
// the counter: ending the run on MaxToolCalls belongs to the driver.
func ToolLimits(l types.RunLimits, st *LimitsState) types.ToolMiddleware {
	return func(next types.ToolFunc) types.ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (res types.ToolResult, err error) {
			s := st
			if fromCtx, ok := LimitsStateFrom(ctx); ok {
				s = fromCtx
			}
			if !consumeReservation(ctx, s) {
				if _, err := s.preToolCall(l, time.Now()); err != nil {
					return types.ToolResult{}, err
				}
			}
			remaining, err := wallClockLeft(s, l, time.Now())
			if err != nil {
				return types.ToolResult{}, err
			}
			callCtx, cancel := context.WithTimeout(ctx, remaining)
			defer cancel()
			res, err = next(callCtx, call)
			if err != nil && callCtx.Err() != nil && ctx.Err() == nil {
				return types.ToolResult{}, &types.LimitExceededError{Limit: "MaxWallClock", Value: l.MaxWallClock.Seconds()}
			}
			return res, err
		}
	}
}

// wallClockLeft reports the wall clock the run has left without charging
// anything; a call that arrives already past its wall clock is refused.
func wallClockLeft(s *LimitsState, l types.RunLimits, now time.Time) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	remaining := l.MaxWallClock - now.Sub(s.start)
	if remaining <= 0 {
		return 0, &types.LimitExceededError{Limit: "MaxWallClock", Value: now.Sub(s.start).Seconds()}
	}
	return remaining, nil
}

type batchReservation struct {
	mu        sync.Mutex
	used      int
	remaining int
}

type batchKey struct{}

// consumeReservation reports whether ctx carries a batch reservation with a
// slot left, and spends one if it does. A reserved call charges nothing: the
// batch paid for its slots up front.
func consumeReservation(ctx context.Context, s *LimitsState) bool {
	r, ok := ctx.Value(batchKey{}).(*batchReservation)
	if !ok || r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.remaining <= 0 {
		return false
	}
	r.remaining--
	r.used++
	return true
}

// ReserveBatch charges a whole batch against the tool total before any call
// executes. The returned context carries the reservation, so every call the
// batch runs consumes a paid slot instead of charging again; denied and
// suspended calls keep their slots charged. When the batch is refused with
// runtime.ErrBatchOverrun, the caller invokes release and the refusal spends
// nothing.
func (s *LimitsState) ReserveBatch(ctx context.Context, l types.RunLimits, n int) (context.Context, func(), error) {
	s.mu.Lock()
	if l.MaxToolCalls > 0 && n > l.MaxToolCalls-s.toolUses {
		refused := fmt.Errorf("%w: need %d, %d of %d remain", runtime.ErrBatchOverrun, n, l.MaxToolCalls-s.toolUses, l.MaxToolCalls)
		s.mu.Unlock()
		return ctx, func() {}, refused
	}
	s.toolUses += n
	s.mu.Unlock()
	r := &batchReservation{remaining: n}
	release := func() {
		r.mu.Lock()
		r.used = 0
		r.remaining = 0
		r.mu.Unlock()
		s.mu.Lock()
		s.toolUses -= n
		s.mu.Unlock()
	}
	return context.WithValue(ctx, batchKey{}, r), release, nil
}
