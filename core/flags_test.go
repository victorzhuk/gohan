package gohan

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/flags"
)

type staticFlags struct {
	values map[string]any
}

type countingFlags struct {
	staticFlags
	evals int
}

func (f *countingFlags) Bool(ctx context.Context, key string, def bool, fc types.FlagContext) (bool, types.Freshness, error) {
	f.evals++
	return f.staticFlags.Bool(ctx, key, def, fc)
}

func (f *countingFlags) String(ctx context.Context, key string, def string, fc types.FlagContext) (string, types.Freshness, error) {
	f.evals++
	return f.staticFlags.String(ctx, key, def, fc)
}

func (f staticFlags) lookup(key string, def any) (any, types.Freshness) {
	if v, ok := f.values[key]; ok {
		return v, types.Freshness{EvaluatedAt: time.Now().UTC()}
	}
	return def, types.Freshness{EvaluatedAt: time.Now().UTC(), FromDefault: true}
}

func (f staticFlags) Bool(_ context.Context, key string, def bool, _ types.FlagContext) (bool, types.Freshness, error) {
	v, fr := f.lookup(key, def)
	return v.(bool), fr, nil
}

func (f staticFlags) String(_ context.Context, key string, def string, _ types.FlagContext) (string, types.Freshness, error) {
	v, fr := f.lookup(key, def)
	return v.(string), fr, nil
}

func (f staticFlags) Int(_ context.Context, key string, def int64, _ types.FlagContext) (int64, types.Freshness, error) {
	v, fr := f.lookup(key, def)
	return v.(int64), fr, nil
}

func TestFlagsPort(t *testing.T) {
	f := staticFlags{values: map[string]any{"rollout": "b", "cap": int64(3)}}
	fc := types.FlagContext{Tenant: "acme", Subject: "u1", Session: "s1", Flow: "agent"}

	t.Run("snapshot honours provider values", func(t *testing.T) {
		snap, err := SnapshotFlags(context.Background(), f, fc, map[string]any{
			"rollout": "a",
			"cap":     1,
			"off":     false,
		})
		if err != nil {
			t.Fatalf("SnapshotFlags: %v", err)
		}
		if snap["rollout"] != "b" {
			t.Fatalf("rollout = %#v (%T), want \"b\"", snap["rollout"], snap["rollout"])
		}
		if snap["cap"] != int64(3) {
			t.Fatalf("cap = %#v (%T), want int64(3)", snap["cap"], snap["cap"])
		}
		if snap["off"] != false {
			t.Fatalf("off = %#v (%T), want false", snap["off"], snap["off"])
		}
	})

	t.Run("unsupported frozen type fails the snapshot", func(t *testing.T) {
		if _, err := SnapshotFlags(context.Background(), f, fc, map[string]any{"bad": 1.5}); err == nil {
			t.Fatal("accepted float frozen default")
		}
	})
}

func TestRunInfoFlags(t *testing.T) {
	info := types.RunInfo{Flow: "agent", SessionID: "s1", RunID: "r1", Flags: map[string]any{"rollout": "b"}}
	ctx := types.WithRunInfo(context.Background(), info)

	t.Run("snapshot readable from ctx inside a run", func(t *testing.T) {
		snap, ok := types.FlagsFrom(ctx)
		if !ok || snap["rollout"] != "b" {
			t.Fatalf("FlagsFrom = %v, %v", snap, ok)
		}
	})

	t.Run("outside a run no snapshot", func(t *testing.T) {
		if _, ok := types.FlagsFrom(context.Background()); ok {
			t.Fatal("flags outside a run")
		}
	})
}

