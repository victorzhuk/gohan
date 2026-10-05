package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// order records the sequence of observable lifecycle effects a test wants
// to compare.
type order struct {
	lines []string
}

func (o *order) mark(line string) { o.lines = append(o.lines, line) }

func (o *order) want(t *testing.T, want ...string) {
	t.Helper()
	if len(o.lines) != len(want) {
		t.Fatalf("order = %v, want %v", o.lines, want)
	}
	for i := range want {
		if o.lines[i] != want[i] {
			t.Fatalf("order = %v, want %v", o.lines, want)
		}
	}
}

// lcStep is one scripted Step outcome.
type lcStep struct {
	events []types.Event
	status runtime.Status
}

// lcRuntime replays the script; every step advances the turn and the
// history version once.
type lcRuntime struct {
	script   []lcStep
	histVer  int64
	steps    int
	starts   int
	sawTurn  []int
	stepWait time.Duration
}

func (s *lcRuntime) Name() string                         { return "scripted" }
func (s *lcRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (s *lcRuntime) Start(_ context.Context, _ runtime.AgentRun) (runtime.State, error) {
	s.starts++
	return runtime.State{HistoryVersion: s.histVer}, nil
}

func (s *lcRuntime) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	s.sawTurn = append(s.sawTurn, st.Turn)
	if s.stepWait > 0 {
		time.Sleep(s.stepWait)
	}
	if s.steps >= len(s.script) {
		return st, nil, runtime.DoneStatus, fmt.Errorf("gohan: script exhausted after %d steps", s.steps)
	}
	out := s.script[s.steps]
	s.steps++
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1, Usage: types.Usage{InputTokens: 2}},
		out.events, out.status, nil
}

// lcRuns is a Runs fake: it records the lifecycle calls it sees and refuses
// Finish with ErrSignalsPending while steers are pending.
type lcRuns struct {
	rec           *order
	pendingSteers []stores.Signal
	drained       bool
	finished      int
	uncertain     [][]types.CallKey
	suspended     types.ResumeToken
	suspendLease  stores.Lease
}

func (s *lcRuns) Start(context.Context, stores.Run, time.Duration) (stores.Lease, error) {
	return stores.Lease{RunID: "run-1"}, nil
}

func (s *lcRuns) Heartbeat(_ context.Context, l stores.Lease) (stores.Lease, error) { return l, nil }

func (s *lcRuns) Suspend(_ context.Context, l stores.Lease, t types.ResumeToken) error {
	s.rec.mark("runs.suspend")
	s.suspendLease = l
	s.suspended = t
	return nil
}

func (s *lcRuns) Resuming(_ context.Context, _ string, _ time.Duration) (stores.Lease, error) {
	return stores.Lease{RunID: "run-1"}, nil
}

func (s *lcRuns) Finish(_ context.Context, _ stores.Lease, _ stores.RunState, uncertain []types.CallKey, _ string) error {
	steerPending := false
	for _, sig := range s.pendingSteers {
		if sig.Kind == stores.SignalSteer {
			steerPending = true
		}
	}
	if steerPending && !s.drained {
		s.rec.mark("runs.finish.refused")
		return fmt.Errorf("gohan: run run-1: %w", types.ErrSignalsPending)
	}
	s.rec.mark("runs.finish")
	s.finished++
	s.uncertain = append(s.uncertain, uncertain)
	return nil
}

func (s *lcRuns) ByOperation(context.Context, string, string) (stores.Run, error) {
	return stores.Run{RunID: "run-1"}, nil
}

func (s *lcRuns) Stale(context.Context, time.Duration, int) ([]stores.Run, error) { return nil, nil }

func (s *lcRuns) Reclaim(context.Context, stores.Run, time.Duration) (stores.Lease, error) {
	return stores.Lease{RunID: "run-1"}, nil
}

func (s *lcRuns) Signal(_ context.Context, _ string, sig stores.Signal) error {
	s.pendingSteers = append(s.pendingSteers, sig)
	return nil
}

func (s *lcRuns) Drain(context.Context, stores.Lease) ([]stores.Signal, error) {
	s.drained = true
	got := s.pendingSteers
	s.pendingSteers = nil
	return got, nil
}

