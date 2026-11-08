package stores

import (
	"context"
	"fmt"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// A claimed notice stays invisible to other dispatchers for LeaseTTL, the
// same bound a run lease uses; after it the claim lapses and the notice
// returns to pending with its id unchanged.
const noticeClaimTTL = LeaseTTL

// SuspendReasonRecorder is the optional Runs extension that carries the
// suspension reason into the notice outbox. A store without it suspends
// without a reason word.
type SuspendReasonRecorder interface {
	SuspendWithReason(ctx context.Context, l Lease, t types.ResumeToken, reason types.SuspendReason) error
}

// FinishReasonRecorder is the optional Runs extension that carries the stop
// reason into the notice outbox, so a cancelled run writes a cancelled
// notice instead of a finished one.
type FinishReasonRecorder interface {
	FinishWithReason(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string, reason types.StopReason) error
}

type noticeEntry struct {
	notice types.RunNotice
	claim  time.Time
}

func (e *noticeEntry) pendingAt(now time.Time) bool {
	return e.claim.IsZero() || !now.Before(e.claim.Add(noticeClaimTTL))
}

// suspendReasonNames maps a SuspendReason value to its constant name, the
// reason word a suspended notice carries.
var suspendReasonNames = map[types.SuspendReason]string{
	types.HumanApproval:    "HumanApproval",
	types.AwaitingExternal: "AwaitingExternal",
	types.AwaitingBatch:    "AwaitingBatch",
	types.AwaitingTool:     "AwaitingTool",
	types.AwaitingControl:  "AwaitingControl",
	types.AwaitingInput:    "AwaitingInput",
	types.Scheduled:        "Scheduled",
	types.Preempted:        "Preempted",
	types.HumanHandoff:     "HumanHandoff",
}

// noticeFor maps a terminal or waiting run state to the notice kind and
// its reason word.
func noticeFor(state RunState, stop types.StopReason, suspend types.SuspendReason) (types.NoticeKind, string) {
	switch state {
	case Failed:
		return types.NoticeFailed, string(types.StopFailed)
	case Suspended:
		if name, ok := suspendReasonNames[suspend]; ok {
			return types.NoticeSuspended, name
		}
		return types.NoticeSuspended, string(types.StopSuspended)
	default:
		if stop == types.StopCancelled {
			return types.NoticeCancelled, string(types.StopCancelled)
		}
		if stop != "" {
			return types.NoticeFinished, string(stop)
		}
		return types.NoticeFinished, string(types.StopCompleted)
	}
}

// appendNoticeLocked records the outbox entry under the caller's lock, so
// the notice commits with the state change it belongs to or not at all.
func (s *MemoryRuns) appendNoticeLocked(ctx context.Context, rec *runRecord, state RunState, stop types.StopReason, suspend types.SuspendReason) {
	kind, reason := noticeFor(state, stop, suspend)
	s.noticeSeq++
	s.notices = append(s.notices, &noticeEntry{notice: types.RunNotice{
		ID:        fmt.Sprintf("nt-%d", s.noticeSeq),
		Kind:      kind,
		Tenant:    s.tenant(ctx),
		SessionID: rec.run.SessionID,
		RunID:     rec.run.RunID,
		Reason:    reason,
		At:        s.now(),
	}})
}

// finish is Finish with the stop reason the notice outbox records.
func (s *MemoryRuns) finish(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string, stop types.StopReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return err
	}
	for _, sig := range rec.signals {
		if sig.Kind == SignalSteer {
			return fmt.Errorf("%w: run %s", types.ErrSignalsPending, rec.run.RunID)
		}
	}
	rec.run.State = state
	rec.run.Uncertain = append([]types.CallKey(nil), uncertain...)
	rec.run.ResultRef = resultRef
	rec.lease = Lease{}
	rec.live = false
	if err := s.encodeRunLocked(rec); err != nil {
		return err
	}
	s.appendNoticeLocked(ctx, rec, state, stop, "")
	return nil
}

func (s *MemoryRuns) Finish(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string) error {
	return s.finish(ctx, l, state, uncertain, resultRef, "")
}

// FinishWithReason records the stop reason with the terminal notice.
func (s *MemoryRuns) FinishWithReason(ctx context.Context, l Lease, state RunState, uncertain []types.CallKey, resultRef string, reason types.StopReason) error {
	return s.finish(ctx, l, state, uncertain, resultRef, reason)
}

// Suspend releases the lease and records the resume token; a suspended run
// is not recoverable until Resuming takes a fresh lease.
func (s *MemoryRuns) Suspend(ctx context.Context, l Lease, t types.ResumeToken) error {
	return s.suspend(ctx, l, t, "")
}

// SuspendWithReason records the suspension reason with the notice.
func (s *MemoryRuns) SuspendWithReason(ctx context.Context, l Lease, t types.ResumeToken, reason types.SuspendReason) error {
	return s.suspend(ctx, l, t, reason)
}

func (s *MemoryRuns) suspend(ctx context.Context, l Lease, t types.ResumeToken, reason types.SuspendReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.heldLocked(l)
	if err != nil {
		return err
	}
	rec.run.State = Suspended
	rec.token = t
	rec.reason = reason
	rec.lease = Lease{}
	rec.live = false
	if err := s.encodeRunLocked(rec); err != nil {
		return err
	}
	s.appendNoticeLocked(ctx, rec, Suspended, "", reason)
	return nil
}

// Notices claims up to limit pending notices for the calling dispatcher
// for noticeClaimTTL. A notice another dispatcher still holds is skipped;
// once its claim lapses it is offered again carrying the same id, which is
// the consumer's idempotency key.
func (s *MemoryRuns) Notices(ctx context.Context, limit int) ([]types.RunNotice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var claimed []types.RunNotice
	for _, e := range s.notices {
		if len(claimed) == limit {
			break
		}
		if !e.pendingAt(now) {
			continue
		}
		e.claim = now
		claimed = append(claimed, e.notice)
	}
	return claimed, nil
}

// AckNotice removes one claimed notice after delivery. The claim ending
// without an ack is what returns the notice to pending.
func (s *MemoryRuns) AckNotice(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.notices {
		if e.notice.ID != id {
			continue
		}
		s.notices = append(s.notices[:i], s.notices[i+1:]...)
		return nil
	}
	return fmt.Errorf("%w: notice %s", ErrRunNotFound, id)
}
