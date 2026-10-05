package gohan

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// A resumed run's events are recorded in the run's event log under the
// checkpoint's run id with a continuous sequence, and the run is marked
// ended so Attach waiters wake at its terminal event.
func TestResumeEventsRecorded(t *testing.T) {
	h := newAuthHarness(t)
	runID := "run-resume-events"
	token := h.seed(t, runID, types.Preempted, runtime.State{Turn: 1, HistoryVersion: 1}, nil)
	h.rt.steps = []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, nil, runtime.DoneStatus, nil
		},
	}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack := &Stack{stores: stores.Stores{SessionLog: h.log}}
	conv, err := NewConversation(stack, "flights", h.rt,
		WithConversationRuns(h.runs),
		WithConversationEventLog(events),
		WithConversationCheckpoints(h.cps),
		WithConversationCredentialSource(&authCreds{}),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}

	ctx := types.WithPrincipal(context.Background(), authOwner)
	var yielded []types.Event
	for ev, err := range conv.Resume(ctx, token, Continue()) {
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		yielded = append(yielded, ev)
	}
	if len(yielded) == 0 {
		t.Fatal("resume yielded no events")
	}
	if _, ok := yielded[len(yielded)-1].(types.Done); !ok {
		t.Fatalf("last resumed event = %+v, want Done", yielded[len(yielded)-1])
	}

	var seq int64
	for e, err := range events.Read(ctx, runID, 0) {
		if err != nil {
			t.Fatalf("read events: %v", err)
		}
		seq++
		if e.Meta.Seq != seq {
			t.Fatalf("event %d has Seq %d, want %d", seq, e.Meta.Seq, seq)
		}
		if e.Meta.RunID != runID {
			t.Fatalf("event %d carries run id %q, want %q", seq, e.Meta.RunID, runID)
		}
	}
	if seq != int64(len(yielded)) {
		t.Fatalf("log holds %d events, resume yielded %d", seq, len(yielded))
	}

	// The run is marked ended, so Attach replays the log instead of
	// waiting for a live producer.
	attached := collectStream(conv.Attach(context.Background(), runID, 0))
	if attached.err != nil {
		t.Fatalf("attach: %v", attached.err)
	}
	if _, ok := attached.evs[len(attached.evs)-1].(types.Done); !ok {
		t.Fatalf("last attached event = %+v, want Done", attached.evs[len(attached.evs)-1])
	}
}