func (s *lcRuns) Notices(context.Context, int) ([]types.RunNotice, error) { return nil, nil }

func (s *lcRuns) AckNotice(context.Context, string) error { return nil }

// hbLostRuns fails every heartbeat refresh: the lease is gone.
type hbLostRuns struct {
	lcRuns
	lost error
}

func (s *hbLostRuns) Heartbeat(context.Context, stores.Lease) (stores.Lease, error) {
	return stores.Lease{}, s.lost
}

// hbCountRuns counts the heartbeat refreshes it sees.
type hbCountRuns struct {
	lcRuns
	hbCalls int
}

func (s *hbCountRuns) Heartbeat(_ context.Context, l stores.Lease) (stores.Lease, error) {
	s.hbCalls++
	return l, nil
}

func TestRuntimeLifecycleHeartbeat(t *testing.T) {
	t.Run("streams.heartbeat-independent-of-consumer", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runs := &hbCountRuns{lcRuns: lcRuns{rec: &order{}}}
			lease, err := runs.Start(context.Background(), stores.Run{SessionID: "s1", RunID: "run-1"}, stores.LeaseTTL)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			rt := &lcRuntime{script: []lcStep{{status: runtime.DoneStatus}}, stepWait: 2 * stores.LeaseTTL}
			lc := NewLifecycle(WithLifecycleRuns(runs, lease))
			for _, err := range DriveLifecycle(context.Background(), lc, rt, runtime.AgentRun{}) {
				if err != nil {
					t.Fatalf("drive: %v", err)
				}
			}
			if lc.hb != nil {
				t.Fatal("heartbeat still running after the iterator returned")
			}
			if runs.hbCalls == 0 {
				t.Fatal("no heartbeat refresh while the step stalled past the lease TTL")
			}
			seen := runs.hbCalls
			time.Sleep(2 * stores.HeartbeatEvery)
			if runs.hbCalls != seen {
				t.Fatalf("heartbeat refreshed %d more times after the iterator returned", runs.hbCalls-seen)
			}
		})
	})

	t.Run("heartbeat loss ends the run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lost := errors.New("run lease refused")
			rec := &order{}
			runs := &hbLostRuns{lcRuns: lcRuns{rec: rec}, lost: lost}
			lease, err := runs.Start(context.Background(), stores.Run{SessionID: "s1", RunID: "run-1"}, stores.LeaseTTL)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			rt := &lcRuntime{script: []lcStep{{status: runtime.DoneStatus}}, stepWait: 2 * stores.HeartbeatEvery}
			lc := NewLifecycle(WithLifecycleRuns(runs, lease))
			var evs []types.Event
			var got error
			for ev, err := range DriveLifecycle(context.Background(), lc, rt, runtime.AgentRun{}) {
				if err != nil {
					got = err
					break
				}
				evs = append(evs, ev)
			}
			if !errors.Is(got, lost) {
				t.Fatalf("drive err = %v, want the heartbeat failure", got)
			}
			for _, ev := range evs {
				if _, ok := ev.(types.Done); ok {
					t.Fatal("Done delivered after ownership loss")
				}
			}
			if runs.finished != 0 {
				t.Fatalf("Finish called %d times after ownership loss, want 0", runs.finished)
			}
		})
	})

	t.Run("runtime.preempted-resume-continue", func(t *testing.T) {
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		lc := NewLifecycle(WithLifecycleResumeState(runtime.State{Turn: 3, HistoryVersion: 1}))
		var done bool
		for ev, err := range DriveLifecycle(context.Background(), lc, rt, runtime.AgentRun{}) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("no Done event")
		}
		if rt.starts != 1 {
			t.Fatalf("Start called %d times, want 1", rt.starts)
		}
		if len(rt.sawTurn) != 1 || rt.sawTurn[0] != 3 {
			t.Fatalf("drove turns %v, want the saved turn 3", rt.sawTurn)
		}
	})

	t.Run("drive resume initializes wiring and drives the saved state", func(t *testing.T) {
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		var done bool
		for ev, err := range DriveResume(context.Background(), rt, runtime.AgentRun{}, runtime.State{Turn: 7, HistoryVersion: 1}, stores.ResumeInput{}) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if _, ok := ev.(types.Done); ok {
				done = true
			}
		}
		if !done {
			t.Fatal("no Done event")
		}
		if rt.starts != 1 {
			t.Fatalf("Start called %d times, want 1", rt.starts)
		}
		if len(rt.sawTurn) != 1 || rt.sawTurn[0] != 7 {
			t.Fatalf("drove turns %v, want the saved turn 7", rt.sawTurn)
		}
	})
}

