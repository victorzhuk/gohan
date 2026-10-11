package gohan

import (
	"context"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// crossBackend is one snapshot backend shared by two pods. Files live in
// the backend, not in any workspace, so a closed pod-A handle still
// restores on pod B.
type crossBackend struct {
	mu    sync.Mutex
	files map[types.WorkspaceRef]map[string]string
	seq   int
}

func newCrossBackend() *crossBackend {
	return &crossBackend{files: make(map[types.WorkspaceRef]map[string]string)}
}

func (b *crossBackend) put(ref types.WorkspaceRef, files map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	copied := make(map[string]string, len(files))
	maps.Copy(copied, files)
	b.files[ref] = copied
}

func (b *crossBackend) get(ref types.WorkspaceRef) (map[string]string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	files, ok := b.files[ref]
	if !ok {
		return nil, false
	}
	copied := make(map[string]string, len(files))
	maps.Copy(copied, files)
	return copied, true
}

// crossWorkspace is one pod-local workspace over the shared backend.
type crossWorkspace struct {
	files  map[string]string
	ref    types.WorkspaceRef
	closes int
}

func (w *crossWorkspace) Exec(context.Context, types.ExecRequest) (types.ExecResult, error) {
	return types.ExecResult{}, nil
}

func (w *crossWorkspace) Put(_ context.Context, path string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	w.files[path] = string(data)
	return nil
}

func (w *crossWorkspace) Get(_ context.Context, path string) (io.ReadCloser, error) {
	body, ok := w.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (w *crossWorkspace) SetEnv(context.Context, map[string]string) error { return nil }

func (w *crossWorkspace) Snapshot(context.Context) (types.WorkspaceRef, error) { return w.ref, nil }

func (w *crossWorkspace) Close(context.Context) error { w.closes++; return nil }

func (w *crossWorkspace) List(context.Context, string) ([]types.WorkspaceEntry, error) {
	return nil, types.ErrWorkspaceListingRequired
}

// crossHandles is one pod's coordinator instance. Each pod holds its own
// live handles; durability flows only through the shared backend and the
// shared SessionWorkspaceMeta store.
type crossHandles struct {
	backend    *crossBackend
	meta       stores.SessionWorkspaceMeta
	pod        string
	live       *crossWorkspace
	freshOpens int
	restores   int
}

func newCrossHandles(pod string, backend *crossBackend, meta stores.SessionWorkspaceMeta) *crossHandles {
	return &crossHandles{backend: backend, meta: meta, pod: pod}
}

func (h *crossHandles) Acquire(ctx context.Context, sessionID string, owner types.WorkspaceOwner) (types.Workspace, error) {
	m, ok, err := h.meta.WorkspaceMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if ok {
		files, found := h.backend.get(m.Ref)
		if !found {
			return nil, fmt.Errorf("pod %s: snapshot %q is missing or expired", h.pod, m.Ref)
		}
		h.restores++
		w := &crossWorkspace{files: files, ref: m.Ref}
		h.live = w
		return w, nil
	}
	h.freshOpens++
	w := &crossWorkspace{files: make(map[string]string), ref: types.WorkspaceRef(h.pod + "-ws")}
	h.live = w
	return w, nil
}

func (h *crossHandles) Publish(ctx context.Context, sessionID string, owner types.WorkspaceOwner) (types.WorkspaceSnapshot, error) {
	h.backend.mu.Lock()
	h.backend.seq++
	ref := types.WorkspaceRef(fmt.Sprintf("snap-%s-%d", h.pod, h.backend.seq))
	h.backend.mu.Unlock()
	if h.live != nil {
		h.backend.put(ref, h.live.files)
	}
	m, ok, err := h.meta.WorkspaceMeta(ctx, sessionID)
	if err != nil {
		return types.WorkspaceSnapshot{}, err
	}
	expected := int64(0)
	if ok {
		expected = m.Generation
	}
	gen, err := h.meta.SetWorkspaceMeta(ctx, sessionID, expected, stores.WorkspaceMeta{Ref: ref, Owner: owner})
	if err != nil {
		return types.WorkspaceSnapshot{}, err
	}
	return types.WorkspaceSnapshot{Ref: ref, Generation: gen}, nil
}

func (h *crossHandles) Release(_ context.Context, _ string, _ types.WorkspaceOwner) error {
	h.live = nil
	return nil
}

func (h *crossHandles) Sweep(context.Context, int) error { return nil }

// crossSeed starts the live run the workspace metadata is fenced under.
func crossSeed(t *testing.T, runs *stores.MemoryRuns, log *stores.MemorySessionLog, runID string) (types.WorkspaceOwner, context.Context) {
	t.Helper()
	pctx := types.WithPrincipal(context.Background(), authOwner)
	if _, err := log.Append(pctx, "s1", 0, types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "start"}},
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := runs.Start(pctx, stores.Run{SessionID: "s1", RunID: runID, Flow: "flights", Backend: "scripted"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	owner := types.WorkspaceOwner{RunID: runID, LeaseGeneration: lease.Generation}
	return owner, types.WithRunInfo(pctx, types.RunInfo{SessionID: "s1", RunID: runID})
}

func TestCrossPodRestore(t *testing.T) {
	t.Run("sandbox.cross-pod-independent-coordinators", func(t *testing.T) {
		runs := stores.NewMemoryRuns()
		log := stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithSessionWorkspaceOwnerGuard(runs),
		)
		owner, ctx := crossSeed(t, runs, log, "run-a")
		backend := newCrossBackend()

		podA := newCrossHandles("pod-a", backend, log)
		podB := newCrossHandles("pod-b", backend, log)

		w, err := podA.Acquire(ctx, "s1", owner)
		if err != nil {
			t.Fatalf("pod-a acquire: %v", err)
		}
		if err := w.Put(ctx, "data.csv", strings.NewReader("a,b\n1,2\n")); err != nil {
			t.Fatalf("pod-a put: %v", err)
		}
		snap, err := podA.Publish(ctx, "s1", owner)
		if err != nil {
			t.Fatalf("pod-a publish: %v", err)
		}
		if err := podA.Release(ctx, "s1", owner); err != nil {
			t.Fatalf("pod-a release: %v", err)
		}

		// Pod B holds no live handle: its Acquire restores the published
		// ref from the shared backend and reports pod A's file without
		// opening a fresh workspace.
		wb, err := podB.Acquire(ctx, "s1", owner)
		if err != nil {
			t.Fatalf("pod-b acquire: %v", err)
		}
		rc, err := wb.Get(ctx, "data.csv")
		if err != nil {
			t.Fatalf("pod-b get: %v", err)
		}
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read restored file: %v", err)
		}
		rc.Close()
		if string(body) != "a,b\n1,2\n" {
			t.Fatalf("pod-b read %q, want pod-a's file body", body)
		}
		if podB.freshOpens != 0 || podB.restores != 1 {
			t.Fatalf("pod-b freshOpens=%d restores=%d, want one restore and no fresh open", podB.freshOpens, podB.restores)
		}
		m, ok, err := log.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != snap.Ref || m.Generation != 1 {
			t.Fatalf("shared meta %+v ok=%v err=%v, want %q at generation 1", m, ok, err, snap.Ref)
		}
	})
}

func TestRestoreMissingSnapshot(t *testing.T) {
	t.Run("sandbox.restore-missing-snapshot-errors", func(t *testing.T) {
		runs := stores.NewMemoryRuns()
		log := stores.NewMemorySessionLog(
			stores.WithSessionPrincipals(types.PrincipalFrom),
			stores.WithSessionWorkspaceOwnerGuard(runs),
		)
		owner, ctx := crossSeed(t, runs, log, "run-a")
		backend := newCrossBackend()
		podB := newCrossHandles("pod-b", backend, log)

		// Metadata present but the snapshot is missing or expired: the
		// restore errors and opens no fresh workspace. Absent metadata
		// fresh-opens instead, so only a recorded ref counts here.
		if _, err := log.SetWorkspaceMeta(ctx, "s1", 0, stores.WorkspaceMeta{Ref: "snap-gone", Owner: owner}); err != nil {
			t.Fatalf("seed meta: %v", err)
		}
		if _, err := podB.Acquire(ctx, "s1", owner); err == nil {
			t.Fatal("acquire over a missing snapshot succeeded, want an error")
		}
		if podB.freshOpens != 0 || podB.restores != 0 {
			t.Fatalf("freshOpens=%d restores=%d, want the failed restore to open nothing", podB.freshOpens, podB.restores)
		}
	})
}
