package stores

import (
	"context"
	"fmt"
	"sort"
)

// Audit kinds for hold transitions. The stores spec fixes the names as
// hold_set and hold_cleared.
const (
	AuditHoldSet     AuditKind = "hold_set"
	AuditHoldCleared AuditKind = "hold_cleared"
)

// WithSessionAudit attaches the trail that receives hold_set and
// hold_cleared records for every hold transition.
func WithSessionAudit(a AuditLog) MemorySessionOption {
	return func(s *MemorySessionLog) { s.audit = a }
}

// auditHold records a hold transition on the attached trail. from is the
// hold id before the patch; an empty to value means the hold was cleared.
// A nil trail or an unchanged id records nothing.
func (s *MemorySessionLog) auditHold(ctx context.Context, meta SessionMeta, from string) error {
	if s.audit == nil || meta.Hold == from {
		return nil
	}
	kind := AuditHoldSet
	if meta.Hold == "" {
		kind = AuditHoldCleared
	}
	rec := AuditRecord{
		Kind:      kind,
		At:        s.now(),
		SessionID: meta.ID,
		Subject:   meta.Owner.Subject,
		Tenant:    meta.Owner.Tenant,
	}
	if err := s.audit.Append(ctx, rec); err != nil {
		return fmt.Errorf("audit %s for session %s: %w", kind, meta.ID, err)
	}
	return nil
}

// HeldSessions lists the ids of all sessions under a hold, sorted. Maintain
// counts them in MaintainReport.Held; EraseSubject lists them in
// EraseReport.Held.
func (s *MemorySessionLog) HeldSessions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, rec := range s.sessions {
		if rec.meta.Hold != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
