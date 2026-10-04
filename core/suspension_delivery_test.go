package gohan

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// suspRT is a scripted stepper whose first step fails with err; a later
// step (after resume) just completes the run.
type suspRT struct {
	err   error
	steps int
}

func (r *suspRT) Name() string                         { return "susp.test" }
func (r *suspRT) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *suspRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *suspRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.steps++
	if r.steps == 1 && r.err != nil {
		return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.Continue, r.err
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

// recWaker records every wake the lifecycle arms.
type recWaker struct {
	tokens []types.ResumeToken
	ats    []time.Time
}

func (w *recWaker) Schedule(_ context.Context, t types.ResumeToken, at time.Time) error {
	w.tokens = append(w.tokens, t)
	w.ats = append(w.ats, at)
	return nil
}

func suspDrive(t *testing.T, rt *suspRT, lc *Lifecycle) ([]types.Event, []stores.Checkpoint) {
	t.Helper()
	var saved []stores.Checkpoint
	run := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
		saved = append(saved, cp)
		return "tok-1", nil
	}}
	var evs []types.Event
	for ev, err := range DriveLifecycle(context.Background(), lc, rt, run) {
		if err != nil {
			t.Fatalf("drive: unexpected error %v", err)
		}
		evs = append(evs, ev)
	}
	return evs, saved
}

func lastEvent(t *testing.T, evs []types.Event) types.Suspended {
	t.Helper()
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	susp, ok := evs[len(evs)-1].(types.Suspended)
	if !ok {
		t.Fatalf("last event %v, want Suspended", evs[len(evs)-1])
	}
	return susp
}

