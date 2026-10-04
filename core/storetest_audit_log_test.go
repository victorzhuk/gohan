package gohan

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// auditAdapter binds the memory AuditLog to the conformance suite's
// port-level interface without letting the suite import core/stores.
type auditAdapter struct {
	log *stores.MemoryAuditLog
}

func (a auditAdapter) Append(ctx context.Context, r storetest.AuditRecord) error {
	return a.log.Append(ctx, stores.AuditRecord{
		Kind:         stores.AuditKind(r.Kind),
		SessionID:    r.SessionID,
		RunID:        r.RunID,
		RootRunID:    r.RootRunID,
		Flow:         r.Flow,
		Subject:      r.Subject,
		Tenant:       r.Tenant,
		Tool:         r.Tool,
		Effect:       r.Effect,
		ArgsChecksum: r.ArgsChecksum,
		Decision:     r.Decision,
		Confidence:   r.Confidence,
		Approver:     r.Approver,
		Verdict:      r.Verdict,
		Outcome:      r.Outcome,
		ResultSHA:    r.ResultSHA,
		ResultBytes:  r.ResultBytes,
		Stage:        r.Stage,
		Model:        r.Model,
		ModelVersion: r.ModelVersion,
		ManifestHash: r.ManifestHash,
	})
}

func (a auditAdapter) Read(ctx context.Context, sessionID string) iter.Seq2[storetest.AuditRecord, error] {
	return func(yield func(storetest.AuditRecord, error) bool) {
		for rec, err := range a.log.Read(ctx, sessionID) {
			if err != nil {
				yield(storetest.AuditRecord{}, err)
				return
			}
			if !yield(auditToStoretest(rec), nil) {
				return
			}
		}
	}
}

func (a auditAdapter) Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error) {
	return a.log.Purge(ctx, tenant, olderThan)
}

func auditToStoretest(r stores.AuditRecord) storetest.AuditRecord {
	return storetest.AuditRecord{
		Kind:         storetest.AuditKind(r.Kind),
		At:           r.At,
		SessionID:    r.SessionID,
		RunID:        r.RunID,
		RootRunID:    r.RootRunID,
		Flow:         r.Flow,
		Subject:      r.Subject,
		Tenant:       r.Tenant,
		Tool:         r.Tool,
		Effect:       r.Effect,
		ArgsChecksum: r.ArgsChecksum,
		Decision:     r.Decision,
		Confidence:   r.Confidence,
		Approver:     r.Approver,
		Verdict:      r.Verdict,
		Outcome:      r.Outcome,
		ResultSHA:    r.ResultSHA,
		ResultBytes:  r.ResultBytes,
		Stage:        r.Stage,
		Model:        r.Model,
		ModelVersion: r.ModelVersion,
		ManifestHash: r.ManifestHash,
		PrevHash:     r.PrevHash,
		Hash:         r.Hash,
	}
}

func TestStoretestAuditLogBind(t *testing.T) {
	storetest.AuditLog(t, func(t *testing.T) storetest.AuditStore {
		t.Helper()
		return auditAdapter{log: stores.NewMemoryAuditLog()}
	})
}
