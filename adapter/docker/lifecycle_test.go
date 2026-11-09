package docker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// casFaultMeta decorates only the SessionWorkspaceMeta writes: on the
// second SetWorkspaceMeta it performs one competing valid metadata
// update through the real store, then forwards the stale expected
// generation, so the later publication loses a genuine CAS.
type casFaultMeta struct {
	*stores.MemorySessionLog

	competing int
}

func (m *casFaultMeta) SetWorkspaceMeta(ctx context.Context, sessionID string, expectedGeneration int64, rec stores.WorkspaceMeta) (int64, error) {
	m.competing++
	if m.competing == 2 {
		if _, err := m.MemorySessionLog.SetWorkspaceMeta(ctx, sessionID, expectedGeneration, stores.WorkspaceMeta{
			Ref:        rec.Ref,
			Generation: expectedGeneration + 1,
			Owner:      rec.Owner,
		}); err != nil {
			return 0, fmt.Errorf("competing metadata update: %w", err)
		}
	}
	return m.MemorySessionLog.SetWorkspaceMeta(ctx, sessionID, expectedGeneration, rec)
}

// TestSandboxSnapshotCAS proves a lost metadata generation CAS keeps the
// earlier committed image and the winner's record: the store refuses the
// stale write, the previous ref restores its original bytes and no prior
// image is retagged or removed.
func TestSandboxSnapshotCAS(t *testing.T) {
	if testing.Short() {
		t.Skip("gohan: CAS run needs a live daemon; skipped in short mode")
	}
	t.Run("sandbox.snapshot-cas-failure-keeps-content", func(t *testing.T) {
		ctx := context.Background()
		sb := newTestSandbox(t)

		runs := stores.NewMemoryRuns()
		meta := &casFaultMeta{MemorySessionLog: stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithSessionWorkspaceOwnerGuard(runs),
			stores.WithSessionLeases(runs),
		)}
		pod, err := sandbox.NewHandles(sb, types.SandboxPolicy{}, meta, sandbox.WithCleanupStore(meta))
		if err != nil {
			t.Fatalf("NewHandles: %v", err)
		}

		pctx := types.WithPrincipal(ctx, types.Principal{Tenant: "acme", Subject: "alice"})
		if _, err := meta.Append(pctx, "s-cas", 0, types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "start"}},
		}); err != nil {
			t.Fatal(err)
		}
		lease, err := runs.Start(pctx, stores.Run{SessionID: "s-cas", RunID: "run-cas", Flow: "flights", Backend: "scripted"}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		owner := types.WorkspaceOwner{RunID: "run-cas", LeaseGeneration: lease.Generation}
		rctx := types.WithRunInfo(pctx, types.RunInfo{SessionID: "s-cas", RunID: "run-cas"})

		w, err := pod.Acquire(rctx, "s-cas", owner)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := w.Put(ctx, "/tmp/data.txt", strings.NewReader("alpha-bytes\n")); err != nil {
			t.Fatalf("put A: %v", err)
		}
		snapA, err := pod.Publish(rctx, "s-cas", owner)
		if err != nil {
			t.Fatalf("publish A: %v", err)
		}
		inspA, err := sb.c.api.ImageInspect(ctx, string(snapA.Ref))
		if err != nil {
			t.Fatalf("inspect A: %v", err)
		}
		tagsA := slices.Clone(inspA.RepoTags)

		if err := w.Put(ctx, "/tmp/data.txt", strings.NewReader("beta-bytes\n")); err != nil {
			t.Fatalf("put B: %v", err)
		}
		before := labelledImageIDs(t, sb)
		_, err = pod.Publish(rctx, "s-cas", owner)
		if !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("stale publish error = %v, want types.ErrVersionConflict", err)
		}
		if meta.competing != 2 {
			t.Fatalf("metadata writes = %d, want the competing update injected exactly once on the second write", meta.competing)
		}

		// The winner's metadata is preserved unchanged by the loser.
		rec, ok, err := meta.WorkspaceMeta(rctx, "s-cas")
		if err != nil || !ok {
			t.Fatalf("workspace meta after conflict ok %v err %v, want the winner retained", ok, err)
		}
		if rec.Generation != 2 {
			t.Fatalf("metadata generation = %d, want the winner's 2", rec.Generation)
		}

		// No prior image was retagged or removed: A keeps its exact ID
		// and tags, and exactly one unpublished image appeared.
		inspAfter, err := sb.c.api.ImageInspect(ctx, string(snapA.Ref))
		if err != nil {
			t.Fatalf("inspect A after conflict: %v", err)
		}
		if inspAfter.ID != inspA.ID {
			t.Fatalf("ref %s now resolves to image %q, want the untouched prior commit %q", snapA.Ref, inspAfter.ID, inspA.ID)
		}
		if !slices.Equal(inspAfter.RepoTags, tagsA) {
			t.Fatalf("A tags = %v, want the original %v", inspAfter.RepoTags, tagsA)
		}
		after := labelledImageIDs(t, sb)
		var newIDs []string
		for id := range after {
			if !before[id] {
				newIDs = append(newIDs, id)
			}
		}
		if len(newIDs) != 1 || newIDs[0] == inspA.ID {
			t.Fatalf("new labelled images after conflict = %v, want exactly the unpublished losing commit", newIDs)
		}

		// Restoring the earlier committed reference still yields A.
		restA, err := sb.Open(ctx, snapA.Ref, types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("restore A after conflict: %v", err)
		}
		if got := readFile(t, restA, "/tmp/data.txt"); got != "alpha-bytes\n" {
			t.Fatalf("restored A after conflict = %q, want %q", got, "alpha-bytes\n")
		}
		if err := restA.Close(ctx); err != nil {
			t.Fatalf("close restore: %v", err)
		}

		t.Cleanup(func() {
			_, _ = sb.c.api.ImageRemove(ctx, newIDs[0], client.ImageRemoveOptions{})
			_ = sb.CloseByRef(ctx, snapA.Ref)
		})
	})
}
