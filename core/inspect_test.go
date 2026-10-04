package gohan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func inspectStores(t *testing.T) (runs *stores.MemoryRuns, log *stores.MemorySessionLog, cps *stores.MemoryCheckpoints) {
	t.Helper()
	runs = stores.NewMemoryRuns()
	log = stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	cps = stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
	return runs, log, cps
}

func inspectStack(runs *stores.MemoryRuns, log *stores.MemorySessionLog, cps *stores.MemoryCheckpoints) *Stack {
	st, err := Build(WithStores(stores.Stores{SessionLog: log, Runs: runs, Checkpoints: cps}))
	if err != nil {
		panic(err)
	}
	return st
}

func inspectOwnerCtx(ctx context.Context) context.Context {
	return types.WithPrincipal(ctx, types.Principal{Tenant: "t1", Subject: "u1"})
}

// inspectSeedSession records the owner of a session the way the first
// append does.
func inspectSeedSession(t *testing.T, log *stores.MemorySessionLog, sessionID string) {
	t.Helper()
	if _, err := log.Append(inspectOwnerCtx(context.Background()), sessionID, 0, types.Message{Role: types.RoleUser}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func inspectSeedRun(t *testing.T, runs *stores.MemoryRuns, r stores.Run) {
	t.Helper()
	if _, err := runs.Start(context.Background(), r, stores.LeaseTTL); err != nil {
		t.Fatalf("seed run %s: %v", r.RunID, err)
	}
}

func TestInspect(t *testing.T) {
	t.Run("tools.inspect-from-another-pod", func(t *testing.T) {
		runs, log, cps := inspectStores(t)
		inspectSeedSession(t, log, "s1")
		inspectSeedRun(t, runs, stores.Run{
			SessionID: "s1", RunID: "r1", Flow: "f", State: stores.Running,
			Turn: 2, Seq: 5, Cost: 0.75,
			Pending: []types.ToolUse{{ID: "c1", Name: "create_booking"}},
		})
		// Pod 2 rebuilds its stack from the same stores: it must answer the
		// inspection from stores alone.
		peer := inspectStack(runs, log, cps)
		view, err := peer.Inspect(inspectOwnerCtx(context.Background()), "r1")
		if err != nil {
			t.Fatalf("inspect from second pod: %v", err)
		}
		if view.RunID != "r1" || view.SessionID != "s1" || view.Turn != 2 || view.Seq != 5 {
			t.Fatalf("view = %+v, want stored run row", view.Run)
		}
		if len(view.Pending) != 1 || view.Pending[0].Name != "create_booking" {
			t.Fatalf("pending = %v, want the stored call", view.Pending)
		}
	})

	t.Run("foreign principal is refused", func(t *testing.T) {
		runs, log, cps := inspectStores(t)
		inspectSeedSession(t, log, "s1")
		inspectSeedRun(t, runs, stores.Run{SessionID: "s1", RunID: "r1", Flow: "f"})
		st := inspectStack(runs, log, cps)

		foreign := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t2", Subject: "u9"})
		if _, err := st.Inspect(foreign, "r1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant inspect err = %v, want ErrSessionForbidden", err)
		}
		peer := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u2"})
		if _, err := st.Inspect(peer, "r1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("same-tenant other-subject inspect err = %v, want ErrSessionForbidden", err)
		}
		if _, err := st.Inspect(context.Background(), "r1"); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("principal-less inspect err = %v, want ErrNoPrincipal", err)
		}
	})

	t.Run("suspended run returns its input", func(t *testing.T) {
		runs, log, cps := inspectStores(t)
		inspectSeedSession(t, log, "s1")
		inspectSeedRun(t, runs, stores.Run{SessionID: "s1", RunID: "r1", Flow: "f"})
		putCtx := types.WithRunInfo(context.Background(), types.RunInfo{RunID: "r1"})
		tok, err := cps.Put(putCtx, stores.Checkpoint{SessionID: "s1", Flow: "f"})
		if err != nil {
			t.Fatalf("put checkpoint: %v", err)
		}
		lease := stores.Lease{RunID: "r1"}
		if err := runs.Suspend(context.Background(), lease, tok); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		in := stores.ResumeInput{Verdict: stores.VerdictApprove}
		if _, err := cps.Consume(context.Background(), tok, in); err != nil {
			t.Fatalf("consume: %v", err)
		}
		st := inspectStack(runs, log, cps)
		view, err := st.Inspect(inspectOwnerCtx(context.Background()), "r1")
		if err != nil {
			t.Fatalf("inspect suspended run: %v", err)
		}
		if view.State != stores.Suspended {
			t.Fatalf("state = %v, want Suspended", view.State)
		}
		if view.Input == nil || view.Input.Verdict != stores.VerdictApprove {
			t.Fatalf("input = %+v, want the delivered verdict", view.Input)
		}
	})

	t.Run("running run returns cost and pending calls with no limits value", func(t *testing.T) {
		runs, log, cps := inspectStores(t)
		inspectSeedSession(t, log, "s1")
		inspectSeedRun(t, runs, stores.Run{
			SessionID: "s1", RunID: "r1", Flow: "f", State: stores.Running,
			Turn: 3, Seq: 7, Cost: 1.5,
			Pending: []types.ToolUse{{ID: "c1", Name: "create_booking"}},
		})
		st := inspectStack(runs, log, cps)
		view, err := st.Inspect(inspectOwnerCtx(context.Background()), "r1")
		if err != nil {
			t.Fatalf("inspect running run: %v", err)
		}
		if view.Cost != 1.5 || view.Turn != 3 || view.Seq != 7 {
			t.Fatalf("view = %+v, want cost 1.5, turn 3, seq 7", view)
		}
		if len(view.Pending) != 1 || view.Pending[0].ID != "c1" {
			t.Fatalf("pending = %v, want c1", view.Pending)
		}
		if view.Input != nil {
			t.Fatalf("input = %+v, want none for a running run", view.Input)
		}
		// The prohibition is structural: RunView carries the run row and the
		// pending input, nothing derived from the run limits.
		if n := reflect.TypeOf(RunView{}).NumField(); n != 2 {
			t.Fatalf("RunView has %d fields, want run row plus input only", n)
		}
	})
}
