package gohan

import (
	"context"
	"fmt"
	"iter"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// burstProvider streams n chunks and a finish as fast as the consumer
// reads them, and counts how often it was called.
type burstProvider struct {
	profile types.ModelProfile
	n       int
	calls   atomic.Int64
}

func (b *burstProvider) Profile() types.ModelProfile { return b.profile }

func (b *burstProvider) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	b.calls.Add(1)
	return func(yield func(types.ModelChunk, error) bool) {
		for i := range b.n {
			if !yield(types.ModelChunk{Kind: types.DeltaText, Delta: fmt.Sprintf("c%d", i)}, nil) {
				return
			}
		}
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func TestStreamBuffer(t *testing.T) {
	req := types.ModelRequest{}

	t.Run("streams.slow-consumer-no-idle-retry", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := &burstProvider{
				n: 200,
				profile: types.ModelProfile{
					Name:         "burst",
					LatencyClass: types.Interactive,
					Timeout:      types.ModelTimeout{FirstChunk: time.Second, Idle: time.Second},
				},
			}
			s := NewModelStream(p)
			var got []types.ModelChunk
			for c, err := range s.Generate(context.Background(), req) {
				if err != nil {
					t.Fatalf("slow consumer hit %v, want completion", err)
				}
				got = append(got, c)
				// The consumer takes one event per Idle; the provider has
				// long finished, so only the buffer paces the read.
				time.Sleep(time.Second)
			}
			if p.calls.Load() != 1 {
				t.Fatalf("model called %d times, want 1", p.calls.Load())
			}
			if len(got) != 201 || got[200].Finish != types.FinishStop {
				t.Fatalf("got %d chunks, want 201 ending in FinishStop", len(got))
			}
			for i, c := range got[:200] {
				if c.Delta != fmt.Sprintf("c%d", i) {
					t.Fatalf("chunk %d = %q, want c%d", i, c.Delta, i)
				}
			}
		})
	})

	t.Run("streams.stream-buffer-bound", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			n := DefaultStreamBuffer + 20
			p := &burstProvider{
				n: n,
				profile: types.ModelProfile{
					Name:         "burst",
					LatencyClass: types.Interactive,
					Timeout:      types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute},
				},
			}
			s := NewModelStream(p)
			next, stop := iter.Pull2(s.Generate(context.Background(), req))
			defer stop()

			// Take the first chunk, then stall: the buffer fills and the
			// provider read blocks on DefaultStreamBuffer + 1.
			if _, err, ok := next(); err != nil || !ok {
				t.Fatalf("first next = (%v, %v), want a chunk", err, ok)
			}
			synctest.Wait()
			if got := s.BufferFull(); got != 1 {
				t.Fatalf("%s = %d, want 1 after stalling", MetricStreamBufferFull, got)
			}

			var got []types.ModelChunk
			for {
				c, err, ok := next()
				if err != nil {
					t.Fatalf("drain err = %v", err)
				}
				if !ok {
					break
				}
				got = append(got, c)
			}
			// The first chunk was taken before the stall; the drain
			// returns the remaining n-1 chunks plus the finish.
			if len(got) != n || got[len(got)-1].Finish != types.FinishStop {
				t.Fatalf("got %d chunks, want %d ending in FinishStop", len(got), n)
			}
			for i, c := range got[:n-1] {
				if c.Delta != fmt.Sprintf("c%d", i+1) {
					t.Fatalf("chunk %d = %q, want c%d", i, c.Delta, i+1)
				}
			}
		})
	})
}
