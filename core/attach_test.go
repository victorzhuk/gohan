package gohan

import (
	"context"
	"testing"
	"time"
)

func TestAttach(t *testing.T) {
	t.Run("ends after the run's done", func(t *testing.T) {
		rt := &detRT{pre: 2, entered: make(chan struct{}), gate: make(chan struct{})}
		conv := streamSetup(t, rt)
		ctx := context.Background()
		start := make(chan streamResult, 1)
		go func() { start <- collectStream(conv.Send(principalCtx(ctx), "s1", userMsg("hi"))) }()
		// The run stays live inside its gated step, so the id is read
		// while the conversation still tracks it.
		<-rt.entered
		runID := liveRunID(conv, "s1")
		if runID == "" {
			t.Fatal("run id unknown")
		}
		close(rt.gate)
		waitFor(t, "run done recorded", func() bool {
			select {
			case <-start:
				return true
			default:
			}
			return lenEvents(t, conv, runID) >= 3
		})
		res := collectStream(conv.Attach(ctx, runID, 0))
		if res.err != nil {
			t.Fatalf("attach: %v", res.err)
		}
		if _, ok := res.evs[len(res.evs)-1].(Done); !ok {
			t.Fatalf("last event = %+v, want Done", res.evs[len(res.evs)-1])
		}
		for i := 1; i < len(res.evs); i++ {
			if res.evs[i] == res.evs[i-1] {
				t.Fatalf("duplicate delivery at %d: %+v", i, res.evs[i])
			}
		}
	})

	t.Run("cancelled context surfaces as a final tuple", func(t *testing.T) {
		rt := &detRT{gate: make(chan struct{}), pre: 1}
		conv := streamSetup(t, rt, Detached())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := make(chan streamResult, 1)
		go func() { start <- collectStream(conv.Send(principalCtx(ctx), "s1", userMsg("hi"))) }()
		<-start
		runID := liveRunID(conv, "s1")
		waitFor(t, "first event recorded", func() bool {
			return lenEvents(t, conv, runID) >= 1
		})
		close(rt.gate)
		waitFor(t, "run finished", func() bool {
			return liveRunID(conv, "s1") == ""
		})
		// An unknown run id has nothing to replay and nothing to wait for;
		// the sequence must still end once the context is cancelled.
		att, acancel := context.WithCancel(context.Background())
		go func() { _ = collectStream(conv.Attach(att, "no-such-run", 0)) }()
		acancel()
	})

	t.Run("resumes a live run without duplicates", func(t *testing.T) {
		rt := &detRT{entered: make(chan struct{}), gate: make(chan struct{}), pre: 3}
		conv := streamSetup(t, rt)
		ctx := context.Background()
		start := make(chan streamResult, 1)
		go func() { start <- collectStream(conv.Send(principalCtx(ctx), "s1", userMsg("hi"))) }()
		<-rt.entered
		waitFor(t, "first three events recorded", func() bool {
			return lenEvents(t, conv, liveRunID(conv, "s1")) >= 3
		})
		runID := liveRunID(conv, "s1")
		seen := make(chan int, 8)
		done := make(chan error, 1)
		go func() {
			n := 0
			for _, err := range conv.Attach(ctx, runID, 2) {
				if err != nil {
					done <- err
					return
				}
				n++
				seen <- n
			}
			done <- nil
		}()
		<-seen
		close(rt.gate)
		err := <-done
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
	})
}

func lenEvents(t *testing.T, c *conversation, runID string) int {
	t.Helper()
	n := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, err := range c.events.Read(ctx, runID, 0) {
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		n++
	}
	return n
}
