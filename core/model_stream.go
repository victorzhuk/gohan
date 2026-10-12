package gohan

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// defaultTimeouts is the compatibility mirror of the std policy value
// std.LatencyTimeouts: the per-class connect / first-chunk / idle table a
// zero profile timeout falls back to when no WithStreamTimeouts option is
// given. It must not be changed independently of std.LatencyTimeouts.
var defaultTimeouts = map[types.LatencyClass][3]time.Duration{
	types.Interactive: {5 * time.Second, 10 * time.Second, 15 * time.Second},
	types.Agentic:     {10 * time.Second, 30 * time.Second, 60 * time.Second},
	types.Batch:       {30 * time.Second, 2 * time.Minute, 5 * time.Minute},
}

// timeoutOf resolves the effective timeout: an explicit profile timeout
// wins, otherwise the latency-class table, otherwise the mirrored default
// table.
func timeoutOf(tbl map[types.LatencyClass][3]time.Duration, d time.Duration, cls types.LatencyClass, idx int) time.Duration {
	if d > 0 {
		return d
	}
	if tbl == nil {
		tbl = defaultTimeouts
	}
	return tbl[cls][idx]
}

// ModelStream wraps a types.Model with the iterator contract: release on
// early break, prompt return on cancellation, and first-chunk / idle
// timeouts classified per the model spec. The zero value is not usable;
// build one with NewModelStream.
type ModelStream struct {
	model types.Model

	// timeouts is the per-class table a zero profile timeout falls back
	// to; without one a zero timeout stays a timeout of none.
	timeouts map[types.LatencyClass][3]time.Duration

	// buffer is the per-call chunk capacity of the provider hand-off.
	buffer int

	// bufferFull accumulates the blocked provider reads of every call
	// this stream supervised, under MetricStreamBufferFull.
	bufferFull atomic.Int64
}

// StreamOption adjusts the supervision of one model stream.
type StreamOption func(*ModelStream)

// WithStreamTimeouts sets the per-class table a zero profile timeout
// falls back to.
func WithStreamTimeouts(t map[types.LatencyClass][3]time.Duration) StreamOption {
	return func(s *ModelStream) { s.timeouts = t }
}

// WithChunkBuffer sets the per-call chunk capacity of the provider
// hand-off.
func WithChunkBuffer(capacity int) StreamOption {
	return func(s *ModelStream) { s.buffer = capacity }
}

// NewModelStream wraps a model value with stream supervision under the
// packaged defaults: the mirrored latency-class timeout table and the
// mirrored chunk-buffer capacity. It is the frozen v1 constructor; a caller
// that needs option plumbing goes through the unexported newModelStream.
func NewModelStream(m types.Model) *ModelStream {
	return newModelStream(m)
}

