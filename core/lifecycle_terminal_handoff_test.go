package gohan

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// terminalRT ends the first turn with a carried Done whose reason the
// lifecycle must report instead of forcing completed.
type terminalRT struct{ reason types.StopReason }

func (r *terminalRT) Name() string                         { return "terminal.test" }
func (r *terminalRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *terminalRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *terminalRT) Step(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{}, []types.Event{types.Done{Reason: r.reason}}, runtime.DoneStatus, nil
}

// handoffSetupLog builds the takeover fixture over a plain session log,
// so the run drives without a control writer or a handoff callback.
func handoffSetupLog(t *testing.T, rt runtime.Runtime, log *stores.MemorySessionLog) *takeoverFixture {
	t.Helper()
	f := takeoverSetup(t, &takeoverRT{}, &takeoverLog{MemorySessionLog: log})
	f.conv.rt = rt
	return f
}

func lastNotice(t *testing.T, runs *stores.MemoryRuns) types.RunNotice {
	t.Helper()
	notices, err := runs.Notices(context.Background(), 10)
	if err != nil || len(notices) != 1 {
		t.Fatalf("Notices() = %v, %d notices, want 1", err, len(notices))
	}
	return notices[0]
}

func findHandoffMeta(t *testing.T, f *takeoverFixture, sid string) types.HandoffContext {
	t.Helper()
	ctx := principalCtx(context.Background())
	metas, _, err := f.log.Sessions(ctx, ownerOf(t, f.log.MemorySessionLog, sid), stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary}})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	for _, m := range metas {
		if m.ID == sid {
			return m.Handoff
		}
	}
	t.Fatalf("session %s not indexed", sid)
	return types.HandoffContext{}
}

func countKinds(kinds []stores.AuditKind, want stores.AuditKind) int {
	n := 0
	for _, k := range kinds {
		if k == want {
			n++
		}
	}
	return n
}

func assertDoneReason(t *testing.T, res streamResult, want types.StopReason) types.Done {
	t.Helper()
	if res.err != nil {
		t.Fatalf("stream: %v", res.err)
	}
	last, ok := res.evs[len(res.evs)-1].(types.Done)
	if !ok {
		t.Fatalf("last event %T, want types.Done", res.evs[len(res.evs)-1])
	}
	if last.Reason != want {
		t.Fatalf("Done reason %q, want %q", last.Reason, want)
	}
	return last
}

func TestLifecycleTerminalReason(t *testing.T) {
	t.Run("runtime.done-carries-limit-reason", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &terminalRT{reason: types.StopLimit})
			seedSession(t, f, "sess-t")
			res := collectStream(f.conv.Send(principalCtx(context.Background()), "sess-t", userMsg("hi")))
			assertDoneReason(t, res, types.StopLimit)
			n := lastNotice(t, f.runs)
			if n.Kind != types.NoticeFinished || n.Reason != string(types.StopLimit) {
				t.Fatalf("notice = %+v, want finished/%s", n, types.StopLimit)
			}
		})
	})

	t.Run("runtime.finish-carries-terminal-reason", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &terminalRT{reason: types.StopGuardBlocked})
			seedSession(t, f, "sess-t")
			res := collectStream(f.conv.Send(principalCtx(context.Background()), "sess-t", userMsg("hi")))
			assertDoneReason(t, res, types.StopGuardBlocked)
			if n := lastNotice(t, f.runs); n.Reason != string(types.StopGuardBlocked) {
				t.Fatalf("notice reason = %q, want %q", n.Reason, types.StopGuardBlocked)
			}
		})
	})

	t.Run("runtime.cancelled-run-keeps-reason", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := &takeoverRT{gate: make(chan struct{}), entered: make(chan struct{})}
			f := handoffSetup(t, rt)
			ctx := principalCtx(context.Background())
			seedSession(t, f, "sess-t")
			streamDone := make(chan streamResult, 1)
			go func() { streamDone <- collectStream(f.conv.Send(ctx, "sess-t", userMsg("run long"))) }()
			<-rt.entered
			cancelled := make(chan error, 1)
			go func() { cancelled <- f.conv.Cancel(ctx, "sess-t") }()
			synctest.Wait()
			close(rt.gate)
			if err := <-cancelled; err != nil {
				t.Fatalf("cancel: %v", err)
			}
			assertDoneReason(t, <-streamDone, types.StopCancelled)
			n := lastNotice(t, f.runs)
			if n.Kind != types.NoticeCancelled || n.Reason != string(types.StopCancelled) {
				t.Fatalf("notice = %+v, want cancelled/%s", n, types.StopCancelled)
			}
		})
	})

	t.Run("runtime.handed-off-run-keeps-reason", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &handoffRT{err: &types.HandoffError{Context: types.HandoffContext{Reason: "needs a human"}}})
			ctx := takeoverCtx(principalCtx(context.Background()), f)
			seedSession(t, f, "sess-t")
			res := collectStream(f.conv.Send(ctx, "sess-t", userMsg("help")))
			assertDoneReason(t, res, types.StopHandedOff)
			if n := lastNotice(t, f.runs); n.Kind != types.NoticeFinished || n.Reason != string(types.StopHandedOff) {
				t.Fatalf("notice = %+v, want finished/%s", n, types.StopHandedOff)
			}
		})
	})
}

