package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// WithLifecycleConsolidation binds the Done seam that writes the closed
// run's subject memory, and the audit port a failed consolidation records
// through. A lifecycle without one finishes without consolidating.
func WithLifecycleConsolidation(c MemoryConsolidator, audit stores.AuditLog) LifecycleOption {
	return func(lc *Lifecycle) { lc.consolidator, lc.consolidationAudit = c, audit }
}

// WithLifecycleApprovalPolicy binds the source the suspension resolves each
// pending request's approval policy through. A HumanApproval suspension
// without one refuses the approval path.
func WithLifecycleApprovalPolicy(src permission.ApprovalPolicySource) LifecycleOption {
	return func(lc *Lifecycle) { lc.approval = src }
}

// WithLifecycleToolSpecs binds the registered tool-spec lookup a suspension
// resolves each pending call's declaration through. It takes precedence
// over the run's carried tools.
func WithLifecycleToolSpecs(lookup func(name string) (types.ToolSpec, bool)) LifecycleOption {
	return func(lc *Lifecycle) { lc.toolLookup = lookup }
}

// WithLifecycleCacheWrite binds the terminal cache-write seam. A lifecycle
// without one writes nothing.
func WithLifecycleCacheWrite(fn CacheWriteFunc) LifecycleOption {
	return func(lc *Lifecycle) { lc.cacheWrite = fn }
}

// WithLifecycleSandboxHandles binds the sandbox coordinator the run's
// suspension and terminal seams publish and release through. A lifecycle
// without one publishes nowhere.
func WithLifecycleSandboxHandles(h types.SandboxHandles) LifecycleOption {
	return func(lc *Lifecycle) { lc.handles = h }
}

// WithLifecycleWorkspaceUse installs the run's acquisition tracker the
// tool path marks on Acquire. The lifecycle reads it at the suspension
// and terminal seams; a run that acquired nothing publishes nowhere.
func WithLifecycleWorkspaceUse(u *types.WorkspaceUse) LifecycleOption {
	return func(lc *Lifecycle) { lc.use = u }
}

// sandboxOwner reports the lease-fenced owner the run's sandbox calls
// acquire, publish and release under.
func (lc *Lifecycle) sandboxOwner() types.WorkspaceOwner {
	return types.WorkspaceOwner{RunID: lc.lease.RunID, LeaseGeneration: lc.lease.Generation}
}

// workspaceLive reports whether the run acquired a sandbox handle the
// lifecycle must publish and release.
func (lc *Lifecycle) workspaceLive() bool {
	return lc.handles != nil && lc.use != nil && lc.use.Acquired() &&
		lc.lease.RunID != "" && lc.sessionID != ""
}

// specFor resolves one registered tool spec the way approvalsFor does:
// the lifecycle lookup first, then the run's carried tools.
func (lc *Lifecycle) specFor(r runtime.AgentRun, name string) types.ToolSpec {
	if lc.toolLookup != nil {
		if s, ok := lc.toolLookup(name); ok {
			return s
		}
	}
	for _, t := range r.Tools {
		if s := t.Spec(); s.Name == name {
			return s
		}
	}
	return types.ToolSpec{}
}

// approvalsFor builds the persisted approval request for the active ask,
// the head of the pending queue. A missing tool spec or an unresolvable
// policy refuses the suspension instead of persisting a partial approvals
// list.
func (lc *Lifecycle) approvalsFor(ctx context.Context, r runtime.AgentRun, st runtime.State) ([]checkpointApproval, error) {
	b, err := decodeNativeBackend(st)
	if err != nil {
		return nil, err
	}
	var taints []types.ArgTaint
	if b.Driver != nil && b.Driver.Batch != nil {
		taints = b.Driver.Batch.Taints
	}
	specs := make(map[string]types.ToolSpec, len(r.Tools))
	for _, t := range r.Tools {
		specs[t.Spec().Name] = t.Spec()
	}
	lookup := lc.toolLookup
	if lookup == nil {
		lookup = func(name string) (types.ToolSpec, bool) { s, ok := specs[name]; return s, ok }
	}
	aps := make([]checkpointApproval, 0, 1)
	if len(st.Pending) == 0 {
		return nil, nil
	}
	for _, call := range st.Pending[:1] {
		spec, ok := lookup(call.Name)
		if !ok {
			return nil, fmt.Errorf("%w: pending call %q has no registered tool", types.ErrCheckpointIncompatible, call.Name)
		}
		if lc.approval == nil {
			return nil, fmt.Errorf("%w: no approval policy wired", types.ErrApproverNotEligible)
		}
		reversible := spec.Effect != types.SideEffect
		pol, err := lc.approval.ApprovalPolicy(ctx, spec.Risk, call.Name, reversible)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve approval policy: %s", types.ErrApproverNotEligible, err)
		}
		elig := permission.Eligibility{Quorum: pol.Quorum}
		if pol.Scope != "" {
			elig.Scopes = []string{pol.Scope}
		}
		if pol.SeparateFromOriginator {
			elig.ExcludeSubjects = []string{lc.originator.Subject}
		}
		aps = append(aps, checkpointApproval{
			Call:        call,
			Risk:        spec.Risk,
			Fingerprint: ToolFingerprint(spec, json.RawMessage(call.Args)),
			Taints:      taints,
			Reversible:  reversible,
			Eligible:    elig,
		})
	}
	return aps, nil
}

