package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/sandbox"
)

// containerRefRE matches a container or network resource path.
var containerRefRE = regexp.MustCompile(`^/(containers|networks)/([^/]+)$`)

var _ types.SandboxResources = (*Sandbox)(nil)

func imageListByRef(ref string) client.ImageListOptions {
	return client.ImageListOptions{Filters: client.Filters{}.Add("reference", ref)}
}

func TestEnumerateCloseByRef(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)

	// The live handle's container is enumerated but never closed by the
	// reclaim path while the handle works.
	live, err := sb.Open(ctx, "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open live: %v", err)
	}
	t.Cleanup(func() { _ = live.Close(ctx) })

	// A dead pod's container: labelled, its handle dropped without a
	// close, exactly what a vanished pod leaves behind.
	dead, err := sb.Open(ctx, "", types.SandboxPolicy{})
	if err != nil {
		t.Fatalf("open dead: %v", err)
	}
	deadRef := dead.(types.WorkspaceResource).ResourceRef()
	deadID := dead.(*workspace).containerID
	t.Cleanup(func() { _ = sb.CloseByRef(ctx, deadRef) })

	refs, err := sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	refSet := map[types.WorkspaceRef]bool{}
	for _, r := range refs {
		refSet[r] = true
	}
	if !refSet[deadRef] {
		t.Fatalf("enumerate = %v, want the encoded ref %s of the labelled orphan", refs, deadRef)
	}

	if err := sb.CloseByRef(ctx, deadRef); err != nil {
		t.Fatalf("close by ref %s: %v", deadRef, err)
	}
	refs, err = sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate after close: %v", err)
	}
	if slices.Contains(refs, deadRef) || slices.Contains(refs, types.WorkspaceRef(deadID)) {
		t.Fatalf("closed ref %s still enumerated", deadRef)
	}

	// The live handle is untouched and still usable.
	res, err := live.Exec(ctx, types.ExecRequest{Argv: []string{"echo", "alive"}})
	if err != nil || res.ExitCode != 0 || string(res.Stdout) != "alive\n" {
		t.Fatalf("live exec after reclaim: %+v err %v", res, err)
	}

	// A snapshot is closed by its ref too.
	ref, err := live.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Cleanup(func() { _, _ = sb.c.api.ImageRemove(ctx, string(ref), client.ImageRemoveOptions{}) })
	refs, err = sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate with snapshot: %v", err)
	}
	if !slices.Contains(refs, ref) {
		t.Fatalf("enumerate = %v, want snapshot %s", refs, ref)
	}
	if err := sb.CloseByRef(ctx, ref); err != nil {
		t.Fatalf("close snapshot by ref: %v", err)
	}
	refs, err = sb.Enumerate(ctx)
	if err != nil {
		t.Fatalf("enumerate after snapshot close: %v", err)
	}
	if slices.Contains(refs, ref) {
		t.Fatalf("closed snapshot %s still enumerated", ref)
	}

	if _, err := live.Exec(ctx, types.ExecRequest{Argv: []string{"true"}}); err != nil {
		t.Fatalf("live exec after snapshot close: %v", err)
	}

	// A ref outside the labels is refused, not guessed.
	if err := sb.CloseByRef(ctx, "alpine:3.23"); err == nil {
		t.Fatal("close by unlabelled ref: want error")
	}
}

// recoveryLog carries the SessionWorkspaceCleanup surface over the real
// memory store. It observes each CompleteWorkspaceCleanup before
// forwarding: the exact fixture-owned group must be absent at the
// callback, which is the only point the durable record may complete.
type recoveryLog struct {
	*stores.MemorySessionLog

	api    *client.Client
	mu     sync.Mutex
	group  map[string]string // group member id -> kind
	purged []stores.WorkspaceCleanupRecord
}

