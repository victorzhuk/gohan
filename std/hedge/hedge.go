// Package hedge fans a second model attempt out beside a slow primary: a
// timer armed on the primary's time to first body chunk, a second attempt on
// the router's next target, the first body chunk wins and the loser is
// cancelled and charged through the run's ledger.
package hedge

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	// DefaultBudget is the hedge rate the policy carries when Budget is unset.
	DefaultBudget = 0.05
	// DefaultAfterFloor is the lower bound on the resolved timer when After
	// comes from the latency source.
	DefaultAfterFloor = 800 * time.Millisecond
)

// Metric names this package records.
const (
	MetricBudgetExhausted = "gohan.hedge.budget_exhausted"
	MetricRefused         = "gohan.hedge.refused"
	MetricLoserUsage      = "gohan.usage.hedge_loser"
)

// HedgePolicy configures the hedge step.
type HedgePolicy struct {
	// After is the time to first body chunk the primary gets before a
	// second attempt starts. Zero reads the latency source's rolling P90,
	// clamped to DefaultAfterFloor.
	After time.Duration
	// Targets is the router's ordered candidate supplier, read live at
	// admission time. The first candidate whose profile name differs from
	// the in-flight endpoint's is the hedge target.
	Targets func() []types.Model
	// Budget is a token bucket of hedges per base call, capped at one
	// token, refilled by this rate on each base model call.
	Budget float64
	// MaxExtra bounds the extra attempts a single call may start.
	MaxExtra int
	// BudgetByTag resolves the refill rate from the run's cost tags. When
	// nil, Budget applies. A zero rate never hedges.
	BudgetByTag func(types.CostTags) float64
}

// Option configures the hedge step's wiring.
type Option func(*step)

// WithLedger attaches the run's ledger and pricing, so the losing attempt's
// usage is charged against the run's cost.
func WithLedger(st *chains.LimitsState, p types.Pricing) Option {
	return func(s *step) { s.ledger, s.pricing = st, p }
}

// WithLatency attaches the source the default After reads: the endpoint's
// rolling P90 time to first chunk.
func WithLatency(fn func(endpoint string) time.Duration) Option {
	return func(s *step) { s.latency = fn }
}

// WithTelemetry attaches the sink the refusal and budget counters emit on.
func WithTelemetry(tel types.Telemetry) Option {
	return func(s *step) { s.tel = tel }
}

// Sink records a hedged call's outcome for the driver; it is the core
// types.HedgeSink.
type Sink = types.HedgeSink

// WithSink returns a context carrying the outcome sink.
func WithSink(ctx context.Context, s *Sink) context.Context {
	return types.WithHedgeSink(ctx, s)
}

func sinkFrom(ctx context.Context) *Sink {
	s, _ := types.HedgeSinkFrom(ctx)
	return s
}

type bucket struct {
	tokens float64
}

type step struct {
	policy  HedgePolicy
	ledger  *chains.LimitsState
	pricing types.Pricing
	latency func(endpoint string) time.Duration
	tel     types.Telemetry

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New returns the hedge step as model middleware. The step is inert when
// the target supplier offers no endpoint other than the one in flight.
func New(p HedgePolicy, opts ...Option) types.ModelMiddleware {
	if p.Budget <= 0 && p.BudgetByTag == nil {
		p.Budget = DefaultBudget
	}
	s := &step{policy: p, buckets: make(map[string]*bucket)}
	for _, opt := range opts {
		opt(s)
	}
	return s.middleware
}

func (s *step) middleware(next types.ModelFunc) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			if sink := sinkFrom(ctx); sink != nil {
				sink.Record(1, "")
			}
			targets := []types.Model(nil)
			if s.policy.Targets != nil {
				targets = s.policy.Targets()
			}
			if len(targets) == 0 {
				forward(next(ctx, req), yield)
				return
			}
			inflight := targets[0].Profile()
			if reason, ok := ineligible(req, inflight); ok {
				s.refuse(ctx, reason)
				forward(next(ctx, req), yield)
				return
			}
			extra := s.targets(targets, inflight.Name)
			if len(extra) == 0 {
				forward(next(ctx, req), yield)
				return
			}
			if !s.spend(ctx) {
				s.count(ctx, MetricBudgetExhausted)
				forward(next(ctx, req), yield)
				return
			}
			s.run(ctx, req, next, extra, yield)
		}
	}
}

// ineligible reports the permanent refusal reason for a call the hedge may
// not fan out: a provider-executed tool, a non-streaming profile or a batch
// latency class.
func ineligible(req types.ModelRequest, p types.ModelProfile) (string, bool) {
	for _, t := range req.Tools {
		if t.Executor == types.ByProvider {
			return "provider_tool", true
		}
	}
	if !p.Caps.Streaming {
		return "no_streaming", true
	}
	if p.LatencyClass == types.Batch {
		return "batch", true
	}
	return "", false
}

// targets filters the candidate list: never the in-flight endpoint's name.
func (s *step) targets(candidates []types.Model, inflight string) []types.Model {
	var out []types.Model
	for _, m := range candidates {
		if m.Profile().Name != inflight {
			out = append(out, m)
			if s.policy.MaxExtra > 0 && len(out) >= s.policy.MaxExtra {
				break
			}
		}
	}
	return out
}