// finishTurn verifies every Uncertain entry, closes the run and returns
// the terminal events ending in Done. A Finish refused with
// ErrSignalsPending drains the mailbox, appends the steers to history and
// reports one more turn; when MaxTurns is already reached the run ends
// with Done{StopLimit} instead, and a pending cancel stops it with
// Done{cancelled}. The returned state carries the history version the
// drained steers advanced to, so the next step appends against it.
func (lc *Lifecycle) finishTurn(ctx context.Context, st runtime.State, terminal types.StopReason) (runtime.State, []types.Event, bool, error) {
	if lc.runs != nil {
		sigs, err := lc.runs.Drain(ctx, lc.lease)
		if err != nil {
			return st, nil, false, err
		}
		st, terminal, again, evs, err := lc.applySignals(ctx, st, sigs)
		if err != nil || terminal || again {
			return st, evs, again, err
		}
	}
	unknown := lc.callKeys(st)
	if len(unknown) > 0 {
		var err error
		st, unknown, err = lc.reconcileUnknown(ctx, st)
		if err != nil {
			return st, nil, false, err
		}
	}
	err := lc.finishRun(ctx, st, stores.Finished, unknown, terminal)
	if errors.Is(err, types.ErrSignalsPending) {
		// A steer arrived between the safe point and Finish: drain it and
		// run one more turn.
		if lc.runs == nil {
			return st, nil, false, fmt.Errorf("gohan: %w without a runs store", types.ErrSignalsPending)
		}
		sigs, derr := lc.runs.Drain(ctx, lc.lease)
		if derr != nil {
			return st, nil, false, derr
		}
		st, terminal, again, evs, aerr := lc.applySignals(ctx, st, sigs)
		if aerr != nil {
			return st, nil, false, aerr
		}
		if !terminal && !again {
			return st, nil, false, fmt.Errorf("gohan: %w reported but the mailbox drained empty", types.ErrSignalsPending)
		}
		return st, evs, again, nil
	}
	if err != nil {
		return st, nil, false, err
	}
	lc.writeCacheEntry(ctx, st, terminal, unknown)
	lc.consolidate(ctx)
	return st, []types.Event{lc.done(st, unknown, terminal)}, false, nil
}

// writeCacheEntry applies the never-cache-failures gate before the
// terminal seam fires: only a clean completion with nothing uncertain and
// a cacheable finish writes an entry. A guard block or a refusal never
// reaches this path with a completed terminal reason.
func (lc *Lifecycle) writeCacheEntry(ctx context.Context, st runtime.State, terminal types.StopReason, uncertain []types.CallKey) {
	if lc.cacheWrite == nil || terminal != types.StopCompleted || len(uncertain) > 0 {
		return
	}
	switch lastFinishFrom(ctx) {
	case types.FinishMaxTokens, types.FinishRefusal:
		return
	}
	lc.cacheWrite(ctx, st)
}

// finishRun closes the run with the given terminal state. The terminal
// order is Publish → the terminal write → Release: the durable snapshot
// precedes the write, and the handle releases only after the write
// succeeded. A Finish refused with ErrSignalsPending keeps the run, the
// live handle and its heartbeat alive, so no release runs. A terminal
// publication failure records Failed without a successful result ref and
// never repeats the failing publication.
func (lc *Lifecycle) finishRun(ctx context.Context, st runtime.State, state stores.RunState, uncertain []types.CallKey, stop types.StopReason) error {
	if lc.runs == nil {
		return nil
	}
	if lc.workspaceLive() {
		if _, err := lc.handles.Publish(ctx, lc.sessionID, lc.sandboxOwner()); err != nil {
			lc.resultRef = ""
			if ferr := lc.finishStore(ctx, stores.Failed, uncertain, "", stop); ferr != nil {
				return fmt.Errorf("terminal workspace publish: %w (failed write: %w)", err, ferr)
			}
			return fmt.Errorf("terminal workspace publish: %w", err)
		}
	}
	err := lc.finishStore(ctx, state, uncertain, lc.resultRef, stop)
	if err != nil {
		return err
	}
	if lc.workspaceLive() {
		_ = lc.handles.Release(ctx, lc.sessionID, lc.sandboxOwner())
	}
	return nil
}

// finishStore performs the terminal store transition.
func (lc *Lifecycle) finishStore(ctx context.Context, state stores.RunState, uncertain []types.CallKey, resultRef string, stop types.StopReason) error {
	if fr, ok := lc.runs.(stores.FinishReasonRecorder); ok {
		return fr.FinishWithReason(ctx, lc.lease, state, uncertain, resultRef, stop)
	}
	return lc.runs.Finish(ctx, lc.lease, state, uncertain, resultRef)
}

// consolidate runs the Done seam after the run closed. A failure is
// logged and, while an audit port can still carry a record, audited; it
// never fails the terminal Done.
func (lc *Lifecycle) consolidate(ctx context.Context) {
	if lc.consolidator == nil {
		return
	}
	ri := types.RunInfo{SessionID: lc.sessionID, RunID: lc.lease.RunID, Principal: lc.originator}
	if err := lc.consolidator.Run(ctx, ri); err != nil {
		consolidateFailure(ctx, lc.consolidationAudit, ri, err)
	}
}

func (lc *Lifecycle) done(st runtime.State, uncertain []types.CallKey, reason types.StopReason) types.Event {
	d := types.Done{Reason: reason, Usage: st.Usage, Uncertain: uncertain}
	if lc.ledger != nil {
		d.Cost = lc.ledger.TreeCost()
		d.Usage.SandboxSeconds = lc.ledger.TreeSandboxSeconds()
	}
	return d
}
