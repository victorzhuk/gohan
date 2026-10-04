package gohan

import (
	"context"
	"errors"
	"iter"
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
}

// NewModelStream wraps a model value with stream supervision.
func NewModelStream(m types.Model) *ModelStream {
	return &ModelStream{model: m}
}

type pulled struct {
	chunk types.ModelChunk
	err   error
}

// Generate yields the model's chunks under the timeouts of the profile. A
// first-chunk expiry yields a ClassTransient ModelError; an idle expiry
// after the first chunk yields ClassPermanent. Cancellation yields the
// context error. Breaking out of the range releases the provider stream
// before Generate returns.
func (s *ModelStream) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		profile := s.model.Profile()
		firstChunk := timeoutOf(profile.Timeout.FirstChunk, profile.LatencyClass, 1)
		idle := timeoutOf(profile.Timeout.Idle, profile.LatencyClass, 2)

		// The helper forwards one read at a time and exits as soon as done
		// closes, so the inner iterator's release runs before Generate
		// returns even when the consumer breaks early.
		res := make(chan pulled)
		done := make(chan struct{})
		defer close(done)
		go func() {
			defer close(res)
			for c, err := range s.model.Generate(ctx, req) {
				select {
				case res <- pulled{c, err}:
				case <-done:
					return
				}
			}
		}()

		timer := time.NewTimer(firstChunk)
		defer timer.Stop()
		seenFirst := false
		for {
			select {
			case <-ctx.Done():
				yield(types.ModelChunk{}, ctx.Err())
				return
			case <-timer.C:
				cls := types.ClassTransient
				if seenFirst {
					cls = types.ClassPermanent
				}
				yield(types.ModelChunk{}, &types.ModelError{
					Class:    cls,
					Provider: profile.Name,
					Err:      errors.New("timeout waiting for model chunk"),
				})
				return
			case r, ok := <-res:
				if !ok {
					return
				}
				if r.err != nil {
					yield(types.ModelChunk{}, r.err)
					return
				}
				if !yield(r.chunk, nil) {
					return
				}
				seenFirst = true
				// A stale tick must not masquerade as an idle timeout on
				// the next wait, so drain before reset.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)
			}
		}
	}
}