// spend takes one token from the run's bucket, refilling by the resolved
// rate and capping at one.
func (s *step) spend(ctx context.Context) bool {
	info, ok := types.RunInfoFrom(ctx)
	if !ok || info.RunID == "" {
		return true
	}
	rate := s.policy.Budget
	if s.policy.BudgetByTag != nil {
		rate = s.policy.BudgetByTag(info.CostTags)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[info.RunID]
	if b == nil {
		b = &bucket{tokens: 1}
		s.buckets[info.RunID] = b
	}
	if rate <= 0 {
		return false
	}
	if b.tokens < 1 {
		b.tokens += rate
	}
	if b.tokens > 1 {
		b.tokens = 1
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens -= 1
	return true
}

func (s *step) refuse(ctx context.Context, reason string) {
	s.count(ctx, MetricRefused, types.Attr{Key: "reason", Value: reason}, types.Attr{Key: "class", Value: "permanent"})
}

func (s *step) count(ctx context.Context, name string, attrs ...types.Attr) {
	if s.tel == nil {
		return
	}
	s.tel.Count(ctx, name, 1, attrs...)
}

type item struct {
	chunk types.ModelChunk
	err   error
}

// run streams the primary and, if no body chunk arrives within the timer,
// a secondary attempt. The first body chunk wins; the other attempt's
// context is cancelled and its reported usage charged once.
func (s *step) run(ctx context.Context, req types.ModelRequest, next types.ModelFunc, extra []types.Model, yield func(types.ModelChunk, error) bool) {
	inflight := extra[0].Profile().Name
	if sink := sinkFrom(ctx); sink != nil {
		sink.Record(1, "")
	}

	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	pch := make(chan item, 8)
	go stream(next(pctx, req), pch)

	timerC := s.arm(extra)
	var (
		secCancel      context.CancelFunc
		sch            chan item
		buffered       []item
		winner         chan item
		winnerEndpoint string
		secUsed        bool
	)
	defer func() {
		if secCancel != nil {
			secCancel()
		}
		if sink := sinkFrom(ctx); sink != nil {
			n := 1
			if secUsed {
				n = 2
			}
			sink.Record(n, winnerEndpoint)
		}
	}()

	loserUsage := func(it item) {
		if it.chunk.Usage != nil && secUsed {
			s.chargeLoser(ctx, *it.chunk.Usage)
		}
	}

	for {
		if winner == nil {
			select {
			case <-ctx.Done():
				return
			case it, ok := <-pch:
				if !ok {
					if sch == nil {
						flush(buffered, yield)
						return
					}
					pch = nil
					continue
				}
				if body(it.chunk) {
					winner = pch
					winnerEndpoint = inflight
					pcancel()
					if secCancel != nil {
						secCancel()
						go s.drainLoser(sch)
					}
					if !flush(append(buffered, it), yield) {
						return
					}
				} else {
					loserUsage(it)
					buffered = append(buffered, it)
				}
			case it, ok := <-sch:
				if !ok {
					sch = nil
					continue
				}
				if body(it.chunk) {
					winner = sch
					winnerEndpoint = extra[0].Profile().Name
					pcancel()
					secCancel()
					for _, b := range buffered {
						if b.chunk.Usage != nil {
							s.chargeLoser(ctx, *b.chunk.Usage)
						}
					}
					go s.drainLoser(pch)
					if !yield(it.chunk, it.err) {
						return
					}
				} else {
					loserUsage(it)
				}
			case <-timerC:
				if !secUsed {
					secUsed = true
					sctx, scancel := context.WithCancel(ctx)
					secCancel = scancel
					sch = make(chan item, 8)
					go stream(extra[0].Generate(sctx, req), sch)
					if sink := sinkFrom(ctx); sink != nil {
						sink.Record(2, "")
					}
				}
				timerC = nil
			}
			continue
		}
		it, ok := <-winner
		if !ok {
			return
		}
		if !yield(it.chunk, it.err) || it.err != nil {
			return
		}
	}
}

// arm resolves the timer for the primary attempt and returns its channel;
// a zero-duration policy with no latency source takes the floor.
func (s *step) arm(extra []types.Model) <-chan time.Time {
	return time.NewTimer(s.resolveAfter(extra)).C
}

// drainLoser reads a cancelled attempt's remaining chunks so usage it
// already reported is still charged; nothing is forwarded.
func (s *step) drainLoser(ch chan item) {
	for it := range ch {
		if it.chunk.Usage != nil {
			s.chargeLoser(context.Background(), *it.chunk.Usage)
		}
	}
}

func flush(items []item, yield func(types.ModelChunk, error) bool) bool {
	for _, it := range items {
		if it.err != nil {
			if !yield(types.ModelChunk{}, it.err) {
				return false
			}
			continue
		}
		if !yield(it.chunk, nil) {
			return false
		}
	}
	return true
}

func stream(seq iter.Seq2[types.ModelChunk, error], out chan<- item) {
	defer close(out)
	for c, err := range seq {
		out <- item{chunk: c, err: err}
		if err != nil {
			return
		}
	}
}

func body(c types.ModelChunk) bool {
	return c.Kind == types.DeltaText || c.Kind == types.DeltaToolArgs || c.ToolUse != nil
}

func (s *step) resolveAfter(extra []types.Model) time.Duration {
	if s.policy.After > 0 {
		return s.policy.After
	}
	if s.latency != nil && len(extra) > 0 {
		if d := s.latency(extra[0].Profile().Name); d > 0 {
			if d < DefaultAfterFloor {
				return DefaultAfterFloor
			}
			return d
		}
	}
	return DefaultAfterFloor
}

func (s *step) chargeLoser(ctx context.Context, u types.Usage) {
	if s.ledger == nil {
		return
	}
	s.ledger.Charge(u, s.pricing)
	if s.tel != nil {
		_, done := s.tel.StartSpan(ctx, "model.hedge.loser", types.Attr{Key: MetricLoserUsage, Value: true})
		done()
	}
}

func forward(seq iter.Seq2[types.ModelChunk, error], yield func(types.ModelChunk, error) bool) {
	for c, err := range seq {
		if !yield(c, err) {
			return
		}
		if err != nil {
			return
		}
	}
}
