package chains

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// LimitsState accumulates what one run's limit steps spend and warn. A
// run keeps one state across every call its chains make; the tree
// accounting projects the root's Done.Cost from it.
type LimitsState struct {
	mu       sync.Mutex
	cost     float64
	start    time.Time
	turns    int
	toolUses int
	warned   bool
	warnings []types.LimitWarning
	tree     *treeSpend
}

// treeSpend is the cost the whole run tree charged; a tree's MaxCost is
// one limit on one shared total, so it lives apart from the per-run
// states that add to it.
type treeSpend struct {
	mu   sync.Mutex
	cost float64
}

func (t *treeSpend) add(v float64) {
	t.mu.Lock()
	t.cost += v
	t.mu.Unlock()
}

func (t *treeSpend) get() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cost
}

// Branch returns a state that charges the same tree budget as s: every
// descendant's spend accumulates into one shared total, and the cost
// checks on a branch see that total against the hub's MaxCost. Branching
// runs under the parent's lock, so two goroutines starting runs under one
// tree share one tree spend, never two.
func (s *LimitsState) Branch() *LimitsState {
	s.mu.Lock()
	t := s.tree
	if t == nil {
		t = &treeSpend{cost: s.cost}
		s.tree = t
	}
	s.mu.Unlock()
	return &LimitsState{tree: t}
}

// TreeCost reports the spend the whole run tree charged so far.
func (s *LimitsState) TreeCost() float64 {
	s.mu.Lock()
	t := s.tree
	cost := s.cost
	s.mu.Unlock()
	if t != nil {
		return t.get()
	}
	return cost
}

// NewLimitsState returns the accumulator one run shares across its
// chain steps.
func NewLimitsState() *LimitsState { return &LimitsState{} }

// NewLimitsStateSeeded returns an accumulator whose cost starts at seed,
// the spend the persisted run record already reports. The counters a
// checkpoint does not carry start at zero: only the spent cost survives
// the store record.
func NewLimitsStateSeeded(seed float64) *LimitsState {
	return &LimitsState{cost: seed}
}

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

// AccountingSnapshot is a copy of one ledger's counters, free of its lock
// and of the tree pointer: exactly what resume stores.
type AccountingSnapshot struct {
	Cost     float64
	Start    time.Time
	Turns    int
	ToolUses int
	Warnings []types.LimitWarning
	TreeCost float64
}

// Snapshot reports the ledger's counters without mutating them.
func (s *LimitsState) Snapshot() AccountingSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := AccountingSnapshot{
		Cost:     s.cost,
		Start:    s.start,
		Turns:    s.turns,
		ToolUses: s.toolUses,
		Warnings: append([]types.LimitWarning(nil), s.warnings...),
	}
	if s.tree != nil {
		snap.TreeCost = s.tree.get()
	} else {
		snap.TreeCost = s.cost
	}
	return snap
}

// charge prices one usage record and adds it to the run's cost. Cache
// writes price at CacheWrite, or at CachedInput when the profile
// socializes them.
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
	if s.tree != nil {
		s.tree.add(spent)
	}
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

// preCall applies the checks that run before the wrapped call: the hard
// cost overrun aborts before the next model call, and turns and wall
// clock are charged or expired here. It returns the context deadline the
// call must run under.
func (s *LimitsState) preCall(l types.RunLimits, now time.Time) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	if l.MaxCost > 0 {
		cost := s.cost
		if s.tree != nil {
			cost = s.tree.get()
		}
		if cost > l.MaxCost {
			return 0, &types.LimitExceededError{Limit: "MaxCost", Value: cost}
		}
	}
	if l.MaxTurns > 0 {
		s.turns++
		if s.turns > l.MaxTurns {
			return 0, &types.LimitExceededError{Limit: "MaxTurns", Value: float64(s.turns)}
		}
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
	cost := s.TreeCost()
	if cost >= l.MaxCost*l.SoftRatio {
		s.warn(types.LimitWarning{Limit: "MaxCost", Ratio: cost / l.MaxCost})
	}
	if cost > l.MaxCost {
		return &types.LimitExceededError{Limit: "MaxCost", Value: cost}
	}
	return nil
}

