package gohan

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type streamEvent struct {
	chunk types.ModelChunk
	err   error
}

// fakeProvider replays events, then holds the read open until ctx is done.
// release closes when the provider stream is torn down, whatever the cause.
type fakeProvider struct {
	profile  types.ModelProfile
	events   []streamEvent
	mu       sync.Mutex
	released bool
	release  chan struct{}
	once     sync.Once
}

func newFakeProvider(timeout types.ModelTimeout, events ...streamEvent) *fakeProvider {
	return &fakeProvider{
		profile: types.ModelProfile{
			Name:         "fake",
			LatencyClass: types.Interactive,
			Timeout:      timeout,
		},
		release: make(chan struct{}),
		events:  events,
	}
}

func (f *fakeProvider) Profile() types.ModelProfile { return f.profile }

func (f *fakeProvider) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		defer f.markReleased()
		for _, ev := range f.events {
			if !yield(ev.chunk, ev.err) {
				return
			}
			if ev.err != nil || ev.chunk.Finish != "" {
				return
			}
		}
		<-ctx.Done()
		yield(types.ModelChunk{}, ctx.Err())
	}
}

func (f *fakeProvider) markReleased() {
	f.mu.Lock()
	f.released = true
	f.mu.Unlock()
	f.once.Do(func() { close(f.release) })
}

func (f *fakeProvider) wasReleased() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.released
}

func textChunk(s string) streamEvent {
	return streamEvent{chunk: types.ModelChunk{Kind: types.DeltaText, Delta: s}}
}

func finishChunk() streamEvent {
	return streamEvent{chunk: types.ModelChunk{Finish: types.FinishStop}}
}

func collect(t *testing.T, s *ModelStream, ctx context.Context, req types.ModelRequest) ([]types.ModelChunk, error) {
	t.Helper()
	var chunks []types.ModelChunk
	for c, err := range s.Generate(ctx, req) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}

func TestModelStream(t *testing.T) {
	req := types.ModelRequest{}

	t.Run("model.early-break-releases", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := newFakeProvider(types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute},
				textChunk("a"), textChunk("b"))
			s := NewModelStream(p)
			n := 0
			for _, err := range s.Generate(context.Background(), req) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				n++
				if n == 1 {
					break
				}
			}
			// The iterator must return only after the provider pump and the
			// timed reader have exited; no synctest.Wait may be needed to
			// observe the release.
			if !p.wasReleased() {
				t.Fatal("provider stream not released after early break")
			}
			select {
			case <-p.release:
			default:
				t.Fatal("provider stream not released after early break")
			}
			if n != 1 {
				t.Fatalf("got %d chunks, want 1", n)
			}
		})
	})

	t.Run("model.cancel-returns-promptly", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := newFakeProvider(types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute})
			s := NewModelStream(p)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := collect(t, s, ctx, req)
				done <- err
			}()
			synctest.Wait()
			cancel()
			err := <-done
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
			synctest.Wait()
			if !p.wasReleased() {
				t.Fatal("provider stream not released after cancellation")
			}
		})
	})

	t.Run("model.first-chunk-timeout-transient", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := newFakeProvider(types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute})
			s := NewModelStream(p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := collect(t, s, ctx, req)
			me, ok := errors.AsType[*types.ModelError](err)
			if !ok {
				t.Fatalf("got %v, want *types.ModelError", err)
			}
			if me.Class != types.ClassTransient {
				t.Fatalf("got class %d, want ClassTransient", me.Class)
			}
		})
	})

	t.Run("model.idle-timeout-permanent", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := newFakeProvider(types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute},
				textChunk("a"))
			s := NewModelStream(p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := collect(t, s, ctx, req)
			me, ok := errors.AsType[*types.ModelError](err)
			if !ok {
				t.Fatalf("got %v, want *types.ModelError", err)
			}
			if me.Class != types.ClassPermanent {
				t.Fatalf("got class %d, want ClassPermanent", me.Class)
			}
		})
	})

	t.Run("normal drain", func(t *testing.T) {
		p := newFakeProvider(types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute},
			textChunk("a"), textChunk("b"), finishChunk())
		s := NewModelStream(p)
		chunks, err := collect(t, s, context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) != 3 || chunks[2].Finish != types.FinishStop {
			t.Fatalf("got %d chunks, want 3 ending in FinishStop", len(chunks))
		}
	})

	t.Run("mid-stream provider failure", func(t *testing.T) {
		want := &types.ModelError{Class: types.ClassRateLimited, Provider: "fake", Err: errors.New("boom")}
		p := newFakeProvider(types.ModelTimeout{FirstChunk: time.Minute, Idle: time.Minute},
			textChunk("a"), streamEvent{err: want})
		s := NewModelStream(p)
		_, err := collect(t, s, context.Background(), req)
		me, ok := errors.AsType[*types.ModelError](err)
		if !ok {
			t.Fatalf("got %v, want *types.ModelError", err)
		}
		if me.Class != types.ClassRateLimited {
			t.Fatalf("got class %d, want ClassRateLimited", me.Class)
		}
	})
}
