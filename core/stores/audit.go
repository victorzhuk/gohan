package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"iter"
	"strconv"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// AuditKind classifies why a record entered the audit trail.
type AuditKind string

const (
	AuditRunStarted   AuditKind = "run.started"
	AuditRunFinished  AuditKind = "run.finished"
	AuditModelCall    AuditKind = "model.call"
	AuditToolDecision AuditKind = "tool.decision"
	AuditToolOutcome  AuditKind = "tool.outcome"
	AuditGuard        AuditKind = "guard"
	AuditSuspended    AuditKind = "suspended"
	AuditResumed      AuditKind = "resumed"
	AuditLimit        AuditKind = "limit"
	AuditSessionFork  AuditKind = "session.fork"
)

// AuditRecord is one entry of the decision trail. It carries checksums and
// sizes of args and results, never their content.
type AuditRecord struct {
	Kind         AuditKind
	At           time.Time
	SessionID    string
	RunID        string
	RootRunID    string
	Flow         string
	Subject      string
	Tenant       string
	Tool         string
	Effect       types.Effect
	ArgsChecksum string
	Decision     string
	Confidence   float64
	Approver     string
	Verdict      string
	Outcome      types.Outcome
	ResultSHA    string
	ResultBytes  int
	Stage        types.GuardStage
	Model        string
	ModelVersion string
	ManifestHash string
	PrevHash     string
	Hash         string
}

// AuditLog is the append-only decision trail. Only chain steps and the
// harness hold it; retention is per tenant and Purge is the only delete.
type AuditLog interface {
	Append(ctx context.Context, r AuditRecord) error
	Read(ctx context.Context, sessionID string) iter.Seq2[AuditRecord, error]
	Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error)
}

// MemoryAuditLog is the in-memory AuditLog reference implementation. The
// store clock is authoritative for the persisted At instant; the per-session
// hash chain and At are computed inside Append and no exported path lets a
// caller set them.
type MemoryAuditLog struct {
	mu       sync.Mutex
	byTenant map[string][]*AuditRecord
	now      func() time.Time
}

// MemoryAuditLogOption configures NewMemoryAuditLog.
type MemoryAuditLogOption func(*MemoryAuditLog)

// WithMemoryAuditClock replaces the store clock. Persisted instants and
// retention are judged against it, so tests drive time through it.
func WithMemoryAuditClock(now func() time.Time) MemoryAuditLogOption {
	return func(s *MemoryAuditLog) { s.now = now }
}

func NewMemoryAuditLog(opts ...MemoryAuditLogOption) *MemoryAuditLog {
	s := &MemoryAuditLog{now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Append records r at the store clock's instant. The record is copied and
// the per-session chain fields are computed here: PrevHash is the previous
// record's Hash for the session and Hash seals the record without Hash.
func (s *MemoryAuditLog) Append(ctx context.Context, r AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("audit append: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.byTenant == nil {
		s.byTenant = make(map[string][]*AuditRecord)
	}
	rec := r
	rec.At = s.now()
	chain := s.byTenant[rec.Tenant]
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].SessionID == rec.SessionID {
			rec.PrevHash = chain[i].Hash
			break
		}
	}
	rec.Hash = auditHash(rec)
	s.byTenant[rec.Tenant] = append(chain, &rec)
	return nil
}

// Read yields the session's records in append order.
func (s *MemoryAuditLog) Read(ctx context.Context, sessionID string) iter.Seq2[AuditRecord, error] {
	return func(yield func(AuditRecord, error) bool) {
		s.mu.Lock()
		chain := s.recordsLocked(sessionID)
		out := make([]AuditRecord, len(chain))
		for i, rec := range chain {
			out[i] = *rec
		}
		s.mu.Unlock()

		for _, rec := range out {
			if !yield(rec, nil) {
				return
			}
		}
	}
}

// Purge deletes the tenant's records older than the given instant, the only
// delete on the log, and reseals the surviving chain.
func (s *MemoryAuditLog) Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chain := s.byTenant[tenant]
	kept := chain[:0]
	removed := 0
	for _, rec := range chain {
		if rec.At.Before(olderThan) {
			removed++
			continue
		}
		kept = append(kept, rec)
	}
	if removed == 0 {
		return 0, nil
	}
	s.byTenant[tenant] = kept
	s.resealLocked(tenant)
	return removed, nil
}

func (s *MemoryAuditLog) recordsLocked(sessionID string) []*AuditRecord {
	var out []*AuditRecord
	for _, chain := range s.byTenant {
		for _, rec := range chain {
			if rec.SessionID == sessionID {
				out = append(out, rec)
			}
		}
	}
	return out
}

func (s *MemoryAuditLog) resealLocked(tenant string) {
	chain := s.byTenant[tenant]
	var prev *AuditRecord
	for _, rec := range chain {
		if prev == nil || prev.SessionID != rec.SessionID {
			rec.PrevHash = ""
		} else {
			rec.PrevHash = prev.Hash
		}
		rec.Hash = auditHash(*rec)
		prev = rec
	}
}

// auditHash seals the record: SHA-256 over the previous hash and the
// record's fields in a fixed order, excluding Hash itself.
func auditHash(r AuditRecord) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%d\n%s\n%s\n%s\n%s\n%s\n%d\n%s\n%d\n%d\n%s\n%s\n%s\n%s\n",
		r.Kind, r.At.UTC().Format(time.RFC3339Nano), r.SessionID, r.RunID, r.RootRunID,
		r.Flow, r.Subject, r.Tenant, r.Tool, int(r.Effect), r.ArgsChecksum, r.Decision,
		r.Approver, strconv.FormatFloat(r.Confidence, 'b', -1, 64), r.Verdict, int(r.Outcome), r.ResultSHA,
		r.ResultBytes, int(r.Stage), r.Model, r.ModelVersion, r.ManifestHash, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}