// lcAppend is a HistoryAppender fake: it records the expected version and
// the roles it appended, and advances the version by one per call.
type lcAppend struct {
	rec   *order
	ver   int64
	calls int
}

func (a *lcAppend) Append(_ context.Context, expected int64, msgs ...types.Message) (int64, error) {
	if expected != a.ver {
		return a.ver, fmt.Errorf("gohan: append at %d: %w", expected, types.ErrVersionConflict)
	}
	roles := ""
	for i, m := range msgs {
		if i > 0 {
			roles += ","
		}
		roles += string(m.Role)
	}
	a.rec.mark(fmt.Sprintf("append[%s]", roles))
	a.calls++
	a.ver++
	return a.ver, nil
}

// saveCheckpoint stands in for Checkpoints.Put through AgentRun.Save.
func saveCheckpoint(rec *order) func(context.Context, stores.Checkpoint) (types.ResumeToken, error) {
	return func(context.Context, stores.Checkpoint) (types.ResumeToken, error) {
		rec.mark("checkpoints.put")
		return "tok-1", nil
	}
}

func lcRun(m *lcRuntime, rec *order) runtime.AgentRun {
	return runtime.AgentRun{Save: saveCheckpoint(rec)}
}

func lcDrive(t *testing.T, lc *Lifecycle, rt *lcRuntime, rec *order) ([]types.Event, error) {
	t.Helper()
	var evs []types.Event
	var err error
	for e, eerr := range DriveLifecycle(context.Background(), lc, rt, lcRun(rt, rec)) {
		if eerr != nil {
			err = eerr
			break
		}
		evs = append(evs, e)
	}
	return evs, err
}

func TestRuntimeLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime.suspend-order", func(t *testing.T) {
		rec := &order{}
		rt := &lcRuntime{histVer: 4, script: []lcStep{
			{status: runtime.Continue},
			{status: runtime.SuspendedStatus},
		}}
		runs := &lcRuns{rec: rec}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, stores.Lease{RunID: "run-1"}),
			WithLifecycleSession("s-1"),
		)
		evs, err := lcDrive(t, lc, rt, rec)
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		rec.want(t, "checkpoints.put", "runs.suspend")
		if len(evs) == 0 {
			t.Fatal("no events")
		}
		last := evs[len(evs)-1]
		susp, ok := last.(types.Suspended)
		if !ok {
			t.Fatalf("last event %v, want Suspended", last)
		}
		if susp.Token != "tok-1" || susp.Reason != types.AwaitingTool {
			t.Fatalf("Suspended = %+v, want token tok-1 reason awaiting_tool", susp)
		}
		for _, e := range evs[:len(evs)-1] {
			if _, ok := e.(types.Done); ok {
				t.Fatalf("Done %v emitted before Suspended", e)
			}
		}
	})

	t.Run("suspend persists checkpoint identity", func(t *testing.T) {
		rec := &order{}
		rt := &lcRuntime{histVer: 4, script: []lcStep{{status: runtime.SuspendedStatus}}}
		runs := &lcRuns{rec: rec}
		lease := stores.Lease{RunID: "run-9", Generation: 2, Expires: time.Now().Add(time.Minute).UTC()}
		originator := types.Principal{Tenant: "t-a", Subject: "u1"}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, stores.Lease{}),
			WithLifecycleLease(lease),
			WithLifecycleSession("s-1"),
			WithLifecycleFlow("chat"),
			WithLifecycleOriginator(originator),
		)
		var saved stores.Checkpoint
		ag := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
			saved = cp
			rec.mark("checkpoints.put")
			return "tok-1", nil
		}}
		for _, err := range DriveLifecycle(ctx, lc, rt, ag) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
		}
		if runs.suspendLease != lease {
			t.Fatalf("suspend lease %+v, want the lifecycle lease %+v", runs.suspendLease, lease)
		}
		if saved.RunID != "run-9" || saved.Flow != "chat" {
			t.Fatalf("checkpoint identity run=%q flow=%q, want run-9/chat", saved.RunID, saved.Flow)
		}
		if saved.Originator.Tenant != originator.Tenant || saved.Originator.Subject != originator.Subject {
			t.Fatalf("checkpoint originator %+v, want %+v", saved.Originator, originator)
		}
		if !saved.ExpiresAt.Equal(lease.Expires) {
			t.Fatalf("checkpoint expiry %v, want lease expiry %v", saved.ExpiresAt, lease.Expires)
		}
		env, st, err := decodeCheckpoint(saved, rt, "chat")
		if err != nil {
			t.Fatalf("decode saved checkpoint: %v", err)
		}
		if env.Run.RunID != "run-9" || env.Run.SessionID != "s-1" || env.Run.Flow != "chat" {
			t.Fatalf("envelope run %+v, want run-9/s-1/chat", env.Run)
		}
		if env.Run.Principal.Tenant != originator.Tenant || env.Run.Principal.Subject != originator.Subject {
			t.Fatalf("envelope principal %+v, want %+v", env.Run.Principal, originator)
		}
		if env.Generation != 1 {
			t.Fatalf("envelope generation %d, want 1", env.Generation)
		}
		if st.HistoryVersion != 5 {
			t.Fatalf("state history version %d, want 5", st.HistoryVersion)
		}
	})

	t.Run("runtime.append-before-tool", func(t *testing.T) {
		rec := &order{}
		app := &lcAppend{rec: rec, ver: 3}
		rt := &lcRuntime{histVer: 3, script: []lcStep{{status: runtime.DoneStatus}}}
		_ = rt
		asst := types.Message{ID: "a1", Role: types.RoleAssistant, Blocks: []types.Block{
			types.ToolUse{ID: "call-1", Name: "writer"},
		}}
		// The committed shape appends the assistant with its pending calls
		// before the batch executes; the gate and the tool run see the
		// advanced version.
		v, err := AppendBeforeBatch(ctx, app, app.ver, asst)
		if err != nil {
			t.Fatalf("AppendBeforeBatch: %v", err)
		}
		rec.mark("gate")
		rec.mark("tool")
		if v != 4 {
			t.Fatalf("version %d, want 4", v)
		}
		rec.want(t, "append[assistant]", "gate", "tool")
	})

	t.Run("no calls append once at step end", func(t *testing.T) {
		rec := &order{}
		app := &lcAppend{rec: rec, ver: 2}
		if ShapeFor() != ShapeStepEnd {
			t.Fatal("a turn with no calls must take the step-end shape")
		}
		asst := types.Message{ID: "a1", Role: types.RoleAssistant}
		v, err := AppendStepEnd(ctx, app, app.ver, asst)
		if err != nil {
			t.Fatalf("AppendStepEnd: %v", err)
		}
		if v != 3 {
			t.Fatalf("version %d, want 3", v)
		}
		if app.calls != 1 {
			t.Fatalf("%d appends, want 1", app.calls)
		}
		rec.want(t, "append[assistant]")
	})

	t.Run("read-only calls append once at step end", func(t *testing.T) {
		rec := &order{}
		app := &lcAppend{rec: rec, ver: 2}
		if ShapeFor(types.ReadOnly, types.ReadOnly) != ShapeStepEnd {
			t.Fatal("a read-only turn must take the step-end shape")
		}
		asst := types.Message{ID: "a1", Role: types.RoleAssistant}
		results := []types.Message{
			{ID: "r1", Role: types.RoleUser},
			{ID: "r2", Role: types.RoleUser},
		}
		v, err := AppendStepEnd(ctx, app, app.ver, asst, results...)
		if err != nil {
			t.Fatalf("AppendStepEnd: %v", err)
		}
		if v != 3 {
			t.Fatalf("version %d, want 3: assistant and results share one append", v)
		}
		if app.calls != 1 {
			t.Fatalf("%d appends, want 1", app.calls)
		}
		rec.want(t, "append[assistant,user,user]")
	})

	t.Run("side-effect calls append pending before the batch", func(t *testing.T) {
		rec := &order{}
		app := &lcAppend{rec: rec, ver: 5}
		if ShapeFor(types.SideEffect) != ShapeBeforeBatch || ShapeFor(types.Idempotent) != ShapeBeforeBatch {
			t.Fatal("a committed turn must take the before-batch shape")
		}
		asst := types.Message{ID: "a1", Role: types.RoleAssistant, Blocks: []types.Block{
			types.ToolUse{ID: "call-1", Name: "writer"},
		}}
		// Append shape, then the batch runs; the version advanced before the
		// first tool executes.
		v, err := AppendBeforeBatch(ctx, app, app.ver, asst)
		if err != nil {
			t.Fatalf("AppendBeforeBatch: %v", err)
		}
		rec.mark("gate")
		rec.mark("tool")
		if v != 6 {
			t.Fatalf("version %d, want 6", v)
		}
		rec.want(t, "append[assistant]", "gate", "tool")
	})

	t.Run("runtime.done-after-finish", func(t *testing.T) {
		rec := &order{}
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		runs := &lcRuns{rec: rec}
		key := types.CallKey{SessionID: "s-1", CallID: "call-1"}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, stores.Lease{RunID: "run-1"}),
			WithLifecycleUncertain(func(runtime.State) []types.CallKey { return []types.CallKey{key} }),
			WithLifecycleVerify(func(_ context.Context, keys []types.CallKey) ([]types.CallKey, error) {
				rec.mark("verify")
				if len(keys) != 1 || keys[0] != key {
					t.Fatalf("verify keys = %v, want [call-1]", keys)
				}
				return nil, nil
			}),
		)
		evs, err := lcDrive(t, lc, rt, rec)
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		rec.want(t, "verify", "runs.finish")
		done, ok := evs[len(evs)-1].(types.Done)
		if !ok {
			t.Fatalf("last event %v, want Done", evs[len(evs)-1])
		}
		if done.Reason != types.StopCompleted || len(done.Uncertain) != 0 {
			t.Fatalf("Done = %+v, want completed with nothing uncertain", done)
		}
		for _, got := range runs.uncertain {
			if len(got) != 0 {
				t.Fatalf("Finish carried %v, want the verified key set cleared", got)
			}
		}
	})

	t.Run("pending steer forces one more turn", func(t *testing.T) {
		rec := &order{}
		rt := &lcRuntime{histVer: 1, script: []lcStep{
			{status: runtime.DoneStatus},
			{status: runtime.DoneStatus},
		}}
		runs := &lcRuns{rec: rec}
		steer := types.Message{ID: "steer-1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "stop"}}}
		if err := runs.Signal(ctx, "run-1", stores.Signal{Kind: stores.SignalSteer, Message: steer}); err != nil {
			t.Fatalf("Signal: %v", err)
		}
		app := &lcAppend{rec: rec, ver: 2}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, stores.Lease{RunID: "run-1"}),
			WithLifecycleAppender(app),
			WithLifecycleMaxTurns(5),
		)
		evs, err := lcDrive(t, lc, rt, rec)
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if rt.steps != 2 {
			t.Fatalf("%d steps, want 2: the steer forces one more turn", rt.steps)
		}
		rec.want(t, "append[user]", "runs.finish")
		var sawSteer bool
		for i, e := range evs {
			if sa, ok := e.(types.SteerApplied); ok {
				sawSteer = true
				if sa.MessageID != "steer-1" {
					t.Fatalf("SteerApplied = %+v, want steer-1", sa)
				}
				if _, ok := evs[i+1].(types.Done); !ok || i != len(evs)-2 {
					t.Fatalf("SteerApplied at %d of %v, want directly before Done", i, evs)
				}
			}
		}
		if !sawSteer {
			t.Fatalf("no SteerApplied in %v", evs)
		}
		if done := evs[len(evs)-1].(types.Done); done.Reason != types.StopCompleted {
			t.Fatalf("Done reason %v, want completed", done.Reason)
		}
		if runs.finished != 1 {
			t.Fatalf("%d finishes, want 1 committed finish", runs.finished)
		}
	})

	t.Run("max turns reached ends with stop limit", func(t *testing.T) {
		rec := &order{}
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		runs := &lcRuns{rec: rec}
		steer := types.Message{ID: "steer-1", Role: types.RoleUser}
		if err := runs.Signal(ctx, "run-1", stores.Signal{Kind: stores.SignalSteer, Message: steer}); err != nil {
			t.Fatalf("Signal: %v", err)
		}
		lc := NewLifecycle(
			WithLifecycleRuns(runs, stores.Lease{RunID: "run-1"}),
			WithLifecycleMaxTurns(1),
		)
		evs, err := lcDrive(t, lc, rt, rec)
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		if rt.steps != 1 {
			t.Fatalf("%d steps, want 1: the turn limit is already reached", rt.steps)
		}
		rec.want(t, "runs.finish")
		done, ok := evs[len(evs)-1].(types.Done)
		if !ok || done.Reason != types.StopLimit {
			t.Fatalf("last event %v, want Done limit", evs[len(evs)-1])
		}
	})

	t.Run("cancel signal stops with cancelled", func(t *testing.T) {
		rec := &order{}
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		runs := &lcRuns{rec: rec}
		if err := runs.Signal(ctx, "run-1", stores.Signal{Kind: stores.SignalCancel}); err != nil {
			t.Fatalf("Signal: %v", err)
		}
		lc := NewLifecycle(WithLifecycleRuns(runs, stores.Lease{RunID: "run-1"}))
		evs, err := lcDrive(t, lc, rt, rec)
		if err != nil {
			t.Fatalf("drive: %v", err)
		}
		done, ok := evs[len(evs)-1].(types.Done)
		if !ok || done.Reason != types.StopCancelled {
			t.Fatalf("last event %v, want Done cancelled", evs[len(evs)-1])
		}
		rec.want(t, "runs.finish")
	})
}

