package flags

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
)

type scriptFlags struct {
	values map[string]any
	at     time.Time
}

func (f *scriptFlags) eval(key string, def any) (any, types.Freshness) {
	if v, ok := f.values[key]; ok {
		return v, types.Freshness{EvaluatedAt: f.at}
	}
	return def, types.Freshness{EvaluatedAt: f.at, FromDefault: true}
}

func (f *scriptFlags) Bool(_ context.Context, key string, def bool, _ types.FlagContext) (bool, types.Freshness, error) {
	v, fr := f.eval(key, def)
	return v.(bool), fr, nil
}

func (f *scriptFlags) String(_ context.Context, key string, def string, _ types.FlagContext) (string, types.Freshness, error) {
	v, fr := f.eval(key, def)
	return v.(string), fr, nil
}

func (f *scriptFlags) Int(_ context.Context, key string, def int64, _ types.FlagContext) (int64, types.Freshness, error) {
	v, fr := f.eval(key, def)
	return v.(int64), fr, nil
}

func TestProvider(t *testing.T) {
	t.Setenv("GOHAN_FLAG_KILL_DEPLOY", "true")
	p := NewProvider(map[string]any{"limit": 3, "greeting": "hi"})
	fc := types.FlagContext{Session: "s1"}

	t.Run("env override is fresh", func(t *testing.T) {
		v, fr, err := p.Bool(context.Background(), "kill_deploy", false, fc)
		if err != nil || !v || fr.FromDefault {
			t.Fatalf("Bool = %v, %+v, %v", v, fr, err)
		}
	})

	t.Run("static value is fresh", func(t *testing.T) {
		if _, fr, _ := p.Int(context.Background(), "limit", 0, fc); fr.FromDefault {
			t.Fatal("static int counted as default")
		}
		if v, _, _ := p.String(context.Background(), "greeting", "", fc); v != "hi" {
			t.Fatalf("String = %q", v)
		}
	})

	t.Run("missing key falls back to default", func(t *testing.T) {
		v, fr, err := p.Bool(context.Background(), "unset", false, fc)
		if err != nil || v || !fr.FromDefault {
			t.Fatalf("Bool = %v, %+v, %v", v, fr, err)
		}
	})

	t.Run("malformed env value errors", func(t *testing.T) {
		t.Setenv("GOHAN_FLAG_BADNUM", "nope")
		if _, _, err := p.Int(context.Background(), "badnum", 0, fc); err == nil {
			t.Fatal("accepted malformed int")
		}
	})
}

func TestFlagSnapshot(t *testing.T) {
	base := time.Unix(1700000000, 0)
	f := &scriptFlags{values: map[string]any{"assist_v2": "b"}, at: base}
	fc := types.FlagContext{Tenant: "acme", Subject: "u1", Session: "s1", Flow: "agent"}
	snap, err := gohan.SnapshotFlags(context.Background(), f, fc, map[string]any{"assist_v2": "a"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	t.Run("flags.frozen-snapshot", func(t *testing.T) {
		if snap["assist_v2"] != "b" {
			t.Fatalf("snapshot = %v, want the provider value", snap)
		}
		f.values["assist_v2"] = "z"
		if snap["assist_v2"] != "b" {
			t.Fatalf("snapshot changed with the provider: %v", snap)
		}
	})

	t.Run("targeting key hashes the session", func(t *testing.T) {
		key := gohan.TargetingKey("s1")
		if key == "s1" || key != gohan.TargetingKey("s1") {
			t.Fatal("targeting key is not a stable session hash")
		}
	})
}

func TestFreshnessWatcher(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &scriptFlags{values: map[string]any{}, at: now}
	control := &Control{
		Flags:          f,
		FC:             types.FlagContext{Session: "s1", Flow: "agent"},
		Keys:           []string{"kill_deploy"},
		FreshnessLimit: time.Minute,
		MaxControlWait: time.Minute,
		Now:            func() time.Time { return now },
	}

	t.Run("flags.from-default-suspends", func(t *testing.T) {
		action, reason, waiting := control.evaluate(context.Background())
		if action != types.TaintAsk || !waiting || reason != permission.ControlWaitReason {
			t.Fatalf("evaluate = %v, %q, %v, want the control wait", action, reason, waiting)
		}
	})

	t.Run("watcher resumes with the empty wake once fresh", func(t *testing.T) {
		f.values["kill_deploy"] = false
		f.at = now.Add(time.Minute)
		resumed := false
		w := Watcher{Control: control, Interval: time.Millisecond, Resume: func(context.Context) error {
			resumed = true
			return nil
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := w.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !resumed {
			t.Fatal("watcher did not resume on fresh state")
		}
	})

	t.Run("stale then denied past the control wait", func(t *testing.T) {
		f.values = nil
		f.at = now
		action, _, _ := control.evaluate(context.Background())
		if action != types.TaintAsk {
			t.Fatalf("action = %v, want control wait", action)
		}
		now = now.Add(2 * time.Minute)
		action, reason, _ := control.evaluate(context.Background())
		if action != types.TaintDeny || reason == "" {
			t.Fatalf("evaluate = %v, %q, want denial past MaxControlWait", action, reason)
		}
	})
}

func TestModelCallControl(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &scriptFlags{values: map[string]any{}, at: now}
	control := &Control{
		Flags:          f,
		FC:             types.FlagContext{Session: "s1", Flow: "agent"},
		Keys:           []string{"kill_model"},
		FreshnessLimit: time.Minute,
		MaxControlWait: time.Minute,
		Now:            func() time.Time { return now },
	}
	step := ModelStep(control)
	call := step.Use(func(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) { yield(types.ModelChunk{}, nil) }
	})
	run := func(ctx context.Context) (types.ModelChunk, error) {
		var ch types.ModelChunk
		var err error
		call(ctx, types.ModelRequest{})(func(c types.ModelChunk, e error) bool {
			ch, err = c, e
			return false
		})
		return ch, err
	}
	ctx := context.Background()

	t.Run("flags.from-default-suspends", func(t *testing.T) {
		_, err := run(ctx)
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("err = %v, want AwaitingControl suspension", err)
		}
		if susp.Reason != types.AwaitingControl || susp.WakeAt.IsZero() {
			t.Fatalf("suspension = %+v, want AwaitingControl with a wake", susp)
		}
	})

	t.Run("fresh evaluation passes through", func(t *testing.T) {
		f.values["kill_model"] = false
		if _, err := run(ctx); err != nil {
			t.Fatalf("err = %v, want the call to pass", err)
		}
	})

	t.Run("kill switch blocks the call", func(t *testing.T) {
		f.values["kill_model"] = true
		if _, err := run(ctx); err == nil || errors.Is(err, &types.SuspendError{}) {
			t.Fatalf("err = %v, want a block", err)
		}
	})

	t.Run("step carries the flags kind", func(t *testing.T) {
		if step.Kind != chains.KindUser || step.Name == "" {
			t.Fatalf("step = %+v", step)
		}
	})
}