func (l *recoveryLog) groupAbsent(ctx context.Context) error {
	for id := range l.group {
		if _, err := l.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err == nil {
			return fmt.Errorf("group member %s still present at completion", id)
		}
		if _, err := l.api.NetworkInspect(ctx, id, client.NetworkInspectOptions{}); err == nil {
			return fmt.Errorf("group member %s still present at completion", id)
		}
	}
	return nil
}

func (l *recoveryLog) CompleteWorkspaceCleanup(ctx context.Context, rec stores.WorkspaceCleanupRecord) error {
	l.mu.Lock()
	absent := l.groupAbsent(ctx)
	l.mu.Unlock()
	if absent != nil {
		return absent
	}
	l.mu.Lock()
	l.purged = append(l.purged, rec)
	l.mu.Unlock()
	return l.MemorySessionLog.CompleteWorkspaceCleanup(ctx, rec)
}

// recoveryStore wires one store clock, runs and the decorated cleanup log
// the same way the lifecycle fixtures do.
func recoveryStore(t *testing.T, api *client.Client) (*stores.MemoryRuns, *recoveryLog, func(time.Duration)) {
	t.Helper()
	clock := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(func() time.Time { return clock }))
	log := &recoveryLog{
		MemorySessionLog: stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithSessionWorkspaceOwnerGuard(runs),
			stores.WithSessionLeases(runs),
		),
		api:   api,
		group: map[string]string{},
	}
	return runs, log, func(d time.Duration) { clock = clock.Add(d) }
}

// recoverySeed starts one run and returns its owner and run context.
func recoverySeed(t *testing.T, runs *stores.MemoryRuns, log *recoveryLog, sessionID, runID string, ttl time.Duration) (types.WorkspaceOwner, context.Context) {
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

func recoveryPod(t *testing.T, sb *Sandbox, log *recoveryLog) *sandbox.Handles {
	t.Helper()
	p := types.SandboxPolicy{}
	p.Egress.Allow = []string{"example.com:80"}
	h, err := sandbox.NewHandles(sb, p, log, sandbox.WithCleanupStore(log))
	if err != nil {
		t.Fatalf("NewHandles: %v", err)
	}
	return h
}

// assertGone fails the test while any of the container or network IDs is
// still inspectable.
func assertGone(t *testing.T, api *client.Client, ids ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := api.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err == nil {
			t.Errorf("container %s still present, want removed", id)
		}
		if _, err := api.NetworkInspect(ctx, id, client.NetworkInspectOptions{}); err == nil {
			t.Errorf("network %s still present, want removed", id)
		}
	}
}

func TestSandboxAnchorRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("gohan: anchor recovery run needs a live daemon; skipped in short mode")
	}
	ctx := context.Background()
	egressPolicy := func() types.SandboxPolicy {
		p := types.SandboxPolicy{Lifecycle: types.PerCall}
		p.Egress.Allow = []string{"example.com:80"}
		return p
	}

	t.Run("crash_before_workload", func(t *testing.T) {
		f := newConformanceFactory(t)
		tr := newSandboxTransport()
		sb := decoratedSandbox(t, f, tr)
		base := f.c.BaseImage()

		// The daemon stream dies after the anchor exists and the normal
		// unwind loses its anchor removal, the crashed-pod shape.
		tr.arm("create_workload", func(_ *sandboxTransport, method, path string, body []byte) bool {
			if method != "POST" || path != "/containers/create" {
				return false
			}
			var req struct {
				Image string `json:"Image"`
			}
			_ = json.Unmarshal(body, &req)
			return req.Image == base && len(tr.containers) > 0
		})
		tr.arm("unwind_anchor_remove", func(tr2 *sandboxTransport, method, path string, _ []byte) bool {
			if method != "DELETE" {
				return false
			}
			m := containerRefRE.FindStringSubmatch(path)
			return m != nil && tr2.containers[m[2]] == base
		})

		w, err := sb.Open(ctx, "", egressPolicy())
		if err == nil && w != nil {
			t.Fatal("open with crashed workload create returned a workspace")
		}
		if tr.hits("unwind_anchor_remove") != 1 {
			t.Fatalf("unwind anchor removal consumed %d times, want once", tr.hits("unwind_anchor_remove"))
		}

		// Fresh coordinator: the unassociated labelled anchor and its
		// empty owned network are enumerated and reclaimed explicitly.
		fresh := f.Sandbox(t).(*Sandbox)
		var anchorID, owner string
		anchors, err := fresh.c.api.ContainerList(ctx, client.ContainerListOptions{
			All:     true,
			Filters: client.Filters{}.Add("label", labelRole+"="+roleAnchor),
		})
		if err != nil {
			t.Fatalf("list anchors: %v", err)
		}
		if len(anchors.Items) != 1 {
			t.Fatalf("crashed open left %d anchors, want the one unassociated anchor", len(anchors.Items))
		}
		anchorID = anchors.Items[0].ID
		owner = anchors.Items[0].Labels[labelOwner]

		refs, err := fresh.Enumerate(ctx)
		if err != nil {
			t.Fatalf("enumerate: %v", err)
		}
		if !slices.Contains(refs, types.WorkspaceRef(anchorID)) {
			t.Fatalf("enumerate = %v, want the orphan anchor %s", refs, anchorID)
		}
		if err := fresh.CloseByRef(ctx, types.WorkspaceRef(anchorID)); err != nil {
			t.Fatalf("close orphan anchor: %v", err)
		}
		assertGone(t, fresh.c.api, anchorID, owner)
	})

	t.Run("crash_before_record", func(t *testing.T) {
		f := newConformanceFactory(t)
		sb := f.Sandbox(t).(*Sandbox)
		w, err := sb.Open(ctx, "", egressPolicy())
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		ws := w.(*workspace)

		// No coordinator recording happened; the helper stays
		// protected while the owner exists, running or stopped.
		if err := sb.CloseByRef(ctx, types.WorkspaceRef(ws.anchorID)); err == nil {
			t.Fatal("close of anchor with running workload owner: want error")
		}
		if _, err := sb.c.api.ContainerStop(ctx, ws.containerID, client.ContainerStopOptions{}); err != nil {
			t.Fatalf("stop workload: %v", err)
		}
		if err := sb.CloseByRef(ctx, types.WorkspaceRef(ws.anchorID)); err == nil {
			t.Fatal("close of anchor with stopped workload owner: want error")
		}
		if err := sb.CloseByRef(ctx, types.WorkspaceRef(ws.containerID)); err == nil {
			t.Fatal("close by raw workload ID: want error")
		}

		if err := w.Close(ctx); err != nil {
			t.Fatalf("close workspace group: %v", err)
		}
		assertGone(t, sb.c.api, ws.containerID, ws.anchorID, ws.netID)
	})

	for _, tc := range []struct {
		name  string
		match func(tr *sandboxTransport, anchorID, netID, netName string) func(*sandboxTransport, string, string, []byte) bool
	}{
		{"anchor_remove", func(tr *sandboxTransport, anchorID, _, _ string) func(*sandboxTransport, string, string, []byte) bool {
			return func(tr2 *sandboxTransport, method, path string, _ []byte) bool {
				return method == "DELETE" && path == "/containers/"+anchorID
			}
		}},
		{"network_remove", func(tr *sandboxTransport, _, netID, netName string) func(*sandboxTransport, string, string, []byte) bool {
			return func(tr2 *sandboxTransport, method, path string, _ []byte) bool {
				if method != "DELETE" {
					return false
				}
				return path == "/networks/"+netID || path == "/networks/"+netName
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConformanceFactory(t)
			tr := newSandboxTransport()
			sb := decoratedSandbox(t, f, tr)
			runs, log, advance := recoveryStore(t, f.c.api)

			// A separate live owner and its committed snapshot are the
			// positive controls the sweeps must never touch.
			liveOwner, liveCtx := recoverySeed(t, runs, log, "s-live", "run-live", time.Minute)
			podLive := recoveryPod(t, sb, log)
			wLive, err := podLive.Acquire(liveCtx, "s-live", liveOwner)
			if err != nil {
				t.Fatalf("live acquire: %v", err)
			}
			t.Cleanup(func() { _ = podLive.Release(context.Background(), "s-live", liveOwner) })
			snap, err := podLive.Publish(liveCtx, "s-live", liveOwner)
			if err != nil {
				t.Fatalf("live publish: %v", err)
			}
			t.Cleanup(func() {
				_, _ = f.c.api.ImageRemove(context.Background(), string(snap.Ref), client.ImageRemoveOptions{})
			})

			owner, rctx := recoverySeed(t, runs, log, "s-retry", "run-retry", time.Second)
			podA := recoveryPod(t, sb, log)
			w, err := podA.Acquire(rctx, "s-retry", owner)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			recRef := w.(types.WorkspaceResource).ResourceRef()
			workloadID, oname, ok := parseWorkloadRef(string(recRef))
			if !ok {
				t.Fatalf("acquired ref %q is not a valid encoded workload identity", recRef)
			}
			ws := w.(*workspace)
			networks, err := sb.c.api.NetworkList(ctx, client.NetworkListOptions{
				Filters: client.Filters{}.Add("label", labelOwner+"="+oname),
			})
			if err != nil {
				t.Fatalf("list owned networks: %v", err)
			}
			if len(networks.Items) != 1 {
				t.Fatalf("owned networks = %d, want the one private network", len(networks.Items))
			}
			netID := networks.Items[0].ID
			netName := networks.Items[0].Name
			log.mu.Lock()
			log.group = map[string]string{workloadID: "container", ws.anchorID: "container", netID: "network"}
			log.mu.Unlock()

			tr.arm(tc.name, tc.match(tr, ws.anchorID, netID, netName))

			advance(2 * time.Second)
			if err := podA.Sweep(ctx, 5); err != nil {
				t.Fatalf("first sweep: %v", err)
			}
			if hits := tr.hits(tc.name); hits != 1 {
				t.Fatalf("armed fault %q consumed %d times, want exactly once", tc.name, hits)
			}
			// The workload is gone; the faulted phase remains; the
			// durable record stays pending. The live control session's
			// record also stays pending: a live lease refuses the
			// claim and waits for the next sweep.
			assertGone(t, f.c.api, workloadID)
			recs, err := log.CleanupCandidates(ctx, 10)
			if err != nil || !slices.ContainsFunc(recs, func(r stores.WorkspaceCleanupRecord) bool { return r.ResourceRef == recRef }) {
				t.Fatalf("cleanup candidates after failed sweep = %+v err %v, want the retained record with ref %s", recs, err, recRef)
			}
			if tc.name == "anchor_remove" {
				if _, err := f.c.api.ContainerInspect(ctx, ws.anchorID, client.ContainerInspectOptions{}); err != nil {
					t.Fatalf("anchor should remain after the faulted first sweep: %v", err)
				}
			} else {
				assertGone(t, f.c.api, ws.anchorID)
				if _, err := f.c.api.NetworkInspect(ctx, netID, client.NetworkInspectOptions{}); err != nil {
					t.Fatalf("network should remain after the faulted first sweep: %v", err)
				}
			}
			if len(log.purged) != 0 {
				t.Fatalf("cleanup record completed despite the failed group cleanup")
			}

			// Fresh coordinator, fresh stores wiring, no Enumerate, no
			// cached fields: the original encoded ref drives recovery.
			fresh := f.Sandbox(t).(*Sandbox)
			podB := recoveryPod(t, fresh, log)
			if err := podB.Sweep(ctx, 5); err != nil {
				t.Fatalf("recovery sweep: %v", err)
			}
			assertGone(t, f.c.api, workloadID, ws.anchorID, netID)
			log.mu.Lock()
			purged, absent := log.purged, log.groupAbsent(ctx)
			log.mu.Unlock()
			if absent != nil {
				t.Fatalf("group not absent at completion: %v", absent)
			}
			if len(purged) != 1 || purged[0].ResourceRef != recRef {
				t.Fatalf("completed records = %+v, want exactly the original ref %s", purged, recRef)
			}
			recs, err = log.CleanupCandidates(ctx, 10)
			if err != nil || slices.ContainsFunc(recs, func(r stores.WorkspaceCleanupRecord) bool { return r.ResourceRef == recRef }) {
				t.Fatalf("cleanup candidates after recovery = %+v err %v, want the recovered record completed", recs, err)
			}

			// The live control and its committed snapshot survive both
			// sweeps.
			if _, err := f.c.api.ImageInspect(ctx, string(snap.Ref)); err != nil {
				t.Fatalf("committed snapshot removed: %v", err)
			}
			if res, err := wLive.Exec(ctx, types.ExecRequest{Argv: []string{"echo", "alive"}}); err != nil || res.ExitCode != 0 {
				t.Fatalf("live control workspace disrupted: %+v err %v", res, err)
			}
		})
	}

	t.Run("ref_controls", func(t *testing.T) {
		f := newConformanceFactory(t)
		sb := f.Sandbox(t).(*Sandbox)
		w, err := sb.Open(ctx, "", types.SandboxPolicy{Lifecycle: types.PerCall})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		ws := w.(*workspace)
		ref := w.(types.WorkspaceResource).ResourceRef()
		t.Cleanup(func() { _ = sb.CloseByRef(ctx, ref) })

		// Malformed identities are refused before any destructive call.
		for _, bad := range []string{
			"gohan-workload:v2:" + ws.containerID + ":owner",
			"gohan-workload:v1",
			"gohan-workload:v1::owner",
			"gohan-workload:v1:" + ws.containerID,
			"alpine:3.23",
		} {
			if err := sb.CloseByRef(ctx, types.WorkspaceRef(bad)); err == nil {
				t.Errorf("close malformed ref %q: want error", bad)
			}
		}

		// A contradicting owner on the extant workload is refused.
		mismatched := types.WorkspaceRef(workloadRefPrefix + ws.containerID + ":other-owner")
		if err := sb.CloseByRef(ctx, mismatched); err == nil {
			t.Fatal("close with mismatched owner: want error")
		}
		if _, err := sb.c.api.ContainerInspect(ctx, ws.containerID, client.ContainerInspectOptions{}); err != nil {
			t.Fatalf("mismatched-owner refusal removed the workload: %v", err)
		}

		// A completely absent group closes idempotently.
		if err := w.Close(ctx); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := sb.CloseByRef(ctx, ref); err != nil {
			t.Fatalf("idempotent close of absent group: %v", err)
		}

		// Legacy containers without the workload role keep raw-ID
		// enumeration and removal.
		legacy, err := sb.c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
			Config: &container.Config{
				Image:  sb.c.BaseImage(),
				Cmd:    []string{"true"},
				Labels: map[string]string{LabelSandbox: "true"},
			},
		})
		if err != nil {
			t.Fatalf("create legacy container: %v", err)
		}
		t.Cleanup(func() {
			_, _ = sb.c.api.ContainerRemove(context.Background(), legacy.ID,
				client.ContainerRemoveOptions{Force: true})
		})
		refs, err := sb.Enumerate(ctx)
		if err != nil {
			t.Fatalf("enumerate: %v", err)
		}
		if !slices.Contains(refs, types.WorkspaceRef(legacy.ID)) {
			t.Fatalf("enumerate = %v, want legacy raw ID %s", refs, legacy.ID)
		}
		if err := sb.CloseByRef(ctx, types.WorkspaceRef(legacy.ID)); err != nil {
			t.Fatalf("close legacy raw ID: %v", err)
		}
		assertGone(t, sb.c.api, legacy.ID)
	})
}
