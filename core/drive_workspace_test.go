package gohan

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// wsHandles records the sandbox coordinator calls the lifecycle seams
// make. A failed release retains the record: the durable snapshot stays
// and the next sweep retries.
type wsHandles struct {
	rec             *order
	acquires        []string
	pubErr          error
	relErr          error
	publishes       int
	releases        int
	sweeps          int
	sweepLimit      int
	cleanupRetained bool
}

func (h *wsHandles) Acquire(_ context.Context, sessionID string, _ types.WorkspaceOwner) (types.Workspace, error) {
	h.acquires = append(h.acquires, sessionID)
	return nil, nil
}

func (h *wsHandles) Publish(_ context.Context, _ string, _ types.WorkspaceOwner) (types.WorkspaceSnapshot, error) {
	h.publishes++
	h.rec.mark("handles.publish")
	if h.pubErr != nil {
		return types.WorkspaceSnapshot{}, h.pubErr
	}
	return types.WorkspaceSnapshot{Ref: "snap-1", Generation: int64(h.publishes)}, nil
}

func (h *wsHandles) Release(_ context.Context, _ string, _ types.WorkspaceOwner) error {
	h.releases++
	h.rec.mark("handles.release")
	if h.relErr != nil {
		h.cleanupRetained = true
		return h.relErr
	}
	return nil
}

func (h *wsHandles) Sweep(_ context.Context, limit int) error {
	h.sweeps++
	h.sweepLimit = limit
	return nil
}

// wsPendingRuns refuses every terminal write with ErrSignalsPending: a
// steer landed between the last drain and the finish.
type wsPendingRuns struct {
	*lcRuns
}

func (s *wsPendingRuns) Finish(_ context.Context, _ stores.Lease, _ stores.RunState, _ []types.CallKey, _ string) error {
	s.rec.mark("runs.finish.refused")
	return fmt.Errorf("gohan: run run-1: %w", types.ErrSignalsPending)
}

func wsTestLifecycle(h *wsHandles, use *types.WorkspaceUse, runs stores.Runs, rec *order) *Lifecycle {
	lease := stores.Lease{RunID: "run-1", Generation: 3, Expires: time.Now().Add(time.Minute).UTC()}
	return NewLifecycle(
		WithLifecycleRuns(runs, lease),
		WithLifecycleSession("s-1"),
		WithLifecycleSandboxHandles(h),
		WithLifecycleWorkspaceUse(use),
	)
}

func TestSandboxSnapshotOnSuspend(t *testing.T) {
	t.Run("sandbox.snapshot-on-suspend", func(t *testing.T) {
		rec := &order{}
		h := &wsHandles{rec: rec}
		use := &types.WorkspaceUse{}
		use.MarkAcquired()
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.SuspendedStatus}}}
		runs := &lcRuns{rec: rec}
		lc := wsTestLifecycle(h, use, runs, rec)
		var saved stores.Checkpoint
		ag := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
			saved = cp
			rec.mark("checkpoints.put")
			return "tok-1", nil
		}}
		var last types.Event
		for ev, err := range DriveLifecycle(context.Background(), lc, rt, ag) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			last = ev
		}
		// The durable snapshot precedes the save; the release follows the
		// suspension transition.
		rec.want(t, "handles.publish", "checkpoints.put", "runs.suspend", "handles.release")
		if saved.Workspace != "snap-1" {
			t.Fatalf("checkpoint workspace %q, want snap-1", saved.Workspace)
		}
		if saved.WorkspaceGeneration != 1 {
			t.Fatalf("checkpoint workspace generation %d, want 1", saved.WorkspaceGeneration)
		}
		if _, ok := last.(types.Suspended); !ok {
			t.Fatalf("last event %v, want Suspended", last)
		}
	})

	t.Run("no acquired workspace publishes nowhere", func(t *testing.T) {
		rec := &order{}
		h := &wsHandles{rec: rec}
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.SuspendedStatus}}}
		runs := &lcRuns{rec: rec}
		lc := wsTestLifecycle(h, &types.WorkspaceUse{}, runs, rec)
		if _, err := lcDrive(t, lc, rt, rec); err != nil {
			t.Fatalf("drive: %v", err)
		}
		rec.want(t, "checkpoints.put", "runs.suspend")
		if h.publishes != 0 || h.releases != 0 {
			t.Fatalf("publishes=%d releases=%d, want none without an acquired handle", h.publishes, h.releases)
		}
	})
}

func TestSuspendReleaseOrdering(t *testing.T) {
	t.Run("sandbox.release-after-suspend-storage", func(t *testing.T) {
		rec := &order{}
		h := &wsHandles{rec: rec, relErr: errors.New("close refused")}
		use := &types.WorkspaceUse{}
		use.MarkAcquired()
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.SuspendedStatus}}}
		runs := &lcRuns{rec: rec}
		lc := wsTestLifecycle(h, use, runs, rec)
		var saved stores.Checkpoint
		ag := runtime.AgentRun{Save: func(_ context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
			saved = cp
			rec.mark("checkpoints.put")
			return "tok-1", nil
		}}
		var last types.Event
		for ev, err := range DriveLifecycle(context.Background(), lc, rt, ag) {
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			last = ev
		}
		if _, ok := last.(types.Suspended); !ok {
			t.Fatalf("last event %v, want a successful suspension despite the failed close", last)
		}
		rec.want(t, "handles.publish", "checkpoints.put", "runs.suspend", "handles.release")
		// The failed close keeps the durable snapshot and leaves a cleanup
		// record for the next sweep.
		if saved.Workspace != "snap-1" || !h.cleanupRetained {
			t.Fatalf("workspace %q cleanupRetained %v, want the durable snapshot and a retained record", saved.Workspace, h.cleanupRetained)
		}
	})
}

func TestSuspendPublishFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"snapshot refused", errors.New("snapshot refused")},
		{"compare-and-set refused", fmt.Errorf("publish: %w", types.ErrVersionConflict)},
	}
	t.Run("sandbox.suspend-publish-failure-aborts", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := &order{}
				h := &wsHandles{rec: rec, pubErr: tc.err}
				use := &types.WorkspaceUse{}
				use.MarkAcquired()
				rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.SuspendedStatus}}}
				runs := &lcRuns{rec: rec}
				lc := wsTestLifecycle(h, use, runs, rec)
				saved := false
				ag := runtime.AgentRun{Save: func(_ context.Context, _ stores.Checkpoint) (types.ResumeToken, error) {
					saved = true
					rec.mark("checkpoints.put")
					return "tok-1", nil
				}}
				for _, derr := range DriveLifecycle(context.Background(), lc, rt, ag) {
					if derr != nil && !errors.Is(derr, tc.err) {
						t.Fatalf("drive: %v, want the publication failure", derr)
					}
				}
				if saved {
					t.Fatal("checkpoint saved after the publish failure")
				}
				rec.want(t, "handles.publish")
				if runs.suspended != "" {
					t.Fatalf("suspension transition recorded with token %q", runs.suspended)
				}
			})
		}
	})
}

func TestFinishRunPublishesWorkspace(t *testing.T) {
	t.Run("sandbox.terminal-publish-before-complete", func(t *testing.T) {
		rec := &order{}
		h := &wsHandles{rec: rec}
		use := &types.WorkspaceUse{}
		use.MarkAcquired()
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		runs := &lcRuns{rec: rec}
		lc := wsTestLifecycle(h, use, runs, rec)
		put := false
		ag := runtime.AgentRun{Save: func(_ context.Context, _ stores.Checkpoint) (types.ResumeToken, error) {
			put = true
			return "tok-1", nil
		}}
		for _, derr := range DriveLifecycle(context.Background(), lc, rt, ag) {
			if derr != nil {
				t.Fatalf("drive: %v", derr)
			}
		}
		// The terminal order publishes before the write and releases only
		// after it succeeded; a detached end creates no checkpoint.
		rec.want(t, "handles.publish", "runs.finish", "handles.release")
		if put {
			t.Fatal("terminal path saved a checkpoint")
		}
	})
}

func TestFinishRunSignalsPending(t *testing.T) {
	t.Run("sandbox.signals-pending-keeps-handle", func(t *testing.T) {
		rec := &order{}
		h := &wsHandles{rec: rec}
		use := &types.WorkspaceUse{}
		use.MarkAcquired()
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		runs := &wsPendingRuns{lcRuns: &lcRuns{rec: rec}}
		lc := wsTestLifecycle(h, use, runs, rec)
		var got error
		for _, err := range DriveLifecycle(context.Background(), lc, rt, runtime.AgentRun{}) {
			if err != nil {
				got = err
			}
		}
		if !errors.Is(got, types.ErrSignalsPending) {
			t.Fatalf("drive err %v, want ErrSignalsPending", got)
		}
		// The refused finish keeps the live handle and its heartbeat: the
		// publication ran, the release never did.
		rec.want(t, "handles.publish", "runs.finish.refused")
		if h.releases != 0 {
			t.Fatalf("%d releases after a refused finish, want the handle kept", h.releases)
		}
	})
}

func TestTerminalPublishFailureRecordsFailed(t *testing.T) {
	rec := &order{}
	h := &wsHandles{rec: rec, pubErr: errors.New("snapshot refused")}
	use := &types.WorkspaceUse{}
	use.MarkAcquired()
	rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
	runs := &lcRuns{rec: rec}
	lease := stores.Lease{RunID: "run-1", Generation: 3, Expires: time.Now().Add(time.Minute).UTC()}
	lc := NewLifecycle(
		WithLifecycleRuns(runs, lease),
		WithLifecycleSession("s-1"),
		WithLifecycleSandboxHandles(h),
		WithLifecycleWorkspaceUse(use),
		WithLifecycleResultRef("res-1"),
	)
	for _, err := range DriveLifecycle(context.Background(), lc, rt, runtime.AgentRun{}) {
		if err != nil && !errors.Is(err, h.pubErr) {
			t.Fatalf("drive: %v", err)
		}
	}
	rec.want(t, "handles.publish", "runs.finish")
	// The failure records Failed without the result ref and never repeats
	// the failing publication.
	if runs.finished != 1 || h.publishes != 1 || h.releases != 0 {
		t.Fatalf("finish=%d publishes=%d releases=%d, want one failed finish, one publish, no release", runs.finished, h.publishes, h.releases)
	}
}