// Limits returns the model-chain limit step. It counts turns, charges the
// message's usage at message end through the profile's pricing, warns
// once at the soft ratio and aborts on a hard cost overrun or an expired
// wall clock. Fallback and retry run inside it, so a chunk is charged
// once no matter how many endpoints it took to serve.
func Limits(l types.RunLimits, p types.Pricing, st *LimitsState) types.ModelMiddleware {
	return func(next types.ModelFunc) types.ModelFunc {
		return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
			return func(yield func(types.ModelChunk, error) bool) {
				if s, ok := LimitsStateFrom(ctx); ok {
					st = s
				}
				remaining, err := st.preCall(l, time.Now())
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
				st.charge(usage, p)
				if err := st.afterCharge(l); err != nil {
					yield(types.ModelChunk{}, err)
				}
			}
		}
	}
}

// preToolCall charges one tool call against MaxToolCalls and returns the
// wall clock the call has left.
func (s *LimitsState) preToolCall(l types.RunLimits, now time.Time) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	if l.MaxToolCalls > 0 {
		s.toolUses++
		if s.toolUses > l.MaxToolCalls {
			return 0, &types.LimitExceededError{Limit: "MaxToolCalls", Value: float64(s.toolUses)}
		}
	}
	remaining := l.MaxWallClock - now.Sub(s.start)
	if remaining <= 0 {
		return 0, &types.LimitExceededError{Limit: "MaxWallClock", Value: now.Sub(s.start).Seconds()}
	}
	return remaining, nil
}

// ToolLimits returns the tool-chain limit step. It counts tool calls and
// bounds each call by the wall clock the run has left, so a tool's
// context is cancelled when the run overruns.
func ToolLimits(l types.RunLimits, st *LimitsState) types.ToolMiddleware {
	return func(next ToolFunc) ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (res types.ToolResult, err error) {
			if s, ok := LimitsStateFrom(ctx); ok {
				st = s
			}
			remaining, err := st.preToolCall(l, time.Now())
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

// Charge prices one usage record and adds it to the run's cost, with the
// same cache-write pricing rules as the model-chain limit step, and reports
// the new total.
func (s *LimitsState) Charge(u types.Usage, p types.Pricing) float64 {
	return s.charge(u, p)
}

// ChargeModelCall records one model turn and returns the wall clock the call
// has left. Turns are budgeting counters, not refusals: passing MaxTurns
// only records itself, and the driver ends the run with Done(StopLimit) at
// its effect boundary. Only a hard cost overrun or an expired wall clock
// aborts before the call.
func (s *LimitsState) ChargeModelCall(l types.RunLimits, now time.Time) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now
	}
	if l.MaxCost > 0 {
		cost := s.cost
		if s.tree != nil {
			cost = s.tree.get()
		}
		if cost > l.MaxCost {
			return 0, &types.LimitExceededError{Limit: "MaxCost", Value: cost}
		}
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

// AfterCharge applies the post-spend checks: the soft-ratio warning once,
// then the hard cost abort.
func (s *LimitsState) AfterCharge(l types.RunLimits) error {
	return s.afterCharge(l)
}

// ChargeToolCall records one tool call against the tool total and returns
// the wall clock the call has left. Tool calls are budgeting counters, not
// refusals: passing MaxToolCalls only records itself, and ending the run on
// MaxToolCalls belongs to the driver.
func (s *LimitsState) ChargeToolCall(l types.RunLimits, now time.Time) (time.Duration, error) {
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

// WallClockLeft reports the wall clock the run has left without charging
// anything; a call that arrives already past its wall clock is refused.
func (s *LimitsState) WallClockLeft(l types.RunLimits, now time.Time) (time.Duration, error) {
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

// ConsumeReservation reports whether ctx carries a batch reservation with a
// slot left, and spends one if it does. A reserved call charges nothing: the
// batch paid for its slots up front.
func (s *LimitsState) ConsumeReservation(ctx context.Context) bool {
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
// types.ErrBatchOverrun, the caller invokes the refund and the refusal
// spends nothing.
func (s *LimitsState) ReserveBatch(ctx context.Context, l types.RunLimits, n int) (context.Context, func(), error) {
	s.mu.Lock()
	if l.MaxToolCalls > 0 && n > l.MaxToolCalls-s.toolUses {
		refused := fmt.Errorf("%w: need %d, %d of %d remain", types.ErrBatchOverrun, n, l.MaxToolCalls-s.toolUses, l.MaxToolCalls)
		s.mu.Unlock()
		return ctx, func() {}, refused
	}
	s.toolUses += n
	s.mu.Unlock()
	r := &batchReservation{remaining: n}
	refund := func() {
		r.mu.Lock()
		r.used = 0
		r.remaining = 0
		r.mu.Unlock()
		s.mu.Lock()
		s.toolUses -= n
		s.mu.Unlock()
	}
	return context.WithValue(ctx, batchKey{}, r), refund, nil
}
