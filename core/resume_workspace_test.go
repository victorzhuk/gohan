package gohan

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestRecoverSweep(t *testing.T) {
	t.Run("sandbox.recover-sweeps-without-stale-runs", func(t *testing.T) {
		rec := &order{}
		h := &wsHandles{rec: rec}
		s := &Stack{
			stores:         stores.Stores{Runs: &lcRuns{rec: rec}},
			sandboxHandles: h,
		}
		// Zero stale rows: the sweep still runs, because cleanup records
		// outlive a run's stale listing.
		if err := s.Recover(context.Background(), 5); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if h.sweeps != 1 {
			t.Fatalf("%d sweeps with no stale rows, want 1", h.sweeps)
		}
		if h.sweepLimit != 5 {
			t.Fatalf("sweep limit %d, want the recovery limit 5", h.sweepLimit)
		}
	})
}

func TestSweepBoundedLimit(t *testing.T) {
	t.Run("sandbox.sweep-bounded-limit", func(t *testing.T) {
		for _, limit := range []int{1, 3, 7} {
			rec := &order{}
			h := &wsHandles{rec: rec}
			s := &Stack{
				stores:         stores.Stores{Runs: &lcRuns{rec: rec}},
				sandboxHandles: h,
			}
			if err := s.Recover(context.Background(), limit); err != nil {
				t.Fatalf("recover with limit %d: %v", limit, err)
			}
			if h.sweepLimit != limit {
				t.Fatalf("sweep limit %d, want the recovery limit %d", h.sweepLimit, limit)
			}
		}
	})
}

func TestCloseFailureRetainsSnapshot(t *testing.T) {
	t.Run("sandbox.close-failure-retains-snapshot", func(t *testing.T) {
		// A release whose close fails during the terminal seam keeps the
		// durable snapshot and the cleanup record; the failed close is
		// retried by the sweep, never by a fresh open.
		rec := &order{}
		h := &wsHandles{rec: rec, relErr: errors.New("close refused")}
		use := &types.WorkspaceUse{}
		use.MarkAcquired()
		rt := &lcRuntime{histVer: 1, script: []lcStep{{status: runtime.DoneStatus}}}
		runs := &lcRuns{rec: rec}
		lc := wsTestLifecycle(h, use, runs, rec)
		for _, derr := range DriveLifecycle(context.Background(), lc, rt, runtime.AgentRun{}) {
			if derr != nil {
				t.Fatalf("drive: %v", derr)
			}
		}
		if !h.cleanupRetained || h.releases != 1 || h.publishes != 1 {
			t.Fatalf("releases=%d publishes=%d retained=%v, want one release attempt with a retained record", h.releases, h.publishes, h.cleanupRetained)
		}
		if h.sweeps != 0 {
			t.Fatal("the lifecycle retried the close itself; the sweep owns the retry")
		}
	})
}

// wsResumeHarness seeds a suspended approval run whose checkpoint carries
// workspace fields, over a session log with the owner guard wired to the
// runs store the conversation uses.
type wsResumeHarness struct {
	cps  *stores.MemoryCheckpoints
	log  *stores.MemorySessionLog
	runs *stores.MemoryRuns
	rt   *resumeRT
	h    *wsHandles
}