func TestLifecycleHandoff(t *testing.T) {
	t.Run("flow.handoff-control-transition-once", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &handoffRT{err: &types.HandoffError{Context: types.HandoffContext{
				Reason: "needs a human",
				Skill:  "billing",
			}}})
			ctx := takeoverCtx(principalCtx(context.Background()), f)
			seedSession(t, f, "sess-t")

			res := collectStream(f.conv.Send(ctx, "sess-t", userMsg("help")))
			assertDoneReason(t, res, types.StopHandedOff)

			sets := f.log.controlSets()
			if len(sets) != 1 || sets[0] != stores.ControlHandoffRequested {
				t.Fatalf("control sets %v, want exactly one ControlHandoffRequested", sets)
			}
			kinds := auditKinds(t, f)
			if n := countKinds(kinds, "handoff_requested"); n != 1 {
				t.Fatalf("handoff_requested audit records = %d (%v), want 1", n, kinds)
			}
			if h := findHandoffMeta(t, f, "sess-t"); h.Reason != "needs a human" || h.Skill != "billing" {
				t.Fatalf("handoff = %+v, want persisted context", h)
			}
			for _, ev := range res.evs {
				if _, ok := ev.(types.Suspended); ok {
					t.Fatal("handoff emitted types.Suspended")
				}
			}
		})
	})

	t.Run("flow.handoff-optional-store", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// A plain session log without a control writer and no decision
			// audit in the context: the lifecycle persists the context
			// directly and still closes the run.
			log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
			f := handoffSetupLog(t, &handoffRT{err: &types.HandoffError{Context: types.HandoffContext{Reason: "hand it over"}}}, log)
			ctx := principalCtx(context.Background())
			if _, err := log.Append(ctx, "sess-t", 0, userMsg("seed")); err != nil {
				t.Fatalf("seed: %v", err)
			}

			res := collectStream(f.conv.Send(ctx, "sess-t", userMsg("help")))
			assertDoneReason(t, res, types.StopHandedOff)
			if h := findHandoffMeta(t, f, "sess-t"); h.Reason != "hand it over" {
				t.Fatalf("handoff = %+v, want persisted context", h)
			}
			if _, err := f.runs.RunForSession(ctx, "sess-t"); !errors.Is(err, stores.ErrRunNotFound) {
				t.Fatalf("RunForSession after handoff err = %v, want ErrRunNotFound", err)
			}
		})
	})

	t.Run("flow.handoff-state-preserved", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &handoffRT{err: &types.HandoffError{Context: types.HandoffContext{Reason: "needs a human"}}})
			ctx := takeoverCtx(principalCtx(context.Background()), f)
			seedSession(t, f, "sess-t")

			res := collectStream(f.conv.Send(ctx, "sess-t", userMsg("help")))
			assertDoneReason(t, res, types.StopHandedOff)

			// The session index, its owner and the handoff metadata all
			// survive the terminal transition.
			owner := ownerOf(t, f.log.MemorySessionLog, "sess-t")
			if owner.Subject != "u1" {
				t.Fatalf("owner = %+v, want u1", owner)
			}
			metas, _, err := f.log.Sessions(ctx, owner, stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary}})
			if err != nil {
				t.Fatalf("sessions: %v", err)
			}
			found := false
			for _, m := range metas {
				if m.ID == "sess-t" {
					found = m.Handoff.Reason == "needs a human"
				}
			}
			if !found {
				t.Fatalf("session state not preserved: metas %+v", metas)
			}
			if control, err := f.log.Control(ctx, "sess-t"); err != nil || control != stores.ControlHandoffRequested {
				t.Fatalf("control = %v (%v), want ControlHandoffRequested", control, err)
			}
		})
	})

	t.Run("flow.handoff-no-resume-token", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := handoffSetup(t, &handoffRT{err: &types.HandoffError{Context: types.HandoffContext{Reason: "needs a human"}}})
			ctx := takeoverCtx(principalCtx(context.Background()), f)
			seedSession(t, f, "sess-t")

			res := collectStream(f.conv.Send(ctx, "sess-t", userMsg("help")))
			assertDoneReason(t, res, types.StopHandedOff)
			for _, ev := range res.evs {
				if s, ok := ev.(types.Suspended); ok {
					t.Fatalf("handoff suspended with token %q, want no resume token", s.Token)
				}
			}
			if _, err := f.runs.RunForSession(ctx, "sess-t"); !errors.Is(err, stores.ErrRunNotFound) {
				t.Fatalf("RunForSession after handoff err = %v, want ErrRunNotFound", err)
			}
		})
	})
}
