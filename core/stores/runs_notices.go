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

type noticeEntry struct {
	notice types.RunNotice
	claim  time.Time
}

func (e *noticeEntry) pendingAt(now time.Time) bool {
	return e.claim.IsZero() || !now.Before(e.claim.Add(noticeClaimTTL))
}

// noticeFor maps a terminal or waiting run state to the notice kind and
// its reason word.
func noticeFor(state RunState) (types.NoticeKind, string) {
	switch state {
	case Failed:
		return types.NoticeFailed, string(types.StopFailed)
	case Suspended:
		return types.NoticeSuspended, string(types.StopSuspended)
	default:
		return types.NoticeFinished, string(types.StopCompleted)
	}
}

// appendNoticeLocked records the outbox entry under the caller's lock, so
// the notice commits with the state change it belongs to or not at all.
func (s *MemoryRuns) appendNoticeLocked(ctx context.Context, rec *runRecord, state RunState) {
	kind, reason := noticeFor(state)
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
