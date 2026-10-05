package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errResumeCheckpointsRequired marks a Resume against a conversation built
// without a checkpoints store: a suspended run has no state to replay.
var errResumeCheckpointsRequired = errors.New("gohan: conversation needs a checkpoints store to resume")

// resumeLeaseTTL is the fresh lease a resumed run holds until its first
// heartbeat refresh.
const resumeLeaseTTL = 30 * time.Second

// WithConversationCheckpoints binds the checkpoints store Resume consumes
// single-use tokens through.
func WithConversationCheckpoints(c stores.Checkpoints) ConversationOption {
	return func(cv *conversation) { cv.cps = c }
}

// WithConversationCredentialSource sets the source that re-issues the
// originator's credential before the resumed run executes a tool
// (identity.credentials-on-resume).
func WithConversationCredentialSource(src types.CredentialSource) ConversationOption {
	return func(cv *conversation) { cv.creds = src }
}

// WithConversationApprovalPolicy sets the source Resume resolves the
// approval policy from. Without one, approvals and edits on a suspended
// HumanApproval checkpoint are refused; rejections still carry session
// access alone.
func WithConversationApprovalPolicy(src permission.ApprovalPolicySource) ConversationOption {
	return func(cv *conversation) { cv.policySrc = src }
}

// validateResume runs the seam guards of the resume entry: a verified
// transport principal is required, a context that already carries a RunInfo
// belongs to a running tool, sub-flow, provider or decider and may never
// resume anything, and a caller-supplied approver is replaced by the
// transport principal (identity.approver-from-transport-only).
func validateResume(ctx context.Context, in stores.ResumeInput) (context.Context, stores.ResumeInput, types.Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return ctx, in, types.Principal{}, types.ErrNoPrincipal
	}
	if _, running := RunInfoFrom(ctx); running {
		return ctx, in, types.Principal{}, types.ErrResumeInsideRun
	}
	in.Approver = &p
	return ctx, in, p, nil
}

// authorizeResume loads the session history and checks the transport
// principal against the recorded owner: same tenant plus the owner subject
// or session:write. A read scope or bare token possession is not authority.
func authorizeResume(ctx context.Context, log stores.SessionLog, sessionID string, p types.Principal) (stores.History, error) {
	if log == nil {
		return stores.History{}, fmt.Errorf("resume session %s: ownership is not wired: %w", sessionID, types.ErrSessionForbidden)
	}
	h, err := log.Load(ctx, sessionID)
	if err != nil {
		return stores.History{}, err
	}
	if p.Tenant != h.Owner.Tenant {
		return stores.History{}, fmt.Errorf("resume session %s: tenant %s: %w", sessionID, p.Tenant, types.ErrSessionForbidden)
	}
	if p.Subject != h.Owner.Subject && !slices.Contains(p.Scopes, types.ScopeSessionWrite) {
		return stores.History{}, fmt.Errorf("resume session %s: subject %s: %w", sessionID, p.Subject, types.ErrSessionForbidden)
	}
	return h, nil
}

// validateResumeInput checks the delivered input against the reason the
// checkpoint saved. Empty deliveries are indistinguishable by shape, so the
// saved reason decides.
func validateResumeInput(cp stores.Checkpoint, in stores.ResumeInput) error {
	switch cp.Reason {
	case types.HumanApproval:
		switch in.Verdict {
		case stores.VerdictApprove:
			if len(in.Args) != 0 || len(in.Data) != 0 {
				return fmt.Errorf("%w: approval carries no delivery payload", types.ErrInputInvalid)
			}
		case stores.VerdictReject:
			if len(in.Args) != 0 || len(in.Data) != 0 {
				return fmt.Errorf("%w: rejection carries no delivery payload", types.ErrInputInvalid)
			}
		case stores.VerdictEdit:
			if len(in.Args) == 0 {
				return fmt.Errorf("%w: edit without arguments", types.ErrInputInvalid)
			}
			if len(in.Data) != 0 {
				return fmt.Errorf("%w: edit carries no delivery payload", types.ErrInputInvalid)
			}
		default:
			return fmt.Errorf("%w: unknown approval verdict", types.ErrInputInvalid)
		}
	case types.AwaitingInput, types.AwaitingTool, types.AwaitingExternal, types.AwaitingBatch:
		if in.Verdict != stores.VerdictApprove || len(in.Args) != 0 {
			return fmt.Errorf("%w: delivery for %s carries no verdict or arguments", types.ErrInputInvalid, cp.Reason)
		}
		if len(in.Data) == 0 {
			return fmt.Errorf("%w: %s resume without delivery data", types.ErrInputInvalid, cp.Reason)
		}
	case types.Scheduled, types.AwaitingControl, types.Preempted:
		if in.Verdict != stores.VerdictApprove || len(in.Args) != 0 || len(in.Data) != 0 {
			return fmt.Errorf("%w: %s resume accepts only the empty wake", types.ErrInputInvalid, cp.Reason)
		}
	case types.HumanHandoff:
		return types.ErrNotSuspendable
	default:
		return fmt.Errorf("%w: unknown suspend reason %q", types.ErrCheckpointIncompatible, cp.Reason)
	}
	return nil
}