func TestFlagSnapshotPersisted(t *testing.T) {
	f := staticFlags{values: map[string]any{"assist_v2": "b"}}
	stack, err := Build(
		WithFlags(f),
		WithFrozenFlags(map[string]any{"assist_v2": "a"}),
		WithStores(stores.Stores{Audit: stores.NewMemoryAuditLog()}),
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	runs := stores.NewMemoryRuns()
	log := stores.NewMemoryEventLog()
	conv, err := NewConversation(stack, "agent", stubFlagsRuntime{}, WithConversationRuns(runs), WithConversationEventLog(log))
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	c := conv.(*conversation)
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "acme", Subject: "u1"})
	runCtx := c.runIdentityCtx(ctx, "run-1", "s1")
	info, _ := types.RunInfoFrom(runCtx)
	if info.Flags["assist_v2"] != "b" {
		t.Fatalf("RunInfo.Flags = %v, want frozen snapshot", info.Flags)
	}

	t.Run("flags.frozen-snapshot", func(t *testing.T) {
		f2 := staticFlags{values: map[string]any{"assist_v2": "c"}}
		snap, err := SnapshotFlags(context.Background(), f2, types.FlagContext{Session: "s1", Flow: "agent"}, map[string]any{"assist_v2": "a"})
		if err != nil {
			t.Fatalf("SnapshotFlags: %v", err)
		}
		f2.values["assist_v2"] = "z"
		if snap["assist_v2"] != "c" {
			t.Fatalf("snapshot changed with the provider: %v", snap)
		}
		if info.Flags["assist_v2"] != "b" {
			t.Fatalf("run snapshot changed with the provider: %v", info.Flags)
		}
	})

	t.Run("checkpoint state carries the snapshot", func(t *testing.T) {
		st := runtime.State{Flags: runtime.FlagSnapshot(info.Flags)}
		if st.Flags["assist_v2"] != "b" {
			t.Fatalf("State.Flags = %v", st.Flags)
		}
	})

	t.Run("run-start audit record", func(t *testing.T) {
		audit := stack.stores.Audit
		found := false
		for rec, err := range audit.Read(context.Background(), "s1") {
			if err != nil {
				t.Fatalf("audit read: %v", err)
			}
			if rec.Kind == stores.AuditRunStarted && strings.Contains(rec.Reason, "assist_v2") {
				found = true
			}
		}
		if !found {
			t.Fatal("no run-start audit record with the snapshot")
		}
	})
}

func TestSteerCancelNoReevaluation(t *testing.T) {
	ctx := principalCtx(context.Background())
	sid := "sess-flags-steer"
	log := steerLog(t)
	runs := &cancelGateRuns{runsFixture: steerNewRuns(), gate: make(chan struct{})}
	f := &countingFlags{staticFlags: staticFlags{values: map[string]any{"assist_v2": "b"}}}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	stack.flags = f
	stack.frozen = map[string]any{"assist_v2": "a"}
	rt := &steerRT{log: log, sess: sid, gate: runs.gate, entered: make(chan struct{})}
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(events),
	)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	res := make(chan streamResult, 1)
	go func() { res <- collectStream(conv.Send(ctx, sid, userMsg("hi"))) }()
	<-rt.entered
	if f.evals == 0 {
		t.Fatal("run start evaluated no flags")
	}
	f.values["assist_v2"] = "z"
	baseline := f.evals
	if err := conv.Steer(ctx, sid, steerMsg("turn left", "steer-flags")); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	cancelErr := make(chan error, 1)
	go func() { cancelErr <- conv.Cancel(ctx, sid) }()
	got := <-res
	if got.err != nil {
		t.Fatalf("Send: %v", got.err)
	}
	if err := <-cancelErr; err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if f.evals != baseline {
		t.Fatalf("flags evaluated %d times across steer/cancel, want %d", f.evals, baseline)
	}
}

// cancelGateRuns releases the run's gate only after the cancel signal is
// recorded, so the run cannot reach a terminal state before Cancel resolves.
type cancelGateRuns struct {
	*runsFixture
	gate chan struct{}
}

func (r *cancelGateRuns) Signal(ctx context.Context, runID string, s stores.Signal) error {
	err := r.runsFixture.Signal(ctx, runID, s)
	if s.Kind == stores.SignalCancel {
		close(r.gate)
	}
	return err
}

