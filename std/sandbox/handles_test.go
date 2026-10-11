package sandbox

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// coordWorkspace is one opened workspace carrying its resource identity.
type coordWorkspace struct {
	fakeWorkspace
	resource    types.WorkspaceRef
	snapshotRef types.WorkspaceRef
}

func (w *coordWorkspace) ResourceRef() types.WorkspaceRef { return w.resource }

func (w *coordWorkspace) Snapshot(context.Context) (types.WorkspaceRef, error) {
	return w.snapshotRef, nil
}

func ownerA(gen uint64) types.WorkspaceOwner {
	return types.WorkspaceOwner{RunID: "run-1", LeaseGeneration: gen}
}

// coordSandbox opens a fresh workspace per call and closes by resource
// identity, so the coordinator's sweep path is observable.
type coordSandbox struct {
	mu       sync.Mutex
	opens    int
	refs     []types.WorkspaceRef
	ws       []*coordWorkspace
	openErr  error
	closeErr error
	closed   map[types.WorkspaceRef]int
}

func (s *coordSandbox) Open(_ context.Context, ref types.WorkspaceRef, _ types.SandboxPolicy) (types.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return nil, s.openErr
	}
	s.opens++
	s.refs = append(s.refs, ref)
	w := &coordWorkspace{resource: types.WorkspaceRef("res-" + string(rune('a'+s.opens-1)))}
	w.snapshotRef = types.WorkspaceRef("snap-" + string(rune('a'+s.opens-1)))
	s.ws = append(s.ws, w)
	return w, nil
}

func (s *coordSandbox) CloseByRef(_ context.Context, ref types.WorkspaceRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeErr != nil {
		return s.closeErr
	}
	if s.closed == nil {
		s.closed = make(map[types.WorkspaceRef]int)
	}
	s.closed[ref]++
	return nil
}

// metaFake holds the durable workspace pointer and the cleanup records in
// memory under the same atomic refusal rules the stores keep.
type metaFake struct {
	mu       sync.Mutex
	meta     map[string]stores.WorkspaceMeta
	cleanups []stores.WorkspaceCleanupRecord
	claims   map[string]bool
	live     map[string]bool

	active   map[types.WorkspaceOwner]bool
	setErr   error
	recorded []stores.WorkspaceCleanupRecord
	validate func(sessionID string, owner types.WorkspaceOwner) error
}

func newMetaFake(live ...types.WorkspaceOwner) *metaFake {
	f := &metaFake{
		meta:     make(map[string]stores.WorkspaceMeta),
		claims:   make(map[string]bool),
		live:     make(map[string]bool),
		active:   make(map[types.WorkspaceOwner]bool),
		validate: func(string, types.WorkspaceOwner) error { return nil },
	}
	for _, o := range live {
		f.active[o] = true
	}
	return f
}

func (f *metaFake) WorkspaceMeta(_ context.Context, sessionID string) (stores.WorkspaceMeta, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.meta[sessionID]
	return m, ok, nil
}

func (f *metaFake) SetWorkspaceMeta(_ context.Context, sessionID string, expectedGeneration int64, m stores.WorkspaceMeta) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return 0, f.setErr
	}
	stored, ok := f.meta[sessionID]
	if ok && stored.Generation != expectedGeneration {
		return 0, types.ErrVersionConflict
	}
	if !ok && expectedGeneration != 0 {
		return 0, types.ErrVersionConflict
	}
	stored.Ref = m.Ref
	stored.Owner = m.Owner
	stored.Generation = expectedGeneration + 1
	f.meta[sessionID] = stored
	return stored.Generation, nil
}

func (f *metaFake) ImportWorkspaceMeta(_ context.Context, sessionID string, owner types.WorkspaceOwner, legacy types.WorkspaceSnapshot) (types.WorkspaceSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meta[sessionID]; ok {
		return types.WorkspaceSnapshot{}, types.ErrVersionConflict
	}
	f.meta[sessionID] = stores.WorkspaceMeta{Ref: legacy.Ref, Generation: 1, Owner: owner}
	return types.WorkspaceSnapshot{Ref: legacy.Ref, Generation: 1}, nil
}