func TestSuspensionDelivery(t *testing.T) {
	t.Run("suspension.async-tool", func(t *testing.T) {
		rt := &suspRT{err: SuspendTool(types.AwaitingTool, "job-9")}
		evs, saved := suspDrive(t, rt, NewLifecycle())
		if len(saved) != 1 || saved[0].Reason != types.AwaitingTool {
			t.Fatalf("checkpoint reasons %v, want one awaiting_tool", saved)
		}
		susp := lastEvent(t, evs)
		if susp.Reason != types.AwaitingTool || susp.Payload != "job-9" {
			t.Fatalf("Suspended = %+v, want reason awaiting_tool payload job-9", susp)
		}

		// Replay: Deliver(result) becomes the pending call's tool result and
		// the run continues.
		alice := types.Principal{Tenant: "acme", Subject: "alice"}
		op := types.Principal{Tenant: "acme", Subject: "op"}
		cps := stores.NewMemoryCheckpoints()
		pending, err := json.Marshal(runtime.State{
			Turn:           1,
			HistoryVersion: 1,
			Pending:        []types.ToolUse{{ID: "call-1", Name: "emailer"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		token, err := cps.Put(types.WithPrincipal(context.Background(), alice), stores.Checkpoint{
			SchemaVersion: stores.CurrentSchemaVersion,
			SessionID:     "s1",
			Backend:       "susp.test",
			Reason:        types.AwaitingTool,
			Originator:    alice,
			Data:          pending,
		})
		if err != nil {
			t.Fatal(err)
		}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
			return types.PrincipalFrom(ctx)
		}))
		if _, err := log.Append(types.WithPrincipal(context.Background(), alice), "s1", 0, types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "send it"}},
		}); err != nil {
			t.Fatal(err)
		}
		resumed := &suspRT{}
		conv, err := NewConversation(&Stack{stores: stores.Stores{SessionLog: log}}, "flights", resumed,
			WithConversationRuns(stores.NewMemoryRuns()),
			WithConversationEventLog(stores.NewMemoryEventLog()),
			WithConversationCheckpoints(cps),
		)
		if err != nil {
			t.Fatal(err)
		}
		var done bool
		for ev, rerr := range conv.Resume(types.WithPrincipal(context.Background(), op), token, Deliver([]byte(`{"id":"job-9"}`))) {
			if rerr != nil {
				t.Fatalf("Resume: unexpected error %v", rerr)
			}
			if _, isDone := ev.(types.Done); isDone {
				done = true
			}
		}
		if !done {
			t.Error("Resume: no Done event; the run did not continue")
		}
		h, err := log.Load(types.WithPrincipal(context.Background(), alice), "s1")
		if err != nil {
			t.Fatal(err)
		}
		last := h.Messages[len(h.Messages)-1]
		res, ok := last.Blocks[len(last.Blocks)-1].(types.ToolResult)
		if !ok || res.ID != "call-1" || res.Outcome != types.Succeeded {
			t.Fatalf("last blocks %+v, want a succeeded tool result for call-1", last.Blocks)
		}
		if len(res.Content) != 1 {
			t.Fatalf("result content %+v, want the delivered data", res.Content)
		}
		if txt, ok := res.Content[0].(types.Text); !ok || txt.Text != `{"id":"job-9"}` {
			t.Fatalf("result content %+v, want the delivered data", res.Content)
		}
	})

	t.Run("suspension.scheduled", func(t *testing.T) {
		wake := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
		rt := &suspRT{err: &types.SuspendError{Reason: types.Scheduled, WakeAt: wake}}
		waker := &recWaker{}
		evs, saved := suspDrive(t, rt, NewLifecycle(WithLifecycleWaker(waker)))
		if len(saved) != 1 || saved[0].Reason != types.Scheduled {
			t.Fatalf("checkpoint reasons %v, want one scheduled", saved)
		}
		if len(waker.tokens) != 1 {
			t.Fatalf("waker schedules %d, want exactly 1", len(waker.tokens))
		}
		if waker.tokens[0] != "tok-1" || !waker.ats[0].Equal(wake) {
			t.Fatalf("waker saw (%q, %v), want (tok-1, %v)", waker.tokens[0], waker.ats[0], wake)
		}
		if susp := lastEvent(t, evs); susp.Reason != types.Scheduled || !susp.WakeAt.Equal(wake) {
			t.Fatalf("Suspended = %+v, want reason scheduled wake %v", susp, wake)
		}
	})

	t.Run("scheduled wake reaches the waker exactly once, after the run is suspended", func(t *testing.T) {
		wake := time.Date(2026, 10, 4, 10, 30, 0, 0, time.UTC)
		rt := &suspRT{err: &types.SuspendError{Reason: types.Scheduled, WakeAt: wake}}
		waker := &recWaker{}
		rec := &order{}
		saved := 0
		run := runtime.AgentRun{Save: func(_ context.Context, _ stores.Checkpoint) (types.ResumeToken, error) {
			rec.mark("checkpoints.put")
			saved++
			return "tok-1", nil
		}}
		runs := &lcRuns{rec: rec}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, stores.Lease{RunID: "run-1"}),
			WithLifecycleWaker(waker),
		)
		for _, err := range DriveLifecycle(context.Background(), lc, rt, run) {
			if err != nil {
				t.Fatalf("drive: unexpected error %v", err)
			}
		}
		if len(waker.tokens) != 1 || saved != 1 {
			t.Fatalf("waker %d schedules over %d checkpoints, want one each", len(waker.tokens), saved)
		}
		rec.want(t, "checkpoints.put", "runs.suspend")
	})

	t.Run("suspended event carries the payload and the wake time", func(t *testing.T) {
		wake := time.Date(2026, 10, 4, 23, 59, 0, 0, time.UTC)
		rt := &suspRT{err: &types.SuspendError{Reason: types.Scheduled, Payload: "nightly", WakeAt: wake}}
		evs, _ := suspDrive(t, rt, NewLifecycle())
		susp := lastEvent(t, evs)
		if susp.Token != "tok-1" || susp.Reason != types.Scheduled || susp.Payload != "nightly" || !susp.WakeAt.Equal(wake) {
			t.Fatalf("Suspended = %+v, want token tok-1, payload nightly, wake %v", susp, wake)
		}
	})
}