func TestControlWait(t *testing.T) {
	now := time.Unix(1700000000, 0)
	control := &flags.Control{
		Flags:          staticFlags{},
		FC:             types.FlagContext{Session: "s1", Flow: "agent"},
		Keys:           []string{"kill_deploy"},
		FreshnessLimit: time.Minute,
		MaxControlWait: time.Minute,
		Now:            func() time.Time { return now },
	}
	spec := types.ToolSpec{Name: "deploy", Effect: types.SideEffect}
	gate := permission.Gate(nil,
		WithSpecLookupFunc(spec),
		WithRunInfoFunc(types.RunInfo{}),
		permission.WithLiveControl(control.Check),
	)
	next := func(context.Context, types.ToolUse) (types.ToolResult, error) {
		t.Fatal("suspended call reached tool")
		return types.ToolResult{}, nil
	}

	t.Run("flags.from-default-suspends", func(t *testing.T) {
		_, err := gate(next)(context.Background(), types.ToolUse{ID: "c1", Name: "deploy"})
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("err = %v, want AwaitingControl suspension", err)
		}
		if susp.Reason != types.AwaitingControl {
			t.Fatalf("reason = %v, want AwaitingControl", susp.Reason)
		}
		if susp.WakeAt.IsZero() {
			t.Fatal("suspension carries no wake time")
		}
	})

	t.Run("flags.stale-then-deny", func(t *testing.T) {
		now = now.Add(2 * time.Minute)
		res, err := gate(next)(context.Background(), types.ToolUse{ID: "c2", Name: "deploy"})
		if err != nil {
			t.Fatalf("err = %v, want permanent denial after the control wait", err)
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent)", res)
		}
	})
}

func TestLiveFlags(t *testing.T) {
	spec := types.ToolSpec{Name: "search", Effect: types.SideEffect, RequiredScopes: []string{"search:run"}}
	gateWith := func(check func(context.Context, *permission.ToolInvocation) (types.TaintAction, string), decider types.Decider[*permission.ToolInvocation, permission.Verdict]) types.ToolFunc {
		g := permission.Gate(decider,
			WithSpecLookupFunc(spec),
			WithRunInfoFunc(types.RunInfo{}),
			permission.WithLiveControl(check),
		)
		return g(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			return types.ToolResult{ID: "ok"}, nil
		})
	}

	t.Run("flags.never-widen", func(t *testing.T) {
		decider := &allowDecider{}
		res, err := gateWith(func(context.Context, *permission.ToolInvocation) (types.TaintAction, string) {
			return types.TaintAllow, ""
		}, decider)(context.Background(), types.ToolUse{ID: "c0", Name: "search"})
		if err != nil {
			t.Fatalf("err = %v, want the closed default to still ask", err)
		}
		if res.Outcome == types.Succeeded || decider.called {
			t.Fatalf("live allow widened the verdict: result %+v decider %v", res, decider.called)
		}
	})

	t.Run("unregistered tool stays denied", func(t *testing.T) {
		g := permission.Gate(nil,
			WithSpecLookupFunc(types.ToolSpec{}),
			WithRunInfoFunc(types.RunInfo{}),
			permission.WithLiveControl(func(context.Context, *permission.ToolInvocation) (types.TaintAction, string) {
				return types.TaintAllow, ""
			}),
		)
		res, _ := g(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("unknown tool ran")
			return types.ToolResult{}, nil
		})(context.Background(), types.ToolUse{ID: "c1", Name: "ghost"})
		if res.Outcome != types.Failed {
			t.Fatalf("result = %+v, want denial", res)
		}
	})

	t.Run("live deny beats taint ask", func(t *testing.T) {
		hookRan := false
		g := permission.Gate(nil,
			WithSpecLookupFunc(spec),
			WithRunInfoFunc(types.RunInfo{}),
			WithTaintHookFunc(func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
				hookRan = true
				return types.TaintAsk, nil
			}),
			permission.WithLiveControl(func(context.Context, *permission.ToolInvocation) (types.TaintAction, string) {
				return types.TaintDeny, "kill switch"
			}),
		)
		res, err := g(func(context.Context, types.ToolUse) (types.ToolResult, error) {
			t.Fatal("denied call reached tool")
			return types.ToolResult{}, nil
		})(context.Background(), types.ToolUse{ID: "c2", Name: "search"})
		if err != nil {
			t.Fatalf("err = %v, want failed result", err)
		}
		if hookRan {
			t.Fatal("taint hook ran after a live deny")
		}
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent)", res)
		}
	})
}