// checkpointEligibility checks the transport principal against one recorded
// approval request under the current policy: the recorded eligibility must
// still hold, a policy error never grants permission, and separation from
// the originator applies when the policy requires it.
func checkpointEligibility(ap checkpointApproval, pol permission.ApprovalPolicy, approver, originator types.Principal) error {
	if err := permission.CheckEligibility(permission.ApprovalRequest{
		Risk:     ap.Risk,
		Eligible: ap.Eligible,
	}, approver); err != nil {
		return err
	}
	if pol.SeparateFromOriginator && approver.Subject == originator.Subject && approver.Tenant == originator.Tenant {
		return fmt.Errorf("%w: policy separates the approval from the originator", types.ErrApproverNotEligible)
	}
	return nil
}

// approveQuorum reports the quorum one recorded approval is judged by: the
// current policy quorum, falling back to the quorum recorded at request
// creation.
func approveQuorum(ap checkpointApproval, pol permission.ApprovalPolicy) int {
	if pol.Quorum > 0 {
		return pol.Quorum
	}
	if ap.Eligible.Quorum > 0 {
		return ap.Eligible.Quorum
	}
	return 1
}

// approverRecorded reports whether the subject already appears among the
// recorded approvers. Distinct subjects only: a duplicate vote is refused.
func approverRecorded(ap checkpointApproval, p types.Principal) bool {
	for _, got := range ap.ApprovedBy {
		if got.Subject == p.Subject && got.Tenant == p.Tenant {
			return true
		}
	}
	return false
}

