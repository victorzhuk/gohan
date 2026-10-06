package gohan

import (
	"context"
	"log/slog"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// AuditMemoryConsolidate is the audit kind the Done consolidation records.
const AuditMemoryConsolidate stores.AuditKind = "memory_consolidate"

// MemoryConsolidator is the Done seam that writes a closed run's
// subject memory from the session log. The embedder supplies the adapter;
// the lifecycle orders the call and owns the failure policy.
type MemoryConsolidator interface {
	Run(ctx context.Context, ri types.RunInfo) error
}

// ConsolidationWiring is what a built stack hands the consolidator
// factory: the flow's resolved profile and declared memory policy, and
// the store ports the consolidation reads and writes.
type ConsolidationWiring struct {
	Stack    *Stack
	Profile  string
	Store    stores.NotesStore
	Sessions stores.SessionLog
	Audit    stores.AuditLog
	Policy   stores.MemoryPolicy
}

// WithConsolidator binds the factory a conversation builds its Done
// consolidation through, for flows that declare a memory policy. A stack
// without one never consolidates.
func WithConsolidator(f func(ConsolidationWiring) MemoryConsolidator) Option {
	return func(c *config) error {
		c.consolidator = f
		return nil
	}
}

// consolidation builds the flow's consolidator from the resolved native
// configuration, or nil when the flow declares no memory policy, the
// stack has no factory, or the factory declines.
func (s *Stack) consolidation(flow string) MemoryConsolidator {
	if s.consolidator == nil {
		return nil
	}
	cfg, ok := s.resolvedNative(flow)
	if !ok || (cfg.memoryPolicy.Schema == nil && cfg.memoryPolicy.MaxEntries == 0 && cfg.memoryPolicy.TTL == 0) {
		return nil
	}
	if s.stores.Notes == nil || s.stores.SessionLog == nil {
		return nil
	}
	return s.consolidator(ConsolidationWiring{
		Stack:    s,
		Profile:  cfg.profile.Name,
		Store:    s.stores.Notes,
		Sessions: s.stores.SessionLog,
		Audit:    s.stores.Audit,
		Policy:   cfg.memoryPolicy,
	})
}

// consolidate records a failed Done consolidation: logged, and audited
// while an audit port can still carry the record. It never fails the run.
func consolidateFailure(ctx context.Context, audit stores.AuditLog, ri types.RunInfo, err error) {
	slog.Warn("memory consolidation failed", "session", ri.SessionID, "run", ri.RunID, "err", err)
	if audit == nil {
		return
	}
	rec := stores.AuditRecord{
		Kind:      AuditMemoryConsolidate,
		SessionID: ri.SessionID,
		RunID:     ri.RunID,
		Tenant:    ri.Principal.Tenant,
		Subject:   ri.Principal.Subject,
	}
	if aerr := audit.Append(ctx, rec); aerr != nil {
		slog.Warn("memory consolidation audit failed", "session", ri.SessionID, "run", ri.RunID, "err", aerr)
	}
}