// newModelStream wraps a model value with stream supervision. A stream
// without WithStreamTimeouts falls back to the mirrored default table, and
// without WithChunkBuffer to the mirrored default capacity, so an
// un-optioned stream keeps the packaged supervision.
func newModelStream(m types.Model, opts ...StreamOption) *ModelStream {
	s := &ModelStream{model: m}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// BufferFull reports how many provider reads blocked on a full call
// buffer across the calls this stream supervised.
func (s *ModelStream) BufferFull() int64 { return s.bufferFull.Load() }

type pulled struct {
	chunk types.ModelChunk
	err   error
}

// Generate yields the model's chunks under the timeouts of the profile.
// A helper goroutine reads the provider into a bounded StreamBuffer that
// yield drains, so a slow consumer never paces the provider and the idle
// clock measures provider reads only. A first-chunk expiry yields a
// ClassTransient ModelError; an idle expiry after the first chunk yields
// ClassPermanent. Cancellation yields the context error. Breaking out of
// the range releases the provider stream before Generate returns.
func (s *ModelStream) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		profile := s.model.Profile()
		firstChunk := timeoutOf(s.timeouts, profile.Timeout.FirstChunk, profile.LatencyClass, 1)
		idle := timeoutOf(s.timeouts, profile.Timeout.Idle, profile.LatencyClass, 2)

		done := make(chan struct{})
		srcctx, cancel := context.WithCancel(ctx)

		// The iterator returns only after both helpers exited, and the stop
		// signal plus provider cancellation are observable to them before
		// the wait: a provider can never still own its stream reader once
		// Generate returns.
		pumpDone := make(chan struct{})
		readerDone := make(chan struct{})
		defer func() {
			cancel()
			close(done)
			<-pumpDone
			<-readerDone
		}()

		// The pump forwards one provider read at a time and exits as soon
		// as done closes, so the inner iterator's release runs before
		// Generate returns whatever the cause.
		src := make(chan pulled)
		go func() {
			defer close(pumpDone)
			defer close(src)
			for c, err := range s.model.Generate(srcctx, req) {
				select {
				case src <- pulled{c, err}:
				case <-done:
					return
				}
			}
		}()

		buf := NewStreamBuffer(s.buffer)
		buf.onFull = func() { s.bufferFull.Add(1) }

		// The reader owns the idle clock. It resets on each provider
		// read and never while blocked on a full buffer, so consumer
		// pacing can neither hold the clock down nor trip it.
		expired := make(chan error, 1)
		failed := make(chan error, 1)
		go func() {
			defer close(readerDone)
			defer buf.close()
			// A zero effective timeout means no timeout: the clock
			// never arms, and a nil timer's channel never fires.
			var timer *time.Timer
			if firstChunk > 0 {
				timer = time.NewTimer(firstChunk)
			}
			timerC := func() <-chan time.Time {
				if timer == nil {
					return nil
				}
				return timer.C
			}
			defer func() {
				if timer != nil {
					timer.Stop()
				}
			}()
			seenFirst := false
			for {
				select {
				case <-ctx.Done():
					return
				case <-srcctx.Done():
					return
				case <-done:
					return
				case <-timerC():
					cls := types.ClassTransient
					if seenFirst {
						cls = types.ClassPermanent
					}
					select {
					case expired <- &types.ModelError{
						Class:    cls,
						Provider: profile.Name,
						Err:      errors.New("timeout waiting for model chunk"),
					}:
					case <-done:
					}
					return
				case p, ok := <-src:
					if !ok {
						return
					}
					// A chunk that arrived after the timer fired while the
					// reader was blocked sending discards the stale tick
					// instead of timing out a healthy provider.
					if timer != nil && !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					// A terminal provider read outranks the queued
					// advisory chunks: it is reported without waiting
					// behind them.
					if p.err != nil {
						select {
						case failed <- p.err:
						case <-done:
						}
						return
					}
					seenFirst = true
					if !buf.send(p, done) {
						return
					}
					// Held down through a blocked send so a full
					// buffer cannot trip the idle clock.
					if timer != nil {
						timer.Reset(idle)
					}
				}
			}
		}()

		delivered := false
		for {
			select {
			case <-ctx.Done():
				yield(types.ModelChunk{}, ctx.Err())
				return
			case p, ok := <-buf.ch:
				if !ok {
					<-readerDone
					// A closed buffer joins the reader and then
					// reports its verdict: provider failure, expiry,
					// cancellation, and only then exhaustion.
					select {
					case err := <-failed:
						yield(types.ModelChunk{}, err)
						return
					default:
					}
					select {
					case err := <-expired:
						yield(types.ModelChunk{}, err)
						return
					default:
					}
					if err := ctx.Err(); err != nil {
						yield(types.ModelChunk{}, err)
						return
					}
					return
				}
				// After the first chunk is out, a terminal verdict
				// jumps the chunks still buffered ahead of it.
				if delivered {
					select {
					case err := <-failed:
						yield(types.ModelChunk{}, err)
						return
					case err := <-expired:
						yield(types.ModelChunk{}, err)
						return
					default:
					}
				}
				delivered = true
				if !yield(p.chunk, nil) {
					return
				}
			}
		}
	}
}
