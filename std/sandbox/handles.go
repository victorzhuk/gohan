package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ErrNilWorkspaceMeta is returned by NewHandles when no durable workspace
// metadata surface is supplied.
var ErrNilWorkspaceMeta = errors.New("gohan: sandbox coordinator requires a durable workspace metadata surface")

// ErrZeroLimit is returned by Sweep when the limit is not positive.
var ErrZeroLimit = errors.New("gohan: sandbox sweep limit must be positive")

// HandlesOption adjusts the coordinator at construction.
type HandlesOption func(*Handles)

// WithCleanupStore wires the durable cleanup-record surface. Without it,
// Acquire skips the owner and claim checks and Sweep has nothing to sweep.
func WithCleanupStore(sc stores.SessionWorkspaceCleanup) HandlesOption {
	return func(h *Handles) { h.cleanup = sc }
}

// Handles is the sandbox lifecycle coordinator. It owns one live workspace
// per (session, owner), publishes generation-fenced snapshots through the
// durable metadata surface, and sweeps leftover resources from the durable
// cleanup records.
type Handles struct {
	sb      types.Sandbox
	p       types.SandboxPolicy
	meta    stores.SessionWorkspaceMeta
	cleanup stores.SessionWorkspaceCleanup

	mu   sync.Mutex
	live map[string]map[types.WorkspaceOwner]*liveHandle
}

// liveHandle is one acquired workspace and the cleanup record its
// acquisition persisted. The record's ResourceRef identifies the exact
// live resource Open returned.
type liveHandle struct {
	w      types.Workspace
	record stores.WorkspaceCleanupRecord
}

