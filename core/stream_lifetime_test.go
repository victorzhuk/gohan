package gohan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// detRT emits pre deltas on its first step, then blocks on the gate until
// the test releases it, emits one more delta and finishes. Every block
// waits on a channel so the test never depends on a timer.
type detRT struct {
	mu      sync.Mutex
	entered chan struct{}
	gate    chan struct{}
	pre     int
	calls   int
}

func (r *detRT) Name() string                         { return "det.test" }
func (r *detRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *detRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *detRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		evs := make([]types.Event, r.pre)
		for i := range evs {
			evs[i] = types.TextDelta{Delta: fmt.Sprintf("d%d", i)}
		}
		if r.entered != nil {
			close(r.entered)
		}
		return st, evs, runtime.Continue, nil
	}
	if r.gate != nil {
		<-r.gate
	}
	if call == 2 {
		return st, []types.Event{types.TextDelta{Delta: "late"}}, runtime.Continue, nil
	}
	return st, nil, runtime.DoneStatus, nil
}

// stallRT blocks inside the step until the context is cancelled, so the
// test observes the cancellation reaching the run mid-model-call.
type stallRT struct {
	once    sync.Once
	entered chan struct{}
}

func (r *stallRT) Name() string                         { return "stall.test" }
func (r *stallRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *stallRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *stallRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	if r.entered != nil {
		r.once.Do(func() { close(r.entered) })
	}
	<-ctx.Done()
	return st, nil, runtime.Continue, ctx.Err()
}

func streamSetup(t *testing.T, rt runtime.Runtime, opts ...ConversationOption) *conversation {
	t.Helper()
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack.stores = stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
	conv, err := NewConversation(stack, "chat", rt, append([]ConversationOption{
		WithConversationRuns(runs),
		WithConversationEventLog(events),
	}, opts...)...)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}
	return conv.(*conversation)
}

func liveRunID(c *conversation, sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.live[sessionID]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStreamLifetime(t *testing.T) {
	t.Run("streams.disconnect-during-model-call", func(t *testing.T) {
		rt := &stallRT{entered: make(chan struct{})}
		conv := streamSetup(t, rt)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		res := make(chan streamResult, 1)
		go func() { res <- collectStream(conv.Send(principalCtx(ctx), "s1", userMsg("hi"))) }()
		<-rt.entered
		cancel()
		got := <-res
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", got.err)
		}
		for _, e := range got.evs {
			if _, tool := e.(types.ToolStarted); tool {
				t.Fatalf("tool started after disconnect: %+v", e)
			}
		}
	})

	t.Run("streams.detached-reconnect", func(t *testing.T) {
		rt := &detRT{entered: make(chan struct{}), gate: make(chan struct{}), pre: 3}
		conv := streamSetup(t, rt, Detached())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := make(chan streamResult, 1)
		go func() { start <- collectStream(conv.Send(principalCtx(ctx), "s1", userMsg("hi"))) }()
		got := <-start
		if got.err != nil || len(got.evs) != 0 {
			t.Fatalf("detached Send = %+v, want no events and no error", got)
		}
		runID := liveRunID(conv, "s1")
		if runID == "" {
			t.Fatal("detached run not tracked")
		}
		<-rt.entered
		waitFor(t, "first three events recorded", func() bool {
			return lenEvents(t, conv, runID) >= 3
		})

		live := make(chan types.Event, 8)
		done := make(chan error, 1)
		go func() {
			for e, err := range conv.Attach(ctx, runID, 2) {
				if err != nil {
					done <- err
					return
				}
				live <- e
			}
			done <- nil
		}()

		var seen []types.Event
		readN := func(n int) {
			for len(seen) < n {
				select {
				case e := <-live:
					seen = append(seen, e)
					continue
				default:
				}
				select {
				case e := <-live:
					seen = append(seen, e)
				case err := <-done:
					// The terminal event may have landed in live at the
					// same moment done fired; drain live before ending.
					for {
						select {
						case e := <-live:
							seen = append(seen, e)
							continue
						default:
						}
						break
					}
					if len(seen) < n {
						t.Fatalf("attach ended after %d events: %v", len(seen), err)
					}
					return
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out after %d events", len(seen))
				}
			}
		}
		readN(1)
		if _, ok := seen[0].(types.TextDelta); !ok || seen[0].(types.TextDelta).Delta != "d2" {
			t.Fatalf("first catch-up event = %+v, want the seq 3 delta", seen[0])
		}
		close(rt.gate)
		readN(3)
		select {
		case e := <-live:
			t.Fatalf("unexpected event after Done: %+v", e)
		case err := <-done:
			if err != nil {
				t.Fatalf("attach error: %v", err)
			}
		}
		if _, isDone := seen[2].(types.Done); !isDone {
			t.Fatalf("last event = %+v, want Done", seen[2])
		}
	})

	t.Run("streams.detached-requires-log", func(t *testing.T) {
		stack, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		runs := stores.NewMemoryRuns()
		_, err = NewConversation(stack, "chat", &detRT{},
			WithConversationRuns(runs), Detached())
		if !errors.Is(err, errDetachedNoLog) {
			t.Fatalf("err = %v, want errDetachedNoLog", err)
		}
	})
}
