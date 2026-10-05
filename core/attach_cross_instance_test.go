package gohan

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func crossInstanceConvs(t *testing.T, rt runtime.Runtime, opts ...ConversationOption) (*conversation, *conversation) {
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
	base := append([]ConversationOption{
		WithConversationRuns(runs),
		WithConversationEventLog(events),
	}, opts...)
	writer, err := NewConversation(stack, "chat", rt, base...)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	observer, err := NewConversation(stack, "chat", rt, base...)
	if err != nil {
		t.Fatalf("new observer: %v", err)
	}
	return writer.(*conversation), observer.(*conversation)
}

func TestAttachObservesAnotherConversationWriter(t *testing.T) {
	rt := &detRT{pre: 2, entered: make(chan struct{}), gate: make(chan struct{})}
	writer, observer := crossInstanceConvs(t, rt, Detached())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan streamResult, 1)
	go func() { start <- collectStream(writer.Send(principalCtx(ctx), "s1", userMsg("hi"))) }()
	<-rt.entered
	runID := liveRunID(writer, "s1")
	if runID == "" {
		t.Fatal("run id unknown")
	}
	waitFor(t, "writer recorded first batch", func() bool {
		return lenEvents(t, writer, runID) >= 2
	})

	var got []types.Event
	evs := make(chan types.Event, 16)
	failed := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for e, err := range observer.Attach(ctx, runID, 0) {
			if err != nil {
				failed <- err
				return
			}
			evs <- e
		}
	}()

	// The observer catches up on the replayed prefix while the writer is
	// still gated behind its second step.
	for range 2 {
		select {
		case e := <-evs:
			got = append(got, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for replayed event %d", len(got))
		}
	}

	close(rt.gate)
loop:
	for {
		select {
		case e := <-evs:
			got = append(got, e)
			if _, ok := e.(types.Done); ok {
				break loop
			}
		case err := <-failed:
			t.Fatalf("attach: %v", err)
		case <-finished:
			// The sequence delivered everything before returning; drain
			// what is still buffered and decide on its tail.
			for {
				select {
				case e := <-evs:
					got = append(got, e)
					if _, ok := e.(types.Done); ok {
						break loop
					}
					continue
				default:
				}
				t.Fatalf("sequence ended without Done after %d events", len(got))
			}
		case <-time.After(4 * time.Second):
			t.Fatalf("timed out waiting for terminal after %d events", len(got))
		}
	}
	select {
	case err := <-failed:
		t.Fatalf("attach: %v", err)
	case <-finished:
	case <-time.After(4 * time.Second):
		t.Fatal("attach sequence did not end after Done")
	}

	var gotDeltas []string
	for _, e := range got {
		switch v := e.(type) {
		case types.TextDelta:
			gotDeltas = append(gotDeltas, v.Delta)
		}
	}
	want := []string{"d0", "d1", "late"}
	if len(gotDeltas) != len(want) {
		t.Fatalf("deltas = %v, want %v", gotDeltas, want)
	}
	for i := range want {
		if gotDeltas[i] != want[i] {
			t.Fatalf("delta %d = %q, want %q", i, gotDeltas[i], want[i])
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("duplicate delivery at %d: %+v", i, got[i])
		}
	}
	if _, ok := got[len(got)-1].(types.Done); !ok {
		t.Fatalf("last event = %+v, want Done", got[len(got)-1])
	}
}