func TestReplayFlagDrift(t *testing.T) {
	t.Run("flags.replay-asserts-snapshot", func(t *testing.T) {
		f := staticFlags{values: map[string]any{"assist_v2": "z"}}
		err := AssertFlags(map[string]any{"assist_v2": "b"}, f, types.FlagContext{Session: "s1", Flow: "agent"})
		if !errors.Is(err, types.ErrFlagDrift) {
			t.Fatalf("err = %v, want ErrFlagDrift", err)
		}
		var drift types.FlagDrift
		if !errors.As(err, &drift) || drift.Key != "assist_v2" {
			t.Fatalf("err = %v, want FlagDrift naming the key", err)
		}
	})

	t.Run("honoured snapshot passes", func(t *testing.T) {
		f := staticFlags{values: map[string]any{"assist_v2": "b"}}
		if err := AssertFlags(map[string]any{"assist_v2": "b"}, f, types.FlagContext{Session: "s1", Flow: "agent"}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("resume fails the replay before any model effect", func(t *testing.T) {
		h := newAuthHarness(t)
		steps := 0
		h.rt.steps = []func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error){
			func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
				steps++
				return st, nil, runtime.DoneStatus, nil
			},
		}
		token := h.seed(t, "run-drift", types.Preempted, runtime.State{
			Turn: 1, HistoryVersion: 1,
			Flags: runtime.FlagSnapshot{"assist_v2": "b"},
		}, nil)
		stack := &Stack{
			stores: stores.Stores{SessionLog: h.log},
			flags:  staticFlags{values: map[string]any{"assist_v2": "z"}},
		}
		events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
		conv, err := NewConversation(stack, "flights", h.rt,
			WithConversationRuns(h.runs),
			WithConversationEventLog(events),
			WithConversationCheckpoints(h.cps),
			WithConversationCredentialSource(&authCreds{}),
		)
		if err != nil {
			t.Fatalf("NewConversation: %v", err)
		}
		ctx := types.WithPrincipal(context.Background(), authOwner)
		var drift error
		for _, err := range conv.Resume(ctx, token, Continue()) {
			drift = err
		}
		if !errors.Is(drift, types.ErrFlagDrift) {
			t.Fatalf("resume err = %v, want ErrFlagDrift", drift)
		}
		if steps != 0 {
			t.Fatalf("runtime ran %d steps after drift", steps)
		}
	})
}

type stubFlagsRuntime struct{}

func (stubFlagsRuntime) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (stubFlagsRuntime) Step(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	return runtime.State{}, nil, runtime.DoneStatus, nil
}

func (stubFlagsRuntime) Name() string                         { return "stub-flags" }
func (stubFlagsRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

type allowDecider struct {
	called bool
}

func (d *allowDecider) Decide(_ context.Context, _ *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	d.called = true
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

func WithSpecLookupFunc(spec types.ToolSpec) permission.Option {
	return permission.WithSpecLookup(func(string) (types.ToolSpec, bool) { return spec, spec.Name != "" })
}

func WithRunInfoFunc(info types.RunInfo) permission.Option {
	return permission.WithRunInfo(func(context.Context) types.RunInfo { return info })
}

func WithTaintHookFunc(hook types.TaintHook) permission.Option {
	return permission.WithTaintHook(hook)
}