// approvalRT suspends its first step with a HumanApproval SuspendError
// carrying the scripted pending calls, then completes the run.
type approvalRT struct {
	pending []types.ToolUse
	steps   int
}

func (r *approvalRT) Name() string                         { return "approval.test" }
func (r *approvalRT) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (r *approvalRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *approvalRT) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.steps++
	if r.steps == 1 {
		st.Pending = r.pending
		return st, nil, runtime.Continue, &types.SuspendError{Reason: types.HumanApproval}
	}
	return runtime.State{Turn: st.Turn + 1, HistoryVersion: st.HistoryVersion + 1}, nil, runtime.DoneStatus, nil
}

func driveApprovalSuspend(t *testing.T, rt *approvalRT, tools []types.Tool, lc *Lifecycle) ([]types.Event, []stores.Checkpoint, error) {
	t.Helper()
	var saved []stores.Checkpoint
	var driveErr error
	run := runtime.AgentRun{Tools: tools, Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
		saved = append(saved, cp)
		return "tok-1", nil
	}}
	for _, err := range DriveLifecycle(context.Background(), lc, rt, run) {
		if err != nil {
			driveErr = err
			break
		}
	}
	return nil, saved, driveErr
}

func TestLifecycleApprovalEnvelope(t *testing.T) {
	t.Run("runtime.batch-ask-after-allowed", func(t *testing.T) {
		rt := &approvalRT{pending: []types.ToolUse{{ID: "c1", Name: "confirm", Args: json.RawMessage(`{"n":1}`)}}}
		_, saved, err := driveApprovalSuspend(t, rt,
			[]types.Tool{stubTool{spec: types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}}},
			NewLifecycle(
				WithLifecycleLease(stores.Lease{RunID: "run-1"}),
				WithLifecycleSession("s1"),
				WithLifecycleFlow("flights"),
				WithLifecycleApprovalPolicy(&authPolicySource{scope: "approve:confirm"}),
			))
		if err != nil {
			t.Fatal(err)
		}
		if len(saved) != 1 {
			t.Fatalf("checkpoints saved: %d, want 1", len(saved))
		}
		env, _, err := decodeCheckpoint(saved[0], rt, "flights")
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Approvals) != 1 {
			t.Fatalf("approvals: %+v, want one", env.Approvals)
		}
		ap := env.Approvals[0]
		if ap.Call.ID != "c1" || ap.Call.Name != "confirm" {
			t.Errorf("approval call: %+v", ap.Call)
		}
		if ap.Risk != types.RiskHigh {
			t.Errorf("risk: %v, want high", ap.Risk)
		}
		if ap.Reversible {
			t.Error("reversible: got true, want false for a side effect")
		}
		if len(ap.Eligible.Scopes) != 1 || ap.Eligible.Scopes[0] != "approve:confirm" {
			t.Errorf("eligible scopes: %+v", ap.Eligible)
		}
		if ap.Eligible.Quorum != 1 {
			t.Errorf("quorum: %d, want 1", ap.Eligible.Quorum)
		}
		if ap.Fingerprint == "" {
			t.Error("fingerprint empty")
		}
		if len(ap.ApprovedBy) != 0 {
			t.Errorf("approved by: %+v, want empty on first suspension", ap.ApprovedBy)
		}
	})

	t.Run("permission.rich-request", func(t *testing.T) {
		src := &authPolicySource{}
		rt := &approvalRT{pending: []types.ToolUse{
			{ID: "c1", Name: "confirm", Args: json.RawMessage(`{"n":1}`)},
			{ID: "c2", Name: "note", Args: json.RawMessage(`{"t":"x"}`)},
		}}
		_, saved, err := driveApprovalSuspend(t, rt,
			[]types.Tool{
				stubTool{spec: types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}},
				stubTool{spec: types.ToolSpec{Name: "note", Risk: types.RiskLow, Effect: types.ReadOnly}},
			},
			NewLifecycle(
				WithLifecycleLease(stores.Lease{RunID: "run-1"}),
				WithLifecycleSession("s1"),
				WithLifecycleFlow("flights"),
				WithLifecycleApprovalPolicy(src),
			))
		if err != nil {
			t.Fatal(err)
		}
		env, _, err := decodeCheckpoint(saved[0], rt, "flights")
		if err != nil {
			t.Fatal(err)
		}
		// Only the active ask, the head of the pending queue, is a
		// persisted approval; the queued tail asks when it becomes the
		// head.
		if len(env.Approvals) != 1 || env.Approvals[0].Call.ID != "c1" {
			t.Fatalf("approvals: %+v, want one for the active ask c1", env.Approvals)
		}
		if src.calls != 1 {
			t.Errorf("policy resolutions: %d, want 1", src.calls)
		}
		if env.Approvals[0].Reversible {
			t.Errorf("reversible: true, want false for the side effect")
		}
		if env.Approvals[0].Risk != types.RiskHigh {
			t.Errorf("risk: %v, want high", env.Approvals[0].Risk)
		}
	})

	t.Run("missing-tool-spec-refused", func(t *testing.T) {
		rt := &approvalRT{pending: []types.ToolUse{{ID: "c1", Name: "ghost", Args: json.RawMessage(`{}`)}}}
		_, saved, err := driveApprovalSuspend(t, rt, nil,
			NewLifecycle(
				WithLifecycleLease(stores.Lease{RunID: "run-1"}),
				WithLifecycleSession("s1"),
				WithLifecycleFlow("flights"),
				WithLifecycleApprovalPolicy(&authPolicySource{}),
			))
		if !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("got %v, want ErrCheckpointIncompatible", err)
		}
		if len(saved) != 0 {
			t.Fatalf("refused suspension persisted %d checkpoints", len(saved))
		}
	})

	t.Run("missing-policy-refused", func(t *testing.T) {
		rt := &approvalRT{pending: []types.ToolUse{{ID: "c1", Name: "confirm", Args: json.RawMessage(`{}`)}}}
		_, saved, err := driveApprovalSuspend(t, rt,
			[]types.Tool{stubTool{spec: types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect}}},
			NewLifecycle(
				WithLifecycleLease(stores.Lease{RunID: "run-1"}),
				WithLifecycleSession("s1"),
				WithLifecycleFlow("flights"),
			))
		if !errors.Is(err, types.ErrApproverNotEligible) {
			t.Fatalf("got %v, want ErrApproverNotEligible", err)
		}
		if len(saved) != 0 {
			t.Fatalf("refused suspension persisted %d checkpoints", len(saved))
		}
	})
}