// Resume consumes the single-use token, restores the originator's identity
// and credential, appends the decision to the session history and re-drives
// the checkpointed state (suspension.approve-on-another-pod). Every refusal
// before consumption leaves the token pending and applies no effect.
func (c *conversation) Resume(ctx context.Context, t ResumeToken, r stores.ResumeInput) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		ctx, r, approver, err := validateResume(ctx, r)
		if err != nil {
			yield(nil, err)
			return
		}
		if c.cps == nil {
			yield(nil, errResumeCheckpointsRequired)
			return
		}
		if _, ok := c.cps.(stores.CheckpointResumer); !ok {
			yield(nil, fmt.Errorf("resume: checkpoints store cannot inspect tokens: %w", types.ErrCheckpointIncompatible))
			return
		}
		if _, ok := c.cps.(stores.ResumeReadyLister); !ok {
			yield(nil, fmt.Errorf("resume: checkpoints store cannot list ready checkpoints: %w", types.ErrCheckpointIncompatible))
			return
		}
		resumer := c.cps.(stores.CheckpointResumer)
		cp, err := resumer.Peek(ctx, t)
		if err != nil {
			yield(nil, err)
			return
		}
		env, st, err := decodeCheckpoint(cp, c.rt, c.spec)
		if err != nil {
			yield(nil, err)
			return
		}
		h, err := authorizeResume(ctx, c.log, cp.SessionID, approver)
		if err != nil {
			yield(nil, err)
			return
		}
		if err := validateResumeInput(cp, r); err != nil {
			yield(nil, err)
			return
		}
		var pols []permission.ApprovalPolicy
		if cp.Reason == types.HumanApproval && r.Verdict != stores.VerdictReject {
			if c.policySrc == nil {
				yield(nil, fmt.Errorf("resume: no approval policy wired: %w", types.ErrApproverNotEligible))
				return
			}
			if len(env.Approvals) != len(st.Pending) {
				yield(nil, fmt.Errorf("resume: approvals do not cover every pending call: %w", types.ErrCheckpointIncompatible))
				return
			}
			pending := map[string]bool{}
			for _, call := range st.Pending {
				pending[call.ID] = true
			}
			pols = make([]permission.ApprovalPolicy, len(env.Approvals))
			for i, ap := range env.Approvals {
				if !pending[ap.Call.ID] {
					yield(nil, fmt.Errorf("resume: approval for unknown call %q: %w", ap.Call.ID, types.ErrCheckpointIncompatible))
					return
				}
				pol, perr := c.policySrc.ApprovalPolicy(ctx, ap.Risk, ap.Call.Name, ap.Reversible)
				if perr != nil {
					yield(nil, fmt.Errorf("resume: resolve approval policy: %w", types.ErrApproverNotEligible))
					return
				}
				pols[i] = pol
				if err := checkpointEligibility(ap, pol, approver, cp.Originator); err != nil {
					yield(nil, err)
					return
				}
				if approverRecorded(ap, approver) {
					yield(nil, fmt.Errorf("resume: %s already approved this request: %w", approver.Subject, types.ErrApproverNotEligible))
					return
				}
			}
		}

		// The originator's credential is resolved before consumption, so a
		// credential failure applies no effect and keeps the token pending.
		credCtx := WithPrincipal(ctx, cp.Originator)
		if c.creds != nil {
			cred, cerr := c.creds.Credentials(credCtx, cp.Originator)
			if cerr != nil {
				yield(nil, cerr)
				return
			}
			credCtx = WithCredential(credCtx, cred)
		}

		switch {
		case cp.Reason == types.HumanApproval && r.Verdict == stores.VerdictEdit:
			next, nerr := applyEdit(env, cp, r)
			if nerr != nil {
				yield(nil, nerr)
				return
			}
			if uerr := resumer.UpdatePending(ctx, t, cp, next); uerr != nil {
				yield(nil, uerr)
				return
			}
			return
		case cp.Reason == types.HumanApproval && r.Verdict == stores.VerdictApprove && !approvalCompletes(env, pols, approver):
			next := cloneCheckpoint(cp)
			nenv := env
			for i := range nenv.Approvals {
				nenv.Approvals[i].ApprovedBy = append(slices.Clone(nenv.Approvals[i].ApprovedBy), approver)
			}
			data, aerr := encodeCheckpoint(nenv)
			if aerr != nil {
				yield(nil, aerr)
				return
			}
			next.Data = data
			if uerr := resumer.UpdatePending(ctx, t, cp, next); uerr != nil {
				yield(nil, uerr)
				return
			}
			return
		}

		consumed, err := resumer.ConsumeIf(ctx, t, cp, r)
		if err != nil {
			yield(nil, err)
			return
		}
		if cp.Reason == types.HumanApproval && r.Verdict == stores.VerdictApprove {
			for i := range env.Approvals {
				if !approverRecorded(env.Approvals[i], approver) {
					env.Approvals[i].ApprovedBy = append(slices.Clone(env.Approvals[i].ApprovedBy), approver)
				}
			}
		}
		lease, err := c.runs.Resuming(credCtx, consumed.RunID, resumeLeaseTTL)
		if err != nil {
			yield(nil, err)
			return
		}
		opts := []LifecycleOption{
			WithLifecycleRuns(c.runs, lease),
			WithLifecycleSession(cp.SessionID),
			WithLifecycleFlow(c.spec),
			WithLifecycleOriginator(cp.Originator),
			WithLifecycleResumeState(st),
		}
		if c.policySrc != nil {
			opts = append(opts, WithLifecycleApprovalPolicy(c.policySrc))
		}
		if c.toolSpecs != nil {
			opts = append(opts, WithLifecycleToolSpecs(c.toolSpecs))
		}
		if c.log != nil {
			opts = append(opts, WithLifecycleAppender(AppendFunc(func(ctx context.Context, expected int64, msgs ...types.Message) (int64, error) {
				return c.log.Append(ctx, cp.SessionID, expected, msgs...)
			})))
		}
		ag := runtime.AgentRun{}
		if c.cps != nil {
			ag.Save = c.cps.Put
		}
		lc := NewLifecycle(opts...)
		ctx = credCtx
		if r.Verdict == stores.VerdictApprove && cp.Reason == types.HumanApproval {
			if aerr := appendReceipts(ctx, c.log, h, consumed.RunID, env, &st); aerr != nil {
				yield(nil, aerr)
				return
			}
		}
		if aerr := applyResume(ctx, c.log, cp.SessionID, &st, r); aerr != nil {
			yield(nil, aerr)
			return
		}
		ctx = types.WithApproval(ctx, types.Approval{Approver: approver, At: time.Now().UTC()})
		for ev, err := range DriveLifecycle(ctx, lc, c.rt, ag) {
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// applyEdit builds the edited pending request: arguments replaced, the
// argument generation advanced once, every recorded approval cleared. The
// editing principal must submit a separate approval for the new generation.
func applyEdit(env checkpointEnvelope, cp stores.Checkpoint, r stores.ResumeInput) (stores.Checkpoint, error) {
	if env.Generation == ^uint64(0) {
		return stores.Checkpoint{}, fmt.Errorf("resume: argument generation exhausted: %w", types.ErrCheckpointIncompatible)
	}
	env.Generation++
	for i := range env.Approvals {
		env.Approvals[i].Call.Args = json.RawMessage(r.Args)
		env.Approvals[i].ApprovedBy = nil
	}
	for i := range env.State.Pending {
		env.State.Pending[i].Args = json.RawMessage(r.Args)
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		return stores.Checkpoint{}, err
	}
	next := cloneCheckpoint(cp)
	next.Data = data
	return next, nil
}

// approvalCompletes reports whether this approval, added to the recorded
// ones, brings every request to the quorum the current policy sets.
func approvalCompletes(env checkpointEnvelope, pols []permission.ApprovalPolicy, approver types.Principal) bool {
	for i, ap := range env.Approvals {
		var pol permission.ApprovalPolicy
		if i < len(pols) {
			pol = pols[i]
		}
		count := len(ap.ApprovedBy)
		if !approverRecorded(ap, approver) {
			count++
		}
		if count < approveQuorum(ap, pol) {
			return false
		}
	}
	return true
}

// cloneCheckpoint copies a checkpoint snapshot so a pending update never
// aliases store-owned bytes.
func cloneCheckpoint(cp stores.Checkpoint) stores.Checkpoint {
	next := cp
	next.Data = slices.Clone(cp.Data)
	return next
}

// appendReceipts records one trusted receipt per approval-controlled call
// that reached its quorum, keyed by run, generation and call id. Recovery
// must not append a second receipt for the same key.
func appendReceipts(ctx context.Context, log stores.SessionLog, h stores.History, runID string, env checkpointEnvelope, st *runtime.State) error {
	if log == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, msg := range h.Messages {
		if raw, ok := msg.Meta[ApprovalReceiptKey]; ok {
			rc, err := decodeReceipt(raw)
			if err != nil {
				return err
			}
			seen[receiptDedupKey(rc)] = true
		}
	}
	var msgs []types.Message
	for _, ap := range env.Approvals {
		if len(ap.ApprovedBy) == 0 {
			continue
		}
		rc := approvalReceipt{
			RunID:      runID,
			Generation: env.Generation,
			CallID:     ap.Call.ID,
			Tool:       ap.Call.Name,
			Args:       slices.Clone(ap.Call.Args),
			Approvers:  slices.Clone(ap.ApprovedBy),
		}
		if seen[receiptDedupKey(rc)] {
			continue
		}
		msg, err := approvalReceiptMessage(rc)
		if err != nil {
			return err
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) == 0 {
		return nil
	}
	ver, err := log.Append(ctx, env.Run.SessionID, h.Version, msgs...)
	if err != nil {
		return err
	}
	st.HistoryVersion = ver
	return nil
}

// applyResume replays the decision into the state a checkpoint carries: a
// rejection becomes the pending calls' permanent error result, a delivery
// becomes their tool result, edited arguments replace the pending calls'
// arguments. An approval appends nothing — the approved call executes on
// replay under the approval already in ctx, and every other reserved call
// stays unexecuted (the batch protocol's reservation seam).
func applyResume(ctx context.Context, log stores.SessionLog, sessionID string, st *runtime.State, r stores.ResumeInput) error {
	if log == nil {
		return nil
	}
	switch {
	case r.Verdict == stores.VerdictReject:
		subject := ""
		if r.Approver != nil {
			subject = r.Approver.Subject
		}
		res := types.ToolResult{
			Outcome: types.Failed,
			Error: &types.ToolError{
				Kind:    types.Permanent,
				Message: runtime.NotExecutedPrefix + "rejected by " + subject,
			},
		}
		return appendResumeResults(ctx, log, sessionID, st, res)
	case r.Verdict == stores.VerdictEdit && len(r.Args) > 0:
		for i, use := range st.Pending {
			use.Args = json.RawMessage(r.Args)
			st.Pending[i] = use
		}
		return nil
	case len(r.Data) > 0:
		res := types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: string(r.Data)}},
		}
		return appendResumeResults(ctx, log, sessionID, st, res)
	}
	return nil
}

// appendResumeResults appends one result per pending call in call order and
// advances the state's history version.
func appendResumeResults(ctx context.Context, log stores.SessionLog, sessionID string, st *runtime.State, res types.ToolResult) error {
	if len(st.Pending) == 0 {
		return nil
	}
	msg := types.Message{Role: types.RoleAssistant}
	for _, use := range st.Pending {
		res.ID = use.ID
		msg.Blocks = append(msg.Blocks, res)
	}
	ver, err := log.Append(ctx, sessionID, st.HistoryVersion, msg)
	if err != nil {
		return err
	}
	st.HistoryVersion = ver
	return nil
}

// Resume refuses on a plain function flow: a function cannot suspend, so
// there is nothing to resume (flow.not-suspendable). The conversation path
// owns the resume entry point.
func (f *flowFunc[In, Out]) Resume(context.Context, types.ResumeToken, stores.ResumeInput) (Out, error) {
	var zero Out
	return zero, types.ErrNotSuspendable
}
