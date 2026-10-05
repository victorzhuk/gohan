package gohan

import (
	"context"
	"iter"
	"sync"
	"sync/atomic"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// MetricConsumerStalled names the counter a transport reads to report a
// consumer stall, labelled with the action the run took
// (streams, *Slow consumers* rule 3).
const MetricConsumerStalled = "gohan.stream.consumer_stalled"

// StallAction says what a run does when its consumer takes no event for
// RunLimits.ConsumerStall. The zero action is preemption.
type StallAction uint8

const (
	// StallPreempt suspends the run Preempted at its next safe point and
	// releases the lease; the client resumes with Resume(token, Continue()).
	StallPreempt StallAction = iota
	// StallDetach continues the run as Detached; the client reattaches
	// through Attach from its last Seq.
	StallDetach
)

// String renders the metric label for the action: "preempt" | "detach".
func (a StallAction) String() string {
	if a == StallDetach {
		return "detach"
	}
	return "preempt"
}

// StallGuard enforces RunLimits.ConsumerStall over one run's event stream.
// The run's events pass through Watch, which measures the interval between
// the consumer's takes; a gap past the limit counts the run stalled, counts
// MetricConsumerStalled and acts:
//
//   - StallPreempt cancels the run context, so the drive loop stops at its
//     next safe point and the landed lifecycle suspends the run Preempted
//     through its checkpoint and Runs.Suspend. A runtime safe point can
//     also ask Stalled before the cancellation lands.
//   - StallDetach stops delivering to the consumer and drains the rest of
//     the run into its EventLog, so the run continues and a client
//     reattaches through Attach from its last Seq.
//
// A zero limit disables the guard: Watch returns the run unchanged and the
// stall machinery never runs. The clock comes from ticks when one is set
// (tests drive it from a channel); otherwise the guard measures wall time.
type StallGuard struct {
	limit  time.Duration
	action StallAction
	cancel context.CancelFunc
	ticks  <-chan time.Time

	mu       sync.Mutex
	last     time.Time
	fired    bool
	preempts atomic.Int64
	detaches atomic.Int64
}

// NewStallGuard guards one run at the given limit. cancel cancels the run's
// context when the guard acts on StallPreempt; nil leaves the preemption to
// safe points that poll Stalled. limit <= 0 disables the guard.
func NewStallGuard(limit time.Duration, action StallAction, cancel context.CancelFunc) *StallGuard {
	return &StallGuard{limit: limit, action: action, cancel: cancel}
}

// Stalled reports whether the consumer stall already fired, so a safe point
// can preempt before the run context's cancellation reaches it.
func (g *StallGuard) Stalled() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fired
}

// Action reports the action the guard takes when it fires.
func (g *StallGuard) Action() StallAction {
	if g == nil {
		return StallPreempt
	}
	return g.action
}

// Preempts reports how often MetricConsumerStalled fired with the preempt
// action.
func (g *StallGuard) Preempts() int64 { return g.preempts.Load() }

// Detaches reports how often MetricConsumerStalled fired with the detach
// action.
func (g *StallGuard) Detaches() int64 { return g.detaches.Load() }

// fire records the stall once and acts. It reports whether this call was
// the one that fired the guard.
func (g *StallGuard) fire() bool {
	g.mu.Lock()
	if g.fired {
		g.mu.Unlock()
		return false
	}
	g.fired = true
	g.mu.Unlock()
	if g.action == StallDetach {
		g.detaches.Add(1)
		return true
	}
	g.preempts.Add(1)
	if g.cancel != nil {
		g.cancel()
	}
	return true
}

// take records that the consumer just took an event.
func (g *StallGuard) take() {
	g.mu.Lock()
	g.last = time.Now()
	g.mu.Unlock()
}

type stallTuple struct {
	ev  types.Event
	err error
}

// Watch wraps one run's sequence with the stall measurement. Events still
// reach the consumer in order; a stalled consumer only changes what the
// run does at its next boundary. The watcher goroutine ends when the run
// ends, when the run context is cancelled or when the consumer breaks, so
// no helper outlives the stream.
func (g *StallGuard) Watch(ctx context.Context, run iter.Seq2[types.Event, error]) iter.Seq2[types.Event, error] {
	if g == nil || g.limit <= 0 {
		return run
	}
	return func(yield func(types.Event, error) bool) {
		ch := make(chan stallTuple)
		produced := make(chan struct{})
		gone := make(chan struct{})
		go func() {
			defer close(produced)
			defer close(ch)
			for ev, err := range run {
				t := stallTuple{ev, err}
				select {
				case ch <- t:
				case <-gone:
					return
				}
			}
		}()

		g.take()
		watched := make(chan struct{})
		stop := make(chan struct{})
		detached := make(chan struct{})
		go func() {
			defer close(watched)
			tick := g.ticks
			if tick == nil {
				t := time.NewTicker(g.limit)
				defer t.Stop()
				tick = t.C
			}
			for {
				select {
				case <-produced:
					return
				case <-ctx.Done():
					return
				case <-stop:
					return
				case <-tick:
				}
				g.mu.Lock()
				idle := time.Since(g.last)
				g.mu.Unlock()
				if idle >= g.limit && g.fire() {
					if g.Action() == StallDetach {
						close(detached)
					}
					return
				}
			}
		}()

	loop:
		for {
			select {
			case t, ok := <-ch:
				if !ok {
					break loop
				}
				g.take()
				if !yield(t.ev, t.err) {
					// The consumer walked away. Under StallDetach the run
					// continues into its EventLog; a reattaching client
					// reads the events it missed through Attach.
					close(gone)
					if g.Action() == StallDetach {
						go func() {
							for range ch {
							}
						}()
					}
					break loop
				}
			case <-detached:
				// The guard detached the run while the consumer was
				// still busy: the rest streams only into the log.
				close(gone)
				go func() {
					for range ch {
					}
				}()
				break loop
			}
		}
		close(stop)
		<-watched
	}
}
