package sandboxtest

import (
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// egressDenied proves sandbox.egress-denied: with an empty Egress the
// dial probe fails inside the workspace and the refusal is observed.
func egressDenied(t *testing.T, f Factory) {
	t.Helper()
	w := open(t, f.Sandbox(t), types.SandboxPolicy{Lifecycle: types.PerCall})
	probe := f.Probe()
	rec := f.Recorder()
	rec.Reset()
	before := rec.Denied()
	res := exec(t, w, probe.Dial("api.example.com", 443))
	if res.ExitCode == 0 {
		t.Fatal("dial under deny-all egress succeeded, want refusal inside the workspace")
	}
	denyObserved(t, rec, before)
}

// egressAllowlisted proves sandbox.egress-allowlisted and the
// private-range rule at the same seam.
func egressAllowlisted(t *testing.T, f Factory) {
	t.Helper()
	t.Run("allowlisted_host", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"api.example.com"}
		w := open(t, f.Sandbox(t), p)
		probe := f.Probe()
		rec := f.Recorder()
		rec.Reset()
		res := exec(t, w, probe.Dial("api.example.com", 443))
		if res.ExitCode != 0 {
			t.Fatalf("dial to allowlisted host exit = %d, want 0", res.ExitCode)
		}
		before := rec.Denied()
		res = exec(t, w, probe.Dial("other.example.com", 443))
		if res.ExitCode == 0 {
			t.Fatal("dial to non-allowlisted host succeeded, want denial")
		}
		denyObserved(t, rec, before)
	})

	t.Run("private-range", func(t *testing.T) {
		t.Run("deny_private", func(t *testing.T) {
			p := types.SandboxPolicy{Lifecycle: types.PerCall}
			p.Egress.Allow = []string{"api.example.com"}
			p.Egress.PrivateRanges = types.DenyPrivate
			w := open(t, f.Sandbox(t), p)
			rec := f.Recorder()
			rec.Reset()
			before := rec.Denied()
			res := exec(t, w, f.Probe().Dial("192.168.1.10", 8080))
			if res.ExitCode == 0 {
				t.Fatal("dial into a private range under DenyPrivate succeeded, want denial")
			}
			denyObserved(t, rec, before)
		})
		t.Run("allow_private_positive_control", func(t *testing.T) {
			p := types.SandboxPolicy{Lifecycle: types.PerCall}
			p.Egress.Allow = []string{"api.example.com"}
			p.Egress.PrivateRanges = types.AllowPrivate
			w := open(t, f.Sandbox(t), p)
			rec := f.Recorder()
			rec.Reset()
			res := exec(t, w, f.Probe().Dial("192.168.1.10", 8080))
			if res.ExitCode != 0 {
				t.Fatalf("private dial under AllowPrivate exit = %d, want not refused by policy", res.ExitCode)
			}
			if rec.Denied() != 0 {
				t.Fatalf("denied observations = %d, want 0 under AllowPrivate", rec.Denied())
			}
		})
	})
}

// execTimeout proves sandbox.exec-timeout: the process group dies at
// MaxExecTime and Exec returns rather than hangs.
func execTimeout(t *testing.T, f Factory) {
	t.Helper()
	t.Run("kill", func(t *testing.T) {
		p := types.SandboxPolicy{Lifecycle: types.PerCall, MaxExecTime: 50 * time.Millisecond}
		w := open(t, f.Sandbox(t), p)
		res := exec(t, w, f.Probe().Sleep(10*time.Second))
		if res.ExitCode == 0 {
			t.Fatal("sleep probe past MaxExecTime exited zero, want a reported kill")
		}
	})
	t.Run("outcome", func(t *testing.T) { execTimeoutOutcome(t, f) })
}
