package docker

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestExecTimeoutKill(t *testing.T) {
	t.Run("request timeout", func(t *testing.T) {
		ctx := context.Background()
		w := newTestWorkspace(t)

		start := time.Now()
		res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"sleep", "30"}, Timeout: 2 * time.Second})
		if err != nil {
			t.Fatalf("timed-out exec: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("timed-out exec returned after %v, deadline was 2s", elapsed)
		}
		if res.ExitCode != -1 {
			t.Fatalf("timed-out exec exit code = %d, want -1", res.ExitCode)
		}

		check := sleepAlive(t, w)
		if check.ExitCode == 0 {
			t.Fatalf("sleep process survived its deadline: %+v", check)
		}
	})

	t.Run("policy max exec time", func(t *testing.T) {
		ctx := context.Background()
		sb, err := New(ctx, "")
		if err != nil {
			t.Skipf("docker daemon unavailable: %v", err)
		}
		defer sb.c.Close()
		w, err := sb.Open(ctx, "", types.SandboxPolicy{MaxExecTime: 2 * time.Second})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer w.Close(ctx)

		start := time.Now()
		res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"sleep", "30"}})
		if err != nil {
			t.Fatalf("timed-out exec: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("policy deadline ignored: exec returned after %v", elapsed)
		}
		if res.ExitCode != -1 {
			t.Fatalf("timed-out exec exit code = %d, want -1", res.ExitCode)
		}
	})
}

func sleepAlive(t *testing.T, w *workspace) types.ExecResult {
	t.Helper()
	res, err := w.Exec(context.Background(), types.ExecRequest{Argv: []string{"sh", "-c", "ps | grep '[s]leep 30'"}})
	if err != nil {
		t.Fatalf("probe exec: %v", err)
	}
	return res
}
