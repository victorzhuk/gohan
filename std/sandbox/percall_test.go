package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type panicWorkspace struct{ *fakeWorkspace }

func (w panicWorkspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	panic("exec exploded")
}

func TestPerCallLifecycle(t *testing.T) {
	t.Run("normal call closes once", func(t *testing.T) {
		sb, w := newFake(false)
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "echo hi"})); err != nil {
			t.Fatalf("run_code: %v", err)
		}
		if sb.opens != 1 {
			t.Errorf("opens = %d, want 1", sb.opens)
		}
		if w.closes != 1 {
			t.Errorf("closes = %d, want exactly 1", w.closes)
		}
	})
	t.Run("panicking call closes once", func(t *testing.T) {
		_, w := newFake(false)
		sb := &fakeSandbox{ws: panicWorkspace{fakeWorkspace: w}}
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Error("run_code did not panic")
				}
			}()
			_, _ = ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "boom"}))
		}()
		if w.closes != 1 {
			t.Errorf("closes = %d, want exactly 1", w.closes)
		}
	})
	t.Run("per session call path is unsupported", func(t *testing.T) {
		sb, _ := newFake(false)
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerSession})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "echo hi"}))
		if !errors.Is(err, types.ErrPerSessionUnsupported) {
			t.Fatalf("err = %v, want ErrPerSessionUnsupported", err)
		}
	})
}
