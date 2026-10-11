package sandboxtest

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/sandbox"
)

// The compile-time half of sandbox.egress-shares-policy-type: one
// egress vocabulary, no second struct.
var (
	_ types.EgressPolicy  = types.SandboxPolicy{}.Egress
	_ *types.EgressPolicy = types.ToolSpec{}.Egress
)

// egressSharesPolicyType proves sandbox.egress-shares-policy-type: the
// shared type at compile time and the private-range denial at the same
// seam. The Explain policy-shape half of the scenario stays open (design
// O2): chains.ToolInfo carries no egress field, so nothing renders a
// policy shape yet and the suite asserts no private shape in its place.
func egressSharesPolicyType(t *testing.T, f Factory) {
	t.Helper()
	t.Run("shared-type", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"api.example.com"}
		ts, err := sandbox.New(f.Sandbox(t), p)
		if err != nil {
			t.Fatalf("build sandbox tool set: %v", err)
		}
		for _, tool := range ts.Tools() {
			if tool.Spec().Egress == nil {
				t.Fatalf("tool %s carries no egress policy, want the shared types.EgressPolicy", tool.Spec().Name)
			}
			if got := tool.Spec().Egress.Allow; len(got) != 1 || got[0] != "api.example.com" {
				t.Fatalf("tool %s egress allow = %v, want [api.example.com]", tool.Spec().Name, got)
			}
		}
	})
	t.Run("private-range-denied", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"api.example.com"}
		p.Egress.PrivateRanges = types.DenyPrivate
		w := open(t, f.Sandbox(t), p)
		rec := f.Recorder()
		rec.Reset()
		before := rec.Denied()
		res := exec(t, w, f.Probe().Dial("10.0.0.7", 9090))
		if res.ExitCode == 0 {
			t.Fatal("dial into a private range succeeded, want denial")
		}
		denyObserved(t, rec, before)
	})
}