// NewHandles builds the coordinator from the adapter, the policy and the
// durable metadata surface. The caller passes the same instance to the
// tool set and to the build registration.
func NewHandles(sb types.Sandbox, p types.SandboxPolicy, meta stores.SessionWorkspaceMeta, opts ...HandlesOption) (*Handles, error) {
	if sb == nil {
		return nil, ErrNilSandbox
	}
	if meta == nil {
		return nil, ErrNilWorkspaceMeta
	}
	h := &Handles{
		sb:   sb,
		p:    p,
		meta: meta,
		live: make(map[string]map[types.WorkspaceOwner]*liveHandle),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// Acquire returns the owner's live handle, or opens one from the stored
// snapshot when the owner holds none. A stale owner or a session a cleanup
// claim excludes is refused before any provider call.
func (h *Handles) Acquire(ctx context.Context, sessionID string, owner types.WorkspaceOwner) (types.Workspace, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.live[sessionID]; m != nil {
		if l, ok := m[owner]; ok {
			return l.w, nil
		}
	}
	if h.cleanup != nil {
		if err := h.cleanup.ValidateWorkspaceAcquire(ctx, sessionID, owner); err != nil {
			return nil, fmt.Errorf("acquire workspace session %s: %w", sessionID, err)
		}
	}
	ref := types.WorkspaceRef("")
	if meta, ok, err := h.meta.WorkspaceMeta(ctx, sessionID); err != nil {
		return nil, err
	} else if ok {
		ref = meta.Ref
	}
	w, err := h.sb.Open(ctx, ref, h.p)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	rec := stores.WorkspaceCleanupRecord{SessionID: sessionID, Owner: owner}
	if rr, ok := w.(types.WorkspaceResource); ok {
		rec.ResourceRef = rr.ResourceRef()
	}
	if h.cleanup != nil {
		if err := h.cleanup.RecordWorkspaceCleanup(ctx, sessionID, owner, rec.ResourceRef); err != nil {
			_ = w.Close(ctx)
			return nil, fmt.Errorf("record workspace cleanup session %s: %w", sessionID, err)
		}
	}
	m := h.live[sessionID]
	if m == nil {
		m = make(map[types.WorkspaceOwner]*liveHandle)
		h.live[sessionID] = m
	}
	l := &liveHandle{w: w, record: rec}
	m[owner] = l
	return w, nil
}

// Publish snapshots the owner's live handle and writes the durable
// metadata under the generation compare-and-set. The handle and the
// cleanup record stay: a publication is not a release.
func (h *Handles) Publish(ctx context.Context, sessionID string, owner types.WorkspaceOwner) (types.WorkspaceSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.lookupLocked(sessionID, owner)
	if l == nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("publish workspace session %s: %w", sessionID, types.ErrRunNotActive)
	}
	ref, err := l.w.Snapshot(ctx)
	if err != nil {
		return types.WorkspaceSnapshot{}, fmt.Errorf("snapshot workspace: %w", err)
	}
	meta, ok, err := h.meta.WorkspaceMeta(ctx, sessionID)
	if err != nil {
		return types.WorkspaceSnapshot{}, err
	}
	expected := int64(0)
	if ok {
		expected = meta.Generation
	}
	generation, err := h.meta.SetWorkspaceMeta(ctx, sessionID, expected, stores.WorkspaceMeta{Ref: ref, Generation: expected, Owner: owner})
	if err != nil {
		return types.WorkspaceSnapshot{}, err
	}
	return types.WorkspaceSnapshot{Ref: ref, Generation: generation}, nil
}

// Release closes the exact workspace Acquire returned and removes only its
// cleanup record after a successful close. A failed close retains the
// record and the durable snapshot for the next sweep. A stale owner holds
// no handle here and is refused.
func (h *Handles) Release(ctx context.Context, sessionID string, owner types.WorkspaceOwner) error {
	h.mu.Lock()
	l := h.lookupLocked(sessionID, owner)
	if l == nil {
		h.mu.Unlock()
		return fmt.Errorf("release workspace session %s: %w", sessionID, types.ErrRunNotActive)
	}
	delete(h.live[sessionID], owner)
	if len(h.live[sessionID]) == 0 {
		delete(h.live, sessionID)
	}
	h.mu.Unlock()
	err := l.w.Close(ctx)
	if h.cleanup == nil {
		return err
	}
	if err != nil {
		if abortErr := h.cleanup.AbortWorkspaceCleanup(ctx, l.record); abortErr != nil {
			slog.Warn("sandbox cleanup claim release failed", "error", abortErr)
		}
		return err
	}
	if err := h.cleanup.CompleteWorkspaceCleanup(ctx, l.record); err != nil {
		slog.Warn("sandbox cleanup record removal failed", "error", err)
	}
	return nil
}

// Sweep processes at most limit durable cleanup records: it claims each
// exactly, closes the exact local handle when this pod still holds it and
// closes by resource identity otherwise, and never opens a workspace. A
// live session lease refuses the claim and the record waits for the next
// sweep. A failed close keeps the record.
func (h *Handles) Sweep(ctx context.Context, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("sweep workspace: %w", ErrZeroLimit)
	}
	if h.cleanup == nil {
		return nil
	}
	recs, err := h.cleanup.CleanupCandidates(ctx, limit)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		claimed, err := h.cleanup.ClaimWorkspaceCleanup(ctx, rec)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		if err := h.sweepOne(ctx, rec); err != nil {
			slog.Warn("sandbox sweep close failed", "session", rec.SessionID, "error", err)
			if abortErr := h.cleanup.AbortWorkspaceCleanup(ctx, rec); abortErr != nil {
				slog.Warn("sandbox cleanup claim release failed", "error", abortErr)
			}
			continue
		}
		if err := h.cleanup.CompleteWorkspaceCleanup(ctx, rec); err != nil {
			slog.Warn("sandbox cleanup record removal failed", "error", err)
		}
	}
	return nil
}

// sweepOne closes the claimed record's exact resource: the local handle
// when this pod holds it, otherwise a close by resource identity. It never
// opens a workspace.
func (h *Handles) sweepOne(ctx context.Context, rec stores.WorkspaceCleanupRecord) error {
	h.mu.Lock()
	l := h.lookupLocked(rec.SessionID, rec.Owner)
	if l != nil {
		delete(h.live[rec.SessionID], rec.Owner)
		if len(h.live[rec.SessionID]) == 0 {
			delete(h.live, rec.SessionID)
		}
	}
	h.mu.Unlock()
	if l != nil {
		return l.w.Close(ctx)
	}
	if rec.ResourceRef == "" {
		return fmt.Errorf("sweep session %s: no resource identity recorded", rec.SessionID)
	}
	res, ok := h.sb.(types.SandboxResources)
	if !ok {
		return fmt.Errorf("sweep session %s: sandbox cannot close by resource identity", rec.SessionID)
	}
	return res.CloseByRef(ctx, rec.ResourceRef)
}

// lookupLocked reports the owner's live handle. The coordinator mutex must
// be held.
func (h *Handles) lookupLocked(sessionID string, owner types.WorkspaceOwner) *liveHandle {
	m := h.live[sessionID]
	if m == nil {
		return nil
	}
	return m[owner]
}
