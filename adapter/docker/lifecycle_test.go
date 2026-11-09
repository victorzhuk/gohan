package docker

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/sandbox"
)

// TestDockerKilledPodCleanup is the task 6.5 provider leg: a labelled
// container whose owner lease expired is enumerated and closed by resource
// identity from the next pod's coordinator Sweep, while a handle under a
// live lease is excluded. The store clock drives lease expiry; the daemon
// is shared, so every container the test opens is removed on cleanup.
func TestDockerKilledPodCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("gohan: lifecycle run needs a live daemon; skipped in short mode")
	}
	sb := newTestSandbox(t)
	ctx := context.Background()

	// Allowed egress runs every acquired workspace through the trusted
	// anchor lifecycle under the real Acquire, Publish and Sweep
	// sequence.
	lifecyclePolicy := types.SandboxPolicy{}
	lifecyclePolicy.Egress.Allow = []string{"example.com:80"}

	// newLifecycleStore builds the shared durable authority of the two
	// pods: one runs store whose clock the test advances and the session
	// log wired with the same guard instance.
	newLifecycleStore := func(t *testing.T) (*stores.MemoryRuns, *stores.MemorySessionLog, func(time.Duration)) {
		t.Helper()
		clock := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
		runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(func() time.Time { return clock }))
		log := stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithSessionWorkspaceOwnerGuard(runs),
			stores.WithSessionLeases(runs),
		)
		return runs, log, func(d time.Duration) { clock = clock.Add(d) }
	}

	newPod := func(t *testing.T, log *stores.MemorySessionLog) *sandbox.Handles {
		t.Helper()
		h, err := sandbox.NewHandles(sb, lifecyclePolicy, log, sandbox.WithCleanupStore(log))
		if err != nil {
			t.Fatalf("NewHandles: %v", err)
		}
		return h
	}

	// seed starts the run and returns the owner plus the run context the
	// coordinator's store proof reads the principal and run identity from.
	seed := func(t *testing.T, runs *stores.MemoryRuns, log *stores.MemorySessionLog, sessionID, runID string, ttl time.Duration) (types.WorkspaceOwner, context.Context) {
		t.Helper()
		pctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "acme", Subject: "alice"})
		if _, err := log.Append(pctx, sessionID, 0, types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "start"}},
		}); err != nil {
			t.Fatal(err)
		}
		lease, err := runs.Start(pctx, stores.Run{SessionID: sessionID, RunID: runID, Flow: "flights", Backend: "scripted"}, ttl)
		if err != nil {
			t.Fatal(err)
		}
		return types.WorkspaceOwner{RunID: runID, LeaseGeneration: lease.Generation},
			types.WithRunInfo(pctx, types.RunInfo{SessionID: sessionID, RunID: runID})
	}

	enumerate := func(t *testing.T) []types.WorkspaceRef {
		t.Helper()
		refs, err := sb.Enumerate(ctx)
		if err != nil {
			t.Fatalf("enumerate: %v", err)
		}
		return refs
	}

	t.Run("sandbox.docker-killed-pod-swept-by-next-pod", func(t *testing.T) {
		runs, log, advance := newLifecycleStore(t)
		owner, rctx := seed(t, runs, log, "s-dead", "run-dead", time.Second)

		podA := newPod(t, log)
		w, err := podA.Acquire(rctx, "s-dead", owner)
		if err != nil {
			t.Fatalf("pod A acquire: %v", err)
		}
		deadID := w.(types.WorkspaceResource).ResourceRef()
		t.Cleanup(func() { _ = sb.CloseByRef(ctx, deadID) })

		// The publication outlives the sweep: the durable snapshot ref
		// must stay while the container record is cleaned.
		snap, err := podA.Publish(rctx, "s-dead", owner)
		if err != nil {
			t.Fatalf("pod A publish: %v", err)
		}
		t.Cleanup(func() {
			_, _ = sb.c.api.ImageRemove(ctx, string(snap.Ref), client.ImageRemoveOptions{})
		})

		// The owner process dies: the handle is never released and the
		// lease expires on the store clock.
		advance(2 * time.Second)
		if runs.SessionLeaseActive(ctx, "s-dead") {
			t.Fatal("lease should be expired before the sweep")
		}

		podB := newPod(t, log)
		if err := podB.Sweep(ctx, 5); err != nil {
			t.Fatalf("pod B sweep: %v", err)
		}

		if slices.Contains(enumerate(t), deadID) {
			t.Fatalf("swept container %s still enumerated", deadID)
		}
		recs, err := log.CleanupCandidates(rctx, 10)
		if err != nil {
			t.Fatalf("cleanup candidates: %v", err)
		}
		if len(recs) != 0 {
			t.Fatalf("cleanup records after sweep = %d, want 0", len(recs))
		}
		meta, ok, err := log.WorkspaceMeta(rctx, "s-dead")
		if err != nil || !ok || meta.Ref != snap.Ref {
			t.Fatalf("snapshot metadata after sweep = %+v ok %v err %v, want ref %s retained", meta, ok, err, snap.Ref)
		}
	})

	t.Run("sandbox.docker-live-handle-excluded-from-sweep", func(t *testing.T) {
		runs, log, advance := newLifecycleStore(t)
		owner, rctx := seed(t, runs, log, "s-live", "run-live", time.Minute)

		pod := newPod(t, log)
		w, err := pod.Acquire(rctx, "s-live", owner)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		liveID := w.(types.WorkspaceResource).ResourceRef()
		t.Cleanup(func() {
			_ = w.Close(ctx)
			_ = sb.CloseByRef(ctx, liveID)
		})

		advance(time.Second)
		if err := pod.Sweep(ctx, 5); err != nil {
			t.Fatalf("sweep with a live lease: %v", err)
		}

		if !slices.Contains(enumerate(t), liveID) {
			t.Fatalf("live container %s removed by the sweep", liveID)
		}
		res, err := w.Exec(ctx, types.ExecRequest{Argv: []string{"echo", "alive"}})
		if err != nil || res.ExitCode != 0 || string(res.Stdout) != "alive\n" {
			t.Fatalf("live exec after sweep: %+v err %v", res, err)
		}
		recs, err := log.CleanupCandidates(rctx, 10)
		if err != nil || len(recs) != 1 {
			t.Fatalf("cleanup records after refused sweep = %d err %v, want 1", len(recs), err)
		}
	})
}
