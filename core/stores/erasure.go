package stores

import (
	"context"
	"fmt"
	"sort"

	"github.com/victorzhuk/gohan/core/types"
)

// ErasedSessions reports what a session-log erasure removed and what a
// legal hold kept.
type ErasedSessions struct {
	Deleted []string
	Held    []string
}

// SessionEraser is the subject-erasure surface on a SessionLog. It removes
// every session owned by the subject except the ones under a hold, which it
// reports untouched.
type SessionEraser interface {
	EraseSessions(ctx context.Context, tenant, subject string) (ErasedSessions, error)
}

// RunEraser removes a session's runs from the run store and reports their
// ids, so the event log and checkpoints can follow without re-scanning.
type RunEraser interface {
	EraseSessions(ctx context.Context, sessionIDs []string) ([]string, error)
}

// EventEraser removes every event of the named runs.
type EventEraser interface {
	EraseRuns(ctx context.Context, runIDs []string) (int, error)
}

// CheckpointEraser removes every checkpoint left for the named sessions and
// returns what it removed, so blob refs can be reported without a scan.
type CheckpointEraser interface {
	EraseSessions(ctx context.Context, sessionIDs []string) ([]Checkpoint, error)
}

// RedactionMapEraser removes one session's map.
type RedactionMapEraser interface {
	Delete(ctx context.Context, tenant, session string) error
}

// OutputEraser drops a subject's claims on the blob store and reports how
// many refs it removed.
type OutputEraser interface {
	EraseSubject(ctx context.Context, tenant, subject string) (int, error)
}

// MemoryEraser removes a subject's memory entries: the subject-scope
// records and the session-scope records of the erased sessions.
type MemoryEraser interface {
	EraseSubject(ctx context.Context, tenant, subject string, sessions []string) ([]string, error)
}

// EraseSessions implements RunEraser on the memory runs store: it drops the
// sessions' run rows and the operation index entries pointing at them.
func (s *MemoryRuns) EraseSessions(_ context.Context, sessionIDs []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var runIDs []string
	for _, sessionID := range sessionIDs {
		if runID, ok := s.onSess[sessionID]; ok {
			runIDs = append(runIDs, runID)
		}
	}
	sort.Strings(runIDs)
	for _, runID := range runIDs {
		if _, ok := s.runs[runID]; !ok {
			continue
		}
		delete(s.runs, runID)
		for op, id := range s.byOp {
			if id == runID {
				delete(s.byOp, op)
			}
		}
	}
	return runIDs, nil
}

// EraseRuns implements EventEraser on the memory event log.
func (s *MemoryEventLog) EraseRuns(_ context.Context, runIDs []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, runID := range runIDs {
		if ring, ok := s.rings[runID]; ok {
			n += ring.count
			ring.head, ring.count = 0, 0
			ring.slots = nil
			delete(s.rings, runID)
		}
		delete(s.next, runID)
	}
	return n, nil
}

// EraseSessions implements CheckpointEraser on the memory checkpoint store.
func (s *MemoryCheckpoints) EraseSessions(_ context.Context, sessionIDs []string) ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		ids[id] = true
	}
	var erased []Checkpoint
	for tok, rec := range s.toks {
		if !ids[rec.cp.SessionID] {
			continue
		}
		erased = append(erased, cloneCheckpoint(rec.cp))
		delete(s.toks, tok)
	}
	for runID, tok := range s.byRun {
		if _, ok := s.toks[tok]; !ok {
			delete(s.byRun, runID)
		}
	}
	sort.Slice(erased, func(i, j int) bool { return erased[i].RunID < erased[j].RunID })
	return erased, nil
}

// DeleteSessionDependents drops the session-scope notes and memory of one
// session, so registering the notes store as a session dependent carries
// them through the delete cascade. The subject-scope records need the
// subject erasure surface instead.
func (s *MemoryNotes) DeleteSessionDependents(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.byKey {
		if key.Scope == ScopeSession && key.Session == sessionID {
			delete(s.byKey, key)
		}
	}
	for key := range s.memory {
		if key.Scope == ScopeSession && key.Session == sessionID {
			delete(s.memory, key)
		}
	}
	return nil
}

// CopySessionDependents copies nothing: session notes are not inherited by
// a fork.
func (s *MemoryNotes) CopySessionDependents(_ context.Context, _, _ string) error {
	return nil
}

// EraseSessions implements SessionEraser on the memory session log.
func (s *MemorySessionLog) EraseSessions(ctx context.Context, tenant, subject string) (ErasedSessions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ErasedSessions{}
	for id, rec := range s.sessions {
		if rec.meta.Owner != (types.SessionOwner{Tenant: tenant, Subject: subject}) {
			continue
		}
		if rec.meta.Hold != "" {
			out.Held = append(out.Held, id)
			continue
		}
		if s.leases != nil && s.leases.SessionLeaseActive(context.Background(), id) {
			return ErasedSessions{}, fmt.Errorf("erase session %s: %w", id, types.ErrRunActive)
		}
		out.Deleted = append(out.Deleted, id)
	}
	sort.Strings(out.Deleted)
	sort.Strings(out.Held)
	for _, id := range out.Deleted {
		s.removeLocked(context.Background(), id)
		if err := s.appendErasure(ctx, id, tenant, subject, "erased"); err != nil {
			return ErasedSessions{}, err
		}
	}
	for _, id := range out.Held {
		if err := s.appendErasure(ctx, id, tenant, subject, "held"); err != nil {
			return ErasedSessions{}, err
		}
	}
	return out, nil
}

// Delete removes one tenant/session map; a missing map is already erased.
func (s *MemoryRedactionMaps) Delete(_ context.Context, tenant, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.maps, redactionMapKey{tenant: tenant, session: session})
	return nil
}

// EraseSubject implements MemoryEraser on the memory notes store: it drops
// the subject-scope entries and the erased sessions' session-scope entries
// and reports their ids.
func (s *MemoryNotes) EraseSubject(_ context.Context, tenant, subject string, sessions []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bySession := make(map[string]bool, len(sessions))
	for _, id := range sessions {
		bySession[id] = true
	}
	var ids []string
	for key, recs := range s.memory {
		if key.Tenant != tenant {
			continue
		}
		subjectHit := key.Scope == ScopeSubject && key.Subject == subject
		sessionHit := key.Scope == ScopeSession && bySession[key.Session]
		if !subjectHit && !sessionHit {
			continue
		}
		for _, rec := range recs {
			ids = append(ids, rec.entry.ID)
		}
		delete(s.memory, key)
	}
	sort.Strings(ids)
	return ids, nil
}
