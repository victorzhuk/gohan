package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// errResumeCheckpointsRequired marks a Resume against a conversation built
// without a checkpoints store: a suspended run has no state to replay.
var errResumeCheckpointsRequired = errors.New("gohan: conversation needs a checkpoints store to resume")

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

// prepareResume validates the resume seam and derives the decision's
// approval record: the approver is the verified transport principal, and a
// context that already carries a RunInfo belongs to a running tool, sub-flow,
// provider or decider, which may never resume anything
// (identity.resume-inside-run-refused). The returned context carries the
// approval so the resumed run reads it through ApprovalFrom.
func prepareResume(ctx context.Context, in stores.ResumeInput) (context.Context, stores.ResumeInput, error) {
	return validateResume(ctx, in)
}

// Resume consumes the single-use token, restores the originator's identity
// and credential, appends the decision to the session history and re-drives
// the checkpointed state (suspension.approve-on-another-pod).
func (c *conversation) Resume(ctx context.Context, t ResumeToken, r stores.ResumeInput) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		ctx, r, err := prepareResume(ctx, r)
		if err != nil {
			yield(nil, err)
			return
		}
		if c.cps == nil {
			yield(nil, errResumeCheckpointsRequired)
			return
		}
		cp, err := c.cps.Consume(ctx, t, r)
		if err != nil {
			yield(nil, err)
			return
		}
		// The run continues as the originator; the approver travels in
		// the approval, never as the run's principal.
		ctx = WithPrincipal(ctx, cp.Originator)
		if c.creds != nil {
			cred, cerr := c.creds.Credentials(ctx, cp.Originator)
			if cerr != nil {
				yield(nil, cerr)
				return
			}
			ctx = WithCredential(ctx, cred)
		}
		var st runtime.State
		if len(cp.Data) > 0 {
			if uerr := json.Unmarshal(cp.Data, &st); uerr != nil {
				yield(nil, fmt.Errorf("gohan: decode checkpoint: %w", uerr))
				return
			}
		}
		if aerr := applyResume(ctx, c.log, cp.SessionID, &st, r); aerr != nil {
			yield(nil, aerr)
			return
		}
		for ev, err := range DriveResume(ctx, c.rt, runtime.AgentRun{}, st, r) {
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
		res := types.ToolResult{
			Outcome: types.Failed,
			Error:   &types.ToolError{Kind: types.Permanent, Message: r.Reason},
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
