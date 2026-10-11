package sandboxtest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/sandbox"
)

// runCode drives one run_code call through the tool-call path and
// returns the classified result.
func runCode(t *testing.T, ts sandbox.ToolSet, ctx context.Context, code string) types.ToolResult {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatalf("marshal run_code args: %v", err)
	}
	set := gohan.ToolSetFunc(func(name string) (types.Tool, bool) {
		for _, tool := range ts.Tools() {
			if tool.Spec().Name == name {
				return tool, true
			}
		}
		return nil, false
	})
	res, err := gohan.CallTool(ctx, set, "run_code", raw)
	if err != nil {
		t.Fatalf("call run_code: %v", err)
	}
	return res
}

// execTimeoutOutcome proves the harness classification: the sleep probe
// runs past its deadline, the call errors with the deadline, and the
// governed path reports Unknown(OutcomeUnknown) under a SideEffect
// policy and Failed otherwise. The effect is read from the built tool
// set, never re-derived here.
func execTimeoutOutcome(t *testing.T, f Factory) {
	t.Helper()
	sleep := f.Probe().Sleep(10 * time.Second)
	code := sleep[0]
	for _, arg := range sleep[1:] {
		code += " " + arg
	}
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "side_effect_unknown", allowed: true},
		{name: "read_only_failed", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := types.SandboxPolicy{Lifecycle: types.PerCall, MaxExecTime: 50 * time.Millisecond}
			if tc.allowed {
				p.Egress.Allow = []string{"api.example.com"}
			}
			ts, err := sandbox.New(f.Sandbox(t), p)
			if err != nil {
				t.Fatalf("build sandbox tool set: %v", err)
			}
			effect := ts.Tools()[0].Spec().Effect
			wantEffect := types.ReadOnly
			if tc.allowed {
				wantEffect = types.SideEffect
			}
			if effect != wantEffect {
				t.Fatalf("derived effect = %v, want %v", effect, wantEffect)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			res := runCode(t, ts, ctx, code)
			if tc.allowed {
				if res.Outcome != types.Unknown || res.Error == nil || res.Error.Kind != types.OutcomeUnknown {
					t.Fatalf("side-effect outcome = %+v, want Unknown(OutcomeUnknown)", res)
				}
				return
			}
			if res.Outcome != types.Failed {
				t.Fatalf("read-only outcome = %+v, want Failed", res)
			}
		})
	}
}
