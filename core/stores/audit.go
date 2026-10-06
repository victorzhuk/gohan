package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"
	"strings"
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
	AuditApproval     AuditKind = "approval"
	AuditGrantedScope AuditKind = "granted_by_scope"
	AuditEgressDenied AuditKind = "egress_denied"
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
	Host         string
	Reason       string
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

// auditRow is one persisted audit record: the decoded value the chain
// logic reads and the envelope bytes the public read decodes through.
type auditRow struct {
	rec AuditRecord
	raw []byte
}

// MemoryAuditLog is the in-memory AuditLog reference implementation. The
// store clock is authoritative for the persisted At instant; the per-session
// hash chain and At are computed inside Append and no exported path lets a
// caller set them.
type MemoryAuditLog struct {
	mu       sync.Mutex
	byTenant map[string][]auditRow
	now      func() time.Time
	schemas  *SchemaRegistry
}

// MemoryAuditLogOption configures NewMemoryAuditLog.
type MemoryAuditLogOption func(*MemoryAuditLog)

// WithMemoryAuditClock replaces the store clock. Persisted instants and
// retention are judged against it, so tests drive time through it.
func WithMemoryAuditClock(now func() time.Time) MemoryAuditLogOption {
	return func(s *MemoryAuditLog) { s.now = now }
}

// WithMemoryAuditSchemas injects the schema registry record reads decode
// through.
func WithMemoryAuditSchemas(reg *SchemaRegistry) MemoryAuditLogOption {
	return func(s *MemoryAuditLog) { s.schemas = reg }
}

func NewMemoryAuditLog(opts ...MemoryAuditLogOption) *MemoryAuditLog {
	s := &MemoryAuditLog{now: time.Now, schemas: NewSchemaRegistry()}
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
		s.byTenant = make(map[string][]auditRow)
	}
	rec := r
	rec.At = s.now()
	chain := s.byTenant[rec.Tenant]
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].rec.SessionID == rec.SessionID {
			rec.PrevHash = chain[i].rec.Hash
			break
		}
	}
	rec.Hash = auditHash(rec)
	raw, err := encodeEnvelope("AuditRecord", rec)
	if err != nil {
		return err
	}
	s.byTenant[rec.Tenant] = append(chain, auditRow{rec: rec, raw: raw})
	return nil
}

// SeedRecord installs envelope as the stored bytes of identity, so the next
// public read consumes them as persisted bytes. Seeding bypasses the chain
// seal: fixtures replay historical bytes, not client writes.
func (s *MemoryAuditLog) SeedRecord(_ context.Context, identity string, envelope RecordEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("seed %s: %w", identity, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.auditSlot(identity)
	if err != nil {
		return err
	}
	row.raw = raw
	return nil
}

// StoredRecord returns the stored bytes of identity unchanged.
func (s *MemoryAuditLog) StoredRecord(_ context.Context, identity string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.auditSlot(identity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), row.raw...), nil
}

// auditSlot resolves an "audit/<tenant>/<index>" identity. Tenants must not
// carry a slash.
func (s *MemoryAuditLog) auditSlot(identity string) (*auditRow, error) {
	rest, ok := strings.CutPrefix(identity, "audit/")
	if !ok {
		return nil, fmt.Errorf("record identity %s: not an audit identity", identity)
	}
	tenant, index, _ := strings.Cut(rest, "/")
	i, err := strconv.Atoi(index)
	if err != nil || i < 0 || strings.Contains(tenant, "/") {
		return nil, fmt.Errorf("record identity %s: bad identity", identity)
	}
	rows := s.byTenant[tenant]
	if i >= len(rows) {
		return nil, fmt.Errorf("record identity %s: not found", identity)
	}
	return &rows[i], nil
}

// Read yields the session's records in append order.
func (s *MemoryAuditLog) Read(ctx context.Context, sessionID string) iter.Seq2[AuditRecord, error] {
	return func(yield func(AuditRecord, error) bool) {
		s.mu.Lock()
		chain := s.recordsLocked(sessionID)
		out := make([]AuditRecord, len(chain))
		for i, row := range chain {
			var env RecordEnvelope
			if err := json.Unmarshal(row.raw, &env); err != nil {
				s.mu.Unlock()
				yield(AuditRecord{}, fmt.Errorf("decode AuditRecord envelope: %w", err))
				return
			}
			rec, err := DecodeRecord[AuditRecord](ctx, s.schemas, env)
			if err != nil {
				s.mu.Unlock()
				yield(AuditRecord{}, err)
				return
			}
			out[i] = rec
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
	for _, row := range chain {
		if row.rec.At.Before(olderThan) {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	if removed == 0 {
		return 0, nil
	}
	s.byTenant[tenant] = kept
	if err := s.resealLocked(tenant); err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *MemoryAuditLog) recordsLocked(sessionID string) []auditRow {
	var out []auditRow
	for _, chain := range s.byTenant {
		for _, row := range chain {
			if row.rec.SessionID == sessionID {
				out = append(out, row)
			}
		}
	}
	return out
}

func (s *MemoryAuditLog) resealLocked(tenant string) error {
	chain := s.byTenant[tenant]
	var prev *AuditRecord
	for i := range chain {
		rec := chain[i].rec
		if prev == nil || prev.SessionID != rec.SessionID {
			rec.PrevHash = ""
		} else {
			rec.PrevHash = prev.Hash
		}
		rec.Hash = auditHash(rec)
		raw, err := encodeEnvelope("AuditRecord", rec)
		if err != nil {
			return err
		}
		chain[i] = auditRow{rec: rec, raw: raw}
		prev = &chain[i].rec
	}
	return nil
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
