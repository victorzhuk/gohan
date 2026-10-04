package gohan

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// defaultTimeouts maps a profile's latency class onto the connect /
// first-chunk / idle defaults a zero ModelTimeout falls back to.
var defaultTimeouts = map[types.LatencyClass][3]time.Duration{
	types.Interactive: {5 * time.Second, 10 * time.Second, 15 * time.Second},
	types.Agentic:     {10 * time.Second, 30 * time.Second, 60 * time.Second},
	types.Batch:       {30 * time.Second, 2 * time.Minute, 5 * time.Minute},
}

func timeoutOf(d time.Duration, cls types.LatencyClass, idx int) time.Duration {
	if d > 0 {
		return d
	}
	return defaultTimeouts[cls][idx]
}

// ModelStream wraps a types.Model with the iterator contract: release on
// early break, prompt return on cancellation, and first-chunk / idle
// timeouts classified per the model spec. The zero value is not usable;
// build one with NewModelStream.
type ModelStream struct {
	model types.Model

	// bufferFull accumulates the blocked provider reads of every call
	// this stream supervised, under MetricStreamBufferFull.
	bufferFull atomic.Int64
}

// NewModelStream wraps a model value with stream supervision.
func NewModelStream(m types.Model) *ModelStream {
	return &ModelStream{model: m}
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
		firstChunk := timeoutOf(profile.Timeout.FirstChunk, profile.LatencyClass, 1)
		idle := timeoutOf(profile.Timeout.Idle, profile.LatencyClass, 2)

		done := make(chan struct{})
		defer close(done)

		// srcctx is cancelled when Generate's iterator exits for any
		// reason, releasing a provider that parked waiting for the next
		// call after its events ran out.
		srcctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// The pump forwards one provider read at a time and exits as soon
		// as done closes, so the inner iterator's release runs before
		// Generate returns whatever the cause.
		src := make(chan pulled)
		go func() {
			defer close(src)
			for c, err := range s.model.Generate(srcctx, req) {
				select {
				case src <- pulled{c, err}:
				case <-done:
					return
				}
			}
		}()

		buf := NewStreamBuffer(DefaultStreamBuffer)
		buf.onFull = func() { s.bufferFull.Add(1) }

		// The reader owns the idle clock. It resets on each provider read
		// and never while blocked on a full buffer, so consumer pacing can
		// neither hold the clock down nor trip it.
		expired := make(chan error, 1)
		go func() {
			defer buf.close()
			timer := time.NewTimer(firstChunk)
			defer timer.Stop()
			seenFirst := false
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
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
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					seenFirst = true
					if !buf.send(p, done) {
						return
					}
					timer.Reset(idle)
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				yield(types.ModelChunk{}, ctx.Err())
				return
			case err := <-expired:
				yield(types.ModelChunk{}, err)
				return
			case p, ok := <-buf.ch:
				if !ok {
					return
				}
				if p.err != nil {
					yield(types.ModelChunk{}, p.err)
					return
				}
				if !yield(p.chunk, nil) {
					return
				}
			}
		}
	}
}