func newWSResumeHarness(t *testing.T) *wsResumeHarness {
	t.Helper()
	h := &wsResumeHarness{
		cps:  stores.NewMemoryCheckpoints(),
		runs: stores.NewMemoryRuns(),
		rt:   &resumeRT{},
		h:    &wsHandles{rec: &order{}},
	}
	h.log = stores.NewMemorySessionLog(
		stores.WithSessionPrincipals(types.PrincipalFrom),
		stores.WithSessionWorkspaceOwnerGuard(h.runs),
	)
	h.rt.steps = []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, nil, runtime.DoneStatus, nil
		},
	}
	pctx := types.WithPrincipal(context.Background(), authOwner)
	if _, err := h.log.Append(pctx, "s1", 0, types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book it"}},
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

// seedMetaRun starts the live run the workspace metadata is published
// under and returns its lease-fenced owner with the lease. The caller
// suspends the run through suspendLease when the session must free its
// lease.
func (h *wsResumeHarness) seedMetaRun(t *testing.T, runID string) (types.WorkspaceOwner, stores.Lease) {
	t.Helper()
	pctx := types.WithPrincipal(context.Background(), authOwner)
	lease, err := h.runs.Start(pctx, stores.Run{SessionID: "s1", RunID: runID, Flow: "flights", Backend: h.rt.Name()}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return types.WorkspaceOwner{RunID: runID, LeaseGeneration: lease.Generation}, lease
}

func (h *wsResumeHarness) suspendLease(t *testing.T, lease stores.Lease, token types.ResumeToken) {
	t.Helper()
	pctx := types.WithPrincipal(context.Background(), authOwner)
	if err := h.runs.Suspend(pctx, lease, token); err != nil {
		t.Fatal(err)
	}
}

func (h *wsResumeHarness) publish(t *testing.T, owner types.WorkspaceOwner, ref types.WorkspaceRef, expected int64) stores.WorkspaceMeta {
	t.Helper()
	pctx := types.WithPrincipal(context.Background(), authOwner)
	gen, err := h.log.SetWorkspaceMeta(pctx, "s1", expected, stores.WorkspaceMeta{Ref: ref, Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	return stores.WorkspaceMeta{Ref: ref, Generation: gen, Owner: owner}
}

func (h *wsResumeHarness) suspend(t *testing.T, runID string, token types.ResumeToken) {
	t.Helper()
	pctx := types.WithPrincipal(context.Background(), authOwner)
	runs, err := h.runs.Start(pctx, stores.Run{SessionID: "s1", RunID: runID, Flow: "flights", Backend: h.rt.Name()}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.runs.Suspend(pctx, runs, token); err != nil {
		t.Fatal(err)
	}
}

// seedWSCp stores an approval checkpoint carrying workspace fields, then
// suspends a run holding its token, exactly as the live suspension seam
// would.
func (h *wsResumeHarness) seedWSCp(t *testing.T, runID string, wsRef types.WorkspaceRef, wsGen int64) types.ResumeToken {
	t.Helper()
	approval := checkpointApproval{
		Call:        types.ToolUse{ID: "call-1", Name: "book", Args: []byte(`{"n":1}`)},
		Risk:        types.RiskHigh,
		Fingerprint: "fp-call-1",
		Eligible:    permissionEligible(1),
	}
	st := runtime.State{Turn: 1, HistoryVersion: 1, Pending: []types.ToolUse{approval.Call}}
	env := checkpointEnvelope{
		Run:        types.RunInfo{Flow: "flights", SessionID: "s1", RunID: runID},
		Generation: 1,
		State:      st,
		Approvals:  []checkpointApproval{approval},
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		t.Fatal(err)
	}
	pctx := types.WithPrincipal(context.Background(), authOwner)
	token, err := h.cps.Put(pctx, stores.Checkpoint{
		RunID:               runID,
		SchemaVersion:       stores.CurrentSchemaVersion,
		SessionID:           "s1",
		Flow:                "flights",
		Backend:             h.rt.Name(),
		Reason:              types.HumanApproval,
		Originator:          authOwner,
		Workspace:           wsRef,
		WorkspaceGeneration: wsGen,
		Data:                data,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.suspend(t, runID, token)
	return token
}

func (h *wsResumeHarness) conv(t *testing.T) Conversation {
	t.Helper()
	stack := &Stack{stores: stores.Stores{SessionLog: h.log}, sandboxHandles: h.h}
	conv, err := NewConversation(stack, "flights", h.rt,
		WithConversationRuns(h.runs),
		WithConversationEventLog(stores.NewMemoryEventLog()),
		WithConversationCheckpoints(h.cps),
		WithConversationCredentialSource(&authCreds{}),
		WithConversationApprovalPolicy(&authPolicySource{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func permissionEligible(quorum int) permission.Eligibility {
	return permission.Eligibility{Scopes: []string{"approve:tool"}, Quorum: quorum}
}

func wsResumeErrors(t *testing.T, seq iter.Seq2[Event, error]) []error {
	t.Helper()
	var errs []error
	for _, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func TestSandboxResumeRestores(t *testing.T) {
	t.Run("sandbox.resume-restores", func(t *testing.T) {
		h := newWSResumeHarness(t)
		owner, metaLease := h.seedMetaRun(t, "run-meta")
		h.publish(t, owner, "snap-1", 0)
		h.suspendLease(t, metaLease, "tok-meta")
		token := h.seedWSCp(t, "run-1", "snap-1", 1)
		conv := h.conv(t)
		if errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve())); len(errs) != 0 {
			t.Fatalf("resume: %v", errs)
		}
		// The stored snapshot survives the resume: equal metadata issues
		// no write and a newer checkpoint never appears.
		m, ok, err := h.log.WorkspaceMeta(types.WithPrincipal(context.Background(), authOwner), "s1")
		if err != nil || !ok || m.Ref != "snap-1" || m.Generation != 1 {
			t.Fatalf("meta = %+v ok = %v err = %v, want snap-1 at generation 1", m, ok, err)
		}
	})

	t.Run("older checkpoint keeps the stored snapshot", func(t *testing.T) {
		h := newWSResumeHarness(t)
		owner, metaLease := h.seedMetaRun(t, "run-meta")
		h.publish(t, owner, "snap-2", 0)
		h.suspendLease(t, metaLease, "tok-meta")
		// The stored generation is 1; a zero-valued old checkpoint is
		// older than every stored generation.
		token := h.seedWSCp(t, "run-1", "", 0)
		conv := h.conv(t)
		if errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve())); len(errs) != 0 {
			t.Fatalf("resume over a stored snapshot: %v", errs)
		}
		m, _, _ := h.log.WorkspaceMeta(types.WithPrincipal(context.Background(), authOwner), "s1")
		if m.Ref != "snap-2" || m.Generation != 1 {
			t.Fatalf("stored meta = %+v, want snap-2 kept at generation 1", m)
		}
	})

	t.Run("checkpoint newer than stored is refused", func(t *testing.T) {
		h := newWSResumeHarness(t)
		owner, metaLease := h.seedMetaRun(t, "run-meta")
		h.publish(t, owner, "snap-1", 0)
		h.suspendLease(t, metaLease, "tok-meta")
		// A generation-2 checkpoint with no matching durable publication
		// is a missing publication: the restore fails.
		token := h.seedWSCp(t, "run-1", "snap-2", 2)
		conv := h.conv(t)
		errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrVersionConflict) {
			t.Fatalf("newer checkpoint errors %v, want ErrVersionConflict", errs)
		}
	})
}

func TestNoRefSessionMigrates(t *testing.T) {
	t.Run("sandbox.no-ref-session-migrates", func(t *testing.T) {
		h := newWSResumeHarness(t)
		// No stored metadata and no checkpoint ref: the fresh open
		// applies, and the resume completes.
		token := h.seedWSCp(t, "run-1", "", 0)
		conv := h.conv(t)
		if errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve())); len(errs) != 0 {
			t.Fatalf("resume: %v", errs)
		}
		if _, ok, err := h.log.WorkspaceMeta(types.WithPrincipal(context.Background(), authOwner), "s1"); err != nil || ok {
			t.Fatalf("fresh open wrote metadata: ok = %v err = %v", ok, err)
		}
	})
}

func TestRestoreSkipsEqualMetadata(t *testing.T) {
	t.Run("sandbox.restore-skips-equal-metadata", func(t *testing.T) {
		h := newWSResumeHarness(t)
		owner, metaLease := h.seedMetaRun(t, "run-meta")
		want := h.publish(t, owner, "snap-1", 0)
		h.suspendLease(t, metaLease, "tok-meta")
		token := h.seedWSCp(t, "run-1", "snap-1", 1)
		conv := h.conv(t)
		if errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve())); len(errs) != 0 {
			t.Fatalf("resume: %v", errs)
		}
		got, ok, err := h.log.WorkspaceMeta(types.WithPrincipal(context.Background(), authOwner), "s1")
		if err != nil || !ok || got != want {
			t.Fatalf("meta = %+v ok = %v err = %v, want the equal record unchanged: %+v", got, ok, err, want)
		}
	})
}

func TestLegacyRefExplicitImport(t *testing.T) {
	t.Run("sandbox.legacy-ref-explicit-import", func(t *testing.T) {
		h := newWSResumeHarness(t)
		// A generation-zero checkpoint ref is a legacy pointer: Resume
		// refuses it and never imports implicitly, while the session
		// holds no imported metadata.
		pctx := types.WithPrincipal(context.Background(), authOwner)
		token := h.seedWSCp(t, "run-1", "legacy-1", 0)
		conv := h.conv(t)
		errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token, Approve()))
		if len(errs) == 0 || !strings.Contains(errs[0].Error(), "ImportWorkspaceMeta") {
			t.Fatalf("legacy ref errors %v, want an explicit import refusal", errs)
		}

		// The authorized import writes generation one under the live
		// lease it validated.
		ownerLive, leaseLive := h.seedMetaRun(t, "run-import")
		snap, err := h.log.ImportWorkspaceMeta(pctx, "s1", ownerLive, types.WorkspaceSnapshot{Ref: "legacy-1"})
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if snap.Ref != "legacy-1" || snap.Generation != 1 {
			t.Fatalf("imported snapshot = %+v, want legacy-1 at generation 1", snap)
		}
		h.suspendLease(t, leaseLive, "tok-import")

		token2 := h.seedWSCp(t, "run-2", "legacy-1", 1)
		if errs := wsResumeErrors(t, conv.Resume(types.WithPrincipal(context.Background(), authOp), token2, Approve())); len(errs) != 0 {
			t.Fatalf("resume after import: %v", errs)
		}
		m, ok, err := h.log.WorkspaceMeta(pctx, "s1")
		if err != nil || !ok || m.Ref != "legacy-1" || m.Generation != 1 {
			t.Fatalf("meta = %+v ok = %v err = %v, want legacy-1 at generation 1", m, ok, err)
		}
	})
}