func (f *metaFake) RecordWorkspaceCleanup(_ context.Context, sessionID string, owner types.WorkspaceOwner, ref types.WorkspaceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.active[owner] {
		return types.ErrRunNotActive
	}
	rec := stores.WorkspaceCleanupRecord{SessionID: sessionID, Owner: owner, ResourceRef: ref}
	f.cleanups = append(f.cleanups, rec)
	f.recorded = append(f.recorded, rec)
	return nil
}

func (f *metaFake) ValidateWorkspaceAcquire(ctx context.Context, sessionID string, owner types.WorkspaceOwner) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.validate(sessionID, owner); err != nil {
		return err
	}
	if !f.active[owner] {
		return types.ErrRunNotActive
	}
	if f.claims[sessionID] {
		return types.ErrRunNotActive
	}
	return nil
}

func (f *metaFake) CleanupCandidates(_ context.Context, limit int) ([]stores.WorkspaceCleanupRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		return nil, types.ErrInputInvalid
	}
	if len(f.cleanups) <= limit {
		return append([]stores.WorkspaceCleanupRecord(nil), f.cleanups...), nil
	}
	return append([]stores.WorkspaceCleanupRecord(nil), f.cleanups[:limit]...), nil
}

func (f *metaFake) claimKey(rec stores.WorkspaceCleanupRecord) string {
	return rec.SessionID + "\x00" + rec.Owner.RunID + "\x00" + string(rec.ResourceRef)
}

func (f *metaFake) ClaimWorkspaceCleanup(_ context.Context, rec stores.WorkspaceCleanupRecord) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, got := range f.cleanups {
		if got == rec {
			found = true
			break
		}
	}
	if !found || f.live[rec.SessionID] || f.claims[f.claimKey(rec)] {
		return false, nil
	}
	f.claims[f.claimKey(rec)] = true
	return true, nil
}

func (f *metaFake) CompleteWorkspaceCleanup(_ context.Context, rec stores.WorkspaceCleanupRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, got := range f.cleanups {
		if got == rec {
			f.cleanups = append(f.cleanups[:i], f.cleanups[i+1:]...)
			break
		}
	}
	delete(f.claims, f.claimKey(rec))
	return nil
}

func (f *metaFake) AbortWorkspaceCleanup(_ context.Context, rec stores.WorkspaceCleanupRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.claims, f.claimKey(rec))
	return nil
}

func newCoord(t *testing.T, sb types.Sandbox, meta *metaFake) *Handles {
	t.Helper()
	h, err := NewHandles(sb, types.SandboxPolicy{Lifecycle: types.PerCall}, meta, WithCleanupStore(meta))
	if err != nil {
		t.Fatalf("NewHandles: %v", err)
	}
	return h
}

func TestSandboxCoordinator(t *testing.T) {
	if _, err := NewHandles(nil, types.SandboxPolicy{}, newMetaFake()); !errors.Is(err, ErrNilSandbox) {
		t.Fatalf("nil sandbox err = %v, want ErrNilSandbox", err)
	}
	if _, err := NewHandles(&fakeSandbox{}, types.SandboxPolicy{}, nil); !errors.Is(err, ErrNilWorkspaceMeta) {
		t.Fatalf("nil meta err = %v, want ErrNilWorkspaceMeta", err)
	}

	sb := &coordSandbox{}
	meta := newMetaFake(ownerA(1))
	h := newCoord(t, sb, meta)
	ctx := context.Background()

	w, err := h.Acquire(ctx, "s1", ownerA(1))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if again, err := h.Acquire(ctx, "s1", ownerA(1)); err != nil || again != w {
		t.Fatalf("second acquire = %v, %v, want the same handle", again, err)
	}
	sb.mu.Lock()
	if sb.opens != 1 {
		t.Fatalf("opens = %d, want 1", sb.opens)
	}
	sb.mu.Unlock()

	snap, err := h.Publish(ctx, "s1", ownerA(1))
	if err != nil || snap.Ref != "snap-a" || snap.Generation != 1 {
		t.Fatalf("publish = %+v err = %v, want snap-a at generation 1", snap, err)
	}
	sb.mu.Lock()
	ws := sb.ws[0]
	sb.mu.Unlock()
	if ws.closes != 0 {
		t.Fatalf("publish closed the handle %d times", ws.closes)
	}
	if len(meta.recorded) != 1 || meta.recorded[0].ResourceRef != "res-a" {
		t.Fatalf("cleanup records = %+v", meta.recorded)
	}

	if err := h.Release(ctx, "s1", ownerA(1)); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ws.closes != 1 {
		t.Fatalf("closes = %d, want 1", ws.closes)
	}
	if len(meta.cleanups) != 0 {
		t.Fatalf("release kept the cleanup record: %v", meta.cleanups)
	}

	if err := h.Sweep(ctx, 0); !errors.Is(err, ErrZeroLimit) {
		t.Fatalf("zero-limit sweep err = %v, want ErrZeroLimit", err)
	}
}

