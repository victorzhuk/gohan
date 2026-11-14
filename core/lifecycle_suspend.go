package gohan

import (
	"context"
	"fmt"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// WithLifecycleHandoffSink binds the session-side transition a model
// handoff performs at the terminal seam: control moves to
// ControlHandoffRequested and the HandoffContext persists on the session
// index before the run closes.
func WithLifecycleHandoffSink(fn func(context.Context, types.HandoffContext) error) LifecycleOption {
	return func(lc *Lifecycle) { lc.handoff = fn }
}

// WithLifecycleSessions binds the session index the handoff terminal
// transition persists its context and control through.
func WithLifecycleSessions(idx stores.SessionIndex) LifecycleOption {
	return func(lc *Lifecycle) { lc.sessions = idx }
}

// WithLifecycleVerify injects the verifier that reconciles the session's
// Unknown journal entries before Finish. Each entry the verifier settles
// returns its result to the durable batch record; the rest stay in the
// uncertainty set.
func WithLifecycleVerify(v types.Verifier) LifecycleOption {
	return func(lc *Lifecycle) { lc.verify = v }
}

// WithLifecycleJournal binds the journal whose Unknown entries the
// verifier reconciles. A lifecycle with a verifier and no journal refuses
// the reconciliation instead of skipping it.
func WithLifecycleJournal(j stores.Journal) LifecycleOption {
	return func(lc *Lifecycle) { lc.journal = j }
}

// WithLifecycleSuspendReason overrides the reason a checkpoint carries;
// the default is types.AwaitingTool.
func WithLifecycleSuspendReason(r types.SuspendReason) LifecycleOption {
	return func(lc *Lifecycle) { lc.reason = r }
}

// suspend persists the checkpoint, releases the lease and only then emits
// Suspended: the order runtime/spec.md fixes. A crash before Suspend leaves
// the run to the reaper; a crash after it leaves the client the EventLog.
// sig, when the step returned a SuspendError, supplies the reason, the
// payload and the wake time the event carries; a Scheduled wake arms the
// waker exactly once, after the run is marked suspended.
func (lc *Lifecycle) suspend(ctx context.Context, rt runtime.Runtime, r runtime.AgentRun, st runtime.State, sig *types.SuspendError) (types.Event, error) {
	reason, payload, wakeAt := lc.reason, any(nil), time.Time{}
	if sig != nil {
		reason, payload, wakeAt = sig.Reason, sig.Payload, sig.WakeAt
	}
	env := checkpointEnvelope{
		Run: types.RunInfo{
			Flow:      lc.flow,
			SessionID: lc.sessionID,
			RunID:     lc.lease.RunID,
			Principal: lc.originator,
			Residency: lc.residency,
		},
		Generation: 1,
		State:      st,
	}
	if reason == types.HumanApproval && (sig == nil || sig.Token == "") {
		aps, aerr := lc.approvalsFor(ctx, r, st)
		if aerr != nil {
			return nil, aerr
		}
		env.Approvals = aps
		if len(aps) > 0 {
			ap := aps[0]
			payload = permission.ApprovalRequest{
				Tool:        lc.specFor(r, ap.Call.Name),
				Call:        ap.Call,
				Fingerprint: ap.Fingerprint,
				Risk:        ap.Risk,
				Reversible:  ap.Reversible,
				Eligible:    ap.Eligible,
				ArgOrigins:  permission.ArgOrigins(ap.Call.Args, ap.Taints),
			}
		}
	}
	redactedState, err := lc.redactState(ctx, st)
	if err != nil {
		return nil, err
	}
	env.State = redactedState
	// The durable snapshot precedes the save: a snapshot or compare-and-set
	// failure produces no checkpoint and no suspension. A run that acquired
	// no workspace publishes nowhere.
	var snap types.WorkspaceSnapshot
	if lc.workspaceLive() {
		s, perr := lc.handles.Publish(ctx, lc.sessionID, lc.sandboxOwner())
		if perr != nil {
			return nil, fmt.Errorf("suspend workspace publish: %w", perr)
		}
		snap = s
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		return nil, err
	}
	var child types.ResumeToken
	if sig != nil {
		child = sig.Token
	}
	token, err := r.Save(ctx, stores.Checkpoint{
		RunID:               lc.lease.RunID,
		SchemaVersion:       stores.CurrentSchemaVersion,
		SessionID:           lc.sessionID,
		Flow:                lc.flow,
		Backend:             rt.Name(),
		Reason:              reason,
		Originator:          lc.originator,
		Workspace:           snap.Ref,
		WorkspaceGeneration: snap.Generation,
		Data:                data,
		Child:               child,
	})
	if err != nil {
		return nil, err
	}
	if lc.runs != nil {
		if sr, ok := lc.runs.(stores.SuspendReasonRecorder); ok {
			if err := sr.SuspendWithReason(ctx, lc.lease, token, reason); err != nil {
				return nil, err
			}
		} else if err := lc.runs.Suspend(ctx, lc.lease, token); err != nil {
			return nil, err
		}
	}
	if reason == types.Scheduled && lc.waker != nil {
		if err := lc.waker.Schedule(ctx, token, wakeAt); err != nil {
			return nil, err
		}
	}
	// The release runs only after the suspension transition and the wake
	// schedule succeeded. A failed close keeps the durable snapshot and the
	// coordinator's cleanup record, which the next sweep retries.
	if lc.workspaceLive() {
		_ = lc.handles.Release(ctx, lc.sessionID, lc.sandboxOwner())
	}
	return types.Suspended{Token: token, Reason: reason, Payload: payload, WakeAt: wakeAt}, nil
}

// reconcileUnknown enumerates the session's Unknown journal entries and
// reconciles each through the verifier: a settled entry's reconciled
// result returns to the durable batch record, an entry the verifier could
// not settle keeps its key in the uncertainty set. Arguments come from
// the entry the journal reserved, never from projected history.
func (lc *Lifecycle) reconcileUnknown(ctx context.Context, st runtime.State) (runtime.State, []types.CallKey, error) {
	if lc.verify == nil {
		return st, lc.callKeys(st), nil
	}
	if lc.journal == nil || lc.sessionID == "" {
		return st, lc.callKeys(st), fmt.Errorf("gohan: verifier wired without a journal to enumerate unknown entries")
	}
	keys := lc.callKeys(st)
	entries, err := lc.journal.Unknown(ctx, lc.sessionID)
	if err != nil {
		return st, keys, fmt.Errorf("enumerate unknown journal entries: %w", err)
	}
	wanted := make(map[string]bool, len(keys))
	for _, k := range keys {
		wanted[k.CallID] = true
	}
	// An errored or absent Verify leaves the entry uncertain with the
	// outcome the effect recorded; only the keys this drive holds are
	// reconciled.
	pending := make([]stores.Entry, 0, len(entries))
	for _, e := range entries {
		if wanted[e.Key] {
			pending = append(pending, e)
		}
	}
	settledKeys, _, err := reconcileEntries(ctx, lc.sessionID, lc.verify, pending, &st)
	if err != nil {
		return st, keys, err
	}
	if len(settledKeys) == 0 {
		return st, keys, nil
	}
	for _, k := range settledKeys {
		wanted[k.CallID] = false
	}
	rest := make([]types.CallKey, 0, len(keys))
	for _, k := range keys {
		if wanted[k.CallID] {
			rest = append(rest, k)
		}
	}
	return st, rest, nil
}

// handoffTerminal performs the single handoff transition: the
// HandoffContext persists, control moves to ControlHandoffRequested, the
// request is audited once, and the run closes with StopHandedOff and no
// resume token. A bound callback owns the whole session-side transition
// and fires exactly once; without one the lifecycle persists context,
// control and audit directly through the optional session ports.
func (lc *Lifecycle) handoffTerminal(ctx context.Context, st runtime.State, hctx types.HandoffContext) error {
	switch {
	case lc.handoff != nil:
		if err := lc.handoff(ctx, hctx); err != nil {
			return err
		}
	case lc.sessions != nil:
		if err := lc.sessions.UpdateSession(ctx, lc.sessionID, stores.SessionPatch{Handoff: &hctx}); err != nil {
			return err
		}
		if cw, ok := lc.sessions.(SessionControlWriter); ok {
			if err := cw.SetControl(ctx, lc.sessionID, stores.ControlHandoffRequested); err != nil {
				return err
			}
		}
		if a, ok := decisionAuditFrom(ctx); ok {
			if err := a.Append(ctx, stores.AuditRecord{
				Kind:      auditHandoffRequest,
				SessionID: lc.sessionID,
				RunID:     lc.lease.RunID,
				Flow:      lc.flow,
				Decision:  RequestHumanName,
			}); err != nil {
				return err
			}
		}
	}
	return lc.finishRun(ctx, st, stores.Finished, nil, types.StopHandedOff)
}