func TestCoordinatorSharedInstance(t *testing.T) {
	sb := &coordSandbox{}
	meta := newMetaFake(ownerA(1))
	h := newCoord(t, sb, meta)

	ts, err := New(&coordSandbox{}, types.SandboxPolicy{Lifecycle: types.PerCall}, WithCoordinator(h))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s, ok := ts.(set)
	if !ok {
		t.Fatalf("tool set type = %T", ts)
	}
	if s.Coordinator() != types.SandboxHandles(h) {
		t.Fatalf("tool set coordinator = %v, want the registered instance", s.Coordinator())
	}
}

func TestSandboxHandleOwner(t *testing.T) {
	t.Run("sandbox.handle-owner-exclusive", func(t *testing.T) {
		sb := &coordSandbox{}
		meta := newMetaFake(ownerA(1))
		h := newCoord(t, sb, meta)
		ctx := context.Background()

		if _, err := h.Acquire(ctx, "s1", ownerA(1)); err != nil {
			t.Fatalf("holder acquire: %v", err)
		}
		sb.mu.Lock()
		opens := sb.opens
		sb.mu.Unlock()

		if _, err := h.Acquire(ctx, "s1", ownerA(2)); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("non-holder acquire err = %v, want ErrRunNotActive", err)
		}
		sb.mu.Lock()
		defer sb.mu.Unlock()
		if sb.opens != opens {
			t.Fatalf("refused acquire opened a workspace: opens = %d, want %d", sb.opens, opens)
		}
	})
}

func TestSandboxHandleFenced(t *testing.T) {
	t.Run("sandbox.handle-owner-fenced", func(t *testing.T) {
		sb := &coordSandbox{}
		meta := newMetaFake(ownerA(1))
		h := newCoord(t, sb, meta)
		ctx := context.Background()

		if _, err := h.Acquire(ctx, "s1", ownerA(1)); err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if _, err := h.Publish(ctx, "s1", ownerA(1)); err != nil {
			t.Fatalf("publish: %v", err)
		}
		stale := ownerA(2)
		if _, err := h.Publish(ctx, "s1", stale); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("stale publish err = %v, want ErrRunNotActive", err)
		}
		if err := h.Release(ctx, "s1", stale); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("stale release err = %v, want ErrRunNotActive", err)
		}
		m, ok, err := meta.WorkspaceMeta(ctx, "s1")
		if err != nil || !ok || m.Ref != "snap-a" || m.Generation != 1 {
			t.Fatalf("durable snapshot = %+v ok = %v err = %v, want snap-a at generation 1", m, ok, err)
		}
	})
}

func TestSandboxReleaseClosesAcquired(t *testing.T) {
	t.Run("sandbox.release-closes-acquired-handle", func(t *testing.T) {
		sb := &coordSandbox{}
		meta := newMetaFake(ownerA(1))
		h := newCoord(t, sb, meta)
		ctx := context.Background()

		if _, err := h.Acquire(ctx, "s1", ownerA(1)); err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := h.Release(ctx, "s1", ownerA(1)); err != nil {
			t.Fatalf("release: %v", err)
		}
		sb.mu.Lock()
		ws := sb.ws[0]
		opens := sb.opens
		sb.mu.Unlock()
		if ws.closes != 1 {
			t.Fatalf("closes = %d, want exactly 1", ws.closes)
		}
		if err := h.Release(ctx, "s1", ownerA(1)); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("double release err = %v, want ErrRunNotActive", err)
		}
		sb.mu.Lock()
		defer sb.mu.Unlock()
		if sb.opens != opens {
			t.Fatalf("release opened a fresh workspace: opens = %d, want %d", sb.opens, opens)
		}
	})
}
